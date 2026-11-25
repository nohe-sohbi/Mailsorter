//go:build acceptance

package api

// The generation of a mailbox (generation.go), through the routes: what a
// screen, a sync or a run of the rules opened on one mailbox does once another
// is connected in its place.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/rules"
	"go.mongodb.org/mongo-driver/bson"
)

// holdingTLSProxy sits between Mailsorter and a person's IMAP server, both
// sides over TLS, and holds the first answer line of the server that contains
// trigger, whatever the connection: reached is closed when it gets there, and
// the line goes on to Mailsorter once release is called. Every other line,
// that one's connection included once released, goes through as it is.
func holdingTLSProxy(a *testApp, serverPort int, trigger string) (port int, reached <-chan struct{}, release func()) {
	a.t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", imapServerTLS)
	if err != nil {
		a.fatalf("démarrage du relais vers le serveur IMAP : %v", err)
	}
	a.t.Cleanup(func() { _ = ln.Close() })
	at := make(chan struct{})
	free := make(chan struct{})
	var once, freeOnce sync.Once
	release = func() { freeOnce.Do(func() { close(free) }) }
	a.t.Cleanup(release)
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
					_, _ = bufio.NewReader(client).WriteTo(server)
					_ = server.Close()
				}()
				answers := bufio.NewReader(server)
				for {
					line, err := answers.ReadBytes('\n')
					if len(line) > 0 {
						held := false
						if bytes.Contains(line, []byte(trigger)) {
							once.Do(func() { held = true })
						}
						if held {
							close(at)
							<-free
						}
						if _, werr := client.Write(line); werr != nil {
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
	return ln.Addr().(*net.TCPAddr).Port, at, release
}

// throughProxy points the connected mailbox of this person at a relay.
func throughProxy(moi *imapPerson, port int) {
	a := moi.app
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := a.h.db.MailAccounts().UpdateOne(ctx, bson.M{"userId": moi.email}, bson.M{"$set": bson.M{"port": port}}); err != nil {
		a.fatalf("boîte de %s rebranchée sur le relais : %v", moi.email, err)
	}
}

// screenGeneration is the mailbox the screen loads (GET /api/mailbox), which
// it sends back with each request (X-Mailbox-Generation, api.js).
func screenGeneration(moi *imapPerson) string {
	a := moi.app
	a.t.Helper()
	code, out := a.callAs(moi.token, http.MethodGet, "/api/mailbox", nil)
	gen, _ := out["generation"].(string)
	if code != http.StatusOK || gen == "" {
		a.fatalf("GET /api/mailbox de %s devrait nommer la boîte chargée (generation), il a répondu %d : %v", moi.email, code, out)
	}
	return gen
}

// callFromScreen is a request of a screen loaded on the mailbox gen.
func callFromScreen(moi *imapPerson, gen, method, path string, body interface{}) (int, map[string]interface{}) {
	moi.app.t.Helper()
	return moi.app.send(method, path, body, map[string]string{
		"Authorization":         "Bearer " + moi.token,
		mailboxGenerationHeader: gen,
	})
}

// waitFor fails the test when ch is not closed within d: what was to happen
// (a request reaching the server, a run ending) did not.
func waitFor(a *testApp, ch <-chan struct{}, d time.Duration, what string) {
	a.t.Helper()
	select {
	case <-ch:
	case <-time.After(d):
		a.fatalf("%s : rien après %s", what, d)
	}
}

// copyOfOtherGenerations counts the rows of the copy, and the pending
// proposals, of this person that are not of the mailbox connected now.
func copyOfOtherGenerations(moi *imapPerson) int64 {
	a := moi.app
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	current, err := a.h.currentGeneration(ctx, moi.email)
	if err != nil {
		a.fatalf("quelle boîte de %s est branchée : %v", moi.email, err)
	}
	n, err := a.h.db.Emails().CountDocuments(ctx, bson.M{"userId": moi.email, "folder": bson.M{"$exists": true}, "generation": bson.M{"$ne": current}})
	if err != nil {
		a.fatalf("lecture de la copie de %s : %v", moi.email, err)
	}
	return n
}

// (a) A screen loaded on one mailbox, left open while another is connected in
// its place, names its mails by numbers the new one gives to its own: its
// "Archiver" is refused (409) and nothing moves on the new server. Loaded
// again, the screen acts on the new mailbox.
func TestAcceptance_AScreenOfAReplacedMailboxActsOnNothing(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, oldUIDs := syncedIMAPInbox(app, bankStatement, shopOrder, travelOffer)
	stale := screenGeneration(moi)
	if code, out := callFromScreen(moi, stale, http.MethodGet, "/api/emails?q=in:inbox", nil); code != http.StatusOK {
		app.fatalf("la liste de %s chargée sur sa première boîte a répondu %d : %v", moi.email, code, out)
	}

	moi.disconnectMailbox()
	moi.connectOtherMailbox(otherMailbox)
	newUIDs := moi.receive(otherBoxMails...)
	moi.expectNumbered(newUIDs[1], oldUIDs[1])
	moi.sync()

	code, out := callFromScreen(moi, stale, http.MethodPost, "/api/emails/action", map[string]string{"messageId": oldUIDs[1], "action": "archive"})
	if code != http.StatusConflict {
		app.failf("« Archiver » depuis l'écran chargé sur l'ancienne boîte de %s devrait être refusé (409), il a répondu %d : %v", moi.email, code, out)
	} else if msg, _ := out["error"].(string); msg != mailboxChangedMessage {
		app.failf("le refus devrait dire %q, il dit %q", mailboxChangedMessage, msg)
	}
	moi.expectOnServer(serverInbox, otherBoxMails[1].Subject)
	if in := moi.onServer(serverArchive); len(in) != 0 {
		app.failf("rien ne devrait bouger sur le serveur de la nouvelle boîte de %s, son archive contient %s", moi.email, quotedList(in))
	}
	if code, _ := callFromScreen(moi, stale, http.MethodGet, "/api/emails?q=in:inbox", nil); code != http.StatusConflict {
		app.failf("la liste demandée par l'écran de l'ancienne boîte de %s devrait être refusée (409), elle a répondu %d", moi.email, code)
	}

	fresh := screenGeneration(moi)
	if fresh == stale {
		app.fatalf("la nouvelle boîte de %s porte la génération de l'ancienne (%s)", moi.email, fresh)
	}
	if code, out := callFromScreen(moi, fresh, http.MethodPost, "/api/emails/action", map[string]string{"messageId": newUIDs[1], "action": "archive"}); code != http.StatusOK {
		app.failf("« Archiver » depuis l'écran rechargé de %s devrait agir sur la nouvelle boîte, il a répondu %d : %v", moi.email, code, out)
	}
	moi.expectOnServer(serverArchive, otherBoxMails[1].Subject)
}

// (b) A background sync of one mailbox still running when another is
// connected in its place, held in the middle of each phase that writes: what
// it writes is never read. The new mailbox's copy holds none of the old one's
// mails, its first sync is recognized as such (a mail arrived since it was
// connected is not judged by the rules), and that sync erases what the old one
// wrote late.
func TestAcceptance_ASyncOfAReplacedMailboxWritesNothingTheNextReads(t *testing.T) {
	for _, phase := range []struct{ name, trigger string }{
		// The flags and sizes of the inbox: before the copy is reconciled and
		// the mails stored.
		{"le stockage", "RFC822.SIZE"},
		// The rule's move: before its history line and the release of its mark.
		{"les règles", "COPYUID"},
		// The structures: before "Pièces jointes" is written.
		{"les structures", "BODYSTRUCTURE"},
	} {
		t.Run(phase.name, func(t *testing.T) {
			app := newTestApp(t, &scenarioResult{})
			moi := app.imapPerson(imapInboxOwner)
			moi.rule("Archiver la boutique", 1, rules.FieldFrom, rules.OpContains, "boutique.com", models.RuleAction{Type: rules.ActionArchive})
			moi.setAutoApplyRules(true)
			moi.receive(bankStatement)
			moi.sync() // the first sync of this mailbox
			moi.arrive(shopOrder, travelOffer)

			port, reached, release := holdingTLSProxy(app, moi.port, phase.trigger)
			throughProxy(moi, port)
			done := make(chan struct{})
			var syncErr error
			go func() {
				defer close(done)
				ctx, cancel := context.WithTimeout(context.Background(), autoSyncTimeout)
				defer cancel()
				_, _, _, syncErr = app.h.syncInbox(ctx, moi.email)
			}()
			waitFor(app, reached, 20*time.Second, "la synchronisation de fond n'atteint pas "+phase.name)

			moi.disconnectMailbox()
			moi.connectOtherMailbox(otherMailbox)
			release()
			waitFor(app, done, 30*time.Second, "la synchronisation de fond de l'ancienne boîte ne finit pas")
			if syncErr != nil {
				t.Logf("la synchronisation de l'ancienne boîte a fini sur : %v", syncErr)
			}
			if copyOfOtherGenerations(moi) == 0 && phase.trigger == "RFC822.SIZE" {
				app.fatalf("la synchronisation de l'ancienne boîte n'a rien écrit après le changement : le test ne prouve rien")
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var account models.MailAccount
			if err := app.h.db.MailAccounts().FindOne(ctx, bson.M{"userId": moi.email}).Decode(&account); err != nil {
				app.fatalf("lecture de la boîte de %s : %v", moi.email, err)
			}
			first, err := app.h.firstSync(ctx, moi.email, account)
			if err != nil || !first {
				app.failf("la prochaine synchronisation de la nouvelle boîte de %s devrait être sa première (firstSyncedAt %v), elle ne l'est pas (%v)", moi.email, account.FirstSyncedAt, err)
			}
			if mails := moi.inbox(); len(mails) != 0 {
				app.failf("la liste de la nouvelle boîte de %s devrait être vide avant sa première synchronisation, elle montre %s", moi.email, describeInbox(mails))
			}

			// No line of the old mailbox offers "Annuler", the ones its sync
			// wrote late included: their numbers are the new mailbox's.
			for _, l := range historyViewOf(moi) {
				if l.Undoable || !l.MailboxGone {
					app.failf("la ligne %s de l'historique de %s est de l'ancienne boîte : elle devrait être définitive, elle dit undoable %v, mailboxGone %v", l.ID, moi.email, l.Undoable, l.MailboxGone)
				}
			}

			newUIDs := moi.arrive(shopSale)
			moi.sync()
			moi.expectOnServer(serverInbox, shopSale.Subject)
			moi.expectNotOnServer(serverArchive, shopSale.Subject, "la première synchronisation d'une boîte ne juge aucun mail arrivé")
			moi.expectShows(newUIDs[0], shopSale.Subject)
			for _, old := range []incoming{bankStatement, shopOrder, travelOffer} {
				moi.expectDoesNotShow(old.Subject, "c'est un mail de l'ancienne boîte")
			}
			if n := copyOfOtherGenerations(moi); n != 0 {
				app.failf("la synchronisation de la nouvelle boîte de %s devrait effacer ce que l'ancienne a écrit en retard, il en reste %d ligne(s)", moi.email, n)
			}
		})
	}
}

// (c) "Appliquer mes règles" holds the mails it judges for as long as it runs
// (POST /api/rules/apply): a sync started while it reads their text does not
// take them, and once it is done it holds nothing.
func TestAcceptance_ApplyRulesHoldsWhatItJudgesWhileItRuns(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi := app.imapPerson(imapInboxOwner)
	moi.rule("Archiver les commandes", 1, rules.FieldBody, rules.OpContains, "colis", models.RuleAction{Type: rules.ActionArchive})
	moi.setAutoApplyRules(true)
	order := incoming{From: "Boutique <info@boutique.com>", Subject: "Votre commande est expédiée", Body: "Votre colis arrive demain."}
	uids := moi.arrive(order)
	moi.sync() // the first sync of a mailbox judges nothing as arrived
	// An arrival a sync has not judged yet (rulesDue): one the next sync
	// takes, unless "Appliquer mes règles" holds it.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := app.h.db.Emails().UpdateOne(ctx, bson.M{"userId": moi.email, "messageId": uids[0]},
		bson.M{"$set": bson.M{"rulesDue": true}}); err != nil {
		app.fatalf("marque du mail n°%s de %s : %v", uids[0], moi.email, err)
	}

	// The text read of the run: the mails are held, none moved yet.
	port, reached, release := holdingTLSProxy(app, moi.port, "RFC822.SIZE")
	throughProxy(moi, port)
	done := make(chan struct{})
	var applied answer
	go func() {
		defer close(done)
		code, out := app.callAs(moi.token, http.MethodPost, "/api/rules/apply", nil)
		applied = answer{code: code, body: out}
	}()
	waitFor(app, reached, 20*time.Second, "« Appliquer mes règles » n'atteint pas la lecture du texte")

	code, out := moi.syncAnswer()
	if code != http.StatusOK {
		app.fatalf("la synchronisation pendant « Appliquer mes règles » de %s a répondu %d : %v", moi.email, code, out)
	}
	if n, _ := out["rulesApplied"].(float64); n != 0 {
		app.failf("la synchronisation ne devrait pas prendre le mail que « Appliquer mes règles » tient, elle en a rangé %v", n)
	}
	moi.expectOnServer(serverInbox, order.Subject)

	release()
	waitFor(app, done, 30*time.Second, "« Appliquer mes règles » ne finit pas")
	if applied.code != http.StatusOK {
		app.fatalf("« Appliquer mes règles » de %s a répondu %s", moi.email, applied)
	}
	moi.expectOnServer(serverArchive, order.Subject)
	if got := ruleLines(moi, uids[0], rules.ActionArchive); got != 1 {
		app.failf("l'historique de %s devrait noter une fois l'archivage par la règle, il le note %d fois", moi.email, got)
	}
	if n, err := app.h.db.Emails().CountDocuments(ctx, bson.M{"userId": moi.email, "rulesClaim": bson.M{"$exists": true}}); err != nil || n != 0 {
		app.failf("« Appliquer mes règles » devrait relâcher les mails qu'il tenait, il en tient encore %d (%v)", n, err)
	}
}

// (d) After a mailbox is disconnected and another connected, a line of the old
// one's history names its mail by a number the new mailbox gives to one of
// its own, which Mailsorter has moved out since: the line no longer offers
// "Annuler" (final), an "Annuler" sent anyway is refused, and the new
// mailbox's mail stays where Mailsorter put it.
func TestAcceptance_UndoOfAReplacedMailboxLeavesTheNewOnesMail(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, oldUIDs := syncedIMAPInbox(app, bankStatement, shopOrder, travelOffer)
	stale := screenGeneration(moi)
	moi.act(oldUIDs[1], "archive")
	line := moi.historyLineFor(oldUIDs[1], "archive")
	if !line.Undoable {
		app.fatalf("l'archivage du mail n°%s de l'ancienne boîte de %s devrait d'abord proposer « Annuler » : le test ne prouve rien", oldUIDs[1], moi.email)
	}

	moi.disconnectMailbox()
	moi.connectOtherMailbox(otherMailbox)
	newUIDs := moi.receive(otherBoxMails...)
	moi.expectNumbered(newUIDs[1], oldUIDs[1])
	moi.sync()
	moi.act(newUIDs[1], "archive")
	moi.expectOnServer(serverArchive, otherBoxMails[1].Subject)

	for _, l := range historyViewOf(moi) {
		if l.ID != line.ID {
			continue
		}
		if l.Undoable || !l.MailboxGone {
			app.failf("la ligne d'archivage de l'ancienne boîte de %s devrait être définitive (mailboxGone, sans « Annuler »), elle dit undoable %v, mailboxGone %v", moi.email, l.Undoable, l.MailboxGone)
		}
	}
	if code, out := callFromScreen(moi, stale, http.MethodPost, "/api/activity/undo", map[string]string{"id": line.ID}); code != http.StatusConflict {
		app.failf("« Annuler » depuis l'écran de l'ancienne boîte de %s devrait être refusé (409), il a répondu %d : %v", moi.email, code, out)
	}
	if got := moi.undoFromHistory(line); got.code != http.StatusGone {
		app.failf("« Annuler » sur la ligne de l'ancienne boîte de %s devrait être refusé (410), il a répondu %s", moi.email, got)
	}
	moi.expectOnServer(serverArchive, otherBoxMails[1].Subject)
	moi.expectNotOnServer(serverInbox, otherBoxMails[1].Subject, "« Annuler » d'une ligne de l'ancienne boîte ne touche pas à la nouvelle")

	// The new mailbox's own line still offers it.
	if l := moi.historyLineFor(newUIDs[1], "archive"); !l.Undoable {
		app.failf("l'archivage du mail n°%s de la nouvelle boîte de %s devrait proposer « Annuler »", newUIDs[1], moi.email)
	}
}

// historyLineView is a line of GET /api/activity/log with what the harness's
// historyLine leaves out: whether it is of a mailbox no longer connected.
type historyLineView struct {
	ID          string `json:"id"`
	Undoable    bool   `json:"undoable"`
	MailboxGone bool   `json:"mailboxGone"`
}

// historyViewOf is the Historique screen of this person, mailboxGone included.
func historyViewOf(moi *imapPerson) []historyLineView {
	a := moi.app
	a.t.Helper()
	code, out := a.callAs(moi.token, http.MethodGet, "/api/activity/log?limit=50", nil)
	if code != http.StatusOK {
		a.fatalf("l'historique de %s ne s'affiche pas : %d : %v", moi.email, code, out)
	}
	raw, _ := out["entries"].([]interface{})
	lines := make([]historyLineView, 0, len(raw))
	for _, r := range raw {
		m, _ := r.(map[string]interface{})
		id, _ := m["id"].(string)
		undoable, _ := m["undoable"].(bool)
		gone, _ := m["mailboxGone"].(bool)
		lines = append(lines, historyLineView{ID: id, Undoable: undoable, MailboxGone: gone})
	}
	return lines
}

// What was kept of the IMAP mailbox connected before rows carried a
// generation is that mailbox's after the boot (stampGenerations): its list,
// and the "Annuler" of its history, as they were.
func TestAcceptance_BootGivesTheConnectedMailboxWhatWasKeptOfIt(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)
	moi.act(uids[1], "archive")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	unstamp := bson.M{"$unset": bson.M{"generation": ""}}
	for _, c := range []string{"emails", "action_log"} {
		if _, err := app.h.db.DB.Collection(c).UpdateMany(ctx, bson.M{"userId": moi.email}, unstamp); err != nil {
			app.fatalf("retrait de la génération de %s : %v", c, err)
		}
	}
	if line := moi.historyLineFor(uids[1], "archive"); line.Undoable {
		app.fatalf("sans génération, l'archivage de %s ne devrait plus proposer « Annuler » : le test ne prouve rien", moi.email)
	}

	if err := app.h.db.EnsureIndexes(ctx); err != nil {
		app.fatalf("EnsureIndexes : %v", err)
	}
	moi.expectShows(uids[0], bankStatement.Subject)
	if line := moi.historyLineFor(uids[1], "archive"); !line.Undoable {
		app.failf("après le démarrage, l'archivage du mail n°%s de %s devrait de nouveau proposer « Annuler »", uids[1], moi.email)
	}
	moi.sync()
	moi.expectShows(uids[0], bankStatement.Subject)
	if got := moi.undoFromHistory(moi.historyLineFor(uids[1], "archive")); got.code != http.StatusOK {
		app.failf("« Annuler » de l'archivage de %s après le démarrage a répondu %s", moi.email, got)
	}
	moi.expectOnServer(serverInbox, shopOrder.Subject)
}
