//go:build acceptance

package api

// "Reporter" on a mailbox reached over IMAP.
//
// Over the Gmail API a reported mail keeps its place and loses the INBOX
// label, under a label of Mailsorter's own. An IMAP mailbox has no labels: a
// mail is in one folder. So reporting one moves it into a folder of the
// mailbox, "Plus tard", which Mailsorter creates the first time, and at the
// hour chosen moves it back into the inbox, unread. These sentences are the
// IMAP side of the two Gmail ones (scenarios_1_single_message_test.go,
// scenarios_2_selection_test.go), read on the inbox screen, on the "Prévus"
// tab of the Reporté screen, and on the server itself, as another mail program
// sees it (acceptance_imap_test.go).
//
// The mailbox is also used elsewhere: the person sees "Plus tard" on their
// phone and may take a mail out of it before its hour. Mailsorter must then
// leave it where they put it.
//
// When a screen must NOT show a mail, it is read right away, without a sync:
// a screen that lists a mail which is not there is wrong at any moment. When
// the inbox must show a mail again after the background sweep, it is read
// after a sync, which opening the inbox runs (fetchData in EmailContext.js):
// the sweep is not something the person did on that screen. "Réactiver" is,
// and its "tout de suite" is read without a sync, as the "Annuler" of
// scenarios_9_imap_undo_test.go is.
//
// "Plus tard" is looked for at the root of the mailbox, or under the prefix a
// server announces for personal folders ("INBOX." on some), which is where a
// folder has to be created there (folderOnServer). The test server announces
// none.

import (
	"net/http"
	"net/mail"
	"strings"
	"testing"
)

// laterFolder is the folder of the mailbox a reported mail waits in, by the
// name the sentences give it and a mail program shows.
const laterFolder = "Plus tard"

// reportedOnIMAP is where the sentences about a reported mail start: "Reporter"
// on this mail answered 200, and the mail in the "Plus tard" folder of the
// server. Anything else stops the scenario: there is then no reported mail to
// look at. It returns the name the server lists that folder under.
func reportedOnIMAP(moi *imapPerson, uid string, m incoming) string {
	app := moi.app
	app.t.Helper()
	if got := moi.snoozeAnswer(uid); got.code != http.StatusOK {
		app.fatalf("reporter le mail n°%s %q de %s devrait répondre 200, le serveur a répondu %s : le mail n'est pas reporté, le scénario s'arrête là", uid, m.Subject, moi.email, got)
	}
	later, ok := moi.folderOnServer(laterFolder)
	if !ok {
		app.fatalf("après le report du mail n°%s %q, le serveur IMAP de %s devrait avoir un dossier « %s », il n'en a pas : le mail n'est rangé nulle part, le scénario s'arrête là", uid, m.Subject, moi.email, laterFolder)
	}
	moi.requireOnServer(later, m.Subject, "il vient d'être reporté")
	return later
}

// expectInLater checks that the "Plus tard" folder of the server exists and
// holds this mail. A missing folder is a failure of its own, not a stop:
// expectOnServer would stop the scenario on a folder that does not open.
func expectInLater(moi *imapPerson, m incoming) {
	app := moi.app
	app.t.Helper()
	later, ok := moi.folderOnServer(laterFolder)
	if !ok {
		app.failf("le mail %q devrait être rangé dans le dossier « %s » du serveur IMAP de %s, ce dossier n'existe pas", m.Subject, laterFolder, moi.email)
		return
	}
	moi.expectOnServer(later, m.Subject)
}

// expectNotInLater checks that the "Plus tard" folder of the server, if there
// is one, does not hold this mail. why says why it must not.
func expectNotInLater(moi *imapPerson, m incoming, why string) {
	moi.app.t.Helper()
	if later, ok := moi.folderOnServer(laterFolder); ok {
		moi.expectNotOnServer(later, m.Subject, why)
	}
}

