package imap

import (
	"context"
	"fmt"
	"strconv"

	imapv2 "github.com/emersion/go-imap/v2"
)

// Reading the text of many messages, for the sorting rules.
//
// A rule may look at what a mail says (its body, or the snippet the list
// shows), and the stored copy of an IMAP inbox keeps neither: the listing
// reads the envelope only. So the rules read the text of the messages they
// judge, here, and only when one of them looks at it. It is a read like the
// listing's, never a reader opening the mail: it peeks, and the messages stay
// unread.

// textBytes is how much of each message Texts reads: its beginning, headers
// included, where the text of a mail is. A file attached after the text is
// not downloaded to judge it, and a batch of messages costs at most this much
// per message, whatever they carry.
const textBytes = 256 << 10 // 256 KiB

// Text is what a rule reads of a message besides its envelope.
type Text struct {
	// Body is the text of the message: its plain-text part, or the text of
	// its HTML part when it has none.
	Body string
	// Snippet is the one-line preview the list would show, as Fetch makes it.
	Snippet string
	// NotReached is a message larger than what is read, in whose beginning
	// no text part was found: a file before the text, for instance. Its text
	// was not read, and Body is empty for that reason, not because the
	// message has none: a rule must not judge it ("does not contain" would
	// match a text nobody read).
	NotReached bool
}

// Texts reads the text of these messages of folder, by UID, in one FETCH of
// the first textBytes of each, peeked: the messages stay unread. A UID the
// folder no longer holds is simply not in the answer. A context already done
// reads nothing: a caller reading batch after batch stops at the next one.
//
// A message go-imap cannot read fails the whole FETCH and closes the
// connection with it: read a batch through ReadInHalves.
func (c *Client) Texts(ctx context.Context, folder string, uids []string) (map[string]Text, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("imap: read the text of %d messages in %q: %w", len(uids), folder, err)
	}
	out := make(map[string]Text, len(uids))
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
	msgs, err := c.c.Fetch(set, &imapv2.FetchOptions{
		UID:        true,
		RFC822Size: true,
		BodySection: []*imapv2.FetchItemBodySection{{
			// Peek, as the listing does: BODY[] without it sets \Seen on
			// every message read, and applying the rules would mark the
			// inbox read.
			Peek:    true,
			Partial: &imapv2.SectionPartial{Offset: 0, Size: textBytes},
		}},
	}).Collect()
	if err != nil {
		return nil, fmt.Errorf("imap: read the text of %d messages in %q: %w", len(uids), folder, err)
	}
	for _, m := range msgs {
		// Only what was asked, with its text: a server may slip in news about
		// another message (a flag changed elsewhere), which carries none.
		if !asked[m.UID] || len(m.BodySection) == 0 {
			continue
		}
		uid := strconv.FormatUint(uint64(m.UID), 10)
		plain, html := readBodies(wholeMessage(m), true)
		if plain == "" && html == "" && m.RFC822Size > textBytes {
			out[uid] = Text{NotReached: true}
			continue
		}
		body := plain
		if body == "" {
			body = stripTags(html)
		}
		out[uid] = Text{Body: body, Snippet: snippet(plain, html)}
	}
	return out, nil
}
