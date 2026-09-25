package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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

func (c *OllamaClient) AnalyzeEmail(email models.Email, existingLabels []string) (*EmailAnalysis, error) {
	prompt := buildAnalyzeEmailPrompt(email, existingLabels)
	response, err := c.chat(prompt)
	if err != nil {
		return nil, fmt.Errorf("ollama error: %w", err)
	}
	return parseEmailAnalysis(response)
}

func (c *OllamaClient) AnalyzeBatch(emails []models.Email, existingLabels []string) ([]EmailAnalysis, error) {
	if len(emails) == 0 {
		return nil, nil
	}
	prompt := buildBatchPrompt(emails, existingLabels)
	response, err := c.chat(prompt)
	if err != nil {
		return nil, fmt.Errorf("ollama error: %w", err)
	}
	return parseBatchAnalysis(response, len(emails))
}

func (c *OllamaClient) AnalyzeSender(senderEmail string, emails []models.Email, existingLabels []string) (*SenderAnalysis, error) {
	prompt := buildSenderPrompt(senderEmail, emails, existingLabels)
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
		Options  struct {
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
		return "", fmt.Errorf("ollama server unreachable: %w", err)
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
