package imap

import (
	"fmt"
	"strconv"
	"strings"

	imapv2 "github.com/emersion/go-imap/v2"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
)

// What a sync reads besides the listing.
//
// A listing reads the newest messages of a folder, envelope included, and
// nothing about the older ones. Two things about those still change, or were
// never read: what another mail program did to them (read, starred), and what
// the filters above the inbox ask of every message (its size, whether it
// carries a file). Both are read here, cheaply: the flags of the whole folder
// in one FETCH, and the size and the MIME structure of a batch of messages in
// another, neither of which downloads a message.

// State is what a message's flags say, of the two the app shows: read
// (\Seen) and the star (\Flagged).
type State struct {
	Read    bool
	Starred bool
}

// States returns the state of every message in folder, by UID, in one FETCH of
// the flags, whatever the size of the folder. It is how the sync knows which
// of the older messages it stored are still there, and what another mail
// program changed on them since.
func (c *Client) States(folder string) (map[string]State, error) {
	// Re-selected, like a listing: the answer must reflect the folder now.
	data, err := c.reselect(folder)
	if err != nil {
		return nil, err
	}
	out := make(map[string]State, data.NumMessages)
	if data.NumMessages == 0 {
		// A range on an empty folder is an error on some servers.
		return out, nil
	}
	// Read one message at a time rather than collected: on an inbox of fifty
	// thousand messages the answer is that many lines, and only two booleans of
	// each are kept.
	cmd := c.c.Fetch(imapv2.SeqSet{{Start: 1, Stop: data.NumMessages}}, &imapv2.FetchOptions{UID: true, Flags: true})
	for {
		msg := cmd.Next()
		if msg == nil {
			break
		}
		buf, err := msg.Collect()
		if err != nil {
			// Close only releases the command: the error to report is this one.
			_ = cmd.Close()
			return nil, fmt.Errorf("imap: read the flags of %q: %w", folder, err)
		}
		if buf.UID == 0 {
			continue
		}
		flags := flagNames(buf.Flags)
		out[strconv.FormatUint(uint64(buf.UID), 10)] = State{
			Read:    mailbox.IMAPIsRead(flags),
			Starred: mailbox.IMAPIsStarred(flags),
		}
	}
	if err := cmd.Close(); err != nil {
		return nil, fmt.Errorf("imap: read the flags of %q: %w", folder, err)
	}
	return out, nil
}

// Details is what the filters "Volumineux" and "Pièces jointes" ask of a
// message: its size in bytes, and whether it carries a file.
type Details struct {
	Size           int64
	HasAttachments bool
}

// Details reads the size (RFC822.SIZE) and the MIME structure (BODYSTRUCTURE)
// of these messages of folder, by UID, in one FETCH. The structure lists the
// parts without their content, so a mail of 8 MB costs what a mail of 8 KB
// does. A UID the folder no longer holds is simply not in the answer.
func (c *Client) Details(folder string, uids []string) (map[string]Details, error) {
	out := make(map[string]Details, len(uids))
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
		UID:           true,
		RFC822Size:    true,
		BodyStructure: &imapv2.FetchItemBodyStructure{Extended: true},
	}).Collect()
	if err != nil {
		return nil, fmt.Errorf("imap: read the size and the parts of %d messages in %q: %w", len(uids), folder, err)
	}
	for _, m := range msgs {
		// Only what was asked: a server may slip in news about another
		// message (a flag changed elsewhere), which carries neither.
		if !asked[m.UID] {
			continue
		}
		out[strconv.FormatUint(uint64(m.UID), 10)] = Details{
			Size:           m.RFC822Size,
			HasAttachments: hasAttachment(m.BodyStructure),
		}
	}
	return out, nil
}

// hasAttachment says whether a message carries a file, read from its MIME
// structure: the paper clip a mail program shows.
//
// Two shapes of mail look like attachments and are not. A newsletter sent in
// text and HTML (multipart/alternative) carries the same text twice. And an
// HTML mail draws its pictures from parts of its own, which it points to by
// their Content-ID (cid:), sometimes under a file name (image001.png): the
// Gmail reader leaves those out of its attachment list for the same reason
// (isInlinePart in internal/api/message.go).
func hasAttachment(bs imapv2.BodyStructure) bool {
	if bs == nil {
		return false
	}
	found := false
	bs.Walk(func(_ []int, part imapv2.BodyStructure) bool {
		if found {
			return false
		}
		single, ok := part.(*imapv2.BodyStructureSinglePart)
		if !ok {
			// A multipart: what it carries is in its parts.
			return true
		}
		found = isAttachedPart(single)
		return false
	})
	return found
}

// isAttachedPart is one part of the structure being a file attached to the
// mail: marked so (Content-Disposition: attachment), or a named file that is
// neither the text nor a picture the text draws.
func isAttachedPart(part *imapv2.BodyStructureSinglePart) bool {
	if disp := part.Disposition(); disp != nil && strings.EqualFold(disp.Value, "attachment") {
		return true
	}
	if part.Filename() == "" || part.ID != "" {
		return false
	}
	return !strings.EqualFold(part.Type, "text")
}
