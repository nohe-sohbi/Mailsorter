//go:build acceptance

package api

// Reporting a mail on an IMAP mailbox, around the locked sentences of
// scenarios_1_imap_snooze_test.go: what the background sweep costs (one
// connection per mailbox, whatever the number of mails due); what it does
// when a mailbox fails it (silent at LOGIN, unavailable, out of reach, a
// connection dropped, a return refused: the report waits for the next pass,
// counted once or not at all, comes back once, and the other accounts' come
// back all the same); a mail found back in the inbox by another way; a mail
// that could not be found again, refused; "Annuler" on a report, which ends
// it, and "Réactiver" while the sweep holds a report, on both transports.
// Wiring, like wiring_imap_test.go: on the acceptance harness (a real
// MongoDB, a real IMAP server over TLS), not in the report.
// `scripts/scenarios.sh -run '^TestAcceptance'` runs them, and so does CI, in
// the scenarios job.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	imapv2 "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
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

// ---------------------------------------------------------------------------
// A mailbox that fails the sweep
// ---------------------------------------------------------------------------

// shortSweepBound shortens the time the sweep gives each mailbox for the
// length of the test, so a mailbox that never answers costs a second, not 30.
func shortSweepBound(a *testApp) {
	a.t.Helper()
	before := snoozeAccountBound
	snoozeAccountBound = time.Second
	a.t.Cleanup(func() { snoozeAccountBound = before })
}

// loginServer is an IMAP server over TLS that greets, then answers LOGIN with
// answer (given the command's tag): nothing at all, a refusal, a BYE. It
// returns its port.
func loginServer(a *testApp, answer func(tag string, conn net.Conn)) int {
	a.t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", imapServerTLS)
	if err != nil {
		a.fatalf("démarrage d'un serveur IMAP de test : %v", err)
	}
	a.t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			a.t.Cleanup(func() { _ = conn.Close() })
			go func() {
				fmt.Fprint(conn, "* OK [CAPABILITY IMAP4rev1 IMAP4rev2 AUTH=PLAIN] ready\r\n")
				lines := bufio.NewReader(conn)
				for {
					line, err := lines.ReadString('\n')
					if err != nil {
						return
					}
					fields := strings.Fields(line)
					if len(fields) >= 2 && strings.EqualFold(fields[1], "LOGIN") {
						answer(fields[0], conn)
						continue
					}
					if len(fields) >= 2 {
						fmt.Fprintf(conn, "%s BAD unexpected\r\n", fields[0])
					}
				}
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// sweepWithin runs the sweep once and stops the test when it has not
// returned after limit: a sweep that never returns brings no report back,
// for anyone.
func sweepWithin(a *testApp, limit time.Duration) {
	a.t.Helper()
	finishesWithin(a, limit, "le réveil des reports (plus aucun report ne revient, pour aucun compte)", a.h.wakeDueSnoozes)
}

// A server that greets and then says nothing at LOGIN holds the sweep only for
// the time the sweep gives that mailbox, the opening of its session
// included: the pass ends, the report of another account due at the same
// pass comes back, and the silent mailbox's report waits, uncounted.
func TestAcceptance_IMAPSnoozeSweepGetsPastAMailboxSilentAtLogin(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	shortSweepBound(app)
	moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)
	reportedOnIMAP(moi, uids[1], shopOrder)
	autre := app.imapPerson("autre-boite@exemple.org")
	autreUIDs := autre.receive(travelOffer)
	autre.sync()
	reportedOnIMAP(autre, autreUIDs[0], travelOffer)

	moi.reachThrough(loginServer(app, func(string, net.Conn) {}))
	moi.reportDue()
	autre.reportDue()

	sweepWithin(app, 20*time.Second)

	expectUnreadInServerInbox(autre, travelOffer)
	expectNotReported(autre, travelOffer, "son heure est venue")
	expectReported(moi, shopOrder)
	if s := moi.storedReport(shopOrder.Subject); s.Attempts != 0 || s.Status != "scheduled" {
		app.failf("un report d'une boîte muette au LOGIN devrait rester prévu sans compter d'essai, il est %q après %d essai(s)", s.Status, s.Attempts)
	}
}

// A server that answers LOGIN "unavailable" did not refuse the password: the
// sync says the mailbox did not answer, never that the password was
// refused, and the sweep tries the report again at the next pass, uncounted.
func TestAcceptance_IMAPUnavailableAtLoginIsNotARefusedPassword(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)
	reportedOnIMAP(moi, uids[1], shopOrder)
	moi.reportDue()

	moi.reachThrough(loginServer(app, func(tag string, c net.Conn) {
		fmt.Fprintf(c, "%s NO [UNAVAILABLE] Temporary authentication failure\r\n", tag)
	}))

	code, out := moi.syncAnswer()
	msg, _ := out["error"].(string)
	if code != http.StatusBadGateway || msg != mailboxNoAnswerMessage || out["syncMessage"] != true {
		app.failf("« Synchroniser » sur une boîte indisponible au LOGIN devrait répondre 502 %q (syncMessage, que la boîte de réception affiche), il a répondu %d : %v", mailboxNoAnswerMessage, code, out)
	}

	if got := moi.snoozeAnswer(uids[0]); got.code != http.StatusBadGateway || got.body["error"] != mailboxNoAnswerMessage {
		app.failf("« Reporter » sur une boîte indisponible au LOGIN devrait répondre 502 %q, il a répondu %s", mailboxNoAnswerMessage, got)
	}

	sweepWithin(app, 20*time.Second)
	if s := moi.storedReport(shopOrder.Subject); s.Attempts != 0 || s.Status != "scheduled" {
		app.failf("un report d'une boîte indisponible devrait rester prévu sans compter d'essai, il est %q après %d essai(s)", s.Status, s.Attempts)
	}
}

