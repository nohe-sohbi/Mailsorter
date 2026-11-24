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
//
// The history behind "Annuler" must stay true too: one line for one archive,
// even sent twice at once, one undo for one "Annuler", even sent twice at
// once, none for an archive or a deletion that moved nothing, and no
// "Annuler" where it would give nothing back: on an action older than the 30
// days Mailsorter keeps track of where a mail went (undoWindow), or on a mail
// marked read that has left the inbox since.

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
)

// notWhereFiled is what "Annuler" answers when the mail is no longer in the
// folder Mailsorter moved it to.
const notWhereFiled = "Ce mail n'est plus là où Mailsorter l'avait rangé."

// notInInbox is what "Marquer comme lu" answers when the mail is no longer in
// the inbox.
const notInInbox = "Ce mail n'est plus dans votre boîte de réception."

// tooOldToUndo is what "Annuler" answers on an action older than 30 days.
const tooOldToUndo = "Cette action est trop ancienne pour être annulée ici : retrouvez ce mail dans l'archive ou la corbeille de votre messagerie."

// alreadyOut is the "status" a row's button answers, with a 200, when the
// mail had already left the inbox and nothing was done: what the inbox screen
// tells apart from "ok" to say the mail had already gone, rather than
// announce a move that did not happen.
const alreadyOut = "already"

// ownFolder is a folder the person makes on their phone to file mail in.
const ownFolder = "Commandes"

