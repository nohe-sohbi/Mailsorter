package ai

import (
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
)

// ProviderConfig describes how to reach one AI backend.
type ProviderConfig struct {
	Name       string `json:"name" bson:"name"`           // "mistral", "openai", "anthropic", "ollama", "openai-compatible"
	APIKey     string `json:"apiKey" bson:"apiKey"`        // encrypted at rest when stored in DB
	Model      string `json:"model" bson:"model"`          // e.g. "gpt-4o-mini"
	BaseURL    string `json:"baseUrl" bson:"baseUrl"`      // custom endpoint (Ollama, Azure, etc.)
	MaxRetries int    `json:"maxRetries" bson:"maxRetries"` // extra attempts on transient failures
	Priority   int    `json:"priority" bson:"priority"`    // lower = tried first
	Enabled    bool   `json:"enabled" bson:"enabled"`
}

// Registry holds an ordered list of AI providers and handles automatic
// fallback: if the primary provider returns a transient error (429, 5xx,
// timeout), the next one in priority order is tried.
type Registry struct {
	mu        sync.RWMutex
	providers []providerEntry
}

type providerEntry struct {
	config   ProviderConfig
	analyzer Analyzer
}

// NewRegistry builds a registry from provider configs. Each config is turned
// into a concrete Analyzer via newAnalyzer. Configs with Enabled=false or
// missing credentials are silently skipped.
func NewRegistry(configs []ProviderConfig) *Registry {
	r := &Registry{}
	for _, cfg := range configs {
		if !cfg.Enabled {
			continue
		}
		a, err := newAnalyzer(cfg)
		if err != nil {
			log.Printf("ai/registry: skipping provider %q: %v", cfg.Name, err)
			continue
		}
		r.providers = append(r.providers, providerEntry{config: cfg, analyzer: a})
	}
	return r
}

// Available reports whether at least one provider is configured.
// WithUserOverride returns a new Registry that prioritizes the user's custom
// provider configuration (BYOK), falling back to the instance providers if
// transient errors occur.
func (r *Registry) WithUserOverride(userCfg ProviderConfig) *Registry {
	userAnalyzer, err := newAnalyzer(userCfg)
	if err != nil {
		log.Printf("ai/registry: cannot initialize user provider %q: %v", userCfg.Name, err)
		return r
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	userEntry := providerEntry{config: userCfg, analyzer: userAnalyzer}
	newProviders := []providerEntry{userEntry}

	for _, p := range r.providers {
		newProviders = append(newProviders, p)
	}

	return &Registry{
		providers: newProviders,
	}
}

func (r *Registry) Available() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.providers) > 0
}

// Primary returns the highest-priority analyzer, or nil.
func (r *Registry) Primary() Analyzer {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.providers) == 0 {
		return nil
	}
	return r.providers[0].analyzer
}

// ProviderNames returns the names of all active providers, in priority order.
func (r *Registry) ProviderNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, len(r.providers))
	for i, p := range r.providers {
		names[i] = p.config.Name
	}
	return names
}

// ─── Fallback-aware Analyzer methods ────────────────────────────────────────

// AnalyzeEmail tries each provider in priority order, falling back on transient
// errors. Permanent errors (auth, bad request) fail immediately.
func (r *Registry) AnalyzeEmail(email models.Email, existingLabels []string) (*EmailAnalysis, error) {
	r.mu.RLock()
	providers := r.providers
	r.mu.RUnlock()

	if len(providers) == 0 {
		return nil, fmt.Errorf("no AI provider configured")
	}

	var lastErr error
	for _, p := range providers {
		result, err := p.analyzer.AnalyzeEmail(email, existingLabels)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !isTransient(err) {
			return nil, err
		}
		log.Printf("ai/registry: provider %q failed (transient), trying next: %v", p.config.Name, err)
	}
	return nil, fmt.Errorf("all AI providers failed: %w", lastErr)
}

