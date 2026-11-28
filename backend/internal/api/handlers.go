package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nohe-sohbi/mailsorter/backend/internal/ai"
	"github.com/nohe-sohbi/mailsorter/backend/internal/auth"
	"github.com/nohe-sohbi/mailsorter/backend/internal/billing"
	"github.com/nohe-sohbi/mailsorter/backend/internal/config"
	"github.com/nohe-sohbi/mailsorter/backend/internal/crypto"
	"github.com/nohe-sohbi/mailsorter/backend/internal/database"
	"github.com/nohe-sohbi/mailsorter/backend/internal/gmail"
	"github.com/nohe-sohbi/mailsorter/backend/internal/imap"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
	"github.com/nohe-sohbi/mailsorter/backend/internal/metrics"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
	"github.com/nohe-sohbi/mailsorter/backend/internal/rules"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
	gmailapi "google.golang.org/api/gmail/v1"
)

// gmailClientFor returns an authenticated Gmail client for the user. It routes
// through getUserToken, so the OAuth token is refreshed and persisted when
// expired. Previously this logic was copy-pasted across handlers (and missing
// entirely from sync/labels, which would fail once the access token aged out).
func (h *Handler) gmailClientFor(ctx context.Context, userEmail string) (*gmailapi.Service, error) {
	// The guard, and the reason this function still exists separately from
	// newGmailClient. Most of the app was written when every user was a Gmail
	// user, and those paths have not been ported. Refusing here means each of
	// them answers "not on this transport yet" instead of sending an IMAP UID
	// to Gmail as a message id, which would act on an unrelated message and
	// journal it as a success. Every path that IS ported opens a session
	// instead and never comes through here.
	transport, _, err := h.transportFor(ctx, userEmail)
	if err != nil {
		return nil, err
	}
	if transport != provider.TransportGmailAPI {
		return nil, fmt.Errorf("%w: mailbox is reached over %s", errWrongTransport, transport)
	}
	return h.newGmailClient(ctx, userEmail)
}

// newGmailClient builds the client without asking which transport the user is
// on. Only openSession may call it, because openSession has already asked.
func (h *Handler) newGmailClient(ctx context.Context, userEmail string) (*gmailapi.Service, error) {
	token, err := h.getUserToken(ctx, userEmail)
	if err != nil {
		return nil, err
	}
	return h.gmailService.GetClient(token), nil
}

// BillingConfig wires the Stripe client and its environment-derived settings
// into the handler. Client is nil when Stripe is not configured.
type BillingConfig struct {
	Client        *billing.Client
	PriceID       string
	WebhookSecret string
	AppBaseURL    string
}

// Version identifies the running build. It defaults to "dev" and is overridden
// from configuration (BUILD_VERSION) at startup so /health and /metrics can
// report exactly which build is live.
var Version = "dev"

// Edition is which distribution is running, set from configuration at startup.
// It gates which mailbox providers this instance can offer: see
// internal/provider, and the catalog served by GET /api/providers.
var Edition = provider.EditionSelfHosted

// UITheme is the look of the SPA (landing and dashboard) and of the digest
// email, set from configuration at startup (UI_THEME) and reported by
// GET /api/config/status.
var UITheme = config.UIThemeHotel

// AllowedOrigins is the CORS allow-list applied by SetupRoutes. It defaults to
// the local-dev + public origins and is overridden from configuration
// (ALLOWED_ORIGINS) at startup, so deploying on a new domain needs no rebuild.
var AllowedOrigins = []string{
	"http://localhost:3000",
	"http://localhost",
	"https://mailsorter.sohbi.dev",
}

type Handler struct {
	db           *database.Database
	gmailService *gmail.Service
	encryptor    *crypto.Encryptor
	aiRegistry   *ai.Registry
	billing      BillingConfig
	auth         *auth.Manager
	jobQueue     chan string
	metrics      *metrics.Registry
	startedAt    time.Time

	// The sign-in limiter is built on first use rather than in the constructor,
	// so it exists whatever built this Handler. A nil limiter would not fail
	// loudly, it would silently lift the one throttle standing between an
	// unauthenticated caller and somebody else's mail provider.
	signInOnce sync.Once
	signIn     *rateLimiter
	// The test-send limiter (digestTestLimiter), built on first use for the
	// same reason.
	digestTestOnce sync.Once
	digestTest     *rateLimiter
}

