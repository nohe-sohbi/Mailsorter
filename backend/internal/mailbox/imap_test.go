package mailbox

import (
	"errors"
	"testing"
)

// The IMAP table, pinned action by action. These assertions are what stops the
// two transports from drifting into each other: several of them are the exact
// opposite of the Gmail row for the same verb, and a copy-paste between the two
// files would silently invert the user's mailbox.
func TestIMAPOpForTranslatesEveryAction(t *testing.T) {
	cases := []struct {
		action  Action
		labelID string
		want    IMAPOp
		why     string
	}{
		{ActionMarkRead, "", IMAPOp{Kind: IMAPFlagChange, AddFlags: []string{FlagSeen}},
			"over IMAP read is the PRESENCE of \\Seen, the opposite convention from the Gmail API"},
		{ActionMarkUnread, "", IMAPOp{Kind: IMAPFlagChange, DropFlags: []string{FlagSeen}}, ""},
		{ActionStar, "", IMAPOp{Kind: IMAPFlagChange, AddFlags: []string{FlagFlagged}}, ""},
		{ActionUnstar, "", IMAPOp{Kind: IMAPFlagChange, DropFlags: []string{FlagFlagged}}, ""},
		{ActionArchive, "", IMAPOp{Kind: IMAPMove, To: FolderArchive},
			"archiving is a MOVE here, not the removal of a label"},
		{ActionTrash, "", IMAPOp{Kind: IMAPMove, To: FolderTrash}, ""},
		{ActionUnarchive, "", IMAPOp{Kind: IMAPMove, To: FolderInbox}, ""},
		{ActionUntrash, "", IMAPOp{Kind: IMAPMove, To: FolderInbox},
			"one move back to the inbox, where the Gmail API needs both an add and a remove"},
		{ActionLabel, "Factures", IMAPOp{Kind: IMAPMove, Into: "Factures"},
			"a label is the folder of that name here: labelling files the mail there, out of the inbox"},
		{ActionUnlabel, "Factures", IMAPOp{Kind: IMAPMove, To: FolderInbox},
			"a message sits in one folder, so leaving the label's folder is going back to the inbox"},
	}

	for _, tc := range cases {
		got, err := IMAPOpFor(Mutation{Action: tc.action, LabelID: tc.labelID})
		if err != nil {
			t.Errorf("IMAPOpFor(%q) returned %v", tc.action, err)
			continue
		}
		if got.Kind != tc.want.Kind || got.To != tc.want.To || got.Into != tc.want.Into ||
			!equal(got.AddFlags, tc.want.AddFlags) || !equal(got.DropFlags, tc.want.DropFlags) {
			msg := ""
			if tc.why != "" {
				msg = ": " + tc.why
			}
			t.Errorf("IMAPOpFor(%q) = %+v, want %+v%s", tc.action, got, tc.want, msg)
		}
	}
}

// A move and a flag change are not interchangeable, and getting the kind wrong
// would mean silently doing nothing (flagging when a move was meant) or losing
// the message from view (moving when a flag was meant).
func TestIMAPOpForNeverConfusesAMoveWithAFlag(t *testing.T) {
	moves := map[Action]bool{
		ActionArchive: true, ActionTrash: true, ActionUnarchive: true, ActionUntrash: true,
		ActionLabel: true, ActionUnlabel: true,
	}
	for verb := range aliases {
		action, err := Parse(verb)
		if err != nil {
			t.Fatalf("Parse(%q): %v", verb, err)
		}
		op, err := IMAPOpFor(Mutation{Action: action, LabelID: "Factures"})
		if err != nil {
			t.Errorf("IMAPOpFor(%q): %v", action, err)
			continue
		}
		if moves[action] {
			if op.Kind != IMAPMove {
				t.Errorf("IMAPOpFor(%q).Kind = %v, want IMAPMove", action, op.Kind)
			}
			if (op.To == "") == (op.Into == "") {
				t.Errorf("IMAPOpFor(%q) = %+v, want a move with exactly one destination", action, op)
			}
			if len(op.AddFlags)+len(op.DropFlags) != 0 {
				t.Errorf("IMAPOpFor(%q) is a move that also changes flags: %+v", action, op)
			}
			continue
		}
		if op.Kind != IMAPFlagChange {
			t.Errorf("IMAPOpFor(%q).Kind = %v, want IMAPFlagChange", action, op.Kind)
		}
		if len(op.AddFlags)+len(op.DropFlags) == 0 {
			t.Errorf("IMAPOpFor(%q) changes nothing, so the action would silently no-op", action)
		}
		if op.To != "" || op.Into != "" {
			t.Errorf("IMAPOpFor(%q) is a flag change with a destination: %+v", action, op)
		}
	}
}

