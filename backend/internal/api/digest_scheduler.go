package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/activity"
	"github.com/nohe-sohbi/mailsorter/backend/internal/digest"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailer"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// DefaultDigestHourUTC is the hour of day (UTC) at which the daily digest goes
// out for users who have not chosen one. Set from config at startup.
var DefaultDigestHourUTC = 7

func defaultDigestHour() int {
	if DefaultDigestHourUTC < 0 || DefaultDigestHourUTC > 23 {
		return 7
	}
	return DefaultDigestHourUTC
}

// digestSweepInterval is how often the scheduler checks who is due for a digest.
// DueAt makes the send idempotent (at most once per day), so a frequent tick is
// safe and just shortens the lag between the target hour and delivery.
const digestSweepInterval = 15 * time.Minute

// startDigestLoop launches the background scheduler that emails the daily recap.
func (h *Handler) startDigestLoop() {
	go func() {
		ticker := time.NewTicker(digestSweepInterval)
		defer ticker.Stop()
		for range ticker.C {
			h.sendDueDigests()
		}
	}()
}

// sendDueDigests emails each opted-in user their 7-day recap when it is due. It
// is best-effort and resilient: a per-user failure is logged and skipped so one
// bad account never stalls the rest. Every user that is due is stamped after the
// attempt, so a transient failure costs at most that day's digest rather than
// triggering a retry storm.
func (h *Handler) sendDueDigests() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cursor, err := h.db.Users().Find(ctx, bson.M{"digestEnabled": true})
	if err != nil {
		log.Printf("digest: failed to list opted-in users: %v", err)
		return
	}
	defer cursor.Close(ctx)

	var users []models.User
	if err := cursor.All(ctx, &users); err != nil {
		log.Printf("digest: failed to decode users: %v", err)
		return
	}

	now := time.Now()
	for _, u := range users {
		hour := u.DigestHourUTC
		if hour <= 0 || hour > 23 {
			hour = defaultDigestHour()
		}
		if !mailer.DueAt(u.DigestLastSentAt, now, hour) {
			continue
		}
		h.sendOneDigest(ctx, u.Email)
		h.stampDigestSent(ctx, u.Email)
	}
}

// sendOneDigest renders and sends a single user's recap. An empty week is
// skipped (no point emailing "0 emails triés"), but the caller still stamps the
// send time so we don't re-evaluate the same user every tick all day.
func (h *Handler) sendOneDigest(ctx context.Context, userEmail string) {
	summary, err := h.activitySummary(ctx, userEmail)
	if err != nil {
		log.Printf("digest: activity summary failed for %s: %v", userEmail, err)
		return
	}
	if summary.Total == 0 {
		return
	}

	if _, err := h.deliverDigest(ctx, userEmail, summary); err != nil {
		// A missing gmail.send scope (user connected before the digest feature)
		// surfaces here; they simply need to reconnect Gmail to grant it.
		log.Printf("digest: send failed for %s: %v", userEmail, err)
		return
	}
	log.Printf("digest: sent to %s", userEmail)
}

// deliverDigest renders a summary and sends it to the user through their own
// Gmail. Shared by the scheduler and the "envoyer un test" button so a test
// exercises the real delivery path, headers included, rather than a lookalike.
func (h *Handler) deliverDigest(ctx context.Context, userEmail string, summary activity.Summary) (digest.Digest, error) {
	d := digest.Render(summary, time.Now())

	gmailClient, err := h.gmailClientFor(ctx, userEmail)
	if err != nil {
		return d, err
	}

	raw := mailer.BuildRaw(userEmail, userEmail, d.Subject, d.Text, d.HTML)
	if err := h.gmailService.SendMessage(gmailClient, raw); err != nil {
		return d, fmt.Errorf("envoi du digest impossible: %w", err)
	}
	return d, nil
}

// SendTestDigest emails the caller their recap right now.
//
// The digest was the one feature nobody could verify: you turned it on, and
// then waited a day to learn whether your Gmail grant still carried the send
// scope. This closes that loop. It deliberately does NOT stamp
// digestLastSentAt: a test must not consume the day's real digest.
//
// An empty week is sent rather than skipped here (unlike the scheduler, which
// has no reason to mail "0 emails triés" unprompted): the point of a test send
// is to prove delivery works, and a user who asked for it is owed the answer.
func (h *Handler) SendTestDigest(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	summary, err := h.activitySummary(ctx, userEmail)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load activity")
		return
	}

	d, err := h.deliverDigest(ctx, userEmail, summary)
	if err != nil {
		// A dead Google grant is the single most likely cause, and it has its
		// own remedy in the UI ("Reconnecter Gmail"), so it must not be flattened
		// into a generic 500.
		if errors.Is(err, errReauthRequired) {
			writeAuthError(w, err)
			return
		}
		writeError(w, http.StatusBadGateway,
			"Envoi impossible. Reconnectez Gmail depuis la carte « Compte Gmail » pour réautoriser l'envoi.")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "sent",
		"subject": d.Subject,
		"total":   summary.Total,
	})
}

// stampDigestSent records that we attempted a digest for the user today so the
// next tick treats them as already handled.
func (h *Handler) stampDigestSent(ctx context.Context, userEmail string) {
	h.db.Users().UpdateOne(ctx,
		bson.M{"email": userEmail},
		bson.M{"$set": bson.M{"digestLastSentAt": time.Now()}},
		options.Update().SetUpsert(false),
	)
}
