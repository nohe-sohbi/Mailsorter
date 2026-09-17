package mailbox

import (
	"errors"
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
)

// IMAPOp is a mutation expressed in IMAP's own terms.
type IMAPOp struct {
	Kind      IMAPKind
	AddFlags  []string
	DropFlags []string
	// To is the destination of an IMAPMove, and empty otherwise.
	To Folder
}

// ErrNoIMAPEquivalent is returned for an action plain IMAP cannot express.
//
// It is a distinct error because it is not a failure: it is a capability the
// route does not have, which internal/provider already records as CapLabels.
// A caller that gets this has offered the user something the connection cannot
// do, and the honest fix is in the catalog or the screen, not a retry.
var ErrNoIMAPEquivalent = errors.New("mailbox: action has no plain IMAP equivalent")

// IMAPOpFor translates a mutation into the IMAP operation that performs it.
//
// Note what the table decides that a per-call-site switch would get wrong.
// Marking read CLEARS nothing and sets \Seen, while marking unread clears it,
// so read state is a flag here and the absence of a label over the Gmail API:
// the two transports disagree, and this is where that disagreement is resolved.
// Archiving and trashing are moves, so they are not reversible by re-running
// the opposite flag change; their inverse is another move, back to the inbox.
// And labelling has no equivalent at all, which is a capability answer rather
// than an error to paper over.
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
	case ActionLabel, ActionUnlabel:
		return IMAPOp{}, fmt.Errorf("%w: %q", ErrNoIMAPEquivalent, m.Action)
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
	for _, f := range flags {
		if f == FlagSeen {
			return true
		}
	}
	return false
}
