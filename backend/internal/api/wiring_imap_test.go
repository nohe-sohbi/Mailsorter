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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	imapv2 "github.com/emersion/go-imap/v2"
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
	moi.sync() // the first sync of a mailbox judges nothing as arrived

	moi.arrive(shopOrder)
	moi.sync()
	moi.requireOnServer(serverArchive, shopOrder.Subject, "la règle vient de l'archiver à la synchronisation qui l'a reçu")

	moi.movedFromElsewhere(serverArchive, shopOrder.Subject, serverInbox)
	moi.requireOnServer(serverInbox, shopOrder.Subject, "un autre logiciel vient de le remettre dans la boîte de réception")
	moi.sync()
	moi.arrive(shopSale)
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

// archiveFolderGone deletes the archive folder of this person's server, from
// another mail program: until it is back (newFolderElsewhere), archiving
// fails there.
func archiveFolderGone(moi *imapPerson) {
	a := moi.app
	a.t.Helper()
	c := moi.otherProgram()
	defer c.Close()
	if err := c.Delete(serverArchive).Wait(); err != nil {
		a.fatalf("suppression du dossier %s du serveur IMAP de %s : %v", serverArchive, moi.email, err)
	}
}

// expectRulesPending checks what a sync answered about the rules: 200, as
// any sync that read and listed the mails, with the mails left for the next
// sync (rulesPending) and the sentence the screen shows as information
// (rulesMessage, absent when there is nothing to tell). when names the sync.
func expectRulesPending(moi *imapPerson, when string, pending int, message string) {
	a := moi.app
	a.t.Helper()
	code, out := moi.syncAnswer()
	if code != http.StatusOK {
		a.fatalf("%s de %s devrait répondre 200 (les mails sont relevés et listés), elle a répondu %d : %v", when, moi.email, code, out)
	}
	if n := a.number(out, "rulesPending"); n != pending {
		a.failf("%s de %s devrait laisser %d mail(s) aux règles de la synchronisation suivante (rulesPending), elle en laisse %d", when, moi.email, pending, n)
	}
	got, has := out["rulesMessage"].(string)
	switch {
	case message == "" && has:
		a.failf("%s de %s ne devrait rien dire des règles, elle dit %q", when, moi.email, got)
	case message != "" && got != message:
		a.failf("%s de %s devrait dire %q, elle dit %q", when, moi.email, message, got)
	}
}

// A sync whose rules could not range the mails that arrived (the archive is
// refused) answers 200 all the same, saying they are left for the next sync,
// and the next sync judges them again, once the archive is back. A mail the
// person put away by hand in between is not judged, nor counted.
func TestAcceptance_AutoRulesJudgeAgainWhatTheyCouldNotRange(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi := app.imapPerson(imapInboxOwner)
	moi.rule("Archiver la boutique", 1, rules.FieldFrom, rules.OpContains, "boutique.com", models.RuleAction{Type: rules.ActionArchive})
	moi.setAutoApplyRules(true)
	archiveFolderGone(moi)
	moi.sync() // the first sync of a mailbox judges nothing as arrived

	uids := moi.arrive(shopOrder, shopSale)
	expectRulesPending(moi, "la synchronisation dont les règles n'ont pas pu archiver les mails arrivés", 2,
		"Vos règles n'ont pas pu être appliquées à 2 nouveaux mails : la prochaine synchronisation réessaiera.")
	moi.expectOnServer(serverInbox, shopOrder.Subject)
	moi.expectShows(uids[0], shopOrder.Subject)

	// Put in the trash by hand before the next sync.
	moi.act(uids[1], "delete")
	moi.newFolderElsewhere(serverArchive)

	code, out := moi.syncAnswer()
	if code != http.StatusOK {
		app.fatalf("la synchronisation suivante de %s, l'archive revenue, devrait répondre 200, elle a répondu %d : %v", moi.email, code, out)
	}
	if n := app.number(out, "rulesApplied"); n != 1 {
		app.failf("la synchronisation suivante de %s devrait compter 1 mail rangé par les règles (celui mis à la corbeille à la main n'est plus à juger), elle en compte %d", moi.email, n)
	}
	if n := app.number(out, "rulesPending"); n != 0 {
		app.failf("la synchronisation suivante de %s ne devrait plus rien laisser aux règles, elle laisse %d mail(s)", moi.email, n)
	}
	if msg, has := out["rulesMessage"]; has {
		app.failf("la synchronisation suivante de %s ne devrait rien dire des règles, elle dit %v", moi.email, msg)
	}
	moi.expectOnServer(serverArchive, shopOrder.Subject)
	moi.expectNotOnServer(serverInbox, shopOrder.Subject, "la synchronisation suivante l'a archivé")
	if _, ok := ruleLineFor(moi, uids[0], "archive"); !ok {
		app.failf("l'historique de %s devrait noter que la règle a archivé le mail %q à la synchronisation suivante : lignes %s", moi.email, shopOrder.Subject, describeHistory(moi.historyLinesOf(uids[0])))
	}
	moi.expectOnServer(serverTrash, shopSale.Subject)
	moi.expectNotOnServer(serverArchive, shopSale.Subject, "tu l'as mis à la corbeille avant la synchronisation suivante")
}