// signInLimiter is the throttle on the public mailbox sign-in. See signInLimit
// in auth_mailbox.go for why it is so much tighter than the global one.
func (h *Handler) signInLimiter() *rateLimiter {
	h.signInOnce.Do(func() {
		h.signIn = newRateLimiter(signInRatePerSec, signInBurst)
	})
	return h.signIn
}

func NewHandler(db *database.Database, gmailService *gmail.Service, encryptor *crypto.Encryptor, aiRegistry *ai.Registry, billingCfg BillingConfig, authManager *auth.Manager) *Handler {
	h := &Handler{
		db:           db,
		gmailService: gmailService,
		encryptor:    encryptor,
		aiRegistry:   aiRegistry,
		billing:      billingCfg,
		auth:         authManager,
		jobQueue:     make(chan string, 256),
		metrics:      metrics.New(),
		startedAt:    time.Now(),
	}
	// Background pool that drains async analysis jobs.
	h.startAnalysisWorkers(3)
	// Background sweeper that returns due snoozed emails to the inbox.
	h.startSnoozeLoop()
	// Background scheduler that sends the daily email digest to opted-in users.
	h.startDigestLoop()
	// Background scheduler that periodically syncs opted-in users' inboxes.
	h.startAutoSyncLoop()
	return h
}

// HealthCheck is a readiness probe: it verifies the process can still reach
// MongoDB (not just that the HTTP server is up) and reports the running build
// and uptime. A failed datastore ping yields 503 so an orchestrator can pull
// the instance out of rotation instead of routing traffic it can't serve.
func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	status := "ok"
	code := http.StatusOK
	dbOK := h.db.Client.Ping(ctx, nil) == nil
	if !dbOK {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":        status,
		"version":       Version,
		"uptimeSeconds": int64(time.Since(h.startedAt).Seconds()),
		"checks":        map[string]bool{"mongo": dbOK},
	})
}

// Metrics exposes the in-process request meter (counts by method and status
// class, latency, uptime). It is intentionally aggregate-only (no user data),
// so it can be scraped without authentication, the way an ops endpoint expects.
func (h *Handler) Metrics(w http.ResponseWriter, r *http.Request) {
	snap := h.metrics.Snapshot()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"version": Version,
		"metrics": snap,
	})
}

// metricsMiddleware records each completed request into the registry. It runs
// inside the chain so it sees the final status code captured by the recorder.
func (h *Handler) metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		h.metrics.Observe(r.Method, rec.status, time.Since(start))
	})
}

// Auth endpoints
func (h *Handler) GetAuthURL(w http.ResponseWriter, r *http.Request) {
	// Signed, expiring state to prevent CSRF on the OAuth callback.
	// If the user is already authenticated on MailSorter, embed their email
	// in the state so the callback binds this mailbox to their account.
	userEmail := r.Header.Get("X-User-Email")
	state := h.auth.IssueStateFor(userEmail)

	// ?reconnect=1 is the "Reconnecter Gmail" path in the settings: the caller
	// already has an account and is repairing a grant, which needs the consent
	// screen to get a refresh token back. A first login does not force consent,
	// so nothing changes for new users.
	authURL := h.gmailService.GetAuthURL(state)
	if r.URL.Query().Get("reconnect") == "1" {
		authURL = h.gmailService.GetReconnectURL(state)
	}

	writeJSON(w, http.StatusOK, models.AuthResponse{AuthURL: authURL})
}

