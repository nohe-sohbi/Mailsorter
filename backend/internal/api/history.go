package api

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/activity"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	gmailapi "google.golang.org/api/gmail/v1"
)

// actionLogView is one row of the user-facing action history. It enriches the
// raw ledger entry with whether the action can still be reversed (it has a clean
// inverse and has not already been undone), so the UI can decide where to show
// an "Annuler" button without re-deriving the rule.
type actionLogView struct {
	models.ActionLog
	Undoable bool `json:"undoable"`
}

// GetActionLog returns the caller's most recent ledger entries (newest first),
// optionally filtered by source. This turns the append-only audit trail (every
// archive, trash, rule firing, snooze, unsubscribe) into a transparent history
// the user can actually see and act on. Each entry is flagged with whether it is
// still reversible.
func (h *Handler) GetActionLog(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	// Bounded page size: default 50, capped at 200 so a crafted query can't ask
	// the server to stream the entire ledger.
	limit := int64(50)
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := parseInt64(v); err == nil && n > 0 {
			limit = n
			if limit > 200 {
				limit = 200
			}
		}
	}

	filter := bson.M{"userId": userEmail}
	if source := r.URL.Query().Get("source"); source != "" {
		filter["source"] = source
	}
	// Cursor pagination: "everything older than the last row I have" is a stable
	// next page even while new actions land at the top. Without it the history
	// stopped dead at the most recent 100 entries.
	//
	// The cursor is (createdAt, _id), not createdAt alone. BSON stores dates to
	// the millisecond and a bulk action writes its entries back to back with no
	// I/O between them, so a page boundary landing inside such a burst would drop
	// every sibling sharing that timestamp — silently, which is the worst way to
	// lose an audit trail. The _id tiebreaker also makes the sort total, so two
	// requests cannot order equal timestamps differently.
	if before := r.URL.Query().Get("before"); before != "" {
		if ts, oid, ok := parseLogCursor(before); ok {
			filter["$and"] = []bson.M{{
				"$or": []bson.M{
					{"createdAt": bson.M{"$lt": ts}},
					{"createdAt": ts, "_id": bson.M{"$lt": oid}},
				},
			}}
		}
	}
	// Free-text search over the message identity. Anchored to the caller's own
	// entries by the userId filter above, and the needle is quoted so a stray
	// regex metacharacter can't turn into a scan the user did not ask for.
	//
	// This matches the identity stored ON the entry. Every writer now records it,
	// so anything logged from here on is searchable; entries predating that still
	// render (resolveLogIdentities fills them in below) but cannot be matched,
	// because their subject only exists after the query has run.
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		needle := bson.M{"$regex": regexp.QuoteMeta(q), "$options": "i"}
		filter["$or"] = []bson.M{{"subject": needle}, {"from": needle}}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// bson.D, not bson.M: a compound sort needs a defined key order, and a map
	// has none.
	cursor, err := h.db.ActionLog().Find(ctx, filter,
		options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}}).SetLimit(limit))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load history")
		return
	}
	defer cursor.Close(ctx)

	var logs []models.ActionLog
	if err := cursor.All(ctx, &logs); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to decode history")
		return
	}

	h.resolveLogIdentities(ctx, userEmail, logs)

	views := make([]actionLogView, 0, len(logs))
	for _, l := range logs {
		_, reversible := activity.Inverse(l.Action)
		views = append(views, actionLogView{ActionLog: l, Undoable: reversible && !l.Undone})
	}

	// A full page means there is probably more; hand back the cursor to ask for
	// it rather than making the client guess.
	var nextBefore string
	if int64(len(logs)) == limit && len(logs) > 0 {
		nextBefore = formatLogCursor(logs[len(logs)-1])
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"entries": views, "nextBefore": nextBefore})
}

// formatLogCursor encodes the (createdAt, _id) pair the next page starts after.
// Opaque to the client, which only ever echoes it back.
func formatLogCursor(entry models.ActionLog) string {
	return entry.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + entry.ID
}

// parseLogCursor decodes what formatLogCursor produced. A cursor missing its id
// half is still accepted so links minted by the previous, timestamp-only scheme
// keep working; it simply falls back to the far end of that millisecond.
func parseLogCursor(raw string) (time.Time, primitive.ObjectID, bool) {
	stamp, id, _ := strings.Cut(raw, "|")
	ts, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return time.Time{}, primitive.NilObjectID, false
	}
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		// No usable id: treat every entry in that millisecond as already seen,
		// which is exactly what the old cursor did.
		return ts, primitive.NilObjectID, true
	}
	return ts, oid, true
}

