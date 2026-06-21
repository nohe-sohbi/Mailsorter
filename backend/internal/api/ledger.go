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
)

// logAction appends one entry to the action ledger. Best-effort: a ledger
// failure must never break the underlying action, so the error is ignored.
func (h *Handler) logAction(ctx context.Context, userEmail, messageID, action, source string) {
	if action == "" || action == "keep" {
		return // nothing was mutated in Gmail
	}
	h.db.ActionLog().InsertOne(ctx, models.ActionLog{
		UserID:    userEmail,
		MessageID: messageID,
		Action:    action,
		Source:    source,
		CreatedAt: time.Now(),
	})
}