// A mail its rule cannot range sync after sync is given up after maxRuleTries
// syncs: the sync from the screen that gives it up says so, once; the next
// ones say nothing, and leave it where it is, even once the archive is back.
func TestAcceptance_AutoRulesGiveUpAfterTries(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi := app.imapPerson(imapInboxOwner)
	moi.rule("Archiver la boutique", 1, rules.FieldFrom, rules.OpContains, "boutique.com", models.RuleAction{Type: rules.ActionArchive})
	moi.setAutoApplyRules(true)
	archiveFolderGone(moi)
	moi.sync() // the first sync of a mailbox judges nothing as arrived

	uids := moi.arrive(shopOrder)
	for i := 1; i < maxRuleTries; i++ {
		expectRulesPending(moi, fmt.Sprintf("la synchronisation n°%d, la règle échouant encore,", i), 1,
			"Vos règles n'ont pas pu être appliquées à 1 nouveau mail : la prochaine synchronisation réessaiera.")
	}
	expectRulesPending(moi, fmt.Sprintf("la synchronisation n°%d, qui abandonne,", maxRuleTries), 0,
		"1 mail n'a pas pu être rangé par vos règles : laissé dans la boîte.")
	expectRulesPending(moi, "la synchronisation d'après", 0, "")
	moi.newFolderElsewhere(serverArchive)
	moi.sync()
	moi.expectOnServer(serverInbox, shopOrder.Subject)
	moi.expectShows(uids[0], shopOrder.Subject)
}

// A mail the copy holds, which another mail program moves out and back
// between two syncs: back in the inbox under a new number, it is not a mail
// that arrives, even though Mailsorter never moved it, and the rules leave it
// there; a mail that does arrive is ranged at the same sync.
func TestAcceptance_AutoRulesLeaveAMailTheCopyHoldsUnderAnotherNumber(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi := app.imapPerson(imapInboxOwner)
	moi.setAutoApplyRules(true)
	moi.arrive(shopOrder)
	moi.sync()
	moi.rule("Archiver la boutique", 1, rules.FieldFrom, rules.OpContains, "boutique.com", models.RuleAction{Type: rules.ActionArchive})

	moi.movedFromElsewhere(serverInbox, shopOrder.Subject, serverTrash)
	moi.movedFromElsewhere(serverTrash, shopOrder.Subject, serverInbox)
	moi.arrive(shopSale)
	moi.sync()

	why := "la copie le tenait sous un autre numéro : ce n'est pas un mail qui arrive"
	moi.expectOnServer(serverInbox, shopOrder.Subject)
	moi.expectNotOnServer(serverArchive, shopOrder.Subject, why)
	if !moi.shows(shopOrder.Subject) {
		app.failf("la boîte de réception de %s devrait montrer le mail %q (%s), elle ne le montre pas", moi.email, shopOrder.Subject, why)
	}
	moi.expectOnServer(serverArchive, shopSale.Subject)
}

