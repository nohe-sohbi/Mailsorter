package imap

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/mail"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	imapv2 "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/unsubscribe"
)

// Reading a folder.
//
// This deliberately mirrors what the Gmail listing decided, because the same
// two costs apply. It fetches the envelope, the flags and the two unsubscribe
// headers, and NOT the body: a page is up to a hundred messages and shipping a
// hundred HTML payloads to render one is waste the user pays for in latency.
// The body is fetched when a reader actually opens a message, which is what
// internal/api/message.go does on the Gmail side.
//
// The headers are asked for by name rather than whole, for the same reason.

// maxListLimit caps a page. A folder can hold a hundred thousand messages and a
// caller asking for "everything" would hold the connection, and the user, for
// as long as that takes.
const maxListLimit = 200

// unsubscribeHeaders are the two the listing reads. Named here so the FETCH
// asks for them specifically: BODY[HEADER] pulls every header a sender wrote,
// which on marketing mail is routinely larger than the text of the message.
var unsubscribeHeaders = []string{"List-Unsubscribe", "List-Unsubscribe-Post"}

// List returns the newest messages in a folder, newest first.
//
// Newest first is not a sort applied afterwards: IMAP numbers messages in
// arrival order, so the newest are the highest sequence numbers, and the page
// is the top of that range read backwards. Sorting the result would be doing
// the same work twice and would quietly differ whenever InternalDate and
// arrival order disagree, which they do for anything filed by a server-side
// rule.
func (c *Client) List(ctx context.Context, folder string, limit int) ([]models.Email, error) {
	// The IMAP client does not take a context: a command is bounded by the
	// dial and TLS timeouts set at connect, and by the server's own write
	// timeout. The parameter stays on the signature because every caller is on
	// a request path and will expect to pass one, and adding it later would
	// mean touching each of them a second time.
	_ = ctx
	if limit <= 0 || limit > maxListLimit {
		limit = maxListLimit
	}

	// A listing always re-selects: it is the one operation whose whole purpose
	// is to see what is there NOW, and the cached selection is by definition
	// what was there when it was taken.
	data, err := c.reselect(folder)
	if err != nil {
		return nil, err
	}
	if data.NumMessages == 0 {
		return []models.Email{}, nil
	}

	high := data.NumMessages
	low := uint32(1)
	if uint32(limit) < high {
		low = high - uint32(limit) + 1
	}

	messages, err := c.c.Fetch(imapv2.SeqSet{{Start: low, Stop: high}}, &imapv2.FetchOptions{
		UID:          true,
		Flags:        true,
		Envelope:     true,
		InternalDate: true,
		BodySection: []*imapv2.FetchItemBodySection{{
			Specifier:    imapv2.PartSpecifierHeader,
			HeaderFields: unsubscribeHeaders,
			// Peek is load-bearing and easy to miss. A plain BODY[...] fetch
			// sets \Seen on every message it touches, so a listing without it
			// marks the entire inbox read just by rendering it, and the user
			// watches their unread count fall to zero for no reason they did.
			Peek: true,
		}},
	}).Collect()
	if err != nil {
		return nil, fmt.Errorf("imap: fetch %q: %w", folder, err)
	}

	out := make([]models.Email, 0, len(messages))
	for i := len(messages) - 1; i >= 0; i-- {
		out = append(out, emailFrom(messages[i]))
	}
	return out, nil
}

// reselect issues a SELECT even when the folder is already selected.
func (c *Client) reselect(folder string) (*imapv2.SelectData, error) {
	if folder == "" {
		folder = "INBOX"
	}
	data, err := c.c.Select(folder, nil).Wait()
	if err != nil {
		c.selected = ""
		return nil, fmt.Errorf("imap: select %q: %w", folder, err)
	}
	c.selected = folder
	return data, nil
}

// emailFrom projects one fetched message onto the app's Email.
//
// Three fields stay empty on purpose rather than being filled with something
// that looks right. Body, because the listing does not download it. ThreadID,
// because plain IMAP has no notion of a thread (Gmail exposes one through
// X-GM-THRID, which is not portable). LabelIDs, because a message on IMAP is in
// exactly one folder rather than carrying a set of labels, and folding the
// folder into a label list would make the Gmail-shaped code above believe it
// can add a second one.
func emailFrom(msg *imapclient.FetchMessageBuffer) models.Email {
	email := models.Email{
		MessageID: strconv.FormatUint(uint64(msg.UID), 10),
		IsRead:    isSeen(msg.Flags),
		CreatedAt: time.Now(),
	}

	if env := msg.Envelope; env != nil {
		email.Subject = env.Subject
		email.From = formatAddress(env.From)
		email.To = addressList(env.To)
		email.ReceivedDate = env.Date
	}
	// InternalDate is when the SERVER received the message, and it is the one a
	// mailbox is really ordered by. The Date header is written by the sender and
	// is wrong often enough (clock skew, deliberate backdating by bulk senders)
	// that trusting it would scatter the inbox.
	if !msg.InternalDate.IsZero() {
		email.ReceivedDate = msg.InternalDate
	}

	links := unsubscribe.Parse(headerValue(msg, "List-Unsubscribe"), headerValue(msg, "List-Unsubscribe-Post"))
	email.UnsubURL = links.URL
	email.UnsubMailto = links.Mailto
	email.UnsubOneClick = links.OneClick

	return email
}

func isSeen(flags []imapv2.Flag) bool {
	as := make([]string, 0, len(flags))
	for _, f := range flags {
		as = append(as, string(f))
	}
	return mailbox.IMAPIsRead(as)
}

// formatAddress renders a sender the way the rest of the app expects to read
// one: "Name <address>", the form a mail header uses, which is what the Gmail
// path stores and what extractSenderAddress and extractSenderName parse back.
func formatAddress(addrs []imapv2.Address) string {
	if len(addrs) == 0 {
		return ""
	}
	a := addrs[0]
	addr := a.Addr()
	if a.Name == "" {
		return addr
	}
	if addr == "" {
		return a.Name
	}
	return (&mail.Address{Name: a.Name, Address: addr}).String()
}

func addressList(addrs []imapv2.Address) []string {
	out := make([]string, 0, len(addrs))
	for i := range addrs {
		if addr := addrs[i].Addr(); addr != "" {
			out = append(out, addr)
		}
	}
	return out
}

// headerValue reads one header out of the fetched header block.
//
// The block comes back as raw bytes rather than parsed fields, so it is parsed
// here with the standard library's MIME header reader, which handles the
// folding a sender is free to use: a List-Unsubscribe with two endpoints
// routinely wraps onto a second line, and a naive line split would return half
// of it.
func headerValue(msg *imapclient.FetchMessageBuffer, name string) string {
	for _, section := range msg.BodySection {
		raw := section.Bytes
		if len(raw) == 0 {
			continue
		}
		// ReadMIMEHeader needs the blank line that ends a header block, and some
		// servers do not include it in a partial section fetch.
		if !bytes.HasSuffix(raw, []byte("\r\n\r\n")) {
			raw = append(append([]byte{}, raw...), "\r\n\r\n"...)
		}
		header, err := textproto.NewReader(bufio.NewReader(bytes.NewReader(raw))).ReadMIMEHeader()
		if err != nil && len(header) == 0 {
			continue
		}
		if v := strings.TrimSpace(header.Get(name)); v != "" {
			return v
		}
	}
	return ""
}
