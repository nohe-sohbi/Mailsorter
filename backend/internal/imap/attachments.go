package imap

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"regexp"
	"strconv"
	"strings"

	imapv2 "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	gomessage "github.com/emersion/go-message"
	"github.com/emersion/go-message/charset"
	"github.com/emersion/go-message/textproto"
	"github.com/nohe-sohbi/mailsorter/backend/internal/attachment"
)

// The files attached to a message.
//
// Over the Gmail API, Google walks the MIME tree itself and hands each file
// over decoded, behind an id of its own. Over IMAP the files are inside the
// message as its server keeps it: leaves of a multipart, encoded for transport
// (base64, quoted-printable), their names sometimes encoded twice over (an
// RFC 2047 word inside the parameter, or an RFC 2231 parameter). This file is
// that walk, and the reader and the download both go through it, so the two
// cannot disagree about which parts of a message are files. Which leaves are
// files, and under which name, is internal/attachment's rule, the one the
// Gmail API transport applies too.
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

// ErrAttachmentTooLarge means the file is larger, decoded, than the caller
// agreed to serve. Typed for the same reason: no retry will make it fit.
var ErrAttachmentTooLarge = errors.New("imap: attachment larger than the limit")

// maxPartDepth bounds how deep the walk follows nested multiparts. Real mail
// nests three or four levels (mixed, related, alternative); the bound is there
// so a crafted message cannot make the walk recurse without end.
const maxPartDepth = 12

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
	Size int64
}

// Attachments lists the files attached to raw, a whole message as BODY[]
// returns it, in the order the message carries them.
//
// A file whose bytes do not decode is left out and named in the error, and so
// is a MIME tree that breaks off, with the files found before the break: the
// reader still opens the mail, with the files that can be saved.
func Attachments(raw []byte) ([]Attachment, error) {
	type candidate struct {
		number string
		part   attachment.Part
		size   int64
		err    error
	}
	var candidates []candidate
	var shown attachment.Shown
	htmlSeen := false
	walkErr := walkLeaves(bytes.NewReader(raw), func(number string, header gomessage.Header, body io.Reader) error {
		part := partOf(header)
		_, file := attachment.Listed(part, nil)
		html := !htmlSeen && isRenderedHTML(header)
		if html {
			htmlSeen = true
		}
		if !file && !html {
			return nil
		}
		// One read serves both: the size of a file, and the start of the
		// HTML the reader renders, for the images it shows.
		keep := 0
		if html {
			keep = maxBodyBytes
		}
		read := &headOf{max: keep}
		decoded, err := transferDecoded(header, body)
		if err == nil {
			if _, err = io.Copy(read, decoded); err != nil {
				err = fmt.Errorf("imap: decode: %w", err)
			}
		}
		if html {
			shown = attachment.ShownBy(string(read.kept))
		}
		if file {
			candidates = append(candidates, candidate{number: number, part: part, size: read.n, err: err})
		}
		return nil
	})

	var out []Attachment
	var undecodable []error
	for _, c := range candidates {
		name, ok := attachment.Listed(c.part, shown)
		if !ok {
			continue
		}
		if c.err != nil {
			undecodable = append(undecodable, fmt.Errorf("imap: attachment %s %q left out: %w", c.number, name, c.err))
			continue
		}
		out = append(out, Attachment{Part: c.number, Filename: name, MimeType: c.part.MediaType, Size: c.size})
	}
	return out, errors.Join(append(undecodable, walkErr)...)
}