// resolveLogIdentities fills in the subject/sender of ledger entries that were
// written without them (either by a caller that did not hold the message, or
// before the ledger carried an identity at all). It costs a single indexed
// lookup for the whole page, and leaves entries whose email is no longer cached
// untouched so the UI can fall back to a neutral label.
func (h *Handler) resolveLogIdentities(ctx context.Context, userEmail string, logs []models.ActionLog) {
	missing := make([]string, 0, len(logs))
	seen := make(map[string]bool, len(logs))
	for _, l := range logs {
		if l.Subject != "" || l.From != "" || l.MessageID == "" || seen[l.MessageID] {
			continue
		}
		seen[l.MessageID] = true
		missing = append(missing, l.MessageID)
	}
	if len(missing) == 0 {
		return
	}

	cursor, err := h.db.Emails().Find(ctx, bson.M{
		"userId":    userEmail,
		"messageId": bson.M{"$in": missing},
	}, options.Find().SetProjection(bson.M{"messageId": 1, "subject": 1, "from": 1}))
	if err != nil {
		return // best-effort: the history still renders without identities
	}
	defer cursor.Close(ctx)

	var found []models.Email
	if err := cursor.All(ctx, &found); err != nil {
		return
	}
	byID := make(map[string]models.Email, len(found))
	for _, e := range found {
		byID[e.MessageID] = e
	}
	for i := range logs {
		if logs[i].Subject != "" || logs[i].From != "" {
			continue
		}
		if e, ok := byID[logs[i].MessageID]; ok {
			logs[i].Subject = e.Subject
			logs[i].From = e.From
		}
	}
}

// UndoActionRequest is the request body for POST /api/activity/undo.
type UndoActionRequest struct {
	ID string `json:"id"`
}

// UndoAction reverses a single recorded action: it looks up the ledger entry the
// caller owns, applies the inverse Gmail action (un-archive, un-trash, mark
// unread), marks the entry undone, and records the reversal in the ledger with
// the "undo" source. Only the stateful triage actions have an inverse; anything
// else is rejected. This gives the user a real safety net over everything
// Mailsorter's automation did on their behalf.
func (h *Handler) UndoAction(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	var req UndoActionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	oid, err := primitive.ObjectIDFromHex(req.ID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid entry id")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var entry models.ActionLog
	if err := h.db.ActionLog().FindOne(ctx, bson.M{"_id": oid, "userId": userEmail}).Decode(&entry); err != nil {
		writeError(w, http.StatusNotFound, "Action introuvable")
		return
	}
	if entry.Undone {
		writeError(w, http.StatusConflict, "Action déjà annulée")
		return
	}
	inverse, ok := activity.Inverse(entry.Action)
	if !ok {
		writeError(w, http.StatusBadRequest, "Cette action n'est pas réversible")
		return
	}

	gmailClient, err := h.gmailClientFor(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	if err := h.applyInverseAction(gmailClient, entry.MessageID, inverse); err != nil {
		writeError(w, http.StatusBadGateway, "Annulation impossible : "+err.Error())
		return
	}

	h.db.ActionLog().UpdateOne(ctx,
		bson.M{"_id": oid},
		bson.M{"$set": bson.M{"undone": true, "undoneAt": time.Now()}},
	)
	// Record the reversal itself so the audit trail stays truthful.
	h.logActionMeta(ctx, userEmail, entry.MessageID, inverse, SourceUndo, entry.Subject, entry.From)

	writeJSON(w, http.StatusOK, map[string]string{"status": "undone", "action": inverse})
}

// applyInverseAction maps an inverse action name onto the corresponding Gmail
// label mutation. It mirrors the undo branches of EmailAction so a reversal from
// the history behaves exactly like a manual one.
func (h *Handler) applyInverseAction(gmailClient *gmailapi.Service, messageID, inverse string) error {
	switch inverse {
	case "unarchive":
		return h.gmailService.ModifyMessage(gmailClient, messageID, []string{"INBOX"}, nil)
	case "untrash":
		return h.gmailService.ModifyMessage(gmailClient, messageID, []string{"INBOX"}, []string{"TRASH"})
	case "unread":
		return h.gmailService.ModifyMessage(gmailClient, messageID, []string{"UNREAD"}, nil)
	}
	return nil
}