// A password the provider really refuses is said in the words of the connect
// screen, by "Synchroniser" and by "Reporter" alike, with 428 (to be
// connected again), never a 401, which would sign the person out of
// Mailsorter.
func TestAcceptance_IMAPRefusedPasswordIsWordedAsOnTheConnectScreen(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, bankStatement)
	moi.reachThrough(loginServer(app, func(tag string, c net.Conn) {
		fmt.Fprintf(c, "%s NO [AUTHENTICATIONFAILED] Invalid credentials\r\n", tag)
	}))

	code, out := moi.syncAnswer()
	if code != http.StatusPreconditionRequired || out["error"] != mailboxRejectedMessage {
		app.failf("« Synchroniser » avec un mot de passe refusé devrait répondre 428 %q, il a répondu %d : %v", mailboxRejectedMessage, code, out)
	}
	if got := moi.snoozeAnswer(uids[0]); got.code != http.StatusPreconditionRequired || got.body["error"] != mailboxRejectedMessage {
		app.failf("« Reporter » avec un mot de passe refusé devrait répondre 428 %q, il a répondu %s", mailboxRejectedMessage, got)
	}
}

// cuttingTLSProxy sits between Mailsorter and a person's IMAP server, both
// sides over TLS, and drops the connection, without passing it on, at the
// first command of Mailsorter that holds word: a connection lost in the
// middle of a pass. It returns its port.
func cuttingTLSProxy(a *testApp, serverPort int, word string) int {
	a.t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", imapServerTLS)
	if err != nil {
		a.fatalf("démarrage du relais vers le serveur IMAP : %v", err)
	}
	a.t.Cleanup(func() { _ = ln.Close() })
	upstream := fmt.Sprintf("127.0.0.1:%d", serverPort)
	go func() {
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer client.Close()
				server, err := tls.Dial("tcp", upstream, &tls.Config{ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12})
				if err != nil {
					return
				}
				defer server.Close()
				go func() {
					_, _ = io.Copy(client, server)
					_ = client.Close()
				}()
				buf := make([]byte, 4096)
				for {
					n, err := client.Read(buf)
					if n > 0 {
						if bytes.Contains(bytes.ToUpper(buf[:n]), []byte(word)) {
							return
						}
						if _, werr := server.Write(buf[:n]); werr != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// A connection lost in the middle of a pass: the report whose return it cut
// is not counted, the next one is not tried on a connection that is gone,
// and the next pass brings both back.
func TestAcceptance_IMAPSnoozeStopsWhenTheConnectionDrops(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder, travelOffer)
	later := reportedOnIMAP(moi, uids[1], shopOrder)
	reportedOnIMAP(moi, uids[2], travelOffer)
	moi.reportDue()

	moi.reachThrough(cuttingTLSProxy(app, moi.port, "MOVE"))
	sweepWithin(app, 20*time.Second)

	for _, m := range []incoming{shopOrder, travelOffer} {
		expectReported(moi, m)
		if s := moi.storedReport(m.Subject); s.Attempts != 0 || s.Status != "scheduled" {
			app.failf("une connexion coupée pendant le réveil devrait laisser le report du mail %q prévu sans compter d'essai, il est %q après %d essai(s)", m.Subject, s.Status, s.Attempts)
		}
		moi.expectOnServer(later, m.Subject)
	}

	moi.reachThrough(moi.port)
	app.h.wakeDueSnoozes()
	for _, m := range []incoming{shopOrder, travelOffer} {
		expectUnreadInServerInbox(moi, m)
		expectNotReported(moi, m, "la connexion est revenue, lui aussi")
	}
}

// ---------------------------------------------------------------------------
// A mail that could not be found again, a mail found back already
// ---------------------------------------------------------------------------

// withoutUIDPlus serves this person's mailbox on a second server over TLS
// that does not say where a move lands (no UIDPLUS, no IMAP4rev2). It
// returns its port.
func withoutUIDPlus(p *imapPerson) int {
	a := p.app
	a.t.Helper()
	mem := imapmemserver.New()
	mem.AddUser(p.mailbox)
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		Caps: imapv2.CapSet{imapv2.CapIMAP4rev1: {}},
	})
	ln, err := tls.Listen("tcp", "127.0.0.1:0", imapServerTLS)
	if err != nil {
		a.fatalf("démarrage d'un serveur IMAP sans UIDPLUS : %v", err)
	}
	go func() { _ = srv.Serve(ln) }()
	a.t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().(*net.TCPAddr).Port
}

// A mail without a Message-ID, on a server that does not say where a move
// lands, could not be found again at its hour: "Reporter" refuses it before
// anything moves or is made. On a server that says it, the same mail is
// reported.
func TestAcceptance_IMAPSnoozeRefusesAMailItCouldNotFindAgain(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, _ := syncedIMAPInbox(app, bankStatement)
	const subject = "Sans identifiant"
	raw := "From: Ami <ami@exemple.fr>\r\nTo: " + moi.email + "\r\nSubject: " + subject +
		"\r\nDate: " + time.Now().Add(-time.Minute).Format(time.RFC1123Z) +
		"\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nBonjour.\r\n"
	data, err := moi.mailbox.Append("INBOX", bytes.NewReader([]byte(raw)), &imapv2.AppendOptions{Time: time.Now()})
	if err != nil {
		app.fatalf("dépôt d'un mail sans Message-ID : %v", err)
	}
	uid := fmt.Sprint(uint32(data.UID))
	moi.sync()
	moi.expectShows(uid, subject)

	moi.reachThrough(withoutUIDPlus(moi))
	got := moi.snoozeAnswer(uid)
	msg, _ := got.body["error"].(string)
	if got.code != http.StatusUnprocessableEntity || msg != cannotFindAgainMessage {
		app.failf("reporter un mail sans Message-ID sur un serveur qui ne dit pas où il le range devrait répondre 422 %q, il a répondu %s", cannotFindAgainMessage, got)
	}
	moi.expectOnServer(serverInbox, subject)
	if _, ok := moi.folderOnServer(laterFolder); ok {
		app.failf("un report refusé ne devrait pas créer le dossier « %s »", laterFolder)
	}
	moi.expectShows(uid, subject)

	// The control: the server that says where a move lands takes it.
	moi.reachThrough(moi.port)
	if got := moi.snoozeAnswer(uid); got.code != http.StatusOK {
		app.failf("le même mail, sur un serveur qui dit où il le range, devrait être reporté (200), il a répondu %s", got)
	}
	expectInLater(moi, incoming{Subject: subject})
}

// A reported mail that another mail program put back in the inbox before its
// hour is back: its hour marks it unread, writes its return, and the inbox
// shows it once, whether a sync stored it again in between or not.
func TestAcceptance_IMAPSnoozeFindsTheMailBackInTheInbox(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder, travelOffer)
	moi.openMail(uids[1])
	moi.openMail(uids[2])
	later := reportedOnIMAP(moi, uids[1], shopOrder)
	reportedOnIMAP(moi, uids[2], travelOffer)

	// One put back, then a sync stores it under its new number; the other
	// put back after that sync.
	moi.movedFromElsewhere(later, shopOrder.Subject, serverInbox)
	moi.sync()
	moi.movedFromElsewhere(later, travelOffer.Subject, serverInbox)

	moi.timeComesForSnoozes()

	for i, m := range []incoming{shopOrder, travelOffer} {
		expectUnreadInServerInbox(moi, m)
		expectNotReported(moi, m, "il était déjà revenu dans la boîte de réception")
		if status := reportStatus(app, moi.email, m.Subject); status != "done" {
			app.failf("le report du mail %q, retrouvé dans la boîte de réception, devrait être « done », il est %q", m.Subject, status)
		}
		if n := moi.returnsJournaled(uids[i+1]); n != 1 {
			app.failf("le retour du mail %q devrait être écrit 1 fois dans l'historique, il l'est %d fois", m.Subject, n)
		}
		moi.expectBackInInbox(m.Subject, "à son heure, sans synchronisation")
		if row, ok := rowBySubject(moi.inbox(), m.Subject); ok && row.IsRead {
			app.failf("à son heure, la boîte de réception montre le mail %q marqué lu : il devrait être non lu", m.Subject)
		}
	}
	moi.sync()
	for _, m := range []incoming{shopOrder, travelOffer} {
		moi.expectBackInInbox(m.Subject, "à son heure, puis une synchronisation")
	}
}

