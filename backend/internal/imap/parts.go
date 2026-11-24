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
	"github.com/nohe-sohbi/mailsorter/backend/internal/attachment"
)

// A message's parts, read without reading its files.
//
// The reader needs a message's text, its HTML and the list of its files; the
// download needs that list and one file. Neither needs the bytes of the
// other files, and a mail with a 20 MB video attached must not cost 20 MB to
// open. So a message is read in two steps: the server's description of its
// parts (BODYSTRUCTURE), then, in one command, the MIME header of each part,
// the text and the HTML, bounded, and the size of each part decoded, as the
// server measures it (BINARY.SIZE, on a server with BINARY). The bytes of a
// file are read by the download only, and only that file's.
//
// The reader and the download both go through readParts, so the list the
// reader shows is the list the download serves from.

// maxLeaves bounds how many parts of one message are read. Real mail has a
// handful; the bound is there so a crafted one cannot make one command ask
// the server for thousands of headers. Parts past it are not listed.
const maxLeaves = 100

// maxTextFetch is how much of the text or the HTML, as stored, is fetched:
// maxBodyBytes once decoded from base64 (four characters for three bytes),
// with room for the line breaks.
const maxTextFetch = maxBodyBytes/3*4 + maxBodyBytes/8

// leaf is one part of a message that is not a multipart: a file, the text,
// the HTML. A forwarded mail (message/rfc822) is one leaf.
type leaf struct {
	// path and number are its address, as IMAP numbers parts: [1 3], "1.3".
	path   []int
	number string
	// root is set when the message is this one part and nothing else.
	root bool
	// desc is the server's description of it.
	desc *imapv2.BodyStructureSinglePart
	// header is its MIME header, as sent.
	header gomessage.Header
	// size is its size decoded: the server's measure when exact is set,
	// estimated from its stored size otherwise.
	size  int64
	exact bool
}

// contents is what readParts read of a message.
type contents struct {
	plain, html string
	// files are the parts the reader lists as files, in the message's order.
	files []Attachment
	// leafOf is the leaf of each file, by part number.
	leafOf map[string]leaf
	// skipped names the files left out of the list, and why.
	skipped error
}

// file is the file listed under part number, and its leaf.
func (c *contents) file(number string) (Attachment, leaf, bool) {
	for _, f := range c.files {
		if f.Part == number {
			return f, c.leafOf[number], true
		}
	}
	return Attachment{}, leaf{}, false
}

