package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// captureServer answers every request with reply and keeps the last request
// body, decoded.
func captureServer(t *testing.T, reply string) (*httptest.Server, *map[string]any) {
	t.Helper()
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got = map[string]any{}
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func newAnthropicTest(url, model string) *AnthropicClient {
	c := NewAnthropicClient("test-key", model)
	c.baseURL = url
	c.sleep = func(time.Duration) {}
	c.SetMaxRetries(0)
	return c
}

func newOpenAITest(url, model string, official bool) *OpenAIClient {
	c := NewOpenAIClient("test-key", model, url)
	c.official = official
	c.sleep = func(time.Duration) {}
	c.SetMaxRetries(0)
	return c
}

const anthropicAnswer = `{"content":[{"type":"thinking","thinking":"","signature":"x"},{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`

// Haiku 5.5 thinks by default and the thinking counts toward max_tokens: the
// request must lower the effort and leave room above the answer's budget.
func TestAnthropicThinkingModelGetsLowEffortAndRoom(t *testing.T) {
	for _, model := range []string{"claude-haiku-5-5", "claude-sonnet-5-5", "claude-opus-5-5", "claude-opus-4-6"} {
		srv, got := captureServer(t, anthropicAnswer)
		text, err := newAnthropicTest(srv.URL, model).chat("ping", 500)
		if err != nil || text != "ok" {
			t.Fatalf("%s: chat = %q, %v; want the text block after the thinking block", model, text, err)
		}
		oc, _ := (*got)["output_config"].(map[string]any)
		if oc["effort"] != "low" {
			t.Errorf("%s: output_config = %v, want effort low", model, (*got)["output_config"])
		}
		if mt, _ := (*got)["max_tokens"].(float64); int(mt) != 500+thinkingRoom {
			t.Errorf("%s: max_tokens = %v, want %d", model, (*got)["max_tokens"], 500+thinkingRoom)
		}
		if _, ok := (*got)["temperature"]; ok {
			t.Errorf("%s: sends a temperature, which these models refuse unless it is the default", model)
		}
	}
}

// Older models refuse output_config.effort with a 400: they get the plain
// request they always had.
func TestAnthropicOlderModelGetsPlainRequest(t *testing.T) {
	srv, got := captureServer(t, anthropicAnswer)
	if _, err := newAnthropicTest(srv.URL, "claude-haiku-4-5").chat("ping", 500); err != nil {
		t.Fatal(err)
	}
	if _, ok := (*got)["output_config"]; ok {
		t.Errorf("claude-haiku-4-5 gets output_config %v, which it refuses", (*got)["output_config"])
	}
	if mt, _ := (*got)["max_tokens"].(float64); int(mt) != 500 {
		t.Errorf("max_tokens = %v, want 500", (*got)["max_tokens"])
	}
}

func TestAnthropicStopReasonsAreErrors(t *testing.T) {
	for reason, want := range map[string]string{
		"max_tokens": "cut off",
		"refusal":    "declined",
	} {
		srv, _ := captureServer(t, `{"content":[{"type":"text","text":"[{\"act"}],"stop_reason":"`+reason+`"}`)
		_, err := newAnthropicTest(srv.URL, "claude-haiku-5-5").chat("ping", 500)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("stop_reason %s: err = %v, want one that says %q", reason, err, want)
		}
	}
}

const openAIAnswer = `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`

// OpenAI's current models reason first: max_tokens is deprecated there, a
// non-default temperature is refused, and the reasoning shares the budget.
func TestOpenAIReasoningModelRequest(t *testing.T) {
	srv, got := captureServer(t, openAIAnswer)
	if _, err := newOpenAITest(srv.URL, "gpt-6-luna", true).chat("ping", 500); err != nil {
		t.Fatal(err)
	}
	if _, ok := (*got)["max_tokens"]; ok {
		t.Errorf("sends max_tokens to OpenAI's API: %v", *got)
	}
	if mt, _ := (*got)["max_completion_tokens"].(float64); int(mt) != 500+reasoningRoom {
		t.Errorf("max_completion_tokens = %v, want %d", (*got)["max_completion_tokens"], 500+reasoningRoom)
	}
	if (*got)["reasoning_effort"] != "low" {
		t.Errorf("reasoning_effort = %v, want low", (*got)["reasoning_effort"])
	}
	if _, ok := (*got)["temperature"]; ok {
		t.Errorf("sends a temperature to a reasoning model: %v", (*got)["temperature"])
	}
}

// A server that copies the format (vLLM, LM Studio, llama.cpp...) keeps the
// classic request: max_tokens and a low temperature, no reasoning field.
func TestOpenAICompatibleServerRequest(t *testing.T) {
	srv, got := captureServer(t, openAIAnswer)
	if _, err := newOpenAITest(srv.URL, "gpt-6-luna", false).chat("ping", 500); err != nil {
		t.Fatal(err)
	}
	if mt, _ := (*got)["max_tokens"].(float64); int(mt) != 500 {
		t.Errorf("max_tokens = %v, want 500", (*got)["max_tokens"])
	}
	for _, k := range []string{"max_completion_tokens", "reasoning_effort"} {
		if _, ok := (*got)[k]; ok {
			t.Errorf("sends %s to a compatible server", k)
		}
	}
	if (*got)["temperature"] != 0.3 {
		t.Errorf("temperature = %v, want 0.3", (*got)["temperature"])
	}
}

func TestOpenAILengthIsAnError(t *testing.T) {
	srv, _ := captureServer(t, `{"choices":[{"message":{"content":""},"finish_reason":"length"}]}`)
	_, err := newOpenAITest(srv.URL, "gpt-6-luna", true).chat("ping", 500)
	if err == nil || !strings.Contains(err.Error(), "cut off") {
		t.Errorf("finish_reason length: err = %v, want one that says the answer was cut off", err)
	}
}

func TestOpenAIOfficialHost(t *testing.T) {
	if !NewOpenAIClient("k", "m", "").official {
		t.Error("the default base URL is OpenAI's API")
	}
	if NewOpenAIClient("k", "m", "http://localhost:1234/v1/chat/completions").official {
		t.Error("a local server is not OpenAI's API")
	}
}
