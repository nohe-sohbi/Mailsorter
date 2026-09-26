package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
)

// AnthropicClient implements Analyzer for the Anthropic Messages API.
type AnthropicClient struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
	maxRetries int
	baseDelay  time.Duration
	maxDelay   time.Duration
	sleep      func(time.Duration)
}

func NewAnthropicClient(apiKey, model string) *AnthropicClient {
	return &AnthropicClient{
		apiKey:     apiKey,
		model:      model,
		baseURL:    "https://api.anthropic.com/v1/messages",
		httpClient: &http.Client{Timeout: 60 * time.Second},
		maxRetries: 2,
		baseDelay:  500 * time.Millisecond,
		maxDelay:   8 * time.Second,
		sleep:      time.Sleep,
	}
}

func (c *AnthropicClient) SetMaxRetries(n int) {
	if n < 0 {
		n = 0
	}
	c.maxRetries = n
}

func (c *AnthropicClient) ProviderName() string { return "anthropic" }

func (c *AnthropicClient) AnalyzeEmail(email models.Email, existingLabels []string) (*EmailAnalysis, error) {
	prompt := buildAnalyzeEmailPrompt(email, existingLabels)
	response, err := c.chat(prompt, 500)
	if err != nil {
		return nil, fmt.Errorf("anthropic API error: %w", err)
	}
	return parseEmailAnalysis(response)
}

func (c *AnthropicClient) AnalyzeBatch(emails []models.Email, existingLabels []string) ([]EmailAnalysis, error) {
	if len(emails) == 0 {
		return nil, nil
	}
	prompt := buildBatchPrompt(emails, existingLabels)
	maxTokens := 120*len(emails) + 200
	if maxTokens > 4000 {
		maxTokens = 4000
	}
	response, err := c.chat(prompt, maxTokens)
	if err != nil {
		return nil, fmt.Errorf("anthropic API error: %w", err)
	}
	return parseBatchAnalysis(response, len(emails))
}

func (c *AnthropicClient) AnalyzeSender(senderEmail string, emails []models.Email, existingLabels []string) (*SenderAnalysis, error) {
	prompt := buildSenderPrompt(senderEmail, emails, existingLabels)
	response, err := c.chat(prompt, 500)
	if err != nil {
		return nil, fmt.Errorf("anthropic API error: %w", err)
	}
	return parseSenderAnalysis(response)
}

func (c *AnthropicClient) chat(prompt string, maxTokens int) (string, error) {
	type content struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type req struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
		Messages  []msg  `json:"messages"`
	}
	body := req{
		Model:     c.model,
		MaxTokens: maxTokens,
		Messages:  []msg{{Role: "user", Content: prompt}},
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		result, retryable, err := c.doChat(jsonBody)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !retryable || attempt >= c.maxRetries {
			break
		}
		d := c.backoff(attempt)
		log.Printf("anthropic: attempt %d failed (%v), retrying in %v", attempt+1, err, d)
		c.sleep(d)
	}
	return "", lastErr
}

func (c *AnthropicClient) doChat(jsonBody []byte) (string, bool, error) {
	req, err := http.NewRequest("POST", c.baseURL, bytes.NewReader(jsonBody))
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

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
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return "", false, err
		}
		for _, c := range result.Content {
			if c.Type == "text" {
				return c.Text, false, nil
			}
		}
		return "", false, fmt.Errorf("no text content in Anthropic response")
	}

	retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
	return "", retryable, fmt.Errorf("anthropic API returned status %d: %s", resp.StatusCode, string(respBody))
}

func (c *AnthropicClient) backoff(attempt int) time.Duration {
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

// Ensure the import is used (strings is used in shared prompts).
var _ = strings.TrimSpace
