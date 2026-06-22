package api

import (
	"context"
	"log"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/schedule"
	"go.mongodb.org/mongo-driver/bson"
)

// autoSyncInterval is the minimum time between two background syncs of the same
// user's inbox. Inbox Zero does not need second-by-second freshness; a periodic
// sweep keeps the box tidy without hammering the Gmail API or the user's quota.
const autoSyncInterval = 30 * time.Minute

// autoSyncSweepInterval is how often the scheduler wakes to look for users due
// for a background sync. schedule.Due gates the actual work per user, so a
// frequent tick only shortens the lag, it does not cause extra syncs.
const autoSyncSweepInterval = 5 * time.Minute

// startAutoSyncLoop launches the background scheduler that keeps opted-in users'
// inboxes synced (and, when rule autopilot is on, auto-triaged) hands-free.
func (h *Handler) startAutoSyncLoop() {
	go func() {
		ticker := time.NewTicker(autoSyncSweepInterval)
		defer ticker.Stop()
		for range ticker.C {
			h.runDueAutoSyncs()
		}
	}()
}

// runDueAutoSyncs syncs every opted-in user whose last background sync is older
// than autoSyncInterval. It is best-effort and resilient: each user is stamped
// before the attempt so a transient failure costs at most one cycle rather than
// triggering a retry storm, and a per-user failure is logged and skipped so one
// bad account never stalls the rest.
func (h *Handler) runDueAutoSyncs() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cursor, err := h.db.Users().Find(ctx, bson.M{"autoSyncEnabled": true})
	if err != nil {
		log.Printf("autosync: failed to list opted-in users: %v", err)
		return
	}
	defer cursor.Close(ctx)

	var users []models.User
	if err := cursor.All(ctx, &users); err != nil {
		log.Printf("autosync: failed to decode users: %v", err)
		return
	}

	now := time.Now()
	for _, u := range users {
		if !schedule.Due(u.LastAutoSyncAt, now, autoSyncInterval) {
			continue
		}
		// Stamp first so a failure does not get retried every sweep all day.
		h.stampAutoSync(ctx, u.Email)

		_, _, rulesApplied, err := h.syncInbox(ctx, u.Email)
		if err != nil {
			log.Printf("autosync: sync failed for %s: %v", u.Email, err)
			continue
		}
		if rulesApplied > 0 {
			log.Printf("autosync: %s — %d email(s) auto-triaged", u.Email, rulesApplied)
		}
	}
}

// stampAutoSync records that a background sync was attempted for the user now, so
// the next sweep honors the minimum interval.
func (h *Handler) stampAutoSync(ctx context.Context, userEmail string) {
	h.db.Users().UpdateOne(ctx,
		bson.M{"email": userEmail},
		bson.M{"$set": bson.M{"lastAutoSyncAt": time.Now()}},
	)
}
