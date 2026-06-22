package api

import (
	"context"
	"net/http"
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
// optionally filtered by source. This turns the append-only audit trail — every
// archive, trash, rule firing, snooze, unsubscribe — into a transparent history
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cursor, err := h.db.ActionLog().Find(ctx, filter,
		options.Find().SetSort(bson.M{"createdAt": -1}).SetLimit(limit))
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

	views := make([]actionLogView, 0, len(logs))
	for _, l := range logs {
		_, reversible := activity.Inverse(l.Action)
		views = append(views, actionLogView{ActionLog: l, Undoable: reversible && !l.Undone})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"entries": views})
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
		writeError(w, http.StatusInternalServerError, "Failed to get user credentials")
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
	h.logAction(ctx, userEmail, entry.MessageID, inverse, SourceUndo)

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
