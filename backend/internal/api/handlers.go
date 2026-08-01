package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/ai"
	"github.com/nohe-sohbi/mailsorter/backend/internal/auth"
	"github.com/nohe-sohbi/mailsorter/backend/internal/billing"
	"github.com/nohe-sohbi/mailsorter/backend/internal/crypto"
	"github.com/nohe-sohbi/mailsorter/backend/internal/database"
	"github.com/nohe-sohbi/mailsorter/backend/internal/gmail"
	"github.com/nohe-sohbi/mailsorter/backend/internal/metrics"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
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
	aiClient     *ai.MistralClient
	billing      BillingConfig
	auth         *auth.Manager
	jobQueue     chan string
	metrics      *metrics.Registry
	startedAt    time.Time
}

func NewHandler(db *database.Database, gmailService *gmail.Service, encryptor *crypto.Encryptor, aiClient *ai.MistralClient, billingCfg BillingConfig, authManager *auth.Manager) *Handler {
	h := &Handler{
		db:           db,
		gmailService: gmailService,
		encryptor:    encryptor,
		aiClient:     aiClient,
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
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
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
	// Signed, expiring state to prevent CSRF on the OAuth callback. It is
	// stateless: the callback verifies the signature, no server storage needed.
	state := h.auth.IssueState()

	// ?reconnect=1 is the "Reconnecter Gmail" path in the settings: the caller
	// already has an account and is repairing a grant, which needs the consent
	// screen to get a refresh token back. A first login does not force consent,
	// so nothing changes for new users.
	authURL := h.gmailService.GetAuthURL(state)
	if r.URL.Query().Get("reconnect") == "1" {
		authURL = h.gmailService.GetReconnectURL(state)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.AuthResponse{AuthURL: authURL})
}

func (h *Handler) HandleAuthCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "No code provided", http.StatusBadRequest)
		return
	}

	// Reject the callback unless it carries the signed state we issued. This is
	// what stops a forged redirect (CSRF) from completing a login.
	state := r.URL.Query().Get("state")
	if err := h.auth.VerifyState(state); err != nil {
		http.Error(w, "Invalid OAuth state", http.StatusBadRequest)
		return
	}

	token, err := h.gmailService.ExchangeCode(code)
	if err != nil {
		http.Error(w, "Failed to exchange code: "+err.Error(), http.StatusInternalServerError)
		return
	}

	gmailClient := h.gmailService.GetClient(token)
	userEmail, err := h.gmailService.GetUserProfile(gmailClient)
	if err != nil {
		http.Error(w, "Failed to get user profile: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Store user in database
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	filter := bson.M{"email": userEmail}
	set := bson.M{
		"accessToken": token.AccessToken,
		"tokenExpiry": token.Expiry,
		"updatedAt":   time.Now(),
	}
	// Google issues a refresh token only on the FIRST authorization for a given
	// client and user, unless consent is forced. Every later authorization comes
	// back with an empty one — and writing that over the stored token destroyed
	// the account: the access token kept working for about an hour, then nothing
	// could be refreshed and there was no way back from inside the app. Keep
	// what we have unless Google actually hands us a new one.
	if token.RefreshToken != "" {
		set["refreshToken"] = token.RefreshToken
	}
	update := bson.M{
		"$set": set,
		"$setOnInsert": bson.M{
			"email":     userEmail,
			"createdAt": time.Now(),
		},
	}

	opts := options.Update().SetUpsert(true)
	_, err = h.db.Users().UpdateOne(ctx, filter, update, opts)
	if err != nil {
		http.Error(w, "Failed to save user: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Hand the browser our own signed session token, never the raw Gmail
	// access token, which must stay server-side.
	sessionToken := h.auth.IssueSession(userEmail)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.TokenResponse{
		AccessToken: sessionToken,
		UserEmail:   userEmail,
	})
}

// Email endpoints
func (h *Handler) GetEmails(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	gmailClient, err := h.gmailClientFor(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}

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

	resp, err := h.gmailService.ListMessagesWithPagination(gmailClient, query, maxResults, pageToken)
	if err != nil {
		http.Error(w, "Failed to fetch emails: "+err.Error(), http.StatusInternalServerError)
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
			Snippet:       msg.Snippet,
			LabelIDs:      msg.LabelIds,
			ReceivedDate:  date,
			IsRead:        !contains(msg.LabelIds, "UNREAD"),
			UnsubURL:      unsubURL,
			UnsubMailto:   unsubMailto,
			UnsubOneClick: oneClick,
			CreatedAt:     time.Now(),
		}
		emails = append(emails, email)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"emails":             emails,
		"nextPageToken":      resp.NextPageToken,
		"resultSizeEstimate": resp.ResultSizeEstimate,
	})
}

