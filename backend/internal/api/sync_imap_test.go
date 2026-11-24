package api

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/imap"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"go.mongodb.org/mongo-driver/bson"
)

// The write that stores a message a sync read never sets the read state or
// the star of a row already stored: those are refreshInboxState's, which
// leaves alone a change made from Mailsorter during the sync. Written by the
// upsert, a star put on while the sync ran was taken off by the listing read
// just before it, until the next sync. A new row still gets them, once.
func TestInboxUpsertWritesTheFlagsOnlyOnANewRow(t *testing.T) {
	received := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	update, err := inboxUpsert(models.Email{
		MessageID:    "42",
		UserID:       "u@example.com",
		Folder:       "INBOX",
		Subject:      "Votre facture",
		ReceivedDate: received,
		IsRead:       true,
		IsStarred:    true,
	}, false)
	if err != nil {
		t.Fatalf("inboxUpsert: %v", err)
	}

	set, ok := update["$set"].(bson.M)
	if !ok {
		t.Fatalf("$set = %#v, want a bson.M", update["$set"])
	}
	for _, field := range []string{"isRead", "isStarred"} {
		if _, there := set[field]; there {
			t.Errorf("$set carries %s: a row already stored would lose a change made during the sync", field)
		}
	}
	if set["subject"] != "Votre facture" || set["messageId"] != "42" || set["folder"] != "INBOX" {
		t.Errorf("$set = %v, want the rest of the message (subject, messageId, folder)", set)
	}
	// Neither erases what the sync read apart from the envelope.
	for _, field := range []string{"sizeEstimate", "hasAttachments", "_id"} {
		if _, there := set[field]; there {
			t.Errorf("$set carries %s, which the listing does not read: it would erase the stored one", field)
		}
	}

	onInsert, ok := update["$setOnInsert"].(bson.M)
	if !ok {
		t.Fatalf("$setOnInsert = %#v, want a bson.M", update["$setOnInsert"])
	}
	if onInsert["isRead"] != true || onInsert["isStarred"] != true {
		t.Errorf("$setOnInsert = %v, want the flags read, for a new row", onInsert)
	}
	if current, _ := update["$currentDate"].(bson.M); current["syncedAt"] != true {
		t.Errorf("$currentDate = %v, want syncedAt, which the reconciliation of a concurrent sync reads", update["$currentDate"])
	}
}

// A mail not starred and unread is stored so on a new row too: the flags are
// written as read, false included.
func TestInboxUpsertWritesFalseFlagsOnANewRow(t *testing.T) {
	update, err := inboxUpsert(models.Email{MessageID: "7"}, false)
	if err != nil {
		t.Fatal(err)
	}
	onInsert, _ := update["$setOnInsert"].(bson.M)
	if v, ok := onInsert["isRead"]; !ok || v != false {
		t.Errorf("$setOnInsert isRead = %v (present %v), want false", v, ok)
	}
	if v, ok := onInsert["isStarred"]; !ok || v != false {
		t.Errorf("$setOnInsert isStarred = %v (present %v), want false", v, ok)
	}
}

