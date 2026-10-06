package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/ai"
)

func TestGetAIProvidersEndpoint(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest("GET", "/api/ai/providers", nil)
	w := httptest.NewRecorder()

	h.GetAIProviders(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var providers []ai.ProviderInfo
	if err := json.Unmarshal(w.Body.Bytes(), &providers); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(providers) < 4 {
		t.Fatalf("expected at least 4 providers, got %d", len(providers))
	}

	foundMistral := false
	foundOpenAI := false
	foundAnthropic := false
	foundOllama := false

	for _, p := range providers {
		switch p.Name {
		case "mistral":
			foundMistral = true
		case "openai":
			foundOpenAI = true
		case "anthropic":
			foundAnthropic = true
		case "ollama":
			foundOllama = true
		}
	}

	if !foundMistral || !foundOpenAI || !foundAnthropic || !foundOllama {
		t.Errorf("missing providers: mistral=%v, openai=%v, anthropic=%v, ollama=%v",
			foundMistral, foundOpenAI, foundAnthropic, foundOllama)
	}
}

func TestResolveAnalyzerFallbackToInstance(t *testing.T) {
	h := newTestHandler(t)
	// h has empty aiRegistry
	reg := h.resolveAnalyzer(context.Background(), "")
	if reg == nil {
		t.Fatal("expected non-nil registry")
	}
}

func TestIsValidBaseURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"", true},
		{"https://api.openai.com/v1", true},
		{"http://localhost:11434", true},
		{"http://127.0.0.1:11434", true},
		{"file:///etc/passwd", false},
		{"ftp://example.com", false},
		{"javascript:alert(1)", false},
		{"not-a-url", false},
		{"http://", false},
	}

	for _, tt := range tests {
		if got := isValidBaseURL(tt.url); got != tt.want {
			t.Errorf("isValidBaseURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestTestAISettingsUnknownProvider(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest("POST", "/api/ai/settings/test", strings.NewReader(`{"provider":"unknown-provider"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Email", "user@example.com")
	w := httptest.NewRecorder()

	h.TestAISettings(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", w.Code, w.Body.String())
	}

	var res map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	errMsg, _ := res["error"].(string)
	if !strings.Contains(errMsg, "Unknown provider") {
		t.Errorf("expected 'Unknown provider' in error message, got %q", errMsg)
	}
}
