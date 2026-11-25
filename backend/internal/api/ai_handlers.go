package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/nohe-sohbi/mailsorter/backend/internal/imap"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/protect"
	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// AnalyzeEmails synchronously analyzes selected emails and creates AI suggestions.
func (h *Handler) AnalyzeEmails(w http.ResponseWriter, r *http.Request) {
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
	if len(req.EmailIDs) > maxBatchActionSize {
		writeError(w, http.StatusBadRequest, "Too many emails to analyze at once")
		return
	}

	aiClient := h.resolveAnalyzer(r.Context(), userEmail)
	if !aiClient.Available() {
		writeError(w, http.StatusServiceUnavailable, "Aucune IA n'est configurée sur cette instance.")
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
// over whichever transport the session is on, recording it as an
// already-applied suggestion. Returns true on success so the caller can skip
// the AI analysis for that email. Over IMAP a label files the mail into the
// folder of that name, settled and made as for the AI's proposal
// (applySessionVerdict); a filing refused there (a special folder, one new
// folder too many) returns false, and the email goes to the analysis like any
// other.
func (h *Handler) autoApplySender(ctx context.Context, session *mailSession, userEmail string, email models.Email, pref models.SenderPreference) bool {
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

	labelID, err := h.applySessionVerdict(ctx, session, userEmail, email.MessageID, pref.DefaultAction, pref.DefaultLabel, "")
	if err != nil {
		if !errors.Is(err, errAlreadyOut) {
			log.Printf("auto-pilot: %s on message %s of %s not applied, left to the analysis: %v", pref.DefaultAction, email.MessageID, userEmail, err)
		}
		return false
	}
	suggestion.LabelID = labelID
	// Over IMAP the folder it went into, which the history and the plan
	// name (appliedFields does the same for a proposal).
	filed := filedUnder(suggestion, session.Transport)
	if filed != "" {
		suggestion.LabelName = filed
	}

	h.db.AISuggestions().InsertOne(ctx, suggestion)
	h.logActionFiled(ctx, userEmail, email.MessageID, pref.DefaultAction, SourceAIAuto, email.Subject, email.From, filed)
	return true
}

// AnalyzeSender analyzes all emails from a specific sender
func (h *Handler) AnalyzeSender(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	aiClient := h.resolveAnalyzer(r.Context(), userEmail)
	if !aiClient.Available() {
		writeError(w, http.StatusServiceUnavailable, "Aucune IA n'est configurée sur cette instance.")
		return
	}
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
		"userId":    userEmail,
		"leftInbox": notLeftInbox,
		"from":      bson.M{"$regex": regexp.QuoteMeta(req.SenderEmail), "$options": "i"},
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

	// The model is told nothing of the user's labels (see runAnalysis): the
	// label it proposes is matched with the user's afterwards.
	analysis, err := aiClient.AnalyzeSender(req.SenderEmail, emails)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to analyze sender: "+err.Error())
		return
	}
	if analysis.SuggestedLabel != "" {
		knownLabels, lErr := h.getSmartLabelNames(ctx, userEmail)
		if lErr != nil {
			log.Printf("analyze-sender: the labels of %s: %v", userEmail, lErr)
		}
		analysis.SuggestedLabel = localMatchLabel(analysis.SuggestedLabel, knownLabels)
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

// ApplySuggestion applies a single AI suggestion to the user's mailbox, over
// whichever transport it uses.
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
	if suggestion.Status == proposalMoot {
		// Its mail left the inbox some other way: nothing to apply.
		writeJSON(w, http.StatusOK, map[string]string{"status": "already"})
		return
	}

	session, err := h.openSession(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	defer session.Close()

	labelID, err := h.applySessionVerdict(ctx, session, userEmail, suggestion.EmailID, suggestion.Action, suggestion.LabelName, "")
	suggestion.LabelID = labelID
	if why, ok := filingRefusal(err); ok {
		writeError(w, http.StatusUnprocessableEntity, why)
		return
	}
	if errors.Is(err, errAlreadyOut) {
		// The mail was already tidied by hand: the proposal is moot. It
		// leaves the plan, and nothing is counted or recorded.
		h.dropMootProposal(ctx, objectID)
		writeJSON(w, http.StatusOK, map[string]string{"status": "already"})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "L'action n'a pas pu être appliquée : "+err.Error())
		return
	}

	// Update suggestion status
	h.db.AISuggestions().UpdateOne(ctx,
		bson.M{"_id": objectID},
		bson.M{"$set": appliedFields(suggestion, session.Transport)},
	)
	meta := h.identityIn(ctx, session, userEmail, suggestion.EmailID)
	filed := filedUnder(suggestion, session.Transport)
	h.logActionFiled(ctx, userEmail, suggestion.EmailID, suggestion.Action, SourceAI, meta.Subject, meta.From, filed)

	answer := map[string]string{"status": "applied"}
	if filed != "" {
		// The name it was filed under, settled when applying: over IMAP the
		// folder ("Factures"), which can differ from the name the proposal
		// was stored with ("Facture"). The screen says this one.
		answer["labelName"] = filed
	}
	writeJSON(w, http.StatusOK, answer)
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
	if len(req.SuggestionIDs) > maxBatchActionSize {
		writeError(w, http.StatusBadRequest, "Too many suggestions to apply at once")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	session, err := h.openSession(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	defer session.Close()

	protectedList := h.protectedValues(ctx, userEmail)
	applied := 0
	appliedIDs := make([]string, 0, len(req.SuggestionIDs))
	// The name each applied filing went under, settled when applying (see
	// ApplySuggestion), keyed by suggestion id.
	labelNames := map[string]string{}
	failed := 0
	protectedSkipped := 0
	// Why some failed, when the user can do something about it: each
	// sentence once, in the order met.
	var reasons []string

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
		if suggestion.Status == proposalMoot {
			// Its mail left the inbox some other way (a screen loaded before
			// still sends its id): neither applied nor failed.
			continue
		}

		// Shield protected senders from a bulk "apply all" that would take
		// their mail out of the inbox (archive, trash, and over IMAP filing
		// into a folder). The suggestion is left pending, untouched.
		if len(protectedList) > 0 && !allowsOn(session.Transport, suggestion.Action, h.senderOf(ctx, userEmail, suggestion.EmailID), protectedList) {
			protectedSkipped++
			continue
		}

		labelID, applyErr := h.applySessionVerdict(ctx, session, userEmail, suggestion.EmailID, suggestion.Action, suggestion.LabelName, "")
		suggestion.LabelID = labelID

		if errors.Is(applyErr, errAlreadyOut) {
			// Already tidied by hand: not applied, not failed, and the moot
			// proposal leaves the plan.
			h.dropMootProposal(ctx, objectID)
			continue
		}
		if why, ok := filingRefusal(applyErr); ok && !containsString(reasons, why) {
			reasons = append(reasons, why)
		}
		if applyErr != nil {
			failed++
			continue
		}

		h.db.AISuggestions().UpdateOne(ctx,
			bson.M{"_id": objectID},
			bson.M{"$set": appliedFields(suggestion, session.Transport)},
		)
		meta := identities[suggestion.EmailID]
		filed := filedUnder(suggestion, session.Transport)
		h.logActionFiled(ctx, userEmail, suggestion.EmailID, suggestion.Action, SourceAI, meta.Subject, meta.From, filed)
		if filed != "" {
			labelNames[id] = filed
		}
		applied++
		appliedIDs = append(appliedIDs, id)
	}

	answer := map[string]interface{}{
		"applied":          applied,
		"failed":           failed,
		"total":            len(req.SuggestionIDs),
		"appliedIds":       appliedIDs,
		"labelNames":       labelNames,
		"protectedSkipped": protectedSkipped,
		// Lets the client offer "Annuler" only where it would work, the way
		// batch-action already says so for a selection.
		"reversible": batchUndoReaches(session.Transport),
	}
	if len(reasons) > 0 {
		answer["message"] = strings.Join(reasons, " ")
	}
	writeJSON(w, http.StatusOK, answer)
}

// ApplyBulk applies an action to all emails from a sender, over whichever
// transport the mailbox uses. Over IMAP a label files them into the folder of
// that name, settled once (FilingName) and made if missing, all in one move
// (MoveOutMany), as a selection is; archiving and deleting move at once too.
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

	session, err := h.openSession(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	defer session.Close()
	imapBox := session.Transport == provider.TransportIMAP

	// The sender is the key of an Expediteurs card: a stored From header, as
	// GetSenders groups them. The sweep reaches exactly that group, by equality,
	// so it acts on the mails the card counted and announced, no more. It used
	// to be a substring match, which also swept every From that merely
	// contained the key: a longer address on the same domain, or the same
	// address on a longer domain.
	cursor, err := h.db.Emails().Find(ctx, bson.M{
		"userId":    userEmail,
		"leftInbox": notLeftInbox,
		"from":      req.SenderEmail,
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

	// Prepare label if needed: resolved once before the loop, a bulk apply
	// must not create the same label per message.
	var labelID string
	switch {
	case req.Action == "label" && imapBox:
		labelID, err = session.imapc.FilingName(req.LabelName)
		if why, ok := filingRefusal(err); ok {
			writeError(w, http.StatusUnprocessableEntity, why)
			return
		}
		if err != nil {
			writeError(w, http.StatusBadGateway, "Classement impossible : "+err.Error())
			return
		}
	case req.Action == "label" && req.LabelName != "":
		labelID, err = h.ensureLabel(ctx, session.gmail, userEmail, req.LabelName)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to create label: "+err.Error())
			return
		}
	}
	filed := ""
	if req.Action == "label" {
		filed = req.LabelName
		if imapBox {
			filed = labelID
		}
	}

	// Apply action to each email, shielding protected senders from what takes
	// their mail out of the inbox: archive and trash, and over IMAP filing
	// into a folder (allowsOn). A protected sender can still be labelled in
	// bulk over the Gmail API, where a label leaves the mail where it is.
	protectedList := h.protectedValues(ctx, userEmail)
	appliedCount := 0
	protectedSkipped := 0
	message := ""
	eligible := make([]models.Email, 0, len(emails))
	for _, email := range emails {
		if !allowsOn(session.Transport, req.Action, email.From, protectedList) {
			protectedSkipped++
			continue
		}
		eligible = append(eligible, email)
	}
	applied := func(email models.Email) {
		appliedCount++
		h.logActionFiled(ctx, userEmail, email.MessageID, req.Action, SourceBulk, email.Subject, email.From, filed)
	}
	notApplied := func(email models.Email, err error) {
		log.Printf("apply-bulk: %s on message %s of %s: %v", req.Action, email.MessageID, userEmail, err)
		if why, ok := filingRefusal(err); ok && message == "" {
			message = why
		}
	}

	if imapMove, mut := movesOutOfIMAPInbox(session.Mailbox(), req.Action, labelID); imapMove != nil {
		ids := make([]string, 0, len(eligible))
		for _, email := range eligible {
			ids = append(ids, email.MessageID)
		}
		outcome := imapMove.MoveOutMany(ctx, ids, mut)
		moved := make(map[string]bool, len(outcome.Moved))
		for _, id := range outcome.Moved {
			moved[id] = true
		}
		for _, email := range eligible {
			switch {
			case moved[email.MessageID]:
				applied(email)
			case outcome.Failed[email.MessageID] != nil:
				notApplied(email, outcome.Failed[email.MessageID])
			}
			// Otherwise it had already left the inbox: nothing happened,
			// nothing to count.
		}
	} else {
		for _, email := range eligible {
			_, applyErr := h.applySessionVerdict(ctx, session, userEmail, email.MessageID, req.Action, "", labelID)
			if errors.Is(applyErr, errAlreadyOut) {
				// Already out of the inbox: nothing happened, nothing to count.
				continue
			}
			if applyErr != nil {
				notApplied(email, applyErr)
				continue
			}
			applied(email)
		}
	}

	answer := map[string]interface{}{
		"applied":          appliedCount,
		"total":            len(emails),
		"protectedSkipped": protectedSkipped,
	}
	if filed != "" {
		answer["labelName"] = filed
	}
	if message != "" {
		answer["message"] = message
	}
	writeJSON(w, http.StatusOK, answer)
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
		// SenderAddress is From read by protect.Address: what the plan
		// groups by and makes a sender rule of, "" when From does not read.
		SenderAddress string `json:"senderAddress,omitempty"`
	}
	views := make([]suggestionView, 0, len(suggestions))
	for _, s := range suggestions {
		v := suggestionView{AISuggestion: s}
		if e, ok := identities[s.EmailID]; ok {
			v.Subject, v.From, v.Snippet = e.Subject, e.From, e.Snippet
			v.SenderAddress = protect.Address(e.From)
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

// maxRejectBatchSize bounds one reject-batch. The client never holds more than
// the 100 pending suggestions GetSuggestions returns, so this is "everything on
// screen" with room to spare.
const maxRejectBatchSize = 200

// RejectBatch rejects several pending suggestions in one request: a whole
// sender group, or everything the panel shows.
//
// The client used to send one POST /api/ai/suggestions/{id}/reject per row, all
// at once. Past the rate limiter's burst of 40 the rest were answered 429, the
// client had already hidden the rows and swallowed the failures, and the next
// refresh brought them back: "Tout ignorer" on a full panel left about sixty
// of a hundred suggestions pending. One UpdateMany is one request at any size.
//
// Only pending rows move. An applied suggestion is history, and rejecting it
// after the fact would contradict what the ledger says happened.
func (h *Handler) RejectBatch(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	var req models.RejectBatchRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.SuggestionIDs) == 0 {
		writeError(w, http.StatusBadRequest, "Aucune suggestion à ignorer")
		return
	}
	if len(req.SuggestionIDs) > maxRejectBatchSize {
		writeError(w, http.StatusBadRequest, "Trop de suggestions à ignorer en une fois")
		return
	}
	// Every id is checked before the datastore is touched. They all come from
	// GetSuggestions, so a malformed one is a client bug, and skipping it would
	// report the batch as done while that suggestion stays pending.
	objectIDs := make([]primitive.ObjectID, 0, len(req.SuggestionIDs))
	for _, id := range req.SuggestionIDs {
		oid, err := primitive.ObjectIDFromHex(id)
		if err != nil {
			writeError(w, http.StatusBadRequest, "Identifiant de suggestion invalide")
			return
		}
		objectIDs = append(objectIDs, oid)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	result, err := h.db.AISuggestions().UpdateMany(ctx,
		bson.M{"_id": bson.M{"$in": objectIDs}, "userId": userEmail, "status": "pending"},
		bson.M{"$set": bson.M{"status": "rejected"}},
	)
	if err != nil {
		log.Printf("ai: reject-batch failed for %s: %v", userEmail, err)
		writeError(w, http.StatusInternalServerError, "Failed to reject suggestions")
		return
	}

	writeJSON(w, http.StatusOK, map[string]int64{
		"rejected": result.ModifiedCount,
		"total":    int64(len(req.SuggestionIDs)),
	})
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
		{"$match": bson.M{"userId": userEmail, "leftInbox": notLeftInbox}},
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

	if label.Name == "" || len(label.Name) > 100 {
		writeError(w, http.StatusBadRequest, "Label name required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	gmailClient, err := h.gmailClientFor(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}

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

// dropMootProposal sets moot a pending proposal about a mail that already
// left the inbox, so it leaves the plan (see proposalMoot). Best effort: a
// failure is logged, and the next sync does it.
func (h *Handler) dropMootProposal(ctx context.Context, id primitive.ObjectID) {
	if _, err := h.db.AISuggestions().UpdateOne(ctx,
		bson.M{"_id": id, "status": "pending"},
		bson.M{"$set": bson.M{"status": proposalMoot}},
	); err != nil {
		log.Printf("ai: set moot the proposal %s: %v", id.Hex(), err)
	}
}

// leftInbox reports whether Mailsorter already moved this IMAP message out of
// the inbox (mirroredIMAP flagged its stored copy).
// A store that cannot answer reads as "still in the inbox": the action then
// goes to the server, which knows.
func (h *Handler) leftInbox(ctx context.Context, userEmail, messageID string) bool {
	n, err := h.db.Emails().CountDocuments(ctx, bson.M{
		"userId": userEmail, "folder": string(mailbox.FolderInbox), "messageId": messageID, "leftInbox": true,
	})
	if err != nil {
		log.Printf("ai: did message %s of %s leave the inbox: %v", messageID, userEmail, err)
		return false
	}
	return n > 0
}

// notOwnFolderMessage is what the user reads when a proposal names, as the
// folder to file into, one that is not theirs (imap.ErrNotOwnFolder): the
// inbox, the archive, the trash, sent mail, drafts or junk.
const notOwnFolderMessage = "Mailsorter ne classe pas de mail dans ce dossier : c'est un dossier spécial de votre boîte."

// badFolderNameMessage is what the user reads when a proposal names a folder
// no folder is made under (imap.ErrBadFolderName).
const badFolderNameMessage = "Mailsorter ne crée pas de dossier sous ce nom : il est trop long ou contient un caractère réservé."

// tooManyNewFoldersMessage is why "Tout ranger" left mails in place once it
// made imap.maxNewFolders new folders in one go.
const tooManyNewFoldersMessage = "Mailsorter crée au plus 10 nouveaux dossiers à la fois : relancez le tri pour ranger les mails suivants."

// labelRequiredOnIMAPMessage is what the user reads for a label proposal that
// names no folder (mailbox.ErrLabelRequired, over IMAP).
const labelRequiredOnIMAPMessage = "Cette proposition ne nomme aucun dossier où classer le mail."

// filingRefusal is the sentence for a filing refused over IMAP, and whether
// err is one: a folder that is not the user's, a name no folder is made
// under, no name at all, or one new folder too many for this request.
// "Appliquer" answers it with a 422, "Tout ranger" in its message.
func filingRefusal(err error) (string, bool) {
	switch {
	case err == nil:
		return "", false
	case errors.Is(err, imap.ErrNotOwnFolder):
		return notOwnFolderMessage, true
	case errors.Is(err, imap.ErrBadFolderName):
		return badFolderNameMessage, true
	case errors.Is(err, imap.ErrTooManyNewFolders):
		return tooManyNewFoldersMessage, true
	case errors.Is(err, mailbox.ErrLabelRequired):
		return labelRequiredOnIMAPMessage, true
	}
	return "", false
}

// containsString reports whether list holds s.
func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// filedUnder is the label a proposal filed its mail under, for the history:
// over IMAP the folder the filing resolved to (applySessionVerdict hands it
// back as the label id: "Amazon-fr" for a proposal saying "Amazon.fr"), over
// the Gmail API the label's name; nothing for another verdict.
func filedUnder(s models.AISuggestion, transport provider.Transport) string {
	if s.Action != "label" {
		return ""
	}
	if transport == provider.TransportIMAP && s.LabelID != "" {
		return s.LabelID
	}
	return s.LabelName
}

// appliedFields is what an applied proposal stores: its status, when, and the
// label id it was applied with; over IMAP that id is the folder it filed into,
// which is also its name there.
func appliedFields(s models.AISuggestion, transport provider.Transport) bson.M {
	fields := bson.M{"status": "applied", "appliedAt": time.Now(), "labelId": s.LabelID}
	if s.Action == "label" && transport == provider.TransportIMAP && s.LabelID != "" {
		fields["labelName"] = s.LabelID
	}
	return fields
}

// applySessionVerdict applies one verdict (the AI's, a sender's auto-pilot, a
// sweep by sender) to one message, over whichever transport the session is on.
//
// "keep" is not a mutation but a decision not to act, so it succeeds having
// changed nothing. Everything else goes through the neutral vocabulary, which
// also closed a latent hole: three of the four call sites this replaced had no
// default branch, so an action they did not recognise left the error nil and
// the message was then recorded, and counted, as applied without the mailbox
// ever being touched. An unknown verb now fails, loudly.
//
// The resolved label id comes back because the caller stores it on the
// suggestion: it is what makes the action undoable later.
func (h *Handler) applySessionVerdict(ctx context.Context, session *mailSession, userEmail, messageID, action, labelName, labelID string) (string, error) {
	if action == "keep" {
		return "", nil
	}
	if session.Transport == provider.TransportIMAP && h.leftInbox(ctx, userEmail, messageID) {
		// The user already tidied this mail by hand: the proposal is moot,
		// whatever it proposed. Nothing moves, nothing is counted or recorded.
		return "", errAlreadyOut
	}
	if action == "label" {
		if session.Transport == provider.TransportIMAP {
			// A message sits in one folder over IMAP, and the folder named
			// like the label stands for it: the name is what the mutation
			// carries (mailbox.IMAPOpFor), and the folder is made if missing.
			// Settled again here, as the analysis does (FilingName): a
			// proposal stored while the mailbox could not be opened carries
			// the model's own name, and "Facture" must go into "Factures",
			// "Amazon.fr" into "Amazon-fr". The name returned is the folder's.
			folder, err := session.imapc.FilingName(labelName)
			if err != nil {
				return "", err
			}
			return folder, h.applyVerb(ctx, session.Mailbox(), session.RefFor(ctx, messageID), action, folder)
		}
		if labelID == "" && session.gmail != nil {
			resolved, err := h.ensureLabel(ctx, session.gmail, userEmail, labelName)
			if err != nil {
				return "", err
			}
			labelID = resolved
		}
	}
	return labelID, h.applyVerb(ctx, session.Mailbox(), session.RefFor(ctx, messageID), action, labelID)
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

// The sender helpers below read a stored From header: the raw header on the
// Gmail API, "Name" <address> as the IMAP listing writes it. The address is
// always the one protect.Address reads, never one in the display name, which
// the sender chooses: "<boss@corp.com>" <spam@evil.io> is spam@evil.io, and
// unsubscribing from it, archiving its mail or making a rule of it must not
// reach boss@corp.com.

// extractDomain returns the domain of the sender's address, or the input,
// trimmed, when it holds no "@".
func extractDomain(email string) string {
	addr := protect.Address(email)
	if idx := strings.LastIndex(addr, "@"); idx >= 0 {
		return addr[idx+1:]
	}
	return addr
}

// extractSenderAddress returns the sender's bare address out of a From header
// (protect.Address), as written.
func extractSenderAddress(from string) string {
	return protect.Address(from)
}

// extractSenderName returns the display name of a From header, or the header
// itself when it has none.
func extractSenderName(from string) string {
	if a, err := mail.ParseAddress(strings.TrimSpace(from)); err == nil {
		if a.Name != "" {
			return a.Name
		}
		return from
	}
	// Not a header net/mail reads: the name is what comes before the address,
	// the last "<...>".
	if idx := strings.LastIndex(from, "<"); idx > 0 {
		return strings.Trim(strings.TrimSpace(from[:idx]), "\"")
	}
	return from
}
