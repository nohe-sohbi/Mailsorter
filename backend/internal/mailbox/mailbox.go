// Package mailbox is the provider-neutral vocabulary for changing a message,
// and the translation of that vocabulary into what each transport actually
// speaks.
//
// It exists because the same intent was spelled out forty times. Every mutating
// path in internal/api carried its own switch over action names, and each branch
// ended in gmailService.ModifyMessage with Gmail's own label ids written inline:
// "INBOX", "TRASH", "UNREAD", "STARRED", fifty-one times across the tree. That
// made every handler a place that knows Gmail, which is the thing standing
// between Mailsorter and a second provider. Worse, the spellings had drifted:
// the direct route accepted "trash", the rules engine emitted "trash" too, the
// ledger recorded "delete", and the undo table matched on both.
//
// So this package owns two things and nothing else:
//
//   - Action, the closed set of things Mailsorter does to a message, plus Parse,
//     which accepts every spelling already in circulation on the wire and in the
//     ledger. The stored vocabulary does not change: Parse absorbs it.
//   - One translation function per transport (GmailLabels today), saying what
//     that action means there. Adding IMAP means adding a function here, not
//     touching a single handler.
//
// The package is pure: no network, no client type, no Gmail import. The
// Mailbox interface below is the contract an adapter satisfies in the I/O layer,
// which is where a Gmail or IMAP client belongs.
package mailbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Action is one thing Mailsorter can do to a message. It is the internal
// vocabulary: what reaches the wire and the ledger goes through Parse.
type Action string

const (
	ActionArchive    Action = "archive"
	ActionUnarchive  Action = "unarchive"
	ActionTrash      Action = "trash"
	ActionUntrash    Action = "untrash"
	ActionMarkRead   Action = "markRead"
	ActionMarkUnread Action = "markUnread"
	ActionStar       Action = "star"
	ActionUnstar     Action = "unstar"
	ActionLabel      Action = "label"
	ActionUnlabel    Action = "unlabel"
)

// ErrUnknownAction is returned by Parse for a verb no caller should be sending.
var ErrUnknownAction = errors.New("mailbox: unknown action")

// ErrLabelRequired is returned when a label mutation carries no label. It is a
// distinct error because it is a caller bug, not a bad request: whoever built
// the mutation forgot to resolve the label id first.
var ErrLabelRequired = errors.New("mailbox: label action requires a label id")

// aliases maps every verb already in circulation onto the internal vocabulary.
//
// Two collisions are deliberate and must not be "cleaned up". "delete" and
// "trash" are the same act, spelled the first way by the API and the ledger and
// the second by the rules engine. "read" and "markRead" likewise. Both
// spellings are stored in action_log rows written over the life of the app, so
// Parse has to keep accepting them or the undo history stops resolving.
var aliases = map[string]Action{
	"archive":    ActionArchive,
	"unarchive":  ActionUnarchive,
	"trash":      ActionTrash,
	"delete":     ActionTrash,
	"untrash":    ActionUntrash,
	"restore":    ActionUntrash,
	"read":       ActionMarkRead,
	"markread":   ActionMarkRead,
	"unread":     ActionMarkUnread,
	"markunread": ActionMarkUnread,
	"star":       ActionStar,
	"unstar":     ActionUnstar,
	"label":      ActionLabel,
	"unlabel":    ActionUnlabel,
}

// Parse turns a verb from the API, the rules engine or the ledger into an
// Action. Matching is case-insensitive so "markRead" and "markread" are the
// same act, which is what already varied between call sites.
func Parse(verb string) (Action, error) {
	if a, ok := aliases[strings.ToLower(strings.TrimSpace(verb))]; ok {
		return a, nil
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownAction, verb)
}

// Destructive reports whether an action removes a message from where the user
// expects to find it. It is the question the protected-senders shield asks, and
// it belongs with the vocabulary rather than being re-derived per call site.
//
// Trashing is destructive. Archiving is destructive in the sense that matters
// here: the message leaves the inbox without the user seeing it go. Labelling,
// starring and marking read are not, which is why a protected sender's mail can
// still be labelled by a rule that would not be allowed to archive it.
func (a Action) Destructive() bool {
	return a == ActionArchive || a == ActionTrash
}

// Mutation is one action, carrying the label id when the action needs one. The
// id is resolved by the caller (the label has to exist before it can be
// applied), so this package never needs to create anything.
type Mutation struct {
	Action  Action
	LabelID string
}

