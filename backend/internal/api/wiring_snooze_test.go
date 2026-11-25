//go:build acceptance

package api

// Reporting a mail on an IMAP mailbox, around the locked sentences of
// scenarios_1_imap_snooze_test.go: what the background sweep costs (one
// connection per mailbox, whatever the number of mails due) and what it does
// when the mailbox fails it (the report waits for the next pass, counted once
// or not at all, and comes back once), "Annuler" on a report, which ends it,
// and "Réactiver" while the sweep holds a report, on both transports. Wiring, like wiring_imap_test.go: on
// the acceptance harness (a real MongoDB, a real IMAP server over TLS), not in
// the report. `scripts/scenarios.sh -run '^TestAcceptance'` runs them, and so
// does CI, in the scenarios job.

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// countingTLSProxy sits between Mailsorter and a person's IMAP server, both
// sides over TLS, and counts the connections Mailsorter opens. It returns its
// port and the count so far.
func countingTLSProxy(a *testApp, serverPort int) (int, func() int64) {
	a.t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", imapServerTLS)
	if err != nil {
		a.fatalf("démarrage du relais vers le serveur IMAP : %v", err)
	}
	a.t.Cleanup(func() { _ = ln.Close() })
	var conns atomic.Int64
	upstream := fmt.Sprintf("127.0.0.1:%d", serverPort)
	go func() {
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}
			conns.Add(1)
			go func() {
				defer client.Close()
				server, err := tls.Dial("tcp", upstream, &tls.Config{ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12})
				if err != nil {
					return
				}
				defer server.Close()
				go func() {
					_, _ = io.Copy(server, client)
					_ = server.Close()
				}()
				_, _ = io.Copy(client, server)
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, conns.Load
}

// reachThrough makes Mailsorter reach this person's mailbox on port rather
// than on their server's own: a relay, or nothing at all.
func (p *imapPerson) reachThrough(port int) {
	a := p.app
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := a.h.db.MailAccounts().UpdateOne(ctx, bson.M{"userId": p.email}, bson.M{"$set": bson.M{"port": port}}); err != nil {
		a.fatalf("changement du port de la boîte de %s : %v", p.email, err)
	}
}

// closedPort is a port on 127.0.0.1 nothing listens on any more.
func closedPort(a *testApp) int {
	a.t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		a.fatalf("réservation d'un port : %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// reportDue moves the hour of this person's scheduled reports into the past,
// without running the sweep: for a test that runs it itself, once for
// several people, or more than once.
func (p *imapPerson) reportDue() {
	a := p.app
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := a.h.db.Snoozes().UpdateMany(ctx,
		bson.M{"userId": p.email, "status": "scheduled"},
		bson.M{"$set": bson.M{"wakeAt": time.Now().Add(-time.Minute)}})
	if err != nil {
		a.fatalf("avance de l'heure des reports de %s : %v", p.email, err)
	}
	if res.MatchedCount == 0 {
		a.fatalf("aucun report de %s n'est prévu : le test ne prouve rien", p.email)
	}
}

// storedReport is the report of the mail with this subject, as stored.
func (p *imapPerson) storedReport(subject string) models.Snooze {
	a := p.app
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var s models.Snooze
	if err := a.h.db.Snoozes().FindOne(ctx, bson.M{"userId": p.email, "subject": subject}).Decode(&s); err != nil {
		a.fatalf("le report du mail %q de %s n'est pas en base : %v", subject, p.email, err)
	}
	return s
}

// returnsJournaled counts the returns the history holds for the mail
// numbered uid in the inbox: the "unarchive" a report's return writes.
func (p *imapPerson) returnsJournaled(uid string) int {
	p.app.t.Helper()
	n := 0
	for _, l := range p.history(uid) {
		if l.Action == "unarchive" && l.Source == SourceSnooze {
			n++
		}
	}
	return n
}

// Every report due on a mailbox comes back through ONE connection to it,
// whatever their number, and each mailbox gets its own: the sweep runs every
// minute, and a connection per mail would be a login per mail.
func TestAcceptance_IMAPSnoozeWakesEachMailboxThroughOneConnection(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder, travelOffer)
	autre := app.imapPerson("autre-boite@exemple.org")
	autreUIDs := autre.receive(shopOrder)
	autre.sync()
	for i, m := range []incoming{bankStatement, shopOrder, travelOffer} {
		reportedOnIMAP(moi, uids[i], m)
	}
	reportedOnIMAP(autre, autreUIDs[0], shopOrder)

	myPort, myConns := countingTLSProxy(app, moi.port)
	moi.reachThrough(myPort)
	otherPort, otherConns := countingTLSProxy(app, autre.port)
	autre.reachThrough(otherPort)
	moi.reportDue()
	autre.reportDue()

	app.h.wakeDueSnoozes()

	if n := myConns(); n != 1 {
		app.failf("le réveil de 3 reports de %s devrait ouvrir 1 connexion à sa boîte, il en a ouvert %d", moi.email, n)
	}
	if n := otherConns(); n != 1 {
		app.failf("le réveil du report de %s devrait ouvrir 1 connexion à sa boîte, il en a ouvert %d", autre.email, n)
	}
	for _, m := range []incoming{bankStatement, shopOrder, travelOffer} {
		expectUnreadInServerInbox(moi, m)
		expectNotReported(moi, m, "son heure est venue")
	}
	expectUnreadInServerInbox(autre, shopOrder)
	expectNotReported(autre, shopOrder, "son heure est venue")
}

// A mailbox out of reach at the hour of a report: the report stays scheduled,
// uncounted, and the mail where it waits; the next pass, the mailbox back,
// brings it back, once.
func TestAcceptance_IMAPSnoozeWaitsForAMailboxOutOfReach(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)
	later := reportedOnIMAP(moi, uids[1], shopOrder)
	moi.reportDue()

	moi.reachThrough(closedPort(app))
	app.h.wakeDueSnoozes()

	expectReported(moi, shopOrder)
	if s := moi.storedReport(shopOrder.Subject); s.Attempts != 0 || s.Status != "scheduled" {
		app.failf("une boîte injoignable à l'heure du report devrait le laisser prévu sans compter d'essai, il est %q après %d essai(s)", s.Status, s.Attempts)
	}
	moi.expectOnServer(later, shopOrder.Subject)
	moi.expectNotOnServer(serverInbox, shopOrder.Subject, "sa boîte était injoignable")

	moi.reachThrough(moi.port)
	app.h.wakeDueSnoozes()

	expectUnreadInServerInbox(moi, shopOrder)
	expectNotReported(moi, shopOrder, "la boîte est de nouveau joignable, il est revenu")
	if n := moi.returnsJournaled(uids[1]); n != 1 {
		app.failf("le retour du mail %q devrait être écrit 1 fois dans l'historique, il l'est %d fois", shopOrder.Subject, n)
	}
}

// A mailbox that refuses the return of a report (here its folder "Plus tard"
// cannot be opened for a while): the attempt counts once, the report stays
// scheduled, and the next pass, the folder back, brings the mail back, once.
func TestAcceptance_IMAPSnoozeTriesAFailedReturnAgain(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)
	later := reportedOnIMAP(moi, uids[1], shopOrder)
	moi.reportDue()

	const aside = "Plus tard (mis de côté)"
	rename := func(from, to string) {
		c := moi.otherProgram()
		defer c.Close()
		if err := c.Rename(from, to, nil).Wait(); err != nil {
			app.fatalf("un autre logiciel n'a pas pu renommer le dossier %q en %q : %v", from, to, err)
		}
	}
	rename(later, aside)
	app.h.wakeDueSnoozes()

	expectReported(moi, shopOrder)
	if s := moi.storedReport(shopOrder.Subject); s.Attempts != 1 || s.Status != "scheduled" {
		app.failf("un retour refusé par la boîte devrait compter 1 essai et laisser le report prévu, il est %q après %d essai(s)", s.Status, s.Attempts)
	}
	if n := moi.returnsJournaled(uids[1]); n != 0 {
		app.failf("un retour refusé ne devrait rien écrire dans l'historique, il y écrit %d retour(s)", n)
	}

	rename(aside, later)
	app.h.wakeDueSnoozes()

	expectUnreadInServerInbox(moi, shopOrder)
	expectNotReported(moi, shopOrder, "le dossier est revenu, le mail aussi")
	if n := moi.returnsJournaled(uids[1]); n != 1 {
		app.failf("le retour du mail %q devrait être écrit 1 fois dans l'historique, il l'est %d fois", shopOrder.Subject, n)
	}
	// Once, on the screen too, after a sync: the stored copy took its new
	// number, so the sync finds it known rather than new.
	moi.sync()
	moi.expectBackInInbox(shopOrder.Subject, "après le second passage et une synchronisation")
}

