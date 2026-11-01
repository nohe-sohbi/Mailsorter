//go:build acceptance

package api

// What happens when you act on a selection of messages at once.

import "testing"

func TestScenarios_Selection(t *testing.T) {
	playScenarios(t, "Quand tu agis sur une sélection de mails", []scenario{
		{
			phrase: "Quand tu archives ou supprimes une sélection, les mails des expéditeurs protégés ne bougent pas ; les autres, si.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				app.protect("banque.fr")
				ids := app.receiveAndSync(
					incoming{From: "Ma Banque <news@banque.fr>", Subject: "Relevé"},
					incoming{From: "Boutique <info@boutique.com>", Subject: "Soldes"},
					incoming{From: "Ma Banque <alertes@banque.fr>", Subject: "Alerte"},
					incoming{From: "Voyages <promo@voyages.fr>", Subject: "Offre"},
				)
				archived := app.batchAction("archive", ids[0], ids[1])
				deleted := app.batchAction("delete", ids[2], ids[3])
				app.expectInInbox(ids[0], ids[2])
				app.expectNotInInbox(ids[1], ids[3])
				app.expectNotInTrash(ids[0], ids[2])
				app.expectInTrash(ids[3])
				if n := app.number(archived, "protectedSkipped"); n != 1 {
					app.failf("l'archivage groupé devrait annoncer 1 mail protégé épargné, il en annonce %d", n)
				}
				if n := app.number(deleted, "protectedSkipped"); n != 1 {
					app.failf("la suppression groupée devrait annoncer 1 mail protégé épargné, elle en annonce %d", n)
				}
			},
		},
		{
			phrase: "Juste après avoir archivé une sélection, \"Annuler\" remet tous ces mails dans ta boîte de réception.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				ids := app.receiveAndSync(
					incoming{From: "Boutique <info@boutique.com>", Subject: "Soldes"},
					incoming{From: "Voyages <promo@voyages.fr>", Subject: "Offre"},
				)
				app.batchAction("archive", ids...)
				app.expectNotInInbox(ids...)
				app.batchUndo("archive", ids...)
				app.expectInInbox(ids...)
			},
		},
		{
			phrase: "Quand tu reportes une sélection, les mails des expéditeurs protégés ne sont pas reportés et restent dans ta boîte.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				app.protect("banque.fr")
				ids := app.receiveAndSync(
					incoming{From: "Ma Banque <news@banque.fr>", Subject: "Relevé"},
					incoming{From: "Boutique <info@boutique.com>", Subject: "Soldes"},
				)
				app.batchSnooze(ids...)
				app.expectInInbox(ids[0])
				app.expectNotInInbox(ids[1])
			},
		},
	})
}