// readParts reads, of the message uid in folder that structure describes,
// everything but its files: their headers and sizes, the HTML the reader
// renders (whose cid: images are not files), and the plain text when
// withText is set. Everything is fetched peeking.
func (c *Client) readParts(folder, uid string, structure imapv2.BodyStructure, withText bool) (*contents, error) {
	leaves := leavesOf(structure)
	plainAt, htmlAt := textLeaves(leaves)

	options := &imapv2.FetchOptions{UID: true}
	headers := make([]*imapv2.FetchItemBodySection, len(leaves))
	for i, l := range leaves {
		headers[i] = headerSection(l)
		options.BodySection = append(options.BodySection, headers[i])
	}
	bounded := &imapv2.SectionPartial{Offset: 0, Size: maxTextFetch}
	var plainSection, htmlSection *imapv2.FetchItemBodySection
	if withText && plainAt >= 0 {
		plainSection = contentSection(leaves[plainAt], bounded)
		options.BodySection = append(options.BodySection, plainSection)
	}
	if htmlAt >= 0 {
		htmlSection = contentSection(leaves[htmlAt], bounded)
		options.BodySection = append(options.BodySection, htmlSection)
	}
	if hasBinary(c.c.Caps()) {
		for _, l := range leaves {
			// A server asked to decode what it does not know refuses the
			// whole command (UNKNOWN-CTE): only the encodings it must know.
			if knownEncoding(l.desc.Encoding) {
				options.BinarySectionSize = append(options.BinarySectionSize, &imapv2.FetchItemBinarySectionSize{Part: l.path})
			}
		}
	}

	msg, err := c.fetchOne(folder, uid, options)
	if err != nil && len(options.BinarySectionSize) > 0 && !errors.Is(err, ErrMessageGone) {
		// A server that cannot decode one part (a broken base64) may refuse
		// the whole command over its size. Without the sizes the reader
		// still opens the mail, with estimated ones.
		options.BinarySectionSize = nil
		msg, err = c.fetchOne(folder, uid, options)
	}
	if err != nil {
		return nil, err
	}

	for i := range leaves {
		leaves[i].header = parseHeader(msg.FindBodySection(headers[i]))
		if n, ok := msg.FindBinarySectionSize(leaves[i].path); ok {
			leaves[i].size, leaves[i].exact = int64(n), true
		} else {
			leaves[i].size = estimatedSize(int64(leaves[i].desc.Size), leaves[i].desc.Encoding)
		}
	}
	out := &contents{leafOf: map[string]leaf{}}
	if plainSection != nil {
		out.plain = decodeText(leaves[plainAt].header, msg.FindBodySection(plainSection))
	}
	if htmlSection != nil {
		out.html = decodeText(leaves[htmlAt].header, msg.FindBodySection(htmlSection))
	}

	shown := attachment.ShownBy(out.html)
	var skipped []error
	for _, l := range leaves {
		part := partOf(l.header)
		name, ok := attachment.Listed(part, shown)
		if !ok {
			continue
		}
		if _, err := transferDecoded(l.header, strings.NewReader("")); err != nil {
			skipped = append(skipped, fmt.Errorf("imap: attachment %s %q left out: %w", l.number, name, err))
			continue
		}
		out.files = append(out.files, Attachment{Part: l.number, Filename: name, MimeType: part.MediaType, Size: l.size})
		out.leafOf[l.number] = l
	}
	out.skipped = errors.Join(skipped...)
	return out, nil
}

// leavesOf lists the leaves of structure, in order, numbered the way IMAP
// numbers parts. A message that is not a multipart is one leaf, numbered 1.
func leavesOf(structure imapv2.BodyStructure) []leaf {
	if single, ok := structure.(*imapv2.BodyStructureSinglePart); ok {
		return []leaf{{path: []int{1}, number: "1", root: true, desc: single}}
	}
	if structure == nil {
		return nil
	}
	var out []leaf
	structure.Walk(func(path []int, part imapv2.BodyStructure) bool {
		if single, ok := part.(*imapv2.BodyStructureSinglePart); ok && len(out) < maxLeaves {
			// The walk reuses the slice it hands over.
			out = append(out, leaf{path: append([]int(nil), path...), number: partNumber(path), desc: single})
		}
		return len(out) < maxLeaves
	})
	return out
}

// textLeaves picks the text and the HTML the reader renders, as a mail
// program does: the first text/plain and the first text/html not declared an
// attachment. -1 when there is none.
func textLeaves(leaves []leaf) (plainAt, htmlAt int) {
	plainAt, htmlAt = -1, -1
	for i, l := range leaves {
		if d := l.desc.Disposition(); d != nil && strings.EqualFold(d.Value, "attachment") {
			continue
		}
		switch l.desc.MediaType() {
		case "text/plain":
			if plainAt < 0 {
				plainAt = i
			}
		case "text/html":
			if htmlAt < 0 {
				htmlAt = i
			}
		}
	}
	return plainAt, htmlAt
}

// headerSection is where a leaf's MIME header is: the header of the message
// when the message is the leaf, BODY[n.MIME] otherwise.
func headerSection(l leaf) *imapv2.FetchItemBodySection {
	if l.root {
		return &imapv2.FetchItemBodySection{Specifier: imapv2.PartSpecifierHeader, Peek: true}
	}
	return &imapv2.FetchItemBodySection{Part: l.path, Specifier: imapv2.PartSpecifierMIME, Peek: true}
}