// ---------------------------------------------------------------------------
// A server that goes silent after LOGIN, for the syncs
// ---------------------------------------------------------------------------

// stallingTLSProxy sits between Mailsorter and a person's IMAP server, both
// sides over TLS, and from the first command of Mailsorter that holds word on
// stops passing on what the server answers, while still taking the commands:
// a server that goes silent after LOGIN. It returns its port.
func stallingTLSProxy(a *testApp, serverPort int, word string) int {
	a.t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", imapServerTLS)
	if err != nil {
		a.fatalf("démarrage du relais vers le serveur IMAP : %v", err)
	}
	a.t.Cleanup(func() { _ = ln.Close() })
	upstream := fmt.Sprintf("127.0.0.1:%d", serverPort)
	go func() {
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer client.Close()
				server, err := tls.Dial("tcp", upstream, &tls.Config{ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12})
				if err != nil {
					return
				}
				defer server.Close()
				var stalled atomic.Bool
				go func() {
					buf := make([]byte, 4096)
					for {
						n, err := server.Read(buf)
						if n > 0 && !stalled.Load() {
							if _, werr := client.Write(buf[:n]); werr != nil {
								return
							}
						}
						if err != nil {
							return
						}
					}
				}()
				buf := make([]byte, 4096)
				for {
					n, err := client.Read(buf)
					if n > 0 {
						if bytes.Contains(bytes.ToUpper(buf[:n]), []byte(word)) {
							stalled.Store(true)
						}
						if _, werr := server.Write(buf[:n]); werr != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// finishesWithin runs work and stops the test when it has not returned after
// limit, naming what in the failure, rather than letting it hang until the
// test binary's own timeout. work must not touch the test (no failf): it may
// still run after the test has ended.
func finishesWithin(a *testApp, limit time.Duration, what string, work func()) {
	a.t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		work()
	}()
	select {
	case <-done:
	case <-time.After(limit):
		a.fatalf("%s n'a pas fini après %v", what, limit)
	}
}

// "Synchroniser" on a mailbox whose server goes silent after LOGIN (at the
// first FETCH) answers in its own time, saying the mailbox did not answer:
// the session is bound to the request.
func TestAcceptance_IMAPRequestSyncAnswersInItsTimeWhenTheServerGoesSilent(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	before := syncRequestTimeout
	syncRequestTimeout = time.Second
	t.Cleanup(func() { syncRequestTimeout = before })
	moi, _ := syncedIMAPInbox(app, bankStatement)
	moi.reachThrough(stallingTLSProxy(app, moi.port, "FETCH"))

	// Its own client, with its own time limit: a request that never answers
	// fails the test in words, not at the test binary's timeout.
	req, err := http.NewRequest(http.MethodPost, app.server.URL+"/api/emails/sync", nil)
	if err != nil {
		app.fatalf("requête de synchronisation : %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+moi.token)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		app.fatalf("« Synchroniser » sur une boîte qui se tait après LOGIN n'a pas répondu en 20 s (il devrait répondre en 1 s) : %v", err)
	}
	defer resp.Body.Close()
	var out map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	code := resp.StatusCode
	msg, _ := out["error"].(string)
	if code != http.StatusBadGateway || msg != mailboxNoAnswerMessage || out["syncMessage"] != true {
		app.failf("« Synchroniser » sur une boîte qui se tait après LOGIN devrait répondre 502 %q (syncMessage, que la boîte de réception affiche), il a répondu %d : %v", mailboxNoAnswerMessage, code, out)
	}
}

// The background sync, account after account: a server that goes silent
// after LOGIN holds its own account for that account's time, and the next
// account is synced in the same sweep.
func TestAcceptance_IMAPBackgroundSyncGoesPastAMailboxSilentAfterLogin(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	before := autoSyncAccountBound
	autoSyncAccountBound = time.Second
	t.Cleanup(func() { autoSyncAccountBound = before })
	moi, _ := syncedIMAPInbox(app, bankStatement)
	autre := app.imapPerson("autre-boite@exemple.org")
	autre.receive(shopOrder)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := app.h.db.Users().UpdateMany(ctx, bson.M{"email": bson.M{"$in": []string{moi.email, autre.email}}},
		bson.M{"$set": bson.M{"autoSyncEnabled": true}}); err != nil {
		app.fatalf("activation de la synchronisation de fond : %v", err)
	}
	moi.reachThrough(stallingTLSProxy(app, moi.port, "FETCH"))

	finishesWithin(app, 20*time.Second, "la synchronisation de fond", app.h.runDueAutoSyncs)

	if !autre.shows(shopOrder.Subject) {
		app.failf("la synchronisation de fond devrait passer au compte suivant et montrer le mail %q de %s, elle ne le montre pas", shopOrder.Subject, autre.email)
	}
}

// The sweep, a server that goes silent after LOGIN at the MOVE that brings a
// mail back (the move done on the server, its answer never sent): the pass
// ends within its time for that mailbox, the report waits uncounted, and the
// next pass finds the mail in the inbox already and settles it as back,
// unread, its return written once.
func TestAcceptance_IMAPSnoozeSweepGetsPastAServerSilentAtTheMove(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	shortSweepBound(app)
	moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)
	moi.openMail(uids[1])
	reportedOnIMAP(moi, uids[1], shopOrder)
	moi.reportDue()

	moi.reachThrough(stallingTLSProxy(app, moi.port, "MOVE"))
	sweepWithin(app, 20*time.Second)

	expectReported(moi, shopOrder)
	if s := moi.storedReport(shopOrder.Subject); s.Attempts != 0 || s.Status != "scheduled" {
		app.failf("un réveil coupé par un serveur muet au MOVE devrait laisser le report prévu sans compter d'essai, il est %q après %d essai(s)", s.Status, s.Attempts)
	}

	moi.reachThrough(moi.port)
	app.h.wakeDueSnoozes()
	expectUnreadInServerInbox(moi, shopOrder)
	expectNotInLater(moi, shopOrder, "il est revenu dans la boîte de réception")
	expectNotReported(moi, shopOrder, "il est revenu")
	if n := moi.returnsJournaled(uids[1]); n != 1 {
		app.failf("le retour du mail %q devrait être écrit 1 fois dans l'historique, il l'est %d fois", shopOrder.Subject, n)
	}
}

// A reported mail no longer in "Plus tard", whose Message-ID the inbox holds
// twice (put back there, and a copy): which one is the mail cannot be told.
// Its hour takes neither: nothing is marked unread nor written as returned,
// and the report ends (cancelled), the inbox left as it is.
func TestAcceptance_IMAPSnoozeTakesNoneOfTwoCopiesInTheInbox(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)
	later := reportedOnIMAP(moi, uids[1], shopOrder)
	id := moi.storedReport(shopOrder.Subject).HeaderMessageID
	if id == "" {
		app.fatalf("le report du mail %q n'a pas de Message-ID : le test ne prouve rien", shopOrder.Subject)
	}
	moi.movedFromElsewhere(later, shopOrder.Subject, serverInbox)
	raw := "From: " + shopOrder.From + "\r\nSubject: " + shopOrder.Subject + " (copie)\r\nMessage-ID: <" + id +
		">\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nCopie.\r\n"
	if _, err := moi.mailbox.Append("INBOX", bytes.NewReader([]byte(raw)), &imapv2.AppendOptions{Time: time.Now()}); err != nil {
		app.fatalf("dépôt d'une copie du mail : %v", err)
	}

	moi.timeComesForSnoozes()

	expectNotReported(moi, shopOrder, "il n'est plus dans « Plus tard »")
	if status := reportStatus(app, moi.email, shopOrder.Subject); status != "cancelled" {
		app.failf("le report d'un mail que la boîte de réception tient en deux exemplaires devrait être « cancelled », il est %q", status)
	}
	if n := moi.returnsJournaled(uids[1]); n != 0 {
		app.failf("aucun retour ne devrait être écrit pour un mail qu'on ne sait pas désigner, il y en a %d", n)
	}
	for _, subject := range []string{shopOrder.Subject, shopOrder.Subject + " (copie)"} {
		moi.expectOnServer(serverInbox, subject)
	}
}

// ---------------------------------------------------------------------------
// "Annuler" where several copies of the mail are
// ---------------------------------------------------------------------------

// "Annuler" on an archived mail that is no longer under the number Mailsorter
// recorded, and whose Message-ID two messages of the archive carry (copies):
// which one to put back cannot be told. Nothing moves, and each "Annuler"
// says so in French: the history and the one right after with 409
// (severalCopies, so the history does not take it for "already undone"), the
// grouped one in its message.
func TestAcceptance_IMAPUndoRefusesToChooseBetweenCopies(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)
	moi.act(uids[1], "archive")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var row models.Email
	if err := app.h.db.Emails().FindOne(ctx, bson.M{"userId": moi.email, "messageId": uids[1]}).Decode(&row); err != nil || row.HeaderMessageID == "" {
		app.fatalf("la copie du mail %q n'a pas son Message-ID (%v) : le test ne prouve rien", shopOrder.Subject, err)
	}
	moi.erasedFromElsewhere(serverArchive, shopOrder.Subject)
	copies := []string{shopOrder.Subject + " (copie 1)", shopOrder.Subject + " (copie 2)"}
	for _, subject := range copies {
		raw := "From: " + shopOrder.From + "\r\nSubject: " + subject + "\r\nMessage-ID: <" + row.HeaderMessageID +
			">\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nCopie.\r\n"
		if _, err := moi.mailbox.Append(serverArchive, bytes.NewReader([]byte(raw)), &imapv2.AppendOptions{Time: time.Now()}); err != nil {
			app.fatalf("dépôt d'une copie du mail dans l'archive : %v", err)
		}
	}

	line := moi.historyLineFor(uids[1], "archive")
	for name, got := range map[string]answer{
		"« Annuler » dans l'historique": moi.undoFromHistory(line),
		"« Annuler » juste après":       moi.undoRightAfter(uids[1], "archive"),
	} {
		if got.code != http.StatusConflict || got.body["error"] != severalCopiesMessage || got.body["severalCopies"] != true {
			app.failf("%s sur un mail en deux copies devrait répondre 409 %q (severalCopies), il a répondu %s", name, severalCopiesMessage, got)
		}
	}
	code, out := app.callAs(moi.token, http.MethodPost, "/api/emails/batch-undo",
		map[string]interface{}{"messageIds": []string{uids[1]}, "action": "archive"})
	msg, _ := out["message"].(string)
	if code != http.StatusOK || !strings.Contains(msg, severalCopiesMessage) || app.number(out, "severalCopies") != 1 || app.number(out, "restored") != 0 {
		app.failf("« Annuler » groupé sur un mail en deux copies devrait le dire (%q, severalCopies 1, restored 0), il a répondu %d : %v", severalCopiesMessage, code, out)
	}

	for _, subject := range copies {
		moi.expectOnServer(serverArchive, subject)
		moi.expectNotOnServer(serverInbox, subject, "Mailsorter ne sait pas laquelle remettre")
	}
	for _, l := range moi.historyLinesOf(uids[1]) {
		if l.ID == line.ID && l.Undone {
			app.failf("la ligne « archive » du mail %q ne devrait pas être marquée annulée : rien n'a été remis", shopOrder.Subject)
		}
	}
}

// ---------------------------------------------------------------------------
// Several copies in "Plus tard", and a request that ends right after the move
// ---------------------------------------------------------------------------

// copiesInLater replaces, in the person's "Plus tard", the reported mail with
// this subject by two copies carrying its Message-ID: another mail program
// deleted it and the server kept two copies.
func copiesInLater(p *imapPerson, later string, m incoming) {
	a := p.app
	a.t.Helper()
	id := p.storedReport(m.Subject).HeaderMessageID
	if id == "" {
		a.fatalf("le report du mail %q n'a pas de Message-ID : le test ne prouve rien", m.Subject)
	}
	p.erasedFromElsewhere(later, m.Subject)
	for _, subject := range []string{m.Subject + " (copie 1)", m.Subject + " (copie 2)"} {
		raw := "From: " + m.From + "\r\nSubject: " + subject + "\r\nMessage-ID: <" + id +
			">\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nCopie.\r\n"
		if _, err := p.mailbox.Append(later, bytes.NewReader([]byte(raw)), &imapv2.AppendOptions{Time: time.Now()}); err != nil {
			a.fatalf("dépôt d'une copie du mail dans « %s » : %v", later, err)
		}
	}
}

// A reported mail whose Message-ID two messages of "Plus tard" carry, its
// number there no longer naming it: which one to bring back cannot be told.
// "Réactiver" says so in French (409, severalCopies) and moves nothing. The
// sweep counts each attempt, so the report is given up ("En échec") after
// maxSnoozeWakeAttempts passes instead of a session every minute forever;
// "Réactiver" there still says why.
func TestAcceptance_IMAPSnoozeTakesNoneOfTwoCopiesInLater(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)
	later := reportedOnIMAP(moi, uids[1], shopOrder)
	copiesInLater(moi, later, shopOrder)
	row, ok := moi.reportedRow(shopOrder.Subject)
	if !ok {
		app.fatalf("le report du mail %q n'apparaît pas dans les mails reportés", shopOrder.Subject)
	}

	got := moi.wakeNowAnswer(row)
	if got.code != http.StatusConflict || got.body["error"] != severalCopiesWakeMessage || got.body["severalCopies"] != true {
		app.failf("« Réactiver » sur un mail en deux copies dans « Plus tard » devrait répondre 409 %q (severalCopies), il a répondu %s", severalCopiesWakeMessage, got)
	}

	for pass := 1; pass <= maxSnoozeWakeAttempts; pass++ {
		moi.timeComesForSnoozes()
		s := moi.storedReport(shopOrder.Subject)
		want := "scheduled"
		if pass == maxSnoozeWakeAttempts {
			want = "failed"
		}
		if s.Attempts != pass || s.Status != want {
			app.fatalf("après %d passage(s) du réveil sur un mail en deux copies, le report devrait être %q après %d essai(s), il est %q après %d", pass, want, pass, s.Status, s.Attempts)
		}
	}
	// "En échec": "Réactiver" still says why.
	if got := moi.wakeNowAnswer(row); got.code != http.StatusConflict || got.body["error"] != severalCopiesWakeMessage {
		app.failf("« Réactiver » sur le report en échec d'un mail en deux copies devrait répondre 409 %q, il a répondu %s", severalCopiesWakeMessage, got)
	}
	for _, subject := range []string{shopOrder.Subject + " (copie 1)", shopOrder.Subject + " (copie 2)"} {
		moi.expectOnServer(later, subject)
		moi.expectNotOnServer(serverInbox, subject, "Mailsorter ne sait pas laquelle ramener")
	}
}