// attachmentIn returns the file attached under part number to the message
// read from r, decoded, or ErrNoSuchAttachment when the message has no file
// there. It decides with the rule Attachments lists with, so a part is served
// only if the reader lists it.
//
// The file is read through a limit: one larger than limit once decoded is
// ErrAttachmentTooLarge, and no more than limit+1 bytes of it are ever held.
// The rest of the message streams past without being kept, so downloading
// the small file of a mail that also carries a large one holds the small one
// only.
func attachmentIn(r io.Reader, number string, limit int64) (Attachment, []byte, error) {
	var (
		found     bool
		part      attachment.Part
		data      []byte
		decodeErr error
		tooLarge  bool
		shown     attachment.Shown
		htmlSeen  bool
	)
	err := walkLeaves(r, func(n string, header gomessage.Header, body io.Reader) error {
		html := !htmlSeen && isRenderedHTML(header)
		if html {
			htmlSeen = true
		}
		switch {
		case n == number:
			found = true
			part = partOf(header)
			if _, file := attachment.Listed(part, nil); !file {
				return ErrNoSuchAttachment
			}
			decoded, err := transferDecoded(header, body)
			if err != nil {
				decodeErr = err
				break
			}
			if data, err = io.ReadAll(io.LimitReader(decoded, limit+1)); err != nil {
				decodeErr = fmt.Errorf("imap: decode attachment %s: %w", n, err)
				break
			}
			if int64(len(data)) > limit {
				tooLarge = true
				data = nil
			}
			if html {
				shown = attachment.ShownBy(string(data[:min(len(data), maxBodyBytes)]))
			}
		case html:
			if decoded, err := transferDecoded(header, body); err == nil {
				head, _ := io.ReadAll(io.LimitReader(decoded, maxBodyBytes))
				shown = attachment.ShownBy(string(head))
			}
		}
		// Only the first HTML counts: once the file and it are read, the
		// rest of the message cannot change the answer.
		if found && htmlSeen {
			return errStopWalk
		}
		return nil
	})
	switch {
	case errors.Is(err, ErrNoSuchAttachment):
		return Attachment{}, nil, ErrNoSuchAttachment
	case !found && err != nil && !errors.Is(err, errStopWalk):
		return Attachment{}, nil, err
	case !found:
		return Attachment{}, nil, ErrNoSuchAttachment
	}
	// A tree that breaks off after the file is judged on what was read
	// before the break, as Attachments lists it.
	name, ok := attachment.Listed(part, shown)
	switch {
	case !ok:
		return Attachment{}, nil, ErrNoSuchAttachment
	case decodeErr != nil:
		return Attachment{}, nil, decodeErr
	case tooLarge:
		return Attachment{}, nil, ErrAttachmentTooLarge
	}
	return Attachment{Part: number, Filename: name, MimeType: part.MediaType, Size: int64(len(data))}, data, nil
}

// Attachment returns the file attached under part to the message uid in
// folder, decoded, with what the reader lists about it. A part the message
// has no file under, whatever else it names there, is ErrNoSuchAttachment; a
// file larger than limit once decoded is ErrAttachmentTooLarge; a message no
// longer in folder is ErrMessageGone.
//
// The size comes first, from the server's description of the message
// (BODYSTRUCTURE): a file that cannot fit under limit is refused before a
// byte of it is read. Then the message is read whole, because the part
// number is only trusted once the same walk as the reader's has listed it,
// but as it arrives rather than into memory, and only the file is kept.
//
// The fetch PEEKS, like Fetch: saving a file someone sent is not reading
// their mail, and the unread count must not change because of it.
//
// The IMAP client takes no context, so ctx is held to it another way: when
// ctx ends, the connection is closed, which ends the command waiting on it.
// The client cannot be used afterwards; the request it served is over anyway.
func (c *Client) Attachment(ctx context.Context, folder, uid, part string, limit int64) (Attachment, []byte, error) {
	if err := ctx.Err(); err != nil {
		return Attachment{}, nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = c.c.Close() })
	defer stop()

	att, data, err := c.attachment(folder, uid, part, limit)
	if err != nil && ctx.Err() != nil {
		return Attachment{}, nil, fmt.Errorf("imap: attachment %s of uid %s in %q: %w (%v)", part, uid, folder, ctx.Err(), err)
	}
	return att, data, err
}

func (c *Client) attachment(folder, uid, part string, limit int64) (Attachment, []byte, error) {
	described, err := c.fetchOne(folder, uid, &imapv2.FetchOptions{
		UID:           true,
		BodyStructure: &imapv2.FetchItemBodyStructure{Extended: true},
	})
	if err != nil {
		return Attachment{}, nil, err
	}
	if tooLargeToRead(described.BodyStructure, part, limit) {
		return Attachment{}, nil, ErrAttachmentTooLarge
	}

	var att Attachment
	var data []byte
	err = c.streamWhole(folder, uid, func(r io.Reader) error {
		var err error
		att, data, err = attachmentIn(r, part, limit)
		return err
	})
	return att, data, err
}

