package ai

import (
	"strings"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
)

// A prompt carries the sender, the subject and an excerpt, and nothing that
// supposes the user's labels: no list, no instruction to reuse one. The
// public pages promise it, and the verdict is cached for every account.
func TestPromptsSupposeNoListOfLabels(t *testing.T) {
	email := models.Email{From: "Énergie <factures@energie.fr>", Subject: "Votre facture", Snippet: "Montant : 42 euros."}
	prompts := map[string]string{
		"email":  buildAnalyzeEmailPrompt(email),
		"batch":  buildBatchPrompt([]models.Email{email, email}),
		"sender": buildSenderPrompt("factures@energie.fr", []models.Email{email}),
	}
	for name, prompt := range prompts {
		for _, word := range []string{"existant", "Labels existants", "label existant"} {
			if strings.Contains(prompt, word) {
				t.Errorf("the %s prompt says %q: %q", name, word, prompt)
			}
		}
		for _, want := range []string{email.Subject} {
			if !strings.Contains(prompt, want) {
				t.Errorf("the %s prompt lost the email's %q: %q", name, want, prompt)
			}
		}
	}
	if !strings.Contains(prompts["email"], email.From) || !strings.Contains(prompts["email"], email.Snippet) {
		t.Errorf("the email prompt lost the sender or the excerpt: %q", prompts["email"])
	}
	if !strings.Contains(prompts["batch"], "Analyse les 2 emails") {
		t.Errorf("the batch prompt lost its count, which the batch parser relies on: %q", prompts["batch"])
	}
}
