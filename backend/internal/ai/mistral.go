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

const (
	mistralAPIURL = "https://api.mistral.ai/v1/chat/completions"
)

type MistralClient struct {
	apiKey     string
	model      string
	httpClient *http.Client
}

func NewMistralClient(apiKey, model string) *MistralClient {
	return &MistralClient{
		apiKey: apiKey,
		model:  model,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
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
	Action     string  `json:"action"`      // "archive", "delete", "label", "keep"
	LabelName  string  `json:"label_name"`  // Suggested label (if action = "label")
	Confidence float64 `json:"confidence"`  // 0.0 to 1.0
	Reasoning  string  `json:"reasoning"`   // Brief explanation
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

// chat sends a message to Mistral and returns the response
func (c *MistralClient) chat(prompt string) (string, error) {
	reqBody := chatRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "user", Content: prompt},
		},
		Temperature: 0.3, // Low temperature for consistent responses
		MaxTokens:   500,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", mistralAPIURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("mistral API returned status %d: %s", resp.StatusCode, string(body))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return "", err
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("no response from Mistral")
	}

	return chatResp.Choices[0].Message.Content, nil
}

// Helper function to truncate strings
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
