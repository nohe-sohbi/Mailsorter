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
	CreatedAt    time.Time `json:"createdAt" bson:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt" bson:"updatedAt"`
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
	CreatedAt    time.Time `json:"createdAt" bson:"createdAt"`
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
	ID          string    `json:"id" bson:"_id,omitempty"`
	UserID      string    `json:"userId" bson:"userId"`
	Name        string    `json:"name" bson:"name"`               // Label name (e.g., "E-commerce")
	GmailLabelID string   `json:"gmailLabelId" bson:"gmailLabelId"` // Corresponding Gmail label ID
	Description string    `json:"description" bson:"description"` // What this label represents
	Keywords    []string  `json:"keywords" bson:"keywords"`       // Associated keywords for consistency
	EmailCount  int       `json:"emailCount" bson:"emailCount"`   // Number of emails with this label
	CreatedAt   time.Time `json:"createdAt" bson:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt" bson:"updatedAt"`
}

// ============================================
// AI API Request/Response Types
// ============================================

// AnalyzeEmailsRequest is the request body for POST /api/ai/analyze
type AnalyzeEmailsRequest struct {
	EmailIDs []string `json:"emailIds"` // Gmail message IDs to analyze
}

// AnalyzeSenderRequest is the request body for POST /api/ai/analyze-sender
type AnalyzeSenderRequest struct {
	SenderEmail string `json:"senderEmail"`
}

// ApplySuggestionRequest is the request body for POST /api/ai/apply
type ApplySuggestionRequest struct {
	SuggestionID string `json:"suggestionId"`
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

// SenderStats represents aggregated info about a sender
type SenderStats struct {
	SenderEmail  string            `json:"senderEmail"`
	SenderDomain string            `json:"senderDomain"`
	SenderName   string            `json:"senderName"`
	EmailCount   int               `json:"emailCount"`
	Preference   *SenderPreference `json:"preference,omitempty"`
}
