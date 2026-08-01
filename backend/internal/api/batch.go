package api

import (
	"context"
	"net/http"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"go.mongodb.org/mongo-driver/bson"
)

// maxBatchActionSize bounds one batch so a crafted request can't make the server
// hold a Gmail connection open for thousands of sequential mutations. The inbox
// page tops out at 100 messages, so this covers "select all" with room to spare.
const maxBatchActionSize = 200

// BatchActionRequest is the body of POST /api/emails/batch-action.
type BatchActionRequest struct {
	MessageIDs []string `json:"messageIds"`
	Action     string   `json:"action"`
	LabelName  string   `json:"labelName,omitempty"`
}

// batchActionResult reports one message's outcome so the client can tell the
// difference between "done", "shielded by your protected list" and "failed".
type batchActionResult struct {
	Applied     []string `json:"applied"`
	Failed      int      `json:"failed"`
	Protected   int      `json:"protectedSkipped"`
	Total       int      `json:"total"`
	LabelName   string   `json:"labelName,omitempty"`
	Reversible  bool     `json:"reversible"`
	InverseName string   `json:"inverse,omitempty"`
}

// batchInverse maps a batch action to the action that undoes it. Labelling and
// marking-as-read are not offered as reversible here: un-labelling needs the
// label id the caller no longer has, and "unread" is rarely what the user means.
var batchInverse = map[string]string{
	"archive": "unarchive",
	"delete":  "untrash",
}

// BatchAction applies one triage action to a list of messages in a single
// request. Selecting emails in the inbox used to only scope AI analysis: there
// was no way to archive, trash, label or mark the selection as read without
// going through the model. This is the deterministic, quota-free path.
//
// Protected senders are shielded from destructive actions exactly as they are
// in the bulk-by-sender and rules paths; non-destructive actions still run on
// them. Failures are counted rather than aborting the batch, so one bad message
// never costs the user the other 99.
func (h *Handler) BatchAction(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	var req BatchActionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.MessageIDs) == 0 {
		writeError(w, http.StatusBadRequest, "Aucun email sélectionné")
		return
	}
	if len(req.MessageIDs) > maxBatchActionSize {
		writeError(w, http.StatusBadRequest, "Trop d'emails sélectionnés en une fois")
		return
	}
	switch req.Action {
	case "archive", "delete", "read", "unread", "star", "unstar":
	case "label":
		if req.LabelName == "" {
			writeError(w, http.StatusBadRequest, "Nom du libellé requis")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "Action non supportée")
		return
	}

	// Gmail mutations are sequential and network-bound; budget generously but
	// stay under any sane proxy timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	gmailClient, err := h.gmailClientFor(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	var labelID string
	if req.Action == "label" {
		labelID, err = h.ensureLabel(ctx, gmailClient, userEmail, req.LabelName)
		if err != nil {
			writeError(w, http.StatusBadGateway, "Création du libellé impossible : "+err.Error())
			return
		}
	}

	// One lookup for the whole batch: the protected check needs each sender, and
	// the ledger needs the subject/sender to say which email was acted on.
	identities := h.emailIdentities(ctx, userEmail, req.MessageIDs)
	protectedList := h.protectedValues(ctx, userEmail)

	res := batchActionResult{
		Applied:     make([]string, 0, len(req.MessageIDs)),
		Total:       len(req.MessageIDs),
		LabelName:   req.LabelName,
		InverseName: batchInverse[req.Action],
	}
	res.Reversible = res.InverseName != ""

	for _, id := range req.MessageIDs {
		if id == "" {
			res.Failed++
			continue
		}
		meta := identities[id]
		// An unknown sender ("" from) is treated as unprotected, matching the
		// bulk-by-sender path: protection is an explicit allow-list on a known
		// address, never a default-deny on missing metadata.
		if !allows(req.Action, meta.From, protectedList) {
			res.Protected++
			continue
		}

		var applyErr error
		switch req.Action {
		case "archive":
			applyErr = h.gmailService.ModifyMessage(gmailClient, id, nil, []string{"INBOX"})
		case "delete":
			applyErr = h.gmailService.ModifyMessage(gmailClient, id, []string{"TRASH"}, nil)
		case "read":
			applyErr = h.gmailService.ModifyMessage(gmailClient, id, nil, []string{"UNREAD"})
		case "unread":
			applyErr = h.gmailService.ModifyMessage(gmailClient, id, []string{"UNREAD"}, nil)
		case "star":
			applyErr = h.gmailService.ModifyMessage(gmailClient, id, []string{"STARRED"}, nil)
		case "unstar":
			applyErr = h.gmailService.ModifyMessage(gmailClient, id, nil, []string{"STARRED"})
		case "label":
			applyErr = h.gmailService.ModifyMessage(gmailClient, id, []string{labelID}, nil)
		}
		if applyErr != nil {
			res.Failed++
			continue
		}

		res.Applied = append(res.Applied, id)
		h.logActionMeta(ctx, userEmail, id, req.Action, SourceDirect, meta.Subject, meta.From)
	}

	writeJSON(w, http.StatusOK, res)
}

// BatchUndo reverses a batch that was just applied. It is deliberately separate
// from the per-entry history undo: the client holds the exact ids it changed, so
// undoing costs one request instead of N ledger round-trips, which is what makes
// a real "Annuler" affordance on a 100-email action possible.
func (h *Handler) BatchUndo(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	var req BatchActionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	inverse, ok := batchInverse[req.Action]
	if !ok {
		writeError(w, http.StatusBadRequest, "Cette action n'est pas réversible")
		return
	}
	if len(req.MessageIDs) == 0 {
		writeError(w, http.StatusBadRequest, "Aucun email à restaurer")
		return
	}
	if len(req.MessageIDs) > maxBatchActionSize {
		writeError(w, http.StatusBadRequest, "Trop d'emails à restaurer en une fois")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	gmailClient, err := h.gmailClientFor(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	restored := 0
	for _, id := range req.MessageIDs {
		if id == "" {
			continue
		}
		if err := h.applyInverseAction(gmailClient, id, inverse); err != nil {
			continue
		}
		restored++
		// Mark the forward entries undone so the history does not offer a second
		// "Annuler" on work that has already been reversed.
		h.db.ActionLog().UpdateMany(ctx,
			bson.M{"userId": userEmail, "messageId": id, "action": req.Action, "undone": bson.M{"$ne": true}},
			bson.M{"$set": bson.M{"undone": true, "undoneAt": time.Now()}},
		)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"restored": restored, "total": len(req.MessageIDs)})
}

// emailIdentities resolves the subject/sender of a set of messages in one
// indexed query. Messages absent from the local cache simply yield a zero value,
// which every caller treats as "unknown but not protected".
func (h *Handler) emailIdentities(ctx context.Context, userEmail string, messageIDs []string) map[string]models.Email {
	out := make(map[string]models.Email, len(messageIDs))
	if len(messageIDs) == 0 {
		return out
	}
	cursor, err := h.db.Emails().Find(ctx, bson.M{
		"userId":    userEmail,
		"messageId": bson.M{"$in": messageIDs},
	})
	if err != nil {
		return out
	}
	defer cursor.Close(ctx)

	var rows []models.Email
	if err := cursor.All(ctx, &rows); err != nil {
		return out
	}
	for _, e := range rows {
		out[e.MessageID] = e
	}
	return out
}