// Over IMAP the preview reads the stored copy, without opening the mailbox
// unless a rule reads the text: with the server out of reach it still
// answers. It counts the mails a rule would file in a folder (a label is a
// folder there), and shows what applying the rule does: the flags, then the
// folder.
func TestAcceptance_IMAPPreviewReadsTheCopyAndCountsTheFiling(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, _ := syncedIMAPInbox(app, bankStatement, shopOrder, travelOffer)
	moi.rule("Classer la boutique", 1, rules.FieldFrom, rules.OpContains, "boutique.com",
		models.RuleAction{Type: rules.ActionLabel, LabelName: "Boutique"})
	moi.rule("Classer et lire les voyages", 2, rules.FieldFrom, rules.OpContains, "voyages.fr",
		models.RuleAction{Type: rules.ActionLabel, LabelName: "Voyages"}, models.RuleAction{Type: rules.ActionMarkRead})

	// The mailbox no longer answers: a port nothing listens on.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		app.fatalf("réservation d'un port : %v", err)
	}
	dead := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := app.h.db.MailAccounts().UpdateOne(ctx, bson.M{"userId": moi.email}, bson.M{"$set": bson.M{"port": dead}}); err != nil {
		app.fatalf("boîte de %s rebranchée sur un port mort : %v", moi.email, err)
	}

	got := moi.previewRules()
	if got.code != http.StatusOK {
		app.fatalf("l'aperçu de %s, sans règle sur le texte, devrait se lire dans la copie sans ouvrir la boîte et répondre 200, il a répondu %s", moi.email, got)
	}
	subjects, announced := previewedMails(app, got)
	if len(subjects) != 2 || announced != 2 {
		app.failf("l'aperçu de %s devrait montrer et annoncer les 2 mails que ses règles classeraient, il montre %s et en annonce %d", moi.email, quotedList(subjects), announced)
	}
	want := map[string][]string{
		shopOrder.Subject:   {rules.ActionLabel},
		travelOffer.Subject: {rules.ActionMarkRead, rules.ActionLabel},
	}
	samples, _ := got.body["samples"].([]interface{})
	for _, v := range samples {
		item, _ := v.(map[string]interface{})
		subject, _ := item["subject"].(string)
		acts, _ := item["actions"].([]interface{})
		var types []string
		for _, a := range acts {
			act, _ := a.(map[string]interface{})
			types = append(types, fmt.Sprint(act["type"]))
			if act["type"] == rules.ActionLabel && act["labelName"] == "" {
				app.failf("l'aperçu de %s montre pour %q un classement sans dossier", moi.email, subject)
			}
		}
		if fmt.Sprint(types) != fmt.Sprint(want[subject]) {
			app.failf("l'aperçu de %s devrait montrer pour %q les actions %v (les drapeaux, puis le dossier), il montre %v", moi.email, subject, want[subject], types)
		}
	}

	// A rule on the text needs the mailbox: then the preview cannot answer.
	moi.rule("Remboursements", 3, rules.FieldBody, rules.OpContains, "remboursement", models.RuleAction{Type: rules.ActionArchive})
	if got := moi.previewRules(); got.code == http.StatusOK {
		app.failf("l'aperçu de %s, avec une règle sur le texte et la boîte injoignable, a répondu 200 : il aurait jugé le texte sans le lire (%v)", moi.email, got.body)
	}
}

