//go:build acceptance

package api

// What a person whose mailbox is reached over IMAP sees after a sync.
//
// Over IMAP a mail is known by the number its server gave it inside the
// folder (its UID), so two people's first mails are both "1". The sync copies
// the inbox into the database and the inbox screen is served from that copy
// (mirror.go): a mail the sync could not store is a mail the person never
// sees. Each mailbox here is a real IMAP server over TLS, and the database
// starts as production's does (acceptance_imap_test.go).

import (
	"net/http"
	"testing"
)

// refusedSubject is the subject of the one mail the database refuses to store
// in the scenarios about a failed write (see refuseToStore).
const refusedSubject = "Invitation au salon de l'auto"

func TestScenarios_IMAPMirror(t *testing.T) {
	playScenarios(t, "Ta boîte IMAP", []scenario{
		{
			phrase: "Quand deux personnes ont chacune, dans leur boîte IMAP, un mail que leur serveur a numéroté 1, chacune le voit dans sa boîte de réception après avoir synchronisé.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				anne := app.imapPerson("anne@exemple.fr")
				bruno := app.imapPerson("bruno@exemple.org")
				a := anne.receive(incoming{From: "Impôts <contact@impots.gouv.fr>", Subject: "Votre avis d'impôt est disponible"})
				b := bruno.receive(incoming{From: "Cabinet dentaire <rdv@dentiste.fr>", Subject: "Rappel de votre rendez-vous"})
				anne.expectNumbered(a[0], "1")
				bruno.expectNumbered(b[0], "1")

				anne.sync()
				bruno.sync()

				anne.expectShows(a[0], "Votre avis d'impôt est disponible")
				bruno.expectShows(b[0], "Rappel de votre rendez-vous")
			},
		},
		{
			phrase: "Après la synchronisation de la seconde personne, la première voit toujours son propre mail n°1, avec son sujet, et jamais celui de l'autre.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				anne := app.imapPerson("anne@exemple.fr")
				bruno := app.imapPerson("bruno@exemple.org")
				a := anne.receive(incoming{From: "Impôts <contact@impots.gouv.fr>", Subject: "Votre avis d'impôt est disponible"})
				anne.expectNumbered(a[0], "1")
				anne.sync()

				b := bruno.receive(incoming{From: "Cabinet dentaire <rdv@dentiste.fr>", Subject: "Rappel de votre rendez-vous"})
				bruno.expectNumbered(b[0], "1")
				bruno.sync()

				anne.expectShows(a[0], "Votre avis d'impôt est disponible")
				anne.expectDoesNotShow("Rappel de votre rendez-vous", "c'est le mail n°1 de bruno@exemple.org")
			},
		},
		{
			phrase: "Quand un des mails de ta boîte IMAP ne peut pas être enregistré, « Synchroniser » répond une erreur au lieu d'annoncer une synchronisation réussie.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi := app.imapPerson("claire@exemple.fr")
				uids := moi.receive(
					incoming{From: "Ma Banque <info@banque.fr>", Subject: "Votre relevé de compte"},
					incoming{From: "Salon <invitations@salon-auto.fr>", Subject: refusedSubject},
					incoming{From: "Garage <rdv@garage.fr>", Subject: "Votre rendez-vous"},
				)
				app.refuseToStore(refusedSubject)

				code, out := moi.syncAnswer()
				if code < http.StatusBadRequest {
					app.failf("\"Synchroniser\" a répondu %d et annonce une synchronisation réussie (%v mail(s) enregistré(s) sur %v) alors que le mail n°%s %q n'a pas pu être enregistré : il devait répondre une erreur",
						code, out["synced"], out["total"], uids[1], refusedSubject)
				}

				// The control: once the database takes that mail again, the
				// same sync succeeds. Without it, an error from anywhere (an
				// unreachable server) would pass for the one this sentence means.
				app.storeEverythingAgain()
				if code, out := moi.syncAnswer(); code != http.StatusOK {
					app.failf("une fois le mail %q de nouveau accepté par la base, \"Synchroniser\" répond encore %d (attendu 200) : l'erreur ne venait donc pas de ce mail (%v)", refusedSubject, code, out)
				}
			},
		},
		{
			phrase: "Dans ce cas, les autres mails de ta boîte sont quand même enregistrés et affichés.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi := app.imapPerson("claire@exemple.fr")
				uids := moi.receive(
					incoming{From: "Ma Banque <info@banque.fr>", Subject: "Votre relevé de compte"},
					incoming{From: "Salon <invitations@salon-auto.fr>", Subject: refusedSubject},
					incoming{From: "Garage <rdv@garage.fr>", Subject: "Votre rendez-vous"},
				)
				app.refuseToStore(refusedSubject)

				// Whatever it answers: the answer is the sentence above.
				moi.syncAnswer()

				if moi.shows(refusedSubject) {
					app.fatalf("le mail %q est affiché alors que la base devait refuser de l'enregistrer : le scénario ne prouve rien (voir refuseToStore dans acceptance_imap_test.go)", refusedSubject)
				}
				moi.expectShows(uids[0], "Votre relevé de compte")
				moi.expectShows(uids[2], "Votre rendez-vous")
			},
		},
	})
}
