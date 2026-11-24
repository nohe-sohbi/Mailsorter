package imap

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	imapv2 "github.com/emersion/go-imap/v2"
	gomessage "github.com/emersion/go-message"
	"github.com/emersion/go-message/textproto"
)

// The files attached to a message.
//
// Over the Gmail API, Google walks the MIME tree itself and hands each file
// over decoded, behind an id of its own. Over IMAP the files are inside the
// message as its server keeps it: leaves of a multipart, encoded for transport
// (base64, quoted-printable), their names sometimes encoded twice over (an
// RFC 2047 word inside the parameter, or an RFC 2231 parameter). This file is
// that walk, and the reader and the download both go through it, so the two
// cannot disagree about which parts of a message are files.
//
// A file is named by its IMAP part number ("2", "1.3"), the address IMAP gives
// a part of a message. It is what the reader hands out and what the download
// sends back, and the download serves a number only when the walk of that
// same message lists a file under it. A number that names the text of the
// mail ("1"), or a section IMAP knows by name ("TEXT"), is not a file and
// gets nothing: the route would otherwise read any part of any message the
// caller can name.

// ErrNoSuchAttachment means the message has no file under that part number.
// Typed because it is an answer to give the caller ("not in this mail"), not a
// failure of the server.
var ErrNoSuchAttachment = errors.New("imap: no such attachment in this message")

// maxPartDepth bounds how deep the walk follows nested multiparts. Real mail
// nests three or four levels (mixed, related, alternative); the bound is there
// so a crafted message cannot make the walk recurse without end.
const maxPartDepth = 12

// Attachment is one file attached to a message.
type Attachment struct {
	// Part is where the file sits in the message, numbered the way IMAP
	// numbers parts. It is the file's id in the reader and in the download.
	Part     string
	Filename string
	// MimeType is the media type the sender declared, without parameters.
	MimeType string
	// Size is the size of the file itself, decoded: what Gmail reports and
	// what the person saves. The part as stored is a third larger in base64.
	Size int64
}

// Attachments lists the files attached to raw, a whole message as BODY[]
// returns it, in the order the message carries them.
//
// A file whose bytes do not decode is left out and named in the error, and so
// is a MIME tree that breaks off, with the files found before the break: the
// reader still opens the mail, with the files that can be saved.
func Attachments(raw []byte) ([]Attachment, error) {
	var out []Attachment
	var undecodable []error
	walkErr := walkLeaves(raw, func(number string, header gomessage.Header, body io.Reader) error {
		att, ok := attachmentOf(number, header)
		if !ok {
			return nil
		}
		size, err := decodedSize(header, body)
		if err != nil {
			undecodable = append(undecodable, fmt.Errorf("imap: attachment %s %q left out: %w", number, att.Filename, err))
			return nil
		}
		att.Size = size
		out = append(out, att)
		return nil
	})
	return out, errors.Join(append(undecodable, walkErr)...)
}

// attachmentIn returns the file attached to raw under part number, decoded,
// or ErrNoSuchAttachment when the message has no file there. It decides with
// the rule Attachments lists with, so a part is served only if the reader
// lists it.
func attachmentIn(raw []byte, number string) (Attachment, []byte, error) {
	var found Attachment
	var data []byte
	err := walkLeaves(raw, func(n string, header gomessage.Header, body io.Reader) error {
		if n != number {
			return nil
		}
		att, ok := attachmentOf(n, header)
		if !ok {
			return ErrNoSuchAttachment
		}
		decoded, err := transferDecoded(header, body)
		if err != nil {
			return err
		}
		if data, err = io.ReadAll(decoded); err != nil {
			return fmt.Errorf("imap: decode attachment %s: %w", n, err)
		}
		att.Size = int64(len(data))
		found = att
		return errStopWalk
	})
	switch {
	case errors.Is(err, errStopWalk):
		return found, data, nil
	case err != nil:
		return Attachment{}, nil, err
	default:
		return Attachment{}, nil, ErrNoSuchAttachment
	}
}

// Attachment returns the file attached under part to the message uid in
// folder, decoded, with what the reader lists about it. A part the message
// has no file under, whatever else it names there, is ErrNoSuchAttachment.
//
// The fetch PEEKS, like Fetch: saving a file someone sent is not reading
// their mail, and the unread count must not change because of it. It reads
// the whole message, as the reader does, because the part number is only
// trusted once the same walk has listed it.
func (c *Client) Attachment(ctx context.Context, folder, uid, part string) (Attachment, []byte, error) {
	_ = ctx // see List on why the IMAP client takes no context

	msg, err := c.fetchOne(folder, uid, &imapv2.FetchOptions{
		UID:         true,
		BodySection: []*imapv2.FetchItemBodySection{{Peek: true}},
	})
	if err != nil {
		return Attachment{}, nil, err
	}
	return attachmentIn(wholeMessage(msg), part)
}

