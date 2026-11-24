package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/activity"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
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
	// every sibling sharing that timestamp: silently, which is the worst way to
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

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
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

	imapState := h.imapUndoState(ctx, userEmail, logs)
	views := make([]actionLogView, 0, len(logs))
	for _, l := range logs {
		inverse, reversible := activity.Inverse(l.Action)
		if imapState != nil && reversible {
			switch inverse {
			case "unarchive", "untrash":
				reversible = imapState.canBringBack[l.MessageID]
			default:
				// A flag change can only be reversed on a mail still in the
				// inbox: elsewhere its number names nothing.
				reversible = imapState.inInbox[l.MessageID]
			}
		}
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

// imapUndo is what the history needs to know, on an IMAP mailbox, to offer
// "Annuler" only where it can work.
type imapUndo struct {
	// canBringBack: messages Mailsorter moved out whose stored copy says
	// where they went (a number or a Message-ID to find them by), within
	// undoWindow. Past it the sync forgets the move, and a move made before
	// Mailsorter recorded destinations was never known.
	canBringBack map[string]bool
	// inInbox: messages still in the inbox, the only ones whose flags can be
	// changed back.
	inInbox map[string]bool
}

// imapUndoState answers for these entries; nil off IMAP, where every
// reversible action stays reversible. When the store cannot answer, nothing
// is offered: offering "Annuler" on what cannot be undone is a promise broken
// on click, hiding it for one page load is not.
func (h *Handler) imapUndoState(ctx context.Context, userEmail string, logs []models.ActionLog) *imapUndo {
	none := &imapUndo{canBringBack: map[string]bool{}, inInbox: map[string]bool{}}
	transport, _, err := h.transportFor(ctx, userEmail)
	if err != nil {
		log.Printf("history: which transport %s is on: %v", userEmail, err)
		return none
	}
	if transport != provider.TransportIMAP {
		return nil
	}
	ids := make([]string, 0, len(logs))
	for _, l := range logs {
		if l.MessageID != "" {
			ids = append(ids, l.MessageID)
		}
	}
	if len(ids) == 0 {
		return none
	}
	cur, err := h.db.Emails().Find(ctx, bson.M{
		"userId":    userEmail,
		"folder":    string(mailbox.FolderInbox),
		"messageId": bson.M{"$in": ids},
	}, options.Find().SetProjection(bson.M{"messageId": 1, "headerMessageId": 1, "leftInbox": 1, "leftAt": 1, "movedTo": 1}))
	if err != nil {
		log.Printf("history: which actions of %s can be undone: %v", userEmail, err)
		return none
	}
	var rows []movedRow
	if err := cur.All(ctx, &rows); err != nil {
		log.Printf("history: which actions of %s can be undone: %v", userEmail, err)
		return none
	}
	since := time.Now().Add(-undoWindow)
	for _, r := range rows {
		switch {
		case !r.LeftInbox:
			none.inInbox[r.MessageID] = true
		case r.MovedTo.Folder != "" && (r.MovedTo.UID != "" || r.HeaderMessageID != "") && !r.LeftAt.Before(since):
			none.canBringBack[r.MessageID] = true
		}
	}
	return none
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

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	var entry models.ActionLog
	if err := h.db.ActionLog().FindOne(ctx, bson.M{"_id": oid, "userId": userEmail}).Decode(&entry); err != nil {
		writeError(w, http.StatusNotFound, "Action introuvable")
		return
	}
	if entry.Undone {
		// Undone a moment ago by another "Annuler" (two tabs, two clicks):
		// that one did what this one asks, so answer like it. Long after,
		// it is a screen showing an old line: say it was already undone.
		if !entry.UndoneAt.IsZero() && time.Since(entry.UndoneAt) < moveInFlight {
			inverse, _ := activity.Inverse(entry.Action)
			writeJSON(w, http.StatusOK, map[string]string{"status": "undone", "action": inverse})
			return
		}
		writeError(w, http.StatusConflict, "Action déjà annulée")
		return
	}
	inverse, ok := activity.Inverse(entry.Action)
	if !ok {
		writeError(w, http.StatusBadRequest, "Cette action n'est pas réversible")
		return
	}

	if transport, _, err := h.transportFor(ctx, userEmail); err == nil && transport == provider.TransportIMAP {
		// On IMAP the inverse goes through the session: a moved message is
		// fetched back from where Mailsorter put it (mirroredIMAP), by the
		// number it had in the inbox, which is what the entry recorded.
		session, err := h.openSession(ctx, userEmail)
		if err != nil {
			writeAuthError(w, err)
			return
		}
		defer session.Close()
		err = h.applyVerb(ctx, session.Mailbox(), session.RefFor(ctx, entry.MessageID), inverse, "")
		switch {
		case errors.Is(err, errNotWhereLeft):
			writeError(w, http.StatusNotFound, notWhereLeftMessage)
			return
		case errors.Is(err, errUndoExpired):
			writeError(w, http.StatusNotFound, undoExpiredMessage)
			return
		case errors.Is(err, errLeftInbox):
			writeError(w, http.StatusNotFound, leftInboxMessage)
			return
		case errors.Is(err, errUndoBusy):
			writeError(w, http.StatusLocked, undoBusyMessage)
			return
		case errors.Is(err, errAlreadyBack):
			// Another "Annuler" brought it back a moment ago, and marks the
			// entry: nothing more to do or to record.
			writeJSON(w, http.StatusOK, map[string]string{"status": "undone", "action": inverse})
			return
		case errors.Is(err, mailbox.ErrNoIMAPEquivalent):
			writeTransportError(w)
			return
		case err != nil:
			writeError(w, http.StatusBadGateway, "Annulation impossible : "+err.Error())
			return
		}
	} else {
		gmailClient, err := h.gmailClientFor(ctx, userEmail)
		if err != nil {
			writeAuthError(w, err)
			return
		}
		if err := h.applyInverseAction(gmailClient, entry.MessageID, inverse); err != nil {
			writeError(w, http.StatusBadGateway, "Annulation impossible : "+err.Error())
			return
		}
	}

	if _, err := h.db.ActionLog().UpdateOne(ctx,
		bson.M{"_id": oid},
		bson.M{"$set": bson.M{"undone": true, "undoneAt": time.Now()}},
	); err != nil {
		log.Printf("history: entry %s of %s undone but not marked: %v", oid.Hex(), userEmail, err)
	}
	// Record the reversal itself so the audit trail stays truthful.
	h.logActionMeta(ctx, userEmail, entry.MessageID, inverse, SourceUndo, entry.Subject, entry.From)

	writeJSON(w, http.StatusOK, map[string]string{"status": "undone", "action": inverse})
}

// applyInverseAction maps an inverse action name onto the corresponding Gmail
// label mutation. It mirrors the undo branches of EmailAction so a reversal from
// the history behaves exactly like a manual one.
func (h *Handler) applyInverseAction(gmailClient *gmailapi.Service, messageID, inverse string) error {
	return h.applyInverseActionWithLabel(gmailClient, messageID, inverse, "")
}

// applyInverseActionWithLabel is applyInverseAction for the one reversal that
// takes an argument: removing a label needs its Gmail id, which the caller
// resolves once per batch rather than per message. An "unlabel" with no id is a
// no-op rather than a silent full-label wipe.
func (h *Handler) applyInverseActionWithLabel(gmailClient *gmailapi.Service, messageID, inverse, labelID string) error {
	// The inverse vocabulary comes from activity.Inverse, which only ever emits
	// verbs this understands. An unlabel with nothing to remove is the one case
	// worth naming: it means the ledger entry did not record which label was
	// applied, so there is nothing to undo.
	if inverse == "unlabel" && labelID == "" {
		return fmt.Errorf("aucun libellé à retirer")
	}
	err := h.applyVerb(context.Background(), h.mailboxOf(gmailClient), mailbox.OnAccount(messageID), inverse, labelID)
	if errors.Is(err, mailbox.ErrUnknownAction) {
		return nil
	}
	return err
}
