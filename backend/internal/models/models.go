package models

import (
	"time"
)

type User struct {
	ID           string    `json:"id" bson:"_id,omitempty"`
	Email        string    `json:"email" bson:"email"`
	AccessToken  string    `json:"-" bson:"accessToken"`
	RefreshToken string    `json:"-" bson:"refreshToken"`
	TokenExpiry  time.Time `json:"-" bson:"tokenExpiry"`
	// Billing — Plan is "free" (default/empty) or "pro".
	Plan                 string    `json:"plan" bson:"plan,omitempty"`
	StripeCustomerID     string    `json:"-" bson:"stripeCustomerId,omitempty"`
	StripeSubscriptionID string    `json:"-" bson:"stripeSubscriptionId,omitempty"`
	PlanUpdatedAt        time.Time `json:"-" bson:"planUpdatedAt,omitempty"`
	// AutoApplyRules, when true, runs the user's deterministic sorting rules
	// automatically over every freshly synced inbox — no extra click, no AI, no
	// quota. Off by default so syncing never mutates Gmail unexpectedly.
	AutoApplyRules bool `json:"autoApplyRules" bson:"autoApplyRules,omitempty"`
	// Daily digest — when DigestEnabled is true, a background scheduler emails a
	// recap of the last 7 days once a day at DigestHourUTC (0–23, UTC).
	// DigestLastSentAt stamps the last attempt so we send at most once per day.
	DigestEnabled    bool      `json:"digestEnabled" bson:"digestEnabled,omitempty"`
	DigestHourUTC    int       `json:"digestHourUTC" bson:"digestHourUTC,omitempty"`
	DigestLastSentAt time.Time `json:"-" bson:"digestLastSentAt,omitempty"`
	CreatedAt        time.Time `json:"createdAt" bson:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt" bson:"updatedAt"`
}

// UserSettings is the user-tunable subset of the account, exposed via
// GET/PUT /api/account/settings.
type UserSettings struct {
	AutoApplyRules bool `json:"autoApplyRules"`
	DigestEnabled  bool `json:"digestEnabled"`
	DigestHourUTC  int  `json:"digestHourUTC"`
}

type Email struct {
	ID           string    `json:"id" bson:"_id,omitempty"`
	MessageID    string    `json:"messageId" bson:"messageId"`
	UserID       string    `json:"userId" bson:"userId"`
	ThreadID     string    `json:"threadId" bson:"threadId"`
	From         string    `json:"from" bson:"from"`
	To           []string  `json:"to" bson:"to"`
	Subject      string    `json:"subject" bson:"subject"`
	Body         string    `json:"body" bson:"body"`
	Snippet      string    `json:"snippet" bson:"snippet"`
	LabelIDs     []string  `json:"labelIds" bson:"labelIds"`
	ReceivedDate time.Time `json:"receivedDate" bson:"receivedDate"`
	IsRead       bool      `json:"isRead" bson:"isRead"`
	// Unsubscribe affordances parsed from RFC 2369 / RFC 8058 headers.
	UnsubURL      string    `json:"unsubUrl,omitempty" bson:"unsubUrl,omitempty"`
	UnsubMailto   string    `json:"unsubMailto,omitempty" bson:"unsubMailto,omitempty"`
	UnsubOneClick bool      `json:"unsubOneClick,omitempty" bson:"unsubOneClick,omitempty"`
	CreatedAt     time.Time `json:"createdAt" bson:"createdAt"`
}

type Label struct {
	ID        string    `json:"id" bson:"_id,omitempty"`
	UserID    string    `json:"userId" bson:"userId"`
	GmailID   string    `json:"gmailId" bson:"gmailId"`
	Name      string    `json:"name" bson:"name"`
	Color     string    `json:"color" bson:"color"`
	CreatedAt time.Time `json:"createdAt" bson:"createdAt"`
}

type AuthResponse struct {
	AuthURL string `json:"authUrl"`
}

