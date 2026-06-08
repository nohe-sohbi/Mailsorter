package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/ai"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	gmailapi "google.golang.org/api/gmail/v1"
)

// analysisBatchSize controls how many emails go into a single Mistral call.
const analysisBatchSize = 8

type analysisProgress struct {
	Total              int
	Processed          int
	AutoApplied        int
	SuggestionsCreated int
	CachedHits         int
}

// runAnalysis is the shared engine behind both the synchronous endpoint and the
// async worker. It auto-applies sender preferences, serves cached verdicts, and
// batches the remaining emails through the AI. onProgress (nullable) is called
// after every email so callers can stream progress.
func (h *Handler) runAnalysis(
	ctx context.Context,
	userEmail string,
	emailIDs []string,
	onProgress func(analysisProgress),
) (analysisProgress, []models.AISuggestion, error) {
	var p analysisProgress
	suggestions := make([]models.AISuggestion, 0)

	existingLabels, _ := h.getSmartLabelNames(ctx, userEmail)

	emails := make([]models.Email, 0, len(emailIDs))
	for _, id := range emailIDs {
		var e models.Email
		if err := h.db.Emails().FindOne(ctx, bson.M{"messageId": id, "userId": userEmail}).Decode(&e); err == nil {
			emails = append(emails, e)
		}
	}
	p.Total = len(emails)
	if len(emails) == 0 {
		return p, suggestions, nil
	}

	report := func() {
		if onProgress != nil {
			onProgress(p)
		}
	}

	// Best-effort Gmail client for sender auto-pilot.
	var gmailClient *gmailapi.Service
	if token, terr := h.getUserToken(ctx, userEmail); terr == nil {
		gmailClient = h.gmailService.GetClient(token)
	}

	// Pass 1: resolve auto-pilot + cache hits, collect the rest for batching.
	pending := make([]models.Email, 0, len(emails))
	for _, email := range emails {
		if gmailClient != nil {
			var pref models.SenderPreference
			err := h.db.SenderPreferences().FindOne(ctx, bson.M{
				"userId":      userEmail,
				"senderEmail": email.From,
				"autoApply":   true,
			}).Decode(&pref)
			if err == nil && pref.DefaultAction != "" && h.autoApplySender(ctx, gmailClient, userEmail, email, pref) {
				p.AutoApplied++
				p.Processed++
				report()
				continue
			}
		}

		key := analysisCacheKey(email.From, email.Subject)
		if cached, ok := h.cacheLookup(ctx, key); ok {
			if s, inserted := h.persistSuggestion(ctx, userEmail, email, cached, existingLabels); inserted {
				suggestions = append(suggestions, s)
				p.SuggestionsCreated++
			}
			p.CachedHits++
			p.Processed++
			report()
			continue
		}

		pending = append(pending, email)
	}

	// Pass 2: batch-analyze the cache misses.
	for i := 0; i < len(pending); i += analysisBatchSize {
		if ctx.Err() != nil {
			break
		}
		end := i + analysisBatchSize
		if end > len(pending) {
			end = len(pending)
		}
		chunk := pending[i:end]

		var analyses []ai.EmailAnalysis
		if h.aiClient != nil {
			if res, err := h.aiClient.AnalyzeBatch(chunk, existingLabels); err == nil {
				analyses = res
			}
		}

		for j, email := range chunk {
			var a ai.EmailAnalysis
			switch {
			case analyses != nil && j < len(analyses):
				a = analyses[j]
			case h.aiClient != nil:
				// Batch failed to align — fall back to a single-email call.
				single, err := h.aiClient.AnalyzeEmail(email, existingLabels)
				if err != nil {
					p.Processed++
					report()
					continue
				}
				a = *single
			default:
				p.Processed++
				report()
				continue
			}

			if s, inserted := h.persistSuggestion(ctx, userEmail, email, a, existingLabels); inserted {
				suggestions = append(suggestions, s)
				p.SuggestionsCreated++
			}
			h.cacheStore(ctx, analysisCacheKey(email.From, email.Subject), a)
			p.Processed++
			report()
		}
	}

	return p, suggestions, nil
}

// persistSuggestion resolves the label, inserts a pending suggestion and returns it.
func (h *Handler) persistSuggestion(
	ctx context.Context,
	userEmail string,
	email models.Email,
	a ai.EmailAnalysis,
	existingLabels []string,
) (models.AISuggestion, bool) {
	suggestion := models.AISuggestion{
		UserID:     userEmail,
		EmailID:    email.MessageID,
		Action:     a.Action,
		LabelName:  a.LabelName,
		Confidence: a.Confidence,
		Reasoning:  a.Reasoning,
		Status:     "pending",
		CreatedAt:  time.Now(),
	}

	if a.Action == "label" && a.LabelName != "" {
		matched := localMatchLabel(a.LabelName, existingLabels)
		suggestion.LabelName = matched
		var sl models.SmartLabel
		if err := h.db.SmartLabels().FindOne(ctx, bson.M{"userId": userEmail, "name": matched}).Decode(&sl); err == nil {
			suggestion.LabelID = sl.GmailLabelID
		}
	}

	res, err := h.db.AISuggestions().InsertOne(ctx, suggestion)
	if err != nil {
		return suggestion, false
	}
	suggestion.ID = res.InsertedID.(primitive.ObjectID).Hex()
	return suggestion, true
}

// localMatchLabel maps a suggested label onto an existing one without an AI call.
func localMatchLabel(suggested string, existing []string) string {
	s := strings.ToLower(strings.TrimSpace(suggested))
	for _, e := range existing {
		le := strings.ToLower(strings.TrimSpace(e))
		if le == s || strings.Contains(le, s) || strings.Contains(s, le) {
			return e
		}
	}
	return suggested
}

func analysisCacheKey(from, subject string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(from)) + "|" + strings.ToLower(strings.TrimSpace(subject))))
	return hex.EncodeToString(sum[:])
}

func (h *Handler) cacheLookup(ctx context.Context, key string) (ai.EmailAnalysis, bool) {
	var e models.AnalysisCacheEntry
	if err := h.db.AnalysisCache().FindOne(ctx, bson.M{"key": key}).Decode(&e); err != nil {
		return ai.EmailAnalysis{}, false
	}
	return ai.EmailAnalysis{
		Action:     e.Action,
		LabelName:  e.LabelName,
		Confidence: e.Confidence,
		Reasoning:  e.Reasoning,
	}, true
}

func (h *Handler) cacheStore(ctx context.Context, key string, a ai.EmailAnalysis) {
	h.db.AnalysisCache().UpdateOne(ctx,
		bson.M{"key": key},
		bson.M{"$set": bson.M{
			"key":        key,
			"action":     a.Action,
			"labelName":  a.LabelName,
			"confidence": a.Confidence,
			"reasoning":  a.Reasoning,
			"createdAt":  time.Now(),
		}},
		options.Update().SetUpsert(true),
	)
}
