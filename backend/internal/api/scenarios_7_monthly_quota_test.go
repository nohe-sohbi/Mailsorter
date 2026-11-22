//go:build acceptance

package api

// How many mails the AI may analyze in a month.
//
// The rule Nohé validated: installed at home, Mailsorter has no monthly cap on
// analyses; the 200 a month hold only for the managed service, which pays for
// the AI. The edition is the one the server boots with (EDITION), set per
// scenario (installedAtHome, onManagedService). The accounts are free ones:
// neither the scenario's user nor an IMAP person has a plan, and Pro has no
// cap in any edition. The model's opinion is faked (acceptance_fakeai_test.go).

import (
	"fmt"
	"net/http"
	"testing"
)

// monthlyCap is the 200 of the sentences, written out rather than read from
// FreeMonthlyLimit: the sentences say 200, so a change to that constant must
// show here as a KO, not follow silently.
const monthlyCap = 200

// quotaReached is what the server answers when it stops an analysis because
// the month's analyses are spent.
const quotaReached = "Quota mensuel atteint. Passez à Pro pour continuer."

// asyncSelection is how many mails a selection needs for the inbox to analyze
// it in the background: more than ASYNC_THRESHOLD (10, Inbox.js).
const asyncSelection = 11

// offers returns n mails from one shop, each with its own subject, so that
// none of them is served from the analysis cache: each one goes to the AI.
func offers(n int) []incoming {
	mails := make([]incoming, 0, n)
	for i := 1; i <= n; i++ {
		mails = append(mails, incoming{From: "Boutique <info@boutique.com>", Subject: fmt.Sprintf("Offre n°%d", i)})
	}
	return mails
}

func TestScenarios_MonthlyQuota(t *testing.T) {
	playScenarios(t, "Le plafond mensuel d'analyses", []scenario{
		{
			phrase: "Installé chez soi, une analyse se lance même quand 200 mails ont déjà été analysés ce mois-ci : l'IA rend ses propositions.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				app.installedAtHome()
				app.ai.willSay("archive", 0.95)
				ids := app.receiveAndSync(offers(2)...)
				app.alreadyAnalyzed(scenarioUser, app.token, monthlyCap)

				code, out := app.call(http.MethodPost, "/api/ai/analyze", map[string]interface{}{"emailIds": ids})
				if code != http.StatusOK {
					app.failf("installé chez soi, l'analyse de %d mails devrait se lancer alors que %d mails ont déjà été analysés ce mois-ci, le serveur a répondu %s", len(ids), monthlyCap, answer{code: code, body: out})
					return
				}
				if n := app.ai.callCount(); n == 0 {
					app.failf("installé chez soi, l'IA devrait être consultée pour rendre ses propositions, elle ne l'a pas été")
				}
				for _, id := range ids {
					if s := app.suggestionFor(id); s.Status != "pending" || s.Action != "archive" {
						app.failf("la proposition de l'IA pour %s devrait être \"archiver, en attente\", elle est %q / %q", app.describe(id), s.Action, s.Status)
					}
				}
			},
		},
		{
			phrase: "Installé chez soi, l'analyse en arrière-plan d'une sélection démarre même quand 200 mails ont déjà été analysés ce mois-ci.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				app.installedAtHome()
				app.ai.willSay("archive", 0.95)
				ids := app.receiveAndSync(offers(asyncSelection)...)
				app.alreadyAnalyzed(scenarioUser, app.token, monthlyCap)

				code, out := app.call(http.MethodPost, "/api/ai/analyze-async", map[string]interface{}{"emailIds": ids})
				if code != http.StatusAccepted {
					app.failf("installé chez soi, l'analyse en arrière-plan d'une sélection de %d mails devrait démarrer alors que %d mails ont déjà été analysés ce mois-ci, le serveur a répondu %s", len(ids), monthlyCap, answer{code: code, body: out})
					return
				}
				jobID, _ := out["jobId"].(string)
				if jobID == "" {
					app.fatalf("l'analyse en arrière-plan a démarré sans numéro à suivre, la boîte de réception ne peut pas montrer où elle en est : %v", out)
				}

				app.workersRunQueuedAnalyses()
				job := app.mustCall(http.MethodGet, "/api/ai/jobs/"+jobID, nil, http.StatusOK)
				if job["status"] != "done" {
					app.failf("l'analyse en arrière-plan a démarré mais devrait aller à son terme, elle en est à %q (erreur : %v)", job["status"], job["error"])
				}
				if n := app.ai.callCount(); n == 0 {
					app.failf("l'analyse en arrière-plan devrait consulter l'IA, elle ne l'a pas fait")
				}
			},
		},
		{
			phrase: "Installé chez soi, le compteur d'analyses du mois n'affiche aucune limite.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				app.installedAtHome()
				usage := app.usageAs(app.token)
				if limit := app.number(usage, "limit"); limit >= 0 {
					app.failf("installé chez soi, le compteur d'analyses du mois ne devrait afficher aucune limite (une limite négative, comme pour Pro), il annonce une limite de %d (%v analysés)", limit, usage["used"])
				}
			},
		},
		{
			phrase: "Sur le service géré, un compte gratuit qui a déjà fait analyser 200 mails ce mois-ci est arrêté avec \"Quota mensuel atteint. Passez à Pro pour continuer.\" et aucune analyse ne part.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				app.onManagedService()
				app.ai.willSay("archive", 0.95)
				// On the managed service a mailbox is reached over IMAP: the
				// Gmail API is not in that edition's catalog.
				person := app.imapPerson("compte-gratuit@exemple.fr")
				ids := person.receive(offers(asyncSelection)...)
				person.sync()
				app.alreadyAnalyzed(person.email, person.token, monthlyCap)
				if plan := app.usageAs(person.token)["plan"]; plan != "free" {
					app.fatalf("le scénario suppose un compte gratuit, le compteur de %s annonce le plan %v : le scénario ne prouve rien", person.email, plan)
				}

				for _, launch := range []struct {
					what string
					path string
					ids  []string
				}{
					{"l'analyse de 2 mails", "/api/ai/analyze", ids[:2]},
					{fmt.Sprintf("l'analyse en arrière-plan d'une sélection de %d mails", len(ids)), "/api/ai/analyze-async", ids},
				} {
					code, out := app.callAs(person.token, http.MethodPost, launch.path, map[string]interface{}{"emailIds": launch.ids})
					got := answer{code: code, body: out}
					if code != http.StatusPaymentRequired || got.said() != quotaReached {
						app.failf("sur le service géré, %s par un compte gratuit qui a déjà fait analyser %d mails ce mois-ci devrait être arrêtée avec %q, le serveur a répondu %s", launch.what, monthlyCap, quotaReached, got)
					}
				}

				app.workersRunQueuedAnalyses()
				if n := app.ai.callCount(); n != 0 {
					app.failf("sur le service géré, aucune analyse ne devrait partir pour un compte gratuit qui a déjà fait analyser %d mails ce mois-ci, l'IA a été consultée %d fois", monthlyCap, n)
				}
			},
		},
	})
}
