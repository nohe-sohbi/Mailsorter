package ai

import (
	"errors"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
)

// mockAnalyzer implements Analyzer for testing fallback behavior.
type mockAnalyzer struct {
	name      string
	calls     int
	err       error
	result    *EmailAnalysis
	batchRes  []EmailAnalysis
	senderRes *SenderAnalysis
}

func (m *mockAnalyzer) AnalyzeEmail(email models.Email, existingLabels []string) (*EmailAnalysis, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	return m.result, nil
}

func (m *mockAnalyzer) AnalyzeBatch(emails []models.Email, existingLabels []string) ([]EmailAnalysis, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	return m.batchRes, nil
}

func (m *mockAnalyzer) AnalyzeSender(senderEmail string, emails []models.Email, existingLabels []string) (*SenderAnalysis, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	return m.senderRes, nil
}

func (m *mockAnalyzer) ProviderName() string {
	return m.name
}

func TestRegistryFallbackOn429(t *testing.T) {
	primary := &mockAnalyzer{
		name: "mistral",
		err:  errors.New("mistral API returned status 429: rate_limited"),
	}
	fallback := &mockAnalyzer{
		name: "openai",
		result: &EmailAnalysis{
			Action:     "archive",
			Confidence: 0.95,
			Reasoning:  "Newsletter handled by fallback provider",
		},
	}

	reg := &Registry{
		providers: []providerEntry{
			{config: ProviderConfig{Name: "mistral", Priority: 0, Enabled: true}, analyzer: primary},
			{config: ProviderConfig{Name: "openai", Priority: 10, Enabled: true}, analyzer: fallback},
		},
	}

	res, err := reg.AnalyzeEmail(models.Email{Subject: "Promo"}, nil)
	if err != nil {
		t.Fatalf("expected fallback to succeed, got error: %v", err)
	}
	if res.Action != "archive" {
		t.Errorf("expected action 'archive', got %q", res.Action)
	}
	if primary.calls != 1 {
		t.Errorf("expected primary to be called once, got %d", primary.calls)
	}
	if fallback.calls != 1 {
		t.Errorf("expected fallback to be called once, got %d", fallback.calls)
	}
}

func TestRegistryPermanentErrorFailsImmediately(t *testing.T) {
	primary := &mockAnalyzer{
		name: "mistral",
		err:  errors.New("mistral API returned status 401: invalid api key"),
	}
	fallback := &mockAnalyzer{
		name: "openai",
		result: &EmailAnalysis{
			Action: "keep",
		},
	}

	reg := &Registry{
		providers: []providerEntry{
			{config: ProviderConfig{Name: "mistral", Priority: 0, Enabled: true}, analyzer: primary},
			{config: ProviderConfig{Name: "openai", Priority: 10, Enabled: true}, analyzer: fallback},
		},
	}

	_, err := reg.AnalyzeEmail(models.Email{Subject: "Test"}, nil)
	if err == nil {
		t.Fatal("expected error on 401, got nil")
	}
	if fallback.calls != 0 {
		t.Errorf("expected fallback NOT to be called on non-transient error, but was called %d times", fallback.calls)
	}
}

func TestRegistryWithUserOverride(t *testing.T) {
	instanceMistral := &mockAnalyzer{
		name: "mistral",
		result: &EmailAnalysis{
			Action:    "label",
			LabelName: "DefaultInstance",
		},
	}

	reg := &Registry{
		providers: []providerEntry{
			{config: ProviderConfig{Name: "mistral", Priority: 0, Enabled: true}, analyzer: instanceMistral},
		},
	}

	// Create user override with OpenAI config
	userCfg := ProviderConfig{
		Name:    "openai",
		APIKey:  "sk-test-user-key",
		Model:   "gpt-4o-mini",
		Enabled: true,
	}

	userReg := reg.WithUserOverride(userCfg)
	if len(userReg.providers) != 2 {
		t.Fatalf("expected 2 providers in userReg, got %d", len(userReg.providers))
	}
	if userReg.providers[0].config.Name != "openai" {
		t.Errorf("expected primary provider to be 'openai', got %q", userReg.providers[0].config.Name)
	}
	if userReg.providers[1].config.Name != "mistral" {
		t.Errorf("expected fallback provider to be 'mistral', got %q", userReg.providers[1].config.Name)
	}
}