type TokenResponse struct {
	AccessToken string `json:"accessToken"`
	UserEmail   string `json:"userEmail"`
}

// GmailConfig stores the Gmail API credentials
type GmailConfig struct {
	ID                    string    `json:"id" bson:"_id,omitempty"`
	ClientID              string    `json:"clientId" bson:"clientId"`
	ClientSecretEncrypted string    `json:"-" bson:"clientSecretEncrypted"`
	RedirectURL           string    `json:"redirectUrl" bson:"redirectUrl"`
	IsConfigured          bool      `json:"isConfigured" bson:"isConfigured"`
	CreatedAt             time.Time `json:"createdAt" bson:"createdAt"`
	UpdatedAt             time.Time `json:"updatedAt" bson:"updatedAt"`
}

// GmailConfigStatus is returned by GET /api/config/status
type GmailConfigStatus struct {
	IsConfigured bool `json:"isConfigured"`
}

// GmailConfigMasked is returned by GET /api/config/gmail
type GmailConfigMasked struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	RedirectURL  string `json:"redirectUrl"`
	IsConfigured bool   `json:"isConfigured"`
}

// GmailConfigInput is the request body for POST /api/config/gmail
type GmailConfigInput struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	RedirectURL  string `json:"redirectUrl"`
}

// ============================================
// Deterministic Sorting Rules
// ============================================

// RuleCondition is a single predicate evaluated against an email field.
// Field is one of: from, subject, snippet, to, body.
// Operator is one of: contains, equals, startsWith, endsWith, regex.
type RuleCondition struct {
	Field    string `json:"field" bson:"field"`
	Operator string `json:"operator" bson:"operator"`
	Value    string `json:"value" bson:"value"`
}

// RuleAction is a single thing a rule does to a matching email. A rule can
// carry several actions (e.g. label "Newsletters" AND archive), applied in
// order — the canonical newsletter cleanup that a single action couldn't express.
type RuleAction struct {
	Type      string `json:"type" bson:"type"`                               // archive, trash, label, markRead, star
	LabelName string `json:"labelName,omitempty" bson:"labelName,omitempty"` // required when Type == "label"
}

// SortingRule is a deterministic, AI-free triage rule. When its conditions
// match an email, its action(s) are applied directly via Gmail — no model call,
// no quota consumed. Rules run before the AI so users can encode the obvious
// cases once and have them handled instantly and predictably.
//
// Actions carries the ordered list of actions a rule performs. The legacy
// Action/LabelName pair is kept for backward compatibility: rules created before
// multi-action (and the one-click sender rules) populate it instead, and it
// always mirrors the primary (first) action so older readers still work.
type SortingRule struct {
	ID           string          `json:"id" bson:"_id,omitempty"`
	UserID       string          `json:"userId" bson:"userId"`
	Name         string          `json:"name" bson:"name"`
	Enabled      bool            `json:"enabled" bson:"enabled"`
	MatchAll     bool            `json:"matchAll" bson:"matchAll"` // true = AND all conditions, false = OR any
	Conditions   []RuleCondition `json:"conditions" bson:"conditions"`
	Action       string          `json:"action" bson:"action"` // primary action (mirrors Actions[0])
	LabelName    string          `json:"labelName,omitempty" bson:"labelName,omitempty"`
	Actions      []RuleAction    `json:"actions,omitempty" bson:"actions,omitempty"` // full ordered action list
	Priority     int             `json:"priority" bson:"priority"`                   // lower runs first
	AppliedCount int             `json:"appliedCount" bson:"appliedCount"`
	CreatedAt    time.Time       `json:"createdAt" bson:"createdAt"`
	UpdatedAt    time.Time       `json:"updatedAt" bson:"updatedAt"`
}

