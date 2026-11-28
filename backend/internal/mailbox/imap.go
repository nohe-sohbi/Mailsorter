package mailbox

import (
	"fmt"
)

// The IMAP half of the translation table.
//
// IMAP does not have labels. It has two things, and every action Mailsorter
// performs becomes one of them:
//
//   - flags, a small set of per-message booleans (\Seen, \Flagged, ...)
//   - folders, which a message belongs to exactly one of, so changing folder is
//     a move rather than an addition
//
// So a label becomes the folder of that name: labelling a mail files it there,
// out of the inbox, and taking the label off brings it back to the inbox.
//
// That is the whole reason this file exists rather than a second switch inside
// the IMAP client. Over the Gmail API archiving is "remove the INBOX label";
// over IMAP the same intent is "move this message to the archive folder". Same
// verb from the user, two genuinely different operations, and neither handler
// nor client should be the place that knows which.

// IMAPKind says which of the two shapes a mutation takes.
type IMAPKind int

const (
	// IMAPFlagChange sets or clears per-message flags. The message stays put.
	IMAPFlagChange IMAPKind = iota + 1
	// IMAPMove relocates the message to another folder.
	IMAPMove
)

// IMAP system flags, as RFC 3501 spells them. They are here rather than in the
// client for the same reason Gmail's label ids are: one place knows the
// vocabulary.
const (
	FlagSeen    = "\\Seen"
	FlagFlagged = "\\Flagged"
	FlagDeleted = "\\Deleted"
)

// Folder is a destination named by WHAT IT IS rather than by what it is called.
//
// This distinction is load-bearing. The archive folder is "[Gmail]/All Mail" on
// Gmail, "Archive" on Fastmail, "Archives" on several French providers, and
// whatever the user renamed it to on a self-run server. The literal name is
// discovered at runtime by listing the mailboxes and reading their RFC 6154
// special-use attributes, which is I/O and therefore the client's job. This
// package says which folder is meant; the client says what it is called there.
type Folder string

const (
	// FolderInbox is the one name RFC 3501 does fix, so it needs no lookup.
	FolderInbox Folder = "INBOX"
	// FolderArchive and FolderTrash are special-use attributes, not names.
	FolderArchive Folder = "\\Archive"
	FolderTrash   Folder = "\\Trash"
	// FolderSent, FolderDrafts and FolderJunk are special-use attributes too.
	// No action moves a message there: they are named so that the folders a
	// mail can be filed into (the user's own) can leave them out.
	FolderSent   Folder = "\\Sent"
	FolderDrafts Folder = "\\Drafts"
	FolderJunk   Folder = "\\Junk"
)

// IMAPOp is a mutation expressed in IMAP's own terms.
type IMAPOp struct {
	Kind      IMAPKind
	AddFlags  []string
	DropFlags []string
	// To is the destination of an IMAPMove to a special folder, named by what
	// it is, and empty otherwise.
	To Folder
	// Into is the destination of an IMAPMove into one of the user's own
	// folders, named by its full name on the server ("Factures",
	// "Projets/Maison"): filing, which is what a label is over IMAP. Empty
	// otherwise. A move has exactly one of To and Into.
	Into string
}

// IMAPOpFor translates a mutation into the IMAP operation that performs it.
//
// Note what the table decides that a per-call-site switch would get wrong.
// Marking read CLEARS nothing and sets \Seen, while marking unread clears it,
// so read state is a flag here and the absence of a label over the Gmail API:
// the two transports disagree, and this is where that disagreement is resolved.
// Archiving and trashing are moves, so they are not reversible by re-running
// the opposite flag change; their inverse is another move, back to the inbox.
// Labelling is a move too, into the folder that stands for the label, and so is
// taking the label off: a message sits in one folder, so leaving the label's
// folder means going back to the inbox, whichever label it was.
func IMAPOpFor(m Mutation) (IMAPOp, error) {
	switch m.Action {
	case ActionMarkRead:
		return IMAPOp{Kind: IMAPFlagChange, AddFlags: []string{FlagSeen}}, nil
	case ActionMarkUnread:
		return IMAPOp{Kind: IMAPFlagChange, DropFlags: []string{FlagSeen}}, nil
	case ActionStar:
		return IMAPOp{Kind: IMAPFlagChange, AddFlags: []string{FlagFlagged}}, nil
	case ActionUnstar:
		return IMAPOp{Kind: IMAPFlagChange, DropFlags: []string{FlagFlagged}}, nil
	case ActionArchive:
		return IMAPOp{Kind: IMAPMove, To: FolderArchive}, nil
	case ActionTrash:
		return IMAPOp{Kind: IMAPMove, To: FolderTrash}, nil
	case ActionUnarchive, ActionUntrash:
		// Both mean "put it back where the user looks", and on IMAP that is one
		// operation. Over the Gmail API they differ (untrashing must also drop
		// the TRASH label), which is exactly the kind of per-transport detail
		// that stops at this file.
		return IMAPOp{Kind: IMAPMove, To: FolderInbox}, nil
	case ActionLabel:
		// The label id is the folder's full name: that is what names a folder
		// on the server, and what the user reads.
		if m.LabelID == "" {
			return IMAPOp{}, ErrLabelRequired
		}
		return IMAPOp{Kind: IMAPMove, Into: m.LabelID}, nil
	case ActionUnlabel:
		return IMAPOp{Kind: IMAPMove, To: FolderInbox}, nil
	}
	return IMAPOp{}, fmt.Errorf("%w: %q", ErrUnknownAction, m.Action)
}

// IMAPOpsFor translates several mutations, preserving order.
//
// Unlike the Gmail side, these do NOT collapse into one operation: a flag
// change and a move are different IMAP commands, and a move invalidates the
// message's sequence number and UID in the folder it left. So the caller runs
// them in order and must re-resolve the message after a move. Returning a slice
// rather than a merged pair is how that constraint is made visible instead of
// being discovered in production.
func IMAPOpsFor(muts []Mutation) ([]IMAPOp, error) {
	ops := make([]IMAPOp, 0, len(muts))
	for _, m := range muts {
		op, err := IMAPOpFor(m)
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}
	return ops, nil
}

// IMAPIsRead reads a message's state from its flags. The counterpart of
// GmailIsRead, and the inverse convention: over IMAP read is the PRESENCE of
// \Seen, where over the Gmail API it is the ABSENCE of UNREAD.
func IMAPIsRead(flags []string) bool {
	return hasFlag(flags, FlagSeen)
}

// IMAPIsStarred reads the star from a message's flags: \Flagged, which mail
// programs show as a star or a flag. Over the Gmail API the same star is the
// STARRED label.
func IMAPIsStarred(flags []string) bool {
	return hasFlag(flags, FlagFlagged)
}

func hasFlag(flags []string, want string) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}
