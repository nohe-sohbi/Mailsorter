package mailbox

import (
	"errors"
	"testing"
)

// Every spelling in circulation has to keep resolving. Two of them are the same
// act under two names, and both are already written into action_log rows: if
// Parse stopped accepting one, the undo history would silently stop resolving
// for every entry written the other way.
func TestParseAcceptsEverySpellingInCirculation(t *testing.T) {
	cases := map[string]Action{
		// The direct action route and the batch route.
		"archive":   ActionArchive,
		"unarchive": ActionUnarchive,
		"delete":    ActionTrash,
		"untrash":   ActionUntrash,
		"read":      ActionMarkRead,
		"unread":    ActionMarkUnread,
		"star":      ActionStar,
		"unstar":    ActionUnstar,
		"label":     ActionLabel,
		// The rules engine spells two of them differently.
		"trash":    ActionTrash,
		"markRead": ActionMarkRead,
		// The undo table.
		"unlabel": ActionUnlabel,
		// Case and padding vary between call sites.
		"  Archive  ": ActionArchive,
		"MARKREAD":    ActionMarkRead,
	}
	for verb, want := range cases {
		got, err := Parse(verb)
		if err != nil {
			t.Errorf("Parse(%q) returned %v, want %q", verb, err, want)
			continue
		}
		if got != want {
			t.Errorf("Parse(%q) = %q, want %q", verb, got, want)
		}
	}
}

func TestParseRejectsAnythingElse(t *testing.T) {
	for _, verb := range []string{"", "   ", "archiv", "supprimer", "forward", "markread!"} {
		if got, err := Parse(verb); !errors.Is(err, ErrUnknownAction) {
			t.Errorf("Parse(%q) = %q, %v; want ErrUnknownAction", verb, got, err)
		}
	}
}

// The protected-senders shield asks exactly this question, so the answer lives
// with the vocabulary rather than being re-derived per call site.
func TestDestructiveMatchesTheProtectionRule(t *testing.T) {
	destructive := map[Action]bool{
		ActionArchive:    true,
		ActionTrash:      true,
		ActionUnarchive:  false,
		ActionUntrash:    false,
		ActionMarkRead:   false,
		ActionMarkUnread: false,
		ActionStar:       false,
		ActionUnstar:     false,
		ActionLabel:      false,
		ActionUnlabel:    false,
	}
	for action, want := range destructive {
		if got := action.Destructive(); got != want {
			t.Errorf("%q.Destructive() = %v, want %v", action, got, want)
		}
	}
}

// The translation table, asserted against what the forty call sites did before
// this package existed. A regression here changes what the app does to real
// mailboxes, so every action is pinned, not a sample.
func TestGmailLabelsMatchesTheFormerCallSites(t *testing.T) {
	cases := []struct {
		action     Action
		labelID    string
		wantAdd    []string
		wantRemove []string
		why        string
	}{
		{ActionArchive, "", nil, []string{"INBOX"}, "archiving is removing INBOX, not adding anything"},
		{ActionUnarchive, "", []string{"INBOX"}, nil, ""},
		{ActionTrash, "", []string{"TRASH"}, nil, ""},
		{ActionUntrash, "", []string{"INBOX"}, []string{"TRASH"},
			"untrashing must do BOTH: restore INBOX and drop TRASH, or the message leaves the trash for nowhere"},
		{ActionMarkRead, "", nil, []string{"UNREAD"},
			"read state is the ABSENCE of UNREAD, so marking read removes rather than adds"},
		{ActionMarkUnread, "", []string{"UNREAD"}, nil, ""},
		{ActionStar, "", []string{"STARRED"}, nil, ""},
		{ActionUnstar, "", nil, []string{"STARRED"}, ""},
		{ActionLabel, "Label_42", []string{"Label_42"}, nil, ""},
		{ActionUnlabel, "Label_42", nil, []string{"Label_42"}, ""},
	}

	for _, tc := range cases {
		add, remove, err := GmailLabels(Mutation{Action: tc.action, LabelID: tc.labelID})
		if err != nil {
			t.Errorf("GmailLabels(%q) returned %v", tc.action, err)
			continue
		}
		if !equal(add, tc.wantAdd) || !equal(remove, tc.wantRemove) {
			msg := ""
			if tc.why != "" {
				msg = ": " + tc.why
			}
			t.Errorf("GmailLabels(%q) = add %v remove %v, want add %v remove %v%s",
				tc.action, add, remove, tc.wantAdd, tc.wantRemove, msg)
		}
	}
}

// A label mutation with no id would reach Gmail as a modify with an empty label,
// which is a caller bug. It must be refused here rather than sent.
func TestGmailLabelsRefusesALabellessLabelAction(t *testing.T) {
	for _, action := range []Action{ActionLabel, ActionUnlabel} {
		if _, _, err := GmailLabels(Mutation{Action: action}); !errors.Is(err, ErrLabelRequired) {
			t.Errorf("GmailLabels(%q) with no label id = %v, want ErrLabelRequired", action, err)
		}
	}
}

func TestGmailLabelsRejectsAnUnknownAction(t *testing.T) {
	if _, _, err := GmailLabels(Mutation{Action: "forward"}); !errors.Is(err, ErrUnknownAction) {
		t.Errorf("GmailLabels(forward) = %v, want ErrUnknownAction", err)
	}
}

