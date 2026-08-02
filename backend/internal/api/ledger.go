package api

import (
	"context"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
)

// Action ledger sources. Every mutating Gmail action is tagged with where it
// originated, so the activity recap can attribute work truthfully.
const (
	SourceDirect      = "direct"      // single explicit action from the reader/shortcuts
	SourceRule        = "rule"        // deterministic rule (manual apply or at-sync autopilot)
	SourceAI          = "ai"          // an AI suggestion the user applied
	SourceAIAuto      = "ai-auto"     // sender auto-pilot (preference auto-applied)
	SourceBulk        = "bulk"        // bulk action across a sender
	SourceSnooze      = "snooze"      // snooze out of / back into the inbox
	SourceUnsubscribe = "unsubscribe" // archive triggered by an unsubscribe sweep
	SourceUndo        = "undo"        // a reversal performed from the action history
)

// logAction appends one entry to the action ledger. Best-effort: a ledger
// failure must never break the underlying action, so the error is ignored.
//
// Use logActionMeta instead whenever the caller already holds the message: the
// history is only useful if it says which email was acted on, and capturing the
// subject/sender at write time survives the email later leaving the local cache.
func (h *Handler) logAction(ctx context.Context, userEmail, messageID, action, source string) {
	h.logActionMeta(ctx, userEmail, messageID, action, source, "", "")
}

// logActionMeta is logAction with the email's human identity attached. Empty
// subject/from are stored as absent (omitempty) and resolved at read time by
// GetActionLog, so entries written before this existed still render.
func (h *Handler) logActionMeta(ctx context.Context, userEmail, messageID, action, source, subject, from string) {
	if action == "" || action == "keep" {
		return // nothing was mutated in Gmail
	}
	h.db.ActionLog().InsertOne(ctx, models.ActionLog{
		UserID:    userEmail,
		MessageID: messageID,
		Action:    action,
		Source:    source,
		Subject:   subject,
		From:      from,
		CreatedAt: time.Now(),
	})
}
