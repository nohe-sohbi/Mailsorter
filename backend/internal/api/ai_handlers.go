package api

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	gmailapi "google.golang.org/api/gmail/v1"
)

// AnalyzeEmails synchronously analyzes selected emails and creates AI suggestions.
func (h *Handler) AnalyzeEmails(w http.ResponseWriter, r *http.Request) {
	if h.aiClient == nil {
		writeError(w, http.StatusServiceUnavailable, "AI service not configured")
		return
	}

	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	var req models.AnalyzeEmailsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.EmailIDs) == 0 {
		writeError(w, http.StatusBadRequest, "No email IDs provided")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	if h.quotaExceeded(ctx, userEmail) {
		writeError(w, http.StatusPaymentRequired, "Quota mensuel atteint. Passez à Pro pour continuer.")
		return
	}

	progress, suggestions, err := h.runAnalysis(ctx, userEmail, req.EmailIDs, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Analysis failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"suggestions": suggestions,
		"autoApplied": progress.AutoApplied,
		"cachedHits":  progress.CachedHits,
	})
}

// autoApplySender applies a sender's saved default action to an email directly,
// recording it as an already-applied suggestion. Returns true on success so the
// caller can skip the AI analysis for that email.
func (h *Handler) autoApplySender(ctx context.Context, gmailClient *gmailapi.Service, userEmail string, email models.Email, pref models.SenderPreference) bool {
	suggestion := models.AISuggestion{
		UserID:     userEmail,
		EmailID:    email.MessageID,
		Action:     pref.DefaultAction,
		LabelName:  pref.DefaultLabel,
		Confidence: 1.0,
		Reasoning:  "Auto-appliqué (préférence expéditeur)",
		Status:     "applied",
		CreatedAt:  time.Now(),
		AppliedAt:  time.Now(),
	}

	labelID, err := h.applyVerdict(ctx, gmailClient, userEmail, email.MessageID, pref.DefaultAction, pref.DefaultLabel, "")
	if err != nil {
		return false
	}
	suggestion.LabelID = labelID

	h.db.AISuggestions().InsertOne(ctx, suggestion)
	h.logActionMeta(ctx, userEmail, email.MessageID, pref.DefaultAction, SourceAIAuto, email.Subject, email.From)
	return true
}

// AnalyzeSender analyzes all emails from a specific sender
func (h *Handler) AnalyzeSender(w http.ResponseWriter, r *http.Request) {
	if h.aiClient == nil {
		writeError(w, http.StatusServiceUnavailable, "AI service not configured")
		return
	}

	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	var req models.AnalyzeSenderRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// Fetch emails from this sender
	cursor, err := h.db.Emails().Find(ctx, bson.M{
		"userId": userEmail,
		"from":   bson.M{"$regex": regexp.QuoteMeta(req.SenderEmail), "$options": "i"},
	}, options.Find().SetLimit(20))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to fetch emails")
		return
	}
	defer cursor.Close(ctx)

	var emails []models.Email
	if err := cursor.All(ctx, &emails); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to decode emails")
		return
	}

	if len(emails) == 0 {
		writeError(w, http.StatusNotFound, "No emails found from this sender")
		return
	}

	// Get existing labels
	existingLabels, _ := h.getSmartLabelNames(ctx, userEmail)

	// Analyze sender
	analysis, err := h.aiClient.AnalyzeSender(req.SenderEmail, emails, existingLabels)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to analyze sender: "+err.Error())
		return
	}

	// Extract domain from sender email
	domain := extractDomain(req.SenderEmail)

	// Create or update sender preference
	senderPref := models.SenderPreference{
		UserID:        userEmail,
		SenderEmail:   req.SenderEmail,
		SenderDomain:  domain,
		SenderName:    extractSenderName(emails[0].From),
		AutoApply:     false,
		DefaultAction: analysis.SuggestedAction,
		DefaultLabel:  analysis.SuggestedLabel,
		EmailCount:    len(emails),
		UpdatedAt:     time.Now(),
	}

	filter := bson.M{"userId": userEmail, "senderEmail": req.SenderEmail}
	update := bson.M{
		"$set": senderPref,
		"$setOnInsert": bson.M{
			"createdAt": time.Now(),
		},
	}
	opts := options.Update().SetUpsert(true)
	h.db.SenderPreferences().UpdateOne(ctx, filter, update, opts)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"analysis":   analysis,
		"emailCount": len(emails),
		"preference": senderPref,
	})
}

