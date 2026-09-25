package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
)

const (
	mistralAPIURL = "https://api.mistral.ai/v1/chat/completions"
)

type MistralClient struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client

	// Resilience knobs. maxRetries is the number of EXTRA attempts after the
	// first (so total attempts = maxRetries+1). baseDelay seeds the exponential
	// backoff; maxDelay caps any single wait so a hostile Retry-After can't stall
	// a request past the server's write timeout. sleep is injectable for tests.
	maxRetries int
	baseDelay  time.Duration
	maxDelay   time.Duration
	sleep      func(time.Duration)
}

func normalizeMistralModel(m string) string {
	m = strings.TrimSpace(m)
	switch m {
	case "", "mistral-large-2411", "mistral-large-2407", "mistral-large-2402":
		return "mistral-small-latest"
	default:
		return m
	}
}

func NewMistralClient(apiKey, model string) *MistralClient {
	return &MistralClient{
		apiKey:  apiKey,
		model:   normalizeMistralModel(model),
		baseURL: mistralAPIURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		maxRetries: 2,
		baseDelay:  500 * time.Millisecond,
		maxDelay:   8 * time.Second,
		sleep:      time.Sleep,
	}
}

// SetMaxRetries configures how many ADDITIONAL attempts a transient failure gets
// after the first (total attempts = n+1). Negative values are clamped to 0.
func (c *MistralClient) SetMaxRetries(n int) {
	if n < 0 {
		n = 0
	}
	c.maxRetries = n
}

// ProviderName implements Analyzer.
func (c *MistralClient) ProviderName() string { return "mistral" }

// Mistral API request/response types
type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// AnalyzeEmail analyzes a single email and returns a suggested action.
// Implements Analyzer.
func (c *MistralClient) AnalyzeEmail(email models.Email, existingLabels []string) (*EmailAnalysis, error) {
	prompt := buildAnalyzeEmailPrompt(email, existingLabels)

	response, err := c.chat(prompt)
	if err != nil {
		return nil, fmt.Errorf("mistral API error: %w", err)
	}

	return parseEmailAnalysis(response)
}

// AnalyzeSender analyzes multiple emails from the same sender.
// Implements Analyzer.
func (c *MistralClient) AnalyzeSender(senderEmail string, emails []models.Email, existingLabels []string) (*SenderAnalysis, error) {
	prompt := buildSenderPrompt(senderEmail, emails, existingLabels)

	response, err := c.chat(prompt)
	if err != nil {
		return nil, fmt.Errorf("mistral API error: %w", err)
	}

	return parseSenderAnalysis(response)
}

// AnalyzeBatch analyzes several emails in a single API call and returns one
// analysis per email, in order. This collapses N requests into ⌈N/batch⌉,
// slashing both cost and latency. Returns an error if the model's response
// can't be aligned with the input, so the caller can fall back per-email.
// Implements Analyzer.
func (c *MistralClient) AnalyzeBatch(emails []models.Email, existingLabels []string) ([]EmailAnalysis, error) {
	if len(emails) == 0 {
		return nil, nil
	}

	prompt := buildBatchPrompt(emails, existingLabels)

	maxTokens := 120*len(emails) + 200
	if maxTokens > 4000 {
		maxTokens = 4000
	}

	response, err := c.chatTokens(prompt, maxTokens)
	if err != nil {
		return nil, fmt.Errorf("mistral API error: %w", err)
	}

	return parseBatchAnalysis(response, len(emails))
}

// chat sends a message to Mistral and returns the response (default token budget).
func (c *MistralClient) chat(prompt string) (string, error) {
	return c.chatTokens(prompt, 500)
}

// chatTokens sends a message to Mistral with an explicit max-tokens budget,
// retrying transient failures (HTTP 429, any 5xx, and network errors) with
// exponential backoff + jitter. Permanent failures (4xx other than 429, JSON
// errors) fail fast. The LLM is the flakiest dependency in the request path, so
// a single 429 no longer collapses a whole analysis batch down to "keep".
func (c *MistralClient) chatTokens(prompt string, maxTokens int) (string, error) {
	currentModel := c.model

	for {
		reqBody := chatRequest{
			Model: currentModel,
			Messages: []chatMessage{
				{Role: "user", Content: prompt},
			},
			Temperature: 0.3, // Low temperature for consistent responses
			MaxTokens:   maxTokens,
		}

		jsonBody, err := json.Marshal(reqBody)
		if err != nil {
			return "", err
		}

		var lastErr error
		fallbackNeeded := false
		for attempt := 0; ; attempt++ {
			content, retryable, retryAfter, err := c.doChat(jsonBody)
			if err == nil {
				return content, nil
			}
			lastErr = err

			// Check for model tier or invalid model error to trigger fallback
			errStr := err.Error()
			if strings.Contains(errStr, "tier_not_allowed") || strings.Contains(errStr, "invalid_model") {
				if currentModel != "mistral-small-latest" {
					log.Printf("Mistral model %s not available (%v), falling back to mistral-small-latest", currentModel, err)
					currentModel = "mistral-small-latest"
					c.model = "mistral-small-latest"
					fallbackNeeded = true
					break
				} else if currentModel != "open-mistral-nemo" {
					log.Printf("Mistral model %s not available (%v), falling back to open-mistral-nemo", currentModel, err)
					currentModel = "open-mistral-nemo"
					c.model = "open-mistral-nemo"
					fallbackNeeded = true
					break
				}
			}

			if !retryable || attempt >= c.maxRetries {
				break
			}
			c.sleep(c.backoff(attempt, retryAfter))
		}

		if fallbackNeeded {
			continue
		}

		return "", lastErr
	}
}

// doChat performs a single Mistral call. It reports whether the failure is worth
// retrying and any server-advised Retry-After delay.
func (c *MistralClient) doChat(jsonBody []byte) (content string, retryable bool, retryAfter time.Duration, err error) {
	req, err := http.NewRequest("POST", c.baseURL, bytes.NewReader(jsonBody))
	if err != nil {
		return "", false, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Transport-level errors (timeouts, resets) are transient.
		return "", true, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", true, 0, err
	}

	if resp.StatusCode == http.StatusOK {
		var chatResp chatResponse
		if err := json.Unmarshal(body, &chatResp); err != nil {
			return "", false, 0, err
		}
		if len(chatResp.Choices) == 0 {
			return "", false, 0, fmt.Errorf("no response from Mistral")
		}
		return chatResp.Choices[0].Message.Content, false, 0, nil
	}

	// Rate limits and server errors are transient; everything else is permanent.
	retryable = resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
	retryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
	return "", retryable, retryAfter, fmt.Errorf("mistral API returned status %d: %s", resp.StatusCode, string(body))
}

// backoff computes the wait before the next attempt: exponential in the attempt
// number with full jitter (random in [d/2, d]) to avoid synchronized retries,
// never shorter than the server's Retry-After and never longer than maxDelay.
func (c *MistralClient) backoff(attempt int, retryAfter time.Duration) time.Duration {
	d := c.baseDelay << attempt // baseDelay * 2^attempt
	if d > 0 {
		half := d / 2
		d = half + time.Duration(rand.Int63n(int64(half)+1))
	}
	if retryAfter > d {
		d = retryAfter
	}
	if c.maxDelay > 0 && d > c.maxDelay {
		d = c.maxDelay
	}
	return d
}

// parseRetryAfter interprets the delta-seconds form of a Retry-After header (the
// form Mistral and its CDN emit). Non-numeric or non-positive values yield 0.
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return 0
}

// Compile-time assertion: MistralClient satisfies Analyzer.
var _ Analyzer = (*MistralClient)(nil)
