//go:build acceptance

package api

// Wiring tests (see wiring_imap_test.go) for what an IMAP mailbox does on
// its own on "Se désabonner" and on the action bar: the unsubscribe mail it
// writes, or refuses to write, through the mailbox's sending server, and the
// selection it moves at once. Not in the report.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/mail"
	"strings"
	"sync"
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

// The display name of a From header is the sender's to write. These mails
// come from lettre@journal.fr and carry another address in their name: the
// address of a third party (the person's boss), whose own mail sits in the
// same inbox.
const trappedFrom = `"<patron@victime.fr>" <lettre@journal.fr>`

var (
	trappedLetter = incoming{From: trappedFrom, Subject: "La lettre piégée",
		Unsubscribe: "<mailto:patron@victime.fr?subject=Je%20d%C3%A9missionne>"}
	trappedLetterAgain = incoming{From: trappedFrom, Subject: "La lettre piégée, encore",
		Unsubscribe: "<mailto:patron@victime.fr?subject=Je%20d%C3%A9missionne>"}
	bossMail = incoming{From: "Patron <patron@victime.fr>", Subject: "Point de lundi"}
)

// unsubscribeRowExists says whether this person has a row of unsubscribes
// for this sender.
func unsubscribeRowExists(a *testApp, userID, sender string) bool {
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	n, err := a.h.db.Unsubscribes().CountDocuments(ctx, bson.M{"userId": userID, "senderEmail": sender})
	if err != nil {
		a.fatalf("lecture des désabonnements de %s : %v", userID, err)
	}
	return n > 0
}

// An address in the display name is not the sender's: the mailto: it names
// is not on the sender's domain, so nothing is written to it.
func TestAcceptance_UnsubscribeMailIsNotSentToAnAddressInTheDisplayName(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, trappedLetter)
	moi.withSendingServer()

	got := moi.unsubscribeFrom(uids[0], false)

	if sent := moi.sentThroughBox(); len(sent) != 0 {
		app.failf("le serveur d'envoi de %s n'aurait dû rien recevoir, l'adresse du désabonnement n'étant que dans le nom affiché de l'expéditeur, il a reçu : %s", moi.email, describeSent(sent))
	}
	if done, _ := got["done"].(bool); done {
		app.failf("la réponse ne devrait pas dire que c'est fait : %v", got)
	}
	if mailto, _ := got["mailto"].(string); mailto != strings.Trim(trappedLetter.Unsubscribe, "<>") {
		app.failf("la réponse devrait rendre l'adresse à ouvrir dans la messagerie, elle rend %q", mailto)
	}
	if sender, _ := got["sender"].(string); sender != "lettre@journal.fr" {
		app.failf("la réponse devrait nommer l'expéditeur lettre@journal.fr, elle nomme %q", sender)
	}
}

// "Se désabonner" with "et archiver ses mails" on such a mail, over IMAP:
// only the mails of the real address leave the inbox, the boss's stays, and
// it is the real address that is recorded as unsubscribed.
func TestAcceptance_UnsubscribeAndArchiveReachOnlyTheRealAddressIMAP(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, trappedLetter, bossMail, trappedLetterAgain)

	got := moi.unsubscribeFrom(uids[0], true)

	for _, m := range []incoming{trappedLetter, trappedLetterAgain} {
		moi.expectOnServer(serverArchive, m.Subject)
		moi.expectDoesNotShow(m.Subject, "tu viens de te désabonner de lettre@journal.fr en rangeant ses mails")
	}
	moi.expectOnServer(serverInbox, bossMail.Subject)
	moi.expectNotOnServer(serverArchive, bossMail.Subject, "il est de patron@victime.fr, pas de lettre@journal.fr")
	moi.expectShows(uids[1], bossMail.Subject)
	if n := app.number(got, "archived"); n != 2 {
		app.failf("la réponse devrait compter 2 mails archivés, elle en compte %d : %v", n, got)
	}
	if unsubscribeRowExists(app, moi.email, "patron@victime.fr") {
		app.failf("patron@victime.fr ne devrait pas être enregistré comme désabonné : il n'a envoyé aucune lettre")
	}
	if !unsubscribeRowExists(app, moi.email, "lettre@journal.fr") {
		app.failf("lettre@journal.fr devrait être enregistré comme désabonné")
	}
}

