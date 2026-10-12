//go:build acceptance

package api

// What happens when a message leaves the inbox.
//
// Every sentence below was written by reading the code, then proven against the
// running app. They are "observed": the app does this today, and Nohé has not yet
// said whether it is what he wants. Each answer turns a sentence into a
// validated one (keep it), a bug (a /feature flips it), or a decision to take.

import "testing"

func TestScenarios_SingleMessage(t *testing.T) {
	playScenarios(t, "Ce que tu fais toi-même, mail par mail", []scenario{
		{
			phrase: "Quand tu archives un mail, il quitte ta boîte de réception et l'archivage apparaît dans ton historique.",
			status: observed,
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
			status: observed,
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
			status: observed,
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
			status: observed,
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
			status: observed,
			run: func(t *testing.T, app *testApp) {
				ids := app.receiveAndSync(incoming{From: "Boutique <info@boutique.com>", Subject: "Soldes"})
				app.act(ids[0], "delete")
				app.undoLast(ids[0])
				app.expectNotInTrash(ids[0])
				app.expectInInbox(ids[0])
			},
		},
		{
			phrase: "Quand tu reportes un mail, il quitte ta boîte de réception ; à l'heure prévue, il revient tout seul, marqué non lu.",
			status: observed,
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
	})
}
