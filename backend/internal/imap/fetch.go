package imap

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	imapv2 "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/mail"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
)

// Reading ONE message, bodies included.
//
// This is the other half of the payload discipline in list.go. The listing
// deliberately downloads no body; this is what runs when a reader actually
// opens a message, and it is the only place in the IMAP transport that pays
// for the full MIME payload.

// maxBodyBytes caps what a single message may cost. Mail has no size limit that
// anyone respects, and the whole payload is read into memory to be parsed, so a
// message with a 40 MB inline image would otherwise be a memory spike per
// reader that opens it.
const maxBodyBytes = 5 << 20 // 5 MiB

// Message is one message with both renderings of its body. It mirrors what the
// reader screen needs: the plain text is what the app stores and searches, the
// HTML is what the user sees when the sender provided one.
type Message struct {
	models.Email
	HTML string
}

// Fetch returns one message, bodies included.
//
// The fetch PEEKS, like the listing does. Opening a message is how a user reads
// it, but marking it read is a separate decision the caller makes (the reader
// passes markRead), and a fetch that set \Seen on its own would take that
// decision away: previewing, or opening by mistake, would be indistinguishable
// from reading.
func (c *Client) Fetch(ctx context.Context, folder, uid string) (Message, error) {
	_ = ctx // see List on why the IMAP client takes no context

	num, err := parseUID(uid)
	if err != nil {
		return Message{}, err
	}
	if err := c.selectFolder(folder); err != nil {
		return Message{}, err
	}

	msgs, err := c.c.Fetch(imapv2.UIDSetNum(num), &imapv2.FetchOptions{
		UID:          true,
		Flags:        true,
		Envelope:     true,
		InternalDate: true,
		BodySection:  []*imapv2.FetchItemBodySection{{Peek: true}},
	}).Collect()
	if err != nil {
		return Message{}, fmt.Errorf("imap: fetch message %s in %q: %w", uid, folder, err)
	}
	if len(msgs) == 0 {
		return Message{}, fmt.Errorf("imap: no message with uid %s in %q", uid, folder)
	}

	out := Message{Email: emailFrom(msgs[0])}
	out.Folder = folder

	raw := wholeMessage(msgs[0])
	if len(raw) == 0 {
		return out, nil
	}
	plain, html := bodies(raw)
	out.Body = plain
	out.HTML = html
	// The snippet is what the list shows when it has no body of its own. Gmail
	// hands one over; IMAP does not, so it is taken from the text here rather
	// than left empty.
	out.Snippet = snippet(plain, html)
	return out, nil
}

// wholeMessage picks the section that holds the entire message. The fetch asks
// for one unqualified BODY.PEEK[], so there is exactly one and it is the whole
// payload, but the reply is a slice and reading it as such costs nothing.
func wholeMessage(msg *imapclient.FetchMessageBuffer) []byte {
	for _, section := range msg.BodySection {
		if len(section.Bytes) > 0 {
			return section.Bytes
		}
	}
	return nil
}

// bodies walks the MIME tree and returns the plain-text and HTML renderings.
//
// Both are collected rather than the first one found: a well-formed message
// carries the same content twice, and which one is usable depends on the
// reader. Falling back to the HTML when there is no plain part is what stops a
// marketing email, which routinely ships HTML only, from rendering blank.
func bodies(raw []byte) (plain, html string) {
	reader, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		// Not every sender produces something the parser accepts. A message that
		// cannot be walked is still worth showing as text rather than as an
		// error, so the raw payload is the last resort.
		return string(raw), ""
	}

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		header, ok := part.Header.(*mail.InlineHeader)
		if !ok {
			// An attachment. Its bytes are not the body, and downloading them is
			// the attachment route's job.
			continue
		}
		content, err := io.ReadAll(io.LimitReader(part.Body, maxBodyBytes))
		if err != nil {
			continue
		}
		mediaType, _, _ := header.ContentType()
		switch {
		case strings.EqualFold(mediaType, "text/html") && html == "":
			html = string(content)
		case strings.EqualFold(mediaType, "text/plain") && plain == "":
			plain = string(content)
		}
	}
	return plain, html
}

// snippet is the one-line preview the list shows. Gmail hands one over; IMAP
// does not, so it is derived from whichever body exists.
func snippet(plain, html string) string {
	const max = 200
	text := plain
	if text == "" {
		text = stripTags(html)
	}
	text = strings.Join(strings.Fields(text), " ")
	// Counted and cut in RUNES, not bytes. French prose is full of multi-byte
	// characters, and slicing at a byte index lands inside one often enough that
	// the preview would ship invalid UTF-8, which JSON encodes as a replacement
	// character the user sees.
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return strings.TrimSpace(string(runes[:max]))
}

// stripTags is a crude tag remover, and crude is the right level here: the
// result is a preview line, never rendered as markup, and the reader gets the
// real HTML through dompurify on the client.
func stripTags(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch r {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	return unescapeEntities(b.String())
}

func unescapeEntities(s string) string {
	replacer := strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", "\"", "&#39;", "'")
	return replacer.Replace(s)
}
