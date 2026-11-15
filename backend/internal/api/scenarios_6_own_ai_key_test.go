//go:build acceptance

package api

// Your own AI provider key, and where the server agrees to send a request with
// it.
//
// The provider's address is typed by the user, so it is the user who picks
// where the SERVER sends a request, from inside the server's network. The
// internal service these scenarios aim at is real (TLS, on 127.0.0.1) and is
// reached through a public-looking name: acceptance_intranet_test.go.

import (
	"net/http"
	"strings"
	"testing"
)

func TestScenarios_OwnAIKey(t *testing.T) {
	playScenarios(t, "Ta clé IA personnelle", []scenario{
		{
			phrase: "Quand tu testes ta clé IA personnelle avec une adresse de fournisseur qui mène au réseau interne du serveur, le test échoue et le message d'erreur ne contient rien de ce que ce service interne a répondu.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				svc := app.internalService()
				code, body := app.testOwnAIKey(ownAIKey{
					Provider: "openai-compatible",
					APIKey:   "cle-perso-123",
					Model:    "modele-perso",
					BaseURL:  svc.url + "/v1/chat/completions",
				})
				if code >= 200 && code < 300 {
					app.failf("le test de ta clé IA vers %s (réseau interne) devrait échouer, il a réussi (%d) : %s", svc.url, code, body)
				}
				if strings.Contains(body, intranetSecret) {
					app.failf("le message d'erreur du test contient ce que le service interne a répondu (%q) : %s", intranetSecret, body)
				}
			},
		},
		{
			phrase: "Quand ta clé IA personnelle enregistrée désigne une adresse du réseau interne du serveur, analyser tes mails n'envoie rien à cette adresse.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				svc := app.internalService()
				app.ownAIKeyAlreadySaved(ownAIKey{
					Provider: "openai-compatible",
					APIKey:   "cle-perso-123",
					Model:    "modele-perso",
					BaseURL:  svc.url + "/v1/chat/completions",
				})
				subject := "Relevé de compte de mars"
				ids := app.receiveAndSync(incoming{From: "Ma Banque <releves@banque.fr>", Subject: subject})
				// Whatever the analysis answers: the sentence is about what
				// leaves the server, not about how the screen reports it.
				app.call(http.MethodPost, "/api/ai/analyze", map[string]interface{}{"emailIds": ids})
				app.expectNothingSentTo(svc, "l'analyse de tes mails", subject)
			},
		},
		{
			phrase: "Enregistrer Ollama comme fournisseur IA personnel sans indiquer d'adresse est refusé.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				code, out := app.saveOwnAIKey(ownAIKey{Provider: "ollama", Model: "llama3"})
				if code != http.StatusBadRequest {
					app.failf("enregistrer Ollama sans adresse devrait être refusé (400), la réponse est %d : %v", code, out)
				}
				shown := app.ownAIKeyShown()
				if shown["provider"] == "ollama" || shown["configured"] == true {
					app.failf("après ce refus, tes réglages IA ne devraient montrer aucun fournisseur personnel, ils montrent le fournisseur %v (personnel : %v, adresse : %q)", shown["provider"], shown["configured"], shown["baseUrl"])
				}
			},
		},
	})
}
