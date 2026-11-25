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
// the Expéditeurs tab, which counts the mails of each sender, another. A sync
// reads only the 100 most recent mails of the inbox (imapListLimit), and the
// copy must lose an older one all the same, and every one of them when the
// inbox has been emptied.
//
// What the person changes from Mailsorter shows at once, without a sync: a
// mail marked "non lu", and the counters above the inbox once a mail is
// archived.
//
// The second theme of this file, "Favoris et filtres", is what the copy must
// hold besides the subject and the sender for the filters above the inbox to
// answer from it: the star, the size, the attachments, and a read or a star
// changed on another mail program, older mails included.

import (
	"fmt"
	"net/http"
	"strings"
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
		{
			phrase: "Sur une boîte IMAP qu'un autre logiciel a vidée, ta boîte de réception dans Mailsorter est vide après la synchronisation suivante.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder, travelOffer)

				// Another mail program empties the inbox, every way a mail
				// leaves it: archived, put in the trash, deleted for good.
				moi.movedElsewhere(uids[0], serverArchive)
				moi.movedElsewhere(uids[1], serverTrash)
				moi.deletedElsewhere(uids[2])
				// Where the sentence starts: nothing left in the inbox of
				// the server. An inbox still holding a mail proves nothing
				// about an empty one.
				if left := moi.onServer(serverInbox); len(left) > 0 {
					app.fatalf("la boîte de réception du serveur IMAP de %s devrait être vide, elle contient encore %q : le scénario ne prouve rien", moi.email, left)
				}

				moi.sync()

				// An empty listing is the case a sync could take for "nothing
				// read" and leave the copy as it was: the whole screen, every
				// page, must be empty.
				if mails := moi.wholeInbox(); len(mails) > 0 {
					app.failf("après la synchronisation, la boîte de réception de %s devrait être vide, comme celle du serveur qu'un autre logiciel a vidée : elle montre %s", moi.email, describeInbox(mails))
				}
			},
		},
		{
			phrase: "Sur une boîte IMAP de plus de 100 mails, un ancien mail qu'un autre logiciel a sorti de ta boîte de réception n'y apparaît plus après la synchronisation suivante, et les autres mails y restent.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi := app.imapPerson(imapInboxOwner)
				// 100 letters, synced, then 10 more, synced: 110 mails on the
				// screen, more than one sync reads (the 100 most recent).
				letters := numberedMails("Lettre n°%d", 100)
				uids := moi.receive(letters...)
				moi.sync()
				later := numberedMails("Nouvelle lettre n°%d", 10)
				moi.receive(later...)
				moi.sync()
				all := append(append([]incoming{}, letters...), later...)

				// Where the sentence starts: every one of the 110 on the
				// screen, scrolled to the end. An old mail never shown proves
				// nothing once gone.
				shown := map[string]bool{}
				for _, m := range moi.wholeInbox() {
					shown[m.Subject] = true
				}
				for _, m := range all {
					if !shown[m.Subject] {
						app.fatalf("après les deux synchronisations, la boîte de réception de %s devrait montrer le mail %q parmi ses %d mails, elle ne le montre pas : le scénario ne prouve rien", moi.email, m.Subject, len(all))
					}
				}

				// The phone takes two old ones out of the inbox, among the 10
				// a sync no longer reads: the two ways, archived (moved to
				// another folder) and deleted for good (expunged).
				archived, deleted := letters[2], letters[4]
				moi.movedElsewhere(uids[2], serverArchive)
				moi.deletedElsewhere(uids[4])

				moi.sync()

				// The whole screen, every page: the first alone does not
				// reach the oldest mails.
				shown = map[string]bool{}
				for _, m := range moi.wholeInbox() {
					shown[m.Subject] = true
				}
				if shown[archived.Subject] {
					app.failf("la boîte de réception de %s ne devrait plus montrer le mail n°%s %q, qu'un autre logiciel a archivé avant cette synchronisation, elle le montre encore", moi.email, uids[2], archived.Subject)
				}
				if shown[deleted.Subject] {
					app.failf("la boîte de réception de %s ne devrait plus montrer le mail n°%s %q, qu'un autre logiciel a supprimé définitivement avant cette synchronisation, elle le montre encore", moi.email, uids[4], deleted.Subject)
				}
				// The others stay, the old ones included: a sync that drops
				// every mail it does not read would pass the lines above.
				var missing []string
				for _, m := range all {
					if m.Subject != archived.Subject && m.Subject != deleted.Subject && !shown[m.Subject] {
						missing = append(missing, fmt.Sprintf("%q", m.Subject))
					}
				}
				if len(missing) > 0 {
					app.failf("après la synchronisation, la boîte de réception de %s devrait montrer encore les %d autres mails, il en manque %d : %s", moi.email, len(all)-2, len(missing), strings.Join(missing, ", "))
				}
			},
		},
		{
			phrase: "Sur une boîte IMAP, un mail que tu marques non lu apparaît non lu dans ta boîte de réception tout de suite, sans attendre de synchronisation.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)

				// Both mails read in the reader, which marks them read. The
				// first stays so: it is the control.
				moi.openMail(uids[0])
				moi.openMail(uids[1])
				for i, uid := range uids {
					if row, ok := moi.shownRow(uid); !ok || !row.IsRead {
						app.fatalf("après l'avoir ouvert dans la liseuse, la boîte de réception de %s devrait montrer lu le mail n°%s %q (affiché : %v, lu : %v) : le scénario ne prouve rien", moi.email, uid, []incoming{bankStatement, shopOrder}[i].Subject, ok, row.IsRead)
					}
				}

				// "Non lu" in the reader on the second mail: over IMAP, the
				// single-message action with "unread" (flagAction in
				// Inbox.js). act expects it to answer 200.
				moi.act(uids[1], "unread")

				// What the inbox loads next, with no "Synchroniser" between.
				switch row, ok := moi.shownRow(uids[1]); {
				case !ok:
					app.failf("la boîte de réception de %s devrait montrer non lu le mail n°%s %q, que tu viens de marquer non lu, elle ne le montre plus", moi.email, uids[1], shopOrder.Subject)
				case row.IsRead:
					app.failf("la boîte de réception de %s devrait montrer non lu le mail n°%s %q, que tu viens de marquer non lu, elle le montre encore lu", moi.email, uids[1], shopOrder.Subject)
				}
				// The control: a listing showing every mail unread would pass
				// the lines above.
				if row, ok := moi.shownRow(uids[0]); !ok || !row.IsRead {
					app.failf("la boîte de réception de %s devrait montrer toujours lu le mail n°%s %q, que tu as lu et pas touché depuis (affiché : %v, lu : %v)", moi.email, uids[0], bankStatement.Subject, ok, row.IsRead)
				}
			},
		},
		{
			phrase: "Sur une boîte IMAP, juste après avoir archivé un mail, les compteurs de ta boîte ne le comptent plus, sans attendre de synchronisation.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)

				// Where the sentence starts: the counters count both mails,
				// unread as they arrived. A mail never counted proves nothing
				// once it is no longer.
				if inbox, unread := moi.counters(); inbox != 2 || unread != 2 {
					app.fatalf("après la synchronisation, les compteurs de la boîte de %s devraient montrer 2 mails dans la boîte de réception, 2 non lus : ils montrent %d et %d, le scénario ne prouve rien", moi.email, inbox, unread)
				}

				// "Archiver" on the row of the second mail.
				moi.act(uids[1], "archive")
				moi.requireOnServer(serverArchive, shopOrder.Subject, "tu viens de l'archiver")

				// The counters as the inbox loads them next, with no
				// "Synchroniser" between. 1, not 0: the mail nobody touched is
				// the control, so counters that drop to nothing do not pass.
				inbox, unread := moi.counters()
				if inbox != 1 {
					app.failf("juste après l'archivage du mail n°%s %q, le compteur « Boîte de réception » de %s devrait montrer 1 mail, il en montre %d", uids[1], shopOrder.Subject, moi.email, inbox)
				}
				if unread != 1 {
					app.failf("juste après l'archivage du mail n°%s %q, non lu, le compteur « Non lus » de %s devrait montrer 1 mail, il en montre %d", uids[1], shopOrder.Subject, moi.email, unread)
				}
			},
		},
		{
			phrase: "Sur une boîte IMAP, changer le mot de passe d'application garde la boîte telle quelle : un mot de passe refusé par le fournisseur n'est pas enregistré, et avec le bon, la synchronisation repart et « Annuler » marche encore sur ce qui avait été rangé avant.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder, travelOffer)

				// Something ranged before: the third mail archived, with its
				// line in the history.
				moi.act(uids[2], "archive")
				moi.requireOnServer(serverArchive, travelOffer.Subject, "il vient d'être archivé")
				line := moi.historyLineFor(uids[2], "archive")

				// A password the provider refuses, typed in "Changer le mot
				// de passe d'application": refused, in words that do not
				// sign the person out of Mailsorter (401 marked
				// mailboxRejected, api.js), and not stored.
				const wrong = "pas-le-bon-mot-de-passe"
				stored := moi.storedAppPassword()
				refused := func(when string) {
					app.t.Helper()
					got := moi.saveAppPassword(wrong)
					if got.code != http.StatusUnauthorized || got.body["mailboxRejected"] != true {
						app.failf("%s, enregistrer un mot de passe d'application que le fournisseur refuse devrait être refusé pour %s (401 marqué mailboxRejected, qui ne déconnecte pas de Mailsorter), le serveur a répondu %s", when, moi.email, got)
					}
					if now := moi.storedAppPassword(); now != stored {
						app.failf("%s, un mot de passe d'application que le fournisseur refuse ne devrait pas être enregistré pour %s, il l'a été", when, moi.email)
					}
				}
				// While the stored one still works: the mailbox goes on
				// syncing with it, which it could not with the wrong one.
				refused("tant que celui enregistré marche encore")
				if code, out := moi.syncAnswer(); code != http.StatusOK {
					app.failf("après un mot de passe d'application refusé, « Synchroniser » devrait marcher encore pour %s avec celui enregistré, il a répondu %d : %v", moi.email, code, out)
				}

				// The person makes a new app password on the provider's
				// side: the stored one is refused, and "Synchroniser" says
				// so, in the words that lead to the form (passwordRefused).
				const fresh = "nouveau-mot-de-passe-d-application"
				moi.providerReplacesAppPassword(fresh)
				if code, out := moi.syncAnswer(); code != http.StatusPreconditionRequired || out["passwordRefused"] != true {
					app.fatalf("« Synchroniser » avec un mot de passe d'application que le fournisseur refuse devrait répondre 428 marqué passwordRefused pour %s, il a répondu %d : %v : le scénario ne prouve rien", moi.email, code, out)
				}
				refused("une fois celui enregistré refusé par le fournisseur")

				// The right one.
				if got := moi.saveAppPassword(fresh); got.code != http.StatusOK {
					app.fatalf("enregistrer le nouveau mot de passe d'application de %s, que le fournisseur accepte, devrait répondre 200, le serveur a répondu %s", moi.email, got)
				}
				if code, out := moi.syncAnswer(); code != http.StatusOK {
					app.failf("avec le nouveau mot de passe d'application, « Synchroniser » devrait repartir pour %s, il a répondu %d : %v", moi.email, code, out)
				}
				moi.expectShows(uids[0], bankStatement.Subject)
				moi.expectShows(uids[1], shopOrder.Subject)

				// "Annuler" on what was ranged before.
				if got := moi.undoFromHistory(line); got.code != http.StatusOK {
					app.failf("avec le nouveau mot de passe d'application, « Annuler » sur l'archivage d'avant du mail n°%s %q de %s devrait répondre 200, le serveur a répondu %s", uids[2], travelOffer.Subject, moi.email, got)
				}
				moi.expectBackInInbox(travelOffer.Subject, "après « Annuler » sur l'archivage d'avant le nouveau mot de passe")
				moi.expectOnServer(serverInbox, travelOffer.Subject)
				moi.expectNotOnServer(serverArchive, travelOffer.Subject, "« Annuler » l'a remis dans la boîte de réception")
			},
		},
	})
}