// streamWhole fetches the whole message uid in folder, peeking, and hands
// its bytes to read as they come off the connection. Whatever read leaves
// unread is drained and dropped, never held. A folder that no longer holds
// the message is ErrMessageGone.
func (c *Client) streamWhole(folder, uid string, read func(io.Reader) error) error {
	num, err := parseUID(uid)
	if err != nil {
		return err
	}
	if err := c.selectFolder(folder); err != nil {
		return err
	}
	cmd := c.c.Fetch(imapv2.UIDSetNum(num), &imapv2.FetchOptions{
		UID:         true,
		BodySection: []*imapv2.FetchItemBodySection{{Peek: true}},
	})
	found := false
	var readErr error
	for msg := cmd.Next(); msg != nil; msg = cmd.Next() {
		for item := msg.Next(); item != nil; item = msg.Next() {
			section, ok := item.(imapclient.FetchItemDataBodySection)
			if !ok || section.Literal == nil || found {
				continue
			}
			found = true
			readErr = read(section.Literal)
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

// tooLargeToRead says whether the leaf numbered number, as the server
// describes the message, is a file that cannot fit under limit once decoded.
// It only says yes when sure: a part the description does not name, or whose
// decoded size it cannot bound, is left to the read, which stops at the limit.
func tooLargeToRead(structure imapv2.BodyStructure, number string, limit int64) bool {
	leaf := leafAt(structure, number)
	if leaf == nil {
		return false
	}
	described := attachment.Part{MediaType: leaf.MediaType(), ContentID: leaf.ID}
	if hasNameParam(leaf) {
		// Only whether it has a name counts here, not the name.
		described.Filename = "named"
	}
	if d := leaf.Disposition(); d != nil {
		described.Disposition = d.Value
	}
	// No HTML yet, so an image it shows counts as a file here: at worst one
	// that large is told it is too large rather than not there.
	if _, file := attachment.Listed(described, nil); !file {
		return false
	}
	return minDecodedSize(int64(leaf.Size), leaf.Encoding) > limit
}

// leafAt is the part of structure numbered number, when it is a leaf. The
// server numbers parts the way walkLeaves does, a forwarded mail being one
// leaf.
func leafAt(structure imapv2.BodyStructure, number string) *imapv2.BodyStructureSinglePart {
	if structure == nil {
		return nil
	}
	var found *imapv2.BodyStructureSinglePart
	structure.Walk(func(path []int, part imapv2.BodyStructure) bool {
		if leaf, ok := part.(*imapv2.BodyStructureSinglePart); ok && partNumber(path) == number {
			found = leaf
		}
		return found == nil
	})
	return found
}

// partNumber writes a part path the way IMAP does: 1.3 for [1 3].
func partNumber(path []int) string {
	parts := make([]string, len(path))
	for i, n := range path {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ".")
}

// hasNameParam says whether a described leaf carries a name, in any of the forms
// a server may pass the parameter on (filename, filename*, name*0*...).
func hasNameParam(leaf *imapv2.BodyStructureSinglePart) bool {
	if d := leaf.Disposition(); d != nil {
		for key, value := range d.Params {
			if strings.HasPrefix(strings.ToLower(key), "filename") && value != "" {
				return true
			}
		}
	}
	for key, value := range leaf.Params {
		if strings.HasPrefix(strings.ToLower(key), "name") && value != "" {
			return true
		}
	}
	return false
}

// minDecodedSize is the smallest size a part stored in size bytes can have
// once its transfer encoding is undone, or 0 when that cannot be told.
func minDecodedSize(size int64, encoding string) int64 {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "7bit", "8bit", "binary":
		return size
	case "base64":
		// Three bytes in four characters, in lines of at most 76 characters
		// (RFC 2045), which is what mail programs write, some 64. Counted
		// as lines of 64 plus their line break, the estimate stays under the
		// real size for both, so a file that fits is never refused here.
		return size / 66 * 48
	default:
		// Quoted-printable shrinks by what only the text knows.
		return 0
	}
}

// errStopWalk ends a walk early, once what it looked for is found.
var errStopWalk = errors.New("imap: stop the walk")

// walkLeaves calls visit for every part of the message read from r that is
// not a multipart, in order, with its IMAP part number, its header, and its
// body as stored (still encoded for transport). A message that is not a
// multipart has one part, its body, numbered 1. A message/rfc822 part is a
// leaf: a forwarded mail is one file. An error from visit ends the walk and
// is returned as is.
func walkLeaves(r io.Reader, visit func(number string, header gomessage.Header, body io.Reader) error) error {
	br := bufio.NewReader(r)
	h, err := textproto.ReadHeader(br)
	if err != nil {
		return fmt.Errorf("imap: read the message header: %w", err)
	}
	header := gomessage.Header{Header: h}
	if boundary, ok := multipartBoundary(header); ok {
		return walkMultipart(br, boundary, "", 1, visit)
	}
	return visit("1", header, br)
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

// isRenderedHTML says whether a leaf is HTML the reader would render rather
// than a file: text/html not declared an attachment, as bodies picks it. The
// first one is the HTML whose cid: images are not listed as files.
func isRenderedHTML(header gomessage.Header) bool {
	mediaType, _, err := header.ContentType()
	return err == nil && mediaType == "text/html" &&
		attachment.DispositionType(header.Get("Content-Disposition")) != "attachment"
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
// given the transfer encoding alone and no charset to convert from. The
// HTML is read this way too: the cid: URLs looked for in it are ASCII.
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

// headOf keeps the first max bytes written to it, and counts them all: the
// size of a file without holding it, and the start of an HTML.
type headOf struct {
	max  int
	kept []byte
	n    int64
}

func (h *headOf) Write(p []byte) (int, error) {
	if room := h.max - len(h.kept); room > 0 {
		h.kept = append(h.kept, p[:min(room, len(p))]...)
	}
	h.n += int64(len(p))
	return len(p), nil
}
