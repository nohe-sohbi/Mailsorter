package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
)

// The request turns thinking off explicitly: unset, a thinking model reasons
// before every batch. The answer is read from message.content, the reasoning
// trace (message.thinking) is ignored.
func TestOllamaTurnsThinkingOffAndReadsTheAnswer(t *testing.T) {
	var got map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path = %q, want /api/chat", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("request body is not JSON: %v", err)
		}
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","thinking":"a newsletter { maybe [ archive",` +
			`"content":"{\"action\":\"archive\",\"label_name\":\"\",\"confidence\":0.9,\"reasoning\":\"newsletter\"}"}}`))
	}))
	defer srv.Close()

	c := NewOllamaClient("qwen3.5:9b", srv.URL)
	res, err := c.AnalyzeEmail(models.Email{ID: "1", From: "news@example.org", Subject: "Weekly", Snippet: "..."}, nil)
	if err != nil {
		t.Fatalf("AnalyzeEmail: %v", err)
	}
	if res.Action != "archive" || res.Confidence != 0.9 {
		t.Errorf("analysis = %+v, want archive at 0.9", res)
	}
	think, present := got["think"]
	if !present || think != false {
		t.Errorf(`request "think" = %v (present %v), want false`, think, present)
	}
	if got["stream"] != false {
		t.Errorf(`request "stream" = %v, want false`, got["stream"])
	}
	if got["model"] != "qwen3.5:9b" {
		t.Errorf(`request "model" = %v, want qwen3.5:9b`, got["model"])
	}
}

// A server that answers too slowly is reported as a timeout, not as
// unreachable, and counts as transient so a fallback provider can take over.
func TestOllamaSlowServerIsATimeoutNotUnreachable(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	c := NewOllamaClient("qwen3.8-27b", srv.URL)
	c.httpClient.Timeout = 50 * time.Millisecond
	_, err := c.AnalyzeEmail(models.Email{ID: "1", Subject: "x"}, nil)
	if err == nil {
		t.Fatal("want an error from a server that never answers")
	}
	msg := err.Error()
	if !strings.Contains(msg, "ollama timeout") || strings.Contains(msg, "unreachable") {
		t.Errorf("error = %q, want a timeout, not unreachable", msg)
	}
	if !strings.Contains(msg, "qwen3.8-27b") {
		t.Errorf("error = %q, want the model named", msg)
	}
	if !isTransient(err) {
		t.Errorf("isTransient(%q) = false, want true", msg)
	}
}

// Nothing listening is still reported as unreachable, with the address.
func TestOllamaNothingListeningIsUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	_, err := NewOllamaClient("m", url).AnalyzeEmail(models.Email{ID: "1"}, nil)
	if err == nil || !strings.Contains(err.Error(), "unreachable at "+url) {
		t.Errorf("error = %v, want unreachable at %s", err, url)
	}
}
