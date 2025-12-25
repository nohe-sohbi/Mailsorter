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

type SortingRule struct {
	ID          string    `json:"id" bson:"_id,omitempty"`
	UserID      string    `json:"userId" bson:"userId"`
	Name        string    `json:"name" bson:"name"`
	Description string    `json:"description" bson:"description"`
	Conditions  []Condition `json:"conditions" bson:"conditions"`
	Actions     []Action    `json:"actions" bson:"actions"`
	Priority    int       `json:"priority" bson:"priority"`
	Enabled     bool      `json:"enabled" bson:"enabled"`
	CreatedAt   time.Time `json:"createdAt" bson:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt" bson:"updatedAt"`
}

type Condition struct {
	Field    string `json:"field" bson:"field"` // from, to, subject, body
	Operator string `json:"operator" bson:"operator"` // contains, equals, startsWith, endsWith
	Value    string `json:"value" bson:"value"`
}

type Action struct {
	Type  string `json:"type" bson:"type"` // addLabel, removeLabel, markAsRead, archive
	Value string `json:"value" bson:"value"`
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
