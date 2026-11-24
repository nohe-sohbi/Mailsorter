//go:build acceptance

package api

// Wiring tests (see wiring_imap_test.go) for what an IMAP mailbox does on
// its own on "Se désabonner" and on the action bar: the unsubscribe mail it
// writes, or refuses to write, through the mailbox's sending server, and the
// selection it moves at once. Not in the report.

import (
	"bytes"
	"context"
	"io"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
	"github.com/nohe-sohbi/mailsorter/backend/internal/unsubscribe"
	"go.mongodb.org/mongo-driver/bson"
)

// sentBodies is the text of every message this person's sending server took,
// oldest first.
func (p *imapPerson) sentBodies() []string {
	a := p.app
	a.t.Helper()
	s := p.sendingServer()
	s.mu.Lock()
	received := append([]smtpMessage(nil), s.received...)
	s.mu.Unlock()
	out := make([]string, 0, len(received))
	for i, m := range received {
		msg, err := mail.ReadMessage(bytes.NewReader(m.data))
		if err != nil {
			a.fatalf("le message n°%d reçu par le serveur d'envoi de %s est illisible : %v", i+1, p.email, err)
		}
		var body io.Reader = msg.Body
		if strings.EqualFold(msg.Header.Get("Content-Transfer-Encoding"), "quoted-printable") {
			body = quotedprintable.NewReader(msg.Body)
		}
		text, err := io.ReadAll(body)
		if err != nil {
			a.fatalf("le texte du message n°%d reçu par le serveur d'envoi de %s est illisible : %v", i+1, p.email, err)
		}
		out = append(out, strings.TrimSpace(string(text)))
	}
	return out
}

// unsubscribeRow is this person's row of unsubscribes for a sender.
func (p *imapPerson) unsubscribeRow(sender string) models.Unsubscribe {
	a := p.app
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var row models.Unsubscribe
	if err := a.h.db.Unsubscribes().FindOne(ctx, bson.M{"userId": p.email, "senderEmail": sender}).Decode(&row); err != nil {
		a.fatalf("lecture du désabonnement de %s à %s : %v", p.email, sender, err)
	}
	return row
}

// A mailto: aimed off the sender's own domain is not written from the
// person's address: the answer hands it to the screen, as on Gmail. The
// three abuses: joining a group, confirming a token, writing to the boss.
func TestAcceptance_UnsubscribeMailtoOffTheSendersDomainIsNotSent(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	abuses := []incoming{
		{From: "Promo <promo@spam.example>", Subject: "Rejoins le groupe", Unsubscribe: "<mailto:groupe+subscribe@googlegroups.com>"},
		{From: "Promo <promo@spam.example>", Subject: "Confirme", Unsubscribe: "<mailto:groupe+subscribe@googlegroups.com?subject=confirm%20a1b2c3d4>"},
		{From: "Promo <promo2@spam.example>", Subject: "Au patron", Unsubscribe: "<mailto:patron@entreprise.fr?body=Je%20d%C3%A9missionne>"},
	}
	moi, uids := syncedIMAPInbox(app, abuses...)
	moi.withSendingServer()

	for i, uid := range uids {
		got := moi.unsubscribeFrom(uid, false)
		want := strings.Trim(abuses[i].Unsubscribe, "<>")
		if done, _ := got["done"].(bool); done {
			app.failf("le désabonnement de %q ne devrait pas être fait par le serveur, l'adresse n'étant pas chez l'expéditeur : %v", abuses[i].Subject, got)
		}
		if mailto, _ := got["mailto"].(string); mailto != want {
			app.failf("le désabonnement de %q devrait rendre à l'écran l'adresse %q à ouvrir, il rend %q", abuses[i].Subject, want, mailto)
		}
	}
	if sent := moi.sentThroughBox(); len(sent) != 0 {
		app.failf("le serveur d'envoi de %s n'aurait dû recevoir aucun mail, il a reçu : %s", moi.email, describeSent(sent))
	}
}

// On the sender's own domain the mail leaves, but its text is never the
// stranger's: the body the mailto: asks for is ignored.
func TestAcceptance_UnsubscribeMailIgnoresTheBodyItIsAskedFor(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, incoming{
		From: "RH <rh@entreprise.fr>", Subject: "La lettre RH",
		Unsubscribe: "<mailto:patron@entreprise.fr?subject=stop&body=Je%20d%C3%A9missionne>",
	})
	moi.withSendingServer()

	got := moi.unsubscribeFrom(uids[0], false)
	if to, _ := got["sentTo"].(string); to != "patron@entreprise.fr" {
		app.failf("la réponse devrait nommer l'adresse à qui le mail est parti (patron@entreprise.fr), elle nomme %q : %v", to, got)
	}
	bodies := moi.sentBodies()
	if len(bodies) != 1 || bodies[0] != unsubscribe.Body {
		app.failf("le serveur d'envoi devrait avoir reçu un mail, dont le texte est %q, il a reçu %q", unsubscribe.Body, bodies)
	}
	if row := moi.unsubscribeRow("rh@entreprise.fr"); row.SentTo != "patron@entreprise.fr" || row.SentAt.IsZero() {
		app.failf("le désabonnement gardé devrait dire à qui et quand le mail est parti, il dit %q le %v", row.SentTo, row.SentAt)
	}
}