// "Réactiver" on a report the sweep holds at that very moment moves nothing a
// second time: it is told to try again, and the mail comes back once.
func TestAcceptance_IMAPSnoozeReactivateWhileTheSweepHoldsIt(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)
	reportedOnIMAP(moi, uids[1], shopOrder)
	row, ok := moi.reportedRow(shopOrder.Subject)
	if !ok {
		app.fatalf("le report du mail %q n'apparaît pas dans les mails reportés de %s", shopOrder.Subject, moi.email)
	}

	// The sweep's claim, as it sets it before moving the mail.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s := moi.storedReport(shopOrder.Subject)
	if _, err := app.h.db.Snoozes().UpdateOne(ctx, bson.M{"subject": shopOrder.Subject, "userId": moi.email},
		bson.M{"$set": bson.M{"wakingAt": time.Now()}}); err != nil {
		app.fatalf("pose de la réservation du report : %v", err)
	}

	if got := moi.wakeNowAnswer(row); got.code != http.StatusLocked {
		app.failf("« Réactiver » sur un report que le réveil tient déjà devrait répondre 423, il a répondu %s", got)
	}
	moi.expectNotOnServer(serverInbox, shopOrder.Subject, "le réveil le tient, « Réactiver » ne doit rien déplacer")

	// The claim released (the sweep done with it), "Réactiver" goes through.
	oid, err := primitive.ObjectIDFromHex(s.ID)
	if err != nil {
		app.fatalf("identifiant du report illisible %q : %v", s.ID, err)
	}
	if _, err := app.h.db.Snoozes().UpdateOne(ctx, bson.M{"_id": oid},
		bson.M{"$unset": bson.M{"wakingAt": ""}}); err != nil {
		app.fatalf("levée de la réservation du report : %v", err)
	}
	if got := moi.wakeNowAnswer(row); got.code != http.StatusOK {
		app.failf("« Réactiver » une fois la réservation levée devrait répondre 200, il a répondu %s", got)
	}
	// A second "Réactiver" from a screen left open: answered like the first,
	// nothing moved nor written again.
	if got := moi.wakeNowAnswer(row); got.code != http.StatusOK {
		app.failf("un second « Réactiver » sur un mail déjà revenu devrait répondre 200, il a répondu %s", got)
	}
	expectUnreadInServerInbox(moi, shopOrder)
	if n := moi.returnsJournaled(uids[1]); n != 1 {
		app.failf("le retour du mail %q devrait être écrit 1 fois dans l'historique, il l'est %d fois", shopOrder.Subject, n)
	}
}