// ApplySuggestion applies a single AI suggestion to Gmail
func (h *Handler) ApplySuggestion(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	var req models.ApplySuggestionRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// Get suggestion
	objectID, err := primitive.ObjectIDFromHex(req.SuggestionID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid suggestion ID")
		return
	}

	var suggestion models.AISuggestion
	err = h.db.AISuggestions().FindOne(ctx, bson.M{
		"_id":    objectID,
		"userId": userEmail,
	}).Decode(&suggestion)
	if err != nil {
		writeError(w, http.StatusNotFound, "Suggestion not found")
		return
	}

	// Get user token
	token, err := h.getUserToken(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	gmailClient := h.gmailService.GetClient(token)

	labelID, err := h.applyVerdict(ctx, gmailClient, userEmail, suggestion.EmailID, suggestion.Action, suggestion.LabelName, "")
	suggestion.LabelID = labelID
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to apply action: "+err.Error())
		return
	}

	// Update suggestion status
	h.db.AISuggestions().UpdateOne(ctx,
		bson.M{"_id": objectID},
		bson.M{"$set": bson.M{
			"status":    "applied",
			"appliedAt": time.Now(),
			"labelId":   suggestion.LabelID,
		}},
	)
	meta := h.emailIdentity(ctx, gmailClient, userEmail, suggestion.EmailID)
	h.logActionMeta(ctx, userEmail, suggestion.EmailID, suggestion.Action, SourceAI, meta.Subject, meta.From)

	writeJSON(w, http.StatusOK, map[string]string{"status": "applied"})
}

// ApplyBatch applies a list of AI suggestions in a single request.
// It refreshes the token once and loops server-side, which scales far better
// than firing one HTTP request per suggestion from the client.
func (h *Handler) ApplyBatch(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	var req models.ApplyBatchRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.SuggestionIDs) == 0 {
		writeError(w, http.StatusBadRequest, "No suggestion IDs provided")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	token, err := h.getUserToken(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	gmailClient := h.gmailService.GetClient(token)

	protectedList := h.protectedValues(ctx, userEmail)
	applied := 0
	appliedIDs := make([]string, 0, len(req.SuggestionIDs))
	failed := 0
	protectedSkipped := 0

	// Resolve every suggested email's identity once, up front, so each ledger
	// entry can name the message it acted on without a lookup inside the loop.
	emailIDs := h.suggestionEmailIDs(ctx, userEmail, req.SuggestionIDs)
	identities := h.emailIdentities(ctx, userEmail, emailIDs)

	for _, id := range req.SuggestionIDs {
		objectID, err := primitive.ObjectIDFromHex(id)
		if err != nil {
			failed++
			continue
		}

		var suggestion models.AISuggestion
		if err := h.db.AISuggestions().FindOne(ctx, bson.M{
			"_id":    objectID,
			"userId": userEmail,
		}).Decode(&suggestion); err != nil {
			failed++
			continue
		}

		// Shield protected senders from a bulk "apply all" that would archive
		// or trash their mail. The suggestion is left pending, untouched.
		if len(protectedList) > 0 && !allows(suggestion.Action, h.senderOf(ctx, userEmail, suggestion.EmailID), protectedList) {
			protectedSkipped++
			continue
		}

		labelID, applyErr := h.applyVerdict(ctx, gmailClient, userEmail, suggestion.EmailID, suggestion.Action, suggestion.LabelName, "")
		suggestion.LabelID = labelID

		if applyErr != nil {
			failed++
			continue
		}

		h.db.AISuggestions().UpdateOne(ctx,
			bson.M{"_id": objectID},
			bson.M{"$set": bson.M{
				"status":    "applied",
				"appliedAt": time.Now(),
				"labelId":   suggestion.LabelID,
			}},
		)
		meta := identities[suggestion.EmailID]
		h.logActionMeta(ctx, userEmail, suggestion.EmailID, suggestion.Action, SourceAI, meta.Subject, meta.From)
		applied++
		appliedIDs = append(appliedIDs, id)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"applied":          applied,
		"failed":           failed,
		"total":            len(req.SuggestionIDs),
		"appliedIds":       appliedIDs,
		"protectedSkipped": protectedSkipped,
	})
}

