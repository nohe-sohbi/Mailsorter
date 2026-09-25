package ai

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
)

// ─── Shared prompt builders ─────────────────────────────────────────────────
// These functions construct the same prompts that mistral.go used to inline.
// Every provider reuses them so the AI sees identical instructions regardless
// of the underlying model.

func buildAnalyzeEmailPrompt(email models.Email, existingLabels []string) string {
	labelsContext := ""
	if len(existingLabels) > 0 {
		labelsContext = fmt.Sprintf("\nLabels existants de l'utilisateur: %s", strings.Join(existingLabels, ", "))
	}

	return fmt.Sprintf(`Tu es un assistant de tri d'emails. Analyse cet email et suggère une action.

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
- Préfère des labels orientés ACTION/TYPE plutôt que SOURCE

Réponds UNIQUEMENT en JSON valide:
{"action":"archive|delete|label|keep","label_name":"label si action=label sinon vide","confidence":0.0,"reasoning":"explication courte en français"}`,
		email.From, email.Subject, truncate(emailSnippet(email), 200), labelsContext)
}

func buildBatchPrompt(emails []models.Email, existingLabels []string) string {
	var list strings.Builder
	for i, e := range emails {
		fmt.Fprintf(&list, "%d. De: %s | Sujet: %s | Extrait: %s\n",
			i+1, e.From, e.Subject, truncate(emailSnippet(e), 160))
	}

	labelsContext := ""
	if len(existingLabels) > 0 {
		labelsContext = "\nLabels existants de l'utilisateur: " + strings.Join(existingLabels, ", ")
	}

	return fmt.Sprintf(`Tu es un assistant de tri d'emails. Analyse les %d emails ci-dessous et propose une action pour CHACUN.

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
}

func buildSenderPrompt(senderEmail string, emails []models.Email, existingLabels []string) string {
	var emailSummaries []string
	for i, email := range emails {
		if i >= 5 {
			break
		}
		emailSummaries = append(emailSummaries, fmt.Sprintf("- Sujet: %s", email.Subject))
	}

	labelsContext := ""
	if len(existingLabels) > 0 {
		labelsContext = fmt.Sprintf("\nLabels existants: %s", strings.Join(existingLabels, ", "))
	}

	return fmt.Sprintf(`Tu es un assistant de tri d'emails. Analyse cet expéditeur et ses emails pour suggérer une action par défaut.

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
}

// ─── Shared response parsers ────────────────────────────────────────────────

func parseEmailAnalysis(response string) (*EmailAnalysis, error) {
	var analysis EmailAnalysis
	if err := json.Unmarshal([]byte(response), &analysis); err != nil {
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

	analysis.Action = strings.ToLower(analysis.Action)
	switch analysis.Action {
	case "archive", "delete", "label", "keep":
	default:
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

func parseBatchAnalysis(response string, expectedCount int) ([]EmailAnalysis, error) {
	start := strings.Index(response, "[")
	end := strings.LastIndex(response, "]")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON array in batch response")
	}

	var results []EmailAnalysis
	if err := json.Unmarshal([]byte(response[start:end+1]), &results); err != nil {
		return nil, fmt.Errorf("failed to parse batch response: %w", err)
	}
	if len(results) < expectedCount {
		return nil, fmt.Errorf("batch returned %d analyses for %d emails", len(results), expectedCount)
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
	return results[:expectedCount], nil
}

func parseSenderAnalysis(response string) (*SenderAnalysis, error) {
	var analysis SenderAnalysis
	if err := json.Unmarshal([]byte(response), &analysis); err != nil {
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

// emailSnippet and truncate are shared helpers.
func emailSnippet(e models.Email) string {
	s := strings.TrimSpace(e.Snippet)
	if s == "" {
		s = strings.TrimSpace(e.Body)
	}
	if s == "" {
		return "(Contenu non disponible)"
	}
	return s
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