// The report records where the mail waits: the folder, its number there and
// its Message-ID. The number may be unknown (a server that does not say where
// a move landed): the mail is then found by its Message-ID at its hour, and
// comes back all the same, once on the screen.
func TestAcceptance_IMAPSnoozeFindsTheMailByItsMessageID(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)
	later := reportedOnIMAP(moi, uids[1], shopOrder)

	s := moi.storedReport(shopOrder.Subject)
	if s.Folder != later || s.FolderUID == "" || s.HeaderMessageID == "" {
		app.fatalf("le report du mail %q devrait dire où il attend (dossier %q, son numéro, son Message-ID), il dit dossier %q, numéro %q, Message-ID %q",
			shopOrder.Subject, later, s.Folder, s.FolderUID, s.HeaderMessageID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := app.h.db.Snoozes().UpdateOne(ctx, bson.M{"userId": moi.email, "subject": shopOrder.Subject},
		bson.M{"$set": bson.M{"folderUid": ""}}); err != nil {
		app.fatalf("effacement du numéro du report : %v", err)
	}

	moi.timeComesForSnoozes()

	expectUnreadInServerInbox(moi, shopOrder)
	expectNotInLater(moi, shopOrder, "retrouvé par son Message-ID, il est revenu")
	expectNotReported(moi, shopOrder, "son heure est venue")
	moi.sync()
	moi.expectBackInInbox(shopOrder.Subject, "après son retour et une synchronisation")
}

// ---------------------------------------------------------------------------
// "Annuler" on a report ends it
// ---------------------------------------------------------------------------

// holdReport is the sweep's claim on the scheduled report of the mail with
// this subject, of this account, as it sets it before moving the mail
// (claimReport), and release takes it off again.
func holdReport(a *testApp, userEmail, subject string) (release func()) {
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	filter := bson.M{"userId": userEmail, "subject": subject, "status": "scheduled"}
	res, err := a.h.db.Snoozes().UpdateOne(ctx, filter, bson.M{"$set": bson.M{"wakingAt": time.Now()}})
	if err != nil {
		a.fatalf("pose de la réservation du report du mail %q de %s : %v", subject, userEmail, err)
	}
	if res.MatchedCount != 1 {
		a.fatalf("pose de la réservation du report du mail %q de %s : %d report(s) prévu(s) trouvé(s), attendu 1", subject, userEmail, res.MatchedCount)
	}
	return func() {
		a.t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := a.h.db.Snoozes().UpdateOne(ctx, filter, bson.M{"$unset": bson.M{"wakingAt": ""}}); err != nil {
			a.fatalf("levée de la réservation du report du mail %q de %s : %v", subject, userEmail, err)
		}
	}
}

// reportStatus is the status of the report of the mail with this subject, of
// this account, as stored.
func reportStatus(a *testApp, userEmail, subject string) string {
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var s models.Snooze
	if err := a.h.db.Snoozes().FindOne(ctx, bson.M{"userId": userEmail, "subject": subject}).Decode(&s); err != nil {
		a.fatalf("le report du mail %q de %s n'est pas en base : %v", subject, userEmail, err)
	}
	return s.Status
}

// scheduledSubjects is the "Prévus" tab of the Gmail person, by subject.
func scheduledSubjects(a *testApp) []string {
	a.t.Helper()
	code, out := a.call(http.MethodGet, "/api/snoozes?status=scheduled", nil)
	if code != http.StatusOK {
		a.fatalf("les mails reportés ne s'affichent pas : GET /api/snoozes a répondu %d : %v", code, out)
	}
	rows, _ := out["snoozes"].([]interface{})
	subjects := make([]string, 0, len(rows))
	for _, r := range rows {
		if row, ok := r.(map[string]interface{}); ok {
			subject, _ := row["subject"].(string)
			subjects = append(subjects, subject)
		}
	}
	return subjects
}

// "Annuler" on the line of a report, in the history of an IMAP mailbox: the
// mail is back in the inbox, and the report leaves the scheduled ones at once
// (cancelled), with no return of its own in the history. While the sweep
// holds the report, nothing moves and the user is told to try again.
func TestAcceptance_IMAPSnoozeUndoneFromTheHistoryEndsTheReport(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)
	later := reportedOnIMAP(moi, uids[1], shopOrder)
	line := moi.historyLineFor(uids[1], "archive")
	if line.Source != SourceSnooze {
		app.fatalf("la ligne « archive » du mail %q devrait venir du report, elle vient de %q", shopOrder.Subject, line.Source)
	}

	release := holdReport(app, moi.email, shopOrder.Subject)
	if got := moi.undoFromHistory(line); got.code != http.StatusLocked {
		app.failf("« Annuler » sur un report que le réveil tient déjà devrait répondre 423, il a répondu %s", got)
	}
	moi.expectOnServer(later, shopOrder.Subject)
	moi.expectNotOnServer(serverInbox, shopOrder.Subject, "le réveil le tient, « Annuler » ne doit rien déplacer")
	expectReported(moi, shopOrder)
	release()

	if got := moi.undoFromHistory(line); got.code != http.StatusOK {
		app.fatalf("« Annuler » sur la ligne du report devrait répondre 200, il a répondu %s", got)
	}
	moi.expectOnServer(serverInbox, shopOrder.Subject)
	expectNotInLater(moi, shopOrder, "« Annuler » l'a remis dans la boîte de réception")
	expectNotReported(moi, shopOrder, "« Annuler » a annulé son report")
	if status := reportStatus(app, moi.email, shopOrder.Subject); status != "cancelled" {
		app.failf("le report annulé du mail %q devrait être « cancelled », il est %q", shopOrder.Subject, status)
	}
	if n := moi.returnsJournaled(uids[1]); n != 0 {
		app.failf("un report annulé ne devrait écrire aucun retour à lui dans l'historique, il en écrit %d", n)
	}
	moi.expectBackInInbox(shopOrder.Subject, "juste après « Annuler »")
	moi.sync()
	moi.expectBackInInbox(shopOrder.Subject, "après « Annuler » et une synchronisation")
}

