package ai

import (
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
)

// EmailAnalysis is the standardized result of analyzing a single email.
type EmailAnalysis struct {
	Action     string  `json:"action"`     // "archive", "delete", "label", "keep"
	LabelName  string  `json:"label_name"` // Suggested label (if action = "label")
	Confidence float64 `json:"confidence"` // 0.0 to 1.0
	Reasoning  string  `json:"reasoning"`  // Brief explanation
}

// SenderAnalysis is the standardized result of analyzing a sender.
type SenderAnalysis struct {
	SuggestedAction string  `json:"suggested_action"`
	SuggestedLabel  string  `json:"suggested_label"`
	Confidence      float64 `json:"confidence"`
	Reasoning       string  `json:"reasoning"`
	SenderType      string  `json:"sender_type"` // "commercial", "personal", "work", "newsletter", "transactional"
}

// Analyzer is the contract every AI provider must satisfy. It decouples the
// rest of the application from any single LLM vendor, enabling multi-provider
// fallback and per-user BYOK configuration.
type Analyzer interface {
	// AnalyzeEmail analyzes a single email and returns a suggested action.
	AnalyzeEmail(email models.Email, existingLabels []string) (*EmailAnalysis, error)

	// AnalyzeBatch analyzes several emails in a single call (or multiple calls
	// internally) and returns one analysis per email, in input order.
	AnalyzeBatch(emails []models.Email, existingLabels []string) ([]EmailAnalysis, error)

	// AnalyzeSender analyzes a sender based on a sample of their emails.
	AnalyzeSender(senderEmail string, emails []models.Email, existingLabels []string) (*SenderAnalysis, error)

	// ProviderName returns a human/machine-readable identifier for the
	// provider, e.g. "mistral", "openai", "anthropic", "ollama".
	ProviderName() string
}

// ProviderInfo describes an AI provider for the settings UI.
type ProviderInfo struct {
	Name           string   `json:"name"`           // "mistral", "openai", etc.
	DisplayName    string   `json:"displayName"`    // "Mistral AI"
	RequiresAPIKey bool     `json:"requiresApiKey"` // false for Ollama
	RequiresURL    bool     `json:"requiresUrl"`    // true for Ollama / custom
	DefaultModel   string   `json:"defaultModel"`   // "mistral-small-latest"
	Models         []string `json:"models"`         // suggested model list
}

// KnownProviders is the catalog shown in the frontend settings.
var KnownProviders = []ProviderInfo{
	{
		Name:           "mistral",
		DisplayName:    "Mistral AI",
		RequiresAPIKey: true,
		DefaultModel:   "mistral-small-latest",
		Models:         []string{"mistral-small-latest", "mistral-medium-latest", "mistral-large-latest", "open-mistral-nemo"},
	},
	{
		Name:           "openai",
		DisplayName:    "OpenAI",
		RequiresAPIKey: true,
		DefaultModel:   "gpt-4o-mini",
		Models:         []string{"gpt-4o-mini", "gpt-4o", "gpt-4.1-mini", "gpt-4.1-nano"},
	},
	{
		Name:           "anthropic",
		DisplayName:    "Anthropic",
		RequiresAPIKey: true,
		DefaultModel:   "claude-sonnet-4-20250514",
		Models:         []string{"claude-sonnet-4-20250514", "claude-haiku-4-20250514"},
	},
	{
		Name:           "ollama",
		DisplayName:    "Ollama (local)",
		RequiresAPIKey: false,
		RequiresURL:    true,
		DefaultModel:   "llama3",
		Models:         []string{"llama3", "llama3.1", "mistral", "gemma2", "phi3"},
	},
	{
		Name:           "openai-compatible",
		DisplayName:    "OpenAI-compatible (Groq, Together, vLLM...)",
		RequiresAPIKey: true,
		RequiresURL:    true,
		DefaultModel:   "",
		Models:         []string{},
	},
}
