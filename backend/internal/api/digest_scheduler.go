package api

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/activity"
	"github.com/nohe-sohbi/mailsorter/backend/internal/config"
	"github.com/nohe-sohbi/mailsorter/backend/internal/digest"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailer"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
	"github.com/nohe-sohbi/mailsorter/backend/internal/smtpsend"
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

// digestHourOf is the hour a user's digest goes out: the one they picked,
// midnight included, or the server default when they never picked one (the
// field is absent) or the stored value is out of range. The settings screen
// and the scheduler both read it here, so what the screen shows is when the
// digest actually leaves.
func digestHourOf(stored *int) int {
	if stored == nil || *stored < 0 || *stored > 23 {
		return defaultDigestHour()
	}
	return *stored
}

// digestPace is how one pass of the recap loop spreads the accounts that are
// due: a few at once, each under its own deadline, so a sending server that
// does not answer holds its own account and nobody else's.
type digestPace struct {
	// senders is how many recaps leave at once: a few, so one slow server
	// holds one of them while the others carry on, and not many, because each
	// is a Mongo aggregation plus an authenticated session with a provider.
	senders int
	// send bounds one account's recap, from reading its week to the answer of
	// Gmail or of the sending server: the budget of "Envoyer un test", which
	// does the same work.
	send time.Duration
	// stamp bounds the write that records the account as done for the day.
	// Its own, so a send that used up its budget is still recorded, and the
	// recap does not leave a second time on the next pass.
	stamp time.Duration
}

// dailyDigestPace is the pace of the recap loop.
var dailyDigestPace = digestPace{senders: 4, send: 30 * time.Second, stamp: 10 * time.Second}

// run hands each account to send, then to stamp once its send is over,
// whatever the outcome, and returns when every account is done. A stamp that
// fails is logged: the recap then leaves again on the next pass.
func (p digestPace) run(emails []string, send func(context.Context, string), stamp func(context.Context, string) error) {
	senders := min(max(p.senders, 1), len(emails))
	queue := make(chan string)
	var wg sync.WaitGroup
	for range senders {
		wg.Go(func() {
			for email := range queue {
				p.one(email, send, stamp)
			}
		})
	}
	for _, email := range emails {
		queue <- email
	}
	close(queue)
	wg.Wait()
}

// one is a single account's turn: its send under its deadline, then its stamp
// under its own.
func (p digestPace) one(email string, send func(context.Context, string), stamp func(context.Context, string) error) {
	sendCtx, cancelSend := context.WithTimeout(context.Background(), p.send)
	send(sendCtx, email)
	cancelSend()

	stampCtx, cancelStamp := context.WithTimeout(context.Background(), p.stamp)
	defer cancelStamp()
	if err := stamp(stampCtx, email); err != nil {
		log.Printf("digest: failed to record the recap of %s as done for today: %v", email, err)
	}
}

// sendDueDigests emails each opted-in user their 7-day recap when it is due. It
// is best-effort and resilient: a per-user failure is logged and skipped, and
// each user has their own deadline (dailyDigestPace), so one bad account never
// stalls the rest. Every user that is due is stamped after the attempt, so a
// transient failure costs at most that day's digest rather than triggering a
// retry storm.
func (h *Handler) sendDueDigests() {
	due, err := h.dueDigests(time.Now())
	if err != nil {
		log.Printf("digest: %v", err)
		return
	}
	dailyDigestPace.run(due, h.sendOneDigest, h.stampDigestSent)
}

// dueDigests is the address of every opted-in user whose recap is due at now.
func (h *Handler) dueDigests(now time.Time) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cursor, err := h.db.Users().Find(ctx, bson.M{"digestEnabled": true})
	if err != nil {
		return nil, fmt.Errorf("failed to list opted-in users: %w", err)
	}
	defer cursor.Close(ctx)

	var users []models.User
	if err := cursor.All(ctx, &users); err != nil {
		return nil, fmt.Errorf("failed to decode users: %w", err)
	}

	var due []string
	for _, u := range users {
		if mailer.DueAt(u.DigestLastSentAt, now, digestHourOf(u.DigestHourUTC)) {
			due = append(due, u.Email)
		}
	}
	return due, nil
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
		// On the Gmail API, a missing gmail.send scope (user connected before
		// the digest feature) surfaces here; on IMAP, a sending server that
		// refuses the app password. "Envoyer un test" says which, in words.
		log.Printf("digest: send failed for %s: %v", userEmail, err)
		return
	}
	log.Printf("digest: sent to %s", userEmail)
}

// digestStyle is the look of the recap's HTML body: the instance's UI theme,
// so the email a user receives each morning wears the same clothes as the app
// it comes from. Switched by the same UI_THEME, at the same restart.
func digestStyle() digest.Style {
	if UITheme == config.UIThemeHotel {
		return digest.StyleHotel
	}
	return digest.StyleClassic
}

// errSendThroughBox marks every failure of the IMAP half of deliverDigest, so
// "Envoyer un test" answers none of them with the Gmail sentence. The causes
// it has words for (no known sending server, a refused app password, a server
// that does not answer) stay reachable under it with errors.Is.
var errSendThroughBox = errors.New("api: the recap did not leave through the mailbox's sending server")

