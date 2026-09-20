package api

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/search"
	"go.mongodb.org/mongo-driver/bson"
)

var mirrorNow = time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)

func filterFor(query string) bson.M {
	crit, _ := search.Parse(query)
	return mirrorFilter("u@example.com", crit, mirrorNow)
}

// The query the SPA sends is the contract; this is what it becomes.
func TestMirrorFilterTranslatesTheQueriesTheAppSends(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  bson.M
	}{
		{
			name:  "the default query",
			query: "in:inbox",
			want:  bson.M{"userId": "u@example.com", "folder": "INBOX"},
		},
		{
			name:  "the unread chip",
			query: "in:inbox is:unread",
			want:  bson.M{"userId": "u@example.com", "folder": "INBOX", "isRead": false},
		},
		{
			name:  "the today chip",
			query: "in:inbox newer_than:1d",
			want: bson.M{
				"userId":       "u@example.com",
				"folder":       "INBOX",
				"receivedDate": bson.M{"$gte": mirrorNow.Add(-24 * time.Hour)},
			},
		},
		{
			name:  "a sender",
			query: "from:linkedin.com",
			want: bson.M{
				"userId": "u@example.com",
				"from":   bson.M{"$regex": "linkedin\\.com", "$options": "i"},
			},
		},
		{
			name:  "no query at all still scopes to the user",
			query: "",
			want:  bson.M{"userId": "u@example.com"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filterFor(tc.query); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("filter for %q =\n  %v\nwant\n  %v", tc.query, got, tc.want)
			}
		})
	}
}

// The one that is not a matter of taste. A query is free text a user typed, and
// it goes into a regular expression. Unescaped, "(" is a syntax error that
// fails the whole listing, and "." silently matches any character, so a search
// for "a.com" would return "axcom" and the user would never know why.
func TestMirrorFilterEscapesWhatTheUserTyped(t *testing.T) {
	got := filterFor("from:a.b(c")

	from, ok := got["from"].(bson.M)
	if !ok {
		t.Fatalf("from clause = %#v, want a bson.M", got["from"])
	}
	pattern, _ := from["$regex"].(string)
	if strings.Contains(pattern, "(") && !strings.Contains(pattern, "\\(") {
		t.Errorf("$regex = %q, want the parenthesis escaped", pattern)
	}
	if strings.Contains(pattern, ".") && !strings.Contains(pattern, "\\.") {
		t.Errorf("$regex = %q, want the dot escaped: it would match any character", pattern)
	}

	// And the whole thing is still a substring match, not an anchored one.
	if strings.HasPrefix(pattern, "^") || strings.HasSuffix(pattern, "$") {
		t.Errorf("$regex = %q, want an unanchored substring match", pattern)
	}
}

// Two words narrow. Gmail reads a query as a conjunction, so a listing that
// ORed them would return more results the more precisely the user searched.
func TestMirrorFilterAndsTheBareWords(t *testing.T) {
	got := filterFor("facture urgente")

	clauses, ok := got["$and"].([]bson.M)
	if !ok {
		t.Fatalf("$and = %#v, want a slice of clauses", got["$and"])
	}
	if len(clauses) != 2 {
		t.Fatalf("%d clause(s) for two words, want 2", len(clauses))
	}
	// Each word may match in any of the three places a human would look.
	for i, clause := range clauses {
		or, ok := clause["$or"].([]bson.M)
		if !ok || len(or) != 3 {
			t.Errorf("clause %d = %#v, want an $or over subject, from and snippet", i, clause)
		}
	}
}

// A window that is open at both ends is one clause, not two: writing
// receivedDate twice would leave only the second bound.
func TestMirrorFilterKeepsBothEndsOfADateWindow(t *testing.T) {
	got := filterFor("newer_than:7d older_than:1d")

	bounds, ok := got["receivedDate"].(bson.M)
	if !ok {
		t.Fatalf("receivedDate = %#v, want a bson.M", got["receivedDate"])
	}
	if bounds["$gte"] != mirrorNow.Add(-7*24*time.Hour) {
		t.Errorf("$gte = %v, want 7 days ago", bounds["$gte"])
	}
	if bounds["$lte"] != mirrorNow.Add(-24*time.Hour) {
		t.Errorf("$lte = %v, want 1 day ago", bounds["$lte"])
	}
}

// A filter that cannot be answered is refused, and refused BEFORE the
// datastore: the check is on the query, not on what comes back.
//
// Running it without the term instead would answer a full inbox under a button
// labelled "Pieces jointes", and nothing on screen would say the filter did
// nothing.
func TestListFromMirrorRefusesAQueryItCannotAnswer(t *testing.T) {
	h := newTestHandler(t)

	page, err := h.listFromMirror(cancelledContext(), "u@example.com", "in:inbox has:attachment", 50, "")
	if err == nil {
		t.Fatal("a query with an unanswerable term returned a page instead of an error")
	}
	if page != nil {
		t.Error("listFromMirror returned both an error and a page")
	}

	var unsupported errUnsupportedQuery
	if !errors.As(err, &unsupported) {
		t.Fatalf("err = %v, want an errUnsupportedQuery so the handler can answer 501", err)
	}
	if !reflect.DeepEqual(unsupported.terms, []string{"has:attachment"}) {
		t.Errorf("terms = %v, want [has:attachment] so the message can name it", unsupported.terms)
	}
}

// The failure the inbox screen is built to show. A datastore that cannot be
// read is NOT an empty mailbox: returning an empty page here would render as
// "Inbox Zero atteint", which is the opposite of the truth and gives the user
// nothing to retry.
func TestListFromMirrorDoesNotReportAnUnreachableDatastoreAsAnEmptyInbox(t *testing.T) {
	h := newTestHandler(t)

	page, err := h.listFromMirror(cancelledContext(), "u@example.com", "in:inbox", 50, "")
	if err == nil {
		t.Fatalf("an unreachable datastore produced a page: %v", page)
	}
}

// Same for the counters: zero unread is a state a user celebrates, so it must
// never be what a failed count looks like.
func TestStatsFromMirrorDoesNotReportAnUnreachableDatastoreAsZero(t *testing.T) {
	h := newTestHandler(t)

	stats, err := h.statsFromMirror(cancelledContext(), "u@example.com")
	if err == nil {
		t.Fatalf("an unreachable datastore produced stats: %+v", stats)
	}
	if stats != nil {
		t.Error("statsFromMirror returned both an error and a value")
	}
}

func TestParseOffsetToken(t *testing.T) {
	cases := []struct {
		token string
		want  int64
		ok    bool
	}{
		{"", 0, true},
		{"0", 0, true},
		{"50", 50, true},
		{"-1", 0, false},
		{"abc", 0, false},
		// A Gmail page token, which is what a client holds after switching
		// transports. Reading it as zero would silently send the user back to
		// page one, which looks like the list forgot where they were.
		{"09876543210987654321-ABC", 0, false},
	}
	for _, tc := range cases {
		got, err := parseOffsetToken(tc.token)
		if (err == nil) != tc.ok {
			t.Errorf("parseOffsetToken(%q) error = %v, want ok=%v", tc.token, err, tc.ok)
		}
		if got != tc.want {
			t.Errorf("parseOffsetToken(%q) = %d, want %d", tc.token, got, tc.want)
		}
	}
}

func TestListFromMirrorRefusesAForeignPageToken(t *testing.T) {
	h := newTestHandler(t)

	if _, err := h.listFromMirror(cancelledContext(), "u@example.com", "in:inbox", 50, "not-an-offset"); err == nil {
		t.Error("listFromMirror accepted a page token it never handed out")
	}
}
