//go:build acceptance

package api

// What the AI does on its own, and what it waits for you to approve.
//
// The model's opinion is faked (acceptance_fakeai_test.go): every message gets
// the verdict the scenario chose. What is real is everything Mailsorter does
// with that opinion.

import "testing"

func TestScenarios_AI(t *testing.T) {
	playScenarios(t, "L'IA", []scenario{
		{
			phrase: "L'IA ne range rien toute seule : elle propose, et le mail reste dans ta boîte tant que tu n'as pas validé.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				app.ai.willSay("archive", 0.95)
				ids := app.receiveAndSync(
					incoming{From: "Boutique <info@boutique.com>", Subject: "Soldes"},
					incoming{From: "Voyages <promo@voyages.fr>", Subject: "Offre"},
				)
				app.analyze(ids...)
				app.expectInInbox(ids...)
				for _, id := range ids {
					if s := app.suggestionFor(id); s.Status != "pending" || s.Action != "archive" {
						app.failf("la proposition pour %s devrait être \"archiver, en attente\", elle est %q / %q", app.describe(id), s.Action, s.Status)
					}
				}
			},
		},
		{
			phrase: "Pour un expéditeur protégé, l'IA propose toujours de garder le mail, même quand elle voulait l'archiver.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				app.protect("banque.fr")
				app.ai.willSay("archive", 0.95)
				ids := app.receiveAndSync(incoming{From: "Ma Banque <news@banque.fr>", Subject: "Offre"})
				app.analyze(ids...)
				if s := app.suggestionFor(ids[0]); s.Action != "keep" {
					app.failf("pour un expéditeur protégé, la proposition devrait être \"garder\", elle est %q", s.Action)
				}
				app.expectInInbox(ids[0])
			},
		},
		{
			phrase: "\"Tout ranger\" laisse en place les mails d'un expéditeur devenu protégé après la proposition de l'IA.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				app.ai.willSay("archive", 0.95)
				ids := app.receiveAndSync(
					incoming{From: "Ma Banque <news@banque.fr>", Subject: "Relevé"},
					incoming{From: "Boutique <info@boutique.com>", Subject: "Soldes"},
				)
				app.analyze(ids...)
				app.protect("banque.fr")
				app.applyAllSuggestions(ids...)
				app.expectInInbox(ids[0])
				app.expectNotInInbox(ids[1])
			},
		},
		{
			phrase: "Appliquer une seule proposition de l'IA la suit même si l'expéditeur est devenu protégé entre-temps.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				app.ai.willSay("archive", 0.95)
				ids := app.receiveAndSync(incoming{From: "Ma Banque <news@banque.fr>", Subject: "Relevé"})
				app.analyze(ids...)
				app.protect("banque.fr")
				app.applySuggestion(ids[0])
				app.expectNotInInbox(ids[0])
				app.expectHistory(ids[0], "archive", SourceAI)
			},
		},
		{
			phrase: "Le pilote automatique d'un expéditeur range ses mails dès l'analyse, sans te demander et sans consulter l'IA.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				app.autopilot("Boutique <info@boutique.com>", "archive")
				ids := app.receiveAndSync(incoming{From: "Boutique <info@boutique.com>", Subject: "Soldes"})
				app.analyze(ids...)
				app.expectNotInInbox(ids[0])
				app.expectHistory(ids[0], "archive", SourceAIAuto)
				if n := app.ai.callCount(); n != 0 {
					app.failf("l'IA ne devrait pas être consultée pour un expéditeur en pilote automatique, elle l'a été %d fois", n)
				}
			},
		},
		{
			phrase: "Le pilote automatique ne range jamais le mail d'un expéditeur protégé.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				app.protect("banque.fr")
				app.autopilot("Ma Banque <news@banque.fr>", "archive")
				ids := app.receiveAndSync(incoming{From: "Ma Banque <news@banque.fr>", Subject: "Offre"})
				app.analyze(ids...)
				app.expectInInbox(ids[0])
			},
		},
	})
}