// A rule that stars and files a mail, over IMAP: the star first, then the
// mail moved once, to the place chosen by what the rule means: the trash if
// it deletes, else the folder if it labels, else the archive. "Étiqueter
// Voyages, puis Archiver" (the Gmail way out of the inbox) files in Voyages,
// written once in the history with the folder; "Supprimer" with a label
// goes to the trash.
func TestAcceptance_IMAPRuleMovesAMailOnce(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, shopOrder, travelOffer, bankStatement)
	moi.rule("Suivre et classer la boutique", 1, rules.FieldFrom, rules.OpContains, "boutique.com",
		models.RuleAction{Type: rules.ActionLabel, LabelName: "Boutique"}, models.RuleAction{Type: rules.ActionStar})
	moi.rule("Classer puis archiver les voyages", 2, rules.FieldFrom, rules.OpContains, "voyages.fr",
		models.RuleAction{Type: rules.ActionLabel, LabelName: "Voyages"}, models.RuleAction{Type: rules.ActionArchive})
	moi.rule("Classer et jeter la banque", 3, rules.FieldFrom, rules.OpContains, "banque.fr",
		models.RuleAction{Type: rules.ActionLabel, LabelName: "Banque"}, models.RuleAction{Type: rules.ActionTrash})

	if got := moi.applyRules(); got.code != http.StatusOK {
		app.fatalf("« Appliquer mes règles » de %s a répondu %s", moi.email, got)
	}

	// Star, then the folder.
	moi.expectOnServer("Boutique", shopOrder.Subject)
	if !moi.flaggedOnServer("Boutique", shopOrder.Subject) {
		app.failf("le mail %q de %s devrait être en favori dans le dossier Boutique : la règle le met en favori avant de le classer", shopOrder.Subject, moi.email)
	}

	// Label and archive: filed, once, written with its folder.
	moi.expectOnServer("Voyages", travelOffer.Subject)
	moi.expectNotOnServer(serverArchive, travelOffer.Subject, "la règle classe et archive : sur une boîte IMAP elle le classe, une seule fois")
	moi.expectNotOnServer(serverInbox, travelOffer.Subject, "la règle l'a classé dans Voyages")
	var filed []string
	for _, l := range moi.history(uids[1]) {
		if l.Source == SourceRule {
			filed = append(filed, l.Action+" "+l.LabelName)
		}
	}
	if len(filed) != 1 || filed[0] != rules.ActionLabel+" Voyages" {
		app.failf("l'historique de %s devrait noter une seule ligne de la règle pour le mail %q, classé dans « Voyages », il note %v", moi.email, travelOffer.Subject, filed)
	}

	// Delete and label: the trash.
	moi.expectOnServer(serverTrash, bankStatement.Subject)
	if moi.hasFolder("Banque") {
		moi.expectNotOnServer("Banque", bankStatement.Subject, "la règle supprime : sur une boîte IMAP le mail va à la corbeille")
	}
	if n := ruleLines(moi, uids[2], rules.ActionLabel); n != 0 {
		app.failf("l'historique de %s ne devrait pas noter de classement du mail %q, mis à la corbeille, il en note %d", moi.email, bankStatement.Subject, n)
	}
}

// A mail larger than what is read of it, whose text comes after a file: its
// text is not read, so no rule on the text judges it, not even "does not
// contain", which would range it on a text nobody read. A small mail without
// the word is ranged.
func TestAcceptance_RulesDoNotJudgeATextNotRead(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi := app.imapPerson(imapInboxOwner)
	const subject = "Le fichier d'abord"
	file := strings.Repeat("QUJDRA==", 2<<15) // 512 KiB of base64, read stops inside it
	raw := "From: Boutique <info@boutique.com>\r\nTo: " + moi.email + "\r\nSubject: " + subject + "\r\n" +
		"Message-ID: <fichier-d-abord@scenarios.test>\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"sep\"\r\n\r\n" +
		"--sep\r\nContent-Type: application/pdf; name=\"a.pdf\"\r\nContent-Disposition: attachment; filename=\"a.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + file + "\r\n" +
		"--sep\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nVotre facture est jointe.\r\n--sep--\r\n"
	if _, err := moi.mailbox.Append(serverInbox, bytes.NewReader([]byte(raw)), &imapv2.AppendOptions{Time: time.Now().Add(-time.Hour)}); err != nil {
		app.fatalf("dépôt du gros mail dans la boîte IMAP de %s : %v", moi.email, err)
	}
	moi.receive(shopOrder)
	moi.sync()
	moi.rule("Sans remboursement", 1, rules.FieldBody, rules.OpNotContains, "remboursement", models.RuleAction{Type: rules.ActionArchive})

	if got := moi.applyRules(); got.code != http.StatusOK {
		app.fatalf("« Appliquer mes règles » sur la boîte IMAP de %s a répondu %s", moi.email, got)
	}
	moi.expectOnServer(serverInbox, subject)
	moi.expectNotOnServer(serverArchive, subject, "son texte n'a pas été lu, la règle ne le juge pas")
	moi.expectOnServer(serverArchive, shopOrder.Subject)
}

