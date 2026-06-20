package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/ai"
	"github.com/nohe-sohbi/mailsorter/backend/internal/auth"
	"github.com/nohe-sohbi/mailsorter/backend/internal/billing"
	"github.com/nohe-sohbi/mailsorter/backend/internal/crypto"
	"github.com/nohe-sohbi/mailsorter/backend/internal/database"
	"github.com/nohe-sohbi/mailsorter/backend/internal/gmail"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
	gmailapi "google.golang.org/api/gmail/v1"
)

// gmailClientFor returns an authenticated Gmail client for the user. It routes
// through getUserToken, so the OAuth token is refreshed and persisted when
// expired — previously this logic was copy-pasted across handlers (and missing
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

type Handler struct {
	db           *database.Database
	gmailService *gmail.Service
	encryptor    *crypto.Encryptor
	aiClient     *ai.MistralClient
	billing      BillingConfig
	auth         *auth.Manager
	jobQueue     chan string
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
	}
	// Background pool that drains async analysis jobs.
	h.startAnalysisWorkers(3)
	return h
}

// Health check
func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// Auth endpoints
func (h *Handler) GetAuthURL(w http.ResponseWriter, r *http.Request) {
	// Signed, expiring state to prevent CSRF on the OAuth callback. It is
	// stateless: the callback verifies the signature, no server storage needed.
	state := h.auth.IssueState()
	authURL := h.gmailService.GetAuthURL(state)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.AuthResponse{AuthURL: authURL})
}

func (h *Handler) HandleAuthCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "No code provided", http.StatusBadRequest)
		return
	}

	// Reject the callback unless it carries the signed state we issued — this is
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
	update := bson.M{
		"$set": bson.M{
			"accessToken":  token.AccessToken,
			"refreshToken": token.RefreshToken,
			"tokenExpiry":  token.Expiry,
			"updatedAt":    time.Now(),
		},
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

	// Hand the browser our own signed session token — never the raw Gmail
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
		http.Error(w, "User not found", http.StatusNotFound)
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
		http.Error(w, "User not found", http.StatusNotFound)
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

	gmailClient, err := h.gmailClientFor(ctx, userEmail)
	if err != nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	messages, err := h.gmailService.ListMessages(gmailClient, "in:inbox", 100)
	if err != nil {
		http.Error(w, "Failed to sync emails: "+err.Error(), http.StatusInternalServerError)
		return
	}

	syncCount := 0
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
		_, err := h.db.Emails().UpdateOne(ctx, filter, update, opts)
		if err == nil {
			syncCount++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"synced": syncCount,
		"total":  len(messages),
	})
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
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
		http.Error(w, "Failed to get user credentials", http.StatusInternalServerError)
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
		http.Error(w, "User not found", http.StatusNotFound)
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

// GetConfigStatus returns whether Gmail credentials are configured
func (h *Handler) GetConfigStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var config models.GmailConfig
	err := h.db.GmailConfig().FindOne(ctx, bson.M{}).Decode(&config)

	status := models.GmailConfigStatus{
		IsConfigured: err == nil && config.IsConfigured,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// GetGmailConfig returns the current config with masked secret
func (h *Handler) GetGmailConfig(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var config models.GmailConfig
	err := h.db.GmailConfig().FindOne(ctx, bson.M{}).Decode(&config)

	masked := models.GmailConfigMasked{
		IsConfigured: false,
	}

	if err == nil {
		masked.ClientID = config.ClientID
		masked.RedirectURL = config.RedirectURL
		masked.IsConfigured = config.IsConfigured
		if config.ClientSecretEncrypted != "" {
			masked.ClientSecret = "********"
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(masked)
}

// SaveGmailConfig saves the Gmail credentials (encrypted)
func (h *Handler) SaveGmailConfig(w http.ResponseWriter, r *http.Request) {
	var input models.GmailConfigInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if input.ClientID == "" {
		http.Error(w, "Client ID is required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Check if we need to keep the existing secret
	var existingConfig models.GmailConfig
	existingErr := h.db.GmailConfig().FindOne(ctx, bson.M{}).Decode(&existingConfig)

	var encryptedSecret string
	var plainSecret string

	if input.ClientSecret != "" {
		// New secret provided, encrypt it
		encrypted, err := h.encryptor.Encrypt(input.ClientSecret)
		if err != nil {
			http.Error(w, "Failed to encrypt credentials", http.StatusInternalServerError)
			return
		}
		encryptedSecret = encrypted
		plainSecret = input.ClientSecret
	} else if existingErr == nil && existingConfig.ClientSecretEncrypted != "" {
		// Keep existing secret
		encryptedSecret = existingConfig.ClientSecretEncrypted
		// Decrypt for hot reload
		decrypted, err := h.encryptor.Decrypt(existingConfig.ClientSecretEncrypted)
		if err != nil {
			http.Error(w, "Failed to decrypt existing credentials", http.StatusInternalServerError)
			return
		}
		plainSecret = decrypted
	} else {
		http.Error(w, "Client Secret is required", http.StatusBadRequest)
		return
	}

	// Set default redirect URL if not provided
	redirectURL := input.RedirectURL
	if redirectURL == "" {
		redirectURL = "http://localhost:3000/auth/callback"
	}

	// Upsert configuration (single document)
	filter := bson.M{}
	update := bson.M{
		"$set": bson.M{
			"clientId":              input.ClientID,
			"clientSecretEncrypted": encryptedSecret,
			"redirectUrl":           redirectURL,
			"isConfigured":          true,
			"updatedAt":             time.Now(),
		},
		"$setOnInsert": bson.M{
			"createdAt": time.Now(),
		},
	}
	opts := options.Update().SetUpsert(true)
	_, err := h.db.GmailConfig().UpdateOne(ctx, filter, update, opts)
	if err != nil {
		http.Error(w, "Failed to save configuration", http.StatusInternalServerError)
		return
	}

	// Hot reload Gmail service
	h.gmailService.UpdateConfig(input.ClientID, plainSecret, redirectURL)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
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