// "Annuler" right after, the single-message route with the verb that undoes
// the move, on a reported mail of an IMAP mailbox: the same end.
func TestAcceptance_IMAPSnoozeUndoneRightAfterEndsTheReport(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)
	reportedOnIMAP(moi, uids[1], shopOrder)

	if got := moi.undoRightAfter(uids[1], "archive"); got.code != http.StatusOK {
		app.fatalf("« Annuler » juste après le report devrait répondre 200, il a répondu %s", got)
	}
	moi.expectOnServer(serverInbox, shopOrder.Subject)
	expectNotReported(moi, shopOrder, "« Annuler » a annulé son report")
	if status := reportStatus(app, moi.email, shopOrder.Subject); status != "cancelled" {
		app.failf("le report annulé du mail %q devrait être « cancelled », il est %q", shopOrder.Subject, status)
	}
	moi.expectBackInInbox(shopOrder.Subject, "juste après « Annuler »")
}

// The same over the Gmail API: "Annuler" on the line of a report brings the
// mail back and ends the report, so its hour neither moves it nor marks it
// unread again; while the sweep holds it, nothing is done.
func TestAcceptance_GmailSnoozeUndoneFromTheHistoryEndsTheReport(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	ids := app.receiveAndSync(incoming{From: "Garage <rdv@garage.fr>", Subject: "Votre rendez-vous"})
	app.act(ids[0], "read")
	app.snooze(ids[0])
	line := app.historyLineFor(ids[0], "archive")

	release := holdReport(app, scenarioUser, "Votre rendez-vous")
	if code, out := app.call(http.MethodPost, "/api/activity/undo", map[string]string{"id": line.ID}); code != http.StatusLocked {
		app.failf("« Annuler » sur un report que le réveil tient déjà devrait répondre 423, il a répondu %d : %v", code, out)
	}
	app.expectNotInInbox(ids[0])
	release()

	app.mustCall(http.MethodPost, "/api/activity/undo", map[string]string{"id": line.ID}, http.StatusOK)
	app.expectInInbox(ids[0])
	for _, subject := range scheduledSubjects(app) {
		if subject == "Votre rendez-vous" {
			app.failf("« Annuler » sur la ligne du report devrait le sortir des mails reportés, ils le montrent encore")
		}
	}
	if status := reportStatus(app, scenarioUser, "Votre rendez-vous"); status != "cancelled" {
		app.failf("le report annulé devrait être « cancelled », il est %q", status)
	}

	// Its hour comes: nothing is due any more, so nothing marks it unread.
	app.timeComesForSnoozes()
	for _, l := range app.history(ids[0]) {
		if l.Action == "unarchive" && l.Source == SourceSnooze {
			app.failf("un report annulé ne devrait pas revenir à son heure, l'historique montre son retour")
		}
	}
}