// AnalyzeBatch tries each provider in priority order with fallback.
func (r *Registry) AnalyzeBatch(emails []models.Email, existingLabels []string) ([]EmailAnalysis, error) {
	r.mu.RLock()
	providers := r.providers
	r.mu.RUnlock()

	if len(providers) == 0 {
		return nil, fmt.Errorf("no AI provider configured")
	}

	var lastErr error
	for _, p := range providers {
		result, err := p.analyzer.AnalyzeBatch(emails, existingLabels)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !isTransient(err) {
			return nil, err
		}
		log.Printf("ai/registry: provider %q batch failed (transient), trying next: %v", p.config.Name, err)
	}
	return nil, fmt.Errorf("all AI providers failed: %w", lastErr)
}

// AnalyzeSender tries each provider in priority order with fallback.
func (r *Registry) AnalyzeSender(senderEmail string, emails []models.Email, existingLabels []string) (*SenderAnalysis, error) {
	r.mu.RLock()
	providers := r.providers
	r.mu.RUnlock()

	if len(providers) == 0 {
		return nil, fmt.Errorf("no AI provider configured")
	}

	var lastErr error
	for _, p := range providers {
		result, err := p.analyzer.AnalyzeSender(senderEmail, emails, existingLabels)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !isTransient(err) {
			return nil, err
		}
		log.Printf("ai/registry: provider %q sender analysis failed (transient), trying next: %v", p.config.Name, err)
	}
	return nil, fmt.Errorf("all AI providers failed: %w", lastErr)
}

// ProviderName returns a comma-joined list of active provider names.
func (r *Registry) ProviderName() string {
	names := r.ProviderNames()
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ",")
}

// ─── Provider factory ───────────────────────────────────────────────────────

// newAnalyzer creates the right Analyzer for a ProviderConfig.
func newAnalyzer(cfg ProviderConfig) (Analyzer, error) {
	switch cfg.Name {
	case "mistral":
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("API key required")
		}
		c := NewMistralClient(cfg.APIKey, cfg.Model)
		if cfg.MaxRetries > 0 {
			c.SetMaxRetries(cfg.MaxRetries)
		}
		if cfg.BaseURL != "" {
			c.baseURL = cfg.BaseURL
		}
		return c, nil

	case "openai", "openai-compatible":
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("API key required")
		}
		baseURL := "https://api.openai.com/v1/chat/completions"
		if cfg.BaseURL != "" {
			baseURL = cfg.BaseURL
		}
		model := cfg.Model
		if model == "" {
			model = "gpt-4o-mini"
		}
		c := NewOpenAIClient(cfg.APIKey, model, baseURL)
		if cfg.MaxRetries > 0 {
			c.SetMaxRetries(cfg.MaxRetries)
		}
		return c, nil

	case "anthropic":
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("API key required")
		}
		model := cfg.Model
		if model == "" {
			model = "claude-sonnet-4-20250514"
		}
		c := NewAnthropicClient(cfg.APIKey, model)
		if cfg.MaxRetries > 0 {
			c.SetMaxRetries(cfg.MaxRetries)
		}
		return c, nil

	case "ollama":
		baseURL := cfg.BaseURL
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}
		model := cfg.Model
		if model == "" {
			model = "llama3"
		}
		c := NewOllamaClient(model, baseURL)
		return c, nil

	default:
		return nil, fmt.Errorf("unknown provider: %s", cfg.Name)
	}
}

// isTransient returns true for errors that suggest a different provider might
// succeed (rate limits, server errors, timeouts). It inspects the error string
// because the providers wrap HTTP status codes into fmt.Errorf messages.
func isTransient(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "status 429") ||
		strings.Contains(s, "status 5") ||
		strings.Contains(s, "rate_limited") ||
		strings.Contains(s, "Rate limit") ||
		strings.Contains(s, "timeout") ||
		strings.Contains(s, "connection refused") ||
		strings.Contains(s, "i/o timeout") ||
		strings.Contains(s, "TLS handshake timeout") ||
		strings.Contains(s, "no such host") ||
		strings.Contains(s, "server error")
}
