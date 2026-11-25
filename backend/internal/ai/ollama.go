package ai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
)

// OllamaClient implements Analyzer for a local Ollama instance. No API key is
// required; the server must be reachable on the configured URL.
type OllamaClient struct {
	model      string
	baseURL    string // e.g. "http://localhost:11434"
	httpClient *http.Client
}

func NewOllamaClient(model, baseURL string) *OllamaClient {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	// Strip trailing slash for consistency.
	baseURL = strings.TrimRight(baseURL, "/")
	return &OllamaClient{
		model:      model,
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 120 * time.Second}, // local models can be slow
	}
}

func (c *OllamaClient) ProviderName() string { return "ollama" }

func (c *OllamaClient) AnalyzeEmail(email models.Email) (*EmailAnalysis, error) {
	prompt := buildAnalyzeEmailPrompt(email)
	response, err := c.chat(prompt)
	if err != nil {
		return nil, fmt.Errorf("ollama error: %w", err)
	}
	return parseEmailAnalysis(response)
}

func (c *OllamaClient) AnalyzeBatch(emails []models.Email) ([]EmailAnalysis, error) {
	if len(emails) == 0 {
		return nil, nil
	}
	prompt := buildBatchPrompt(emails)
	response, err := c.chat(prompt)
	if err != nil {
		return nil, fmt.Errorf("ollama error: %w", err)
	}
	return parseBatchAnalysis(response, len(emails))
}

func (c *OllamaClient) AnalyzeSender(senderEmail string, emails []models.Email) (*SenderAnalysis, error) {
	prompt := buildSenderPrompt(senderEmail, emails)
	response, err := c.chat(prompt)
	if err != nil {
		return nil, fmt.Errorf("ollama error: %w", err)
	}
	return parseSenderAnalysis(response)
}

// chat sends a request to the Ollama /api/chat endpoint.
func (c *OllamaClient) chat(prompt string) (string, error) {
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type req struct {
		Model    string `json:"model"`
		Messages []msg  `json:"messages"`
		Stream   bool   `json:"stream"`
		// Think false: triage needs a short JSON answer, not a reasoning trace.
		// Thinking models (Qwen among them) think by default when this is unset,
		// which multiplies the tokens of every batch and can outlast the client
		// timeout. Ollama ignores false on a model that cannot think.
		Think   bool `json:"think"`
		Options struct {
			Temperature float64 `json:"temperature"`
		} `json:"options"`
	}

	body := req{
		Model:    c.model,
		Messages: []msg{{Role: "user", Content: prompt}},
		Stream:   false,
	}
	body.Options.Temperature = 0.3

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequest("POST", c.baseURL+"/api/chat", bytes.NewReader(jsonBody))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		// A server that is up but slower than the client timeout is not
		// unreachable: say which one it is, or the operator debugs the network
		// instead of the model. "timeout" also marks it transient for fallback.
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return "", fmt.Errorf("ollama timeout: model %q at %s gave no answer within %s (still loading, or too slow for this batch): %w", c.model, c.baseURL, c.httpClient.Timeout, err)
		}
		return "", fmt.Errorf("ollama server unreachable at %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", err
	}

	return result.Message.Content, nil
}
