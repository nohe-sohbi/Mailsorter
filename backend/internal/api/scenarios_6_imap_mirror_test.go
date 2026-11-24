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
//
// The copy must also lose what left the inbox: a mail archived or deleted
// from Mailsorter, or moved out by another mail program on the same mailbox,
// is gone from the server's inbox, and a screen that still lists it offers to
// act on a mail that is no longer there. The inbox is one of those screens,
// the Expéditeurs tab, which counts the mails of each sender, another.

import (
	"net/http"
	"testing"
)

// refusedSubject is the subject of the one mail the database refuses to store
// in the scenarios about a failed write (see refuseToStore).
const refusedSubject = "Invitation au salon de l'auto"

// imapInboxOwner is the person whose IMAP mailbox the sentences about a mail
// leaving the inbox run on.
const imapInboxOwner = "ma-boite@exemple.fr"

// The mails of those sentences. Their subjects differ because a mail is found
// by its subject on the server: its number changes when it changes folder.
var (
	bankStatement = incoming{From: "Ma Banque <info@banque.fr>", Subject: "Votre relevé de compte"}
	shopOrder     = incoming{From: "Boutique <info@boutique.com>", Subject: "Votre commande est expédiée"}
	travelOffer   = incoming{From: "Voyages <promo@voyages.fr>", Subject: "Offre de la semaine"}
	// shopInvoice is a second mail from the sender of shopOrder.
	shopInvoice = incoming{From: "Boutique <info@boutique.com>", Subject: "Votre facture"}
)

// syncedIMAPInbox brings a person to where those sentences start: these mails
// in their IMAP inbox, synced, and every one of them on the inbox screen. It
// returns the person and the mails' numbers.
func syncedIMAPInbox(app *testApp, mails ...incoming) (*imapPerson, []string) {
	app.t.Helper()
	moi := app.imapPerson(imapInboxOwner)
	uids := moi.receive(mails...)
	moi.sync()
	for i, uid := range uids {
		if !moi.shows(mails[i].Subject) {
			app.fatalf("après la synchronisation, la boîte de réception de %s devrait montrer le mail n°%s %q, elle ne le montre pas : le scénario ne prouve rien", moi.email, uid, mails[i].Subject)
		}
	}
	return moi, uids
}

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
		{
			phrase: "Sur une boîte IMAP, un mail que tu archives disparaît de ta boîte de réception, et n'y revient pas après une nouvelle synchronisation.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)

				// "Archiver" on the row of the second mail.
				moi.act(uids[1], "archive")
				// Archived, not lost: a screen that only hid the mail would
				// pass everything below.
				moi.expectOnServer(serverArchive, shopOrder.Subject)

				// What the inbox loads once the row has left: on a reload, or
				// on the next screen that reads the listing.
				moi.expectDoesNotShow(shopOrder.Subject, "tu viens de l'archiver")
				// The control: the mail nobody touched is still there. Without
				// it, an inbox emptied of everything would pass.
				moi.expectShows(uids[0], bankStatement.Subject)

				moi.sync()
				moi.expectDoesNotShow(shopOrder.Subject, "tu l'as archivé avant cette synchronisation")
				moi.expectShows(uids[0], bankStatement.Subject)
			},
		},
		{
			phrase: "Sur une boîte IMAP, un mail que tu supprimes disparaît de ta boîte de réception, et n'y revient pas après une nouvelle synchronisation.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)

				// "Supprimer" on the row of the second mail, which sends
				// "delete" (Inbox.js, directAction).
				moi.act(uids[1], "delete")
				// In the trash, not lost: a screen that only hid the mail
				// would pass everything below.
				moi.expectOnServer(serverTrash, shopOrder.Subject)

				moi.expectDoesNotShow(shopOrder.Subject, "tu viens de le supprimer")
				// The control: the mail nobody touched is still there.
				moi.expectShows(uids[0], bankStatement.Subject)

				moi.sync()
				moi.expectDoesNotShow(shopOrder.Subject, "tu l'as supprimé avant cette synchronisation")
				moi.expectShows(uids[0], bankStatement.Subject)
			},
		},
		{
			phrase: "Sur une boîte IMAP, un expéditeur dont tu as rangé tous les mails n'apparaît plus dans l'onglet Expéditeurs, même après une synchronisation.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				const shop, bank = "info@boutique.com", "info@banque.fr"
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder, shopInvoice)

				// Where the sentence starts: the tab shows the shop, with
				// its two mails. A sender never shown proves nothing once
				// gone.
				if card, ok := moi.senderCardOf(shop); !ok || card.EmailCount != 2 {
					app.fatalf("après la synchronisation, l'onglet Expéditeurs de %s devrait montrer %s avec ses 2 mails, il montre : %s. Le scénario ne prouve rien", moi.email, shop, describeSenders(moi.sendersTab()))
				}

				// Both ways a row's button tidies a mail: "Archiver" on the
				// first, "Supprimer" on the second.
				moi.act(uids[1], "archive")
				moi.act(uids[2], "delete")
				moi.requireOnServer(serverArchive, shopOrder.Subject, "tu viens de l'archiver")
				moi.requireOnServer(serverTrash, shopInvoice.Subject, "tu viens de le supprimer")

				moi.expectSenderNotListed(shop, "tu viens d'archiver et de supprimer ses 2 mails")
				moi.sync()
				moi.expectSenderNotListed(shop, "tu as archivé et supprimé ses 2 mails avant cette synchronisation")
				// The control: the sender nobody touched is still there.
				// Without it, a tab emptied of everything would pass.
				moi.expectSenderListed(bank)
			},
		},
		{
			phrase: "Sur une boîte IMAP, un mail qu'un autre logiciel a sorti de ta boîte de réception n'y apparaît plus après la synchronisation suivante, et les autres mails y restent.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder, travelOffer)

				// The two ways a mail leaves an inbox from another mail
				// program: archived (moved to another folder), and deleted
				// for good (expunged, in no folder at all).
				moi.movedElsewhere(uids[1], serverArchive)
				moi.deletedElsewhere(uids[2])

				moi.sync()

				moi.expectDoesNotShow(shopOrder.Subject, "un autre logiciel l'a archivé avant cette synchronisation")
				moi.expectDoesNotShow(travelOffer.Subject, "un autre logiciel l'a supprimé définitivement avant cette synchronisation")
				moi.expectShows(uids[0], bankStatement.Subject)
			},
		},
	})
}
