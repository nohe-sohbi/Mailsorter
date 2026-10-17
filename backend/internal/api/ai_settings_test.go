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
		{"https://openrouter.ai/api/v1", true},
		{"http://api.openai.com/v1", false}, // HTTP not allowed by egress policy
		{"http://localhost:11434", false},  // HTTP and loopback blocked
		{"https://localhost:11434", false}, // Loopback blocked
		{"https://127.0.0.1:11434", false}, // Loopback IP blocked
		{"https://169.254.169.254/latest", false}, // Cloud metadata service blocked
		{"https://10.0.0.1/v1", false},    // Private RFC 1918 IP blocked
		{"https://192.168.1.1/v1", false}, // Private RFC 1918 IP blocked
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
	req := httptest.NewRequest("POST", "/api/ai/settings/test", strings.NewReader(`{"provider":"unknown-provider-payload-123"}`))
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
	if errMsg != "Unknown provider" {
		t.Errorf("expected exact 'Unknown provider' error message, got %q", errMsg)
	}
	if strings.Contains(errMsg, "unknown-provider-payload-123") {
		t.Errorf("unvalidated provider input reflected in error response: %q", errMsg)
	}
}

func TestAISettingsInputLengthBounds(t *testing.T) {
	h := newTestHandler(t)

	tests := []struct {
		name    string
		payload map[string]interface{}
		wantErr string
	}{
		{
			name: "oversized apiKey",
			payload: map[string]interface{}{
				"provider": "openai",
				"apiKey":   strings.Repeat("a", 1025),
			},
			wantErr: "Clé API trop longue",
		},
		{
			name: "oversized model",
			payload: map[string]interface{}{
				"provider": "openai",
				"model":    strings.Repeat("m", 257),
			},
			wantErr: "Nom de modèle trop long",
		},
		{
			name: "oversized baseUrl",
			payload: map[string]interface{}{
				"provider": "openai",
				"baseUrl":  "https://example.com/" + strings.Repeat("u", 2048),
			},
			wantErr: "URL de base trop longue",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.payload)

			// Test UpdateAISettings
			reqUpdate := httptest.NewRequest("POST", "/api/ai/settings", strings.NewReader(string(body)))
			reqUpdate.Header.Set("Content-Type", "application/json")
			reqUpdate.Header.Set("X-User-Email", "user@example.com")
			wUpdate := httptest.NewRecorder()

			h.UpdateAISettings(wUpdate, reqUpdate)

			if wUpdate.Code != http.StatusBadRequest {
				t.Fatalf("UpdateAISettings: expected status 400, got %d: %s", wUpdate.Code, wUpdate.Body.String())
			}
			if !strings.Contains(wUpdate.Body.String(), tt.wantErr) {
				t.Errorf("UpdateAISettings: expected error message to contain %q, got %s", tt.wantErr, wUpdate.Body.String())
			}

			// Test TestAISettings
			reqTest := httptest.NewRequest("POST", "/api/ai/settings/test", strings.NewReader(string(body)))
			reqTest.Header.Set("Content-Type", "application/json")
			reqTest.Header.Set("X-User-Email", "user@example.com")
			wTest := httptest.NewRecorder()

			h.TestAISettings(wTest, reqTest)

			if wTest.Code != http.StatusBadRequest {
				t.Fatalf("TestAISettings: expected status 400, got %d: %s", wTest.Code, wTest.Body.String())
			}
			if !strings.Contains(wTest.Body.String(), tt.wantErr) {
				t.Errorf("TestAISettings: expected error message to contain %q, got %s", tt.wantErr, wTest.Body.String())
			}
		})
	}
}