// ApplyBulk applies an action to all emails from a sender
func (h *Handler) ApplyBulk(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	var req models.ApplyBulkRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	// Get user token
	token, err := h.getUserToken(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	gmailClient := h.gmailService.GetClient(token)

	// Fetch all emails from this sender
	cursor, err := h.db.Emails().Find(ctx, bson.M{
		"userId": userEmail,
		"from":   bson.M{"$regex": regexp.QuoteMeta(req.SenderEmail), "$options": "i"},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to fetch emails")
		return
	}
	defer cursor.Close(ctx)

	var emails []models.Email
	if err := cursor.All(ctx, &emails); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to decode emails")
		return
	}

	// Prepare label if needed
	var labelID string
	if req.Action == "label" && req.LabelName != "" {
		labelID, err = h.ensureLabel(ctx, gmailClient, userEmail, req.LabelName)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to create label: "+err.Error())
			return
		}
	}

	// Apply action to each email, shielding protected senders from destructive
	// sweeps (a protected sender can still be labelled in bulk).
	protectedList := h.protectedValues(ctx, userEmail)
	appliedCount := 0
	protectedSkipped := 0
	for _, email := range emails {
		if !allows(req.Action, email.From, protectedList) {
			protectedSkipped++
			continue
		}
		// labelID is resolved once before the loop: a bulk apply must not create
		// the same label per message.
		_, applyErr := h.applyVerdict(ctx, gmailClient, userEmail, email.MessageID, req.Action, "", labelID)
		if applyErr == nil {
			appliedCount++
			h.logActionMeta(ctx, userEmail, email.MessageID, req.Action, SourceBulk, email.Subject, email.From)
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"applied":          appliedCount,
		"total":            len(emails),
		"protectedSkipped": protectedSkipped,
	})
}

// GetSuggestions returns pending AI suggestions
func (h *Handler) GetSuggestions(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	status := r.URL.Query().Get("status")
	if status == "" {
		status = "pending"
	}

	cursor, err := h.db.AISuggestions().Find(ctx, bson.M{
		"userId": userEmail,
		"status": status,
	}, options.Find().SetSort(bson.M{"createdAt": -1}).SetLimit(100))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to fetch suggestions")
		return
	}
	defer cursor.Close(ctx)

	var suggestions []models.AISuggestion
	if err := cursor.All(ctx, &suggestions); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to decode suggestions")
		return
	}

	// Attach the subject/sender of each suggested email in one indexed lookup.
	// The client used to find it by scanning the emails currently on screen,
	// which meant any suggestion for a message outside the loaded page rendered
	// as "Sans sujet · Expéditeur inconnu", asking the user to approve an
	// action on an email they cannot see.
	ids := make([]string, 0, len(suggestions))
	for _, s := range suggestions {
		if s.EmailID != "" {
			ids = append(ids, s.EmailID)
		}
	}
	identities := h.emailIdentities(ctx, userEmail, ids)

	type suggestionView struct {
		models.AISuggestion
		Subject string `json:"subject,omitempty"`
		From    string `json:"from,omitempty"`
		Snippet string `json:"snippet,omitempty"`
	}
	views := make([]suggestionView, 0, len(suggestions))
	for _, s := range suggestions {
		v := suggestionView{AISuggestion: s}
		if e, ok := identities[s.EmailID]; ok {
			v.Subject, v.From, v.Snippet = e.Subject, e.From, e.Snippet
		}
		views = append(views, v)
	}

	writeJSON(w, http.StatusOK, views)
}