// SortingRuleInput is the request body for creating/updating a rule. A client
// may send either the multi-action Actions list or the legacy Action/LabelName
// pair; the server normalizes one into the other.
type SortingRuleInput struct {
	Name       string          `json:"name"`
	Enabled    bool            `json:"enabled"`
	MatchAll   bool            `json:"matchAll"`
	Conditions []RuleCondition `json:"conditions"`
	Action     string          `json:"action"`
	LabelName  string          `json:"labelName"`
	Actions    []RuleAction    `json:"actions"`
	Priority   int             `json:"priority"`
}

// CreateSenderRuleRequest is the request body for POST /api/senders/rule. It
// turns the "learn once, apply forever" promise into a concrete deterministic
// rule: every future email whose From contains SenderEmail gets Action.
type CreateSenderRuleRequest struct {
	SenderEmail string `json:"senderEmail"`
	Action      string `json:"action"`    // archive, trash, label, markRead, star
	LabelName   string `json:"labelName"` // required when Action == "label"
}

// ============================================
// Protected senders (VIP safety net)
// ============================================

// ProtectedSender shields a sender from automated destructive triage. While a
// sender (a full address or a whole domain) is protected, no automated pass —
// AI suggestion, deterministic rule, sender auto-pilot or bulk action — may
// archive, trash or delete their emails. Non-destructive actions (label, star,
// mark read) are unaffected, and the user can still act manually.
type ProtectedSender struct {
	ID        string    `json:"id" bson:"_id,omitempty"`
	UserID    string    `json:"userId" bson:"userId"`
	Value     string    `json:"value" bson:"value"` // normalized address or domain
	Kind      string    `json:"kind" bson:"kind"`   // "address" | "domain"
	Note      string    `json:"note,omitempty" bson:"note,omitempty"`
	CreatedAt time.Time `json:"createdAt" bson:"createdAt"`
}

// ProtectedSenderInput is the request body for POST /api/protected.
type ProtectedSenderInput struct {
	Value string `json:"value"`
	Note  string `json:"note"`
}

// ============================================
// Snooze ("Reporter")
// ============================================

// Snooze records an email pulled out of the inbox until WakeAt, when it is
// brought back (marked unread). It is keyed by (userId, messageId) while
// scheduled so a message is only snoozed once at a time.
type Snooze struct {
	ID        string    `json:"id" bson:"_id,omitempty"`
	UserID    string    `json:"userId" bson:"userId"`
	MessageID string    `json:"messageId" bson:"messageId"`
	ThreadID  string    `json:"threadId,omitempty" bson:"threadId,omitempty"`
	From      string    `json:"from" bson:"from"`
	Subject   string    `json:"subject" bson:"subject"`
	WakeAt    time.Time `json:"wakeAt" bson:"wakeAt"`
	Status    string    `json:"status" bson:"status"` // "scheduled", "done", "cancelled"
	CreatedAt time.Time `json:"createdAt" bson:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt" bson:"updatedAt"`
}

// SnoozeRequest is the request body for POST /api/emails/snooze. Either Preset
// (resolved server-side) or an explicit WakeAt (RFC 3339) must be provided.
type SnoozeRequest struct {
	MessageID string    `json:"messageId"`
	Preset    string    `json:"preset"`
	WakeAt    time.Time `json:"wakeAt"`
}

// ============================================
// Action ledger (audit / activity)
// ============================================

// ActionLog is one append-only entry in the action ledger. Every mutating Gmail
// action Mailsorter performs is recorded here with its originating Source, which
// powers a truthful activity recap independent of what the client observed.
type ActionLog struct {
	ID        string    `json:"id" bson:"_id,omitempty"`
	UserID    string    `json:"userId" bson:"userId"`
	MessageID string    `json:"messageId" bson:"messageId"`
	Action    string    `json:"action" bson:"action"`
	Source    string    `json:"source" bson:"source"` // direct, rule, ai, ai-auto, bulk, snooze, unsubscribe
	CreatedAt time.Time `json:"createdAt" bson:"createdAt"`
}

