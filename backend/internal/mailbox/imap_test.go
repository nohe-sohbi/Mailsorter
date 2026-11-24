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
		action Action
		want   IMAPOp
		why    string
	}{
		{ActionMarkRead, IMAPOp{Kind: IMAPFlagChange, AddFlags: []string{FlagSeen}},
			"over IMAP read is the PRESENCE of \\Seen, the opposite convention from the Gmail API"},
		{ActionMarkUnread, IMAPOp{Kind: IMAPFlagChange, DropFlags: []string{FlagSeen}}, ""},
		{ActionStar, IMAPOp{Kind: IMAPFlagChange, AddFlags: []string{FlagFlagged}}, ""},
		{ActionUnstar, IMAPOp{Kind: IMAPFlagChange, DropFlags: []string{FlagFlagged}}, ""},
		{ActionArchive, IMAPOp{Kind: IMAPMove, To: FolderArchive},
			"archiving is a MOVE here, not the removal of a label"},
		{ActionTrash, IMAPOp{Kind: IMAPMove, To: FolderTrash}, ""},
		{ActionUnarchive, IMAPOp{Kind: IMAPMove, To: FolderInbox}, ""},
		{ActionUntrash, IMAPOp{Kind: IMAPMove, To: FolderInbox},
			"one move back to the inbox, where the Gmail API needs both an add and a remove"},
	}

	for _, tc := range cases {
		got, err := IMAPOpFor(Mutation{Action: tc.action})
		if err != nil {
			t.Errorf("IMAPOpFor(%q) returned %v", tc.action, err)
			continue
		}
		if got.Kind != tc.want.Kind || got.To != tc.want.To ||
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
	moves := map[Action]bool{ActionArchive: true, ActionTrash: true, ActionUnarchive: true, ActionUntrash: true}
	for verb := range aliases {
		action, err := Parse(verb)
		if err != nil {
			t.Fatalf("Parse(%q): %v", verb, err)
		}
		op, err := IMAPOpFor(Mutation{Action: action})
		if errors.Is(err, ErrNoIMAPEquivalent) {
			continue
		}
		if err != nil {
			t.Errorf("IMAPOpFor(%q): %v", action, err)
			continue
		}
		if moves[action] {
			if op.Kind != IMAPMove {
				t.Errorf("IMAPOpFor(%q).Kind = %v, want IMAPMove", action, op.Kind)
			}
			if op.To == "" {
				t.Errorf("IMAPOpFor(%q) is a move with no destination", action)
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
		if op.To != "" {
			t.Errorf("IMAPOpFor(%q) is a flag change with a destination: %+v", action, op)
		}
	}
}

// Labelling is not a failure here, it is a capability plain IMAP does not have.
// The distinct error is what lets a caller tell "this connection cannot" from
// "this went wrong", and internal/provider already records which routes can.
func TestIMAPOpForReportsLabellingAsUnsupported(t *testing.T) {
	for _, action := range []Action{ActionLabel, ActionUnlabel} {
		_, err := IMAPOpFor(Mutation{Action: action, LabelID: "Label_42"})
		if !errors.Is(err, ErrNoIMAPEquivalent) {
			t.Errorf("IMAPOpFor(%q) = %v, want ErrNoIMAPEquivalent", action, err)
		}
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
	if _, err := IMAPOpsFor([]Mutation{{Action: ActionArchive}, {Action: ActionLabel, LabelID: "x"}}); !errors.Is(err, ErrNoIMAPEquivalent) {
		t.Errorf("a label action inside a compound = %v, want ErrNoIMAPEquivalent", err)
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