// scheduledReportID is the id of the Gmail person's scheduled report of the
// mail with this subject, as the "Prévus" tab holds it for "Réactiver".
func scheduledReportID(a *testApp, subject string) string {
	a.t.Helper()
	code, out := a.call(http.MethodGet, "/api/snoozes?status=scheduled", nil)
	if code != http.StatusOK {
		a.fatalf("les mails reportés ne s'affichent pas : GET /api/snoozes a répondu %d : %v", code, out)
	}
	rows, _ := out["snoozes"].([]interface{})
	for _, r := range rows {
		if row, ok := r.(map[string]interface{}); ok && row["subject"] == subject {
			id, _ := row["id"].(string)
			return id
		}
	}
	a.fatalf("les mails reportés ne montrent pas le mail %q : il n'y a pas de « Réactiver » à presser", subject)
	return ""
}

// "Réactiver" over the Gmail API takes the report the way the sweep does:
// pressed while the sweep holds it, nothing is done and the user is told to
// try again; pressed again once the mail is back, nothing more is done. The
// history holds one return, never two.
func TestAcceptance_GmailSnoozeReactivateWhileTheSweepHoldsIt(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	const subject = "Votre rendez-vous"
	ids := app.receiveAndSync(incoming{From: "Garage <rdv@garage.fr>", Subject: subject})
	app.act(ids[0], "read")
	app.snooze(ids[0])
	id := scheduledReportID(app, subject)
	reactivate := func() (int, map[string]interface{}) {
		return app.call(http.MethodPost, "/api/snoozes/"+id+"/wake", nil)
	}
	returns := func() int {
		n := 0
		for _, l := range app.history(ids[0]) {
			if l.Action == "unarchive" && l.Source == SourceSnooze {
				n++
			}
		}
		return n
	}

	release := holdReport(app, scenarioUser, subject)
	if code, out := reactivate(); code != http.StatusLocked {
		app.failf("« Réactiver » sur un report que le réveil tient déjà devrait répondre 423, il a répondu %d : %v", code, out)
	}
	app.expectNotInInbox(ids[0])
	if n := returns(); n != 0 {
		app.failf("« Réactiver » refusé ne devrait écrire aucun retour dans l'historique, il en écrit %d", n)
	}
	release()

	if code, out := reactivate(); code != http.StatusOK {
		app.fatalf("« Réactiver » une fois la réservation levée devrait répondre 200, il a répondu %d : %v", code, out)
	}
	app.expectInInbox(ids[0])
	app.expectUnread(ids[0])
	if status := reportStatus(app, scenarioUser, subject); status != "done" {
		app.failf("le report réactivé devrait être « done », il est %q", status)
	}
	// The sweep woke it a moment before a second press: answered like the
	// first, nothing written again.
	if code, out := reactivate(); code != http.StatusOK {
		app.failf("un second « Réactiver » sur un mail déjà revenu devrait répondre 200, il a répondu %d : %v", code, out)
	}
	if n := returns(); n != 1 {
		app.failf("le retour du mail %q devrait être écrit 1 fois dans l'historique, il l'est %d fois", subject, n)
	}
}