// The mark for the rules goes on a new row only: a mail the copy already
// holds is not an arrival, so writing it again from a listing never marks it.
func TestInboxUpsertMarksForTheRulesOnlyANewRow(t *testing.T) {
	update, err := inboxUpsert(models.Email{MessageID: "8"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if set, _ := update["$set"].(bson.M); set["rulesDue"] != nil {
		t.Errorf("$set carries rulesDue = %v: a row already stored would be judged again", set["rulesDue"])
	}
	if onInsert, _ := update["$setOnInsert"].(bson.M); onInsert["rulesDue"] != true {
		t.Errorf("$setOnInsert = %v, want rulesDue true on a new row", onInsert)
	}

	unmarked, err := inboxUpsert(models.Email{MessageID: "9"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if onInsert, _ := unmarked["$setOnInsert"].(bson.M); onInsert["rulesDue"] != nil {
		t.Errorf("$setOnInsert = %v, want no rulesDue for a mail that is not an arrival", onInsert)
	}
}

// Over IMAP a mail is an arrival from the later of two moments, the option
// turned on and the mailbox connected, to the second: INTERNALDATE has no
// finer, and a mail received in the second the option was turned on is one.
func TestArrivalsSince(t *testing.T) {
	on := time.Date(2026, 11, 24, 21, 0, 5, 700_000_000, time.UTC)
	connected := time.Date(2026, 11, 24, 21, 3, 1, 200_000_000, time.UTC)
	cases := []struct {
		name          string
		on, connected time.Time
		want          time.Time
	}{
		{"connected after the option", on, connected, time.Date(2026, 11, 24, 21, 3, 1, 0, time.UTC)},
		{"option after the connection", connected, on, time.Date(2026, 11, 24, 21, 3, 1, 0, time.UTC)},
		{"option turned on before it was stamped", time.Time{}, on, time.Date(2026, 11, 24, 21, 0, 5, 0, time.UTC)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := arrivalsSince(tc.on, tc.connected); !got.Equal(tc.want) {
				t.Errorf("arrivalsSince(%v, %v) = %v, want %v", tc.on, tc.connected, got, tc.want)
			}
		})
	}
}

// What a sync marks for the rules: a mail received since the option counts,
// not one the copy holds under another number, and none when the rules are
// off or the sync cannot tell a mail coming back.
func TestSyncRulesArrival(t *testing.T) {
	since := time.Date(2026, 11, 24, 21, 0, 5, 0, time.UTC)
	on := syncRules{ruleset: []models.SortingRule{{Name: "Archiver la boutique"}}, since: since}
	blind := on
	blind.markNone = true
	mail := func(uid string, at time.Time) models.Email { return models.Email{MessageID: uid, ReceivedDate: at} }
	back := map[string]bool{"7": true}

	cases := []struct {
		name  string
		rules syncRules
		mail  models.Email
		want  bool
	}{
		{"received after", on, mail("5", since.Add(3*time.Second)), true},
		{"received in the same second", on, mail("5", since), true},
		{"received before", on, mail("5", since.Add(-time.Second)), false},
		{"held by the copy under another number", on, mail("7", since.Add(time.Minute)), false},
		{"rules off", syncRules{since: since}, mail("5", since.Add(time.Minute)), false},
		{"coming back not told apart", blind, mail("5", since.Add(time.Minute)), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.rules.arrival(tc.mail, back); got != tc.want {
				t.Errorf("arrival(uid %s at %v) = %v, want %v", tc.mail.MessageID, tc.mail.ReceivedDate, got, tc.want)
			}
		})
	}
}

// What the screen is told after a sync whose rules could not judge every mail
// that arrived: nothing when they all were, one sentence per case otherwise,
// in the singular for one mail.
func TestRulesMessage(t *testing.T) {
	cases := []struct {
		pending, givenUp int
		want             string
	}{
		{0, 0, ""},
		{1, 0, "Vos règles n'ont pas pu être appliquées à 1 nouveau mail : la prochaine synchronisation réessaiera."},
		{3, 0, "Vos règles n'ont pas pu être appliquées à 3 nouveaux mails : la prochaine synchronisation réessaiera."},
		{0, 1, "1 mail n'a pas pu être rangé par vos règles : laissé dans la boîte."},
		{0, 2, "2 mails n'ont pas pu être rangés par vos règles : laissés dans la boîte."},
		{2, 1, "Vos règles n'ont pas pu être appliquées à 2 nouveaux mails : la prochaine synchronisation réessaiera. 1 mail n'a pas pu être rangé par vos règles : laissé dans la boîte."},
	}
	for _, tc := range cases {
		if got := rulesMessage(tc.pending, tc.givenUp); got != tc.want {
			t.Errorf("rulesMessage(%d, %d) = %q, want %q", tc.pending, tc.givenUp, got, tc.want)
		}
	}
}

// The reopen budget of one phase of a sync: phaseReopens openings, then
// imap.ErrReopenLimit, which ReadInHalves reads as "leave the rest for next
// time" rather than as a lost mailbox. The test handler's datastore is
// unreachable, so each opening fails, and is counted all the same.
func TestSessionReopenerStopsAtTheBudget(t *testing.T) {
	h := newTestHandler(t)
	session := &mailSession{h: h, userEmail: "u@example.com", imapc: &imap.Client{}}
	reopen := h.sessionReopener(cancelledContext(), "u@example.com", session)
	// Each phase gets a reopener of its own: one spent leaves the next whole.
	other := h.sessionReopener(cancelledContext(), "u@example.com", session)
	for i := 0; i < phaseReopens; i++ {
		if err := reopen(); err == nil || errors.Is(err, imap.ErrReopenLimit) {
			t.Fatalf("reopen %d = %v, want the failure to open the mailbox", i+1, err)
		}
	}
	if err := reopen(); !errors.Is(err, imap.ErrReopenLimit) {
		t.Errorf("reopen past the budget = %v, want imap.ErrReopenLimit", err)
	}
	if err := other(); err == nil || errors.Is(err, imap.ErrReopenLimit) {
		t.Errorf("the next phase's first reopen = %v, want an attempt (the failure to open the mailbox), not the limit", err)
	}
}

// What ReadInHalves returns becomes a typed error of the sync, never a
// success: a lost connection, or a mailbox that stopped answering.
func TestHalvesErrorIsTyped(t *testing.T) {
	lost := fmt.Errorf("%w: dial: refused", imap.ErrConnectionLost)
	if err := halvesError("u@example.com", "read", lost); !errors.Is(err, errMailboxLost) {
		t.Errorf("halvesError(lost connection) = %v, want errMailboxLost", err)
	}
	stopped := fmt.Errorf("%w: NO [UNAVAILABLE]", imap.ErrReadStopped)
	if err := halvesError("u@example.com", "read", stopped); !errors.Is(err, errMailboxUnavailable) {
		t.Errorf("halvesError(read stopped) = %v, want errMailboxUnavailable", err)
	}
}

func TestUIDLessOrdersAsNumbers(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"9", "10", true},
		{"10", "9", false},
		{"100", "99", false},
		{"42", "43", true},
		{"42", "42", false},
	}
	for _, tc := range cases {
		if got := uidLess(tc.a, tc.b); got != tc.want {
			t.Errorf("uidLess(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// Another "Annuler" of an entry already undone is the same click only within
// sameUndoWindow of the undo; a missing date or one in the future never is.
func TestRecentUndo(t *testing.T) {
	undone := time.Date(2026, 11, 24, 20, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		undoneAt time.Time
		now      time.Time
		want     bool
	}{
		{"at once", undone, undone.Add(200 * time.Millisecond), true},
		{"just inside the window", undone, undone.Add(sameUndoWindow - time.Second), true},
		{"at the end of the window", undone, undone.Add(sameUndoWindow), false},
		{"long after", undone, undone.Add(time.Hour), false},
		{"no date recorded", time.Time{}, undone, false},
		{"a date ahead of the clock", undone.Add(time.Minute), undone, false},
	}
	for _, tc := range cases {
		if got := recentUndo(tc.undoneAt, tc.now); got != tc.want {
			t.Errorf("recentUndo(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
