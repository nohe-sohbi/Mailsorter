package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/nohe-sohbi/mailsorter/backend/internal/gmail"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/rules"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	gmailapi "google.golang.org/api/gmail/v1"
)

// loadRules returns the user's rules sorted by priority (then creation), so the
// first match in slice order is always the highest-priority one.
func (h *Handler) loadRules(ctx context.Context, userEmail string) ([]models.SortingRule, error) {
	cursor, err := h.db.SortingRules().Find(ctx, bson.M{"userId": userEmail},
		options.Find().SetSort(bson.D{{Key: "priority", Value: 1}, {Key: "createdAt", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	out := make([]models.SortingRule, 0)
	if err := cursor.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// enabledRules returns the caller's enabled rules, sorted by priority (loadRules
// already sorts). Shared by the manual apply, the dry-run preview, and the
// at-sync autopilot so all three see exactly the same ruleset.
func (h *Handler) enabledRules(ctx context.Context, userEmail string) []models.SortingRule {
	ruleset, err := h.loadRules(ctx, userEmail)
	if err != nil {
		return nil
	}
	enabled := make([]models.SortingRule, 0, len(ruleset))
	for _, ru := range ruleset {
		if ru.Enabled {
			enabled = append(enabled, ru)
		}
	}
	return enabled
}

// GetRules lists the caller's deterministic sorting rules.
func (h *Handler) GetRules(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ruleset, err := h.loadRules(ctx, userEmail)
	if err != nil {
		http.Error(w, "Failed to load rules", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"rules": ruleset})
}

// CreateRule validates and persists a new rule.
func (h *Handler) CreateRule(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	var in models.SortingRuleInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	rule := ruleFromInput(userEmail, in)
	if err := rules.Validate(rule); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rule.CreatedAt = time.Now()
	rule.UpdatedAt = rule.CreatedAt

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := h.db.SortingRules().InsertOne(ctx, rule)
	if err != nil {
		http.Error(w, "Failed to save rule", http.StatusInternalServerError)
		return
	}
	if oid, ok := res.InsertedID.(primitive.ObjectID); ok {
		rule.ID = oid.Hex()
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(rule)
}

// UpdateRule replaces an existing rule's editable fields after validation.
func (h *Handler) UpdateRule(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	oid, err := primitive.ObjectIDFromHex(mux.Vars(r)["id"])
	if err != nil {
		http.Error(w, "Invalid rule ID", http.StatusBadRequest)
		return
	}

	var in models.SortingRuleInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	rule := ruleFromInput(userEmail, in)
	if err := rules.Validate(rule); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := h.db.SortingRules().UpdateOne(ctx,
		bson.M{"_id": oid, "userId": userEmail},
		bson.M{"$set": bson.M{
			"name":       rule.Name,
			"enabled":    rule.Enabled,
			"matchAll":   rule.MatchAll,
			"conditions": rule.Conditions,
			"action":     rule.Action,
			"labelName":  rule.LabelName,
			"priority":   rule.Priority,
			"updatedAt":  time.Now(),
		}},
	)
	if err != nil {
		http.Error(w, "Failed to update rule", http.StatusInternalServerError)
		return
	}
	if res.MatchedCount == 0 {
		http.Error(w, "Rule not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

// DeleteRule removes a rule the caller owns.
func (h *Handler) DeleteRule(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	oid, err := primitive.ObjectIDFromHex(mux.Vars(r)["id"])
	if err != nil {
		http.Error(w, "Invalid rule ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := h.db.SortingRules().DeleteOne(ctx, bson.M{"_id": oid, "userId": userEmail})
	if err != nil {
		http.Error(w, "Failed to delete rule", http.StatusInternalServerError)
		return
	}
	if res.DeletedCount == 0 {
		http.Error(w, "Rule not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
}

// ApplyRules runs every enabled rule across the current inbox. Each email is
// matched against the rules in priority order and the first match's action is
// applied via Gmail. This never calls the AI and never consumes quota — it is
// the free, deterministic counterpart to the AI triage.
func (h *Handler) ApplyRules(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	enabled := h.enabledRules(ctx, userEmail)
	if len(enabled) == 0 {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"applied": 0, "scanned": 0, "byRule": map[string]int{}})
		return
	}

	gmailClient, err := h.gmailClientFor(ctx, userEmail)
	if err != nil {
		http.Error(w, "Failed to get user credentials", http.StatusInternalServerError)
		return
	}

	messages, err := h.gmailService.ListMessages(gmailClient, "in:inbox", 200)
	if err != nil {
		http.Error(w, "Failed to read inbox: "+err.Error(), http.StatusBadGateway)
		return
	}

	labelCache := map[string]string{} // labelName -> Gmail label ID
	byRule := map[string]int{}        // rule name -> count applied
	applied := 0

	for _, msg := range messages {
		from, subject, to, date := gmail.ParseEmailHeaders(msg)
		email := models.Email{
			MessageID:    msg.Id,
			From:         from,
			To:           to,
			Subject:      subject,
			Snippet:      msg.Snippet,
			Body:         gmail.GetEmailBody(msg),
			ReceivedDate: date,
		}

		match := rules.FirstMatch(email, enabled)
		if match == nil {
			continue
		}
		if err := h.applyRuleAction(ctx, gmailClient, userEmail, msg.Id, *match, labelCache); err != nil {
			continue
		}
		applied++
		byRule[match.Name]++
	}

	// Persist per-rule application counts (best-effort).
	for name, n := range byRule {
		h.db.SortingRules().UpdateOne(ctx,
			bson.M{"userId": userEmail, "name": name},
			bson.M{"$inc": bson.M{"appliedCount": n}},
		)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"applied": applied,
		"scanned": len(messages),
		"byRule":  byRule,
	})
}

// previewSampleCap bounds how many example emails the dry-run returns.
const previewSampleCap = 12

// PreviewRules is a DRY RUN: it reports which emails each rule WOULD act on,
// without touching Gmail and without consuming quota. This lets users see the
// blast radius of their ruleset (especially before enabling at-sync autopilot)
// and build trust before any irreversible archive/trash happens.
func (h *Handler) PreviewRules(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	enabled := h.enabledRules(ctx, userEmail)
	if len(enabled) == 0 {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"scanned": 0, "willApply": 0, "byRule": []rules.RuleHits{}, "samples": []rules.PreviewItem{},
		})
		return
	}

	gmailClient, err := h.gmailClientFor(ctx, userEmail)
	if err != nil {
		http.Error(w, "Failed to get user credentials", http.StatusInternalServerError)
		return
	}

	messages, err := h.gmailService.ListMessages(gmailClient, "in:inbox", 200)
	if err != nil {
		http.Error(w, "Failed to read inbox: "+err.Error(), http.StatusBadGateway)
		return
	}

	emails := make([]models.Email, 0, len(messages))
	for _, msg := range messages {
		from, subject, to, date := gmail.ParseEmailHeaders(msg)
		emails = append(emails, models.Email{
			MessageID:    msg.Id,
			From:         from,
			To:           to,
			Subject:      subject,
			Snippet:      msg.Snippet,
			Body:         gmail.GetEmailBody(msg),
			ReceivedDate: date,
		})
	}

	items, hits := rules.Preview(emails, enabled)

	samples := items
	if len(samples) > previewSampleCap {
		samples = samples[:previewSampleCap]
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"scanned":   len(messages),
		"willApply": len(items),
		"byRule":    hits,
		"samples":   samples,
	})
}

// ruleForSender builds a deterministic "always do X to this sender" rule from a
// sender address and action. Pure (no I/O) so it is cheap to test. The sender
// is matched on From contains <address>, mirroring how the rest of the app
// scopes per-sender operations.
func ruleForSender(userEmail string, req models.CreateSenderRuleRequest) models.SortingRule {
	addr := extractSenderAddress(req.SenderEmail)
	now := time.Now()
	return models.SortingRule{
		UserID:   userEmail,
		Name:     "Expéditeur : " + addr,
		Enabled:  true,
		MatchAll: true,
		Conditions: []models.RuleCondition{
			{Field: rules.FieldFrom, Operator: rules.OpContains, Value: addr},
		},
		Action:    req.Action,
		LabelName: req.LabelName,
		Priority:  0,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// CreateSenderRule turns a sender into a permanent deterministic rule in one
// click — the concrete form of "learn once, apply forever". The new rule then
// runs for free on every manual apply and (if enabled) automatically at sync.
func (h *Handler) CreateSenderRule(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	var req models.CreateSenderRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if extractSenderAddress(req.SenderEmail) == "" {
		http.Error(w, "Adresse d'expéditeur requise", http.StatusBadRequest)
		return
	}

	rule := ruleForSender(userEmail, req)
	if err := rules.Validate(rule); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := h.db.SortingRules().InsertOne(ctx, rule)
	if err != nil {
		http.Error(w, "Failed to save rule", http.StatusInternalServerError)
		return
	}
	if oid, ok := res.InsertedID.(primitive.ObjectID); ok {
		rule.ID = oid.Hex()
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(rule)
}

// applyRuleAction performs a single rule's action on one message.
func (h *Handler) applyRuleAction(ctx context.Context, gmailClient *gmailapi.Service, userEmail, messageID string, rule models.SortingRule, labelCache map[string]string) error {
	svc := h.gmailService
	switch rule.Action {
	case rules.ActionArchive:
		return svc.ModifyMessage(gmailClient, messageID, nil, []string{"INBOX"})
	case rules.ActionTrash:
		return svc.ModifyMessage(gmailClient, messageID, []string{"TRASH"}, nil)
	case rules.ActionMarkRead:
		return svc.ModifyMessage(gmailClient, messageID, nil, []string{"UNREAD"})
	case rules.ActionStar:
		return svc.ModifyMessage(gmailClient, messageID, []string{"STARRED"}, nil)
	case rules.ActionLabel:
		labelID, ok := labelCache[rule.LabelName]
		if !ok {
			id, err := h.ensureLabel(ctx, gmailClient, userEmail, rule.LabelName)
			if err != nil {
				return err
			}
			labelID = id
			labelCache[rule.LabelName] = id
		}
		return svc.ModifyMessage(gmailClient, messageID, []string{labelID}, nil)
	}
	return nil
}

// ruleFromInput maps an input payload onto a SortingRule owned by the caller.
func ruleFromInput(userEmail string, in models.SortingRuleInput) models.SortingRule {
	return models.SortingRule{
		UserID:     userEmail,
		Name:       in.Name,
		Enabled:    in.Enabled,
		MatchAll:   in.MatchAll,
		Conditions: in.Conditions,
		Action:     in.Action,
		LabelName:  in.LabelName,
		Priority:   in.Priority,
	}
}