// numberedMails is n mails from one sender, their subjects numbered from 1 in
// format, oldest first: a mailbox fuller than one sync reads, or enough mails
// to try one sentence on each.
func numberedMails(format string, n int) []incoming {
	mails := make([]incoming, 0, n)
	for i := 1; i <= n; i++ {
		mails = append(mails, incoming{From: "Le Journal <lettre@journal.fr>", Subject: fmt.Sprintf(format, i)})
	}
	return mails
}

// ---------------------------------------------------------------------------
// Favoris et filtres
//
// The filters above the inbox ask the listing a question in Gmail's language
// (QUICK_FILTERS in Inbox.js): "Favoris" is:starred, "Pièces jointes"
// has:attachment, "Volumineux" larger:5M. Over IMAP the listing is served
// from the copy of the inbox the sync writes (mirror.go), so a filter can
// only answer what that copy holds: the star (\Flagged on the server), the
// size, whether a mail carries a file. And a star or a read set on another
// mail program reaches the copy only through a sync, which lists the 100 most
// recent mails (imapListLimit): an older mail must follow all the same.
// ---------------------------------------------------------------------------

// The mails of the sentences about the filters "Pièces jointes" and
// "Volumineux". A real attachment is a part of a multipart/mixed mail with
// Content-Disposition: attachment (writeMailContent in
// acceptance_imap_test.go).
var (
	// invoiceWithPDF is a shop's invoice, in text and HTML, with the invoice
	// attached: a small mail with an attachment.
	invoiceWithPDF = incoming{From: "Boutique <info@boutique.com>", Subject: "Votre facture en pièce jointe",
		HTML:       "<p>Bonjour, vous trouverez votre facture en pièce jointe.</p>",
		Attachment: &attachedFile{Name: "facture.pdf", Type: "application/pdf", Size: 40_000}}
	// weeklyLetter is a newsletter in text and HTML, with nothing attached:
	// multipart, yet no attachment.
	weeklyLetter = incoming{From: "Le Journal <lettre@journal.fr>", Subject: "La lettre de la semaine",
		HTML: "<h1>La lettre de la semaine</h1><p>Les nouvelles de la semaine.</p>"}
	// contractWithPDF is a mail with an attachment the scenario archives.
	contractWithPDF = incoming{From: "Assurance <contrat@assurance.fr>", Subject: "Votre contrat signé",
		Attachment: &attachedFile{Name: "contrat.pdf", Type: "application/pdf", Size: 60_000}}
	// flatPlans and hdPhotos weigh over 8 MB with their attachment, base64
	// included.
	flatPlans = incoming{From: "Agence immobilière <dossier@agence-immo.fr>", Subject: "Les plans de l'appartement",
		Attachment: &attachedFile{Name: "plans.pdf", Type: "application/pdf", Size: 6 << 20}}
	hdPhotos = incoming{From: "Studio photo <envoi@studio-photo.fr>", Subject: "Vos photos en haute définition",
		Attachment: &attachedFile{Name: "photos.zip", Type: "application/zip", Size: 6 << 20}}
	// townHallForm weighs about 4.1 MB with its attachment, base64 included:
	// under 5 on either reading.
	townHallForm = incoming{From: "Mairie <etat-civil@mairie.fr>", Subject: "Le dossier à compléter",
		Attachment: &attachedFile{Name: "dossier.pdf", Type: "application/pdf", Size: 3_000_000}}
)