func (h *Handler) HandleAuthCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		writeError(w, http.StatusBadRequest, "No code provided")
		return
	}

	// Reject the callback unless it carries the signed state we issued. This is
	// what stops a forged redirect (CSRF) from completing a login.
	state := r.URL.Query().Get("state")
	linkedUserEmail, err := h.auth.VerifyStateEmail(state)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid OAuth state")
		return
	}

	token, err := h.gmailService.ExchangeCode(code)
	if err != nil {
		log.Printf("auth callback: failed to exchange code: %v", err)
		writeError(w, http.StatusInternalServerError, "Failed to exchange code")
		return
	}

	gmailClient := h.gmailService.GetClient(token)
	gmailUserEmail, err := h.gmailService.GetUserProfile(gmailClient)
	if err != nil {
		log.Printf("auth callback: failed to get user profile: %v", err)
		writeError(w, http.StatusInternalServerError, "Failed to get user profile")
		return
	}

	accountOwner := gmailUserEmail
	if linkedUserEmail != "" {
		accountOwner = linkedUserEmail
	}

	// Store user in database
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Both tokens are encrypted before they touch the database: see tokens.go for
	// why they are the one thing here that must never sit at rest in the clear.
	sealedAccess, err := h.sealToken(token.AccessToken)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to secure credentials")
		return
	}
	sealedRefresh, err := h.sealToken(token.RefreshToken)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to secure credentials")
		return
	}

	filter := bson.M{"email": accountOwner}
	set := bson.M{
		"accessToken": sealedAccess,
		"tokenExpiry": token.Expiry,
		"updatedAt":   time.Now(),
	}
	// Google issues a refresh token only on the FIRST authorization for a given
	// client and user, unless consent is forced. Every later authorization comes
	// back with an empty one, and writing that over the stored token destroyed
	// the account: the access token kept working for about an hour, then nothing
	// could be refreshed and there was no way back from inside the app. Keep
	// what we have unless Google actually hands us a new one.
	if sealedRefresh != "" {
		set["refreshToken"] = sealedRefresh
	}
	update := bson.M{
		"$set": set,
		"$setOnInsert": bson.M{
			"email":     accountOwner,
			"createdAt": time.Now(),
		},
	}

	opts := options.Update().SetUpsert(true)
	_, err = h.db.Users().UpdateOne(ctx, filter, update, opts)
	if err != nil {
		log.Printf("auth callback: failed to save user: %v", err)
		writeError(w, http.StatusInternalServerError, "Failed to save user")
		return
	}

	// Also record in mail_accounts that this user's active mailbox is Google Gmail.
	// A mailbox connected over IMAP before is another mailbox: its row goes,
	// and with it what Mailsorter kept of it (see generation.go).
	if _, err := h.db.MailAccounts().DeleteOne(ctx, bson.M{"userId": accountOwner, "transport": bson.M{"$ne": "gmail"}}); err != nil {
		log.Printf("auth callback: replace the mailbox of %s: %v", accountOwner, err)
	}
	now := time.Now()
	_, _ = h.db.MailAccounts().UpdateOne(ctx,
		bson.M{"userId": accountOwner},
		bson.M{
			"$set": bson.M{
				"provider":  "google",
				"transport": "gmail",
				"username":  gmailUserEmail,
				"host":      "imap.gmail.com",
				"port":      993,
				"tls":       "tls",
				"updatedAt": now,
			},
			"$setOnInsert": bson.M{"userId": accountOwner, "createdAt": now},
		},
		options.Update().SetUpsert(true),
	)
	if err := h.clearMailboxCopy(ctx, accountOwner); err != nil {
		log.Printf("auth callback: erase what was kept of the mailbox of %s before, done at the next connection: %v", accountOwner, err)
	}

	// Hand the browser our own signed session token, never the raw Gmail
	// access token, which must stay server-side.
	sessionToken := h.auth.IssueSession(accountOwner)

	writeJSON(w, http.StatusOK, models.TokenResponse{
		AccessToken: sessionToken,
		UserEmail:   accountOwner,
	})
}