// newsletter is a fourth mail, for the sentence where "Annuler" puts back
// several mails besides the one another program took away.
var newsletter = incoming{From: "Le Journal <lettre@journal.fr>", Subject: "Les nouvelles du jour"}

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
		{
			phrase: "Sur une boîte IMAP, quand un autre logiciel a remis dans ta boîte de réception un mail que Mailsorter avait archivé, « Annuler » depuis l'historique répond « Ce mail n'est plus là où Mailsorter l'avait rangé. », ne marque pas la ligne annulée, et la ligne ne propose plus « Annuler » après la synchronisation suivante.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)

				moi.act(uids[1], "archive")
				moi.requireOnServer(serverArchive, shopOrder.Subject, "tu viens de l'archiver")
				// The line "Annuler" is pressed on below: the one this archive
				// wrote.
				line := moi.historyLineFor(uids[1], "archive")
				what := "de l'archivage du mail n°" + uids[1] + " \"" + shopOrder.Subject + "\""

				// The person changes their mind on their phone: another mail
				// program moves the mail from the archives back to the inbox,
				// where the server numbers it anew. Then a sync, so Mailsorter
				// has read the inbox since.
				moi.movedFromElsewhere(serverArchive, shopOrder.Subject, serverInbox)
				moi.requireOnServer(serverInbox, shopOrder.Subject, "un autre logiciel l'y a remis")
				moi.sync()

				// "Annuler" on that line. A refusal, as History.js shows one: a
				// 4xx status and its "error" field (apiError in
				// services/api.js). On a 2xx the screen announces the mail
				// restored and shows the line "Annulé" until it loads again.
				got := moi.undoFromHistory(line)
				if got.code/100 != 4 || got.said() != notWhereFiled {
					app.failf("« Annuler » sur la ligne %s, qu'un autre logiciel a remis de %s dans la boîte de réception, devrait répondre %q, le serveur a répondu %s", what, serverArchive, notWhereFiled, got)
				}
				moi.expectMarkedUndone(line, false, what)

				// The next sync, and the Historique screen as it loads after
				// it: the line no longer offers "Annuler", since the mail it
				// would bring back is in the inbox already.
				moi.sync()
				shown := false
				for _, l := range moi.historyScreen() {
					if l.ID != line.ID {
						continue
					}
					shown = true
					if l.Undoable {
						app.failf("après la synchronisation suivante, dans l'historique de %s, la ligne %s, dont un autre logiciel a remis le mail dans la boîte de réception, ne devrait plus proposer « Annuler », elle le propose encore", moi.email, what)
					}
				}
				if !shown {
					app.failf("après la synchronisation suivante, l'historique de %s ne montre plus la ligne %s", moi.email, what)
				}

				// The control: the mail nobody touched is still there.
				moi.expectShows(uids[0], bankStatement.Subject)
			},
		},
		{
			phrase: "Sur une boîte IMAP, « Annuler » après « Tout ranger » remet les autres mails dans ta boîte de réception, et répond « Ce mail n'est plus là où Mailsorter l'avait rangé. » pour celui qu'un autre logiciel a déjà sorti de là.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder, travelOffer, newsletter)
				tidied := []incoming{shopOrder, travelOffer, newsletter}

				// One verdict for the three, so "Annuler" sends them in one
				// request, as the plan does: the answer about the mail taken
				// away and the count of the others are then the same answer.
				// The first mail is not analyzed: it is the control.
				app.ai.willSay("archive", 0.95)
				moi.analyze(uids[1], uids[2], uids[3])
				var proposals []models.AISuggestion
				var ids []string
				for i, m := range tidied {
					p := moi.proposalFor(uids[i+1])
					if p.Action != "archive" {
						app.fatalf("le plan de l'IA devrait proposer d'archiver le mail n°%s %q, il propose %q : le scénario ne prouve rien", uids[i+1], m.Subject, p.Action)
					}
					proposals = append(proposals, p)
					ids = append(ids, p.ID)
				}

				got := moi.tidyAll(ids...)
				if got.code != http.StatusOK || app.number(got.body, "applied") != len(tidied) {
					app.fatalf("« Tout ranger » devrait archiver les %d mails proposés, le serveur a répondu %s : le scénario ne prouve rien", len(tidied), got)
				}
				for _, m := range tidied {
					moi.requireOnServer(serverArchive, m.Subject, "« Tout ranger » vient de l'archiver")
				}
				// The plan offers "Annuler" only when the answer says the
				// tidying can be undone (TriagePlan.js).
				if got.body["reversible"] != true {
					app.failf("après « Tout ranger », l'écran devrait proposer « Annuler » : la réponse dit reversible=%v, le bouton n'est donc pas proposé", got.body["reversible"])
				}

				// Before "Annuler", another mail program files the first
				// tidied mail elsewhere, out of the archives.
				moi.newFolderElsewhere(ownFolder)
				moi.movedFromElsewhere(serverArchive, shopOrder.Subject, ownFolder)

				undos := moi.undoTidy(proposals)
				if len(undos) != 1 {
					app.fatalf("« Annuler » après « Tout ranger » devrait envoyer une seule demande pour les %d mails archivés, il en envoie %d : le scénario ne prouve rien (à corriger dans acceptance_imap_test.go)", len(tidied), len(undos))
				}
				undo := undos[0]
				if undo.got.code != http.StatusOK {
					app.failf("« Annuler » après « Tout ranger » devrait répondre 200 pour les mails n°%v, le serveur a répondu %s", undo.uids, undo.got)
				}
				if restored := app.number(undo.got.body, "restored"); restored != 2 {
					app.failf("« Annuler » après « Tout ranger » devrait annoncer 2 mails remis dans la boîte de réception (%q et %q), il en annonce %d : %v", travelOffer.Subject, newsletter.Subject, restored, undo.got.body)
				}
				// The sentence the plan shows, in the "message" field of the
				// answer.
				switch said, _ := undo.got.body["message"].(string); {
				case said == "":
					app.failf("« Annuler » après « Tout ranger » devrait répondre %q pour le mail n°%s %q, qu'un autre logiciel a déplacé de %s vers %s : la réponse n'en dit rien (%v)", notWhereFiled, uids[1], shopOrder.Subject, serverArchive, ownFolder, undo.got.body)
				case said != notWhereFiled:
					app.failf("« Annuler » après « Tout ranger » devrait répondre %q pour le mail n°%s %q, qu'un autre logiciel a déplacé de %s vers %s, il répond %q", notWhereFiled, uids[1], shopOrder.Subject, serverArchive, ownFolder, said)
				}

				// The others: what the screen shows right after (the plan
				// reloads the listing once the undo answers, without a
				// sync), and the server itself.
				for _, m := range []incoming{travelOffer, newsletter} {
					moi.expectBackInInbox(m.Subject, "juste après « Annuler » sur « Tout ranger »")
					moi.expectOnServer(serverInbox, m.Subject)
					moi.expectNotOnServer(serverArchive, m.Subject, "« Annuler » l'a remis dans la boîte de réception")
				}
				// The one taken away stays where the other program put it,
				// off the inbox screen.
				moi.expectOnServer(ownFolder, shopOrder.Subject)
				moi.expectDoesNotShow(shopOrder.Subject, "un autre logiciel l'a rangé dans "+ownFolder+" avant « Annuler »")
				// The control: the mail the AI never saw is still there.
				moi.expectShows(uids[0], bankStatement.Subject)
			},
		},
		{
			phrase: "Sur une boîte IMAP, archiver de nouveau un mail déjà archivé ne change rien, ni sur le serveur ni dans l'historique, et « Annuler » depuis l'historique le remet toujours dans ta boîte de réception.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)

				moi.act(uids[1], "archive")
				moi.requireOnServer(serverArchive, shopOrder.Subject, "tu viens de l'archiver")
				// The line "Annuler" is pressed on below: the one this first
				// archive wrote, read before the second one is asked for.
				line := moi.historyLineFor(uids[1], "archive")
				what := "du premier archivage du mail n°" + uids[1] + " \"" + shopOrder.Subject + "\""
				if line.Undone || !line.Undoable {
					app.fatalf("juste après l'archivage, l'historique de %s devrait proposer « Annuler » sur la ligne %s : elle est marquée annulée=%v, annulable=%v, le scénario ne prouve rien", moi.email, what, line.Undone, line.Undoable)
				}

				// "Archiver" again on the same number: a tab left open on the
				// inbox, or a proposal still waiting in the plan. act expects
				// it to answer 200.
				moi.act(uids[1], "archive")
				// Nothing changed: in the archives once, not in the inbox.
				copies := 0
				for _, s := range moi.onServer(serverArchive) {
					if s == shopOrder.Subject {
						copies++
					}
				}
				if copies != 1 {
					app.failf("après le second archivage, le dossier %s du serveur IMAP de %s devrait contenir le mail %q une fois, il le contient %d fois", serverArchive, moi.email, shopOrder.Subject, copies)
				}
				moi.expectNotOnServer(serverInbox, shopOrder.Subject, "tu l'as archivé deux fois")
				moi.expectDoesNotShow(shopOrder.Subject, "tu l'as archivé deux fois")
				// Nor in the history: the line of the first archive, and no
				// line for a second one that moved nothing.
				if lines := moi.historyLinesOf(uids[1]); len(lines) != 1 || lines[0].ID != line.ID {
					app.failf("après le second archivage, l'historique de %s devrait montrer une seule ligne \"archive\" pour le mail n°%s %q, celle du premier archivage : il en montre %d (%s)", moi.email, uids[1], shopOrder.Subject, len(lines), describeHistory(lines))
				}

				if got := moi.undoFromHistory(line); got.code != http.StatusOK {
					app.failf("« Annuler » sur la ligne %s, après un second archivage, devrait répondre 200, le serveur a répondu %s", what, got)
				}

				moi.expectOnServer(serverInbox, shopOrder.Subject)
				moi.expectNotOnServer(serverArchive, shopOrder.Subject, "« Annuler » dans l'historique l'a remis dans la boîte de réception")
				// And the inbox screen, once it has read the server again.
				moi.sync()
				moi.expectBackInInbox(shopOrder.Subject, "après « Annuler » dans l'historique et une synchronisation")
				// The control: the mail nobody touched is still there.
				moi.expectShows(uids[0], bankStatement.Subject)
			},
		},
		{
			phrase: "Sur une boîte IMAP, supprimer depuis un écran resté ouvert un mail que Mailsorter a déjà archivé le met bien à la corbeille, et « Annuler » juste après le remet dans ta boîte de réception.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)

				moi.act(uids[1], "archive")
				moi.requireOnServer(serverArchive, shopOrder.Subject, "tu viens de l'archiver")

				// "Supprimer" on the same mail, from another tab left open on
				// the inbox from before the archive: "delete" (Inbox.js,
				// directAction) on the number the row still shows. act
				// expects it to answer 200.
				moi.act(uids[1], "delete")
				moi.expectOnServer(serverTrash, shopOrder.Subject)
				moi.expectNotOnServer(serverArchive, shopOrder.Subject, "tu l'as supprimé depuis un écran resté ouvert, après l'avoir archivé")
				moi.expectNotOnServer(serverInbox, shopOrder.Subject, "tu l'as archivé puis supprimé")

				// "Annuler" in the message that follows that "Supprimer":
				// "untrash" on the same number.
				if got := moi.undoRightAfter(uids[1], "delete"); got.code != http.StatusOK {
					app.failf("« Annuler » juste après la suppression du mail n°%s %q, déjà archivé, devrait répondre 200, le serveur a répondu %s", uids[1], shopOrder.Subject, got)
				}

				// What the screen shows right after: Inbox.js reloads the
				// listing once the undo answers, without a sync. Then the
				// server: back in the inbox, and in neither folder it went
				// through.
				moi.expectBackInInbox(shopOrder.Subject, "juste après « Annuler » sur sa suppression")
				moi.expectOnServer(serverInbox, shopOrder.Subject)
				moi.expectNotOnServer(serverTrash, shopOrder.Subject, "« Annuler » l'a remis dans la boîte de réception")
				moi.expectNotOnServer(serverArchive, shopOrder.Subject, "« Annuler » l'a remis dans la boîte de réception")
				// The history is not read: the sentence says nothing of it,
				// and it may show a line for the deletion next to the one
				// for the archive.
				//
				// The control: the mail nobody touched is still there.
				moi.expectShows(uids[0], bankStatement.Subject)
			},
		},
		{
			phrase: "Sur une boîte IMAP, archiver deux fois de suite le même mail sans attendre la réponse ne l'inscrit qu'une fois dans l'historique, et « Annuler » le remet ensuite dans ta boîte de réception.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				// Two requests at once are a race the app can win by chance,
				// so the sentence is tried on doubleClicks mails, one after
				// the other, and must hold for each. The first mail is not
				// touched: it is the control.
				clicked := numberedMails("Les nouvelles du jour n°%d", doubleClicks)
				moi, uids := syncedIMAPInbox(app, append([]incoming{bankStatement}, clicked...)...)

				var errored, twice, notBack []string
				for i, m := range clicked {
					uid := uids[i+1]
					what := fmt.Sprintf("n°%s %q", uid, m.Subject)

					// Nobody goes through twelve mails in one second, and
					// the server throttles whoever does (20 requests a second
					// per person, rateLimiter in middleware.go): each mail's
					// five requests get the time the throttle needs to give
					// them back. The two clicks themselves still leave
					// together.
					time.Sleep(300 * time.Millisecond)

					// "Archiver" clicked twice on the row before the first
					// answer: both requests leave at the same instant.
					got := moi.actTwiceAtOnce(uid, "archive")
					if got[0].code/100 != 2 || got[1].code/100 != 2 {
						errored = append(errored, fmt.Sprintf("%s (%s, puis %s)", what, got[0], got[1]))
					}

					lines := moi.historyLinesOf(uid)
					if len(lines) != 1 || lines[0].Action != "archive" {
						twice = append(twice, fmt.Sprintf("%s (%d ligne(s) : %s)", what, len(lines), describeHistory(lines)))
					}

					// "Annuler" on the newest "archive" line the Historique
					// screen shows for it.
					var line *historyLine
					for j := range lines {
						if lines[j].Action == "archive" {
							line = &lines[j]
							break
						}
					}
					if line == nil {
						notBack = append(notBack, fmt.Sprintf("%s (aucune ligne « archive » sur laquelle appuyer)", what))
						continue
					}
					if undo := moi.undoFromHistory(*line); undo.code != http.StatusOK {
						notBack = append(notBack, fmt.Sprintf("%s (« Annuler » a répondu %s)", what, undo))
						continue
					}
					// Back on the inbox screen, and in the inbox of the
					// server: a screen that only showed the row again would
					// pass the first half.
					onScreen := moi.shows(m.Subject)
					onServer := false
					for _, s := range moi.onServer(serverInbox) {
						if s == m.Subject {
							onServer = true
						}
					}
					if !onScreen || !onServer {
						notBack = append(notBack, fmt.Sprintf("%s (« Annuler » a répondu 200, mais le mail est sur l'écran : %v, dans la boîte de réception du serveur : %v)", what, onScreen, onServer))
					}
				}

				if len(errored) > 0 {
					app.failf("archiver deux fois de suite le même mail sans attendre la réponse ne devrait répondre aucune erreur : une des deux requêtes en répond une pour %d des %d mails : %s", len(errored), len(clicked), strings.Join(errored, " ; "))
				}
				if len(twice) > 0 {
					app.failf("archiver deux fois de suite le même mail sans attendre la réponse devrait l'inscrire une seule fois dans l'historique de %s, en une ligne \"archive\" : ce n'est pas le cas pour %d des %d mails : %s", moi.email, len(twice), len(clicked), strings.Join(twice, " ; "))
				}
				if len(notBack) > 0 {
					app.failf("« Annuler » dans l'historique, après deux archivages de suite sans attendre la réponse, devrait remettre le mail dans ta boîte de réception : ce n'est pas le cas pour %d des %d mails : %s", len(notBack), len(clicked), strings.Join(notBack, " ; "))
				}
				// The control: the mail nobody touched is still there.
				moi.expectShows(uids[0], bankStatement.Subject)
			},
		},
		{
			phrase: "Sur une boîte IMAP, « Annuler » envoyé deux fois en même temps sur le même archivage remet le mail une seule fois dans ta boîte de réception, les deux réponses sont sans erreur, et l'historique ne montre qu'une annulation.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				// Two requests at once are a race the app can win by chance,
				// so the sentence is tried on doubleUndos mails, one after
				// the other, and must hold for each. The first mail is not
				// touched: it is the control.
				archived := numberedMails("La lettre de la semaine n°%d", doubleUndos)
				moi, uids := syncedIMAPInbox(app, append([]incoming{bankStatement}, archived...)...)

				var errored, notOnce, undos []string
				for i, m := range archived {
					uid := uids[i+1]
					what := fmt.Sprintf("n°%s %q", uid, m.Subject)

					// Each mail's five requests get the time the server's
					// throttle needs to give them back (20 requests a second
					// per person, rateLimiter in middleware.go), as in the
					// sentence about two archives at once. The two "Annuler"
					// themselves still leave together.
					time.Sleep(300 * time.Millisecond)

					// "Archiver" on its row, then "Annuler" in the message
					// that follows, clicked twice before the first answer
					// comes back (a double click, or the same message in two
					// tabs): "unarchive" twice at once, on the number the row
					// had in the inbox.
					moi.act(uid, "archive")
					moi.requireOnServer(serverArchive, m.Subject, "tu viens de l'archiver")
					got := moi.actTwiceAtOnce(uid, "unarchive")
					if got[0].code/100 != 2 || got[1].code/100 != 2 {
						errored = append(errored, fmt.Sprintf("%s (%s, puis %s)", what, got[0], got[1]))
					}

					// Back once: in the inbox of the server, out of the
					// archives, and on the inbox screen, which Inbox.js
					// reloads once the undo answers, without a sync.
					inInbox, inArchive, onScreen := 0, 0, 0
					for _, s := range moi.onServer(serverInbox) {
						if s == m.Subject {
							inInbox++
						}
					}
					for _, s := range moi.onServer(serverArchive) {
						if s == m.Subject {
							inArchive++
						}
					}
					for _, row := range moi.inbox() {
						if row.Subject == m.Subject {
							onScreen++
						}
					}
					if inInbox != 1 || inArchive != 0 || onScreen != 1 {
						notOnce = append(notOnce, fmt.Sprintf("%s (dans la boîte de réception du serveur : %d fois, dans le dossier %s : %d fois, sur l'écran : %d fois)", what, inInbox, serverArchive, inArchive, onScreen))
					}

					// The Historique screen: one line "Annulation" (source
					// undo) for this mail, whichever request wrote it.
					var undoLines int
					lines := moi.historyLinesOf(uid)
					for _, l := range lines {
						if l.Source == SourceUndo {
							undoLines++
						}
					}
					if undoLines != 1 {
						undos = append(undos, fmt.Sprintf("%s (%d annulation(s) ; lignes : %s)", what, undoLines, describeHistory(lines)))
					}
				}

				if len(errored) > 0 {
					app.failf("« Annuler » envoyé deux fois en même temps sur le même archivage ne devrait répondre aucune erreur : une des deux requêtes en répond une pour %d des %d mails : %s", len(errored), len(archived), strings.Join(errored, " ; "))
				}
				if len(notOnce) > 0 {
					app.failf("« Annuler » envoyé deux fois en même temps sur le même archivage devrait remettre le mail une seule fois dans la boîte de réception de %s, et plus dans le dossier %s : ce n'est pas le cas pour %d des %d mails : %s", moi.email, serverArchive, len(notOnce), len(archived), strings.Join(notOnce, " ; "))
				}
				if len(undos) > 0 {
					app.failf("après « Annuler » envoyé deux fois en même temps sur le même archivage, l'historique de %s devrait montrer une seule annulation (une ligne de source %q) pour le mail : ce n'est pas le cas pour %d des %d mails : %s", moi.email, SourceUndo, len(undos), len(archived), strings.Join(undos, " ; "))
				}
				// The control: the mail nobody touched is still there.
				moi.expectShows(uids[0], bankStatement.Subject)
			},
		},
		{
			phrase: "Sur une boîte IMAP, « Annuler » envoyé deux fois en même temps depuis l'historique sur le même archivage remet le mail une seule fois dans ta boîte de réception, les deux réponses sont sans erreur, et l'historique ne montre qu'une annulation.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				// Two requests at once are a race the app can win by chance,
				// so the sentence is tried on doubleHistoryUndos mails, one
				// after the other, and must hold for each. The first mail is
				// not touched: it is the control.
				archived := numberedMails("Le bulletin du club n°%d", doubleHistoryUndos)
				moi, uids := syncedIMAPInbox(app, append([]incoming{bankStatement}, archived...)...)

				var errored, notOnce, undos []string
				for i, m := range archived {
					uid := uids[i+1]
					what := fmt.Sprintf("n°%s %q", uid, m.Subject)

					// Each mail's six requests get the time the server's
					// throttle needs to give them back (20 requests a second
					// per person, rateLimiter in middleware.go), as in the
					// sentence about two archives at once. The two "Annuler"
					// themselves still leave together.
					time.Sleep(300 * time.Millisecond)

					// "Archiver" on its row, then, on the Historique screen,
					// "Annuler" on the line of that archive, confirmed twice
					// before the first answer comes back (a double click, or
					// the history open in two tabs): two POST
					// /api/activity/undo at once, on the same line.
					moi.act(uid, "archive")
					moi.requireOnServer(serverArchive, m.Subject, "tu viens de l'archiver")
					line := moi.historyLineFor(uid, "archive")
					if line.Undone || !line.Undoable {
						app.fatalf("juste après l'archivage, l'historique de %s devrait proposer « Annuler » sur la ligne de l'archivage du mail %s : elle est marquée annulée=%v, annulable=%v, le scénario ne prouve rien", moi.email, what, line.Undone, line.Undoable)
					}
					got := moi.undoFromHistoryTwiceAtOnce(line)
					if got[0].code/100 != 2 || got[1].code/100 != 2 {
						errored = append(errored, fmt.Sprintf("%s (%s, puis %s)", what, got[0], got[1]))
					}

					// Back once: in the inbox of the server, out of the
					// archives, and on the inbox screen.
					inInbox, inArchive, onScreen := 0, 0, 0
					for _, s := range moi.onServer(serverInbox) {
						if s == m.Subject {
							inInbox++
						}
					}
					for _, s := range moi.onServer(serverArchive) {
						if s == m.Subject {
							inArchive++
						}
					}
					for _, row := range moi.inbox() {
						if row.Subject == m.Subject {
							onScreen++
						}
					}
					if inInbox != 1 || inArchive != 0 || onScreen != 1 {
						notOnce = append(notOnce, fmt.Sprintf("%s (dans la boîte de réception du serveur : %d fois, dans le dossier %s : %d fois, sur l'écran : %d fois)", what, inInbox, serverArchive, inArchive, onScreen))
					}

					// The Historique screen: one line "Annulation" (source
					// undo) for this mail, whichever request wrote it.
					var undoLines int
					lines := moi.historyLinesOf(uid)
					for _, l := range lines {
						if l.Source == SourceUndo {
							undoLines++
						}
					}
					if undoLines != 1 {
						undos = append(undos, fmt.Sprintf("%s (%d annulation(s) ; lignes : %s)", what, undoLines, describeHistory(lines)))
					}
				}

				if len(errored) > 0 {
					app.failf("« Annuler » envoyé deux fois en même temps depuis l'historique sur le même archivage ne devrait répondre aucune erreur : une des deux requêtes en répond une pour %d des %d mails : %s", len(errored), len(archived), strings.Join(errored, " ; "))
				}
				if len(notOnce) > 0 {
					app.failf("« Annuler » envoyé deux fois en même temps depuis l'historique sur le même archivage devrait remettre le mail une seule fois dans la boîte de réception de %s, et plus dans le dossier %s : ce n'est pas le cas pour %d des %d mails : %s", moi.email, serverArchive, len(notOnce), len(archived), strings.Join(notOnce, " ; "))
				}
				if len(undos) > 0 {
					app.failf("après « Annuler » envoyé deux fois en même temps depuis l'historique sur le même archivage, l'historique de %s devrait montrer une seule annulation (une ligne de source %q) pour le mail : ce n'est pas le cas pour %d des %d mails : %s", moi.email, SourceUndo, len(undos), len(archived), strings.Join(undos, " ; "))
				}
				// The control: the mail nobody touched is still there.
				moi.expectShows(uids[0], bankStatement.Subject)
			},
		},
		{
			phrase: "Sur une boîte IMAP, archiver depuis deux écrans restés ouverts un mail qu'un autre logiciel a déjà sorti de ta boîte de réception répond sans erreur les deux fois, et n'ajoute rien à l'historique.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder, travelOffer)

				// The two ways a mail leaves an inbox from another mail
				// program: archived (moved to another folder), and deleted
				// for good. No "Synchroniser" after: both screens still show
				// the two rows.
				moi.movedElsewhere(uids[1], serverArchive)
				moi.deletedElsewhere(uids[2])

				for _, c := range []struct {
					uid, subject, how string
				}{
					{uids[1], shopOrder.Subject, "archivé"},
					{uids[2], travelOffer.Subject, "supprimé définitivement"},
				} {
					// "Archiver" on its row, on one screen, then on the other.
					for n, screen := range []string{"le premier écran", "le second écran"} {
						if got := moi.actAnswer(c.uid, "archive"); got.code/100 != 2 {
							app.failf("« Archiver » depuis %s sur le mail n°%s %q, qu'un autre logiciel a déjà %s, devrait répondre sans erreur (archivage n°%d), le serveur a répondu %s", screen, c.uid, c.subject, c.how, n+1, got)
						}
					}
					if lines := moi.historyLinesOf(c.uid); len(lines) > 0 {
						app.failf("l'historique de %s ne devrait rien montrer pour le mail n°%s %q, qu'un autre logiciel avait déjà %s avant tes deux « Archiver » : il montre %d ligne(s) (%s)", moi.email, c.uid, c.subject, c.how, len(lines), describeHistory(lines))
					}
				}

				// The control: the mail nobody touched is still there.
				moi.expectShows(uids[0], bankStatement.Subject)
			},
		},
		{
			phrase: "Sur une boîte IMAP, « Supprimer » depuis un écran resté ouvert un mail qu'un autre logiciel a déjà sorti de ta boîte de réception ne le met pas à la corbeille, répond qu'il n'était plus dans ta boîte de réception, et n'ajoute rien à l'historique.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder, travelOffer)

				// The two ways a mail leaves an inbox from another mail
				// program: archived on the phone (moved to the archives),
				// and deleted for good. No "Synchroniser" after: the screen
				// left open still shows the two rows.
				moi.movedElsewhere(uids[1], serverArchive)
				moi.deletedElsewhere(uids[2])

				for _, c := range []struct {
					uid, subject, how string
				}{
					{uids[1], shopOrder.Subject, "archivé"},
					{uids[2], travelOffer.Subject, "supprimé définitivement"},
				} {
					// "Supprimer" on its row: "delete" (Inbox.js, directAction).
					// The answer the screen reads: a 200 whose status says the
					// mail had already gone, not "ok", which announces it moved
					// to the trash.
					got := moi.actAnswer(c.uid, "delete")
					if got.code != http.StatusOK || got.body["status"] != alreadyOut {
						app.failf("« Supprimer » depuis un écran resté ouvert sur le mail n°%s %q, qu'un autre logiciel a déjà %s, devrait répondre %d avec le statut %q (il n'était plus dans ta boîte de réception), le serveur a répondu %s", c.uid, c.subject, c.how, http.StatusOK, alreadyOut, got)
					}
					if lines := moi.historyLinesOf(c.uid); len(lines) > 0 {
						app.failf("l'historique de %s ne devrait rien montrer pour le mail n°%s %q, qu'un autre logiciel avait déjà %s avant ton « Supprimer » : il montre %d ligne(s) (%s)", moi.email, c.uid, c.subject, c.how, len(lines), describeHistory(lines))
					}
				}

				// Not in the trash: the one archived on the phone is still in
				// the archives, where the other program put it, and the one
				// deleted for good is nowhere.
				why := "un autre logiciel l'avait sorti de ta boîte de réception avant ton « Supprimer »"
				moi.expectOnServer(serverArchive, shopOrder.Subject)
				moi.expectNotOnServer(serverTrash, shopOrder.Subject, why)
				moi.expectNotOnServer(serverTrash, travelOffer.Subject, why)
				// The control: the mail nobody touched is still there.
				moi.expectShows(uids[0], bankStatement.Subject)
			},
		},
		{
			phrase: "Sur une boîte IMAP, « Marquer comme lu » envoyé depuis un écran resté ouvert sur un mail qu'un autre logiciel a sorti de ta boîte de réception répond « Ce mail n'est plus dans votre boîte de réception. » et ne change rien au mail sur le serveur.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder, travelOffer)

				// The two ways a mail leaves an inbox from another mail
				// program: archived on the phone (moved to the archives),
				// and deleted for good. No "Synchroniser" after: the screen
				// left open still shows the two rows.
				moi.movedElsewhere(uids[1], serverArchive)
				moi.deletedElsewhere(uids[2])
				// Where the sentence starts: the mail archived on the phone
				// is unread there. One already read would prove nothing.
				if moi.seenOnServer(serverArchive, shopOrder.Subject) {
					app.fatalf("le mail %q, qu'un autre logiciel a archivé sans l'ouvrir, devrait être non lu dans le dossier %s du serveur IMAP de %s, il est lu : le scénario ne prouve rien", shopOrder.Subject, serverArchive, moi.email)
				}

				for _, c := range []struct {
					uid, subject, how string
				}{
					{uids[1], shopOrder.Subject, "archivé"},
					{uids[2], travelOffer.Subject, "supprimé définitivement"},
				} {
					// "Marquer comme lu" on its row: over IMAP, the
					// single-message action with "read" (flagAction in
					// Inbox.js). A refusal, as the screen shows one: a 4xx
					// status and its "error" field (apiError in
					// services/api.js), and the row's "lu" rolled back. On a
					// 2xx the screen announces "Lu".
					got := moi.actAnswer(c.uid, "read")
					if got.code/100 != 4 || got.said() != notInInbox {
						app.failf("« Marquer comme lu » depuis un écran resté ouvert sur le mail n°%s %q, qu'un autre logiciel a déjà %s, devrait répondre %q, le serveur a répondu %s", c.uid, c.subject, c.how, notInInbox, got)
					}
				}

				// Nothing changed on the server: the mail archived on the
				// phone is still unread, in the archives where the other
				// program put it, and not back in the inbox.
				why := "un autre logiciel l'avait sorti de ta boîte de réception avant ton « Marquer comme lu »"
				if moi.seenOnServer(serverArchive, shopOrder.Subject) {
					app.failf("le mail %q devrait être encore non lu dans le dossier %s du serveur IMAP de %s, où un autre logiciel l'a archivé (%s) : il est lu", shopOrder.Subject, serverArchive, moi.email, why)
				}
				moi.expectNotOnServer(serverInbox, shopOrder.Subject, why)
				moi.expectNotOnServer(serverInbox, travelOffer.Subject, why)
				// The control: the mail nobody touched is still there.
				moi.expectShows(uids[0], bankStatement.Subject)
			},
		},
		{
			phrase: "Sur une boîte IMAP, une action de l'historique faite il y a plus de 30 jours ne propose plus « Annuler ».",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder, travelOffer)

				moi.act(uids[1], "archive")
				moi.act(uids[2], "archive")
				old := moi.historyLineFor(uids[1], "archive")
				recent := moi.historyLineFor(uids[2], "archive")
				// Where the sentence starts: both lines offer "Annuler". A line
				// never offered proves nothing once old.
				for _, l := range []historyLine{old, recent} {
					if l.Undone || !l.Undoable {
						app.fatalf("juste après l'archivage, l'historique de %s devrait proposer « Annuler » sur la ligne de l'archivage du mail n°%s : elle est marquée annulée=%v, annulable=%v, le scénario ne prouve rien", moi.email, l.MessageID, l.Undone, l.Undoable)
					}
				}

				// 31 days go by for the first archive (see actionTakenDaysAgo:
				// the database is moved back, there is no clock to turn).
				moi.actionTakenDaysAgo(old, 31)

				// The Historique screen, as it loads again.
				var sawOld, sawRecent bool
				for _, l := range moi.historyScreen() {
					switch l.ID {
					case old.ID:
						sawOld = true
						if l.Undoable {
							app.failf("dans l'historique de %s, la ligne de l'archivage du mail n°%s %q, fait il y a 31 jours, ne devrait plus proposer « Annuler », elle le propose encore", moi.email, uids[1], shopOrder.Subject)
						}
					case recent.ID:
						sawRecent = true
						// The control: a history that offers "Annuler" on
						// nothing would pass the line above.
						if !l.Undoable {
							app.failf("dans l'historique de %s, la ligne de l'archivage du mail n°%s %q, fait à l'instant, devrait proposer « Annuler », elle ne le propose pas", moi.email, uids[2], travelOffer.Subject)
						}
					}
				}
				if !sawOld || !sawRecent {
					app.failf("l'historique de %s devrait montrer les lignes de l'archivage des mails n°%s %q (il y a 31 jours) et n°%s %q (à l'instant) : il montre la première : %v, la seconde : %v", moi.email, uids[1], shopOrder.Subject, uids[2], travelOffer.Subject, sawOld, sawRecent)
				}
			},
		},
		{
			phrase: "Sur une boîte IMAP, l'historique ne propose plus « Annuler » sur un « marquer comme lu » dont le mail a quitté ta boîte de réception.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				mails := []incoming{bankStatement, shopOrder, travelOffer}
				moi, uids := syncedIMAPInbox(app, mails...)

				// "Marquer comme lu" on the three mails: over IMAP, the
				// single-message action with "read" (flagAction in
				// Inbox.js). act expects it to answer 200. The first mail
				// stays in the inbox: it is the control.
				for _, uid := range uids {
					moi.act(uid, "read")
				}
				// Where the sentence starts: the three lines offer
				// "Annuler". A line never offered proves nothing once its
				// mail has left.
				reads := make([]historyLine, len(uids))
				for i, uid := range uids {
					reads[i] = moi.historyLineFor(uid, "read")
					if reads[i].Undone || !reads[i].Undoable {
						app.fatalf("juste après « Marquer comme lu », l'historique de %s devrait proposer « Annuler » sur la ligne du mail n°%s %q : elle est marquée annulée=%v, annulable=%v, le scénario ne prouve rien", moi.email, uid, mails[i].Subject, reads[i].Undone, reads[i].Undoable)
					}
				}

				// expectReadLines reads the Historique screen once, as it
				// loads, and checks the "read" line of each mail: no
				// "Annuler" on those whose mail has left the inbox (left
				// says how), and "Annuler" still on the others, so a
				// history that offers it on nothing does not pass.
				expectReadLines := func(when string, left map[int]string) {
					t.Helper()
					shown := map[string]historyLine{}
					for _, l := range moi.historyScreen() {
						shown[l.ID] = l
					}
					for i, read := range reads {
						what := fmt.Sprintf("du « marquer comme lu » du mail n°%s %q", uids[i], mails[i].Subject)
						l, ok := shown[read.ID]
						how, gone := left[i]
						switch {
						case !ok:
							app.failf("%s, l'historique de %s ne montre plus la ligne %s", when, moi.email, what)
						case gone && l.Undoable:
							app.failf("%s, dans l'historique de %s, la ligne %s, %s, ne devrait plus proposer « Annuler », elle le propose encore", when, moi.email, what, how)
						case !gone && !l.Undoable:
							app.failf("%s, dans l'historique de %s, la ligne %s, resté dans ta boîte de réception, devrait toujours proposer « Annuler », elle ne le propose plus", when, moi.email, what)
						}
					}
				}

				// The two ways a mail leaves the inbox that Mailsorter
				// knows of. First, "Archiver" on the row of the second
				// mail, and the Historique screen opened right after.
				moi.act(uids[1], "archive")
				moi.requireOnServer(serverArchive, shopOrder.Subject, "tu viens de l'archiver")
				archived := "que tu as archivé depuis"
				expectReadLines("juste après l'archivage du mail n°"+uids[1], map[int]string{1: archived})

				// Then the third mail, archived on the phone, which
				// Mailsorter learns at the next sync: the Historique screen
				// opened after it.
				moi.movedElsewhere(uids[2], serverArchive)
				moi.sync()
				expectReadLines("après une synchronisation", map[int]string{
					1: archived,
					2: "qu'un autre logiciel a sorti depuis de ta boîte de réception",
				})
			},
		},
		{
			phrase: "Sur une boîte IMAP, « Annuler » sur une action de plus de 30 jours, envoyé depuis un écran resté ouvert, répond « Cette action est trop ancienne pour être annulée ici : retrouvez ce mail dans l'archive ou la corbeille de votre messagerie. » et ne déplace rien.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder, travelOffer)

				moi.act(uids[1], "archive")
				moi.act(uids[2], "archive")
				moi.requireOnServer(serverArchive, shopOrder.Subject, "tu viens de l'archiver")
				moi.requireOnServer(serverArchive, travelOffer.Subject, "tu viens de l'archiver")

				// The Historique screen, opened now and left open: both lines
				// offer "Annuler".
				first := moi.historyLineFor(uids[1], "archive")
				second := moi.historyLineFor(uids[2], "archive")
				for _, l := range []historyLine{first, second} {
					if l.Undone || !l.Undoable {
						app.fatalf("juste après l'archivage, l'historique de %s devrait proposer « Annuler » sur la ligne de l'archivage du mail n°%s : elle est marquée annulée=%v, annulable=%v, le scénario ne prouve rien", moi.email, l.MessageID, l.Undone, l.Undoable)
					}
				}

				// 31 days go by (see actionTakenDaysAgo: the database is
				// moved back, there is no clock to turn).
				moi.actionTakenDaysAgo(first, 31)
				moi.actionTakenDaysAgo(second, 31)

				for _, c := range []struct {
					line          historyLine
					uid, subject  string
					when          string
					syncedBetween bool
				}{
					{first, uids[1], shopOrder.Subject, "avant toute synchronisation", false},
					// In 30 days the background sync runs (every 30 minutes,
					// auto_sync.go): the second "Annuler" comes after one.
					{second, uids[2], travelOffer.Subject, "après une synchronisation", true},
				} {
					if c.syncedBetween {
						moi.sync()
					}
					what := "de l'archivage du mail n°" + c.uid + " \"" + c.subject + "\", fait il y a 31 jours"
					// A refusal, as History.js shows one: a 4xx status and its
					// "error" field (apiError in services/api.js).
					got := moi.undoFromHistory(c.line)
					if got.code/100 != 4 || got.said() != tooOldToUndo {
						app.failf("« Annuler » sur la ligne %s, %s, devrait répondre %q, le serveur a répondu %s", what, c.when, tooOldToUndo, got)
					}
					// Nothing moved: still in the archives, neither in the
					// inbox of the server nor on the screen, and the line is
					// not marked undone.
					why := "« Annuler » sur une action de plus de 30 jours ne déplace rien"
					moi.expectOnServer(serverArchive, c.subject)
					moi.expectNotOnServer(serverInbox, c.subject, why)
					moi.expectDoesNotShow(c.subject, why)
					moi.expectMarkedUndone(c.line, false, what)
				}

				// The control: the mail nobody touched is still there.
				moi.expectShows(uids[0], bankStatement.Subject)
			},
		},
	})
}

// doubleClicks is how many mails the sentence about two archives at once is
// tried on. When it was written the app lost the race on several of the 12
// mails in each of 7 runs (two history lines, or one of the two requests in
// error), so the scenario failed every time: one mail alone could pass by
// chance.
const doubleClicks = 12

// doubleUndos is how many mails the sentence about "Annuler" sent twice at
// once is tried on. When it was written one of the two requests answered an
// error on each of the 12 mails in each of 7 runs: a 404 saying the mail is
// no longer where Mailsorter filed it, or that the action is too old, or a
// 500 where the test IMAP server (imapmemserver) answers the second move of
// a mail already moved with an empty COPYUID its client cannot read. Twelve
// stay, so a change that only narrows the race does not pass by chance.
const doubleUndos = 12

// doubleHistoryUndos is how many mails the sentence about "Annuler" sent twice
// at once from the history is tried on. When it was written, with the claim
// putBack takes before moving a mail back removed, one of the two requests
// answered an error on 5 to 7 of the 12 mails in each of 5 runs: a 502 where
// the test IMAP server answers the second move of a mail already moved with
// an empty COPYUID its client cannot read. Twelve, so a change that only
// narrows the race does not pass by chance.
const doubleHistoryUndos = 12
