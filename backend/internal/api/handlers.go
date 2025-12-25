package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/nohe-sohbi/mailsorter/backend/internal/crypto"
	"github.com/nohe-sohbi/mailsorter/backend/internal/database"
	"github.com/nohe-sohbi/mailsorter/backend/internal/gmail"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/oauth2"
)

type Handler struct {
	db           *database.Database
	gmailService *gmail.Service
	encryptor    *crypto.Encryptor
}

func NewHandler(db *database.Database, gmailService *gmail.Service, encryptor *crypto.Encryptor) *Handler {
	return &Handler{
		db:           db,
		gmailService: gmailService,
		encryptor:    encryptor,
	}
}

// Health check
func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// Auth endpoints
func (h *Handler) GetAuthURL(w http.ResponseWriter, r *http.Request) {
	// Generate a cryptographically secure random state to prevent CSRF attacks
	state := generateRandomState()
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.TokenResponse{
		AccessToken: token.AccessToken,
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

	// Get user from database
	var user models.User
	err := h.db.Users().FindOne(ctx, bson.M{"email": userEmail}).Decode(&user)
	if err != nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	// Check if token is expired and refresh if needed
	token := &oauth2.Token{
		AccessToken:  user.AccessToken,
		RefreshToken: user.RefreshToken,
		Expiry:       user.TokenExpiry,
	}

	if token.Expiry.Before(time.Now()) && user.RefreshToken != "" {
		newToken, err := h.gmailService.RefreshToken(user.RefreshToken)
		if err == nil {
			token = newToken
			// Update token in database
			h.db.Users().UpdateOne(ctx, bson.M{"email": userEmail}, bson.M{
				"$set": bson.M{
					"accessToken": newToken.AccessToken,
					"tokenExpiry": newToken.Expiry,
					"updatedAt":   time.Now(),
				},
			})
		}
	}

	gmailClient := h.gmailService.GetClient(token)
	query := r.URL.Query().Get("q")
	if query == "" {
		query = "in:inbox"
	}

	messages, err := h.gmailService.ListMessages(gmailClient, query, 50)
	if err != nil {
		http.Error(w, "Failed to fetch emails: "+err.Error(), http.StatusInternalServerError)
		return
	}

	emails := make([]models.Email, 0)
	for _, msg := range messages {
		from, subject, to, date := gmail.ParseEmailHeaders(msg)
		
		email := models.Email{
			MessageID:    msg.Id,
			UserID:       userEmail,
			ThreadID:     msg.ThreadId,
			From:         from,
			To:           to,
			Subject:      subject,
			Snippet:      msg.Snippet,
			LabelIDs:     msg.LabelIds,
			ReceivedDate: date,
			IsRead:       !contains(msg.LabelIds, "UNREAD"),
			CreatedAt:    time.Now(),
		}
		emails = append(emails, email)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(emails)
}

func (h *Handler) SyncEmails(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var user models.User
	err := h.db.Users().FindOne(ctx, bson.M{"email": userEmail}).Decode(&user)
	if err != nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	token := &oauth2.Token{
		AccessToken:  user.AccessToken,
		RefreshToken: user.RefreshToken,
		Expiry:       user.TokenExpiry,
	}

	gmailClient := h.gmailService.GetClient(token)
	messages, err := h.gmailService.ListMessages(gmailClient, "in:inbox", 100)
	if err != nil {
		http.Error(w, "Failed to sync emails: "+err.Error(), http.StatusInternalServerError)
		return
	}

	syncCount := 0
	for _, msg := range messages {
		from, subject, to, date := gmail.ParseEmailHeaders(msg)
		body := gmail.GetEmailBody(msg)
		
		email := models.Email{
			MessageID:    msg.Id,
			UserID:       userEmail,
			ThreadID:     msg.ThreadId,
			From:         from,
			To:           to,
			Subject:      subject,
			Body:         body,
			Snippet:      msg.Snippet,
			LabelIDs:     msg.LabelIds,
			ReceivedDate: date,
			IsRead:       !contains(msg.LabelIds, "UNREAD"),
			CreatedAt:    time.Now(),
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

// Sorting rules endpoints
func (h *Handler) GetSortingRules(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cursor, err := h.db.SortingRules().Find(ctx, bson.M{"userId": userEmail})
	if err != nil {
		http.Error(w, "Failed to fetch rules: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var rules []models.SortingRule
	if err := cursor.All(ctx, &rules); err != nil {
		http.Error(w, "Failed to decode rules: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rules)
}

func (h *Handler) CreateSortingRule(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	var rule models.SortingRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	rule.UserID = userEmail
	rule.CreatedAt = time.Now()
	rule.UpdatedAt = time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := h.db.SortingRules().InsertOne(ctx, rule)
	if err != nil {
		http.Error(w, "Failed to create rule: "+err.Error(), http.StatusInternalServerError)
		return
	}

	rule.ID = result.InsertedID.(primitive.ObjectID).Hex()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(rule)
}

func (h *Handler) UpdateSortingRule(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	vars := mux.Vars(r)
	ruleID := vars["id"]

	objectID, err := primitive.ObjectIDFromHex(ruleID)
	if err != nil {
		http.Error(w, "Invalid rule ID", http.StatusBadRequest)
		return
	}

	var rule models.SortingRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	rule.UpdatedAt = time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	update := bson.M{"$set": rule}
	filter := bson.M{"_id": objectID, "userId": userEmail}

	result, err := h.db.SortingRules().UpdateOne(ctx, filter, update)
	if err != nil {
		http.Error(w, "Failed to update rule: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if result.MatchedCount == 0 {
		http.Error(w, "Rule not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rule)
}

func (h *Handler) DeleteSortingRule(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	vars := mux.Vars(r)
	ruleID := vars["id"]

	objectID, err := primitive.ObjectIDFromHex(ruleID)
	if err != nil {
		http.Error(w, "Invalid rule ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	filter := bson.M{"_id": objectID, "userId": userEmail}
	result, err := h.db.SortingRules().DeleteOne(ctx, filter)
	if err != nil {
		http.Error(w, "Failed to delete rule: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if result.DeletedCount == 0 {
		http.Error(w, "Rule not found", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
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

	var user models.User
	err := h.db.Users().FindOne(ctx, bson.M{"email": userEmail}).Decode(&user)
	if err != nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	token := &oauth2.Token{
		AccessToken:  user.AccessToken,
		RefreshToken: user.RefreshToken,
		Expiry:       user.TokenExpiry,
	}

	gmailClient := h.gmailService.GetClient(token)
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

func generateRandomState() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based state if random generation fails
		return base64.URLEncoding.EncodeToString([]byte(time.Now().String()))
	}
	return base64.URLEncoding.EncodeToString(b)
}
