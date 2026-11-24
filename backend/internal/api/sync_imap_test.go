package api

import (
	"errors"
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
	})
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
	update, err := inboxUpsert(models.Email{MessageID: "7"})
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

// The reopen budget of one sync: syncReopens openings, then
// imap.ErrReopenLimit, which ReadInHalves reads as "leave the rest for next
// time" rather than as a lost mailbox. The test handler's datastore is
// unreachable, so each opening fails, and is counted all the same.
func TestSessionReopenerStopsAtTheBudget(t *testing.T) {
	h := newTestHandler(t)
	session := &mailSession{h: h, userEmail: "u@example.com", imapc: &imap.Client{}}
	reopen := h.sessionReopener(cancelledContext(), "u@example.com", session)
	for i := 0; i < syncReopens; i++ {
		if err := reopen(); err == nil || errors.Is(err, imap.ErrReopenLimit) {
			t.Fatalf("reopen %d = %v, want the failure to open the mailbox", i+1, err)
		}
	}
	if err := reopen(); !errors.Is(err, imap.ErrReopenLimit) {
		t.Errorf("reopen past the budget = %v, want imap.ErrReopenLimit", err)
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
