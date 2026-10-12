//go:build acceptance

package api

// What happens when you act on a whole sender: "Tout supprimer" on the
// Expediteurs screen, and unsubscribing with "et archiver ses mails".

import "testing"

func TestScenarios_WholeSender(t *testing.T) {
	playScenarios(t, "Quand tu agis sur tout un expéditeur", []scenario{
		{
			phrase: "\"Tout supprimer\" pour un expéditeur met à la corbeille tous ses mails connus de l'app, y compris ceux que tu avais déjà archivés.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				ids := app.receiveAndSync(
					incoming{From: "Boutique <info@boutique.com>", Subject: "Soldes"},
					incoming{From: "Boutique <info@boutique.com>", Subject: "Facture de mars"},
				)
				app.act(ids[1], "archive")
				app.deleteAllFrom("Boutique <info@boutique.com>")
				app.expectInTrash(ids...)
				app.expectHistory(ids[1], "delete", SourceBulk)
			},
		},
		{
			phrase: "\"Tout supprimer\" pour un expéditeur protégé ne met aucun de ses mails à la corbeille.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				app.protect("banque.fr")
				ids := app.receiveAndSync(
					incoming{From: "Ma Banque <news@banque.fr>", Subject: "Relevé"},
					incoming{From: "Ma Banque <news@banque.fr>", Subject: "Offre"},
				)
				app.deleteAllFrom("Ma Banque <news@banque.fr>")
				app.expectInInbox(ids...)
				app.expectNotInTrash(ids...)
			},
		},
		{
			phrase: "Quand un expéditeur n'affiche pas de nom (juste info@boutique.com), \"Tout supprimer\" pour lui met aussi à la corbeille les mails de facturation-info@boutique.com et de info@boutique.community.",
			status: rejected,
			run: func(t *testing.T, app *testApp) {
				ids := app.receiveAndSync(
					incoming{From: "info@boutique.com", Subject: "Soldes"},
					incoming{From: "Facturation <facturation-info@boutique.com>", Subject: "Votre facture"},
					incoming{From: "info@boutique.community", Subject: "Forum"},
					incoming{From: "Service client <contact@boutique.com>", Subject: "Votre commande"},
				)
				app.deleteAllFrom("info@boutique.com")
				app.expectInTrash(ids[0], ids[1], ids[2])
				app.expectInInbox(ids[3])
			},
		},
		{
			phrase: "Un expéditeur qui change de nom affiché (\"Boutique\" puis \"Boutique Soldes\") compte comme deux expéditeurs : \"Tout supprimer\" sur l'un laisse les mails de l'autre.",
			status: rejected,
			run: func(t *testing.T, app *testApp) {
				ids := app.receiveAndSync(
					incoming{From: "Boutique <info@boutique.com>", Subject: "Nouveautés"},
					incoming{From: "Boutique Soldes <info@boutique.com>", Subject: "Soldes"},
				)
				app.deleteAllFrom("Boutique <info@boutique.com>")
				app.expectInTrash(ids[0])
				app.expectInInbox(ids[1])
			},
		},
		{
			phrase: "Quand tu te désabonnes en demandant d'archiver, l'app archive aussi les mails des adresses qui contiennent celle de l'expéditeur, comme facturation-info@boutique.com.",
			status: rejected,
			run: func(t *testing.T, app *testApp) {
				ids := app.receiveAndSync(
					incoming{From: "Boutique <info@boutique.com>", Subject: "Soldes", Unsubscribe: "<mailto:stop@boutique.com>"},
					incoming{From: "Facturation <facturation-info@boutique.com>", Subject: "Votre facture"},
					incoming{From: "Service client <contact@boutique.com>", Subject: "Votre commande"},
				)
				app.unsubscribeAndArchive(ids[0])
				app.expectNotInInbox(ids[0], ids[1])
				app.expectInInbox(ids[2])
				app.expectHistory(ids[1], "archive", SourceUnsubscribe)
			},
		},
		{
			phrase: "Quand tu te désabonnes en demandant d'archiver, les mails d'un expéditeur protégé sont archivés quand même.",
			status: rejected,
			run: func(t *testing.T, app *testApp) {
				app.protect("banque.fr")
				ids := app.receiveAndSync(
					incoming{From: "Ma Banque <news@banque.fr>", Subject: "Lettre d'info", Unsubscribe: "<mailto:stop@banque.fr>"},
					incoming{From: "Ma Banque <news@banque.fr>", Subject: "Relevé"},
				)
				app.unsubscribeAndArchive(ids[0])
				app.expectNotInInbox(ids...)
			},
		},
	})
}