// The option counts from when it is turned on: the stamp moves when it goes
// from off to on, not when it is saved on again.
func TestAcceptance_AutoApplyRulesStampedWhenTurnedOn(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi := app.imapPerson(imapInboxOwner)
	stamp := func() time.Time {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var u models.User
		if err := app.h.db.Users().FindOne(ctx, bson.M{"email": moi.email}).Decode(&u); err != nil {
			app.fatalf("lecture du compte de %s : %v", moi.email, err)
		}
		return u.AutoApplyRulesSince
	}
	if !stamp().IsZero() {
		app.fatalf("le compte de %s porte une date d'activation avant toute activation", moi.email)
	}
	moi.setAutoApplyRules(true)
	first := stamp()
	if first.IsZero() {
		app.fatalf("activer l'option n'a pas noté quand elle l'a été, pour %s", moi.email)
	}
	time.Sleep(20 * time.Millisecond)
	moi.setAutoApplyRules(true)
	if again := stamp(); !again.Equal(first) {
		app.failf("enregistrer l'option déjà activée a déplacé sa date d'activation de %v à %v", first, again)
	}
	moi.setAutoApplyRules(false)
	moi.setAutoApplyRules(true)
	if later := stamp(); !later.After(first) {
		app.failf("couper puis réactiver l'option devrait noter une nouvelle date d'activation, elle reste %v (avant : %v)", later, first)
	}
}

// ruleLines counts the lines the rules wrote in this person's history for the
// mail with this number, with this action.
func ruleLines(moi *imapPerson, uid, action string) int {
	moi.app.t.Helper()
	n := 0
	for _, l := range moi.historyLinesOf(uid) {
		if l.Source == SourceRule && l.Action == action {
			n++
		}
	}
	return n
}

// appliedCountOf is the count the Règles screen shows for this rule.
func appliedCountOf(moi *imapPerson, name string) int {
	a := moi.app
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var rule models.SortingRule
	if err := a.h.db.SortingRules().FindOne(ctx, bson.M{"userId": moi.email, "name": name}).Decode(&rule); err != nil {
		a.fatalf("lecture de la règle %q de %s : %v", name, moi.email, err)
	}
	return rule.AppliedCount
}

