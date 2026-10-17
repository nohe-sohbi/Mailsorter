//go:build acceptance

package api

// What happens when you act on a whole sender: "Tout supprimer" and "Tout
// archiver" on the Expediteurs screen, and unsubscribing with "et archiver ses
// mails".

import (
	"strings"
	"testing"
)

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
		// The three sentences below write info@boutique.com with no display
		// name, and with the same From on every one of its mails: they are
		// about which ADDRESSES a sender-wide action reaches, and must not
		// depend on how two display names of one address are grouped. That is
		// the "Boutique" / "Boutique Soldes" sentence after them, a separate
		// decision.
		{
			phrase: "\"Tout supprimer\" pour info@boutique.com ne met à la corbeille que les mails de info@boutique.com : ceux de facturation-info@boutique.com et de info@boutique.community restent dans ta boîte de réception.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				ids := app.receiveAndSync(
					incoming{From: "info@boutique.com", Subject: "Soldes"},
					incoming{From: "Facturation <facturation-info@boutique.com>", Subject: "Votre facture"},
					incoming{From: "info@boutique.community", Subject: "Forum"},
				)
				app.deleteAllFrom("info@boutique.com")
				app.expectInTrash(ids[0])
				app.expectInInbox(ids[1], ids[2])
				app.expectNoHistory(ids[1])
				app.expectNoHistory(ids[2])
			},
		},
		{
			phrase: "Quand l'écran Expéditeurs annonce 2 mails pour info@boutique.com, \"Tout supprimer\" en met 2 à la corbeille, pas un de plus, même si facturation-info@boutique.com t'a aussi écrit.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				ids := app.receiveAndSync(
					incoming{From: "info@boutique.com", Subject: "Soldes"},
					incoming{From: "info@boutique.com", Subject: "Nouveautés"},
					incoming{From: "Facturation <facturation-info@boutique.com>", Subject: "Votre facture"},
				)
				announced := app.announcedCount("info@boutique.com")
				if announced != 2 {
					app.fatalf("l'écran Expéditeurs devrait annoncer 2 mails pour info@boutique.com, il en annonce %d", announced)
				}
				app.deleteAllFrom("info@boutique.com")
				var trashed []string
				for _, id := range ids {
					if app.gmail.has(id, labelTrash) {
						trashed = append(trashed, app.describe(id))
					}
				}
				if len(trashed) != announced {
					app.failf("l'écran Expéditeurs annonçait %d mails pour info@boutique.com, \"Tout supprimer\" en a mis %d à la corbeille : %s", announced, len(trashed), strings.Join(trashed, ", "))
				}
				app.expectInTrash(ids[0], ids[1])
				app.expectInInbox(ids[2])
			},
		},
		{
			phrase: "\"Tout archiver\" pour info@boutique.com n'archive que les mails de info@boutique.com, jamais ceux de facturation-info@boutique.com ou de info@boutique.community.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				ids := app.receiveAndSync(
					incoming{From: "info@boutique.com", Subject: "Soldes"},
					incoming{From: "info@boutique.com", Subject: "Nouveautés"},
					incoming{From: "Facturation <facturation-info@boutique.com>", Subject: "Votre facture"},
					incoming{From: "info@boutique.community", Subject: "Forum"},
				)
				app.archiveAllFrom("info@boutique.com")
				app.expectNotInInbox(ids[0], ids[1])
				app.expectInInbox(ids[2], ids[3])
				app.expectNoHistory(ids[2])
				app.expectNoHistory(ids[3])
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
			phrase: "Quand tu te désabonnes de info@boutique.com en demandant d'archiver, l'app n'archive que les mails de info@boutique.com : ceux de facturation-info@boutique.com et de info@boutique.community restent dans ta boîte de réception.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				ids := app.receiveAndSync(
					incoming{From: "Boutique <info@boutique.com>", Subject: "Soldes", Unsubscribe: "<mailto:stop@boutique.com>"},
					incoming{From: "Boutique <info@boutique.com>", Subject: "Nouveautés", Unsubscribe: "<mailto:stop@boutique.com>"},
					incoming{From: "Facturation <facturation-info@boutique.com>", Subject: "Votre facture"},
					incoming{From: "info@boutique.community", Subject: "Forum"},
				)
				app.unsubscribeAndArchive(ids[0])
				app.expectNotInInbox(ids[0], ids[1])
				app.expectInInbox(ids[2], ids[3])
				app.expectNoHistory(ids[2])
				app.expectNoHistory(ids[3])
			},
		},
		{
			phrase: "Quand tu te désabonnes d'un expéditeur protégé (news@banque.fr) en demandant d'archiver, il est bien marqué \"Désabonné\", mais ses mails restent dans ta boîte de réception.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				app.protect("news@banque.fr")
				ids := app.receiveAndSync(
					incoming{From: "Ma Banque <news@banque.fr>", Subject: "Lettre d'info", Unsubscribe: "<mailto:stop@banque.fr>"},
					incoming{From: "Ma Banque <news@banque.fr>", Subject: "Relevé"},
				)
				app.unsubscribeAndArchive(ids[0])
				app.expectUnsubscribed("news@banque.fr")
				app.expectInInbox(ids...)
			},
		},
	})
}