// The same over the Gmail API, which never wrote an unsubscribe mail but
// archived and recorded the address of the display name all the same.
func TestAcceptance_UnsubscribeAndArchiveReachOnlyTheRealAddressGmail(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	ids := app.receiveAndSync(trappedLetter, bossMail, trappedLetterAgain)

	app.unsubscribeAndArchive(ids[0])

	app.expectNotInInbox(ids[0], ids[2])
	app.expectInInbox(ids[1])
	if unsubscribeRowExists(app, scenarioUser, "patron@victime.fr") {
		app.failf("patron@victime.fr ne devrait pas être enregistré comme désabonné : il n'a envoyé aucune lettre")
	}
	app.expectUnsubscribed("lettre@journal.fr")
}

// unsubscribeAtOnce is "Se désabonner" on each of these mails, the requests
// released at the same instant (two tabs, two lists in one click each). Like
// twiceAtOnce, on goroutines that never stop the test themselves.
func (p *imapPerson) unsubscribeAtOnce(uids ...string) []answer {
	a := p.app
	a.t.Helper()
	got := make([]answer, len(uids))
	errs := make([]error, len(uids))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, uid := range uids {
		raw, err := json.Marshal(map[string]interface{}{"messageId": uid, "alsoArchive": false})
		if err != nil {
			a.fatalf("encodage du désabonnement : %v", err)
		}
		wg.Add(1)
		go func(i int, raw []byte) {
			defer wg.Done()
			req, err := http.NewRequest(http.MethodPost, a.server.URL+"/api/unsubscribe", bytes.NewReader(raw))
			if err != nil {
				errs[i] = err
				return
			}
			req.Header.Set("Authorization", "Bearer "+p.token)
			req.Header.Set("Content-Type", "application/json")
			<-start
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				errs[i] = err
				return
			}
			defer resp.Body.Close()
			var out map[string]interface{}
			_ = json.NewDecoder(resp.Body).Decode(&out)
			got[i] = answer{code: resp.StatusCode, body: out}
		}(i, raw)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			a.fatalf("le désabonnement n°%d de %s n'a pas abouti : %v", i+1, p.email, err)
		}
	}
	return got
}

// Two lists of one publisher share their unsubscribe address. Unsubscribing
// from both at once writes to it once: the 24 hours are counted per address,
// whichever list asks.
func TestAcceptance_UnsubscribeMailIsSentOnceForTwoListsSharingAnAddress(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app,
		incoming{From: "Le Journal <lettre@journal.fr>", Subject: "La lettre", Unsubscribe: "<mailto:stop@journal.fr>"},
		incoming{From: "Les offres du Journal <offres@journal.fr>", Subject: "Les offres", Unsubscribe: "<mailto:stop@journal.fr>"},
	)
	moi.withSendingServer()

	got := moi.unsubscribeAtOnce(uids...)

	if sent := moi.sentThroughBox(); len(sent) != 1 {
		app.failf("deux désabonnements à la fois vers stop@journal.fr devraient envoyer un seul mail, le serveur d'envoi en a reçu : %s", describeSent(sent))
	}
	done, already := 0, 0
	for i, g := range got {
		if g.code != http.StatusOK {
			app.failf("le désabonnement n°%d a répondu %s", i+1, g)
			continue
		}
		if d, _ := g.body["done"].(bool); d {
			done++
		}
		if al, _ := g.body["alreadySent"].(bool); al {
			already++
		}
	}
	if done != 1 || already != 1 {
		app.failf("une réponse devrait dire que c'est fait et l'autre que la demande est déjà partie, elles disent : %v et %v", got[0].body, got[1].body)
	}
}