// A second click within 24 hours sends nothing more, and says why.
func TestAcceptance_UnsubscribeMailIsNotSentTwiceInADay(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, mailtoOnlyLetter)
	moi.withSendingServer()

	moi.unsubscribeFrom(uids[0], false)
	again := moi.unsubscribeFrom(uids[0], false)

	if sent := moi.sentThroughBox(); len(sent) != 1 {
		app.failf("deux clics sur « Se désabonner » devraient envoyer un seul mail, le serveur d'envoi en a reçu : %s", describeSent(sent))
	}
	if already, _ := again["alreadySent"].(bool); !already {
		app.failf("la seconde réponse devrait dire que la demande est déjà partie : %v", again)
	}
	if to, _ := again["sentTo"].(string); to != letterUnsubscribeAddress {
		app.failf("la seconde réponse devrait nommer %s, elle nomme %q", letterUnsubscribeAddress, to)
	}
	if mailto, _ := again["mailto"].(string); mailto != "" {
		app.failf("la seconde réponse ne devrait pas ouvrir la messagerie, elle donne %q", mailto)
	}
	if message, _ := again["message"].(string); !strings.Contains(message, letterUnsubscribeAddress) {
		app.failf("la seconde réponse devrait dire en mots qu'une demande est déjà partie vers %s, elle dit %q", letterUnsubscribeAddress, message)
	}
}

// A sending server that does not answer has its own time: the mails are
// archived first, the answer says the mail did not leave and hands it to the
// screen, and the next try is not refused.
func TestAcceptance_UnsubscribeArchivesWhenTheSendingServerIsSilent(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	second := mailtoOnlyLetter
	second.Subject = "La lettre du mercredi"
	moi, uids := syncedIMAPInbox(app, mailtoOnlyLetter, second)

	silent, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		app.fatalf("serveur d'envoi muet : %v", err)
	}
	t.Cleanup(func() { _ = silent.Close() })
	go func() {
		for {
			c, err := silent.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = c.Close() })
		}
	}()
	moi.updateRow(bson.M{"$set": bson.M{
		rowSMTPHost: "127.0.0.1",
		rowSMTPPort: silent.Addr().(*net.TCPAddr).Port,
		rowSMTPTLS:  string(provider.TLSImplicit),
	}})
	budget := unsubscribeSendBudget
	unsubscribeSendBudget = 2 * time.Second
	t.Cleanup(func() { unsubscribeSendBudget = budget })

	got := moi.unsubscribeFrom(uids[0], true)

	for _, m := range []incoming{mailtoOnlyLetter, second} {
		moi.expectOnServer(serverArchive, m.Subject)
	}
	if n := app.number(got, "archived"); n != 2 {
		app.failf("la réponse devrait compter 2 mails archivés, elle en compte %d : %v", n, got)
	}
	if done, _ := got["done"].(bool); done {
		app.failf("la réponse ne devrait pas dire que c'est fait, le mail n'étant pas parti : %v", got)
	}
	if mailto, _ := got["mailto"].(string); mailto == "" {
		app.failf("la réponse devrait rendre l'adresse à ouvrir dans la messagerie : %v", got)
	}
	if message, _ := got["message"].(string); message == "" {
		app.failf("la réponse devrait dire en mots que le mail n'est pas parti : %v", got)
	}
	if row := moi.unsubscribeRow("lettre@journal.fr"); !row.SentAt.IsZero() {
		app.failf("un mail qui n'est pas parti ne devrait pas compter pour la fenêtre de 24 h, le désabonnement gardé dit %q le %v", row.SentTo, row.SentAt)
	}
}

// Archiving a selection over IMAP moves what is still in the inbox, and says
// which mails had already left it (moved from the phone): neither done nor
// failed, for the screen to take off the list.
func TestAcceptance_SelectionSaysWhatHadAlreadyLeft(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder, travelOffer)
	moi.movedElsewhere(uids[1], serverTrash)

	done := moi.actOnSelection("archive", uids...)

	moi.expectDoneInSelection(done, "l'archivage de la sélection", uids[0], uids[2])
	if n := app.number(done, "failed"); n != 0 {
		app.failf("l'archivage de la sélection ne devrait compter aucun échec, il en compte %d : %v", n, done)
	}
	out, _ := done["alreadyOut"].([]interface{})
	if len(out) != 1 || out[0] != uids[1] {
		app.failf("la réponse devrait dire que le mail n°%s était déjà sorti de la boîte, elle dit %v", uids[1], done["alreadyOut"])
	}
	for _, m := range []incoming{bankStatement, travelOffer} {
		moi.expectOnServer(serverArchive, m.Subject)
		moi.expectDoesNotShow(m.Subject, "tu viens d'archiver la sélection")
	}
	moi.expectOnServer(serverTrash, shopOrder.Subject)
	moi.expectNotOnServer(serverArchive, shopOrder.Subject, "un autre logiciel l'avait mis à la corbeille")

	// "Annuler" brings back what moved, from where the one MOVE put it.
	if got := moi.undoSelection("archive", doneInSelection(done)...); got.code != 200 || app.number(got.body, "restored") != 2 {
		app.failf("« Annuler » devrait remettre les 2 mails archivés, il a répondu %s", got)
	}
	for _, m := range []incoming{bankStatement, travelOffer} {
		moi.expectOnServer(serverInbox, m.Subject)
	}
}