// "5 Mo" read either way: 5 000 000 bytes, or 5 x 1024 x 1024. The sentence
// about "Volumineux" holds on both readings, so its mails weigh clear of both:
// over overFiveMB, or under underFiveMB.
const (
	overFiveMB  = 5 << 20
	underFiveMB = 5_000_000
)

func TestScenarios_IMAPFavoritesAndFilters(t *testing.T) {
	playScenarios(t, "Favoris et filtres sur une boîte IMAP", []scenario{
		{
			phrase: "Sur une boîte IMAP, un mail que tu mets en favori apparaît en favori dans ta boîte de réception tout de suite, et il l'est aussi sur le serveur.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)

				// Where the sentence starts: neither row shows the star. A
				// star already there proves nothing once "added".
				for i, uid := range uids {
					if row, _ := moi.shownRow(uid); row.starred() {
						app.fatalf("après la synchronisation, la boîte de réception de %s montre déjà en favori le mail n°%s %q, que personne n'a mis en favori : le scénario ne prouve rien", moi.email, uid, []incoming{bankStatement, shopOrder}[i].Subject)
					}
				}

				// "Mettre en favori" in the reader (or the S key) on the
				// second mail: over IMAP, the single-message action with
				// "star" (flagAction in Inbox.js). act expects it to answer
				// 200.
				moi.act(uids[1], "star")

				// What the inbox loads next, with no "Synchroniser" between.
				switch row, ok := moi.shownRow(uids[1]); {
				case !ok:
					app.failf("la boîte de réception de %s devrait montrer en favori le mail n°%s %q, que tu viens de mettre en favori, elle ne le montre plus", moi.email, uids[1], shopOrder.Subject)
				case !row.starred():
					app.failf("la boîte de réception de %s devrait montrer en favori le mail n°%s %q, que tu viens de mettre en favori, elle le montre sans étoile", moi.email, uids[1], shopOrder.Subject)
				}
				// On the server too, as another mail program shows it: a
				// star kept in Mailsorter alone would pass the lines above.
				if !moi.flaggedOnServer(serverInbox, shopOrder.Subject) {
					app.failf("le mail %q, que tu viens de mettre en favori, devrait l'être aussi sur le serveur IMAP de %s (drapeau \\Flagged), il ne l'est pas", shopOrder.Subject, moi.email)
				}
				// The control: the mail nobody touched shows no star, here or
				// there. Every mail shown starred would pass the lines above.
				if row, ok := moi.shownRow(uids[0]); !ok || row.starred() {
					app.failf("la boîte de réception de %s devrait montrer sans étoile le mail n°%s %q, que personne n'a mis en favori (affiché : %v, en favori : %v)", moi.email, uids[0], bankStatement.Subject, ok, row.starred())
				}
				if moi.flaggedOnServer(serverInbox, bankStatement.Subject) {
					app.failf("le mail %q, que personne n'a mis en favori, ne devrait pas l'être sur le serveur IMAP de %s, il l'est", bankStatement.Subject, moi.email)
				}
			},
		},
		{
			phrase: "Sur une boîte IMAP, le filtre « Favoris » ne montre que les mails en favori de ta boîte de réception, et un mail que tu retires des favoris n'y est plus tout de suite.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder, travelOffer, shopInvoice)

				// Three mails put in the favorites, the last of them then
				// archived: still a favorite, no longer in the inbox. The
				// third mail is never starred.
				moi.act(uids[0], "star")
				moi.act(uids[1], "star")
				moi.act(uids[3], "star")
				moi.act(uids[3], "archive")
				moi.requireOnServer(serverArchive, shopInvoice.Subject, "tu viens de l'archiver")

				moi.expectFilterShows(favoritesFilter, "une fois trois mails mis en favori et l'un d'eux archivé",
					[]string{bankStatement.Subject, shopOrder.Subject},
					map[string]string{
						travelOffer.Subject: "personne ne l'a mis en favori",
						shopInvoice.Subject: "tu l'as archivé, il n'est plus dans ta boîte de réception",
					})

				// "Retirer des favoris" on the second mail, then the filter
				// as it loads again, with no "Synchroniser" between.
				moi.act(uids[1], "unstar")

				moi.expectFilterShows(favoritesFilter, "juste après avoir retiré un mail des favoris",
					[]string{bankStatement.Subject},
					map[string]string{
						shopOrder.Subject:   "tu viens de le retirer des favoris",
						travelOffer.Subject: "personne ne l'a mis en favori",
						shopInvoice.Subject: "tu l'as archivé, il n'est plus dans ta boîte de réception",
					})
			},
		},
		{
			phrase: "Sur une boîte IMAP, un mail mis en favori depuis un autre logiciel apparaît dans le filtre « Favoris » après la synchronisation suivante, même s'il n'est pas parmi les 100 plus récents.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, mails, uids := syncedFullIMAPInbox(app)

				// The phone stars two mails: an old one, among the 10 a sync
				// no longer reads, and a recent one.
				old, recent, untouched := 2, 104, 3
				moi.flaggedElsewhere(uids[old])
				moi.flaggedElsewhere(uids[recent])

				moi.sync()

				moi.expectFilterShows(favoritesFilter, "après la synchronisation qui suit",
					[]string{mails[old].Subject, mails[recent].Subject},
					map[string]string{mails[untouched].Subject: "personne ne l'a mis en favori"})
			},
		},
		{
			phrase: "Sur une boîte IMAP de plus de 100 mails, un ancien mail en favori apparaît dans le filtre « Favoris » dès la première synchronisation, même s'il n'a jamais été parmi les 100 plus récents.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi := app.imapPerson(imapInboxOwner)
				// 150 mails, oldest first: a sync lists the 100 most recent
				// (n°51 to n°150), so the first 50 have never been among them.
				letters := numberedMails("Lettre n°%d", 150)
				uids := moi.receive(letters...)

				// Two old ones already in the favorites before Mailsorter
				// ever looks at the mailbox, starred on the phone, and an old
				// one nobody starred, the control.
				starred1, starred2, untouched := 2, 19, 9
				moi.expectNumbered(uids[starred1], "3")
				moi.expectNumbered(uids[starred2], "20")
				moi.expectNumbered(uids[untouched], "10")
				moi.flaggedElsewhere(uids[starred1])
				moi.flaggedElsewhere(uids[starred2])

				// The very first "Synchroniser", and no other.
				moi.sync()

				moi.expectFilterShows(favoritesFilter, "après la toute première synchronisation",
					[]string{letters[starred1].Subject, letters[starred2].Subject},
					map[string]string{letters[untouched].Subject: "personne ne l'a mis en favori"})
			},
		},
		{
			phrase: "Sur une boîte IMAP, un ancien mail lu depuis un autre logiciel apparaît lu après la synchronisation suivante, même s'il n'est pas parmi les 100 plus récents.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, mails, uids := syncedFullIMAPInbox(app)
				old, recent, untouched := 2, 104, 3

				// Where the sentence starts: those mails shown unread, as they
				// arrived. A mail already shown read proves nothing once read.
				shown := moi.wholeInbox()
				for _, i := range []int{old, recent, untouched} {
					if row, ok := rowBySubject(shown, mails[i].Subject); !ok || row.IsRead {
						app.fatalf("après les deux synchronisations, la boîte de réception de %s devrait montrer non lu le mail n°%s %q (affiché : %v, lu : %v) : le scénario ne prouve rien", moi.email, uids[i], mails[i].Subject, ok, row.IsRead)
					}
				}

				// The phone reads two mails: an old one, among the 10 a sync
				// no longer reads, and a recent one.
				moi.readElsewhere(uids[old])
				moi.readElsewhere(uids[recent])

				moi.sync()

				// The whole screen, every page: the first alone does not
				// reach the oldest mails.
				shown = moi.wholeInbox()
				for _, i := range []int{old, recent} {
					switch row, ok := rowBySubject(shown, mails[i].Subject); {
					case !ok:
						app.failf("après la synchronisation, la boîte de réception de %s devrait montrer lu le mail n°%s %q, qu'un autre logiciel a lu avant cette synchronisation, elle ne le montre plus", moi.email, uids[i], mails[i].Subject)
					case !row.IsRead:
						app.failf("après la synchronisation, la boîte de réception de %s devrait montrer lu le mail n°%s %q, qu'un autre logiciel a lu avant cette synchronisation, elle le montre encore non lu", moi.email, uids[i], mails[i].Subject)
					}
				}
				// The control: an old mail nobody read stays unread. A sync
				// that marks every old mail read would pass the lines above.
				if row, ok := rowBySubject(shown, mails[untouched].Subject); !ok || row.IsRead {
					app.failf("après la synchronisation, la boîte de réception de %s devrait montrer toujours non lu le mail n°%s %q, que personne n'a lu (affiché : %v, lu : %v)", moi.email, uids[untouched], mails[untouched].Subject, ok, row.IsRead)
				}
			},
		},
		{
			phrase: "Sur une boîte IMAP, le filtre « Pièces jointes » ne montre que les mails de ta boîte de réception qui ont une pièce jointe.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, invoiceWithPDF, weeklyLetter, contractWithPDF)

				// The contract leaves the inbox: it still has its
				// attachment, it is no longer in the inbox.
				moi.act(uids[3], "archive")
				moi.requireOnServer(serverArchive, contractWithPDF.Subject, "tu viens de l'archiver")

				moi.expectFilterShows(attachmentsFilter, "une fois ta boîte synchronisée et un des mails archivé",
					[]string{invoiceWithPDF.Subject},
					map[string]string{
						bankStatement.Subject:   "il n'a pas de pièce jointe",
						weeklyLetter.Subject:    "il a une version HTML et aucune pièce jointe",
						contractWithPDF.Subject: "tu l'as archivé, il n'est plus dans ta boîte de réception",
					})
			},
		},
		{
			phrase: "Sur une boîte IMAP, le filtre « Volumineux » ne montre que les mails de ta boîte de réception de plus de 5 Mo.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, invoiceWithPDF, townHallForm, flatPlans, hdPhotos)

				// Where the sentence starts: the size of each mail on the
				// server, as another mail program reads it. A "large" mail
				// under 5 Mo, or a "small" one over, proves nothing.
				for _, m := range []incoming{flatPlans, hdPhotos} {
					if size := moi.sizeOnServer(serverInbox, m.Subject); size <= overFiveMB {
						app.fatalf("le mail %q fait %d octets sur le serveur IMAP de %s, pas plus de 5 Mo : le scénario ne prouve rien", m.Subject, size, moi.email)
					}
				}
				for _, m := range []incoming{bankStatement, invoiceWithPDF, townHallForm} {
					if size := moi.sizeOnServer(serverInbox, m.Subject); size >= underFiveMB {
						app.fatalf("le mail %q fait %d octets sur le serveur IMAP de %s, pas moins de 5 Mo : le scénario ne prouve rien", m.Subject, size, moi.email)
					}
				}

				// The photos leave the inbox: still over 5 Mo, no longer in
				// the inbox.
				moi.act(uids[4], "archive")
				moi.requireOnServer(serverArchive, hdPhotos.Subject, "tu viens de l'archiver")

				moi.expectFilterShows(largeMailFilter, "une fois ta boîte synchronisée et un des mails archivé",
					[]string{flatPlans.Subject},
					map[string]string{
						bankStatement.Subject:  "il fait moins de 5 Mo",
						invoiceWithPDF.Subject: "il a une pièce jointe, mais fait moins de 5 Mo",
						townHallForm.Subject:   "il fait environ 4 Mo, moins de 5 Mo",
						hdPhotos.Subject:       "tu l'as archivé, il n'est plus dans ta boîte de réception",
					})
			},
		},
	})
}

