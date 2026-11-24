//go:build acceptance

package api

// What happens when you act on a selection of mails at once, on a mailbox
// reached over IMAP: the action bar of the inbox (runBulk in Inbox.js), and
// the "Annuler" that follows it.
//
// The same sentences as scenarios_2_selection_test.go, for the Gmail API,
// read where an IMAP mailbox keeps its mail: archiving MOVES a mail into the
// Archive folder of the server, deleting moves it into the Trash folder,
// marking read sets its \Seen flag. Each mailbox is a real IMAP server over
// TLS, and what the server holds is read the way another mail program reads
// it (acceptance_imap_test.go). The inbox screen is read too: it is served
// from the copy Mailsorter keeps (mirror.go), and must say the same as the
// server, without waiting for a sync.

import (
	"net/http"
	"strings"
	"testing"
)

// bankAlert is a second mail from the bank of bankStatement, from another of
// its addresses: the sentence about protected senders protects both, and
// puts one in each selection.
var bankAlert = incoming{From: "Ma Banque <alertes@banque.fr>", Subject: "Alerte sur votre compte"}

func TestScenarios_IMAPSelection(t *testing.T) {
	playScenarios(t, "Quand tu agis sur une sélection de mails, sur une boîte IMAP", []scenario{
		{
			phrase: "Sur une boîte IMAP, archiver une sélection de mails les range tous sur le serveur, et « Annuler » juste après les remet tous dans ta boîte de réception.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder, travelOffer)
				selected := []incoming{shopOrder, travelOffer}

				// The last two rows selected, then "Archiver" in the action
				// bar. The first mail is not selected: it is the control.
				done := moi.actOnSelection("archive", uids[1], uids[2])
				moi.expectDoneInSelection(done, "l'archivage de la sélection", uids[1], uids[2])
				for _, m := range selected {
					moi.expectOnServer(serverArchive, m.Subject)
					moi.expectNotOnServer(serverInbox, m.Subject, "tu viens d'archiver la sélection")
					moi.expectDoesNotShow(m.Subject, "tu viens d'archiver la sélection")
				}
				moi.expectShows(uids[0], bankStatement.Subject)

				// The screen offers "Annuler" only when the answer says the
				// action can be undone (runBulk in Inbox.js).
				if done["reversible"] != true {
					app.failf("après l'archivage de la sélection, l'écran de %s devrait proposer « Annuler » : la réponse dit reversible=%v, le bouton n'est donc pas proposé", moi.email, done["reversible"])
				}
				// "Annuler" sends back the numbers the action gave as done.
				if got := moi.undoSelection("archive", doneInSelection(done)...); got.code != http.StatusOK {
					app.failf("« Annuler » juste après l'archivage de la sélection des mails n°%s devrait répondre 200, le serveur a répondu %s", strings.Join(uids[1:], ", n°"), got)
				}

				// What the screen shows right after: Inbox.js reloads the
				// listing once the undo answers, without a sync.
				for _, m := range selected {
					moi.expectBackInInbox(m.Subject, "juste après « Annuler » sur l'archivage de la sélection")
					moi.expectOnServer(serverInbox, m.Subject)
					moi.expectNotOnServer(serverArchive, m.Subject, "« Annuler » l'a remis dans la boîte de réception")
				}
				moi.expectShows(uids[0], bankStatement.Subject)
			},
		},
		{
			phrase: "Sur une boîte IMAP, supprimer une sélection de mails les met tous à la corbeille de ta boîte.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder, travelOffer)

				// "Supprimer" in the action bar sends "delete" (runBulk in
				// Inbox.js). The first mail is not selected: the control.
				done := moi.actOnSelection("delete", uids[1], uids[2])
				moi.expectDoneInSelection(done, "la suppression de la sélection", uids[1], uids[2])
				for _, m := range []incoming{shopOrder, travelOffer} {
					moi.expectOnServer(serverTrash, m.Subject)
					moi.expectNotOnServer(serverInbox, m.Subject, "tu viens de supprimer la sélection")
					moi.expectDoesNotShow(m.Subject, "tu viens de supprimer la sélection")
				}
				moi.expectShows(uids[0], bankStatement.Subject)
				moi.expectNotOnServer(serverTrash, bankStatement.Subject, "il n'était pas sélectionné")
			},
		},
		{
			phrase: "Sur une boîte IMAP, marquer une sélection comme lue la marque lue sur le serveur et dans ta boîte.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder, travelOffer)
				mails := []incoming{bankStatement, shopOrder, travelOffer}

				// Where the sentence starts: the three mails arrived unread,
				// on the server and on the screen.
				for i, uid := range uids {
					if row, ok := moi.shownRow(uid); !ok || row.IsRead {
						app.fatalf("après la synchronisation, la boîte de réception de %s devrait montrer non lu le mail n°%s %q (affiché : %v, lu : %v) : le scénario ne prouve rien", moi.email, uid, mails[i].Subject, ok, row.IsRead)
					}
				}

				// "Marquer comme lu" in the action bar sends "read". The
				// first mail is not selected: the control.
				done := moi.actOnSelection("read", uids[1], uids[2])
				moi.expectDoneInSelection(done, "le marquage comme lu de la sélection", uids[1], uids[2])

				for i := 1; i < len(uids); i++ {
					if !moi.seenOnServer(serverInbox, mails[i].Subject) {
						app.failf("le mail %q devrait être lu sur le serveur IMAP de %s, que tu viens de marquer lu avec la sélection, il ne l'est pas", mails[i].Subject, moi.email)
					}
					// What the inbox loads next, with no "Synchroniser"
					// between.
					switch row, ok := moi.shownRow(uids[i]); {
					case !ok:
						app.failf("la boîte de réception de %s devrait montrer lu le mail n°%s %q, que tu viens de marquer lu avec la sélection, elle ne le montre plus", moi.email, uids[i], mails[i].Subject)
					case !row.IsRead:
						app.failf("la boîte de réception de %s devrait montrer lu le mail n°%s %q, que tu viens de marquer lu avec la sélection, elle le montre encore non lu", moi.email, uids[i], mails[i].Subject)
					}
				}
				// The control: a server or a screen marking every mail read
				// would pass the lines above.
				if moi.seenOnServer(serverInbox, bankStatement.Subject) {
					app.failf("le mail %q ne devrait pas être lu sur le serveur IMAP de %s : il n'était pas sélectionné", bankStatement.Subject, moi.email)
				}
				if row, ok := moi.shownRow(uids[0]); !ok || row.IsRead {
					app.failf("la boîte de réception de %s devrait montrer toujours non lu le mail n°%s %q, qui n'était pas sélectionné (affiché : %v, lu : %v)", moi.email, uids[0], bankStatement.Subject, ok, row.IsRead)
				}
			},
		},
		{
			phrase: "Sur une boîte IMAP, quand tu archives ou supprimes une sélection, les mails des expéditeurs protégés ne bougent pas ; les autres, si.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder, bankAlert, travelOffer)
				moi.protect(bankStatement.From)
				moi.protect(bankAlert.From)

				// One protected mail and one other in each selection.
				archived := moi.actOnSelection("archive", uids[0], uids[1])
				deleted := moi.actOnSelection("delete", uids[2], uids[3])

				for i, m := range []incoming{bankStatement, bankAlert} {
					uid := []string{uids[0], uids[2]}[i]
					moi.expectOnServer(serverInbox, m.Subject)
					moi.expectNotOnServer(serverArchive, m.Subject, "son expéditeur est protégé")
					moi.expectNotOnServer(serverTrash, m.Subject, "son expéditeur est protégé")
					moi.expectShows(uid, m.Subject)
				}
				moi.expectOnServer(serverArchive, shopOrder.Subject)
				moi.expectNotOnServer(serverInbox, shopOrder.Subject, "tu viens de l'archiver avec la sélection")
				moi.expectDoesNotShow(shopOrder.Subject, "tu viens de l'archiver avec la sélection")
				moi.expectOnServer(serverTrash, travelOffer.Subject)
				moi.expectNotOnServer(serverInbox, travelOffer.Subject, "tu viens de le supprimer avec la sélection")
				moi.expectDoesNotShow(travelOffer.Subject, "tu viens de le supprimer avec la sélection")

				// What the screen announces: "1 protégé ignoré" (runBulk in
				// Inbox.js).
				if n := app.number(archived, "protectedSkipped"); n != 1 {
					app.failf("l'archivage de la sélection devrait annoncer 1 mail protégé épargné, il en annonce %d", n)
				}
				if n := app.number(deleted, "protectedSkipped"); n != 1 {
					app.failf("la suppression de la sélection devrait annoncer 1 mail protégé épargné, elle en annonce %d", n)
				}
			},
		},
	})
}
