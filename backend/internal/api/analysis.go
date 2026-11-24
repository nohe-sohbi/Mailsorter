package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/ai"
	"github.com/nohe-sohbi/mailsorter/backend/internal/database"
	"github.com/nohe-sohbi/mailsorter/backend/internal/gmail"
	"github.com/nohe-sohbi/mailsorter/backend/internal/imap"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// analysisBatchSize controls how many emails go into a single model call,
// whichever provider the registry picks.
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

	// The model is told nothing of the user's labels or folders: its prompt
	// carries what the public pages say it does (sender, subject, an excerpt),
	// and its verdict goes into a cache every account shares. What it proposes
	// is matched with the user's own here, after the fact (mailbox.MatchLabel):
	// the Gmail labels Mailsorter made, or over IMAP the user's folders
	// (fileVerdict).
	protectedList := h.protectedValues(ctx, userEmail)

	session, serr := h.openSession(ctx, userEmail)
	if serr == nil {
		defer session.Close()
	} else {
		log.Printf("runAnalysis: openSession for %s: %v", userEmail, serr)
	}
	transport := h.analysisTransport(ctx, userEmail, session)
	// The Gmail labels Mailsorter made are matched over the Gmail API only.
	// Over IMAP only the folders count (fileVerdict): an account that came
	// from Gmail keeps its smart labels, and matching "Facture" with one of
	// them would leave the proposal beside the folder "Factures".
	var knownLabels []string
	if transport != provider.TransportIMAP {
		var lErr error
		if knownLabels, lErr = h.getSmartLabelNames(ctx, userEmail); lErr != nil {
			log.Printf("runAnalysis: the labels of %s: %v", userEmail, lErr)
		}
	}

	emails := make([]models.Email, 0, len(emailIDs))
	for _, id := range emailIDs {
		var e models.Email
		findErr := h.db.Emails().FindOne(ctx, bson.M{"messageId": id, "userId": userEmail}).Decode(&e)
		if findErr == nil && session != nil && session.Transport == provider.TransportIMAP && h.leftInbox(ctx, userEmail, id) {
			// Tidied since the screen was loaded: a proposal about it would
			// invite tidying it a second time, and cost an analysis.
			continue
		}
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
						Snippet:       gmail.Snippet(msg),
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
		if session != nil {
			var pref models.SenderPreference
			err := h.db.SenderPreferences().FindOne(ctx, bson.M{
				"userId":      userEmail,
				"senderEmail": email.From,
				"autoApply":   true,
			}).Decode(&pref)
			if err == nil && pref.DefaultAction != "" && allows(pref.DefaultAction, email.From, protectedList) &&
				h.autoApplySender(ctx, session, userEmail, email, pref) {
				p.AutoApplied++
				p.Processed++
				report()
				continue
			}
		}

		key := analysisCacheKey(email.From, email.Subject)
		if cached, ok := h.cacheLookup(ctx, key); ok {
			cached = fileVerdict(protectAnalysis(cached, email.From, protectedList, transport), session)
			if s, inserted := h.persistSuggestion(ctx, userEmail, email, cached, knownLabels, transport); inserted {
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

		aiClient := h.resolveAnalyzer(ctx, userEmail)
		if !aiClient.Available() {
			log.Printf("runAnalysis: AI client is nil")
			lastErr = fmt.Errorf("service IA non disponible")
			break
		}

		// When analyzing a single email, call AnalyzeEmail directly for maximum reliability.
		if len(chunk) == 1 {
			single, err := aiClient.AnalyzeEmail(chunk[0])
			if err != nil {
				log.Printf("runAnalysis: AnalyzeEmail error for %s: %v", chunk[0].MessageID, err)
				lastErr = err
				p.Processed++
				report()
				continue
			}
			p.Analyzed++
			h.cacheStore(ctx, analysisCacheKey(chunk[0].From, chunk[0].Subject), *single)
			verdict := fileVerdict(protectAnalysis(*single, chunk[0].From, protectedList, transport), session)
			if s, inserted := h.persistSuggestion(ctx, userEmail, chunk[0], verdict, knownLabels, transport); inserted {
				suggestions = append(suggestions, s)
				p.SuggestionsCreated++
			}
			p.Processed++
			report()
			continue
		}

		var analyses []ai.EmailAnalysis
		if res, err := aiClient.AnalyzeBatch(chunk); err == nil {
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
				single, err := aiClient.AnalyzeEmail(email)
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
			a = fileVerdict(protectAnalysis(a, email.From, protectedList, transport), session)
			if s, inserted := h.persistSuggestion(ctx, userEmail, email, a, knownLabels, transport); inserted {
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

// analysisTransport is the transport the analysis shields protected senders
// for: the session's, or, when it could not open, the one the account is on.
// Unknown, it is the Gmail API's reading, where a label leaves the mail in
// the inbox.
func (h *Handler) analysisTransport(ctx context.Context, userEmail string, session *mailSession) provider.Transport {
	if session != nil {
		return session.Transport
	}
	transport, _, err := h.transportFor(ctx, userEmail)
	if err != nil {
		log.Printf("runAnalysis: which transport %s is on: %v", userEmail, err)
		return ""
	}
	return transport
}

// protectAnalysis downgrades to "keep" a verdict that would take the mail of
// a sender on the user's protected list out of the inbox, so a VIP's mail is
// never suggested for archive/trash, nor, over IMAP where a label is a folder,
// for filing (allowsOn): the plan does not propose what "Tout ranger" would
// refuse. Other verdicts pass through untouched.
func protectAnalysis(a ai.EmailAnalysis, from string, protectedList []string, transport provider.Transport) ai.EmailAnalysis {
	if allowsOn(transport, a.Action, from, protectedList) {
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

// fileVerdict settles, over IMAP, where a "label" verdict files the mail: the
// user's folder named alike, or the cleaned name of the folder Mailsorter
// would make (imap.Client.FilingName), which the plan then shows and the
// history records. A label naming a folder that is not the user's (the inbox,
// a special folder, a shared one) or a name no folder is made under becomes
// "keep": the plan proposes nothing "Appliquer" would refuse. Over the Gmail
// API, or without a session, the verdict passes untouched.
func fileVerdict(a ai.EmailAnalysis, session *mailSession) ai.EmailAnalysis {
	if a.Action != "label" || session == nil || session.Transport != provider.TransportIMAP || session.imapc == nil {
		return a
	}
	name, err := session.imapc.FilingName(a.LabelName)
	switch {
	case err == nil:
		a.LabelName = name
		return a
	case errors.Is(err, imap.ErrNotOwnFolder):
		a.Reasoning = "Le dossier proposé est un dossier spécial de votre boîte : le mail reste dans la boîte."
	case errors.Is(err, imap.ErrBadFolderName), errors.Is(err, mailbox.ErrLabelRequired):
		a.Reasoning = "Le nom de dossier proposé n'est pas utilisable : le mail reste dans la boîte."
	default:
		log.Printf("runAnalysis: where to file under %q: %v", a.LabelName, err)
		return a
	}
	a.Action = "keep"
	a.LabelName = ""
	return a
}

// persistSuggestion resolves the label, inserts a pending suggestion and
// returns it. Over the Gmail API the label is matched with the ones Mailsorter
// made (knownLabels) and takes its Gmail id. Over IMAP it was settled already,
// against the folders (fileVerdict): neither the Gmail labels nor their ids
// have a say there.
func (h *Handler) persistSuggestion(
	ctx context.Context,
	userEmail string,
	email models.Email,
	a ai.EmailAnalysis,
	knownLabels []string,
	transport provider.Transport,
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

	if a.Action == "label" && a.LabelName != "" && transport != provider.TransportIMAP {
		matched := localMatchLabel(a.LabelName, knownLabels)
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

// localMatchLabel maps a suggested label onto an existing one without an AI
// call: the one named alike (mailbox.MatchLabel: case, accents and a plural
// aside, never a part of a name, so "Pro" does not take "Promotions").
func localMatchLabel(suggested string, existing []string) string {
	if matched, ok := mailbox.MatchLabel(suggested, existing); ok {
		return matched
	}
	return suggested
}

// analysisCacheKey starts with the cache version (database.AnalysisCacheVersion),
// so a verdict written for another one is never served, even before the boot
// that deletes it.
func analysisCacheKey(from, subject string) string {
	sum := sha256.Sum256([]byte(database.AnalysisCacheVersion + "|" + strings.ToLower(strings.TrimSpace(from)) + "|" + strings.ToLower(strings.TrimSpace(subject))))
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
			"version":    database.AnalysisCacheVersion,
			"action":     a.Action,
			"labelName":  a.LabelName,
			"confidence": a.Confidence,
			"reasoning":  a.Reasoning,
			"createdAt":  time.Now(),
		}},
		options.Update().SetUpsert(true),
	)
}
