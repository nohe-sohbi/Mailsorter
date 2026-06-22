package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/nohe-sohbi/mailsorter/backend/internal/gmail"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
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

// Snooze pulls a message out of the inbox until a chosen wake time. It resolves
// the wake time from a friendly preset (or an explicit timestamp), archives the
// message (removing INBOX) and tags it with the snooze label so it is easy to
// find. A background loop returns it to the inbox, marked unread, when due.
func (h *Handler) Snooze(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	var req models.SnoozeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.MessageID == "" {
		http.Error(w, "Message ID required", http.StatusBadRequest)
		return
	}

	// Resolve the wake time: an explicit future timestamp wins, otherwise a preset.
	wakeAt := req.WakeAt
	now := time.Now()
	if wakeAt.IsZero() {
		resolved, err := snooze.Resolve(req.Preset, now)
		if err != nil {
			http.Error(w, "Choisissez une échéance valide", http.StatusBadRequest)
			return
		}
		wakeAt = resolved
	}
	if !wakeAt.After(now) {
		http.Error(w, "L'échéance doit être dans le futur", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	gmailClient, err := h.gmailClientFor(ctx, userEmail)
	if err != nil {
		http.Error(w, "Failed to get user credentials", http.StatusInternalServerError)
		return
	}

	// Enrich the record with sender/subject for the snoozed list (best-effort).
	from, subject, threadID := "", "", ""
	if msg, mErr := h.gmailService.GetMessage(gmailClient, req.MessageID); mErr == nil {
		from, subject, _, _ = gmail.ParseEmailHeaders(msg)
		threadID = msg.ThreadId
	}

	labelID, err := h.ensureLabel(ctx, gmailClient, userEmail, snoozeLabelName)
	if err != nil {
		http.Error(w, "Impossible de préparer le report", http.StatusBadGateway)
		return
	}
	// Out of the inbox, tagged as snoozed.
	if err := h.gmailService.ModifyMessage(gmailClient, req.MessageID, []string{labelID}, []string{"INBOX"}); err != nil {
		http.Error(w, "Report impossible : "+err.Error(), http.StatusBadGateway)
		return
	}

	_, err = h.db.Snoozes().UpdateOne(ctx,
		bson.M{"userId": userEmail, "messageId": req.MessageID, "status": "scheduled"},
		bson.M{
			"$set": bson.M{
				"from": from, "subject": subject, "threadId": threadID,
				"wakeAt": wakeAt, "status": "scheduled", "updatedAt": now,
			},
			"$setOnInsert": bson.M{
				"userId": userEmail, "messageId": req.MessageID, "createdAt": now,
			},
		},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		http.Error(w, "Failed to save snooze", http.StatusInternalServerError)
		return
	}

	h.logAction(ctx, userEmail, req.MessageID, "archive", SourceSnooze)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "snoozed",
		"wakeAt": wakeAt,
	})
}

// GetSnoozes lists the caller's snoozes (scheduled by default), soonest first.
func (h *Handler) GetSnoozes(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	status := r.URL.Query().Get("status")
	if status == "" {
		status = "scheduled"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cursor, err := h.db.Snoozes().Find(ctx,
		bson.M{"userId": userEmail, "status": status},
		options.Find().SetSort(bson.M{"wakeAt": 1}).SetLimit(200))
	if err != nil {
		http.Error(w, "Failed to load snoozes", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	rows := make([]models.Snooze, 0)
	if err := cursor.All(ctx, &rows); err != nil {
		http.Error(w, "Failed to decode snoozes", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"snoozes": rows})
}

// WakeSnooze brings a snoozed email back to the inbox immediately (the user
// changed their mind), marking it unread so it is not missed.
func (h *Handler) WakeSnooze(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	oid, err := primitive.ObjectIDFromHex(mux.Vars(r)["id"])
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var s models.Snooze
	if err := h.db.Snoozes().FindOne(ctx, bson.M{"_id": oid, "userId": userEmail}).Decode(&s); err != nil {
		http.Error(w, "Snooze introuvable", http.StatusNotFound)
		return
	}

	gmailClient, err := h.gmailClientFor(ctx, userEmail)
	if err != nil {
		http.Error(w, "Failed to get user credentials", http.StatusInternalServerError)
		return
	}

	if err := h.restoreSnoozed(ctx, gmailClient, userEmail, s.MessageID); err != nil {
		http.Error(w, "Réactivation impossible : "+err.Error(), http.StatusBadGateway)
		return
	}

	h.db.Snoozes().UpdateOne(ctx, bson.M{"_id": oid},
		bson.M{"$set": bson.M{"status": "done", "updatedAt": time.Now()}})
	h.logAction(ctx, userEmail, s.MessageID, "unarchive", SourceSnooze)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "woken"})
}

// restoreSnoozed returns a message to the inbox, marks it unread and strips the
// snooze label.
func (h *Handler) restoreSnoozed(ctx context.Context, gmailClient *gmailapi.Service, userEmail, messageID string) error {
	add := []string{"INBOX", "UNREAD"}
	remove := []string{}
	if labelID, err := h.ensureLabel(ctx, gmailClient, userEmail, snoozeLabelName); err == nil {
		remove = append(remove, labelID)
	}
	return h.gmailService.ModifyMessage(gmailClient, messageID, add, remove)
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
			log.Printf("snooze: failed to restore %s for %s: %v", s.MessageID, s.UserID, err)
			continue
		}
		oid, _ := primitive.ObjectIDFromHex(s.ID)
		h.db.Snoozes().UpdateOne(ctx, bson.M{"_id": oid},
			bson.M{"$set": bson.M{"status": "done", "updatedAt": time.Now()}})
		h.logAction(ctx, s.UserID, s.MessageID, "unarchive", SourceSnooze)
	}
}