// GetMailboxStats returns mailbox statistics
func (h *Handler) GetMailboxStats(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	gmailClient, err := h.gmailClientFor(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	stats, err := h.gmailService.GetMailboxStats(gmailClient)
	if err != nil {
		http.Error(w, "Failed to get mailbox stats: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func (h *Handler) SyncEmails(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	synced, total, rulesApplied, err := h.syncInbox(ctx, userEmail)
	if err != nil {
		if errors.Is(err, errReauthRequired) {
			writeAuthError(w, err)
			return
		}
		http.Error(w, "Failed to sync emails: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"synced":       synced,
		"total":        total,
		"rulesApplied": rulesApplied,
	})
}

// syncInbox pulls the user's inbox into MongoDB and, when the user opted into
// rule autopilot, applies their deterministic rules to each freshly synced email
// (instant, AI-free, quota-free). It is shared by the manual SyncEmails handler
// and the background auto-sync scheduler so both behave identically. It returns
// how many emails were persisted, how many were seen, and how many had a rule
// applied.
func (h *Handler) syncInbox(ctx context.Context, userEmail string) (synced, total, rulesApplied int, err error) {
	gmailClient, err := h.gmailClientFor(ctx, userEmail)
	if err != nil {
		return 0, 0, 0, err
	}

	messages, err := h.gmailService.ListMessages(gmailClient, "in:inbox", 100)
	if err != nil {
		return 0, 0, 0, err
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
			Snippet:       msg.Snippet,
			LabelIDs:      msg.LabelIds,
			ReceivedDate:  date,
			IsRead:        !contains(msg.LabelIds, "UNREAD"),
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
				appliedActs, _ := h.applyRuleToMessage(ctx, gmailClient, userEmail, msg.Id, email.From, *match, protectedList, labelCache)
				if len(appliedActs) > 0 {
					rulesApplied++
					byRule[match.Name]++
					for _, act := range appliedActs {
						h.logActionMeta(ctx, userEmail, msg.Id, act, SourceRule, email.Subject, email.From)
					}
				}
			}
		}
	}

	// Persist per-rule application counts (best-effort).
	for name, n := range byRule {
		h.db.SortingRules().UpdateOne(ctx,
			bson.M{"userId": userEmail, "name": name},
			bson.M{"$inc": bson.M{"appliedCount": n}},
		)
	}

	return synced, len(messages), rulesApplied, nil
}

// EmailAction performs a direct action on a single Gmail message
// (archive, trash, mark read/unread) without going through AI suggestions.
func (h *Handler) EmailAction(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	var req models.EmailActionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.MessageID == "" {
		http.Error(w, "Message ID required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	gmailClient, err := h.gmailClientFor(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	switch req.Action {
	case "archive":
		err = h.gmailService.ModifyMessage(gmailClient, req.MessageID, nil, []string{"INBOX"})
	case "delete", "trash":
		err = h.gmailService.ModifyMessage(gmailClient, req.MessageID, []string{"TRASH"}, nil)
	case "unarchive":
		err = h.gmailService.ModifyMessage(gmailClient, req.MessageID, []string{"INBOX"}, nil)
	case "untrash":
		err = h.gmailService.ModifyMessage(gmailClient, req.MessageID, []string{"INBOX"}, []string{"TRASH"})
	case "read":
		err = h.gmailService.ModifyMessage(gmailClient, req.MessageID, nil, []string{"UNREAD"})
	case "unread":
		err = h.gmailService.ModifyMessage(gmailClient, req.MessageID, []string{"UNREAD"}, nil)
	default:
		http.Error(w, "Unsupported action", http.StatusBadRequest)
		return
	}

	if err != nil {
		http.Error(w, "Failed to apply action: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Record forward triage actions in the ledger (undo actions are not counted).
	switch req.Action {
	case "archive":
		h.logAction(ctx, userEmail, req.MessageID, "archive", SourceDirect)
	case "delete", "trash":
		h.logAction(ctx, userEmail, req.MessageID, "delete", SourceDirect)
	case "read":
		h.logAction(ctx, userEmail, req.MessageID, "read", SourceDirect)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "action": req.Action})
}

// Labels endpoints
func (h *Handler) GetLabels(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	gmailClient, err := h.gmailClientFor(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	labels, err := h.gmailService.ListLabels(gmailClient)
	if err != nil {
		http.Error(w, "Failed to fetch labels: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(labels)
}

// Config endpoints

// GetConfigStatus reports whether the instance holds usable Gmail credentials.
//
// It answers from the live OAuth client rather than from the database, so it
// reflects what the server is actually able to do, whichever source those
// credentials came from at boot (environment variables first, legacy stored
// document as fallback). The credentials themselves are never readable or
// writable over HTTP; see cmd/server/main.go for how they are loaded.
func (h *Handler) GetConfigStatus(w http.ResponseWriter, r *http.Request) {
	status := models.InstanceStatus{
		IsConfigured: h.gmailService.IsConfigured(),
		BillingOn:    h.billingEnabled(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// Helper functions
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func parseInt64(s string) (int64, error) {
	var n int64
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}