// syncedFullIMAPInbox brings a person to where the sentences about a mailbox
// fuller than one sync reads start: 100 letters, synced, then 10 more,
// synced, and every one of the 110 on the inbox screen, scrolled to the end.
// It returns the person, the mails oldest first and their numbers: the first
// 10 are the ones a sync no longer reads (it reads the 100 most recent).
func syncedFullIMAPInbox(app *testApp) (*imapPerson, []incoming, []string) {
	app.t.Helper()
	moi := app.imapPerson(imapInboxOwner)
	letters := numberedMails("Lettre n°%d", 100)
	uids := moi.receive(letters...)
	moi.sync()
	later := numberedMails("Nouvelle lettre n°%d", 10)
	uids = append(uids, moi.receive(later...)...)
	moi.sync()
	all := append(append([]incoming{}, letters...), later...)

	// An old mail never shown proves nothing about what a sync makes of it.
	shown := moi.wholeInbox()
	for i, m := range all {
		if _, ok := rowBySubject(shown, m.Subject); !ok {
			app.fatalf("après les deux synchronisations, la boîte de réception de %s devrait montrer le mail n°%s %q parmi ses %d mails, elle ne le montre pas : le scénario ne prouve rien", moi.email, uids[i], m.Subject, len(all))
		}
	}
	return moi, all, uids
}

// rowBySubject returns the row of a listing for the mail with this subject,
// and whether the listing has one.
func rowBySubject(mails []shownMail, subject string) (shownMail, bool) {
	for _, m := range mails {
		if m.Subject == subject {
			return m, true
		}
	}
	return shownMail{}, false
}
