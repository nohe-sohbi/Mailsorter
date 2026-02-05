package api

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/oauth2"
)

// AnalyzeEmails analyzes selected emails and creates AI suggestions
func (h *Handler) AnalyzeEmails(w http.ResponseWriter, r *http.Request) {
	if h.aiClient == nil {
		http.Error(w, "AI service not configured", http.StatusServiceUnavailable)
		return
	}

	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	var req models.AnalyzeEmailsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if len(req.EmailIDs) == 0 {
		http.Error(w, "No email IDs provided", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Get existing labels for context
	existingLabels, _ := h.getSmartLabelNames(ctx, userEmail)

	// Fetch emails from database
	var emails []models.Email
	for _, emailID := range req.EmailIDs {
		var email models.Email
		err := h.db.Emails().FindOne(ctx, bson.M{
			"messageId": emailID,
			"userId":    userEmail,
		}).Decode(&email)
		if err == nil {
			emails = append(emails, email)
		}
	}

	if len(emails) == 0 {
		http.Error(w, "No emails found", http.StatusNotFound)
		return
	}

	// Analyze each email
	var suggestions []models.AISuggestion
	for _, email := range emails {
		analysis, err := h.aiClient.AnalyzeEmail(email, existingLabels)
		if err != nil {
			continue // Skip failed analyses
		}

		suggestion := models.AISuggestion{
			UserID:     userEmail,
			EmailID:    email.MessageID,
			Action:     analysis.Action,
			LabelName:  analysis.LabelName,
			Confidence: analysis.Confidence,
			Reasoning:  analysis.Reasoning,
			Status:     "pending",
			CreatedAt:  time.Now(),
		}

		// Check if label already exists
		if analysis.Action == "label" && analysis.LabelName != "" {
			matchedLabel, exists, _ := h.aiClient.FindMatchingLabel(analysis.LabelName, existingLabels)
			suggestion.LabelName = matchedLabel
			if exists {
				// Find Gmail label ID
				var smartLabel models.SmartLabel
				err := h.db.SmartLabels().FindOne(ctx, bson.M{
					"userId": userEmail,
					"name":   matchedLabel,
				}).Decode(&smartLabel)
				if err == nil {
					suggestion.LabelID = smartLabel.GmailLabelID
				}
			}
		}

		// Save suggestion
		result, err := h.db.AISuggestions().InsertOne(ctx, suggestion)
		if err == nil {
			suggestion.ID = result.InsertedID.(primitive.ObjectID).Hex()
			suggestions = append(suggestions, suggestion)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(suggestions)
}

// AnalyzeSender analyzes all emails from a specific sender
func (h *Handler) AnalyzeSender(w http.ResponseWriter, r *http.Request) {
	if h.aiClient == nil {
		http.Error(w, "AI service not configured", http.StatusServiceUnavailable)
		return
	}

	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	var req models.AnalyzeSenderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Fetch emails from this sender
	cursor, err := h.db.Emails().Find(ctx, bson.M{
		"userId": userEmail,
		"from":   bson.M{"$regex": regexp.QuoteMeta(req.SenderEmail), "$options": "i"},
	}, options.Find().SetLimit(20))
	if err != nil {
		http.Error(w, "Failed to fetch emails", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var emails []models.Email
	if err := cursor.All(ctx, &emails); err != nil {
		http.Error(w, "Failed to decode emails", http.StatusInternalServerError)
		return
	}

	if len(emails) == 0 {
		http.Error(w, "No emails found from this sender", http.StatusNotFound)
		return
	}

	// Get existing labels
	existingLabels, _ := h.getSmartLabelNames(ctx, userEmail)

	// Analyze sender
	analysis, err := h.aiClient.AnalyzeSender(req.SenderEmail, emails, existingLabels)
	if err != nil {
		http.Error(w, "Failed to analyze sender: "+err.Error(), http.StatusInternalServerError)
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"analysis":   analysis,
		"emailCount": len(emails),
		"preference": senderPref,
	})
}

// ApplySuggestion applies a single AI suggestion to Gmail
func (h *Handler) ApplySuggestion(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	var req models.ApplySuggestionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Get suggestion
	objectID, err := primitive.ObjectIDFromHex(req.SuggestionID)
	if err != nil {
		http.Error(w, "Invalid suggestion ID", http.StatusBadRequest)
		return
	}

	var suggestion models.AISuggestion
	err = h.db.AISuggestions().FindOne(ctx, bson.M{
		"_id":    objectID,
		"userId": userEmail,
	}).Decode(&suggestion)
	if err != nil {
		http.Error(w, "Suggestion not found", http.StatusNotFound)
		return
	}

	// Get user token
	token, err := h.getUserToken(ctx, userEmail)
	if err != nil {
		http.Error(w, "Failed to get user credentials", http.StatusInternalServerError)
		return
	}

	gmailClient := h.gmailService.GetClient(token)

	// Apply action based on suggestion type
	switch suggestion.Action {
	case "archive":
		err = h.gmailService.ModifyMessage(gmailClient, suggestion.EmailID, nil, []string{"INBOX"})
	case "delete":
		err = h.gmailService.ModifyMessage(gmailClient, suggestion.EmailID, []string{"TRASH"}, nil)
	case "label":
		// Ensure label exists and get its ID
		labelID, err := h.ensureLabel(ctx, gmailClient, userEmail, suggestion.LabelName)
		if err != nil {
			http.Error(w, "Failed to create label: "+err.Error(), http.StatusInternalServerError)
			return
		}
		err = h.gmailService.ModifyMessage(gmailClient, suggestion.EmailID, []string{labelID}, nil)
		suggestion.LabelID = labelID
	case "keep":
		// No action needed
	}

	if err != nil {
		http.Error(w, "Failed to apply action: "+err.Error(), http.StatusInternalServerError)
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "applied"})
}

// ApplyBulk applies an action to all emails from a sender
func (h *Handler) ApplyBulk(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	var req models.ApplyBulkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Get user token
	token, err := h.getUserToken(ctx, userEmail)
	if err != nil {
		http.Error(w, "Failed to get user credentials", http.StatusInternalServerError)
		return
	}

	gmailClient := h.gmailService.GetClient(token)

	// Fetch all emails from this sender
	cursor, err := h.db.Emails().Find(ctx, bson.M{
		"userId": userEmail,
		"from":   bson.M{"$regex": regexp.QuoteMeta(req.SenderEmail), "$options": "i"},
	})
	if err != nil {
		http.Error(w, "Failed to fetch emails", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var emails []models.Email
	if err := cursor.All(ctx, &emails); err != nil {
		http.Error(w, "Failed to decode emails", http.StatusInternalServerError)
		return
	}

	// Prepare label if needed
	var labelID string
	if req.Action == "label" && req.LabelName != "" {
		labelID, err = h.ensureLabel(ctx, gmailClient, userEmail, req.LabelName)
		if err != nil {
			http.Error(w, "Failed to create label: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Apply action to each email
	appliedCount := 0
	for _, email := range emails {
		var applyErr error
		switch req.Action {
		case "archive":
			applyErr = h.gmailService.ModifyMessage(gmailClient, email.MessageID, nil, []string{"INBOX"})
		case "delete":
			applyErr = h.gmailService.ModifyMessage(gmailClient, email.MessageID, []string{"TRASH"}, nil)
		case "label":
			applyErr = h.gmailService.ModifyMessage(gmailClient, email.MessageID, []string{labelID}, nil)
		}
		if applyErr == nil {
			appliedCount++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"applied": appliedCount,
		"total":   len(emails),
	})
}

// GetSuggestions returns pending AI suggestions
func (h *Handler) GetSuggestions(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
		http.Error(w, "Failed to fetch suggestions", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var suggestions []models.AISuggestion
	if err := cursor.All(ctx, &suggestions); err != nil {
		http.Error(w, "Failed to decode suggestions", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(suggestions)
}

// RejectSuggestion rejects an AI suggestion
func (h *Handler) RejectSuggestion(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	vars := mux.Vars(r)
	suggestionID := vars["id"]

	objectID, err := primitive.ObjectIDFromHex(suggestionID)
	if err != nil {
		http.Error(w, "Invalid suggestion ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := h.db.AISuggestions().UpdateOne(ctx,
		bson.M{"_id": objectID, "userId": userEmail},
		bson.M{"$set": bson.M{"status": "rejected"}},
	)
	if err != nil {
		http.Error(w, "Failed to reject suggestion", http.StatusInternalServerError)
		return
	}

	if result.MatchedCount == 0 {
		http.Error(w, "Suggestion not found", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// GetSenders returns aggregated sender statistics
func (h *Handler) GetSenders(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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
		http.Error(w, "Failed to aggregate senders", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var results []struct {
		ID         string    `bson:"_id"`
		EmailCount int       `bson:"emailCount"`
		LastEmail  time.Time `bson:"lastEmail"`
	}
	if err := cursor.All(ctx, &results); err != nil {
		http.Error(w, "Failed to decode results", http.StatusInternalServerError)
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(senders)
}

// UpdateSenderPreference updates auto-apply settings for a sender
func (h *Handler) UpdateSenderPreference(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	vars := mux.Vars(r)
	prefID := vars["id"]

	objectID, err := primitive.ObjectIDFromHex(prefID)
	if err != nil {
		http.Error(w, "Invalid preference ID", http.StatusBadRequest)
		return
	}

	var req models.UpdateSenderPreferenceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
		http.Error(w, "Failed to update preference", http.StatusInternalServerError)
		return
	}

	if result.MatchedCount == 0 {
		http.Error(w, "Preference not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

// GetSmartLabels returns the user's smart labels
func (h *Handler) GetSmartLabels(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cursor, err := h.db.SmartLabels().Find(ctx, bson.M{"userId": userEmail})
	if err != nil {
		http.Error(w, "Failed to fetch labels", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var labels []models.SmartLabel
	if err := cursor.All(ctx, &labels); err != nil {
		http.Error(w, "Failed to decode labels", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(labels)
}

// CreateSmartLabel creates a new smart label manually
func (h *Handler) CreateSmartLabel(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	var label models.SmartLabel
	if err := json.NewDecoder(r.Body).Decode(&label); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if label.Name == "" {
		http.Error(w, "Label name required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Get user token to create Gmail label
	token, err := h.getUserToken(ctx, userEmail)
	if err != nil {
		http.Error(w, "Failed to get user credentials", http.StatusInternalServerError)
		return
	}

	gmailClient := h.gmailService.GetClient(token)

	// Create Gmail label
	gmailLabelID, err := h.gmailService.CreateLabel(gmailClient, label.Name)
	if err != nil {
		http.Error(w, "Failed to create Gmail label: "+err.Error(), http.StatusInternalServerError)
		return
	}

	label.UserID = userEmail
	label.GmailLabelID = gmailLabelID
	label.CreatedAt = time.Now()
	label.UpdatedAt = time.Now()

	result, err := h.db.SmartLabels().InsertOne(ctx, label)
	if err != nil {
		http.Error(w, "Failed to save label", http.StatusInternalServerError)
		return
	}

	label.ID = result.InsertedID.(primitive.ObjectID).Hex()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(label)
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

func (h *Handler) getUserToken(ctx context.Context, userEmail string) (*oauth2.Token, error) {
	var user models.User
	err := h.db.Users().FindOne(ctx, bson.M{"email": userEmail}).Decode(&user)
	if err != nil {
		return nil, err
	}

	token := &oauth2.Token{
		AccessToken:  user.AccessToken,
		RefreshToken: user.RefreshToken,
		Expiry:       user.TokenExpiry,
	}

	// Refresh if expired
	if token.Expiry.Before(time.Now()) && user.RefreshToken != "" {
		newToken, err := h.gmailService.RefreshToken(user.RefreshToken)
		if err == nil {
			token = newToken
			h.db.Users().UpdateOne(ctx, bson.M{"email": userEmail}, bson.M{
				"$set": bson.M{
					"accessToken": newToken.AccessToken,
					"tokenExpiry": newToken.Expiry,
					"updatedAt":   time.Now(),
				},
			})
		}
	}

	return token, nil
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

func extractSenderName(from string) string {
	// Handle "Name <email@domain.com>" format
	if idx := strings.Index(from, "<"); idx > 0 {
		name := strings.TrimSpace(from[:idx])
		name = strings.Trim(name, "\"")
		return name
	}
	return from
}
