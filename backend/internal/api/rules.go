package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/nohe-sohbi/mailsorter/backend/internal/gmail"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
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
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	ruleset, err := h.loadRules(ctx, userEmail)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load rules")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"rules": ruleset})
}

// CreateRule validates and persists a new rule.
func (h *Handler) CreateRule(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	var in models.SortingRuleInput
	if !decodeJSON(w, r, &in) {
		return
	}

	rule := ruleFromInput(userEmail, in)
	if err := rules.Validate(rule); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rule.CreatedAt = time.Now()
	rule.UpdatedAt = rule.CreatedAt

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	res, err := h.db.SortingRules().InsertOne(ctx, rule)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save rule")
		return
	}
	if oid, ok := res.InsertedID.(primitive.ObjectID); ok {
		rule.ID = oid.Hex()
	}

	writeJSON(w, http.StatusCreated, rule)
}

// UpdateRule replaces an existing rule's editable fields after validation.
func (h *Handler) UpdateRule(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	oid, err := primitive.ObjectIDFromHex(mux.Vars(r)["id"])
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid rule ID")
		return
	}

	var in models.SortingRuleInput
	if !decodeJSON(w, r, &in) {
		return
	}

	rule := ruleFromInput(userEmail, in)
	if err := rules.Validate(rule); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
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
			"actions":    rule.Actions,
			"priority":   rule.Priority,
			"updatedAt":  time.Now(),
		}},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update rule")
		return
	}
	if res.MatchedCount == 0 {
		writeError(w, http.StatusNotFound, "Rule not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// DeleteRule removes a rule the caller owns.
func (h *Handler) DeleteRule(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	oid, err := primitive.ObjectIDFromHex(mux.Vars(r)["id"])
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid rule ID")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	res, err := h.db.SortingRules().DeleteOne(ctx, bson.M{"_id": oid, "userId": userEmail})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to delete rule")
		return
	}
	if res.DeletedCount == 0 {
		writeError(w, http.StatusNotFound, "Rule not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ApplyRules runs every enabled rule across the current inbox. Each email is
// matched against the rules in priority order and the first match's action is
// applied via Gmail. This never calls the AI and never consumes quota: it is
// the free, deterministic counterpart to the AI triage.
func (h *Handler) ApplyRules(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	enabled := h.enabledRules(ctx, userEmail)
	if len(enabled) == 0 {
		writeJSON(w, http.StatusOK, map[string]interface{}{"applied": 0, "scanned": 0, "byRule": map[string]int{}})
		return
	}

	gmailClient, err := h.gmailClientFor(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	messages, err := h.gmailService.ListMessages(gmailClient, "in:inbox", 200)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Failed to read inbox: "+err.Error())
		return
	}

	protectedList := h.protectedValues(ctx, userEmail)
	labelCache := map[string]string{} // labelName -> Gmail label ID
	byRule := map[string]int{}        // rule name -> count applied
	applied := 0
	protectedSkipped := 0

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
		// A protected sender is shielded from destructive actions, but
		// non-destructive actions in the same rule still run.
		appliedActs, skipped := h.applyRuleToMessage(ctx, gmailClient, userEmail, msg.Id, email.From, *match, protectedList, labelCache)
		if len(appliedActs) == 0 {
			if skipped {
				protectedSkipped++
			}
			continue
		}
		applied++
		byRule[match.Name]++
		for _, act := range appliedActs {
			h.logActionMeta(ctx, userEmail, msg.Id, act, SourceRule, email.Subject, email.From)
		}
	}

	// Persist per-rule application counts (best-effort).
	for name, n := range byRule {
		h.db.SortingRules().UpdateOne(ctx,
			bson.M{"userId": userEmail, "name": name},
			bson.M{"$inc": bson.M{"appliedCount": n}},
		)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"applied":          applied,
		"scanned":          len(messages),
		"byRule":           byRule,
		"protectedSkipped": protectedSkipped,
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
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	enabled := h.enabledRules(ctx, userEmail)
	if len(enabled) == 0 {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"scanned": 0, "willApply": 0, "byRule": []rules.RuleHits{}, "samples": []rules.PreviewItem{},
		})
		return
	}

	gmailClient, err := h.gmailClientFor(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	messages, err := h.gmailService.ListMessages(gmailClient, "in:inbox", 200)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Failed to read inbox: "+err.Error())
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

	writeJSON(w, http.StatusOK, map[string]interface{}{
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
// click, the concrete form of "learn once, apply forever". The new rule then
// runs for free on every manual apply and (if enabled) automatically at sync.
func (h *Handler) CreateSenderRule(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	var req models.CreateSenderRuleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if extractSenderAddress(req.SenderEmail) == "" {
		writeError(w, http.StatusBadRequest, "Adresse d'expéditeur requise")
		return
	}

	rule := ruleForSender(userEmail, req)
	if err := rules.Validate(rule); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	res, err := h.db.SortingRules().InsertOne(ctx, rule)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save rule")
		return
	}
	if oid, ok := res.InsertedID.(primitive.ObjectID); ok {
		rule.ID = oid.Hex()
	}

	writeJSON(w, http.StatusCreated, rule)
}

// applyRuleToMessage runs every action of a matched rule on one message, in
// order. A protected (VIP) sender shields against destructive actions only:
// archive/trash are skipped, but a non-destructive action in the same rule
// (label/star/markRead) still runs. It returns the action types actually applied
// (for the ledger) and whether a destructive action was skipped for protection.
func (h *Handler) applyRuleToMessage(ctx context.Context, gmailClient *gmailapi.Service, userEmail, messageID, from string, rule models.SortingRule, protectedList []string, labelCache map[string]string) (applied []string, protectedSkip bool) {
	for _, a := range rules.EffectiveActions(rule) {
		if !allows(a.Type, from, protectedList) {
			protectedSkip = true
			continue
		}
		if err := h.applyOneAction(ctx, gmailClient, userEmail, messageID, a, labelCache); err != nil {
			continue
		}
		applied = append(applied, a.Type)
	}
	return applied, protectedSkip
}

// applyOneAction performs a single rule action on one message.
func (h *Handler) applyOneAction(ctx context.Context, gmailClient *gmailapi.Service, userEmail, messageID string, a models.RuleAction, labelCache map[string]string) error {
	// Only the label action needs anything resolved before it can run: the
	// label has to exist in the mailbox before a message can carry it, and the
	// cache keeps one rule application from creating it once per message.
	var labelID string
	if a.Type == rules.ActionLabel {
		id, ok := labelCache[a.LabelName]
		if !ok {
			created, err := h.ensureLabel(ctx, gmailClient, userEmail, a.LabelName)
			if err != nil {
				return err
			}
			id = created
			labelCache[a.LabelName] = created
		}
		labelID = id
	}

	// An action the rules engine validated but this transport does not know is
	// skipped rather than failed: it must not abort the rest of the ruleset.
	err := h.applyVerb(ctx, gmailClient, mailbox.OnAccount(messageID), a.Type, labelID)
	if errors.Is(err, mailbox.ErrUnknownAction) {
		return nil
	}
	return err
}

// ruleFromInput maps an input payload onto a SortingRule owned by the caller. It
// normalizes the two authoring shapes into one: when a client sends the
// multi-action Actions list, the legacy Action/LabelName fields are backfilled
// with the primary action so older readers (and per-rule stats) keep working.
func ruleFromInput(userEmail string, in models.SortingRuleInput) models.SortingRule {
	rule := models.SortingRule{
		UserID:     userEmail,
		Name:       in.Name,
		Enabled:    in.Enabled,
		MatchAll:   in.MatchAll,
		Conditions: in.Conditions,
		Action:     in.Action,
		LabelName:  in.LabelName,
		Actions:    in.Actions,
		Priority:   in.Priority,
	}
	if len(rule.Actions) > 0 {
		rule.Action = rule.Actions[0].Type
		rule.LabelName = rule.Actions[0].LabelName
	}
	return rule
}
