package api

import (
	"testing"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// The history cursor is (createdAt, _id). A timestamp alone loses every entry
// sharing the millisecond at a page boundary — and a bulk action writes its
// entries back to back with no I/O between them, so those collisions are the
// normal case, not an exotic one. Losing audit-trail rows silently is the worst
// failure mode this endpoint has, hence the round-trip test.
func TestLogCursorRoundTrip(t *testing.T) {
	oid := primitive.NewObjectID()
	created := time.Date(2026, 8, 1, 14, 32, 5, 123456789, time.UTC)

	raw := formatLogCursor(models.ActionLog{ID: oid.Hex(), CreatedAt: created})

	ts, gotOID, ok := parseLogCursor(raw)
	if !ok {
		t.Fatalf("parseLogCursor(%q) reported failure", raw)
	}
	if !ts.Equal(created) {
		t.Errorf("timestamp = %v, want %v", ts, created)
	}
	if gotOID != oid {
		t.Errorf("object id = %v, want %v", gotOID, oid)
	}
}

// A non-UTC timestamp must still come back as the same instant: the cursor is
// compared against BSON dates, which have no zone.
func TestLogCursorNormalizesZone(t *testing.T) {
	zone := time.FixedZone("CEST", 2*3600)
	created := time.Date(2026, 8, 1, 16, 32, 5, 0, zone)
	oid := primitive.NewObjectID()

	ts, _, ok := parseLogCursor(formatLogCursor(models.ActionLog{ID: oid.Hex(), CreatedAt: created}))
	if !ok {
		t.Fatal("parseLogCursor reported failure")
	}
	if !ts.Equal(created) {
		t.Errorf("timestamp = %v, want the same instant as %v", ts, created)
	}
}

func TestParseLogCursorRejectsGarbage(t *testing.T) {
	for _, raw := range []string{"", "pas-une-date", "|", "hier|" + primitive.NewObjectID().Hex()} {
		if _, _, ok := parseLogCursor(raw); ok {
			t.Errorf("parseLogCursor(%q) accepted an unusable cursor", raw)
		}
	}
}

// Cursors minted by the earlier timestamp-only scheme are still in flight in
// clients when a deploy lands; they must keep paging rather than 400 or reset to
// the top of the ledger.
func TestParseLogCursorAcceptsLegacyTimestampOnly(t *testing.T) {
	created := time.Date(2026, 8, 1, 14, 32, 5, 0, time.UTC)

	ts, oid, ok := parseLogCursor(created.Format(time.RFC3339Nano))
	if !ok {
		t.Fatal("a legacy timestamp-only cursor must still be accepted")
	}
	if !ts.Equal(created) {
		t.Errorf("timestamp = %v, want %v", ts, created)
	}
	// No id half: the whole millisecond is treated as already seen, which is
	// exactly what the old cursor did.
	if oid != primitive.NilObjectID {
		t.Errorf("object id = %v, want the nil id", oid)
	}
}