// ============================================
// AI Sorting Models
// ============================================

// AISuggestion represents an AI-generated suggestion for an email
type AISuggestion struct {
	ID         string    `json:"id" bson:"_id,omitempty"`
	UserID     string    `json:"userId" bson:"userId"`
	EmailID    string    `json:"emailId" bson:"emailId"`       // Gmail message ID
	Action     string    `json:"action" bson:"action"`         // "archive", "delete", "label", "keep"
	LabelName  string    `json:"labelName" bson:"labelName"`   // Suggested label (if action = "label")
	LabelID    string    `json:"labelId" bson:"labelId"`       // Gmail label ID (after creation/matching)
	Confidence float64   `json:"confidence" bson:"confidence"` // 0.0 to 1.0
	Reasoning  string    `json:"reasoning" bson:"reasoning"`   // AI explanation
	Status     string    `json:"status" bson:"status"`         // "pending", "applied", "rejected"
	CreatedAt  time.Time `json:"createdAt" bson:"createdAt"`
	AppliedAt  time.Time `json:"appliedAt,omitempty" bson:"appliedAt,omitempty"`
}

// SenderPreference stores learned preferences for a specific sender
type SenderPreference struct {
	ID            string    `json:"id" bson:"_id,omitempty"`
	UserID        string    `json:"userId" bson:"userId"`
	SenderEmail   string    `json:"senderEmail" bson:"senderEmail"`     // Full email or domain
	SenderDomain  string    `json:"senderDomain" bson:"senderDomain"`   // Extracted domain
	SenderName    string    `json:"senderName" bson:"senderName"`       // Display name
	AutoApply     bool      `json:"autoApply" bson:"autoApply"`         // Auto-apply suggestions?
	DefaultAction string    `json:"defaultAction" bson:"defaultAction"` // Default action for this sender
	DefaultLabel  string    `json:"defaultLabel" bson:"defaultLabel"`   // Default label name
	EmailCount    int       `json:"emailCount" bson:"emailCount"`       // Number of emails from this sender
	CreatedAt     time.Time `json:"createdAt" bson:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt" bson:"updatedAt"`
}

// SmartLabel represents an AI-managed label category
type SmartLabel struct {
	ID           string    `json:"id" bson:"_id,omitempty"`
	UserID       string    `json:"userId" bson:"userId"`
	Name         string    `json:"name" bson:"name"`                 // Label name (e.g., "E-commerce")
	GmailLabelID string    `json:"gmailLabelId" bson:"gmailLabelId"` // Corresponding Gmail label ID
	Description  string    `json:"description" bson:"description"`   // What this label represents
	Keywords     []string  `json:"keywords" bson:"keywords"`         // Associated keywords for consistency
	EmailCount   int       `json:"emailCount" bson:"emailCount"`     // Number of emails with this label
	CreatedAt    time.Time `json:"createdAt" bson:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt" bson:"updatedAt"`
}

// AnalysisJob tracks an asynchronous batch-analysis run.
type AnalysisJob struct {
	ID                 string    `json:"id" bson:"_id,omitempty"`
	UserID             string    `json:"userId" bson:"userId"`
	Status             string    `json:"status" bson:"status"` // queued, running, done, error
	Total              int       `json:"total" bson:"total"`
	Processed          int       `json:"processed" bson:"processed"`
	AutoApplied        int       `json:"autoApplied" bson:"autoApplied"`
	SuggestionsCreated int       `json:"suggestionsCreated" bson:"suggestionsCreated"`
	CachedHits         int       `json:"cachedHits" bson:"cachedHits"`
	Error              string    `json:"error,omitempty" bson:"error,omitempty"`
	EmailIDs           []string  `json:"-" bson:"emailIds"`
	CreatedAt          time.Time `json:"createdAt" bson:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt" bson:"updatedAt"`
}