// contentSection is where a leaf's content is, as stored, within partial
// when it is set: the text of the message when the message is the leaf,
// BODY[n] otherwise.
func contentSection(l leaf, partial *imapv2.SectionPartial) *imapv2.FetchItemBodySection {
	if l.root {
		return &imapv2.FetchItemBodySection{Specifier: imapv2.PartSpecifierText, Partial: partial, Peek: true}
	}
	return &imapv2.FetchItemBodySection{Part: l.path, Partial: partial, Peek: true}
}

// partNumber writes a part path the way IMAP does: 1.3 for [1 3].
func partNumber(path []int) string {
	parts := make([]string, len(path))
	for i, n := range path {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ".")
}

// parseHeader reads a MIME header as a section returns it. One that does not
// parse is empty: the part then says nothing about itself, and is no file.
func parseHeader(raw []byte) gomessage.Header {
	h, err := textproto.ReadHeader(bufio.NewReader(bytes.NewReader(raw)))
	if err != nil {
		return gomessage.Header{}
	}
	return gomessage.Header{Header: h}
}

// decodeText is a text part read from raw, its stored bytes, decoded from its
// transfer encoding and its charset, and cut at maxBodyBytes. raw may stop
// short of the end (the fetch is bounded): what decodes before the cut is
// kept.
func decodeText(header gomessage.Header, raw []byte) string {
	// An encoding or a charset go-message does not know leaves the bytes as
	// they are, with an error saying so: they are still closer to the text
	// than nothing. A cut in the middle of a base64 group ends the read with
	// an error too, after what decoded before it.
	entity, _ := gomessage.New(header, bytes.NewReader(raw))
	text, _ := io.ReadAll(io.LimitReader(entity.Body, maxBodyBytes))
	return string(text)
}

// hasBinary says whether a server measures parts decoded (BINARY.SIZE,
// RFC 3516). IMAP4rev2 includes BINARY (RFC 9051), which go-imap's list of
// what IMAP4rev2 implies leaves out, so a server that announces IMAP4rev2
// alone has it too.
func hasBinary(caps imapv2.CapSet) bool {
	return caps.Has(imapv2.CapBinary) || caps.Has(imapv2.CapIMAP4rev2)
}

// knownEncoding says whether a transfer encoding is one every server must
// decode.
func knownEncoding(encoding string) bool {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "7bit", "8bit", "binary", "base64", "quoted-printable":
		return true
	}
	return false
}

// estimatedSize is the size a part stored in size bytes has decoded, as near
// as its stored size tells, for a server that does not measure it (no
// BINARY). Base64 is counted in the lines of 76 characters mail programs
// write, a few bytes off at most; quoted-printable and the rest as stored.
func estimatedSize(size int64, encoding string) int64 {
	if strings.EqualFold(strings.TrimSpace(encoding), "base64") {
		lines := (size + 77) / 78
		return max(0, (size-2*(lines-1))/4*3)
	}
	return size
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

// cannotFit says whether a file is sure to be larger than limit once
// decoded, before a byte of it is read: on the server's measure, or, without
// one, on the least its stored size allows.
func cannotFit(l leaf, limit int64) bool {
	if l.exact {
		return l.size > limit
	}
	return minDecodedSize(int64(l.desc.Size), l.desc.Encoding) > limit
}

// within runs work with the connection bound to ctx.
//
// The IMAP client takes no context: a command waits on the server as long as
// the server takes. So when ctx ends, the connection is closed, which ends
// the command waiting on it. The client cannot be used afterwards; the
// request it served is over anyway.
func (c *Client) within(ctx context.Context, what string, work func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = c.c.Close() })
	defer stop()
	err := work()
	if err != nil && ctx.Err() != nil {
		return fmt.Errorf("imap: %s: %w (%v)", what, ctx.Err(), err)
	}
	return err
}
