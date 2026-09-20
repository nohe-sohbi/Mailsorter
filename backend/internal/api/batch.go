package api

import (
	"context"
	"net/http"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/gmail"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/protect"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	gmailapi "google.golang.org/api/gmail/v1"
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

// batchInverse maps a batch action to the action that undoes it.
//
// Un-labelling needs a Gmail label id, which the client does not have. It has
// the label NAME though (it is what it sent to apply it), and the server can
// resolve one to the other, so labelling is reversible after all: this is the
// bulk action a user is most likely to regret, since it is the only one that
// leaves no visible trace in the inbox to undo by hand.
//
// Marking as read stays one-way on purpose: "unread" is rarely what the user
// means by undoing a triage pass.
var batchInverse = map[string]string{
	"archive": "unarchive",
	"delete":  "untrash",
	"label":   "unlabel",
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
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
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
	//
	// The local mailbox is only written by syncInbox, while the list the user
	// selects from is served live from Gmail, so a message can perfectly well
	// be on screen and absent here. That gap used to silently defeat the
	// protected-sender shield: an unknown sender reads as "not protected", and a
	// VIP's mail would be trashed by a bulk action the moment a sync had not run
	// (or had failed, which the client swallows). Anything still missing is
	// resolved straight from Gmail below.
	identities := h.emailIdentities(ctx, userEmail, req.MessageIDs)
	protectedList := h.protectedValues(ctx, userEmail)
	destructive := protect.IsDestructive(req.Action)
	if len(protectedList) > 0 || len(identities) < len(req.MessageIDs) {
		h.fillMissingIdentities(ctx, gmailClient, req.MessageIDs, identities)
	}

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
		// The deadline is not plumbed into the Gmail client, so the loop has to
		// check it itself: past it, every ledger write would fail silently and
		// the batch would keep mutating Gmail off the books. Since the context
		// descends from the request, this also stops the batch when the caller
		// hangs up, rather than mutating an inbox nobody is waiting on.
		if ctx.Err() != nil {
			res.Failed++
			continue
		}
		meta := identities[id]
		// Fail safe. A destructive action on a message whose sender could not be
		// established is refused rather than assumed harmless: the whole point of
		// the protected list is that it cannot be bypassed by a missing lookup.
		if destructive && len(protectedList) > 0 && meta.From == "" {
			res.Protected++
			continue
		}
		if !allows(req.Action, meta.From, protectedList) {
			res.Protected++
			continue
		}

		applyErr := h.applyVerb(ctx, h.mailboxOf(gmailClient), mailbox.OnAccount(id), req.Action, labelID)
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
	// Un-labelling is the one reversal that takes an argument: which label to
	// take off. Checked here with the rest of the payload, before any Gmail or
	// Mongo call, so a malformed request costs nothing.
	if inverse == "unlabel" && req.LabelName == "" {
		writeError(w, http.StatusBadRequest, "Nom du libellé requis")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	gmailClient, err := h.gmailClientFor(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	// The label id is resolved once for the whole batch, from the name the
	// caller applied.
	var labelID string
	if inverse == "unlabel" {
		labelID, err = h.ensureLabel(ctx, gmailClient, userEmail, req.LabelName)
		if err != nil {
			writeError(w, http.StatusBadGateway, "Libellé introuvable : "+err.Error())
			return
		}
	}

	restored := 0
	for _, id := range req.MessageIDs {
		if id == "" {
			continue
		}
		if ctx.Err() != nil {
			break
		}
		if err := h.applyInverseActionWithLabel(gmailClient, id, inverse, labelID); err != nil {
			continue
		}
		restored++

		// Mark the forward entry undone so the history does not offer a second
		// "Annuler" on work already reversed. Deliberately the MOST RECENT
		// matching entry only: an UpdateMany here would stamp every past
		// archive of the same message as undone, rewriting history the user
		// never touched.
		var entry models.ActionLog
		err := h.db.ActionLog().FindOneAndUpdate(ctx,
			bson.M{"userId": userEmail, "messageId": id, "action": req.Action, "undone": bson.M{"$ne": true}},
			bson.M{"$set": bson.M{"undone": true, "undoneAt": time.Now()}},
			options.FindOneAndUpdate().SetSort(bson.M{"createdAt": -1}),
		).Decode(&entry)

		// Record the reversal itself, exactly as the per-entry undo does. Without
		// it a batch undo left no trace and the ledger stopped being a truthful
		// account of what Mailsorter did.
		if err == nil {
			h.logActionMeta(ctx, userEmail, id, inverse, SourceUndo, entry.Subject, entry.From)
		} else {
			h.logAction(ctx, userEmail, id, inverse, SourceUndo)
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"restored": restored, "total": len(req.MessageIDs)})
}

// emailIdentities resolves the subject/sender of a set of messages in one
// indexed query. Messages absent from the local mailbox simply yield a zero
// value; callers that need certainty must fill the gaps (see
// fillMissingIdentities).
//
// The projection matters: without it this pulls every message body in the batch
// out of Mongo to read two header fields.
func (h *Handler) emailIdentities(ctx context.Context, userEmail string, messageIDs []string) map[string]models.Email {
	out := make(map[string]models.Email, len(messageIDs))
	if len(messageIDs) == 0 {
		return out
	}
	cursor, err := h.db.Emails().Find(ctx, bson.M{
		"userId":    userEmail,
		"messageId": bson.M{"$in": messageIDs},
	}, options.Find().SetProjection(bson.M{"messageId": 1, "subject": 1, "from": 1, "threadId": 1}))
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

// suggestionEmailIDs maps a set of suggestion ids to the message ids they act
// on, in one query, so a batch can pre-resolve identities without a per-item
// round trip. Unparseable or foreign ids are skipped; the caller's loop reports
// them as failures on its own.
func (h *Handler) suggestionEmailIDs(ctx context.Context, userEmail string, suggestionIDs []string) []string {
	oids := make([]primitive.ObjectID, 0, len(suggestionIDs))
	for _, id := range suggestionIDs {
		if oid, err := primitive.ObjectIDFromHex(id); err == nil {
			oids = append(oids, oid)
		}
	}
	if len(oids) == 0 {
		return nil
	}
	cursor, err := h.db.AISuggestions().Find(ctx,
		bson.M{"_id": bson.M{"$in": oids}, "userId": userEmail},
		options.Find().SetProjection(bson.M{"emailId": 1}))
	if err != nil {
		return nil
	}
	defer cursor.Close(ctx)

	var rows []models.AISuggestion
	if err := cursor.All(ctx, &rows); err != nil {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, s := range rows {
		if s.EmailID != "" {
			out = append(out, s.EmailID)
		}
	}
	return out
}

// emailIdentity resolves one message's sender and subject: local mailbox first,
// Gmail headers as a fallback. Used by the single-message triage paths so their
// ledger entries carry an identity at WRITE time, which is what makes them
// searchable, since the read-time resolution happens after the query has run.
func (h *Handler) emailIdentity(ctx context.Context, gmailClient *gmailapi.Service, userEmail, messageID string) models.Email {
	found := h.emailIdentities(ctx, userEmail, []string{messageID})
	if e, ok := found[messageID]; ok && (e.From != "" || e.Subject != "") {
		return e
	}
	h.fillMissingIdentities(ctx, gmailClient, []string{messageID}, found)
	return found[messageID]
}

// fillMissingIdentities asks Gmail for the sender/subject of the messages the
// local mailbox does not know about, mutating the map in place. Headers only, so
// the cost is one small call per unknown message and nothing for the rest.
//
// A message whose metadata cannot be fetched is left absent on purpose: the
// caller must be able to tell "sender unknown" from "sender is nobody", because
// that distinction is what keeps the protected-sender shield honest.
func (h *Handler) fillMissingIdentities(ctx context.Context, gmailClient *gmailapi.Service, messageIDs []string, into map[string]models.Email) {
	for _, id := range messageIDs {
		if id == "" {
			continue
		}
		if e, ok := into[id]; ok && (e.From != "" || e.Subject != "") {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		msg, err := h.gmailService.GetMessageMetadata(gmailClient, id)
		if err != nil {
			continue
		}
		from, subject, _, _ := gmail.ParseEmailHeaders(msg)
		into[id] = models.Email{MessageID: id, From: from, Subject: subject, ThreadID: msg.ThreadId}
	}
}
