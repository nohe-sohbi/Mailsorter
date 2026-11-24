//go:build acceptance

package api

// "Annuler" on a mailbox reached over IMAP.
//
// Over the Gmail API, undoing an archive puts a label back on a message that
// never moved. Over IMAP the archive MOVED the mail into another folder, where
// the server numbers it anew. Yet every "Annuler" of the app sends back what
// it held before the action: the message after a row's action and the one
// after "Tout ranger" send the numbers the mails had in the inbox, the
// Historique screen sends the line written when the mail left it. These
// sentences say what each one gives back, read on the inbox screen and on the
// server itself, as another mail program sees it (acceptance_imap_test.go).

import (
	"net/http"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
)

// notWhereFiled is what "Annuler" answers when the mail is no longer in the
// folder Mailsorter moved it to.
const notWhereFiled = "Ce mail n'est plus là où Mailsorter l'avait rangé."

// ownFolder is a folder the person makes on their phone to file mail in.
const ownFolder = "Commandes"

func TestScenarios_IMAPUndo(t *testing.T) {
	playScenarios(t, "Annuler sur une boîte IMAP", []scenario{
		{
			phrase: "Sur une boîte IMAP, « Annuler » juste après avoir archivé un mail le remet tout de suite dans ta boîte de réception, et il n'est plus dans les archives.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)

				// "Archiver" on the row of the second mail, then "Annuler" in
				// the message that follows, on the number the row had.
				moi.act(uids[1], "archive")
				moi.requireOnServer(serverArchive, shopOrder.Subject, "tu viens de l'archiver")

				if got := moi.undoRightAfter(uids[1], "archive"); got.code != http.StatusOK {
					app.failf("« Annuler » juste après l'archivage du mail n°%s %q devrait répondre 200, le serveur a répondu %s", uids[1], shopOrder.Subject, got)
				}

				// What the screen shows right after: Inbox.js reloads the
				// listing once the undo answers, without a sync.
				moi.expectBackInInbox(shopOrder.Subject, "juste après « Annuler »")
				// Back in the inbox of the server, and out of the archives:
				// a screen that only showed the row again would pass the
				// line above.
				moi.expectOnServer(serverInbox, shopOrder.Subject)
				moi.expectNotOnServer(serverArchive, shopOrder.Subject, "« Annuler » l'a remis dans la boîte de réception")
				// The control: the mail nobody touched is still there, under
				// its number.
				moi.expectShows(uids[0], bankStatement.Subject)
			},
		},
		{
			phrase: "Sur une boîte IMAP, « Annuler » juste après avoir supprimé un mail le remet tout de suite dans ta boîte de réception, et il n'est plus dans la corbeille.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)

				// "Supprimer" on the row of the second mail, which sends
				// "delete" (Inbox.js, directAction), then "Annuler".
				moi.act(uids[1], "delete")
				moi.requireOnServer(serverTrash, shopOrder.Subject, "tu viens de le supprimer")

				if got := moi.undoRightAfter(uids[1], "delete"); got.code != http.StatusOK {
					app.failf("« Annuler » juste après la suppression du mail n°%s %q devrait répondre 200, le serveur a répondu %s", uids[1], shopOrder.Subject, got)
				}

				moi.expectBackInInbox(shopOrder.Subject, "juste après « Annuler »")
				moi.expectOnServer(serverInbox, shopOrder.Subject)
				moi.expectNotOnServer(serverTrash, shopOrder.Subject, "« Annuler » l'a remis dans la boîte de réception")
				// The control: the mail nobody touched is still there.
				moi.expectShows(uids[0], bankStatement.Subject)
			},
		},
		{
			phrase: "Sur une boîte IMAP, « Annuler » juste après avoir archivé un mail marque l'archivage annulé dans l'historique, qui ne propose plus de l'annuler.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)

				moi.act(uids[1], "archive")
				moi.requireOnServer(serverArchive, shopOrder.Subject, "tu viens de l'archiver")
				// Where the sentence starts: the history offers to undo the
				// archive. A line never offered proves nothing once undone.
				line := moi.historyLineFor(uids[1], "archive")
				what := "de l'archivage du mail n°" + uids[1] + " \"" + shopOrder.Subject + "\""
				if line.Undone || !line.Undoable {
					app.fatalf("juste après l'archivage, l'historique de %s devrait proposer « Annuler » sur la ligne %s : elle est marquée annulée=%v, annulable=%v, le scénario ne prouve rien", moi.email, what, line.Undone, line.Undoable)
				}

				// "Annuler" in the message that follows the row's "Archiver".
				if got := moi.undoRightAfter(uids[1], "archive"); got.code != http.StatusOK {
					app.fatalf("« Annuler » juste après l'archivage du mail n°%s %q devrait répondre 200, le serveur a répondu %s : le scénario ne prouve rien", uids[1], shopOrder.Subject, got)
				}

				// The Historique screen, opened next: the same line.
				moi.expectNoLongerUndoable(line, what)
			},
		},
		{
			phrase: "Sur une boîte IMAP, « Annuler » après « Tout ranger » remet dans ta boîte de réception les mails rangés.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder, travelOffer)

				// Two verdicts, so "Annuler" sends one request per verdict as
				// the plan does. The first mail is not analyzed: it is the
				// control.
				app.ai.willSay("archive", 0.95)
				moi.analyze(uids[1])
				app.ai.willSay("delete", 0.95)
				moi.analyze(uids[2])
				archive := moi.proposalFor(uids[1])
				trash := moi.proposalFor(uids[2])
				if archive.Action != "archive" || trash.Action != "delete" {
					app.fatalf("le plan de l'IA devrait proposer d'archiver le mail n°%s %q et de supprimer le mail n°%s %q, il propose %q et %q : le scénario ne prouve rien", uids[1], shopOrder.Subject, uids[2], travelOffer.Subject, archive.Action, trash.Action)
				}

				got := moi.tidyAll(archive.ID, trash.ID)
				if got.code != http.StatusOK || app.number(got.body, "applied") != 2 {
					app.fatalf("« Tout ranger » devrait ranger les 2 mails proposés, le serveur a répondu %s : le scénario ne prouve rien", got)
				}
				moi.requireOnServer(serverArchive, shopOrder.Subject, "« Tout ranger » vient de l'archiver")
				moi.requireOnServer(serverTrash, travelOffer.Subject, "« Tout ranger » vient de le supprimer")

				// The plan offers "Annuler" only when the answer says the
				// tidying can be undone (TriagePlan.js).
				if got.body["reversible"] != true {
					app.failf("après « Tout ranger », l'écran devrait proposer « Annuler » : la réponse dit reversible=%v, le bouton n'est donc pas proposé", got.body["reversible"])
				}
				for _, u := range moi.undoTidy([]models.AISuggestion{archive, trash}) {
					if u.got.code != http.StatusOK {
						app.failf("« Annuler » après « Tout ranger » devrait répondre 200 pour les mails n°%v rangés par %q, le serveur a répondu %s", u.uids, u.action, u.got)
					}
				}

				// What the screen shows right after: the plan reloads the
				// listing once the undo answers, without a sync.
				moi.expectBackInInbox(shopOrder.Subject, "juste après « Annuler » sur « Tout ranger »")
				moi.expectBackInInbox(travelOffer.Subject, "juste après « Annuler » sur « Tout ranger »")
				moi.expectOnServer(serverInbox, shopOrder.Subject)
				moi.expectOnServer(serverInbox, travelOffer.Subject)
				moi.expectNotOnServer(serverArchive, shopOrder.Subject, "« Annuler » l'a remis dans la boîte de réception")
				moi.expectNotOnServer(serverTrash, travelOffer.Subject, "« Annuler » l'a remis dans la boîte de réception")
				// The control: the mail the AI never saw is still there.
				moi.expectShows(uids[0], bankStatement.Subject)
			},
		},
		{
			phrase: "Sur une boîte IMAP, « Annuler » dans l'historique remet dans ta boîte de réception un mail archivé, même après une synchronisation, et la ligne de l'historique est marquée annulée.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)

				moi.act(uids[1], "archive")
				moi.sync()
				// Where the sentence starts: the sync has dropped the mail
				// from the copy of the inbox, so only the history still
				// knows it.
				if moi.shows(shopOrder.Subject) {
					app.fatalf("après l'archivage puis une synchronisation, la boîte de réception de %s ne devrait plus montrer le mail %q : le scénario ne prouve rien", moi.email, shopOrder.Subject)
				}
				moi.requireOnServer(serverArchive, shopOrder.Subject, "tu l'as archivé")

				line := moi.historyLineFor(uids[1], "archive")
				what := "de l'archivage du mail n°" + uids[1] + " \"" + shopOrder.Subject + "\""
				if got := moi.undoFromHistory(line); got.code != http.StatusOK {
					app.failf("« Annuler » sur la ligne %s devrait répondre 200, le serveur a répondu %s", what, got)
				}

				moi.expectOnServer(serverInbox, shopOrder.Subject)
				moi.expectNotOnServer(serverArchive, shopOrder.Subject, "« Annuler » dans l'historique l'a remis dans la boîte de réception")
				moi.expectMarkedUndone(line, true, what)
				// And the inbox screen, once it has read the server again.
				moi.sync()
				moi.expectBackInInbox(shopOrder.Subject, "après « Annuler » dans l'historique et une synchronisation")
				// The control: the mail nobody touched is still there.
				moi.expectShows(uids[0], bankStatement.Subject)
			},
		},
		{
			phrase: "Sur une boîte IMAP, quand un autre logiciel a déjà sorti un mail de là où Mailsorter l'avait rangé, « Annuler » répond « Ce mail n'est plus là où Mailsorter l'avait rangé. » et l'historique ne le marque pas annulé.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder, travelOffer)

				moi.act(uids[1], "archive")
				moi.act(uids[2], "archive")
				moi.requireOnServer(serverArchive, shopOrder.Subject, "tu viens de l'archiver")
				moi.requireOnServer(serverArchive, travelOffer.Subject, "tu viens de l'archiver")

				// The two ways a mail leaves a folder from another mail
				// program: filed elsewhere, and deleted for good.
				moi.newFolderElsewhere(ownFolder)
				moi.movedFromElsewhere(serverArchive, shopOrder.Subject, ownFolder)
				moi.erasedFromElsewhere(serverArchive, travelOffer.Subject)

				for _, c := range []struct {
					uid, subject, how string
				}{
					{uids[1], shopOrder.Subject, "déplacé de " + serverArchive + " vers " + ownFolder},
					{uids[2], travelOffer.Subject, "supprimé définitivement de " + serverArchive},
				} {
					line := moi.historyLineFor(c.uid, "archive")
					what := "de l'archivage du mail n°" + c.uid + " \"" + c.subject + "\""
					got := moi.undoFromHistory(line)
					if got.code != http.StatusNotFound || got.said() != notWhereFiled {
						app.failf("« Annuler » sur la ligne %s, qu'un autre logiciel a %s, devrait répondre %d %q, le serveur a répondu %s", what, c.how, http.StatusNotFound, notWhereFiled, got)
					}
					moi.expectMarkedUndone(line, false, what)
				}

				// The control: the mail nobody touched is still there.
				moi.expectShows(uids[0], bankStatement.Subject)
			},
		},
	})
}