// Mailbox is what the I/O layer must provide to change a message. It is
// deliberately narrow: reading is still done through the transport clients, and
// widening this contract before there is a second implementation to widen it
// for would be guessing.
type Mailbox interface {
	// Apply performs one mutation on one message. Applying to several messages
	// is a loop by the caller, matching what every call site already does.
	Apply(ctx context.Context, messageID string, m Mutation) error
}

// GmailLabelsFor translates SEVERAL mutations into one pair of label lists, so
// a compound act costs one API call rather than one per verb.
//
// Snooze is why this exists: parking a message is "label it" AND "take it out of
// the inbox", and waking it is "put it back" AND "mark it unread" AND "strip the
// label". Splitting those into separate calls would triple the Gmail traffic on
// the sweeper that runs every minute, and would leave a message visibly
// half-moved if the second call failed.
//
// A label appearing on both sides is a contradiction the caller built, and it is
// refused rather than resolved: guessing which side wins would make the outcome
// depend on argument order.
func GmailLabelsFor(muts []Mutation) (add, remove []string, err error) {
	adding := map[string]bool{}
	removing := map[string]bool{}

	for _, m := range muts {
		a, r, err := GmailLabels(m)
		if err != nil {
			return nil, nil, err
		}
		for _, label := range a {
			if !adding[label] {
				adding[label] = true
				add = append(add, label)
			}
		}
		for _, label := range r {
			if !removing[label] {
				removing[label] = true
				remove = append(remove, label)
			}
		}
	}

	for _, label := range add {
		if removing[label] {
			return nil, nil, fmt.Errorf("mailbox: label %q is both added and removed", label)
		}
	}
	return add, remove, nil
}

// GmailIsRead reads a message's state rather than changing it: Gmail marks a
// message unread by CARRYING the UNREAD label, so read is its absence. Kept here
// with the rest of the Gmail vocabulary so no handler has to know that.
func GmailIsRead(labelIDs []string) bool {
	for _, id := range labelIDs {
		if id == "UNREAD" {
			return false
		}
	}
	return true
}

// GmailAfter applies a mutation to a message's label list locally, so a caller
// that just mutated a message can keep rendering from the copy it already holds
// instead of fetching the message again to learn what it did.
//
// It is the same table as GmailLabels, read in the other direction, and it is
// here for the same reason: knowing that read state is the absence of UNREAD
// belongs in one place, including when the knowledge is used to edit a slice
// rather than to build a request.
func GmailAfter(labelIDs []string, m Mutation) []string {
	add, remove, err := GmailLabels(m)
	if err != nil {
		return labelIDs
	}

	dropped := map[string]bool{}
	for _, label := range remove {
		dropped[label] = true
	}
	out := make([]string, 0, len(labelIDs)+len(add))
	present := map[string]bool{}
	for _, label := range labelIDs {
		if dropped[label] {
			continue
		}
		present[label] = true
		out = append(out, label)
	}
	for _, label := range add {
		if !present[label] {
			out = append(out, label)
		}
	}
	return out
}

// GmailLabels translates a mutation into the label ids to add and remove
// through the Gmail API's messages.modify.
//
// This is the only place in the tree that knows Gmail's system label ids. Note
// what each one costs to get wrong: untrashing has to both restore INBOX and
// remove TRASH, or the message comes back out of the trash into nowhere; and
// read state is the absence of UNREAD, not the presence of a READ label, so
// marking read REMOVES rather than adds.
func GmailLabels(m Mutation) (add, remove []string, err error) {
	switch m.Action {
	case ActionArchive:
		return nil, []string{"INBOX"}, nil
	case ActionUnarchive:
		return []string{"INBOX"}, nil, nil
	case ActionTrash:
		return []string{"TRASH"}, nil, nil
	case ActionUntrash:
		return []string{"INBOX"}, []string{"TRASH"}, nil
	case ActionMarkRead:
		return nil, []string{"UNREAD"}, nil
	case ActionMarkUnread:
		return []string{"UNREAD"}, nil, nil
	case ActionStar:
		return []string{"STARRED"}, nil, nil
	case ActionUnstar:
		return nil, []string{"STARRED"}, nil
	case ActionLabel:
		if m.LabelID == "" {
			return nil, nil, ErrLabelRequired
		}
		return []string{m.LabelID}, nil, nil
	case ActionUnlabel:
		if m.LabelID == "" {
			return nil, nil, ErrLabelRequired
		}
		return nil, []string{m.LabelID}, nil
	}
	return nil, nil, fmt.Errorf("%w: %q", ErrUnknownAction, m.Action)
}
