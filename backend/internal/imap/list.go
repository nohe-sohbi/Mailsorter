package imap

import (
	"context"
	"fmt"
	"net/mail"
	"strconv"
	"strings"
	"time"

	imapv2 "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/protect"
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

// listedHeaders are the headers the listing and the reader read besides the
// envelope: the two unsubscribe ones, and From as its sender wrote it
// (senderFrom). Named here so the FETCH asks for them specifically:
// BODY[HEADER] pulls every header a sender wrote, which on marketing mail is
// routinely larger than the text of the message.
var listedHeaders = []string{"From", "List-Unsubscribe", "List-Unsubscribe-Post"}

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

	messages, err := c.c.Fetch(imapv2.SeqSet{{Start: low, Stop: high}}, listOptions()).Collect()
	if err != nil {
		return nil, fmt.Errorf("imap: fetch %q: %w", folder, err)
	}

	out := make([]models.Email, 0, len(messages))
	for i := len(messages) - 1; i >= 0; i-- {
		out = append(out, emailFrom(messages[i]))
	}
	return out, nil
}

// listOptions is what a listing reads of each message: the envelope, the
// flags and the two unsubscribe headers, never the body.
func listOptions() *imapv2.FetchOptions {
	return &imapv2.FetchOptions{
		UID:          true,
		Flags:        true,
		Envelope:     true,
		InternalDate: true,
		BodySection: []*imapv2.FetchItemBodySection{{
			Specifier:    imapv2.PartSpecifierHeader,
			HeaderFields: listedHeaders,
			// Peek is load-bearing and easy to miss. A plain BODY[...] fetch
			// sets \Seen on every message it touches, so a listing without it
			// marks the entire inbox read just by rendering it, and the user
			// watches their unread count fall to zero for no reason they did.
			Peek: true,
		}},
	}
}

// Envelopes reads these messages of folder, by UID, the way List reads a
// page: envelope, flags and unsubscribe headers, peeked. It is how a sync
// stores a message older than the page it lists, a favorite of years ago for
// instance. A UID the folder no longer holds is simply not in the answer.
func (c *Client) Envelopes(folder string, uids []string) (map[string]models.Email, error) {
	out := make(map[string]models.Email, len(uids))
	if len(uids) == 0 {
		return out, nil
	}
	var set imapv2.UIDSet
	asked := make(map[imapv2.UID]bool, len(uids))
	for _, uid := range uids {
		num, err := parseUID(uid)
		if err != nil {
			return nil, err
		}
		set.AddNum(num)
		asked[num] = true
	}
	if err := c.selectFolder(folder); err != nil {
		return nil, err
	}
	messages, err := c.c.Fetch(set, listOptions()).Collect()
	if err != nil {
		return nil, fmt.Errorf("imap: fetch %d messages of %q: %w", len(uids), folder, err)
	}
	for _, m := range messages {
		// Only what was asked, with its envelope: a server may slip in news
		// about another message (a flag changed elsewhere).
		if !asked[m.UID] || m.Envelope == nil {
			continue
		}
		email := emailFrom(m)
		out[email.MessageID] = email
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
	flags := flagNames(msg.Flags)
	email := models.Email{
		MessageID: strconv.FormatUint(uint64(msg.UID), 10),
		IsRead:    mailbox.IMAPIsRead(flags),
		IsStarred: mailbox.IMAPIsStarred(flags),
		CreatedAt: time.Now(),
	}

	if env := msg.Envelope; env != nil {
		email.Subject = env.Subject
		email.HeaderMessageID = env.MessageID
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
	email.From = senderFrom(msg, email.MessageID)

	headers, _ := fetchedHeaders(msg)
	links := unsubscribe.Parse(headers.first("List-Unsubscribe"), headers.first("List-Unsubscribe-Post"))
	email.UnsubURL = links.URL
	email.UnsubMailto = links.Mailto
	email.UnsubOneClick = links.OneClick

	return email
}

// flagNames is a message's flags as internal/mailbox reads them.
func flagNames(flags []imapv2.Flag) []string {
	as := make([]string, 0, len(flags))
	for _, f := range flags {
		as = append(as, string(f))
	}
	return as
}

// formatAddress renders a sender the way the rest of the app expects to read
// one: "Name <address>", the form a mail header uses, which is what the Gmail
// path stores and what extractSenderAddress and extractSenderName parse back.
//
// The address is written the way net/mail reads it back: its local part in
// quotes when it is not a dot-atom. go-imap hands the local part unquoted, so
// "<boss@corp.com>"@evil.io came back as <boss@corp.com>@evil.io, which no
// parser reads as evil.io's address, and a reader falling back on the last
// "<...>" took it for boss@corp.com.
//
// A From of several addresses is stored whole, as net/mail writes a list
// ("a@x.fr", "Lettre" <lettre@journal.fr>): keeping only the first one made
// "patron@victime.fr, Lettre <lettre@journal.fr>" read as the patron's, and
// the Gmail path, which stores the header as it is, reads such a list as no
// sender at all (protect.Address). The same goes for an address of the
// envelope without a mailbox or a domain (a group's marker): its name is
// written in quotes, which reads as no address, never alone, since a name
// "patron@victime.fr" would then be one.
func formatAddress(addrs []imapv2.Address) string {
	parts := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if part := formatOne(a); part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, ", ")
}

// formatOne is one address of an envelope, as formatAddress writes it.
func formatOne(a imapv2.Address) string {
	addr := addrSpec(a)
	name := a.Name
	if addr == "" && name == "" {
		// A group's marker carries its phrase in Mailbox.
		name = a.Mailbox
	}
	if addr == "" {
		if name == "" {
			return ""
		}
		return quoteName(name)
	}
	if name == "" {
		return addr
	}
	// Written out by hand, not with net/mail's Address.String: that one
	// re-encodes any non-ASCII name as RFC 2047 ("=?utf-8?q?R=C3=A9seau?="),
	// which the screens and the history then showed as is. go-imap already
	// decoded the name, in any charset go-message knows (Connect's
	// WordDecoder); it is stored readable, quoted the way the Gmail path
	// stores it.
	return quoteName(name) + ` <` + addr + `>`
}

// quoteName is a display name as a quoted string (protect.QuotedText: valid
// UTF-8, a control character turned into a space).
func quoteName(name string) string {
	return protect.QuotedText(name)
}

// addrSpec is the address of a, as an RFC 5322 addr-spec: the local part
// quoted when it needs to be (net/mail's own rule), "" when a has none.
func addrSpec(a imapv2.Address) string {
	addr := a.Addr()
	if addr == "" {
		return ""
	}
	spec := (&mail.Address{Address: addr}).String()
	return strings.TrimSuffix(strings.TrimPrefix(spec, "<"), ">")
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
