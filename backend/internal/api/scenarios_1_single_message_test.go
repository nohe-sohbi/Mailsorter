//go:build acceptance

package api

// What happens when a message leaves the inbox.
//
// Every sentence below was written by reading the code, then proven against the
// running app, and started "observed": what the app does today, not yet
// confirmed by Nohé. His answer makes it validated (keep it) or rejected (a
// bug: the change that fixes it replaces the sentence).

import (
	"net/http"
	"testing"
)

func TestScenarios_SingleMessage(t *testing.T) {
	playScenarios(t, "Ce que tu fais toi-même, mail par mail", []scenario{
		{
			phrase: "Quand tu archives un mail, il quitte ta boîte de réception et l'archivage apparaît dans ton historique.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				ids := app.receiveAndSync(incoming{From: "Boutique <info@boutique.com>", Subject: "Soldes"})
				app.act(ids[0], "archive")
				app.expectNotInInbox(ids[0])
				app.expectNotInTrash(ids[0])
				app.expectHistory(ids[0], "archive", SourceDirect)
			},
		},
		{
			phrase: "Quand tu supprimes un mail, il part dans la corbeille de Gmail : rien n'est effacé définitivement.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				ids := app.receiveAndSync(incoming{From: "Boutique <info@boutique.com>", Subject: "Soldes"})
				app.act(ids[0], "delete")
				app.expectInTrash(ids[0])
				app.expectNotInInbox(ids[0])
				app.expectHistory(ids[0], "delete", SourceDirect)
			},
		},
		{
			phrase: "Un expéditeur protégé ne t'empêche pas d'archiver ou de supprimer toi-même un de ses mails.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				app.protect("banque.fr")
				ids := app.receiveAndSync(
					incoming{From: "Ma Banque <news@banque.fr>", Subject: "Relevé"},
					incoming{From: "Ma Banque <alertes@banque.fr>", Subject: "Alerte"},
				)
				app.act(ids[0], "archive")
				app.act(ids[1], "delete")
				app.expectNotInInbox(ids...)
				app.expectInTrash(ids[1])
			},
		},
		{
			phrase: "Depuis l'historique, annuler un archivage remet le mail dans ta boîte de réception.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				ids := app.receiveAndSync(incoming{From: "Boutique <info@boutique.com>", Subject: "Soldes"})
				app.act(ids[0], "archive")
				app.undoLast(ids[0])
				app.expectInInbox(ids[0])
				app.expectHistory(ids[0], "unarchive", SourceUndo)
			},
		},
		{
			phrase: "Depuis l'historique, annuler une suppression sort le mail de la corbeille et le remet dans ta boîte de réception.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				ids := app.receiveAndSync(incoming{From: "Boutique <info@boutique.com>", Subject: "Soldes"})
				app.act(ids[0], "delete")
				app.undoLast(ids[0])
				app.expectNotInTrash(ids[0])
				app.expectInInbox(ids[0])
			},
		},
		{
			phrase: "Sur Gmail aussi, « Annuler » juste après avoir supprimé un mail marque la suppression annulée dans l'historique, qui ne propose plus de l'annuler.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				ids := app.receiveAndSync(incoming{From: "Boutique <info@boutique.com>", Subject: "Soldes"})
				app.act(ids[0], "delete")
				// Where the sentence starts: the history offers to undo the
				// deletion. A line never offered proves nothing once undone.
				line := app.historyLineFor(ids[0], "delete")
				what := "de la suppression du mail " + app.describe(ids[0])
				if line.Undone || !line.Undoable {
					app.fatalf("juste après la suppression, l'historique devrait proposer « Annuler » sur la ligne %s : elle est marquée annulée=%v, annulable=%v, le scénario ne prouve rien", what, line.Undone, line.Undoable)
				}

				// "Annuler" in the message that follows "Supprimer", which
				// sends "untrash" (undoToast in Inbox.js).
				if got := app.undoRightAfter(ids[0], "delete"); got.code != http.StatusOK {
					app.fatalf("« Annuler » juste après la suppression du mail %s devrait répondre 200, le serveur a répondu %s : le scénario ne prouve rien", app.describe(ids[0]), got)
				}

				// The Historique screen, opened next: the same line.
				app.expectNoLongerUndoable(line, what)
			},
		},
		{
			phrase: "Sur Gmail aussi, « Annuler » juste après avoir archivé un mail marque l'archivage annulé dans l'historique, qui ne propose plus de l'annuler.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				ids := app.receiveAndSync(incoming{From: "Boutique <info@boutique.com>", Subject: "Soldes"})
				app.act(ids[0], "archive")
				// Where the sentence starts: the history offers to undo the
				// archive. A line never offered proves nothing once undone.
				line := app.historyLineFor(ids[0], "archive")
				what := "de l'archivage du mail " + app.describe(ids[0])
				if line.Undone || !line.Undoable {
					app.fatalf("juste après l'archivage, l'historique devrait proposer « Annuler » sur la ligne %s : elle est marquée annulée=%v, annulable=%v, le scénario ne prouve rien", what, line.Undone, line.Undoable)
				}

				// "Annuler" in the message that follows "Archiver", which
				// sends "unarchive" (undoToast in Inbox.js).
				if got := app.undoRightAfter(ids[0], "archive"); got.code != http.StatusOK {
					app.fatalf("« Annuler » juste après l'archivage du mail %s devrait répondre 200, le serveur a répondu %s : le scénario ne prouve rien", app.describe(ids[0]), got)
				}

				// The Historique screen, opened next: the same line.
				app.expectNoLongerUndoable(line, what)
			},
		},
		{
			phrase: "Quand tu reportes un mail, il quitte ta boîte de réception ; à l'heure prévue, il revient tout seul, marqué non lu.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				ids := app.receiveAndSync(incoming{From: "Garage <rdv@garage.fr>", Subject: "Votre rendez-vous"})
				app.act(ids[0], "read")
				app.snooze(ids[0])
				app.expectNotInInbox(ids[0])
				app.timeComesForSnoozes()
				app.expectInInbox(ids[0])
				app.expectUnread(ids[0])
				app.expectHistory(ids[0], "unarchive", SourceSnooze)
			},
		},
		{
			phrase: "« Annuler » sur un report depuis l'Historique remet le mail dans ta boîte de réception et le retire de tes mails reportés : à l'heure prévue, rien ne bouge plus.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				const subject = "Votre rendez-vous"
				ids := app.receiveAndSync(incoming{From: "Garage <rdv@garage.fr>", Subject: subject})
				app.act(ids[0], "read")
				app.snooze(ids[0])
				if app.gmail.inInbox(ids[0]) {
					app.fatalf("le mail %s devrait avoir quitté ta boîte de réception une fois reporté, il y est encore : le scénario ne prouve rien", app.describe(ids[0]))
				}

				// The line the Historique screen shows for the report.
				line := app.historyLineFor(ids[0], "archive")
				if line.Source != SourceSnooze {
					app.fatalf("la ligne « archive » du mail %s devrait venir du report, elle vient de %q : le scénario ne prouve rien", app.describe(ids[0]), line.Source)
				}
				if !line.Undoable {
					app.failf("l'historique devrait proposer « Annuler » sur la ligne du report du mail %s, il ne le propose pas", app.describe(ids[0]))
				}

				// "Annuler" on it (handleUndo in History.js).
				if code, out := app.call(http.MethodPost, "/api/activity/undo", map[string]string{"id": line.ID}); code != http.StatusOK {
					app.fatalf("« Annuler » sur la ligne du report du mail %s devrait répondre 200, le serveur a répondu %d : %v", app.describe(ids[0]), code, out)
				}
				app.expectInInbox(ids[0])
				for _, r := range app.reportedAs(app.token, scenarioUser) {
					if r.Subject == subject {
						app.failf("après « Annuler » sur son report, les mails reportés ne devraient plus montrer le mail %s, ils le montrent encore", app.describe(ids[0]))
					}
				}

				// Its hour passes: nothing moves any more, nothing is marked
				// unread, nothing is written in the history.
				before := app.historyScreenAs(app.token, scenarioUser)
				wasUnread := app.gmail.has(ids[0], labelUnread)
				app.reportsHourPassesFor(scenarioUser)
				app.expectInInbox(ids[0])
				if unread := app.gmail.has(ids[0], labelUnread); unread != wasUnread {
					app.failf("à l'heure prévue du report annulé, le mail %s ne devrait pas changer : il était non lu=%v, il est non lu=%v", app.describe(ids[0]), wasUnread, unread)
				}
				if after := app.historyScreenAs(app.token, scenarioUser); len(after) != len(before) {
					app.failf("à l'heure prévue du report annulé, l'historique ne devrait rien montrer de nouveau, il montre %d ligne(s) de plus, la plus récente : %q (%s)", len(after)-len(before), after[0].Action, after[0].Source)
				}
			},
		},
	})
}