// A rule that half-succeeds (read and star done, the archive refused) is tried
// again at the next syncs for what failed only: "lu" and "favori" are not done
// again (a mail marked unread since stays unread), nor written twice in the
// history, nor counted twice; the mail is pending until the archive passes,
// then counted once as applied.
func TestAcceptance_AutoRulesDoOnlyWhatFailedAgain(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi := app.imapPerson(imapInboxOwner)
	const name = "Lire, suivre et archiver la boutique"
	moi.rule(name, 1, rules.FieldFrom, rules.OpContains, "boutique.com",
		models.RuleAction{Type: rules.ActionMarkRead}, models.RuleAction{Type: rules.ActionStar}, models.RuleAction{Type: rules.ActionArchive})
	moi.setAutoApplyRules(true)
	archiveFolderGone(moi)
	moi.sync() // the first sync of a mailbox judges nothing as arrived

	uids := moi.arrive(shopOrder)
	pending := "Vos règles n'ont pas pu être appliquées à 1 nouveau mail : la prochaine synchronisation réessaiera."
	expectRulesPending(moi, "la synchronisation qui reçoit le mail, l'archive refusée,", 1, pending)
	if !moi.seenOnServer(serverInbox, shopOrder.Subject) || !moi.flaggedOnServer(serverInbox, shopOrder.Subject) {
		app.fatalf("la règle devrait avoir marqué lu et mis en favori le mail %q de %s avant l'archive refusée", shopOrder.Subject, moi.email)
	}

	// The person marks it unread again before the next sync.
	moi.act(uids[0], "unread")
	expectRulesPending(moi, "la synchronisation suivante, l'archive encore refusée,", 1, pending)
	if moi.seenOnServer(serverInbox, shopOrder.Subject) {
		app.failf("le mail %q de %s, remis en non lu, a été marqué lu de nouveau par la règle au nouvel essai", shopOrder.Subject, moi.email)
	}

	moi.newFolderElsewhere(serverArchive)
	code, out := moi.syncAnswer()
	if code != http.StatusOK {
		app.fatalf("la synchronisation de %s, l'archive revenue, a répondu %d : %v", moi.email, code, out)
	}
	if n := app.number(out, "rulesApplied"); n != 1 {
		app.failf("la synchronisation qui archive enfin le mail devrait compter 1 mail rangé, elle en compte %d", n)
	}
	moi.expectOnServer(serverArchive, shopOrder.Subject)
	moi.expectNotOnServer(serverInbox, shopOrder.Subject, "l'archive est revenue, le nouvel essai l'archive")
	if moi.seenOnServer(serverArchive, shopOrder.Subject) {
		app.failf("le mail %q de %s, remis en non lu, est arrivé lu dans l'archive", shopOrder.Subject, moi.email)
	}
	for action, want := range map[string]int{rules.ActionMarkRead: 1, rules.ActionStar: 1, rules.ActionArchive: 1} {
		if got := ruleLines(moi, uids[0], action); got != want {
			app.failf("l'historique de %s devrait noter %d fois %q par la règle sur le mail %q, il le note %d fois (%s)", moi.email, want, action, shopOrder.Subject, got, describeHistory(moi.historyLinesOf(uids[0])))
		}
	}
	if n := appliedCountOf(moi, name); n != 1 {
		app.failf("la règle %q devrait compter 1 mail traité, elle en compte %d", name, n)
	}
}

// A mail given up by background syncs is told by the next sync from the
// screen, once: the background sync only logs it, and three tries could pass
// without the person knowing.
func TestAcceptance_GivenUpInTheBackgroundIsToldByTheScreen(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi := app.imapPerson(imapInboxOwner)
	moi.rule("Archiver la boutique", 1, rules.FieldFrom, rules.OpContains, "boutique.com", models.RuleAction{Type: rules.ActionArchive})
	moi.setAutoApplyRules(true)
	archiveFolderGone(moi)
	moi.sync() // the first sync of a mailbox judges nothing as arrived
	moi.arrive(shopOrder)

	ctx, cancel := context.WithTimeout(context.Background(), autoSyncTimeout)
	defer cancel()
	for i := 1; i <= maxRuleTries; i++ {
		if _, _, _, err := app.h.syncInbox(ctx, moi.email); err != nil {
			app.fatalf("synchronisation de fond n°%d de %s : %v", i, moi.email, err)
		}
	}

	expectRulesPending(moi, "la synchronisation depuis l'écran, après trois essais en arrière-plan,", 0,
		"1 mail n'a pas pu être rangé par vos règles : laissé dans la boîte.")
	expectRulesPending(moi, "la synchronisation depuis l'écran d'après", 0, "")
	moi.expectOnServer(serverInbox, shopOrder.Subject)
}

// "Appliquer mes règles" leaves alone a mail a sync is judging (rulesClaim),
// the claim lasting longer than any sync: the two never act on it at once.
func TestAcceptance_ApplyRulesLeavesAMailASyncHolds(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, shopOrder, shopSale)
	moi.rule("Archiver la boutique", 1, rules.FieldFrom, rules.OpContains, "boutique.com", models.RuleAction{Type: rules.ActionArchive})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	held := time.Now().Add(-autoSyncTimeout) // a sync running for five minutes
	if _, err := app.h.db.Emails().UpdateOne(ctx, bson.M{"userId": moi.email, "messageId": uids[0]},
		bson.M{"$set": bson.M{"rulesDue": true, "rulesClaim": "une-synchro", "rulesClaimAt": held}}); err != nil {
		app.fatalf("réservation du mail n°%s de %s : %v", uids[0], moi.email, err)
	}

	if got := moi.applyRules(); got.code != http.StatusOK {
		app.fatalf("« Appliquer mes règles » de %s a répondu %s", moi.email, got)
	}
	moi.expectOnServer(serverInbox, shopOrder.Subject)
	moi.expectNotOnServer(serverArchive, shopOrder.Subject, "une synchronisation le juge en ce moment")
	moi.expectOnServer(serverArchive, shopSale.Subject)
}