// Every action Parse can produce must have a translation, or a verb the API
// accepts would resolve and then fail at the transport with no useful message.
func TestEveryParsedActionTranslates(t *testing.T) {
	for verb := range aliases {
		action, err := Parse(verb)
		if err != nil {
			t.Fatalf("Parse(%q): %v", verb, err)
		}
		mutation := Mutation{Action: action}
		if action == ActionLabel || action == ActionUnlabel {
			mutation.LabelID = "Label_1"
		}
		add, remove, err := GmailLabels(mutation)
		if err != nil {
			t.Errorf("GmailLabels(%q), reached from the verb %q, returned %v", action, verb, err)
			continue
		}
		if len(add) == 0 && len(remove) == 0 {
			t.Errorf("GmailLabels(%q) changes nothing, so the action would silently no-op", action)
		}
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Snooze is the reason compound mutations exist, so its two real cases are the
// ones pinned: they must each collapse into ONE pair of lists.
func TestGmailLabelsForComposesSnooze(t *testing.T) {
	const snoozeLabel = "Label_snooze"

	// Parking: label it and take it out of the inbox.
	add, remove, err := GmailLabelsFor([]Mutation{
		{Action: ActionLabel, LabelID: snoozeLabel},
		{Action: ActionArchive},
	})
	if err != nil {
		t.Fatalf("parking: %v", err)
	}
	if !equal(add, []string{snoozeLabel}) || !equal(remove, []string{"INBOX"}) {
		t.Errorf("parking = add %v remove %v, want add [%s] remove [INBOX]", add, remove, snoozeLabel)
	}

	// Waking: back to the inbox, unread, and the snooze label stripped.
	add, remove, err = GmailLabelsFor([]Mutation{
		{Action: ActionUnarchive},
		{Action: ActionMarkUnread},
		{Action: ActionUnlabel, LabelID: snoozeLabel},
	})
	if err != nil {
		t.Fatalf("waking: %v", err)
	}
	if !equal(add, []string{"INBOX", "UNREAD"}) || !equal(remove, []string{snoozeLabel}) {
		t.Errorf("waking = add %v remove %v, want add [INBOX UNREAD] remove [%s]", add, remove, snoozeLabel)
	}
}

// A label on both sides is a contradiction the caller built. Resolving it would
// make the outcome depend on argument order, so it is refused.
func TestGmailLabelsForRefusesAContradiction(t *testing.T) {
	_, _, err := GmailLabelsFor([]Mutation{{Action: ActionArchive}, {Action: ActionUnarchive}})
	if err == nil {
		t.Error("archive plus unarchive was accepted; want an error naming the contradicted label")
	}
}

// Repeating a verb must not repeat the label: Gmail would accept it, but the
// duplicate is a sign the caller built the list twice.
func TestGmailLabelsForDeduplicates(t *testing.T) {
	add, _, err := GmailLabelsFor([]Mutation{{Action: ActionStar}, {Action: ActionStar}})
	if err != nil {
		t.Fatalf("GmailLabelsFor: %v", err)
	}
	if !equal(add, []string{"STARRED"}) {
		t.Errorf("add = %v, want [STARRED] once", add)
	}
}

func TestGmailLabelsForPropagatesAnError(t *testing.T) {
	if _, _, err := GmailLabelsFor([]Mutation{{Action: ActionArchive}, {Action: ActionLabel}}); !errors.Is(err, ErrLabelRequired) {
		t.Errorf("a labelless label action inside a compound = %v, want ErrLabelRequired", err)
	}
}

// Read state is the ABSENCE of UNREAD. Getting this backwards would show every
// unread message as read across the whole inbox.
func TestGmailIsRead(t *testing.T) {
	cases := []struct {
		labels []string
		want   bool
	}{
		{[]string{"INBOX", "UNREAD"}, false},
		{[]string{"INBOX"}, true},
		{[]string{}, true},
		{nil, true},
		{[]string{"UNREAD"}, false},
		{[]string{"INBOX", "STARRED", "Label_1"}, true},
	}
	for _, tc := range cases {
		if got := GmailIsRead(tc.labels); got != tc.want {
			t.Errorf("GmailIsRead(%v) = %v, want %v", tc.labels, got, tc.want)
		}
	}
}

// GmailAfter is the translation table read backwards, so it has to agree with
// GmailLabels on every action: whatever a mutation adds must appear, whatever it
// removes must be gone, and nothing else may move.
func TestGmailAfterAgreesWithGmailLabels(t *testing.T) {
	start := []string{"INBOX", "UNREAD", "Label_1"}

	cases := map[Action][]string{
		ActionMarkRead:  {"INBOX", "Label_1"},
		ActionArchive:   {"UNREAD", "Label_1"},
		ActionStar:      {"INBOX", "UNREAD", "Label_1", "STARRED"},
		ActionTrash:     {"INBOX", "UNREAD", "Label_1", "TRASH"},
		ActionUnarchive: {"INBOX", "UNREAD", "Label_1"}, // already there, not duplicated
	}
	for action, want := range cases {
		if got := GmailAfter(start, Mutation{Action: action}); !equal(got, want) {
			t.Errorf("GmailAfter(%v, %q) = %v, want %v", start, action, got, want)
		}
	}

	// And the property that matters: after marking read, the list reads as read.
	if !GmailIsRead(GmailAfter(start, Mutation{Action: ActionMarkRead})) {
		t.Error("a list that just had markRead applied still reads as unread")
	}
}

// An action it cannot translate must leave the list untouched rather than
// silently dropping labels.
func TestGmailAfterIgnoresAnUnknownAction(t *testing.T) {
	start := []string{"INBOX", "UNREAD"}
	if got := GmailAfter(start, Mutation{Action: "forward"}); !equal(got, start) {
		t.Errorf("GmailAfter with an unknown action = %v, want the list unchanged %v", got, start)
	}
}
