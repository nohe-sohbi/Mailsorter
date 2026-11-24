package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
)

// OpenAIClient implements Analyzer for OpenAI and any OpenAI-compatible API
// (Groq, Together, Fireworks, vLLM, LM Studio, etc.). The only difference is
// the base URL.
type OpenAIClient struct {
	apiKey  string
	model   string
	baseURL string
	// official is true when baseURL is OpenAI's own API. Its current models
	// reason before they answer and want max_completion_tokens; the servers
	// that copy the format (vLLM, LM Studio, llama.cpp, Groq...) keep the
	// classic max_tokens.
	official   bool
	httpClient *http.Client
	maxRetries int
	baseDelay  time.Duration
	maxDelay   time.Duration
	sleep      func(time.Duration)
}

func NewOpenAIClient(apiKey, model, baseURL string) *OpenAIClient {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1/chat/completions"
	}
	return &OpenAIClient{
		apiKey:     apiKey,
		model:      model,
		baseURL:    baseURL,
		official:   isOpenAIHost(baseURL),
		httpClient: &http.Client{Timeout: 60 * time.Second},
		maxRetries: 2,
		baseDelay:  500 * time.Millisecond,
		maxDelay:   8 * time.Second,
		sleep:      time.Sleep,
	}
}

func (c *OpenAIClient) SetMaxRetries(n int) {
	if n < 0 {
		n = 0
	}
	c.maxRetries = n
}

func (c *OpenAIClient) ProviderName() string { return "openai" }

func (c *OpenAIClient) AnalyzeEmail(email models.Email) (*EmailAnalysis, error) {
	prompt := buildAnalyzeEmailPrompt(email)
	response, err := c.chat(prompt, 500)
	if err != nil {
		return nil, fmt.Errorf("openai API error: %w", err)
	}
	return parseEmailAnalysis(response)
}

func (c *OpenAIClient) AnalyzeBatch(emails []models.Email) ([]EmailAnalysis, error) {
	if len(emails) == 0 {
		return nil, nil
	}
	prompt := buildBatchPrompt(emails)
	maxTokens := 120*len(emails) + 200
	if maxTokens > 4000 {
		maxTokens = 4000
	}
	response, err := c.chat(prompt, maxTokens)
	if err != nil {
		return nil, fmt.Errorf("openai API error: %w", err)
	}
	return parseBatchAnalysis(response, len(emails))
}

func (c *OpenAIClient) AnalyzeSender(senderEmail string, emails []models.Email) (*SenderAnalysis, error) {
	prompt := buildSenderPrompt(senderEmail, emails)
	response, err := c.chat(prompt, 500)
	if err != nil {
		return nil, fmt.Errorf("openai API error: %w", err)
	}
	return parseSenderAnalysis(response)
}

// isOpenAIHost says whether baseURL is OpenAI's own API: api.openai.com, or
// one of its regional hosts (eu.api.openai.com, us.api.openai.com...), which
// data residency uses.
func isOpenAIHost(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "api.openai.com" || strings.HasSuffix(host, ".api.openai.com")
}

// openAIReasoningModels names OpenAI's reasoning models (gpt-5 onward, the o
// series). They spend reasoning tokens inside max_completion_tokens before
// the answer, default to a medium reasoning effort, and accept no
// temperature but the default.
var openAIReasoningModels = regexp.MustCompile(`^(?:gpt-[5-9]|o[1-9])`)

// reasoningRoom is what a reasoning model may spend before its answer, on
// top of the answer's own budget.
const reasoningRoom = 2048

// chat sends a message using the OpenAI chat completions format, with retry.
func (c *OpenAIClient) chat(prompt string, maxTokens int) (string, error) {
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type req struct {
		Model               string   `json:"model"`
		Messages            []msg    `json:"messages"`
		Temperature         *float64 `json:"temperature,omitempty"`
		MaxTokens           int      `json:"max_tokens,omitempty"`
		MaxCompletionTokens int      `json:"max_completion_tokens,omitempty"`
		ReasoningEffort     string   `json:"reasoning_effort,omitempty"`
	}
	low := 0.3 // consistent answers from a model that samples
	body := req{
		Model:    c.model,
		Messages: []msg{{Role: "user", Content: prompt}},
	}
	switch {
	case c.official && openAIReasoningModels.MatchString(c.model):
		// A short classification: the lowest effort keeps the reasoning
		// small, and the room above keeps the answer whole.
		body.ReasoningEffort = "low"
		body.MaxCompletionTokens = maxTokens + reasoningRoom
	case c.official:
		// max_tokens is deprecated on OpenAI's API in favour of this one.
		body.Temperature = &low
		body.MaxCompletionTokens = maxTokens
	default:
		body.Temperature = &low
		body.MaxTokens = maxTokens
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		content, retryable, err := c.doChat(jsonBody)
		if err == nil {
			return content, nil
		}
		lastErr = err
		if !retryable || attempt >= c.maxRetries {
			break
		}
		d := c.backoff(attempt)
		log.Printf("openai: attempt %d failed (%v), retrying in %v", attempt+1, err, d)
		c.sleep(d)
	}
	return "", lastErr
}

func (c *OpenAIClient) doChat(jsonBody []byte) (string, bool, error) {
	req, err := http.NewRequest("POST", c.baseURL, bytes.NewReader(jsonBody))
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", true, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", true, err
	}

	if resp.StatusCode == http.StatusOK {
		var result struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return "", false, err
		}
		if len(result.Choices) == 0 {
			return "", false, fmt.Errorf("no response from OpenAI")
		}
		choice := result.Choices[0]
		if choice.FinishReason == "length" {
			// The JSON answer is cut, or the reasoning used the whole budget
			// before it: nothing whole to parse.
			return "", false, fmt.Errorf("openai answer cut off at the token limit")
		}
		return choice.Message.Content, false, nil
	}

	retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
	return "", retryable, fmt.Errorf("openai API returned status %d: %s", resp.StatusCode, string(respBody))
}

func (c *OpenAIClient) backoff(attempt int) time.Duration {
	d := c.baseDelay << attempt
	if d > 0 {
		half := d / 2
		d = half + time.Duration(rand.Int63n(int64(half)+1))
	}
	if c.maxDelay > 0 && d > c.maxDelay {
		d = c.maxDelay
	}
	return d
}