// Labelling names its folder, and one without a name is a caller bug, as on
// the Gmail side: filing into "" would be filing nowhere.
func TestIMAPOpForRequiresTheLabelsFolder(t *testing.T) {
	if _, err := IMAPOpFor(Mutation{Action: ActionLabel}); !errors.Is(err, ErrLabelRequired) {
		t.Errorf("IMAPOpFor(label without a name) = %v, want ErrLabelRequired", err)
	}
}

func TestIMAPOpForRejectsAnUnknownAction(t *testing.T) {
	if _, err := IMAPOpFor(Mutation{Action: "forward"}); !errors.Is(err, ErrUnknownAction) {
		t.Errorf("IMAPOpFor(forward) = %v, want ErrUnknownAction", err)
	}
}

// The compound case keeps its order, because over IMAP the operations are
// separate commands and a move changes the message's identity in the folder it
// left. Merging them the way the Gmail side does would be wrong here.
func TestIMAPOpsForKeepsOrder(t *testing.T) {
	ops, err := IMAPOpsFor([]Mutation{{Action: ActionMarkRead}, {Action: ActionArchive}})
	if err != nil {
		t.Fatalf("IMAPOpsFor: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("IMAPOpsFor returned %d ops, want 2 kept separate", len(ops))
	}
	if ops[0].Kind != IMAPFlagChange || !equal(ops[0].AddFlags, []string{FlagSeen}) {
		t.Errorf("first op = %+v, want the \\Seen flag change", ops[0])
	}
	if ops[1].Kind != IMAPMove || ops[1].To != FolderArchive {
		t.Errorf("second op = %+v, want the move to the archive folder", ops[1])
	}
}

func TestIMAPOpsForPropagatesAnError(t *testing.T) {
	if _, err := IMAPOpsFor([]Mutation{{Action: ActionArchive}, {Action: ActionLabel}}); !errors.Is(err, ErrLabelRequired) {
		t.Errorf("a label action without a name inside a compound = %v, want ErrLabelRequired", err)
	}
}

// The two transports read read-state through opposite conventions. Pinning both
// in one test is the point: this is the pair that inverts a whole inbox when
// someone copies one into the other.
func TestIMAPIsReadIsTheOppositeConventionFromGmail(t *testing.T) {
	cases := []struct {
		flags []string
		want  bool
	}{
		{[]string{FlagSeen}, true},
		{[]string{FlagSeen, FlagFlagged}, true},
		{[]string{FlagFlagged}, false},
		{[]string{}, false},
		{nil, false},
	}
	for _, tc := range cases {
		if got := IMAPIsRead(tc.flags); got != tc.want {
			t.Errorf("IMAPIsRead(%v) = %v, want %v", tc.flags, got, tc.want)
		}
	}

	// The same message, described in each transport's own terms, must read the
	// same way. An unread message carries UNREAD over the API and lacks \Seen
	// over IMAP.
	if GmailIsRead([]string{"INBOX", "UNREAD"}) != IMAPIsRead([]string{}) {
		t.Error("an unread message reads differently across the two transports")
	}
	if GmailIsRead([]string{"INBOX"}) != IMAPIsRead([]string{FlagSeen}) {
		t.Error("a read message reads differently across the two transports")
	}
}

// The star is \Flagged and nothing else: a read message is not a favorite.
func TestIMAPIsStarredReadsFlagged(t *testing.T) {
	cases := []struct {
		flags []string
		want  bool
	}{
		{[]string{FlagFlagged}, true},
		{[]string{FlagSeen, FlagFlagged}, true},
		{[]string{FlagSeen}, false},
		{[]string{}, false},
		{nil, false},
	}
	for _, tc := range cases {
		if got := IMAPIsStarred(tc.flags); got != tc.want {
			t.Errorf("IMAPIsStarred(%v) = %v, want %v", tc.flags, got, tc.want)
		}
	}
}
