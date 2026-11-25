package imap

import (
	"fmt"
	"strconv"
	"strings"

	imapv2 "github.com/emersion/go-imap/v2"
	"github.com/nohe-sohbi/mailsorter/backend/internal/attachment"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
)

// What a sync reads besides the listing.
//
// A listing reads the newest messages of a folder, envelope included, and
// nothing about the older ones. Two things about those still change, or were
// never read: what another mail program did to them (read, starred), and what
// the filters above the inbox ask of every message (its size, whether it
// carries a file). Both are read here, cheaply: the flags and the size of the
// whole folder in one FETCH, and the MIME structure of a batch of messages in
// another, neither of which downloads a message.

// State is what the sync reads of every message of a folder: the two flags the
// app shows, read (\Seen) and the star (\Flagged), and the size of the whole
// message in bytes (RFC822.SIZE), a few bytes more per message.
type State struct {
	Read    bool
	Starred bool
	Size    int64
}

// States returns the state of every message in folder, by UID, in one FETCH of
// the flags and the size, whatever the size of the folder. It is how the sync
// knows which of the older messages it stored are still there, what another
// mail program changed on them since, and how large each one is.
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
	cmd := c.c.Fetch(imapv2.SeqSet{{Start: 1, Stop: data.NumMessages}}, &imapv2.FetchOptions{UID: true, Flags: true, RFC822Size: true})
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
			Size:    buf.RFC822Size,
		}
	}
	if err := cmd.Close(); err != nil {
		return nil, fmt.Errorf("imap: read the flags of %q: %w", folder, err)
	}
	return out, nil
}

// Attachments reads the MIME structure (BODYSTRUCTURE) of these messages of
// folder, by UID, in one FETCH, and says for each whether it carries a file,
// for the filter "Pièces jointes". The structure lists the parts without their
// content, so a mail of 8 MB costs what a mail of 8 KB does. A UID the folder
// no longer holds is simply not in the answer.
//
// A structure go-imap cannot read fails the whole FETCH and closes the
// connection with it: read a batch through ReadInHalves, which opens it again
// and finds the message.
func (c *Client) Attachments(folder string, uids []string) (map[string]bool, error) {
	out := make(map[string]bool, len(uids))
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
		BodyStructure: &imapv2.FetchItemBodyStructure{Extended: true},
	}).Collect()
	if err != nil {
		return nil, fmt.Errorf("imap: read the parts of %d messages in %q: %w", len(uids), folder, err)
	}
	for _, m := range msgs {
		// Only what was asked: a server may slip in news about another
		// message (a flag changed elsewhere), which carries no structure.
		if !asked[m.UID] || m.BodyStructure == nil {
			continue
		}
		out[strconv.FormatUint(uint64(m.UID), 10)] = hasAttachment(m.BodyStructure)
	}
	return out, nil
}

// hasAttachment says whether a message carries a file, read from its MIME
// structure: the paper clip a mail program shows.
//
// A part is a file by the rule the reader lists files with
// (internal/attachment), read without the HTML: a text in two versions
// (multipart/alternative) is no file, and a part with a Content-ID not
// declared an attachment is taken for a picture the HTML draws
// (attachment.ListedWithoutHTML). The reader, which reads the HTML, lists an
// image with a Content-ID that the HTML does not draw; the filter does not
// count it.
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
// mail, as attachment.ListedWithoutHTML decides it.
func isAttachedPart(part *imapv2.BodyStructureSinglePart) bool {
	p := attachment.Part{Filename: structureName(part), MediaType: part.MediaType(), ContentID: part.ID}
	if d := part.Disposition(); d != nil {
		p.Disposition = d.Value
	}
	return attachment.ListedWithoutHTML(p)
}

// structureName is a part's file name as the server describes it, in any of
// the forms it may pass the parameter on (filename, filename*, name*0*...).
// Only whether there is one counts for the filter, not how it reads.
func structureName(part *imapv2.BodyStructureSinglePart) string {
	if name := part.Filename(); name != "" {
		return name
	}
	if d := part.Disposition(); d != nil {
		for key, value := range d.Params {
			if strings.HasPrefix(strings.ToLower(key), "filename") && value != "" {
				return value
			}
		}
	}
	for key, value := range part.Params {
		if strings.HasPrefix(strings.ToLower(key), "name") && value != "" {
			return value
		}
	}
	return ""
}
