package imap

import (
	"context"
	"fmt"
	"log"
	"strings"

	imapv2 "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
)

// Reading ONE message, bodies included.
//
// This is the other half of the payload discipline in list.go. The listing
// deliberately downloads no body; this is what runs when a reader actually
// opens a message. It reads the text and the HTML, bounded, and the list of
// the files (parts.go), never the files themselves: those are the download's
// (attachments.go), one at a time.

// maxBodyBytes caps the text, and the HTML, a single message may cost. Mail
// has no size limit that anyone respects, and a newsletter with a 40 MB
// inline image in its HTML would otherwise be a memory spike per reader that
// opens it.
const maxBodyBytes = 5 << 20 // 5 MiB

// Message is one message with both renderings of its body. It mirrors what the
// reader screen needs: the plain text is what the app stores and searches, the
// HTML is what the user sees when the sender provided one, and the files are
// what it offers to save.
type Message struct {
	models.Email
	HTML        string
	Attachments []Attachment
	// InlineImages maps the cid of each picture the HTML draws to the part
	// number the screen downloads it under, to show it in the text.
	InlineImages map[string]string
}

// Fetch returns one message, bodies included.
//
// The fetch PEEKS, like the listing does. Opening a message is how a user reads
// it, but marking it read is a separate decision the caller makes (the reader
// passes markRead), and a fetch that set \Seen on its own would take that
// decision away: previewing, or opening by mistake, would be indistinguishable
// from reading.
//
// It reads the server's description of the message first, then the parts the
// reader shows (readParts): a mail with a large file opens for the cost of
// its text. When ctx ends, the connection is closed (within).
func (c *Client) Fetch(ctx context.Context, folder, uid string) (Message, error) {
	var out Message
	err := c.within(ctx, fmt.Sprintf("message uid %s in %q", uid, folder), func() error {
		var err error
		out, err = c.fetch(folder, uid)
		return err
	})
	if err != nil {
		return Message{}, err
	}
	return out, nil
}

func (c *Client) fetch(folder, uid string) (Message, error) {
	msg, err := c.fetchOne(folder, uid, &imapv2.FetchOptions{
		UID:           true,
		Flags:         true,
		Envelope:      true,
		InternalDate:  true,
		BodyStructure: &imapv2.FetchItemBodyStructure{Extended: true},
		BodySection: []*imapv2.FetchItemBodySection{{
			Specifier:    imapv2.PartSpecifierHeader,
			HeaderFields: listedHeaders,
			Peek:         true,
		}},
	})
	if err != nil {
		return Message{}, err
	}

	out := Message{Email: emailFrom(msg)}
	out.Folder = folder

	inside, err := c.readParts(folder, uid, msg.BodyStructure, true)
	if err != nil {
		return Message{}, err
	}
	out.Body = inside.plain
	out.HTML = inside.html
	// The snippet is what the list shows when it has no body of its own. Gmail
	// hands one over; IMAP does not, so it is taken from the text here rather
	// than left empty.
	out.Snippet = snippet(inside.plain, inside.html)

	// A file whose encoding nobody knows must not keep the person from
	// reading the mail, or from saving the others: it is left out, and said
	// here.
	if inside.skipped != nil {
		log.Printf("imap: message uid %s in %q: %v", uid, folder, inside.skipped)
	}
	out.Attachments = inside.files
	out.InlineImages = inside.inlineImages()
	return out, nil
}

// fetchOne fetches the message uid in folder with these options. A folder
// that holds no message under that number is ErrMessageGone: the list showed
// it, so another mail client (the person's phone) moved or deleted it since.
func (c *Client) fetchOne(folder, uid string, options *imapv2.FetchOptions) (*imapclient.FetchMessageBuffer, error) {
	num, err := parseUID(uid)
	if err != nil {
		return nil, err
	}
	if err := c.selectFolder(folder); err != nil {
		return nil, err
	}
	msgs, err := c.c.Fetch(imapv2.UIDSetNum(num), options).Collect()
	if err != nil {
		return nil, fmt.Errorf("imap: fetch message %s in %q: %w", uid, folder, err)
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("%w: uid %s in %q", ErrMessageGone, uid, folder)
	}
	return msgs[0], nil
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
