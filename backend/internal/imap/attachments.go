package imap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"regexp"
	"strings"

	imapv2 "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	gomessage "github.com/emersion/go-message"
	"github.com/emersion/go-message/charset"
	"github.com/nohe-sohbi/mailsorter/backend/internal/attachment"
)

// The files attached to a message.
//
// Over the Gmail API, Google walks the MIME tree itself and hands each file
// over decoded, behind an id of its own. Over IMAP the files are inside the
// message as its server keeps it: leaves of a multipart, encoded for transport
// (base64, quoted-printable), their names sometimes encoded twice over (an
// RFC 2047 word inside the parameter, or an RFC 2231 parameter). The server
// describes the parts (parts.go); which of them are files, and under which
// name, is internal/attachment's rule, the one the Gmail API transport
// applies too, read off each part's own MIME header.
//
// A file is named by its IMAP part number ("2", "1.3"), the address IMAP gives
// a part of a message. It is what the reader hands out and what the download
// sends back, and the download serves a number only when the reader's list
// of that same message has a file under it. A number that names the text of
// the mail ("1"), or a section IMAP knows by name ("TEXT"), is not a file and
// gets nothing: the route would otherwise read any part of any message the
// caller can name.

// ErrNoSuchAttachment means the message has no file under that part number.
// Typed because it is an answer to give the caller ("not in this mail"), not a
// failure of the server.
var ErrNoSuchAttachment = errors.New("imap: no such attachment in this message")

// ErrAttachmentTooLarge means the file is larger, decoded, than the caller
// agreed to serve. Typed for the same reason: no retry will make it fit.
var ErrAttachmentTooLarge = errors.New("imap: attachment larger than the limit")

// Attachment is one file attached to a message.
type Attachment struct {
	// Part is where the file sits in the message, numbered the way IMAP
	// numbers parts. It is the file's id in the reader and in the download.
	Part string
	// Filename is the name the reader lists it under, cleaned by
	// attachment.SafeName.
	Filename string
	// MimeType is the media type the sender declared, without parameters.
	MimeType string
	// Size is the size of the file itself, decoded: what Gmail reports and
	// what the person saves. The part as stored is a third larger in base64.
	// It is the server's measure (BINARY.SIZE); a server without BINARY
	// gives none, and it is then estimated from the stored size.
	Size int64
}

// Attachment returns the file attached under part to the message uid in
// folder, decoded, with what the reader lists about it. A part the message
// has no file under, whatever else it names there, is ErrNoSuchAttachment; a
// file larger than limit once decoded is ErrAttachmentTooLarge; a message no
// longer in folder is ErrMessageGone.
//
// The message is read the way the reader reads it (readParts), so the part
// number is served only when the reader lists a file under it. A file the
// server measures over limit is refused before a byte of it is read. Then the
// file alone is fetched, as it arrives rather than into memory, through the
// limit: no more than limit+1 bytes of it are ever held.
//
// The fetches PEEK, like Fetch: saving a file someone sent is not reading
// their mail, and the unread count must not change because of it. When ctx
// ends, the connection is closed (within).
func (c *Client) Attachment(ctx context.Context, folder, uid, part string, limit int64) (Attachment, []byte, error) {
	var att Attachment
	var data []byte
	err := c.within(ctx, fmt.Sprintf("attachment %s of uid %s in %q", part, uid, folder), func() error {
		var err error
		att, data, err = c.attachment(folder, uid, part, limit)
		return err
	})
	if err != nil {
		return Attachment{}, nil, err
	}
	return att, data, nil
}

func (c *Client) attachment(folder, uid, part string, limit int64) (Attachment, []byte, error) {
	described, err := c.fetchOne(folder, uid, &imapv2.FetchOptions{
		UID:           true,
		BodyStructure: &imapv2.FetchItemBodyStructure{Extended: true},
	})
	if err != nil {
		return Attachment{}, nil, err
	}
	inside, err := c.readParts(folder, uid, described.BodyStructure, false)
	if err != nil {
		return Attachment{}, nil, err
	}
	file, l, ok := inside.file(part)
	if !ok {
		return Attachment{}, nil, ErrNoSuchAttachment
	}
	if cannotFit(l, limit) {
		return Attachment{}, nil, ErrAttachmentTooLarge
	}

	var data []byte
	err = c.streamSection(folder, uid, contentSection(l, nil), func(r io.Reader) error {
		var err error
		data, err = readFile(l.header, r, limit)
		return err
	})
	if err != nil {
		return Attachment{}, nil, err
	}
	file.Size = int64(len(data))
	return file, data, nil
}

// readFile is a file read from r, its stored bytes, with its transfer
// encoding undone, through limit: one larger than limit once decoded is
// ErrAttachmentTooLarge, and no more than limit+1 bytes of it are held.
func readFile(header gomessage.Header, r io.Reader, limit int64) ([]byte, error) {
	decoded, err := transferDecoded(header, r)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(decoded, limit+1))
	if err != nil {
		return nil, fmt.Errorf("imap: decode: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, ErrAttachmentTooLarge
	}
	return data, nil
}