// RejectSuggestion rejects an AI suggestion
func (h *Handler) RejectSuggestion(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	vars := mux.Vars(r)
	suggestionID := vars["id"]

	objectID, err := primitive.ObjectIDFromHex(suggestionID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid suggestion ID")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	result, err := h.db.AISuggestions().UpdateOne(ctx,
		bson.M{"_id": objectID, "userId": userEmail},
		bson.M{"$set": bson.M{"status": "rejected"}},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to reject suggestion")
		return
	}

	if result.MatchedCount == 0 {
		writeError(w, http.StatusNotFound, "Suggestion not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// GetSenders returns aggregated sender statistics
func (h *Handler) GetSenders(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	// Aggregate emails by sender
	pipeline := []bson.M{
		{"$match": bson.M{"userId": userEmail}},
		{"$group": bson.M{
			"_id":        "$from",
			"emailCount": bson.M{"$sum": 1},
			"lastEmail":  bson.M{"$max": "$receivedDate"},
		}},
		{"$sort": bson.M{"emailCount": -1}},
		{"$limit": 50},
	}

	cursor, err := h.db.Emails().Aggregate(ctx, pipeline)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to aggregate senders")
		return
	}
	defer cursor.Close(ctx)

	var results []struct {
		ID         string    `bson:"_id"`
		EmailCount int       `bson:"emailCount"`
		LastEmail  time.Time `bson:"lastEmail"`
	}
	if err := cursor.All(ctx, &results); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to decode results")
		return
	}

	// Enrich with preferences
	var senders []models.SenderStats
	for _, r := range results {
		sender := models.SenderStats{
			SenderEmail:  r.ID,
			SenderDomain: extractDomain(r.ID),
			SenderName:   extractSenderName(r.ID),
			EmailCount:   r.EmailCount,
		}

		// Check for existing preference
		var pref models.SenderPreference
		err := h.db.SenderPreferences().FindOne(ctx, bson.M{
			"userId":      userEmail,
			"senderEmail": r.ID,
		}).Decode(&pref)
		if err == nil {
			sender.Preference = &pref
		}

		senders = append(senders, sender)
	}

	writeJSON(w, http.StatusOK, senders)
}

// UpdateSenderPreference updates auto-apply settings for a sender
func (h *Handler) UpdateSenderPreference(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	vars := mux.Vars(r)
	prefID := vars["id"]

	objectID, err := primitive.ObjectIDFromHex(prefID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid preference ID")
		return
	}

	var req models.UpdateSenderPreferenceRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	result, err := h.db.SenderPreferences().UpdateOne(ctx,
		bson.M{"_id": objectID, "userId": userEmail},
		bson.M{"$set": bson.M{
			"autoApply":     req.AutoApply,
			"defaultAction": req.DefaultAction,
			"defaultLabel":  req.DefaultLabel,
			"updatedAt":     time.Now(),
		}},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update preference")
		return
	}

	if result.MatchedCount == 0 {
		writeError(w, http.StatusNotFound, "Preference not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// GetSmartLabels returns the user's smart labels
func (h *Handler) GetSmartLabels(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	cursor, err := h.db.SmartLabels().Find(ctx, bson.M{"userId": userEmail})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to fetch labels")
		return
	}
	defer cursor.Close(ctx)

	var labels []models.SmartLabel
	if err := cursor.All(ctx, &labels); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to decode labels")
		return
	}

	writeJSON(w, http.StatusOK, labels)
}

// CreateSmartLabel creates a new smart label manually
func (h *Handler) CreateSmartLabel(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	var label models.SmartLabel
	if !decodeJSON(w, r, &label) {
		return
	}

	if label.Name == "" {
		writeError(w, http.StatusBadRequest, "Label name required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Get user token to create Gmail label
	token, err := h.getUserToken(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	gmailClient := h.gmailService.GetClient(token)

	// Create Gmail label
	gmailLabelID, err := h.gmailService.CreateLabel(gmailClient, label.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create Gmail label: "+err.Error())
		return
	}

	label.UserID = userEmail
	label.GmailLabelID = gmailLabelID
	label.CreatedAt = time.Now()
	label.UpdatedAt = time.Now()

	result, err := h.db.SmartLabels().InsertOne(ctx, label)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save label")
		return
	}

	label.ID = result.InsertedID.(primitive.ObjectID).Hex()

	writeJSON(w, http.StatusCreated, label)
}

// Helper functions

func (h *Handler) getSmartLabelNames(ctx context.Context, userEmail string) ([]string, error) {
	cursor, err := h.db.SmartLabels().Find(ctx, bson.M{"userId": userEmail})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var labels []models.SmartLabel
	if err := cursor.All(ctx, &labels); err != nil {
		return nil, err
	}

	names := make([]string, len(labels))
	for i, l := range labels {
		names[i] = l.Name
	}
	return names, nil
}

// applyVerdict applies one AI verdict to one message.
//
// "keep" is not a mutation but a decision not to act, so it succeeds having
// changed nothing. Everything else goes through the neutral vocabulary, which
// also closed a latent hole: three of the four call sites this replaced had no
// default branch, so an action they did not recognise left the error nil and
// the message was then recorded, and counted, as applied without Gmail ever
// being touched. An unknown verb now fails, loudly.
//
// The resolved label id comes back because the caller stores it on the
// suggestion: it is what makes the action undoable later.
func (h *Handler) applyVerdict(ctx context.Context, gmailClient *gmailapi.Service, userEmail, messageID, action, labelName, labelID string) (string, error) {
	if action == "keep" {
		return "", nil
	}
	if action == "label" && labelID == "" {
		resolved, err := h.ensureLabel(ctx, gmailClient, userEmail, labelName)
		if err != nil {
			return "", err
		}
		labelID = resolved
	}
	return labelID, h.applyVerb(ctx, gmailClient, mailbox.OnAccount(messageID), action, labelID)
}

// senderOf returns the stored From of a message, or "" if unknown. Used to
// consult the protected list when applying AI suggestions, which don't carry the
// sender themselves.
func (h *Handler) senderOf(ctx context.Context, userEmail, messageID string) string {
	var e models.Email
	if err := h.db.Emails().FindOne(ctx, bson.M{"userId": userEmail, "messageId": messageID}).Decode(&e); err == nil {
		return e.From
	}
	return ""
}

func (h *Handler) ensureLabel(ctx context.Context, gmailClient interface{}, userEmail, labelName string) (string, error) {
	// Check if we already have this smart label
	var smartLabel models.SmartLabel
	err := h.db.SmartLabels().FindOne(ctx, bson.M{
		"userId": userEmail,
		"name":   labelName,
	}).Decode(&smartLabel)

	if err == nil {
		return smartLabel.GmailLabelID, nil
	}

	// Create new Gmail label
	gmailLabelID, err := h.gmailService.CreateLabel(gmailClient, labelName)
	if err != nil {
		return "", err
	}

	// Save as smart label
	newLabel := models.SmartLabel{
		UserID:       userEmail,
		Name:         labelName,
		GmailLabelID: gmailLabelID,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	h.db.SmartLabels().InsertOne(ctx, newLabel)

	return gmailLabelID, nil
}

func extractDomain(email string) string {
	// Handle "Name <email@domain.com>" format
	if idx := strings.Index(email, "<"); idx >= 0 {
		email = email[idx+1:]
		if idx := strings.Index(email, ">"); idx >= 0 {
			email = email[:idx]
		}
	}
	if idx := strings.Index(email, "@"); idx >= 0 {
		return email[idx+1:]
	}
	return email
}

// extractSenderAddress returns the bare email address from a "Name <addr>" header,
// or the trimmed input if no angle brackets are present.
func extractSenderAddress(from string) string {
	if idx := strings.Index(from, "<"); idx >= 0 {
		rest := from[idx+1:]
		if j := strings.Index(rest, ">"); j >= 0 {
			return strings.TrimSpace(rest[:j])
		}
	}
	return strings.TrimSpace(from)
}

func extractSenderName(from string) string {
	// Handle "Name <email@domain.com>" format
	if idx := strings.Index(from, "<"); idx > 0 {
		name := strings.TrimSpace(from[:idx])
		name = strings.Trim(name, "\"")
		return name
	}
	return from
}
