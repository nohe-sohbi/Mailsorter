package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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

func NewMistralClient(apiKey, model string) *MistralClient {
	return &MistralClient{
		apiKey:  apiKey,
		model:   model,
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

// EmailAnalysis represents the AI's analysis of an email
type EmailAnalysis struct {
	Action     string  `json:"action"`     // "archive", "delete", "label", "keep"
	LabelName  string  `json:"label_name"` // Suggested label (if action = "label")
	Confidence float64 `json:"confidence"` // 0.0 to 1.0
	Reasoning  string  `json:"reasoning"`  // Brief explanation
}

// AnalyzeEmail analyzes a single email and returns a suggested action
func (c *MistralClient) AnalyzeEmail(email models.Email, existingLabels []string) (*EmailAnalysis, error) {
	labelsContext := ""
	if len(existingLabels) > 0 {
		labelsContext = fmt.Sprintf("\nLabels existants de l'utilisateur: %s", strings.Join(existingLabels, ", "))
	}

	prompt := fmt.Sprintf(`Tu es un assistant de tri d'emails. Analyse cet email et suggère une action.

Email:
- De: %s
- Sujet: %s
- Extrait: %s
%s

Actions possibles:
- "archive": Pour les emails informatifs déjà lus ou non importants (newsletters lues, confirmations, notifications)
- "delete": Pour les emails indésirables, spam, ou promotions non souhaitées
- "label": Pour les emails à catégoriser
- "keep": Pour les emails importants qui nécessitent une action ou attention

Réponds UNIQUEMENT en JSON valide avec ce format exact:
{
  "action": "archive|delete|label|keep",
  "label_name": "Nom du label si action=label, sinon chaîne vide",
  "confidence": 0.0 à 1.0,
  "reasoning": "Explication courte en français (max 100 caractères)"
}

IMPORTANT pour les labels - sois PRECIS et SPECIFIQUE:
- Utilise un label existant si pertinent
- Propose des labels PRECIS selon le TYPE d'email:
  * Livraisons/Colis: "Livraison" ou "Suivi Colis"
  * Factures/Paiements: "Factures"
  * Confirmations d'achat: "Achats"
  * Newsletters: "Newsletters"
  * Réseaux sociaux: "Social" (Facebook, Twitter, LinkedIn...)
  * Voyages: "Voyages" (billets, réservations)
  * Banque: "Banque"
  * Travail: "Travail"
  * Administration: "Administratif"
- NE PAS utiliser de labels trop génériques comme "E-commerce"
- Préfère des labels orientés ACTION/TYPE plutôt que SOURCE`,
		email.From, email.Subject, truncate(email.Snippet, 200), labelsContext)

	response, err := c.chat(prompt)
	if err != nil {
		return nil, fmt.Errorf("mistral API error: %w", err)
	}

	var analysis EmailAnalysis
	if err := json.Unmarshal([]byte(response), &analysis); err != nil {
		// Try to extract JSON from response if it contains extra text
		jsonStart := strings.Index(response, "{")
		jsonEnd := strings.LastIndex(response, "}")
		if jsonStart >= 0 && jsonEnd > jsonStart {
			cleanJSON := response[jsonStart : jsonEnd+1]
			if err := json.Unmarshal([]byte(cleanJSON), &analysis); err != nil {
				return nil, fmt.Errorf("failed to parse AI response: %w", err)
			}
		} else {
			return nil, fmt.Errorf("failed to parse AI response: %w", err)
		}
	}

	// Validate and normalize
	analysis.Action = strings.ToLower(analysis.Action)
	if analysis.Action != "archive" && analysis.Action != "delete" && analysis.Action != "label" && analysis.Action != "keep" {
		analysis.Action = "keep"
	}
	if analysis.Confidence < 0 {
		analysis.Confidence = 0
	}
	if analysis.Confidence > 1 {
		analysis.Confidence = 1
	}

	return &analysis, nil
}

// SenderAnalysis represents the AI's analysis of a sender's emails
type SenderAnalysis struct {
	SuggestedAction string  `json:"suggested_action"`
	SuggestedLabel  string  `json:"suggested_label"`
	Confidence      float64 `json:"confidence"`
	Reasoning       string  `json:"reasoning"`
	SenderType      string  `json:"sender_type"` // "commercial", "personal", "work", "newsletter", "transactional"
}

// AnalyzeSender analyzes multiple emails from the same sender
func (c *MistralClient) AnalyzeSender(senderEmail string, emails []models.Email, existingLabels []string) (*SenderAnalysis, error) {
	// Build email summaries
	var emailSummaries []string
	for i, email := range emails {
		if i >= 5 { // Limit to 5 emails for context
			break
		}
		emailSummaries = append(emailSummaries, fmt.Sprintf("- Sujet: %s", email.Subject))
	}

	labelsContext := ""
	if len(existingLabels) > 0 {
		labelsContext = fmt.Sprintf("\nLabels existants: %s", strings.Join(existingLabels, ", "))
	}

	prompt := fmt.Sprintf(`Tu es un assistant de tri d'emails. Analyse cet expéditeur et ses emails pour suggérer une action par défaut.

Expéditeur: %s
Nombre d'emails: %d

Exemples de sujets:
%s
%s

Actions possibles:
- "archive": Archiver automatiquement (notifications, confirmations)
- "delete": Supprimer (spam, promotions non voulues)
- "label": Catégoriser avec un label
- "keep": Garder en inbox (emails importants)

Réponds UNIQUEMENT en JSON valide:
{
  "suggested_action": "archive|delete|label|keep",
  "suggested_label": "Nom du label si action=label",
  "confidence": 0.0 à 1.0,
  "reasoning": "Explication courte en français",
  "sender_type": "commercial|personal|work|newsletter|transactional"
}`,
		senderEmail, len(emails), strings.Join(emailSummaries, "\n"), labelsContext)

	response, err := c.chat(prompt)
	if err != nil {
		return nil, fmt.Errorf("mistral API error: %w", err)
	}

	var analysis SenderAnalysis
	if err := json.Unmarshal([]byte(response), &analysis); err != nil {
		// Try to extract JSON
		jsonStart := strings.Index(response, "{")
		jsonEnd := strings.LastIndex(response, "}")
		if jsonStart >= 0 && jsonEnd > jsonStart {
			cleanJSON := response[jsonStart : jsonEnd+1]
			if err := json.Unmarshal([]byte(cleanJSON), &analysis); err != nil {
				return nil, fmt.Errorf("failed to parse AI response: %w", err)
			}
		} else {
			return nil, fmt.Errorf("failed to parse AI response: %w", err)
		}
	}

	return &analysis, nil
}

// AnalyzeBatch analyzes several emails in a single API call and returns one
// analysis per email, in order. This collapses N requests into ⌈N/batch⌉,
// slashing both cost and latency. Returns an error if the model's response
// can't be aligned with the input, so the caller can fall back per-email.
func (c *MistralClient) AnalyzeBatch(emails []models.Email, existingLabels []string) ([]EmailAnalysis, error) {
	if len(emails) == 0 {
		return nil, nil
	}

	var list strings.Builder
	for i, e := range emails {
		fmt.Fprintf(&list, "%d. De: %s | Sujet: %s | Extrait: %s\n",
			i+1, e.From, e.Subject, truncate(e.Snippet, 160))
	}

	labelsContext := ""
	if len(existingLabels) > 0 {
		labelsContext = "\nLabels existants de l'utilisateur: " + strings.Join(existingLabels, ", ")
	}

	prompt := fmt.Sprintf(`Tu es un assistant de tri d'emails. Analyse les %d emails ci-dessous et propose une action pour CHACUN.

Emails:
%s%s

Actions possibles:
- "archive": informatif déjà lu / non important (newsletters lues, confirmations, notifications)
- "delete": indésirable, spam, promotions non souhaitées
- "label": à catégoriser (labels PRÉCIS par TYPE: Livraison, Factures, Achats, Newsletters, Social, Voyages, Banque, Travail, Administratif)
- "keep": important, nécessite une action ou attention

Réponds UNIQUEMENT avec un TABLEAU JSON de %d objets, dans le MÊME ORDRE que les emails, format exact:
[{"action":"archive|delete|label|keep","label_name":"label si action=label sinon vide","confidence":0.0,"reasoning":"explication courte en français"}]`,
		len(emails), list.String(), labelsContext, len(emails))

	maxTokens := 120*len(emails) + 200
	if maxTokens > 4000 {
		maxTokens = 4000
	}

	response, err := c.chatTokens(prompt, maxTokens)
	if err != nil {
		return nil, fmt.Errorf("mistral API error: %w", err)
	}

	start := strings.Index(response, "[")
	end := strings.LastIndex(response, "]")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON array in batch response")
	}

	var results []EmailAnalysis
	if err := json.Unmarshal([]byte(response[start:end+1]), &results); err != nil {
		return nil, fmt.Errorf("failed to parse batch response: %w", err)
	}
	if len(results) < len(emails) {
		return nil, fmt.Errorf("batch returned %d analyses for %d emails", len(results), len(emails))
	}

	for i := range results {
		results[i].Action = strings.ToLower(strings.TrimSpace(results[i].Action))
		switch results[i].Action {
		case "archive", "delete", "label", "keep":
		default:
			results[i].Action = "keep"
		}
		if results[i].Confidence < 0 {
			results[i].Confidence = 0
		}
		if results[i].Confidence > 1 {
			results[i].Confidence = 1
		}
	}

	return results[:len(emails)], nil
}

// FindMatchingLabel checks if a suggested label matches an existing one
func (c *MistralClient) FindMatchingLabel(suggestedLabel string, existingLabels []string) (string, bool, error) {
	if len(existingLabels) == 0 {
		return suggestedLabel, false, nil
	}

	prompt := fmt.Sprintf(`Tu dois déterminer si un label suggéré correspond à un label existant.

Label suggéré: "%s"
Labels existants: %s

Réponds UNIQUEMENT en JSON valide:
{
  "matches_existing": true ou false,
  "matched_label": "nom du label existant qui correspond, ou le label suggéré si pas de correspondance"
}

Règles:
- "E-commerce" et "Shopping" sont équivalents
- "Newsletters" et "Newsletter" sont équivalents
- Ignore les différences de casse
- Si aucun label existant ne correspond, renvoie le label suggéré`,
		suggestedLabel, strings.Join(existingLabels, ", "))

	response, err := c.chat(prompt)
	if err != nil {
		return suggestedLabel, false, nil // Fallback to suggested label
	}

	var result struct {
		MatchesExisting bool   `json:"matches_existing"`
		MatchedLabel    string `json:"matched_label"`
	}

	if err := json.Unmarshal([]byte(response), &result); err != nil {
		jsonStart := strings.Index(response, "{")
		jsonEnd := strings.LastIndex(response, "}")
		if jsonStart >= 0 && jsonEnd > jsonStart {
			cleanJSON := response[jsonStart : jsonEnd+1]
			if err := json.Unmarshal([]byte(cleanJSON), &result); err != nil {
				return suggestedLabel, false, nil
			}
		} else {
			return suggestedLabel, false, nil
		}
	}

	return result.MatchedLabel, result.MatchesExisting, nil
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
	reqBody := chatRequest{
		Model: c.model,
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
	for attempt := 0; ; attempt++ {
		content, retryable, retryAfter, err := c.doChat(jsonBody)
		if err == nil {
			return content, nil
		}
		lastErr = err
		if !retryable || attempt >= c.maxRetries {
			return "", lastErr
		}
		c.sleep(c.backoff(attempt, retryAfter))
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

// Helper function to truncate strings
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