// answerHookTLSProxy sits between Mailsorter and a person's IMAP server, both
// sides over TLS, and calls hook once, before passing on the first answer of
// the server that follows a command of Mailsorter holding word. It returns
// its port.
func answerHookTLSProxy(a *testApp, serverPort int, word string, hook func()) int {
	a.t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", imapServerTLS)
	if err != nil {
		a.fatalf("démarrage du relais vers le serveur IMAP : %v", err)
	}
	a.t.Cleanup(func() { _ = ln.Close() })
	upstream := fmt.Sprintf("127.0.0.1:%d", serverPort)
	var sent, fired atomic.Bool
	go func() {
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer client.Close()
				server, err := tls.Dial("tcp", upstream, &tls.Config{ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12})
				if err != nil {
					return
				}
				defer server.Close()
				go func() {
					buf := make([]byte, 4096)
					for {
						n, err := server.Read(buf)
						if n > 0 {
							if sent.Load() && fired.CompareAndSwap(false, true) {
								hook()
							}
							if _, werr := client.Write(buf[:n]); werr != nil {
								return
							}
						}
						if err != nil {
							return
						}
					}
				}()
				buf := make([]byte, 4096)
				for {
					n, err := client.Read(buf)
					if n > 0 {
						if bytes.Contains(bytes.ToUpper(buf[:n]), []byte(word)) {
							sent.Store(true)
						}
						if _, werr := server.Write(buf[:n]); werr != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// The request that reports a mail ends right after the server moved it into
// "Plus tard" (its context cancelled before the answer to the MOVE comes
// back): the report and its history line are written all the same, with the
// folder it waits in, so its hour brings it back. Written under the request's
// context, the mail would stay in "Plus tard" with no report, for good.
func TestAcceptance_IMAPSnoozeRecordedWhenTheRequestEndsAfterTheMove(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)

	reqCtx, cancelReq := context.WithCancel(context.Background())
	defer cancelReq()
	moi.reachThrough(answerHookTLSProxy(app, moi.port, "MOVE", cancelReq))
	// The session outlives the request here, as one opened by another
	// request would: only the request's own writes are at stake.
	session, err := app.h.openSession(context.Background(), moi.email)
	if err != nil {
		app.fatalf("ouverture de la boîte de %s : %v", moi.email, err)
	}
	defer session.Close()

	now := time.Now()
	if err := app.h.snoozeIMAP(reqCtx, session, moi.email, uids[1], now.Add(24*time.Hour), now); err != nil {
		app.fatalf("le report du mail %q, la requête finie juste après le déplacement, a échoué : %v", shopOrder.Subject, err)
	}
	if reqCtx.Err() == nil {
		app.fatalf("la requête n'a pas été terminée après le déplacement : le test ne prouve rien")
	}

	later, ok := moi.folderOnServer(laterFolder)
	if !ok {
		app.fatalf("le dossier « %s » n'existe pas : le mail n'a pas été déplacé, le test ne prouve rien", laterFolder)
	}
	moi.expectOnServer(later, shopOrder.Subject)
	s := moi.storedReport(shopOrder.Subject)
	if s.Status != "scheduled" || s.Folder != later || s.FolderUID == "" {
		app.failf("le report du mail %q devrait être prévu avec son dossier %q et son numéro, il est %q, dossier %q, numéro %q", shopOrder.Subject, later, s.Status, s.Folder, s.FolderUID)
	}
	found := false
	for _, l := range moi.history(uids[1]) {
		found = found || (l.Action == "archive" && l.Source == SourceSnooze)
	}
	if !found {
		app.failf("la ligne d'historique du report du mail %q devrait être écrite, elle ne l'est pas", shopOrder.Subject)
	}
}

// ---------------------------------------------------------------------------
// A new app password for the mailbox already connected
// ---------------------------------------------------------------------------

// changePassword is "Enregistrer" in the form "Changer le mot de passe
// d'application" of /connect, as this person.
func (p *imapPerson) changePassword(password string) answer {
	p.app.t.Helper()
	code, out := p.app.callAs(p.token, http.MethodPut, "/api/mailbox/password", map[string]string{"password": password})
	return answer{code: code, body: out}
}

// storedMailbox is this person's mail_accounts row, as stored.
func (p *imapPerson) storedMailbox() models.MailAccount {
	a := p.app
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var account models.MailAccount
	if err := a.h.db.MailAccounts().FindOne(ctx, bson.M{"userId": p.email}).Decode(&account); err != nil {
		a.fatalf("la boîte branchée de %s n'est pas en base : %v", p.email, err)
	}
	return account
}

// The provider refuses the password on file (a new one was made on its
// side): "Synchroniser" says so (428, passwordRefused). A wrong new password
// replaces nothing; the right one replaces the password only: the same
// mailbox row (its creation, its server), and with it the stored copy, the
// AI's proposals and what "Annuler" can still bring back.
func TestAcceptance_IMAPNewAppPasswordKeepsTheMailbox(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder, travelOffer)
	moi.act(uids[2], "archive")
	line := moi.historyLineFor(uids[2], "archive")
	moi.analyze(uids[0])
	proposal := moi.proposalFor(uids[0])
	before := moi.storedMailbox()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stale, err := app.h.sealSecret("ancien-mot-de-passe")
	if err != nil {
		app.fatalf("chiffrement d'un ancien mot de passe : %v", err)
	}
	if _, err := app.h.db.MailAccounts().UpdateOne(ctx, bson.M{"userId": moi.email}, bson.M{"$set": bson.M{"secret": stale}}); err != nil {
		app.fatalf("pose d'un mot de passe que le fournisseur refuse : %v", err)
	}
	if code, out := moi.syncAnswer(); code != http.StatusPreconditionRequired || out["passwordRefused"] != true || out["error"] != mailboxRejectedMessage {
		app.fatalf("« Synchroniser » avec un mot de passe que le fournisseur refuse devrait répondre 428 %q (passwordRefused), il a répondu %d : %v", mailboxRejectedMessage, code, out)
	}

	got := moi.changePassword("pas-le-bon")
	if got.code != http.StatusUnauthorized || got.body["error"] != mailboxRejectedMessage || got.body["mailboxRejected"] != true {
		app.failf("un nouveau mot de passe refusé devrait répondre 401 %q (mailboxRejected), il a répondu %s", mailboxRejectedMessage, got)
	}
	if now := moi.storedMailbox(); now.Secret != stale {
		app.failf("un nouveau mot de passe refusé ne devrait pas remplacer celui enregistré, il l'a remplacé")
	}

	if got := moi.changePassword(imapTestPassword); got.code != http.StatusOK {
		app.fatalf("le bon nouveau mot de passe devrait être enregistré (200), il a répondu %s", got)
	}
	after := moi.storedMailbox()
	if after.ID != before.ID || !after.CreatedAt.Equal(before.CreatedAt) || after.Host != before.Host || after.Port != before.Port || after.Username != before.Username {
		app.failf("un nouveau mot de passe devrait garder la même boîte (ligne, création, serveur), elle est passée de %+v à %+v", before, after)
	}
	if after.Secret == stale {
		app.failf("le bon nouveau mot de passe n'a pas remplacé l'ancien")
	}

	moi.sync()
	moi.expectShows(uids[0], bankStatement.Subject)
	moi.expectShows(uids[1], shopOrder.Subject)
	if !moi.inPlan(proposal.ID) {
		app.failf("la proposition de l'IA sur le mail %q devrait rester dans le plan après un nouveau mot de passe", bankStatement.Subject)
	}
	if got := moi.undoFromHistory(line); got.code != http.StatusOK {
		app.failf("« Annuler » sur l'archivage d'avant le nouveau mot de passe devrait marcher (200), il a répondu %s", got)
	}
	moi.expectOnServer(serverInbox, travelOffer.Subject)
}

// "Synchroniser" for an account with no mailbox: one French sentence, 428,
// marked for the inbox, which offers "Brancher une boîte" (no "/connect"
// written in it).
func TestAcceptance_SyncWithoutMailboxSaysSo(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi := app.imapPerson("sans-boite@exemple.fr")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := app.h.db.MailAccounts().DeleteOne(ctx, bson.M{"userId": moi.email}); err != nil {
		app.fatalf("retrait de la boîte de %s : %v", moi.email, err)
	}
	code, out := moi.syncAnswer()
	if code != http.StatusPreconditionRequired || out["error"] != noMailboxMessage || out["syncMessage"] != true {
		app.failf("« Synchroniser » sans boîte branchée devrait répondre 428 %q (syncMessage), il a répondu %d : %v", noMailboxMessage, code, out)
	}
}

// putPasswordRaw is "Enregistrer" of the new password form as a raw request,
// for what the harness does not show: the Retry-After of a 429.
func putPasswordRaw(p *imapPerson, password string) (*http.Response, map[string]interface{}) {
	a := p.app
	a.t.Helper()
	body, _ := json.Marshal(map[string]string{"password": password})
	req, err := http.NewRequest(http.MethodPut, a.server.URL+"/api/mailbox/password", bytes.NewReader(body))
	if err != nil {
		a.fatalf("requête du nouveau mot de passe : %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		a.fatalf("le nouveau mot de passe n'a pas répondu : %v", err)
	}
	defer resp.Body.Close()
	var out map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

// Wrong new passwords one after another: each is a LOGIN at the provider,
// until the sixth in a row, which answers 429 in French, with Retry-After,
// without reaching the server.
func TestAcceptance_IMAPNewPasswordAttemptsAreThrottled(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, _ := syncedIMAPInbox(app, bankStatement)
	port, conns := countingTLSProxy(app, moi.port)
	moi.reachThrough(port)

	for i := 1; i <= 5; i++ {
		if got := moi.changePassword("pas-le-bon"); got.code != http.StatusUnauthorized {
			app.fatalf("le mauvais mot de passe n°%d devrait être refusé par le fournisseur (401), il a répondu %s", i, got)
		}
	}
	tried := conns()
	resp, out := putPasswordRaw(moi, "pas-le-bon")
	if resp.StatusCode != http.StatusTooManyRequests || out["error"] != "Trop de tentatives. Patientez quelques instants." || resp.Header.Get("Retry-After") == "" {
		app.failf("le 6e mauvais mot de passe de suite devrait répondre 429 « Trop de tentatives. Patientez quelques instants. » avec Retry-After, il a répondu %d %v (Retry-After %q)", resp.StatusCode, out, resp.Header.Get("Retry-After"))
	}
	if n := conns(); n != tried {
		app.failf("le 6e mauvais mot de passe ne devrait pas atteindre le serveur, il a ouvert %d connexion(s) de plus", n-tried)
	}
}

// Connecting a mailbox again and again is throttled the same way, before any
// LOGIN: the sixth attempt in a row answers 429. (These attempts name an
// address with no IMAP route, so none of them reaches a server; the limit is
// checked before the route is.)
func TestAcceptance_ConnectAttemptsAreThrottled(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi := app.imapPerson("essais@exemple.fr")
	connect := func() answer {
		code, out := app.callAs(moi.token, http.MethodPost, "/api/mailbox/connect",
			map[string]string{"address": "quelquun@exemple.fr", "password": "pas-le-bon"})
		return answer{code: code, body: out}
	}
	for i := 1; i <= 5; i++ {
		if got := connect(); got.code == http.StatusTooManyRequests {
			app.fatalf("la tentative de branchement n°%d ne devrait pas encore être freinée, elle a répondu %s", i, got)
		}
	}
	if got := connect(); got.code != http.StatusTooManyRequests || got.body["error"] != "Trop de tentatives. Patientez quelques instants." {
		app.failf("la 6e tentative de branchement de suite devrait répondre 429 « Trop de tentatives. Patientez quelques instants. », elle a répondu %s", got)
	}
}

// The mailbox disconnected while its new password was being tried: the
// password is not stored anywhere, and the answer is 428 "no mailbox", never
// "Mot de passe mis à jour.".
func TestAcceptance_IMAPNewPasswordForAMailboxDisconnectedMeanwhile(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, _ := syncedIMAPInbox(app, bankStatement)
	var deleted atomic.Bool
	moi.reachThrough(answerHookTLSProxy(app, moi.port, "LOGIN", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := app.h.db.MailAccounts().DeleteOne(ctx, bson.M{"userId": moi.email}); err == nil {
			deleted.Store(true)
		}
	}))

	got := moi.changePassword(imapTestPassword)
	if !deleted.Load() {
		app.fatalf("la boîte n'a pas été débranchée pendant l'essai du mot de passe : le test ne prouve rien")
	}
	if got.code != http.StatusPreconditionRequired || got.body["error"] != noMailboxMessage {
		app.failf("un nouveau mot de passe pour une boîte débranchée pendant l'essai devrait répondre 428 %q, il a répondu %s", noMailboxMessage, got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if n, err := app.h.db.MailAccounts().CountDocuments(ctx, bson.M{"userId": moi.email}); err != nil || n != 0 {
		app.failf("aucune boîte ne devrait être recréée par le nouveau mot de passe, il y en a %d (%v)", n, err)
	}
}

// A mailbox reached through the Gmail API has no app password here: the
// route says so in French.
func TestAcceptance_GmailMailboxHasNoAppPasswordToChange(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	code, out := app.call(http.MethodPut, "/api/mailbox/password", map[string]string{"password": "quelque-chose"})
	if code != http.StatusUnprocessableEntity || out["error"] != googleMailboxPasswordMessage {
		app.failf("un nouveau mot de passe sur une boîte branchée par Google devrait répondre 422 %q, il a répondu %d : %v", googleMailboxPasswordMessage, code, out)
	}
}

// "Réactiver" refused by the mailbox (here its folder "Plus tard" cannot be
// opened): a French sentence, nothing counted, the report still scheduled.
func TestAcceptance_IMAPReactivateRefusedSaysSoInFrench(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)
	later := reportedOnIMAP(moi, uids[1], shopOrder)
	row, ok := moi.reportedRow(shopOrder.Subject)
	if !ok {
		app.fatalf("le report du mail %q n'apparaît pas dans les mails reportés", shopOrder.Subject)
	}
	c := moi.otherProgram()
	if err := c.Rename(later, "Plus tard (mis de côté)", nil).Wait(); err != nil {
		app.fatalf("un autre logiciel n'a pas pu renommer « %s » : %v", later, err)
	}
	c.Close()

	got := moi.wakeNowAnswer(row)
	if got.code != http.StatusBadGateway || got.body["error"] != "Ce mail n'a pas pu être ramené, réessayez dans un instant." {
		app.failf("« Réactiver » refusé par la boîte devrait répondre 502 « Ce mail n'a pas pu être ramené, réessayez dans un instant. », il a répondu %s", got)
	}
	if s := moi.storedReport(shopOrder.Subject); s.Attempts != 0 || s.Status != "scheduled" {
		app.failf("« Réactiver » refusé ne devrait rien compter, le report est %q après %d essai(s)", s.Status, s.Attempts)
	}
}