// errStopWalk ends a walk early, once what it looked for is found.
var errStopWalk = errors.New("imap: stop the walk")

// walkLeaves calls visit for every part of raw that is not a multipart, in
// order, with its IMAP part number, its header, and its body as stored (still
// encoded for transport). A message that is not a multipart has one part, its
// body, numbered 1. A message/rfc822 part is a leaf: a forwarded mail is one
// file. An error from visit ends the walk and is returned as is.
func walkLeaves(raw []byte, visit func(number string, header gomessage.Header, body io.Reader) error) error {
	r := bufio.NewReader(bytes.NewReader(raw))
	h, err := textproto.ReadHeader(r)
	if err != nil {
		return fmt.Errorf("imap: read the message header: %w", err)
	}
	header := gomessage.Header{Header: h}
	if boundary, ok := multipartBoundary(header); ok {
		return walkMultipart(r, boundary, "", 1, visit)
	}
	return visit("1", header, r)
}

// walkMultipart walks the parts of one multipart body, numbering them under
// prefix ("" at the top, "2." inside part 2).
func walkMultipart(body io.Reader, boundary, prefix string, depth int, visit func(string, gomessage.Header, io.Reader) error) error {
	if depth > maxPartDepth {
		return fmt.Errorf("imap: parts nested deeper than %d levels", maxPartDepth)
	}
	mr := textproto.NewMultipartReader(body, boundary)
	for i := 1; ; i++ {
		p, err := mr.NextPart()
		if err == io.EOF {
			return nil
		}
		number := prefix + strconv.Itoa(i)
		if err != nil {
			return fmt.Errorf("imap: read part %s: %w", number, err)
		}
		header := gomessage.Header{Header: p.Header}
		if inner, ok := multipartBoundary(header); ok {
			err = walkMultipart(p, inner, number+".", depth+1, visit)
		} else {
			err = visit(number, header, p)
		}
		if err != nil {
			return err
		}
	}
}

// multipartBoundary is the boundary of a multipart part, and whether it is
// one. A multipart without a boundary cannot be split, so it is a leaf.
func multipartBoundary(header gomessage.Header) (string, bool) {
	mediaType, params, err := header.ContentType()
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		return "", false
	}
	return params["boundary"], params["boundary"] != ""
}

// attachmentOf says whether a leaf is a file attached to the message, and
// describes it, size aside.
//
// A file has a name. Without one, a part is the text of the mail in some form.
// A named part is still not an attachment when it is a piece of the body: an
// image the HTML shows through cid: carries a Content-ID, and listing those
// turns a newsletter into a row of logo.png chips. Content-Disposition:
// attachment wins over that, because Gmail puts a Content-ID on every file a
// person attaches from its web interface.
func attachmentOf(number string, header gomessage.Header) (Attachment, bool) {
	name := partFilename(header)
	if name == "" {
		return Attachment{}, false
	}
	if dispositionOf(header) != "attachment" && strings.TrimSpace(header.Get("Content-Id")) != "" {
		return Attachment{}, false
	}
	mediaType, _, err := header.ContentType()
	if err != nil {
		// A type that does not parse says nothing reliable about the file:
		// saved as bytes, it still opens with whatever the name says.
		mediaType = "application/octet-stream"
	}
	return Attachment{Part: number, Filename: name, MimeType: mediaType}, true
}

// partFilename is a part's file name, decoded: the filename parameter of
// Content-Disposition, else the name parameter of Content-Type, which older
// programs write instead. go-message decodes both ways of writing an accented
// name, RFC 2231 (mime.ParseMediaType) and an RFC 2047 word (its own pass over
// each value). A header whose parameters do not parse gives no name from
// there: the other header may still have one.
func partFilename(header gomessage.Header) string {
	if _, params, err := header.ContentDisposition(); err == nil && params["filename"] != "" {
		return params["filename"]
	}
	if _, params, err := header.ContentType(); err == nil {
		return params["name"]
	}
	return ""
}

// dispositionOf is the disposition type alone ("attachment", "inline"), read
// before the first parameter, so a malformed parameter does not hide it.
func dispositionOf(header gomessage.Header) string {
	value, _, _ := strings.Cut(header.Get("Content-Disposition"), ";")
	return strings.ToLower(strings.TrimSpace(value))
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

// decodedSize is the size of a part's body once decoded, counted without
// holding it.
func decodedSize(header gomessage.Header, body io.Reader) (int64, error) {
	decoded, err := transferDecoded(header, body)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(io.Discard, decoded)
	if err != nil {
		return 0, fmt.Errorf("imap: decode: %w", err)
	}
	return n, nil
}
