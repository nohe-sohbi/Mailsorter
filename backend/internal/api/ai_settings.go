package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/ai"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// GetAIProviders returns the catalog of known AI providers with their
// capabilities (required fields, suggested models). Public-ish: no secret is
// revealed, but the route sits behind the auth middleware.
func (h *Handler) GetAIProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, ai.KnownProviders)
}

// GetAISettings returns the user's current AI provider configuration.
// API keys are masked in the response.
func (h *Handler) GetAISettings(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var settings models.AIProviderSettings
	err := h.db.AISettings().FindOne(ctx, bson.M{"userId": userEmail}).Decode(&settings)
	if err != nil {
		// No custom settings: user is on the instance default
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"configured":   false,
			"provider":     h.aiRegistry.ProviderName(),
			"instanceMode": true,
		})
		return
	}

	// Mask the API key before returning
	maskedKey := ""
	if settings.APIKey != "" {
		decrypted, err := h.encryptor.Decrypt(settings.APIKey)
		if err == nil && len(decrypted) > 8 {
			maskedKey = decrypted[:4] + "..." + decrypted[len(decrypted)-4:]
		} else if err == nil {
			maskedKey = "****"
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"configured":   true,
		"provider":     settings.Provider,
		"model":        settings.Model,
		"baseUrl":      settings.BaseURL,
		"apiKeyMasked": maskedKey,
		"instanceMode": false,
	})
}

// UpdateAISettings saves the user's own AI provider configuration (BYOK).
// The API key is encrypted before storage.
func (h *Handler) UpdateAISettings(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	var req struct {
		Provider string `json:"provider"`
		APIKey   string `json:"apiKey"`
		Model    string `json:"model"`
		BaseURL  string `json:"baseUrl"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	// Validate provider name
	validProvider := false
	for _, p := range ai.KnownProviders {
		if p.Name == req.Provider {
			validProvider = true
			break
		}
	}
	if !validProvider {
		writeError(w, http.StatusBadRequest, "Unknown provider: "+req.Provider)
		return
	}

	// Encrypt the API key
	encryptedKey := ""
	if req.APIKey != "" {
		encrypted, err := h.encryptor.Encrypt(req.APIKey)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to encrypt API key")
			return
		}
		encryptedKey = encrypted
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	settings := models.AIProviderSettings{
		UserID:    userEmail,
		Provider:  req.Provider,
		APIKey:    encryptedKey,
		Model:     req.Model,
		BaseURL:   req.BaseURL,
		UpdatedAt: time.Now(),
	}

	_, err := h.db.AISettings().UpdateOne(ctx,
		bson.M{"userId": userEmail},
		bson.M{"$set": settings, "$setOnInsert": bson.M{"createdAt": time.Now()}},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save AI settings")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

// TestAISettings tests the user's AI provider configuration by sending a
// trivial prompt and checking for a valid response.
func (h *Handler) TestAISettings(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	var req struct {
		Provider string `json:"provider"`
		APIKey   string `json:"apiKey"`
		Model    string `json:"model"`
		BaseURL  string `json:"baseUrl"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	// Build a one-off analyzer from the provided credentials
	cfg := ai.ProviderConfig{
		Name:       req.Provider,
		APIKey:     req.APIKey,
		Model:      req.Model,
		BaseURL:    req.BaseURL,
		MaxRetries: 0, // no retries for test
		Enabled:    true,
	}
	testRegistry := ai.NewRegistry([]ai.ProviderConfig{cfg})
	if !testRegistry.Available() {
		writeError(w, http.StatusBadRequest, "Could not initialize provider with the given credentials")
		return
	}

	// Send a trivial test email
	testEmail := models.Email{
		From:    "test@example.com",
		Subject: "Test de connexion",
		Snippet: "Ceci est un test de connexion au provider IA.",
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	_ = ctx // The AI call doesn't use context yet, but we bound the handler.

	result, err := testRegistry.AnalyzeEmail(testEmail, nil)
	if err != nil {
		errMsg := err.Error()
		// Provide user-friendly messages for common errors
		if strings.Contains(errMsg, "status 401") || strings.Contains(errMsg, "Unauthorized") {
			writeError(w, http.StatusBadRequest, "Clé API invalide ou expirée")
			return
		}
		if strings.Contains(errMsg, "status 403") {
			writeError(w, http.StatusBadRequest, "Accès refusé — vérifiez votre clé API et vos permissions")
			return
		}
		if strings.Contains(errMsg, "connection refused") || strings.Contains(errMsg, "no such host") {
			writeError(w, http.StatusBadRequest, "Impossible de joindre le serveur — vérifiez l'URL")
			return
		}
		writeError(w, http.StatusBadRequest, "Erreur du provider: "+errMsg)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":   "ok",
		"provider": req.Provider,
		"model":    req.Model,
		"test":     result,
	})
}

// DeleteAISettings removes the user's custom AI provider configuration,
// reverting to the instance default.
func (h *Handler) DeleteAISettings(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	h.db.AISettings().DeleteOne(ctx, bson.M{"userId": userEmail})

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}


// resolveAnalyzer returns a Registry configured for the given user:
// if the user has custom BYOK settings saved, they take precedence over
// the instance-wide providers with instance-wide providers as fallback.
func (h *Handler) resolveAnalyzer(ctx context.Context, userEmail string) *ai.Registry {
	if h.aiRegistry == nil {
		h.aiRegistry = ai.NewRegistry(nil)
	}
	if userEmail == "" {
		return h.aiRegistry
	}

	var settings models.AIProviderSettings
	err := h.db.AISettings().FindOne(ctx, bson.M{"userId": userEmail}).Decode(&settings)
	if err != nil || settings.Provider == "" {
		return h.aiRegistry
	}

	apiKey := settings.APIKey
	if apiKey != "" && h.encryptor != nil {
		if decrypted, err := h.encryptor.Decrypt(apiKey); err == nil {
			apiKey = decrypted
		}
	}

	userCfg := ai.ProviderConfig{
		Name:       settings.Provider,
		APIKey:     apiKey,
		Model:      settings.Model,
		BaseURL:    settings.BaseURL,
		MaxRetries: 2,
		Priority:   0,
		Enabled:    true,
	}

	return h.aiRegistry.WithUserOverride(userCfg)
}
