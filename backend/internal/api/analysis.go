package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/ai"
	"github.com/nohe-sohbi/mailsorter/backend/internal/gmail"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// analysisBatchSize controls how many emails go into a single Mistral call.
const analysisBatchSize = 8

type analysisProgress struct {
	Total              int
	Processed          int
	AutoApplied        int
	SuggestionsCreated int
	CachedHits         int
	Analyzed           int // emails that actually hit the AI (counts toward quota)
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
	var lastErr error

	existingLabels, _ := h.getSmartLabelNames(ctx, userEmail)
	protectedList := h.protectedValues(ctx, userEmail)

	session, serr := h.openSession(ctx, userEmail)
	if serr == nil {
		defer session.Close()
	} else {
		log.Printf("runAnalysis: openSession for %s: %v", userEmail, serr)
	}

	emails := make([]models.Email, 0, len(emailIDs))
	for _, id := range emailIDs {
		var e models.Email
		findErr := h.db.Emails().FindOne(ctx, bson.M{"messageId": id, "userId": userEmail}).Decode(&e)
		if findErr == nil && (e.Snippet != "" || e.Body != "") {
			emails = append(emails, e)
			continue
		}

		fetched := false
		if session != nil {
			if session.Transport == provider.TransportIMAP && session.imapc != nil {
				ref := session.RefFor(ctx, id)
				if msg, ferr := session.imapc.Fetch(ctx, ref.Folder, ref.ID); ferr == nil {
					e = msg.Email
					e.UserID = userEmail
					e.Folder = ref.Folder
					opts := options.Update().SetUpsert(true)
					h.db.Emails().UpdateOne(ctx, bson.M{"messageId": id, "userId": userEmail}, bson.M{"$set": e}, opts)
					emails = append(emails, e)
					fetched = true
				} else {
					log.Printf("runAnalysis: failed to fetch IMAP message %s: %v", id, ferr)
					if findErr != nil {
						lastErr = fmt.Errorf("impossible de charger le message IMAP (%s): %w", id, ferr)
					}
				}
			} else if session.gmail != nil {
				if msg, gerr := h.gmailService.GetMessage(session.gmail, id); gerr == nil {
					from, subject, to, date := gmail.ParseEmailHeaders(msg)
					unsubURL, unsubMailto, oneClick := gmail.ParseUnsubscribe(msg)
					plain, _ := gmail.GetEmailBodies(msg)
					e = models.Email{
						MessageID:     msg.Id,
						UserID:        userEmail,
						ThreadID:      msg.ThreadId,
						From:          from,
						To:            to,
						Subject:       subject,
						Body:          plain,
						Snippet:       msg.Snippet,
						LabelIDs:      msg.LabelIds,
						ReceivedDate:  date,
						IsRead:        mailbox.GmailIsRead(msg.LabelIds),
						UnsubURL:      unsubURL,
						UnsubMailto:   unsubMailto,
						UnsubOneClick: oneClick,
						CreatedAt:     time.Now(),
					}
					opts := options.Update().SetUpsert(true)
					h.db.Emails().UpdateOne(ctx, bson.M{"messageId": id, "userId": userEmail}, bson.M{"$set": e}, opts)
					emails = append(emails, e)
					fetched = true
				} else {
					log.Printf("runAnalysis: failed to fetch Gmail message %s: %v", id, gerr)
					if findErr != nil {
						lastErr = fmt.Errorf("impossible de charger le message Gmail (%s): %w", id, gerr)
					}
				}
			}
		}

		if !fetched {
			if findErr == nil {
				emails = append(emails, e)
			} else if lastErr == nil {
				lastErr = fmt.Errorf("email introuvable et session de messagerie indisponible (%s)", id)
			}
		}
	}
	p.Total = len(emails)
	if len(emails) == 0 {
		if lastErr != nil {
			return p, suggestions, lastErr
		}
		return p, suggestions, fmt.Errorf("aucun email valide trouve a analyser")
	}

	report := func() {
		if onProgress != nil {
			onProgress(p)
		}
	}

	// Pass 1: resolve auto-pilot + cache hits, collect the rest for batching.
	pending := make([]models.Email, 0, len(emails))
	for _, email := range emails {
		if session != nil && session.gmail != nil {
			var pref models.SenderPreference
			err := h.db.SenderPreferences().FindOne(ctx, bson.M{
				"userId":      userEmail,
				"senderEmail": email.From,
				"autoApply":   true,
			}).Decode(&pref)
			if err == nil && pref.DefaultAction != "" && allows(pref.DefaultAction, email.From, protectedList) &&
				h.autoApplySender(ctx, session.gmail, userEmail, email, pref) {
				p.AutoApplied++
				p.Processed++
				report()
				continue
			}
		}

		key := analysisCacheKey(email.From, email.Subject)
		if cached, ok := h.cacheLookup(ctx, key); ok {
			cached = protectAnalysis(cached, email.From, protectedList)
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

		if h.aiClient == nil {
			log.Printf("runAnalysis: AI client is nil")
			lastErr = fmt.Errorf("service IA non disponible")
			break
		}

		// When analyzing a single email, call AnalyzeEmail directly for maximum reliability.
		if len(chunk) == 1 {
			single, err := h.aiClient.AnalyzeEmail(chunk[0], existingLabels)
			if err != nil {
				log.Printf("runAnalysis: AnalyzeEmail error for %s: %v", chunk[0].MessageID, err)
				lastErr = err
				p.Processed++
				report()
				continue
			}
			p.Analyzed++
			h.cacheStore(ctx, analysisCacheKey(chunk[0].From, chunk[0].Subject), *single)
			verdict := protectAnalysis(*single, chunk[0].From, protectedList)
			if s, inserted := h.persistSuggestion(ctx, userEmail, chunk[0], verdict, existingLabels); inserted {
				suggestions = append(suggestions, s)
				p.SuggestionsCreated++
			}
			p.Processed++
			report()
			continue
		}

		var analyses []ai.EmailAnalysis
		if res, err := h.aiClient.AnalyzeBatch(chunk, existingLabels); err == nil {
			analyses = res
		} else {
			log.Printf("runAnalysis: AnalyzeBatch failed (%v), falling back to AnalyzeEmail", err)
		}

		for j, email := range chunk {
			var a ai.EmailAnalysis
			switch {
			case analyses != nil && j < len(analyses):
				a = analyses[j]
			default:
				single, err := h.aiClient.AnalyzeEmail(email, existingLabels)
				if err != nil {
					log.Printf("runAnalysis: fallback AnalyzeEmail error for %s: %v", email.MessageID, err)
					lastErr = err
					p.Processed++
					report()
					continue
				}
				a = *single
			}

			p.Analyzed++
			h.cacheStore(ctx, analysisCacheKey(email.From, email.Subject), a)
			a = protectAnalysis(a, email.From, protectedList)
			if s, inserted := h.persistSuggestion(ctx, userEmail, email, a, existingLabels); inserted {
				suggestions = append(suggestions, s)
				p.SuggestionsCreated++
			}
			p.Processed++
			report()
		}
	}

	h.incrUsage(ctx, userEmail, p.Analyzed)

	if len(suggestions) == 0 && p.AutoApplied == 0 {
		if lastErr != nil {
			return p, suggestions, lastErr
		}
		return p, suggestions, fmt.Errorf("aucune recommandation générée pour cet email")
	}

	return p, suggestions, nil
}

// protectAnalysis downgrades a destructive AI verdict to "keep" when the sender
// is on the user's protected list, so a VIP's mail is never suggested for
// archive/trash. Non-destructive verdicts (label/keep) pass through untouched.
func protectAnalysis(a ai.EmailAnalysis, from string, protectedList []string) ai.EmailAnalysis {
	if allows(a.Action, from, protectedList) {
		return a
	}
	a.Action = "keep"
	a.LabelName = ""
	a.Reasoning = "Expéditeur protégé, conservé en boîte"
	if a.Confidence < 0.9 {
		a.Confidence = 0.9
	}
	return a
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

	h.db.AISuggestions().DeleteMany(ctx, bson.M{
		"userId":  userEmail,
		"emailId": email.MessageID,
		"status":  "pending",
	})

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