// Email endpoints
func (h *Handler) GetEmails(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	query := r.URL.Query().Get("q")
	if query == "" {
		query = "in:inbox"
	}

	// Parse maxResults (default 50, max 500)
	maxResults := int64(50)
	if maxStr := r.URL.Query().Get("maxResults"); maxStr != "" {
		if parsed, err := parseInt64(maxStr); err == nil && parsed > 0 {
			maxResults = parsed
			if maxResults > 500 {
				maxResults = 500
			}
		}
	}

	// Get page token for pagination
	pageToken := r.URL.Query().Get("pageToken")

	transport, account, err := h.transportFor(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	if transport == provider.TransportIMAP {
		// Served from the stored mailbox rather than from the provider: see the
		// file comment in mirror.go for why a listing does not open a socket.
		page, err := h.listFromMirror(ctx, userEmail, generationOf(transport, account), query, maxResults, pageToken)
		if err != nil {
			var unsupported errUnsupportedQuery
			if errors.As(err, &unsupported) {
				writeError(w, http.StatusNotImplemented,
					"Ce filtre n'est pas encore disponible sur une boîte branchée en IMAP : "+
						strings.Join(unsupported.terms, " "))
				return
			}
			writeError(w, http.StatusInternalServerError, "Failed to fetch emails: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, page)
		return
	}

	gmailClient, err := h.newGmailClient(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	// Metadata only: this listing renders the sender, the subject and the snippet,
	// and never touches Email.Body. Asking for full payloads here downloaded every
	// MIME part of every message on screen, which is both the wait the user felt
	// and the bulk of the account's Gmail quota.
	resp, err := h.gmailService.ListMessagesWithPagination(gmailClient, query, maxResults, pageToken, gmail.FieldsMetadata)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to fetch emails: "+err.Error())
		return
	}

	emails := make([]models.Email, 0)
	for _, msg := range resp.Messages {
		from, subject, to, date := gmail.ParseEmailHeaders(msg)
		unsubURL, unsubMailto, oneClick := gmail.ParseUnsubscribe(msg)

		email := models.Email{
			MessageID:     msg.Id,
			UserID:        userEmail,
			ThreadID:      msg.ThreadId,
			From:          from,
			To:            to,
			Subject:       subject,
			Snippet:       gmail.Snippet(msg),
			LabelIDs:      msg.LabelIds,
			ReceivedDate:  date,
			IsRead:        mailbox.GmailIsRead(msg.LabelIds),
			UnsubURL:      unsubURL,
			UnsubMailto:   unsubMailto,
			UnsubOneClick: oneClick,
			CreatedAt:     time.Now(),
		}
		emails = append(emails, email)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"emails":             emails,
		"nextPageToken":      resp.NextPageToken,
		"resultSizeEstimate": resp.ResultSizeEstimate,
	})
}

// GetMailboxStats returns mailbox statistics
func (h *Handler) GetMailboxStats(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	transport, account, err := h.transportFor(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	if transport == provider.TransportIMAP {
		stats, err := h.statsFromMirror(ctx, userEmail, generationOf(transport, account))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to get mailbox stats: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, stats)
		return
	}

	gmailClient, err := h.newGmailClient(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	stats, err := h.gmailService.GetMailboxStats(gmailClient)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to get mailbox stats: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

func (h *Handler) SyncEmails(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "Connexion requise.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), syncRequestTimeout)
	defer cancel()

	synced, total, ruled, err := h.syncInbox(ctx, userEmail)
	if err != nil {
		// Every answer here is a French sentence the inbox shows as is
		// (errorMessage in EmailContext.js): what failed is logged.
		switch {
		case errors.Is(err, errReauthRequired), errors.Is(err, imap.ErrAuth), errors.Is(err, errMailboxChanged):
			// A dead Google grant, and an IMAP password the provider
			// refuses, in the words of the connect screen; a screen loaded
			// on another mailbox (409).
			writeAuthError(w, err)
		case errors.Is(err, errMailboxNoAnswer):
			writeSyncError(w, http.StatusBadGateway, mailboxNoAnswerMessage)
		case errors.Is(err, errNoMailboxConnected):
			// The mailbox to connect: the inbox says so, with the way there.
			writeSyncError(w, http.StatusPreconditionRequired, noMailboxMessage)
		default:
			writeSyncError(w, http.StatusInternalServerError, syncFailureMessage(userEmail, err))
		}
		return
	}

	out := map[string]interface{}{
		"synced":       synced,
		"total":        total,
		"rulesApplied": ruled.applied,
		// Mails the rules could not judge at this sync, over IMAP: the sync
		// worked all the same, so this is information, not an error.
		"rulesPending": ruled.pending,
	}
	// The mails the rules gave up on, whichever sync gave them up (the
	// background one included), are told once, to a sync that asks for it
	// (?notice=1): the screen sends it from where it shows the answer, the
	// "Synchroniser" button and the page load. A sync whose answer nobody
	// shows (a refresh after "Annuler", a search) leaves the count for later.
	givenUp := 0
	if r.URL.Query().Get("notice") == "1" {
		givenUp = h.takeGivenUp(ctx, userEmail)
	}
	if msg := rulesMessage(ruled.pending, givenUp); msg != "" {
		out["rulesMessage"] = msg
	}
	writeJSON(w, http.StatusOK, out)
}

// writeSyncError answers a failed "Synchroniser" with a sentence written for
// the user, marked syncMessage: the inbox shows a 5xx sentence only when it
// carries the mark, so the "Internal server error" of a panic (or anything
// else a 5xx may say) never reaches the screen.
func writeSyncError(w http.ResponseWriter, status int, sentence string) {
	writeJSON(w, status, map[string]interface{}{"error": sentence, "status": status, "syncMessage": true})
}

// syncFailureMessage is what "Synchroniser" says of a failure: the sentence of
// one the user is meant to read (errMailboxUnavailable, errMailboxLost,
// errMirrorNotReconciled, errMessagesNotStored), or else a general one, the
// detail logged rather than shown (it may be the English of a library).
func syncFailureMessage(userEmail string, err error) string {
	for _, said := range []error{errMailboxUnavailable, errMailboxLost, errMirrorNotReconciled, errMessagesNotStored} {
		if errors.Is(err, said) {
			return asSentence(err.Error())
		}
	}
	log.Printf("sync: %s: %v", userEmail, err)
	return "La synchronisation a échoué, réessayez dans un instant."
}

// asSentence starts s with a capital letter and ends it with a period: the
// errors of a sync are written to follow another sentence.
func asSentence(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	s = string(unicode.ToUpper(r)) + s[size:]
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

// syncRequestTimeout is how long "Synchroniser" waits for the mailbox. An
// IMAP session is bound to it (imap.Connect): past it, the connection is
// dropped and the sync answers errMailboxNoAnswer. A variable, so a test can
// shorten it.
var syncRequestTimeout = 30 * time.Second

// syncInbox mirrors the caller's inbox into Mongo, through whichever transport
// their mailbox is reached by, and, when the user opted into rule autopilot
// (autoApplyRules), applies their deterministic rules (instant, AI-free,
// quota-free). It is shared by the manual SyncEmails handler and the
// background auto-sync scheduler so both behave identically. It returns how
// many emails were persisted, how many were seen, and how many had a rule
// applied, could not be judged yet, or were given up (syncRulesDone).
//
// This is the fork the whole edition split was for. Everything above it (the
// handler, the auto-sync loop) asks for a sync and gets one; only this function
// knows there are two ways to perform it. The rules do not judge the same
// messages on both: every message listed over the Gmail API, only the ones
// that arrive over IMAP (see syncInboxIMAP).
func (h *Handler) syncInbox(ctx context.Context, userEmail string) (synced, total int, ruled syncRulesDone, err error) {
	session, err := h.openSession(ctx, userEmail)
	if errors.Is(err, imap.ErrUnreachable) {
		// Not a password refused: the mailbox did not answer, or not in
		// time. Logged with what it was; the user is told to try again.
		log.Printf("sync: the mailbox of %s could not be opened: %v", userEmail, err)
		return 0, 0, syncRulesDone{}, errMailboxNoAnswer
	}
	if err != nil {
		return 0, 0, syncRulesDone{}, err
	}
	defer session.Close()

	if session.Transport == provider.TransportIMAP {
		synced, total, ruled, err = h.syncInboxIMAP(ctx, userEmail, session)
		if err != nil && ctx.Err() != nil {
			// Its time ran out (the session is bound to ctx): the mailbox
			// did not answer in time, whatever the command it stopped in.
			log.Printf("sync: the mailbox of %s did not answer in time: %v", userEmail, err)
			return synced, total, ruled, errMailboxNoAnswer
		}
		return synced, total, ruled, err
	}
	return h.syncInboxGmail(ctx, userEmail, session)
}

func (h *Handler) syncInboxGmail(ctx context.Context, userEmail string, session *mailSession) (synced, total int, ruled syncRulesDone, err error) {
	messages, err := h.gmailService.ListMessages(session.gmail, "in:inbox", 100)
	if err != nil {
		return 0, 0, syncRulesDone{}, err
	}

	// Autopilot: when the user opted in, run their deterministic rules over each
	// freshly synced email: instant, AI-free, quota-free triage at sync time.
	var autoRules []models.SortingRule
	var protectedList []string
	if h.autoApplyRulesEnabled(ctx, userEmail) {
		autoRules = h.enabledRules(ctx, userEmail)
		protectedList = h.protectedValues(ctx, userEmail)
	}
	labelCache := map[string]string{}
	byRule := map[string]int{}

	for _, msg := range messages {
		from, subject, to, date := gmail.ParseEmailHeaders(msg)
		body := gmail.GetEmailBody(msg)
		unsubURL, unsubMailto, oneClick := gmail.ParseUnsubscribe(msg)

		email := models.Email{
			MessageID:     msg.Id,
			UserID:        userEmail,
			ThreadID:      msg.ThreadId,
			From:          from,
			To:            to,
			Subject:       subject,
			Body:          body,
			Snippet:       gmail.Snippet(msg),
			LabelIDs:      msg.LabelIds,
			ReceivedDate:  date,
			IsRead:        mailbox.GmailIsRead(msg.LabelIds),
			UnsubURL:      unsubURL,
			UnsubMailto:   unsubMailto,
			UnsubOneClick: oneClick,
			CreatedAt:     time.Now(),
		}

		filter := bson.M{"messageId": msg.Id, "userId": userEmail}
		update := bson.M{"$set": email}
		opts := options.Update().SetUpsert(true)
		if _, uErr := h.db.Emails().UpdateOne(ctx, filter, update, opts); uErr == nil {
			synced++
		}

		if len(autoRules) > 0 {
			if match := rules.FirstMatch(email, autoRules); match != nil {
				appliedActs, _, _ := h.applyRuleToMessage(ctx, session, userEmail, email, *match, protectedList, labelCache, nil)
				if len(appliedActs) > 0 {
					ruled.applied++
					byRule[match.Name]++
					for _, act := range appliedActs {
						h.logActionFiled(ctx, userEmail, "", msg.Id, act.Type, SourceRule, email.Subject, email.From, act.LabelName)
					}
				}
			}
		}
	}

	h.countRuleHits(ctx, userEmail, byRule)

	return synced, len(messages), ruled, nil
}

// reversedBy names, for each inverse a client sends right after an action, the
// action as the ledger recorded it ("trash" is recorded as "delete"). "unlabel"
// is what "Annuler" sends after a proposal filed a mail in a folder over IMAP.
var reversedBy = map[string]string{"unarchive": "archive", "untrash": "delete", "unlabel": "label"}

// labelRequiredMessage is what POST /api/emails/action answers for "label"
// (and, over the Gmail API, "unlabel"), which need the name of a folder or a
// label that route does not carry.
const labelRequiredMessage = "Cette action demande le nom d'un dossier ou d'une étiquette, que cette route ne reçoit pas."

// EmailAction performs a direct action on a single message, over whichever
// transport the mailbox uses (archive, trash, mark read/unread), without going
// through AI suggestions.
func (h *Handler) EmailAction(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	var req models.EmailActionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.MessageID == "" {
		writeError(w, http.StatusBadRequest, "Message ID required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	session, err := h.openSession(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	defer session.Close()

	// The verb vocabulary and its translation per transport live in
	// internal/mailbox. Starring was reachable over the batch route but not
	// here, so the single message path (what the reader and the shortcuts use)
	// could not express "flag this one"; one vocabulary for both is what fixed
	// that, and what stops the two from drifting again.
	//
	// The reference is built by the session rather than here, because what a
	// message id MEANS depends on the transport and this handler has no opinion
	// about that: it holds a string the client sent.
	back := func() error {
		return h.applyVerb(ctx, session.Mailbox(), session.RefFor(ctx, req.MessageID), req.Action, "")
	}
	if returnsToInbox(req.Action, session.Transport) {
		// "Annuler" right after a move out: on a reported mail, it ends the
		// report (endReportOnReturn).
		err = h.endReportOnReturn(ctx, userEmail, session.generation(), session.account.CreatedAt, req.MessageID, back)
	} else {
		err = back()
	}
	if errors.Is(err, errWakeBusy) {
		writeError(w, http.StatusLocked, wakeBusyMessage)
		return
	}
	if errors.Is(err, imap.ErrAmbiguous) {
		writeSeveralCopies(w)
		return
	}
	if errors.Is(err, mailbox.ErrUnknownAction) {
		writeError(w, http.StatusBadRequest, "Unsupported action")
		return
	}
	if errors.Is(err, mailbox.ErrLabelRequired) {
		// This route takes no label nor folder name: classifying a mail goes
		// through an AI proposal or the batch route, which carry one.
		writeError(w, http.StatusBadRequest, labelRequiredMessage)
		return
	}
	if errors.Is(err, errNotWhereLeft) {
		writeError(w, http.StatusNotFound, notWhereLeftMessage)
		return
	}
	if errors.Is(err, errUndoExpired) {
		writeError(w, http.StatusNotFound, undoExpiredMessage)
		return
	}
	if errors.Is(err, errLeftInbox) {
		writeError(w, http.StatusNotFound, leftInboxMessage)
		return
	}
	if errors.Is(err, errAlreadyOut) {
		// It had already left the inbox (moved there by Mailsorter, or by
		// another mail client): nothing happened, so nothing is recorded, and
		// the screen is told so rather than announce a move.
		writeJSON(w, http.StatusOK, map[string]string{"status": "already", "action": req.Action})
		return
	}
	if errors.Is(err, errUndoBusy) {
		writeError(w, http.StatusLocked, undoBusyMessage)
		return
	}
	if errors.Is(err, errAlreadyBack) {
		// Another "Annuler" brought it back a moment ago and recorded it.
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "action": req.Action})
		return
	}
	if err == nil {
		// "Annuler" right after an action sends its inverse here. Mark the
		// action it reverses undone, as the history and batch-undo do, or the
		// history keeps offering "Annuler" on work already reversed.
		if forward, ok := reversedBy[req.Action]; ok {
			h.recordUndo(ctx, userEmail, session.generation(), req.MessageID, forward, req.Action)
		}
	}

	if err != nil {
		writeError(w, http.StatusInternalServerError, "L'action n'a pas pu être appliquée : "+err.Error())
		return
	}

	// Record forward triage actions in the ledger (undo actions are not counted).
	// One lookup, only on the branches that actually log: the history is useless
	// without the subject, and resolving it here rather than at read time is what
	// lets the entry be found by search later.
	switch req.Action {
	case "archive", "delete", "trash", "read", "star":
		action := req.Action
		if action == "trash" {
			action = "delete"
		}
		meta := h.identityIn(ctx, session, userEmail, req.MessageID)
		h.logActionMeta(ctx, userEmail, session.generation(), req.MessageID, action, SourceDirect, meta.Subject, meta.From)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "action": req.Action})
}

// folderLabel is one of the user's own folders of an IMAP mailbox, in the
// shape of a Gmail label, which is what every screen offering one reads: over
// IMAP the folder stands for the label, so its full name is both its id and
// its name, and it is the user's ("user"), never a system one.
type folderLabel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// Labels endpoints
//
// GetLabels lists the labels a mail can be filed under: the Gmail labels, or
// over IMAP the user's own folders (not the inbox nor a special folder,
// imap.Client.OwnFolders), a folder inside another by its full name.
func (h *Handler) GetLabels(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if transport, _, err := h.transportFor(ctx, userEmail); err == nil && transport == provider.TransportIMAP {
		session, err := h.openSession(ctx, userEmail)
		if err != nil {
			writeAuthError(w, err)
			return
		}
		defer session.Close()
		folders := session.imapc.OwnFolders()
		labels := make([]folderLabel, 0, len(folders))
		for _, name := range folders {
			labels = append(labels, folderLabel{ID: name, Name: name, Type: "user"})
		}
		writeJSON(w, http.StatusOK, labels)
		return
	}

	gmailClient, err := h.gmailClientFor(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	labels, err := h.gmailService.ListLabels(gmailClient)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to fetch labels: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, labels)
}

// Config endpoints

// GetConfigStatus reports what this instance is: whether it holds usable Gmail
// credentials, whether a mailbox can sign in, billing, edition and UI theme.
//
// It answers from the live OAuth client rather than from the database, so it
// reflects what the server is actually able to do, whichever source those
// credentials came from at boot (environment variables first, legacy stored
// document as fallback). The credentials themselves are never readable or
// writable over HTTP; see cmd/server/main.go for how they are loaded.
func (h *Handler) GetConfigStatus(w http.ResponseWriter, r *http.Request) {
	status := models.InstanceStatus{
		IsConfigured:  h.gmailService.IsConfigured(),
		MailboxSignIn: mailboxSignInAvailable(),
		BillingOn:     h.billingEnabled(),
		Edition:       string(Edition),
		UITheme:       string(UITheme),
	}

	writeJSON(w, http.StatusOK, status)
}

// mailboxSignInAvailable reports whether this edition can reach any mailbox
// over IMAP, which is what POST /api/auth/mailbox needs to be able to do
// anything. Read from the catalog rather than from the edition name: the
// question is which routes exist, and internal/provider is the one place that
// knows.
func mailboxSignInAvailable() bool {
	for _, p := range provider.ForEdition(Edition) {
		for _, route := range p.Routes {
			if route.Transport == provider.TransportIMAP {
				return true
			}
		}
	}
	return false
}

// Helper functions
func parseInt64(s string) (int64, error) {
	var n int64
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}
