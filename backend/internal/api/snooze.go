package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/nohe-sohbi/mailsorter/backend/internal/gmail"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/protect"
	"github.com/nohe-sohbi/mailsorter/backend/internal/snooze"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	gmailapi "google.golang.org/api/gmail/v1"
)

// snoozeLabelName is the Gmail label applied to snoozed mail so it stays
// findable in Gmail itself while it is out of the inbox.
const snoozeLabelName = "Mailsorter/Reporté"

// snoozeSweepInterval is how often the background loop checks for emails whose
// snooze has elapsed and brings them back.
const snoozeSweepInterval = time.Minute

// maxSnoozeWakeAttempts caps how many times a due snooze is retried before it is
// parked as "failed". Without it, a wake that can never succeed (e.g. the message
// was hard deleted) would be re-attempted on every sweep indefinitely.
const maxSnoozeWakeAttempts = 5

// Snooze pulls a message out of the inbox until a chosen wake time. It resolves
// the wake time from a friendly preset (or an explicit timestamp), archives the
// message (removing INBOX) and tags it with the snooze label so it is easy to
// find. A background loop returns it to the inbox, marked unread, when due.
func (h *Handler) Snooze(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	var req models.SnoozeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.MessageID == "" {
		writeError(w, http.StatusBadRequest, "Message ID required")
		return
	}

	now := time.Now()
	wakeAt, wErr := resolveWakeAt(req.Preset, req.WakeAt, now)
	if wErr != nil {
		writeError(w, http.StatusBadRequest, wErr.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	gmailClient, err := h.gmailClientFor(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	// Enrich the record with sender/subject for the snoozed list (best-effort).
	var identity models.Email
	if msg, mErr := h.gmailService.GetMessage(gmailClient, req.MessageID); mErr == nil {
		from, subject, _, _ := gmail.ParseEmailHeaders(msg)
		identity = models.Email{MessageID: req.MessageID, From: from, Subject: subject, ThreadID: msg.ThreadId}
	}

	labelID, err := h.ensureLabel(ctx, gmailClient, userEmail, snoozeLabelName)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Impossible de préparer le report")
		return
	}

	if err := h.snoozeMessage(ctx, gmailClient, userEmail, labelID, identity, wakeAt, now); err != nil {
		writeError(w, http.StatusBadGateway, "Report impossible : "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "snoozed",
		"wakeAt": wakeAt,
	})
}

// resolveWakeAt turns a request's preset-or-timestamp pair into one concrete
// wake time. An explicit timestamp wins and is validated on its own terms (in
// the future, inside the supported horizon) rather than merely being "not in the
// past": presets cannot produce a bad time, a date picker can. Shared by the
// single and batch snooze routes so both accept exactly the same input.
func resolveWakeAt(preset string, explicit, now time.Time) (time.Time, error) {
	if explicit.IsZero() {
		resolved, err := snooze.Resolve(preset, now)
		if err != nil {
			return time.Time{}, errInvalidSnoozeDeadline
		}
		return resolved, nil
	}
	// Surfaced verbatim, like rules.Validate: the pure package owns the wording
	// so the same sentence reaches the user from every caller.
	if err := snooze.ValidateWake(explicit, now); err != nil {
		return time.Time{}, err
	}
	return explicit, nil
}

// errInvalidSnoozeDeadline is what an unknown preset reports. The preset names
// are ours, not the user's, so the message names the fix rather than the value.
var errInvalidSnoozeDeadline = errors.New("choisissez une échéance valide")

// snoozeMessage performs one snooze: out of the inbox, tagged with the snooze
// label, recorded as scheduled and journaled. The caller resolves the wake time
// and the label id once (they are identical across a batch) and passes whatever
// identity it already holds, so the batch route costs one label lookup rather
// than one per message.
func (h *Handler) snoozeMessage(ctx context.Context, gmailClient *gmailapi.Service, userEmail, labelID string, identity models.Email, wakeAt, now time.Time) error {
	messageID := identity.MessageID
	// One call, two verbs: tag it and take it out of the inbox. See
	// mailbox.GmailLabelsFor for why this must not become two requests.
	if err := h.applyMutations(ctx, h.mailboxOf(gmailClient), mailbox.OnAccount(messageID),
		mailbox.Mutation{Action: mailbox.ActionLabel, LabelID: labelID},
		mailbox.Mutation{Action: mailbox.ActionArchive},
	); err != nil {
		return err
	}

	if _, err := h.db.Snoozes().UpdateOne(ctx,
		bson.M{"userId": userEmail, "messageId": messageID, "status": "scheduled"},
		bson.M{
			"$set": bson.M{
				"from": identity.From, "subject": identity.Subject, "threadId": identity.ThreadID,
				"wakeAt": wakeAt, "status": "scheduled", "updatedAt": now,
			},
			"$setOnInsert": bson.M{
				"userId": userEmail, "messageId": messageID, "createdAt": now,
			},
		},
		options.Update().SetUpsert(true),
	); err != nil {
		// The message is already out of the inbox at this point. Without a
		// scheduled row nothing would ever bring it back, so this is a real
		// failure and not something to swallow.
		return fmt.Errorf("le report n'a pas pu être enregistré: %w", err)
	}

	h.logActionMeta(ctx, userEmail, messageID, "archive", SourceSnooze, identity.Subject, identity.From)
	return nil
}

// BatchSnooze reports a whole selection to the same wake time in one request.
//
// Snoozing was a one-message-at-a-time affordance reachable only from the
// reader, which made it useless against the case it is best at: the twenty
// newsletters you want out of the way until Saturday. Everything else about it
// is unchanged, protected senders included: a bulk snooze takes mail out of the
// inbox, which is exactly what the VIP list is there to veto.
func (h *Handler) BatchSnooze(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	var req models.BatchSnoozeRequest
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

	now := time.Now()
	wakeAt, wErr := resolveWakeAt(req.Preset, req.WakeAt, now)
	if wErr != nil {
		writeError(w, http.StatusBadRequest, wErr.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	gmailClient, err := h.gmailClientFor(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	labelID, err := h.ensureLabel(ctx, gmailClient, userEmail, snoozeLabelName)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Impossible de préparer le report")
		return
	}

	// One lookup for the whole batch, exactly as BatchAction does: the shield
	// needs each sender, and the snoozed list needs the subject to be readable.
	identities := h.emailIdentities(ctx, userEmail, req.MessageIDs)
	protectedList := h.protectedValues(ctx, userEmail)
	if len(protectedList) > 0 || len(identities) < len(req.MessageIDs) {
		h.fillMissingIdentities(ctx, gmailClient, req.MessageIDs, identities)
	}

	res := batchSnoozeResult{Total: len(req.MessageIDs), WakeAt: wakeAt}
	res.Snoozed = make([]string, 0, len(req.MessageIDs))

	for _, id := range req.MessageIDs {
		if id == "" || ctx.Err() != nil {
			res.Failed++
			continue
		}
		meta := identities[id]
		meta.MessageID = id
		// Fail safe, like the batch actions: a sender we could not establish is
		// treated as possibly protected rather than assumed harmless.
		if len(protectedList) > 0 && (meta.From == "" || !allows(protect.ActionArchive, meta.From, protectedList)) {
			res.Protected++
			continue
		}
		if err := h.snoozeMessage(ctx, gmailClient, userEmail, labelID, meta, wakeAt, now); err != nil {
			log.Printf("snooze: batch failed for %s: %v", id, err)
			res.Failed++
			continue
		}
		res.Snoozed = append(res.Snoozed, id)
	}

	writeJSON(w, http.StatusOK, res)
}

// batchSnoozeResult mirrors batchActionResult: the client has to be able to tell
// "done" from "shielded by your VIP list" from "failed".
type batchSnoozeResult struct {
	Snoozed   []string  `json:"snoozed"`
	Failed    int       `json:"failed"`
	Protected int       `json:"protectedSkipped"`
	Total     int       `json:"total"`
	WakeAt    time.Time `json:"wakeAt"`
}

// GetSnoozes lists the caller's snoozes (scheduled by default), soonest first.
func (h *Handler) GetSnoozes(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	status := r.URL.Query().Get("status")
	if status == "" {
		status = "scheduled"
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	cursor, err := h.db.Snoozes().Find(ctx,
		bson.M{"userId": userEmail, "status": status},
		options.Find().SetSort(bson.M{"wakeAt": 1}).SetLimit(200))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load snoozes")
		return
	}
	defer cursor.Close(ctx)

	rows := make([]models.Snooze, 0)
	if err := cursor.All(ctx, &rows); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to decode snoozes")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"snoozes": rows})
}

// WakeSnooze brings a snoozed email back to the inbox immediately (the user
// changed their mind), marking it unread so it is not missed.
func (h *Handler) WakeSnooze(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	oid, err := primitive.ObjectIDFromHex(mux.Vars(r)["id"])
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid id")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	var s models.Snooze
	if err := h.db.Snoozes().FindOne(ctx, bson.M{"_id": oid, "userId": userEmail}).Decode(&s); err != nil {
		writeError(w, http.StatusNotFound, "Snooze introuvable")
		return
	}

	gmailClient, err := h.gmailClientFor(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	if err := h.restoreSnoozed(ctx, gmailClient, userEmail, s.MessageID); err != nil {
		writeError(w, http.StatusBadGateway, "Réactivation impossible : "+err.Error())
		return
	}

	h.db.Snoozes().UpdateOne(ctx, bson.M{"_id": oid},
		bson.M{"$set": bson.M{"status": "done", "updatedAt": time.Now()}})
	h.logActionMeta(ctx, userEmail, s.MessageID, "unarchive", SourceSnooze, s.Subject, s.From)

	writeJSON(w, http.StatusOK, map[string]string{"status": "woken"})
}

// restoreSnoozed returns a message to the inbox, marks it unread and strips the
// snooze label.
func (h *Handler) restoreSnoozed(ctx context.Context, gmailClient *gmailapi.Service, userEmail, messageID string) error {
	muts := []mailbox.Mutation{
		{Action: mailbox.ActionUnarchive},
		{Action: mailbox.ActionMarkUnread},
	}
	// Stripping the snooze label is best effort: if the label cannot be
	// resolved the message still has to come back, which is the part the user
	// is waiting for.
	if labelID, err := h.ensureLabel(ctx, gmailClient, userEmail, snoozeLabelName); err == nil {
		muts = append(muts, mailbox.Mutation{Action: mailbox.ActionUnlabel, LabelID: labelID})
	}
	return h.applyMutations(ctx, h.mailboxOf(gmailClient), mailbox.OnAccount(messageID), muts...)
}

// startSnoozeLoop launches the background sweeper that resurfaces due snoozes.
func (h *Handler) startSnoozeLoop() {
	go func() {
		ticker := time.NewTicker(snoozeSweepInterval)
		defer ticker.Stop()
		for range ticker.C {
			h.wakeDueSnoozes()
		}
	}()
}

// wakeDueSnoozes brings back every snooze whose wake time has passed. It is
// best-effort and resilient: a per-message Gmail failure is logged and skipped
// so one bad message never stalls the rest.
func (h *Handler) wakeDueSnoozes() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cursor, err := h.db.Snoozes().Find(ctx,
		bson.M{"status": "scheduled", "wakeAt": bson.M{"$lte": time.Now()}},
		options.Find().SetLimit(200))
	if err != nil {
		return
	}
	defer cursor.Close(ctx)

	var due []models.Snooze
	if err := cursor.All(ctx, &due); err != nil {
		return
	}

	// Cache one Gmail client per user across their due snoozes.
	clients := map[string]*gmailapi.Service{}
	seen := map[string]bool{}
	for _, s := range due {
		if !seen[s.UserID] {
			seen[s.UserID] = true
			if c, cerr := h.gmailClientFor(ctx, s.UserID); cerr == nil {
				clients[s.UserID] = c
			} else {
				log.Printf("snooze: no Gmail client for %s: %v", s.UserID, cerr)
			}
		}
		client := clients[s.UserID]
		if client == nil {
			continue
		}

		if err := h.restoreSnoozed(ctx, client, s.UserID, s.MessageID); err != nil {
			log.Printf("snooze: failed to restore %s for %s (attempt %d): %v", s.MessageID, s.UserID, s.Attempts+1, err)
			oid, _ := primitive.ObjectIDFromHex(s.ID)
			// Cap retries: a permanently-failing wake (e.g. the message was hard
			// deleted) would otherwise be re-attempted on every 60s sweep forever.
			// After maxSnoozeWakeAttempts, park it as "failed" so it drops out of
			// the scheduled query.
			update := bson.M{"$inc": bson.M{"attempts": 1}, "$set": bson.M{"updatedAt": time.Now()}}
			if s.Attempts+1 >= maxSnoozeWakeAttempts {
				update = bson.M{
					"$inc": bson.M{"attempts": 1},
					"$set": bson.M{"status": "failed", "updatedAt": time.Now()},
				}
			}
			h.db.Snoozes().UpdateOne(ctx, bson.M{"_id": oid}, update)
			continue
		}
		oid, _ := primitive.ObjectIDFromHex(s.ID)
		h.db.Snoozes().UpdateOne(ctx, bson.M{"_id": oid},
			bson.M{"$set": bson.M{"status": "done", "updatedAt": time.Now()}})
		h.logActionMeta(ctx, s.UserID, s.MessageID, "unarchive", SourceSnooze, s.Subject, s.From)
	}
}