// deliverDigest renders a summary and sends it to the user, from their own
// mailbox: through their Gmail on the Gmail API, through their mailbox's
// sending server on IMAP. Shared by the scheduler and the "envoyer un test"
// button so a test exercises the real delivery path, headers included, rather
// than a lookalike.
func (h *Handler) deliverDigest(ctx context.Context, userEmail string, summary activity.Summary) (digest.Digest, error) {
	now := time.Now()
	d := digest.RenderStyle(summary, now, digestStyle())
	recap := func(address string) []byte { return digestMessage(d, address, now) }

	transport, account, err := h.transportFor(ctx, userEmail)
	if err != nil {
		return d, err
	}
	if transport == provider.TransportIMAP {
		// From and to the mailbox's own address, which need not be the
		// account's: the sending server takes a message only from the
		// address it logged in as, and the mailbox is where the person reads.
		if err := h.sendThroughBox(ctx, account, recap(account.Username)); err != nil {
			return d, fmt.Errorf("%w: %w", errSendThroughBox, err)
		}
		return d, nil
	}

	gmailClient, err := h.gmailClientFor(ctx, userEmail)
	if err != nil {
		return d, err
	}
	if err := h.gmailService.SendMessage(gmailClient, mailer.ForGmail(recap(userEmail))); err != nil {
		return d, fmt.Errorf("envoi du digest impossible: %w", err)
	}
	return d, nil
}

// digestMessage is the recap as an email from address to address, dated now.
// Its Message-ID is drawn here, on the domain of that address, the mailbox
// it leaves from: a message without one is the kind a spam filter frowns on,
// and a sending server is not bound to add it.
func digestMessage(d digest.Digest, address string, now time.Time) []byte {
	return mailer.Build(mailer.Message{
		From:    address,
		To:      address,
		Subject: d.Subject,
		Text:    d.Text,
		HTML:    d.HTML,
		Date:    now,
		ID:      mailer.MessageID(rand.Text(), address),
	})
}

// writeDigestSendError answers a test send that did not leave. A dead Google
// grant keeps its own answer (401, and "Reconnecter Gmail" in the UI). On
// IMAP, every answer is about the mailbox's sending server, never Gmail, and
// none is a 401: the SPA signs the person out on one before they read it.
func writeDigestSendError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errReauthRequired):
		writeAuthError(w, err)
	case errors.Is(err, provider.ErrNoSendingServer):
		writeError(w, http.StatusUnprocessableEntity,
			"Mailsorter ne connaît pas le serveur d'envoi de cette boîte : le récap ne peut pas en partir.")
	case errors.Is(err, smtpsend.ErrAuth):
		writeError(w, http.StatusBadGateway,
			"Le serveur d'envoi de votre boîte a refusé le mot de passe d'application.")
	case errors.Is(err, smtpsend.ErrBlocked):
		// The password is right: saying it was refused would send the person
		// to replace one that works.
		writeError(w, http.StatusBadGateway,
			"Votre fournisseur a bloqué la connexion venant du serveur de Mailsorter. Autorisez-la depuis la sécurité de votre compte, puis réessayez.")
	case errors.Is(err, smtpsend.ErrUnreachable):
		writeError(w, http.StatusBadGateway,
			"Le serveur d'envoi de votre boîte ne répond pas. Réessayez dans quelques minutes.")
	case errors.Is(err, errSendThroughBox):
		writeError(w, http.StatusBadGateway,
			"Le récap n'a pas pu partir du serveur d'envoi de votre boîte. Réessayez dans quelques minutes.")
	default:
		writeError(w, http.StatusBadGateway,
			"Envoi impossible. Reconnectez Gmail depuis la carte « Compte Gmail » pour réautoriser l'envoi.")
	}
}

// digestTestInterval is the least time between two test sends of one
// account. Each one opens an authenticated session with the person's sending
// server, and a burst of refused passwords from one address is what gets that
// address throttled by a provider, for every account of the instance.
const digestTestInterval = time.Minute

// digestTestTooSoon is what a test send asked within digestTestInterval of
// the previous one answers.
const digestTestTooSoon = "Vous venez de demander un test. Attendez une minute avant d'en envoyer un autre."

// digestTestLimiter allows one test send per account per digestTestInterval.
// Built on first use, like the sign-in limiter, so it exists whatever built
// the Handler.
func (h *Handler) digestTestLimiter() *rateLimiter {
	h.digestTestOnce.Do(func() {
		h.digestTest = newRateLimiter(1/digestTestInterval.Seconds(), 1)
	})
	return h.digestTest
}

// SendTestDigest emails the caller their recap right now.
//
// The digest was the one feature nobody could verify: you turned it on, and
// then waited a day to learn whether delivery worked (on the Gmail API,
// whether your grant still carried the send scope; on IMAP, whether your
// sending server takes your app password). This closes that loop. It
// deliberately does NOT stamp digestLastSentAt: a test must not consume the
// day's real digest.
//
// An empty week is sent rather than skipped here (unlike the scheduler, which
// has no reason to mail "0 emails triés" unprompted): the point of a test send
// is to prove delivery works, and a user who asked for it is owed the answer.
//
// Every attempt counts against digestTestInterval, a failed one included:
// those are the ones a provider counts too.
func (h *Handler) SendTestDigest(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}
	if !h.digestTestLimiter().allow(userEmail) {
		w.Header().Set("Retry-After", strconv.Itoa(int(digestTestInterval.Seconds())))
		writeError(w, http.StatusTooManyRequests, digestTestTooSoon)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	summary, err := h.activitySummary(ctx, userEmail)
	if err != nil {
		log.Printf("digest test: activity summary failed for %s: %v", userEmail, err)
		writeError(w, http.StatusInternalServerError, "Failed to load activity")
		return
	}

	d, err := h.deliverDigest(ctx, userEmail, summary)
	if err != nil {
		log.Printf("digest test: send failed for %s: %v", userEmail, err)
		writeDigestSendError(w, err)
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
func (h *Handler) stampDigestSent(ctx context.Context, userEmail string) error {
	_, err := h.db.Users().UpdateOne(ctx,
		bson.M{"email": userEmail},
		bson.M{"$set": bson.M{"digestLastSentAt": time.Now()}},
		options.Update().SetUpsert(false),
	)
	return err
}