// expectReported checks that the "Prévus" tab of the Reporté screen shows this
// mail: its subject, and its sender, which the row shows too.
func expectReported(moi *imapPerson, m incoming) {
	app := moi.app
	app.t.Helper()
	row, ok := moi.reportedRow(m.Subject)
	if !ok {
		app.failf("les mails reportés de %s devraient montrer le mail %q, ils ne le montrent pas (ils montrent : %s)", moi.email, m.Subject, describeReported(moi.reported()))
		return
	}
	want, err := mail.ParseAddress(m.From)
	if err != nil {
		app.fatalf("l'expéditeur %q du mail %q n'est pas une adresse lisible (à corriger dans le scénario)", m.From, m.Subject)
	}
	if !strings.Contains(strings.ToLower(row.From), strings.ToLower(want.Address)) {
		app.failf("dans les mails reportés de %s, le mail %q devrait montrer son expéditeur %s, il montre %q", moi.email, m.Subject, want.Address, row.From)
	}
}

// expectNotReported checks that the "Prévus" tab of the Reporté screen does
// not show this mail. why says why it must not.
func expectNotReported(moi *imapPerson, m incoming, why string) {
	app := moi.app
	app.t.Helper()
	if _, ok := moi.reportedRow(m.Subject); ok {
		app.failf("les mails reportés de %s ne devraient pas montrer le mail %q (%s), ils le montrent", moi.email, m.Subject, why)
	}
}

// expectUnreadInServerInbox checks that the inbox of the server holds this
// mail and that it is unread there, as another mail program shows it.
func expectUnreadInServerInbox(moi *imapPerson, m incoming) {
	app := moi.app
	app.t.Helper()
	for _, s := range moi.onServer(serverInbox) {
		if s != m.Subject {
			continue
		}
		if moi.seenOnServer(serverInbox, m.Subject) {
			app.failf("le mail %q est de retour dans la boîte de réception du serveur IMAP de %s, mais marqué lu : il devrait être non lu", m.Subject, moi.email)
		}
		return
	}
	app.failf("le mail %q devrait être de retour dans la boîte de réception du serveur IMAP de %s, il n'y est pas (elle contient : %q)", m.Subject, moi.email, moi.onServer(serverInbox))
}