// streamSection fetches one section of the message uid in folder, which
// must peek, and hands its bytes to read as they come off the connection.
// Whatever read leaves unread is drained and dropped, never held. A folder
// that no longer holds the message is ErrMessageGone.
func (c *Client) streamSection(folder, uid string, section *imapv2.FetchItemBodySection, read func(io.Reader) error) error {
	num, err := parseUID(uid)
	if err != nil {
		return err
	}
	if err := c.selectFolder(folder); err != nil {
		return err
	}
	cmd := c.c.Fetch(imapv2.UIDSetNum(num), &imapv2.FetchOptions{
		UID:         true,
		BodySection: []*imapv2.FetchItemBodySection{section},
	})
	found := false
	var readErr error
	for msg := cmd.Next(); msg != nil; msg = cmd.Next() {
		for item := msg.Next(); item != nil; item = msg.Next() {
			data, ok := item.(imapclient.FetchItemDataBodySection)
			if !ok || data.Literal == nil || found {
				continue
			}
			found = true
			readErr = read(data.Literal)
		}
	}
	if err := cmd.Close(); err != nil {
		return fmt.Errorf("imap: fetch message %s in %q: %w", uid, folder, err)
	}
	if !found {
		return fmt.Errorf("%w: uid %s in %q", ErrMessageGone, uid, folder)
	}
	return readErr
}

// partOf is what a leaf says about itself, as internal/attachment's rule
// reads it.
func partOf(header gomessage.Header) attachment.Part {
	mediaType, _, err := header.ContentType()
	if err != nil {
		// A type that does not parse says nothing reliable about the file:
		// saved as bytes, it still opens with whatever the name says.
		mediaType = "application/octet-stream"
	}
	return attachment.Part{
		Filename:    partFilename(header),
		MediaType:   mediaType,
		Disposition: header.Get("Content-Disposition"),
		ContentID:   header.Get("Content-Id"),
	}
}

// partFilename is a part's file name, decoded: the filename parameter of
// Content-Disposition, else the name parameter of Content-Type, which older
// programs write instead. A header whose parameters do not parse gives no
// name from there: the other header may still have one.
func partFilename(header gomessage.Header) string {
	if params, err := headerParams(header.Get("Content-Disposition")); err == nil && params["filename"] != "" {
		return params["filename"]
	}
	if params, err := headerParams(header.Get("Content-Type")); err == nil {
		return params["name"]
	}
	return ""
}

// wordDecoder decodes RFC 2047 words in any charset go-message knows.
var wordDecoder = &mime.WordDecoder{CharsetReader: charset.Reader}

// extendedCharset finds the charset of a parameter written the RFC 2231
// way, whole (filename*=iso-8859-1'fr'Factur%E9.pdf) or cut in pieces, the
// first of which carries it (filename*0*=iso-8859-1'fr'Factur).
var extendedCharset = regexp.MustCompile(`(?i)(;\s*([a-z0-9_.-]+)\*(?:0\*)?\s*=\s*"?)([^'";\s]*)'`)

// headerParams reads the parameters of a header value, decoded the two ways
// an accented name is written: an RFC 2231 parameter, in any charset, and an
// RFC 2047 word inside the value.
//
// mime.ParseMediaType reads RFC 2231 in UTF-8 and US-ASCII only, and drops
// a parameter in any other charset without a word: a file sent as
// filename*=iso-8859-1'fr'Factur%E9.pdf lost its name. Such a charset is
// swapped for utf-8 before parsing, which makes the parser keep the bytes as
// they were sent, and the bytes are then read in the charset they were
// written in. A charset nobody knows leaves the parameter out, as before.
func headerParams(value string) (map[string]string, error) {
	charsets := map[string]string{}
	value = extendedCharset.ReplaceAllStringFunc(value, func(m string) string {
		sub := extendedCharset.FindStringSubmatch(m)
		name := strings.ToLower(sub[3])
		if name == "utf-8" || name == "us-ascii" {
			return m
		}
		charsets[strings.ToLower(sub[2])] = name
		return sub[1] + "utf-8'"
	})
	_, params, err := mime.ParseMediaType(value)
	if err != nil {
		return nil, err
	}
	for key, name := range charsets {
		raw, ok := params[key]
		if !ok || name == "" {
			continue
		}
		decoded, err := fromCharset(name, raw)
		if err != nil {
			delete(params, key)
			continue
		}
		params[key] = decoded
	}
	for key, v := range params {
		if decoded, err := wordDecoder.DecodeHeader(v); err == nil {
			params[key] = decoded
		}
	}
	return params, nil
}

// fromCharset reads s, bytes in the named charset, as UTF-8.
func fromCharset(name, s string) (string, error) {
	r, err := charset.Reader(name, strings.NewReader(s))
	if err != nil {
		return "", err
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// transferDecoded is a part's body with its transfer encoding undone
// (base64, quoted-printable), and only that. go-message converts a text part
// to UTF-8 when it reads a whole entity; a file must come back as it was
// sent, a CSV in windows-1252 included, so the entity it decodes here is
// given the transfer encoding alone and no charset to convert from.
func transferDecoded(header gomessage.Header, body io.Reader) (io.Reader, error) {
	encoding := header.Get("Content-Transfer-Encoding")
	var only gomessage.Header
	only.Set("Content-Transfer-Encoding", encoding)
	entity, err := gomessage.New(only, body)
	if err != nil {
		return nil, fmt.Errorf("imap: transfer encoding %q: %w", encoding, err)
	}
	return entity.Body, nil
}
