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

func (m *mockAnalyzer) AnalyzeEmail(email models.Email) (*EmailAnalysis, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	return m.result, nil
}

func (m *mockAnalyzer) AnalyzeBatch(emails []models.Email) ([]EmailAnalysis, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	return m.batchRes, nil
}

func (m *mockAnalyzer) AnalyzeSender(senderEmail string, emails []models.Email) (*SenderAnalysis, error) {
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

	res, err := reg.AnalyzeEmail(models.Email{Subject: "Promo"})
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

	_, err := reg.AnalyzeEmail(models.Email{Subject: "Test"})
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
		Model:   "gpt-6-luna",
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

// A provider a user configured never gets a client that can reach the server's
// own network: a refused base URL builds nothing, and Ollama needs an address.
// The operator's providers are left alone, internal addresses included.
func TestNewAnalyzerGuardsUserSuppliedProviders(t *testing.T) {
	cases := []struct {
		name    string
		cfg     ProviderConfig
		wantErr bool
	}{
		{"user openai-compatible on a public https host", ProviderConfig{Name: "openai-compatible", APIKey: ***, BaseURL: "https://api.example.com/v1/chat/completions", UserSupplied: true}, false},
		{"user openai-compatible on loopback", ProviderConfig{Name: "openai-compatible", APIKey: ***, BaseURL: "https://127.0.0.1/v1", UserSupplied: true}, true},
		{"user openai-compatible over http", ProviderConfig{Name: "openai-compatible", APIKey: ***, BaseURL: "http://api.example.com/v1", UserSupplied: true}, true},
		{"user mistral pointed at metadata", ProviderConfig{Name: "mistral", APIKey: ***, BaseURL: "https://169.254.169.254/", UserSupplied: true}, true},
		{"user ollama without address", ProviderConfig{Name: "ollama", UserSupplied: true}, true},
		{"operator ollama without address", ProviderConfig{Name: "ollama"}, false},
		{"operator openai-compatible on the docker network", ProviderConfig{Name: "openai-compatible", APIKey: ***, BaseURL: "http://10.0.0.5:8000/v1"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newAnalyzer(tc.cfg)
			if (err != nil) != tc.wantErr {
				t.Errorf("newAnalyzer(%+v) error = %v, want error %v", tc.cfg, err, tc.wantErr)
			}
		})
	}
}

// AI_FALLBACK_PROVIDER works by priority: the registry must try providers in
// ascending Priority, whatever order the configs arrive in.
func TestNewRegistryOrdersByPriority(t *testing.T) {
	r := NewRegistry([]ProviderConfig{
		{Name: "ollama", Model: "m", BaseURL: "http://127.0.0.1:1", Priority: 30, Enabled: true},
		{Name: "openai", APIKey: ***, Priority: 10, Enabled: true},
		{Name: "ollama", Model: "fallback", BaseURL: "http://127.0.0.1:2", Priority: 5, Enabled: true},
		{Name: "mistral", APIKey: ***, Priority: 0, Enabled: true},
	})
	got := r.ProviderNames()
	want := []string{"mistral", "ollama", "openai", "ollama"}
	if len(got) != len(want) {
		t.Fatalf("ProviderNames() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ProviderNames() = %v, want %v", got, want)
		}
	}
}
	}
}
