//go:build acceptance

package api

// Tests that run on the acceptance harness (a real MongoDB, a real IMAP server
// over TLS) and prove a piece of wiring rather than a sentence: what a sync
// or a handler does with what a real server and a real database answer. They
// are not in the report. `scripts/scenarios.sh -run '^TestAcceptance'` runs
// them, and so does CI, in the scenarios job.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/rules"
	"go.mongodb.org/mongo-driver/bson"
)

// corruptingTLSProxy sits between Mailsorter and a person's IMAP server, both
// sides over TLS, and rewrites every occurrence of from into to in what the
// server answers: a server that describes one message in a way go-imap cannot
// read. It returns its port and the number of rewrites so far.
func corruptingTLSProxy(a *testApp, serverPort int, from, to string) (int, func() int64) {
	a.t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", imapServerTLS)
	if err != nil {
		a.fatalf("démarrage du relais vers le serveur IMAP : %v", err)
	}
	a.t.Cleanup(func() { _ = ln.Close() })
	var rewrites atomic.Int64
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
						if n := bytes.Count(line, []byte(from)); n > 0 {
							rewrites.Add(int64(n))
							line = bytes.ReplaceAll(line, []byte(from), []byte(to))
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
	return ln.Addr().(*net.TCPAddr).Port, rewrites.Load
}

// storedRow is a stored inbox message, as these tests read it.
type storedRow struct {
	Subject             string `bson:"subject"`
	HasAttachments      *bool  `bson:"hasAttachments"`
	StructureUnreadable bool   `bson:"structureUnreadable"`
}

// storedRows reads the stored inbox of this person, by subject.
func (p *imapPerson) storedRows() map[string]storedRow {
	a := p.app
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cur, err := a.h.db.Emails().Find(ctx, bson.M{"userId": p.email, "folder": serverInbox})
	if err != nil {
		a.fatalf("lecture de la copie de %s : %v", p.email, err)
	}
	var rows []storedRow
	if err := cur.All(ctx, &rows); err != nil {
		a.fatalf("lecture de la copie de %s : %v", p.email, err)
	}
	out := map[string]storedRow{}
	for _, r := range rows {
		out[r.Subject] = r
	}
	return out
}

// A server that describes one message's structure in a way go-imap cannot
// read, in the middle of the inbox: the sync answers without an error, that
// message is stored marked structureUnreadable and without an attachment, the
// others with their attachments read, and the next sync does not ask for it
// again. Read in one FETCH, the whole batch would fail at every sync and
// "Pièces jointes" would stay empty.
func TestAcceptance_SyncReadsPastAnUnreadableStructure(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi := app.imapPerson(imapInboxOwner)
	pdf := func(name string) *attachedFile {
		return &attachedFile{Name: name, Type: "application/pdf", Size: 2_000}
	}
	moi.receive(
		incoming{From: "Boutique <info@boutique.com>", Subject: "Facture", Attachment: pdf("facture.pdf")},
		incoming{From: "Ami <ami@exemple.fr>", Subject: "Texte"},
		incoming{From: "Banque <releve@banque.fr>", Subject: "Illisible", Attachment: pdf("illisible.pdf")},
		incoming{From: "Assurance <contrat@assurance.fr>", Subject: "Contrat", Attachment: pdf("contrat.pdf")},
		incoming{From: "Ami <ami@exemple.fr>", Subject: "Encore du texte"},
	)

	// Mailsorter reaches the server through the proxy from now on.
	proxyPort, rewrites := corruptingTLSProxy(app, moi.port, `"illisible.pdf"`, `(`)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := app.h.db.MailAccounts().UpdateOne(ctx, bson.M{"userId": moi.email}, bson.M{"$set": bson.M{"port": proxyPort}}); err != nil {
		app.fatalf("boîte de %s rebranchée sur le relais : %v", moi.email, err)
	}

	// sync expects 200.
	moi.sync()
	if rewrites() == 0 {
		app.fatalf("le relais n'a rien rendu illisible pendant la synchronisation : le test ne prouve rien")
	}

	rows := moi.storedRows()
	for subject, want := range map[string]bool{"Facture": true, "Texte": false, "Contrat": true, "Encore du texte": false} {
		r, ok := rows[subject]
		switch {
		case !ok:
			app.failf("le mail %q n'est pas dans la copie de %s", subject, moi.email)
		case r.HasAttachments == nil:
			app.failf("la copie de %s ne sait pas si le mail %q a une pièce jointe : sa structure n'a pas été lue à côté du mail illisible", moi.email, subject)
		case *r.HasAttachments != want:
			app.failf("la copie de %s dit hasAttachments = %v pour le mail %q, attendu %v", moi.email, *r.HasAttachments, subject, want)
		case r.StructureUnreadable:
			app.failf("le mail %q de %s est marqué structureUnreadable alors que sa structure se lit", subject, moi.email)
		}
	}
	switch r, ok := rows["Illisible"]; {
	case !ok:
		app.failf("le mail illisible n'est pas dans la copie de %s", moi.email)
	case !r.StructureUnreadable:
		app.failf("le mail illisible de %s n'est pas marqué structureUnreadable (hasAttachments %v)", moi.email, r.HasAttachments)
	case r.HasAttachments == nil || *r.HasAttachments:
		app.failf("le mail illisible de %s devrait être gardé sans pièce jointe, hasAttachments = %v", moi.email, r.HasAttachments)
	}

	// The next sync does not ask for it again.
	before := rewrites()
	moi.sync()
	if after := rewrites(); after != before {
		app.failf("la synchronisation suivante a redemandé la structure du mail illisible de %s (%d réécritures de plus)", moi.email, after-before)
	}

	moi.expectFilterShows(attachmentsFilter, "après la synchronisation", []string{"Facture", "Contrat"},
		map[string]string{
			"Illisible":       "sa structure ne se lit pas",
			"Texte":           "il n'a pas de pièce jointe",
			"Encore du texte": "il n'a pas de pièce jointe",
		})
}

// "Annuler" from a screen left open on an old line. Right after the undo, a
// second "Annuler" of the same line (another tab) answers like the first. But
// once the mail has been archived again, the old line is stale: it answers
// 409 "Action déjà annulée", and the mail stays where the new archive put it.
// Over IMAP the mail came back under a new number, so the new archive is
// recorded under that one.
func TestAcceptance_StaleUndoAfterTheMailWasArchivedAgain(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi := app.imapPerson(imapInboxOwner)
	const subject = "Lettre à ranger"
	uids := moi.receive(incoming{From: "Ami <ami@exemple.fr>", Subject: subject})
	moi.sync()

	moi.act(uids[0], "archive")
	line := moi.historyLineFor(uids[0], "archive")
	if got := moi.undoFromHistory(line); got.code != http.StatusOK {
		app.fatalf("« Annuler » dans l'historique a répondu %d : %v", got.code, got.body)
	}
	if got := moi.undoFromHistory(line); got.code != http.StatusOK {
		app.failf("un second « Annuler » juste après le premier, rien n'ayant changé, devrait répondre comme lui (200), il a répondu %d : %v", got.code, got.body)
	}

	back := ""
	for _, m := range moi.inbox() {
		if m.Subject == subject {
			back = m.MessageID
		}
	}
	if back == "" {
		app.fatalf("le mail %q n'est pas revenu dans la boîte de réception de %s", subject, moi.email)
	}
	moi.act(back, "archive")
	moi.requireOnServer(serverArchive, subject, "tu viens de l'archiver de nouveau")

	if got := moi.undoFromHistory(line); got.code != http.StatusConflict {
		app.failf("« Annuler » sur l'ancienne ligne, une fois le mail archivé de nouveau, devrait répondre 409 « Action déjà annulée », il a répondu %d : %v", got.code, got.body)
	}
	moi.expectOnServer(serverArchive, subject)
}

// A mail a rule archived at the sync that received it, moved back into the
// inbox by another mail program (the person's phone): the server numbers it
// anew there, a number the copy does not know. Only its Message-ID says it is
// not a mail that arrives, and the rules a sync applies on its own leave it
// where the person put it, at the next syncs, while still sorting a mail that
// does arrive.
func TestAcceptance_AutoRulesLeaveAMailMovedBackElsewhere(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi := app.imapPerson(imapInboxOwner)
	moi.rule("Archiver la boutique", 1, rules.FieldFrom, rules.OpContains, "boutique.com", models.RuleAction{Type: rules.ActionArchive})
	moi.setAutoApplyRules(true)

	moi.receive(shopOrder)
	moi.sync()
	moi.requireOnServer(serverArchive, shopOrder.Subject, "la règle vient de l'archiver à la synchronisation qui l'a reçu")

	moi.movedFromElsewhere(serverArchive, shopOrder.Subject, serverInbox)
	moi.requireOnServer(serverInbox, shopOrder.Subject, "un autre logiciel vient de le remettre dans la boîte de réception")
	moi.sync()
	moi.receive(shopSale)
	moi.sync()

	why := "un autre logiciel l'a remis dans la boîte de réception : la règle ne doit pas le ranger de nouveau"
	moi.expectOnServer(serverInbox, shopOrder.Subject)
	moi.expectNotOnServer(serverArchive, shopOrder.Subject, why)
	if !moi.shows(shopOrder.Subject) {
		app.failf("la boîte de réception de %s devrait montrer le mail %q (%s), elle ne le montre pas", moi.email, shopOrder.Subject, why)
	}
	moi.expectOnServer(serverArchive, shopSale.Subject)
	moi.expectNotOnServer(serverInbox, shopSale.Subject, "il est arrivé ensuite, la règle l'archive à la synchronisation qui le reçoit")
}