// AnalysisCacheEntry memoizes an AI verdict for a (from, subject) fingerprint
// so identical emails are never analyzed twice. Content-based, not user-scoped.
type AnalysisCacheEntry struct {
	ID         string    `bson:"_id,omitempty"`
	Key        string    `bson:"key"`
	Action     string    `bson:"action"`
	LabelName  string    `bson:"labelName"`
	Confidence float64   `bson:"confidence"`
	Reasoning  string    `bson:"reasoning"`
	CreatedAt  time.Time `bson:"createdAt"`
}

// ============================================
// AI API Request/Response Types
// ============================================

// AnalyzeEmailsRequest is the request body for POST /api/ai/analyze
type AnalyzeEmailsRequest struct {
	EmailIDs []string `json:"emailIds"` // Gmail message IDs to analyze
}

// EmailActionRequest is the request body for POST /api/emails/action
type EmailActionRequest struct {
	MessageID string `json:"messageId"`
	Action    string `json:"action"` // "archive", "delete", "read", "unread"
}

// AnalyzeSenderRequest is the request body for POST /api/ai/analyze-sender
type AnalyzeSenderRequest struct {
	SenderEmail string `json:"senderEmail"`
}

// ApplySuggestionRequest is the request body for POST /api/ai/apply
type ApplySuggestionRequest struct {
	SuggestionID string `json:"suggestionId"`
}

// ApplyBatchRequest is the request body for POST /api/ai/apply-batch
type ApplyBatchRequest struct {
	SuggestionIDs []string `json:"suggestionIds"`
}

// ApplyBulkRequest is the request body for POST /api/ai/apply-bulk
type ApplyBulkRequest struct {
	SenderEmail string `json:"senderEmail"`
	Action      string `json:"action"`    // Action to apply
	LabelName   string `json:"labelName"` // Label to apply (if action = "label")
}

// UpdateSenderPreferenceRequest is the request body for PUT /api/senders/{id}/preferences
type UpdateSenderPreferenceRequest struct {
	AutoApply     bool   `json:"autoApply"`
	DefaultAction string `json:"defaultAction"`
	DefaultLabel  string `json:"defaultLabel"`
}

// Unsubscribe records a completed or assisted unsubscribe from a mailing-list
// sender, keyed by (userId, senderEmail) so it is idempotent.
type Unsubscribe struct {
	ID          string    `json:"id" bson:"_id,omitempty"`
	UserID      string    `json:"userId" bson:"userId"`
	SenderEmail string    `json:"senderEmail" bson:"senderEmail"`
	SenderName  string    `json:"senderName" bson:"senderName"`
	Method      string    `json:"method" bson:"method"` // "one-click", "browser", "mailto"
	Status      string    `json:"status" bson:"status"` // "done", "opened"
	CreatedAt   time.Time `json:"createdAt" bson:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt" bson:"updatedAt"`
}

// UnsubscribeRequest is the request body for POST /api/unsubscribe
type UnsubscribeRequest struct {
	MessageID   string `json:"messageId"`
	AlsoArchive bool   `json:"alsoArchive"`
}

// Subscription is an aggregated mailing-list sender that advertises an
// unsubscribe link, returned by GET /api/subscriptions.
type Subscription struct {
	SenderEmail     string    `json:"senderEmail"`
	SenderName      string    `json:"senderName"`
	EmailCount      int       `json:"emailCount"`
	LastReceived    time.Time `json:"lastReceived"`
	SampleMessageID string    `json:"sampleMessageId"`
	OneClick        bool      `json:"oneClick"`
	Unsubscribed    bool      `json:"unsubscribed"`
}

// SenderStats represents aggregated info about a sender
type SenderStats struct {
	SenderEmail  string            `json:"senderEmail"`
	SenderDomain string            `json:"senderDomain"`
	SenderName   string            `json:"senderName"`
	EmailCount   int               `json:"emailCount"`
	Preference   *SenderPreference `json:"preference,omitempty"`
}