func TestScenarios_IMAPSnooze(t *testing.T) {
	playScenarios(t, "Reporter un mail sur une boîte IMAP", []scenario{
		{
			phrase: "Sur une boîte IMAP, reporter un mail le sort de ta boîte de réception et le range dans le dossier « Plus tard » de ta boîte, créé s'il n'existait pas ; il apparaît dans tes mails reportés.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder, travelOffer)
				// Where the sentence starts: no "Plus tard" folder yet, so
				// the first report has to create it.
				if _, ok := moi.folderOnServer(laterFolder); ok {
					app.fatalf("le serveur IMAP de %s a déjà un dossier « %s » avant le premier report : le scénario ne prouve pas qu'il est créé (à corriger dans acceptance_imap_test.go)", moi.email, laterFolder)
				}

				// The first report creates the folder, the second finds it
				// there: both have to go through.
				for i, m := range []incoming{shopOrder, travelOffer} {
					uid := uids[i+1]
					if got := moi.snoozeAnswer(uid); got.code != http.StatusOK {
						app.failf("reporter le mail n°%s %q de %s devrait répondre 200, le serveur a répondu %s", uid, m.Subject, moi.email, got)
					}
				}

				for _, m := range []incoming{shopOrder, travelOffer} {
					// Out of the inbox: on the screen as it loads again,
					// without a sync, and on the server.
					moi.expectDoesNotShow(m.Subject, "tu l'as reporté")
					moi.expectNotOnServer(serverInbox, m.Subject, "tu l'as reporté")
					expectInLater(moi, m)
					expectReported(moi, m)
				}

				// The control: the mail nobody reported stays where it was.
				moi.expectShows(uids[0], bankStatement.Subject)
				moi.expectOnServer(serverInbox, bankStatement.Subject)
				expectNotReported(moi, bankStatement, "personne ne l'a reporté")
			},
		},
		{
			phrase: "Sur une boîte IMAP, à l'heure prévue, un mail reporté revient tout seul dans ta boîte de réception, marqué non lu, et quitte tes mails reportés.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)
				// Read first, in the reader: "marqué non lu" proves nothing
				// on a mail that was never read.
				moi.openMail(uids[1])
				if !moi.seenOnServer(serverInbox, shopOrder.Subject) {
					app.fatalf("le mail n°%s %q de %s devrait être lu sur le serveur une fois ouvert dans la liseuse, il ne l'est pas : le scénario ne prouve rien", uids[1], shopOrder.Subject, moi.email)
				}
				reportedOnIMAP(moi, uids[1], shopOrder)

				moi.timeComesForSnoozes()

				// On the server, right after the sweep: back in the inbox,
				// unread, and out of "Plus tard".
				expectUnreadInServerInbox(moi, shopOrder)
				expectNotInLater(moi, shopOrder, "son heure est venue, il est revenu dans la boîte de réception")
				expectNotReported(moi, shopOrder, "son heure est venue")

				// On the inbox screen, once it syncs: once, and unread.
				moi.sync()
				moi.expectBackInInbox(shopOrder.Subject, "à l'heure prévue, après une synchronisation")
				if row, ok := rowBySubject(moi.inbox(), shopOrder.Subject); ok && row.IsRead {
					app.failf("à l'heure prévue, la boîte de réception de %s montre le mail %q marqué lu : il devrait être non lu", moi.email, shopOrder.Subject)
				}
				// The control: the mail nobody reported is still there.
				moi.expectShows(uids[0], bankStatement.Subject)
			},
		},
		{
			phrase: "Sur une boîte IMAP, « Réactiver » sur un mail reporté le remet tout de suite dans ta boîte de réception, marqué non lu.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)
				// Read first, in the reader: "marqué non lu" proves nothing
				// on a mail that was never read.
				moi.openMail(uids[1])
				if !moi.seenOnServer(serverInbox, shopOrder.Subject) {
					app.fatalf("le mail n°%s %q de %s devrait être lu sur le serveur une fois ouvert dans la liseuse, il ne l'est pas : le scénario ne prouve rien", uids[1], shopOrder.Subject, moi.email)
				}
				reportedOnIMAP(moi, uids[1], shopOrder)
				row, ok := moi.reportedRow(shopOrder.Subject)
				if !ok {
					app.fatalf("les mails reportés de %s devraient montrer le mail %q, ils ne le montrent pas (ils montrent : %s) : il n'y a pas de « Réactiver » à presser", moi.email, shopOrder.Subject, describeReported(moi.reported()))
				}

				if got := moi.wakeNowAnswer(row); got.code != http.StatusOK {
					app.failf("« Réactiver » sur le mail reporté %q de %s devrait répondre 200, le serveur a répondu %s", shopOrder.Subject, moi.email, got)
				}

				// What the inbox shows right after, without a sync: the mail,
				// once, and unread, as the confirmation "Ramener maintenant"
				// promises.
				moi.expectBackInInbox(shopOrder.Subject, "juste après « Réactiver »")
				if shown, ok := rowBySubject(moi.inbox(), shopOrder.Subject); ok && shown.IsRead {
					app.failf("juste après « Réactiver », la boîte de réception de %s montre le mail %q marqué lu : il devrait être non lu", moi.email, shopOrder.Subject)
				}
				// The server: back in the inbox, unread, out of "Plus tard".
				expectUnreadInServerInbox(moi, shopOrder)
				expectNotInLater(moi, shopOrder, "« Réactiver » l'a remis dans la boîte de réception")
				// The control: the mail nobody reported is still there.
				moi.expectShows(uids[0], bankStatement.Subject)
			},
		},
		{
			phrase: "Sur une boîte IMAP, quand tu reportes une sélection, les mails des expéditeurs protégés ne sont pas reportés et restent dans ta boîte.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)
				moi.protect("banque.fr")

				got := moi.snoozeSelectionAnswer(uids...)
				if got.code != http.StatusOK {
					app.failf("reporter la sélection des mails n°%s de %s devrait répondre 200, le serveur a répondu %s", strings.Join(uids, " et n°"), moi.email, got)
				} else {
					// What the inbox tells after it: "1 protégé ignoré", and
					// it takes off the screen only the mails the answer says
					// were reported (runBulkSnooze in Inbox.js).
					if n := app.number(got.body, "protectedSkipped"); n != 1 {
						app.failf("le report de la sélection devrait annoncer 1 mail protégé épargné, il en annonce %d", n)
					}
					snoozed, _ := got.body["snoozed"].([]interface{})
					if len(snoozed) != 1 || snoozed[0] != uids[1] {
						app.failf("le report de la sélection devrait dire reporté le seul mail n°%s %q, il dit reportés : %v", uids[1], shopOrder.Subject, got.body["snoozed"])
					}
				}

				// The protected sender's mail: still in the inbox, on the
				// screen and on the server, and not reported.
				moi.expectShows(uids[0], bankStatement.Subject)
				moi.expectOnServer(serverInbox, bankStatement.Subject)
				expectNotInLater(moi, bankStatement, "son expéditeur est protégé")
				expectNotReported(moi, bankStatement, "son expéditeur est protégé")

				// The control: the other mail of the selection is reported.
				moi.expectDoesNotShow(shopOrder.Subject, "tu l'as reporté")
				moi.expectNotOnServer(serverInbox, shopOrder.Subject, "tu l'as reporté")
				expectInLater(moi, shopOrder)
				expectReported(moi, shopOrder)
			},
		},
		{
			phrase: "Sur une boîte IMAP, un mail reporté qu'un autre logiciel a sorti du dossier « Plus tard » n'est pas déplacé à l'heure prévue, et quitte tes mails reportés.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder, travelOffer)
				later := reportedOnIMAP(moi, uids[1], shopOrder)
				reportedOnIMAP(moi, uids[2], travelOffer)

				// On their phone, the person files the first one in a folder
				// of their own before its hour.
				moi.newFolderElsewhere(ownFolder)
				moi.movedFromElsewhere(later, shopOrder.Subject, ownFolder)

				moi.timeComesForSnoozes()

				// Where they put it, and nowhere else: not in the inbox of
				// the server, nor on the screen as it loads again.
				moi.expectOnServer(ownFolder, shopOrder.Subject)
				moi.expectNotOnServer(serverInbox, shopOrder.Subject, "un autre logiciel l'avait sorti du dossier « Plus tard » pour le ranger dans "+ownFolder)
				moi.expectDoesNotShow(shopOrder.Subject, "un autre logiciel l'a rangé dans "+ownFolder)
				expectNotReported(moi, shopOrder, "il n'est plus dans le dossier « Plus tard », il n'y a plus rien à faire revenir")

				// The control: the other reported mail, still in "Plus tard",
				// does come back at the same hour. Without it, a sweep that
				// moves nothing would pass the lines above.
				moi.expectOnServer(serverInbox, travelOffer.Subject)
				expectNotInLater(moi, travelOffer, "son heure est venue, il est revenu dans la boîte de réception")
				expectNotReported(moi, travelOffer, "son heure est venue")
			},
		},
		{
			phrase: "Sur une boîte IMAP, un mail reporté n'apparaît pas dans ta boîte de réception, même après une synchronisation, jusqu'à son réveil.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)
				reportedOnIMAP(moi, uids[1], shopOrder)

				// Right after, as the inbox loads again without a sync.
				moi.expectDoesNotShow(shopOrder.Subject, "tu l'as reporté, juste avant")
				// After a sync, then a second one: still not there.
				moi.sync()
				moi.expectDoesNotShow(shopOrder.Subject, "tu l'as reporté, et sa synchronisation suivante ne doit pas le ramener")
				moi.sync()
				moi.expectDoesNotShow(shopOrder.Subject, "tu l'as reporté, et une deuxième synchronisation ne doit pas le ramener")
				expectInLater(moi, shopOrder)
				// The control: the mail nobody reported is still there.
				moi.expectShows(uids[0], bankStatement.Subject)

				// Until its hour: then it is back, once the inbox syncs.
				moi.timeComesForSnoozes()
				moi.sync()
				moi.expectBackInInbox(shopOrder.Subject, "après son réveil et une synchronisation")
			},
		},
		{
			phrase: "« Annuler » sur un report depuis l'Historique remet le mail dans ta boîte de réception et le retire de tes mails reportés : à l'heure prévue, rien ne bouge plus.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)
				// Read first, so a return at the hour, which marks unread,
				// would show.
				moi.openMail(uids[1])
				reportedOnIMAP(moi, uids[1], shopOrder)

				// The line the Historique screen shows for the report.
				line := moi.historyLineFor(uids[1], "archive")
				if line.Source != SourceSnooze {
					app.fatalf("la ligne « archive » du mail n°%s %q de %s devrait venir du report, elle vient de %q : le scénario ne prouve rien", uids[1], shopOrder.Subject, moi.email, line.Source)
				}
				if !line.Undoable {
					app.failf("l'historique de %s devrait proposer « Annuler » sur la ligne du report du mail n°%s %q, il ne le propose pas", moi.email, uids[1], shopOrder.Subject)
				}

				// "Annuler" on it (handleUndo in History.js).
				if got := moi.undoFromHistory(line); got.code != http.StatusOK {
					app.fatalf("« Annuler » sur la ligne du report du mail n°%s %q de %s devrait répondre 200, le serveur a répondu %s", uids[1], shopOrder.Subject, moi.email, got)
				}
				moi.expectBackInInbox(shopOrder.Subject, "juste après « Annuler » sur son report")
				moi.expectOnServer(serverInbox, shopOrder.Subject)
				expectNotInLater(moi, shopOrder, "« Annuler » l'a remis dans la boîte de réception")
				expectNotReported(moi, shopOrder, "« Annuler » sur sa ligne de l'Historique a défait son report")

				// Its hour passes: nothing moves any more, nothing is marked
				// unread, nothing is written in the history.
				before := moi.historyScreen()
				inInbox := func() bool {
					for _, s := range moi.onServer(serverInbox) {
						if s == shopOrder.Subject {
							return true
						}
					}
					return false
				}
				wasSeen := inInbox() && moi.seenOnServer(serverInbox, shopOrder.Subject)
				moi.reportsHourPasses()
				moi.expectOnServer(serverInbox, shopOrder.Subject)
				expectNotInLater(moi, shopOrder, "son report est annulé, l'heure prévue ne doit plus le déplacer")
				moi.expectBackInInbox(shopOrder.Subject, "à l'heure prévue du report annulé")
				if inInbox() {
					if seen := moi.seenOnServer(serverInbox, shopOrder.Subject); seen != wasSeen {
						app.failf("à l'heure prévue du report annulé, le mail %q ne devrait pas changer sur le serveur IMAP de %s : il était lu=%v, il est lu=%v", shopOrder.Subject, moi.email, wasSeen, seen)
					}
				}
				if after := moi.historyScreen(); len(after) != len(before) {
					app.failf("à l'heure prévue du report annulé, l'historique de %s ne devrait rien montrer de nouveau, il montre %d ligne(s) de plus, la plus récente : %q (%s)", moi.email, len(after)-len(before), after[0].Action, after[0].Source)
				}
				// The control: the mail nobody reported is still there.
				moi.expectShows(uids[0], bankStatement.Subject)
			},
		},
	})
}