// The very first sync of a mailbox judges nothing as arrived, whatever the
// dates (the mail server's clock and Mailsorter's may differ): a mail the
// server dated after the option and the connection is left there. The next
// syncs judge the mails that arrive.
func TestAcceptance_FirstSyncOfAMailboxJudgesNoArrival(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi := app.imapAccount(imapInboxOwner)
	moi.rule("Archiver la boutique", 1, rules.FieldFrom, rules.OpContains, "boutique.com", models.RuleAction{Type: rules.ActionArchive})
	moi.setAutoApplyRules(true)
	moi.connectMailbox()

	uids := moi.arrive(shopOrder)
	moi.sync()
	moi.expectOnServer(serverInbox, shopOrder.Subject)
	moi.expectNotOnServer(serverArchive, shopOrder.Subject, "la toute première synchronisation de la boîte ne juge aucun mail arrivé")
	moi.expectShows(uids[0], shopOrder.Subject)

	moi.arrive(shopSale)
	moi.sync()
	moi.expectOnServer(serverArchive, shopSale.Subject)
}

// An empty inbox, once synced, has had its first sync: the first mail that
// arrives in it afterwards is judged, though the copy held no row before.
func TestAcceptance_AnEmptyInboxHasHadItsFirstSync(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi := app.imapPerson(imapInboxOwner)
	moi.rule("Archiver la boutique", 1, rules.FieldFrom, rules.OpContains, "boutique.com", models.RuleAction{Type: rules.ActionArchive})
	moi.setAutoApplyRules(true)
	moi.sync()

	moi.arrive(shopOrder)
	moi.sync()
	moi.expectOnServer(serverArchive, shopOrder.Subject)
	moi.expectNotOnServer(serverInbox, shopOrder.Subject, "il est arrivé après la première synchronisation, d'une boîte vide")
}

// Disconnecting an IMAP mailbox clears what Mailsorter kept of it: the copy
// of its inbox and the AI's pending proposals about its mails. The history
// stays.
func TestAcceptance_DisconnectClearsTheCopyOfTheMailbox(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder, travelOffer)
	moi.act(uids[2], "archive")
	app.ai.willLabel("Banque", 0.95)
	moi.analyze(uids[0], uids[1])
	if len(moi.plan()) == 0 {
		app.fatalf("le plan de l'IA de %s est vide avant le débranchement : le test ne prouve rien", moi.email)
	}

	if code, out := app.callAs(moi.token, http.MethodDelete, "/api/mailbox", nil); code != http.StatusOK {
		app.fatalf("débrancher la boîte de %s a répondu %d : %v", moi.email, code, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if n, err := app.h.db.Emails().CountDocuments(ctx, bson.M{"userId": moi.email}); err != nil || n != 0 {
		app.failf("la copie de la boîte de %s devrait être vide après le débranchement, elle tient %d mail(s) (%v)", moi.email, n, err)
	}
	if n, err := app.h.db.AISuggestions().CountDocuments(ctx, bson.M{"userId": moi.email, "status": "pending"}); err != nil || n != 0 {
		app.failf("les propositions de l'IA en attente de %s devraient être effacées au débranchement, il en reste %d (%v)", moi.email, n, err)
	}
	if n, err := app.h.db.ActionLog().CountDocuments(ctx, bson.M{"userId": moi.email}); err != nil || n == 0 {
		app.failf("l'historique de %s devrait rester au débranchement, il tient %d ligne(s) (%v)", moi.email, n, err)
	}
}
