//go:build acceptance

package api

// A fake AI provider for the acceptance scenarios: an OpenAI-compatible chat
// endpoint that answers every analysis with one verdict the scenario chose.
//
// It is plugged in exactly like a user's own provider (an "openai-compatible"
// entry in the registry), so the request goes through the real client, the
// real prompt and the real parsing. What it fakes is the model's opinion, and
// only that: which action Mailsorter then takes on that opinion is what the
// scenarios check.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"sync"

	"github.com/nohe-sohbi/mailsorter/backend/internal/ai"
)

// batchCount reads how many emails a batch prompt asks about. The single-email
// prompt does not contain this sentence, which is how the fake tells the two
// apart (one JSON object versus a JSON array).
var batchCount = regexp.MustCompile(`Analyse les (\d+) emails`)

type fakeAI struct {
	mu      sync.Mutex
	server  *httptest.Server
	verdict ai.EmailAnalysis
	calls   int
}

func newFakeAI() *fakeAI {
	f := &fakeAI{verdict: ai.EmailAnalysis{
		Action:     "archive",
		Confidence: 0.95,
		Reasoning:  "Newsletter promotionnelle",
	}}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

func (f *fakeAI) close() { f.server.Close() }

// willSay sets the verdict the fake model gives every email from now on.
func (f *fakeAI) willSay(action string, confidence float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.verdict.Action = action
	f.verdict.Confidence = confidence
}

func (f *fakeAI) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeAI) serve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Messages) == 0 {
		http.Error(w, `{"error":{"message":"bad request"}}`, http.StatusBadRequest)
		return
	}
	prompt := req.Messages[len(req.Messages)-1].Content

	f.mu.Lock()
	f.calls++
	verdict := f.verdict
	f.mu.Unlock()

	var content []byte
	if m := batchCount.FindStringSubmatch(prompt); m != nil {
		n, _ := strconv.Atoi(m[1])
		list := make([]ai.EmailAnalysis, n)
		for i := range list {
			list[i] = verdict
		}
		content, _ = json.Marshal(list)
	} else {
		content, _ = json.Marshal(verdict)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"choices": []map[string]interface{}{
			{"message": map[string]string{"role": "assistant", "content": string(content)}},
		},
	})
}
