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

// Undoing a labelled selection: over the Gmail API the label name says which
// label to take off, and without it nothing can be removed (400, rather than
// a reversal that removed nothing). Over IMAP the mail comes back from the
// folder Mailsorter recorded, with or without the name.
func TestAcceptance_BatchUndoOfALabelNeedsItsNameOnlyOnGmail(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	ids := app.receiveAndSync(shopOrder)
	if code, out := app.call(http.MethodPost, "/api/emails/batch-undo",
		map[string]interface{}{"messageIds": ids, "action": "label"}); code != http.StatusBadRequest {
		app.failf("« Annuler » d'un classement sur Gmail sans nom d'étiquette devrait répondre 400, il a répondu %d : %v", code, out)
	}

	moi, uids := syncedIMAPInbox(app, travelOffer)
	if got := moi.fileSelectionInto("Voyages", uids...); got.code != http.StatusOK || app.number(got.body, "failed") != 0 {
		app.fatalf("« Classer » la sélection dans Voyages a répondu %s", got)
	}
	moi.requireOnServer("Voyages", travelOffer.Subject, "tu viens de classer la sélection dans Voyages")
	if got := moi.undoSelection("label", uids...); got.code != http.StatusOK || app.number(got.body, "restored") != 1 {
		app.failf("« Annuler » d'un classement sur IMAP sans nom d'étiquette devrait remettre le mail, il a répondu %s", got)
	}
	moi.expectOnServer(serverInbox, travelOffer.Subject)
}

// Filing a selection over IMAP: into the user's folder named alike (the
// plural set aside), one move, a protected sender's mail left in the inbox,
// and the folder named in the answer.
func TestAcceptance_SelectionIsFiledIntoTheFolderNamedAlike(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder, shopInvoice)
	moi.newFolderElsewhere("Factures")
	moi.protect(bankStatement.From)

	got := moi.fileSelectionInto("facture", uids...)
	if got.code != http.StatusOK {
		app.fatalf("« Classer » la sélection a répondu %s", got)
	}
	moi.expectDoneInSelection(got.body, "le classement de la sélection", uids[1], uids[2])
	if name, _ := got.body["labelName"].(string); name != "Factures" {
		app.failf("la réponse devrait nommer le dossier Factures, elle nomme %q", name)
	}
	if n := app.number(got.body, "protectedSkipped"); n != 1 {
		app.failf("le classement devrait épargner 1 mail protégé, il en épargne %d", n)
	}
	for _, m := range []incoming{shopOrder, shopInvoice} {
		moi.expectOnServer("Factures", m.Subject)
		moi.expectDoesNotShow(m.Subject, "tu viens de le classer dans Factures")
	}
	moi.expectOnServer(serverInbox, bankStatement.Subject)
	moi.expectShows(uids[0], bankStatement.Subject)
	for _, uid := range uids[1:] {
		lines := moi.historyLinesOf(uid)
		if len(lines) == 0 || lines[0].Action != "label" {
			app.failf("l'historique devrait montrer le classement du mail n°%s, il montre : %s", uid, describeHistory(lines))
		}
	}
}

// A special folder is not one Mailsorter files into: the whole selection is
// refused, in the sentence the AI's proposal gets, and nothing moves.
func TestAcceptance_SelectionIsNotFiledIntoASpecialFolder(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, shopOrder)

	got := moi.fileSelectionInto("Trash", uids...)
	if got.code != http.StatusUnprocessableEntity || got.said() != notOwnFolderMessage {
		app.failf("« Classer » dans Trash devrait être refusé (422, %q), il a répondu %s", notOwnFolderMessage, got)
	}
	moi.expectOnServer(serverInbox, shopOrder.Subject)
}

// "Tout classer" for a sender over IMAP (apply-bulk with a label): all of his
// mails in one folder, made for it.
func TestAcceptance_SenderIsFiledIntoAFolder(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, _ := syncedIMAPInbox(app, shopOrder, bankStatement, shopInvoice)
	card := moi.requireSenderCard("info@boutique.com")

	code, out := app.callAs(moi.token, http.MethodPost, "/api/ai/apply-bulk",
		map[string]string{"senderEmail": card.SenderEmail, "action": "label", "labelName": "Boutique"})
	if code != http.StatusOK || app.number(out, "applied") != 2 {
		app.fatalf("classer tous les mails de info@boutique.com a répondu %d : %v", code, out)
	}
	for _, m := range []incoming{shopOrder, shopInvoice} {
		moi.expectOnServer("Boutique", m.Subject)
	}
	moi.expectOnServer(serverInbox, bankStatement.Subject)
}

// The auto-pilot of a sender whose default is a label files his mails at the
// analysis, without the model; a protected sender's stays.
func TestAcceptance_AutopilotFilesIntoAFolder(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)
	moi.protect(bankStatement.From)
	app.ai.willSay("keep", 0.95)
	moi.autopilotFiling("info@boutique.com", "Courrier")
	moi.autopilotFiling("info@banque.fr", "Courrier")

	moi.analyze(uids...)

	moi.requireOnServer("Courrier", shopOrder.Subject, "le pilote automatique de info@boutique.com le classe dans Courrier")
	moi.expectDoesNotShow(shopOrder.Subject, "le pilote automatique l'a classé")
	moi.expectOnServer(serverInbox, bankStatement.Subject)
	moi.expectShows(uids[0], bankStatement.Subject)
}

// A From whose local part is quoted and looks like someone else's address:
// "<boss@corp.com>"@evil.io is an address on evil.io. Stored unquoted, it read
// as boss@corp.com, and "Se désabonner et archiver" on this spam archived the
// boss's mail and recorded him unsubscribed.
var (
	quotedLocalSpam = incoming{From: `"<boss@corp.com>"@evil.io`, Subject: "Promo au nom piégé",
		Unsubscribe: "<mailto:boss@corp.com?subject=unsubscribe>"}
	realBossMail = incoming{From: "Patron <boss@corp.com>", Subject: "Réunion de demain"}
)

// listedSenderAddress is the senderAddress GET /api/emails gives the mail of
// this subject, and whether it lists that mail.
func listedSenderAddress(a *testApp, token, subject string) (string, bool) {
	a.t.Helper()
	code, out := a.callAs(token, http.MethodGet, "/api/emails?q=in:inbox", nil)
	if code != http.StatusOK {
		a.fatalf("GET /api/emails a répondu %d : %v", code, out)
	}
	list, _ := out["emails"].([]interface{})
	for _, raw := range list {
		m, _ := raw.(map[string]interface{})
		if s, _ := m["subject"].(string); s == subject {
			addr, _ := m["senderAddress"].(string)
			return addr, true
		}
	}
	return "", false
}

func TestAcceptance_QuotedLocalPartIsNotTheAddressItLooksLikeIMAP(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, quotedLocalSpam, realBossMail)
	moi.withSendingServer()

	if addr, ok := listedSenderAddress(app, moi.token, quotedLocalSpam.Subject); !ok || addr != `"<boss@corp.com>"@evil.io` {
		app.failf("la liste devrait donner l'adresse \"<boss@corp.com>\"@evil.io au spam, elle donne %q (listé : %v)", addr, ok)
	}

	got := moi.unsubscribeFrom(uids[0], true)

	if sent := moi.sentThroughBox(); len(sent) != 0 {
		app.failf("rien ne devrait partir vers boss@corp.com, le serveur d'envoi a reçu : %s", describeSent(sent))
	}
	moi.expectOnServer(serverInbox, realBossMail.Subject)
	moi.expectNotOnServer(serverArchive, realBossMail.Subject, "il est de boss@corp.com, pas de evil.io")
	moi.expectShows(uids[1], realBossMail.Subject)
	moi.expectOnServer(serverArchive, quotedLocalSpam.Subject)
	if unsubscribeRowExists(app, moi.email, "boss@corp.com") {
		app.failf("boss@corp.com ne devrait pas être enregistré comme désabonné")
	}
	if sender, _ := got["sender"].(string); sender != `"<boss@corp.com>"@evil.io` {
		app.failf("la réponse devrait nommer l'expéditeur \"<boss@corp.com>\"@evil.io, elle nomme %q", sender)
	}
}

func TestAcceptance_QuotedLocalPartIsNotTheAddressItLooksLikeGmail(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	ids := app.receiveAndSync(quotedLocalSpam, realBossMail)

	app.unsubscribeAndArchive(ids[0])

	app.expectNotInInbox(ids[0])
	app.expectInInbox(ids[1])
	if unsubscribeRowExists(app, scenarioUser, "boss@corp.com") {
		app.failf("boss@corp.com ne devrait pas être enregistré comme désabonné")
	}
}

// A subject that is not an unsubscribe command is not written in the
// person's name, even on the sender's own domain: the screen opens it.
func TestAcceptance_UnsubscribeMailWithASentenceAsSubjectIsNotSent(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, incoming{From: "Le Journal <lettre@journal.fr>", Subject: "La lettre du jeudi",
		Unsubscribe: "<mailto:desabonnement@journal.fr?subject=Je%20d%C3%A9missionne%20avec%20effet%20imm%C3%A9diat>"})
	moi.withSendingServer()

	got := moi.unsubscribeFrom(uids[0], false)
	if sent := moi.sentThroughBox(); len(sent) != 0 {
		app.failf("un sujet qui n'est pas une commande ne devrait pas partir, le serveur d'envoi a reçu : %s", describeSent(sent))
	}
	if mailto, _ := got["mailto"].(string); mailto == "" {
		app.failf("la réponse devrait rendre l'adresse à ouvrir dans la messagerie : %v", got)
	}
}

// The mail goes to the address as the list wrote it; only the 24-hour count
// ignores its case.
func TestAcceptance_UnsubscribeMailGoesToTheAddressAsWritten(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi, uids := syncedIMAPInbox(app, incoming{From: "Le Journal <lettre@journal.fr>", Subject: "La lettre du vendredi",
		Unsubscribe: "<mailto:Desabonnement+Lettre@journal.fr>"})
	moi.withSendingServer()

	moi.unsubscribeFrom(uids[0], false)
	sent := moi.sentThroughBox()
	if len(sent) != 1 || len(sent[0].envelopeTo) != 1 || sent[0].envelopeTo[0] != "Desabonnement+Lettre@journal.fr" {
		app.failf("le mail devrait partir vers Desabonnement+Lettre@journal.fr tel qu'écrit, il est parti : %s", describeSent(sent))
	}
}

// A Gmail header whose display name nobody quoted, "Doe, John <j@x.com>",
// names j@x.com: "Créer une règle", "Se désabonner et archiver" and
// "Protéger" all reach that address, as for any readable sender.
func TestAcceptance_GmailUnquotedNameWithACommaIsItsSendersAddress(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	const doe = "Doe, John <j@x.com>"
	const page = "<https://desabonnement.x.invalid/lettre>"
	ids := app.receiveAndSync(
		incoming{From: doe, Subject: "Le point de John"},
		incoming{From: doe, Subject: "La lettre de John", Unsubscribe: page},
		incoming{From: "Autre <autre@x.com>", Subject: "Un autre expéditeur"},
	)

	// "Créer une règle" on the card: From contains j@x.com.
	code, out := app.call(http.MethodPost, "/api/senders/rule", map[string]string{"senderEmail": doe, "action": "archive"})
	if code != http.StatusCreated && code != http.StatusOK {
		app.fatalf("« Créer une règle » pour %q a répondu %d : %v", doe, code, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var rule models.SortingRule
	if err := app.h.db.SortingRules().FindOne(ctx, bson.M{"userId": scenarioUser}).Decode(&rule); err != nil {
		app.fatalf("lecture de la règle créée : %v", err)
	}
	if len(rule.Conditions) != 1 || rule.Conditions[0].Value != "j@x.com" {
		app.failf("la règle créée pour %q devrait viser j@x.com, elle vise %+v", doe, rule.Conditions)
	}

	// "Se désabonner" with "et archiver ses mails": his two, not the other.
	app.unsubscribeAndArchive(ids[1])
	app.expectNotInInbox(ids[0], ids[1])
	app.expectInInbox(ids[2])
	if !unsubscribeRowExists(app, scenarioUser, "j@x.com") {
		app.failf("j@x.com devrait être enregistré comme désabonné")
	}

	// "Protéger": his next mail is spared by an archive of the selection.
	app.protect(doe)
	next := app.receiveAndSync(incoming{From: doe, Subject: "Encore John"})
	done := app.batchAction("archive", next...)
	if n := app.number(done, "protectedSkipped"); n != 1 {
		app.failf("l'archivage du mail de %q, protégé, devrait l'épargner, il en épargne %d : %v", doe, n, done)
	}
	app.expectInInbox(next...)
}

// A From naming two authors, the first one protected. It names no sender:
// the shield spares it while anyone is protected, and a sweep by the other
// author's address does not reach it, on both transports.
const twoAuthors = "patron@victime.fr, Lettre <lettre@journal.fr>"

func TestAcceptance_TwoAuthorsSpareTheProtectedOneGmail(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	app.protect("patron@victime.fr")
	ids := app.receiveAndSync(
		incoming{From: "Lettre <lettre@journal.fr>", Subject: "La lettre", Unsubscribe: "<https://desabonnement.x.invalid/lettre>"},
		incoming{From: twoAuthors, Subject: "Deux auteurs"},
	)

	done := app.batchAction("archive", ids[1])
	if n := app.number(done, "protectedSkipped"); n != 1 {
		app.failf("le mail dont le From nomme patron@victime.fr (protégé) devrait être épargné, la réponse en épargne %d : %v", n, done)
	}
	app.expectInInbox(ids[1])

	app.unsubscribeAndArchive(ids[0])
	app.expectNotInInbox(ids[0])
	app.expectInInbox(ids[1])
}

func TestAcceptance_TwoAuthorsSpareTheProtectedOneIMAP(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	letter := incoming{From: "Lettre <lettre@journal.fr>", Subject: "La lettre", Unsubscribe: "<https://desabonnement.x.invalid/lettre>"}
	both := incoming{From: twoAuthors, Subject: "Deux auteurs"}
	moi, uids := syncedIMAPInbox(app, letter, both)
	moi.protect("patron@victime.fr")

	if addr, ok := listedSenderAddress(app, moi.token, both.Subject); !ok || addr != "" {
		app.failf("la liste ne devrait donner aucune adresse d'expéditeur au mail à deux auteurs, elle donne %q (listé : %v)", addr, ok)
	}
	done := moi.actOnSelection("archive", uids[1])
	if n := app.number(done, "protectedSkipped"); n != 1 {
		app.failf("le mail dont le From nomme patron@victime.fr (protégé) devrait être épargné, la réponse en épargne %d : %v", n, done)
	}
	moi.expectOnServer(serverInbox, both.Subject)

	moi.unsubscribeFrom(uids[0], true)
	moi.expectOnServer(serverArchive, letter.Subject)
	moi.expectOnServer(serverInbox, both.Subject)
	moi.expectShows(uids[1], both.Subject)
}

// "Protéger" on a mail whose address has a quoted local part, through the
// route the reader calls: the selection archived next spares it.
func TestAcceptance_ProtectingAQuotedLocalPartSparesItGmail(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	const jean = `"jean dupont"@exemple.fr`
	ids := app.receiveAndSync(incoming{From: jean, Subject: "De Jean"}, incoming{From: "Autre <autre@x.fr>", Subject: "D'un autre"})
	app.protect(jean)

	done := app.batchAction("archive", ids...)
	if n := app.number(done, "protectedSkipped"); n != 1 {
		app.failf("l'archivage devrait épargner le mail de %s, protégé, il en épargne %d : %v", jean, n, done)
	}
	app.expectInInbox(ids[0])
	app.expectNotInInbox(ids[1])
}

func TestAcceptance_ProtectingAQuotedLocalPartSparesItIMAP(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	jean := incoming{From: `"jean dupont"@exemple.fr`, Subject: "De Jean"}
	other := incoming{From: "Autre <autre@x.fr>", Subject: "D'un autre"}
	moi, uids := syncedIMAPInbox(app, jean, other)
	moi.protect(jean.From)

	done := moi.actOnSelection("archive", uids...)
	if n := app.number(done, "protectedSkipped"); n != 1 {
		app.failf("l'archivage devrait épargner le mail de %s, protégé, il en épargne %d : %v", jean.From, n, done)
	}
	moi.expectOnServer(serverInbox, jean.Subject)
	moi.expectOnServer(serverArchive, other.Subject)
}

// protectFromMail is "Protéger" in the reader, on a mail whose From is from:
// POST /api/protected with the header and fromMail, as handleProtect sends
// it. It returns the answer, whatever it is.
func protectFromMail(a *testApp, token, from string) answer {
	a.t.Helper()
	code, out := a.callAs(token, http.MethodPost, "/api/protected",
		map[string]interface{}{"value": from, "fromMail": true})
	return answer{code: code, body: out}
}

// 'patron@victime.fr' is an address, apostrophes included: "Protéger" on its
// mail protects exactly it, and an archived selection spares the mail. The
// typed-entry path trims the apostrophes, and protected nobody from there.
func TestAcceptance_ProtectingFromTheMailKeepsTheAddressAsReadGmail(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	const patron = "'patron@victime.fr'"
	ids := app.receiveAndSync(incoming{From: patron, Subject: "Du patron"}, incoming{From: "Autre <autre@x.fr>", Subject: "D'un autre"})
	if got := protectFromMail(app, app.token, patron); got.code != http.StatusCreated && got.code != http.StatusOK {
		app.fatalf("« Protéger » sur le mail de %s a répondu %s", patron, got)
	}

	done := app.batchAction("archive", ids...)
	if n := app.number(done, "protectedSkipped"); n != 1 {
		app.failf("l'archivage devrait épargner le mail de %s, protégé depuis ce mail, il en épargne %d : %v", patron, n, done)
	}
	app.expectInInbox(ids[0])
	app.expectNotInInbox(ids[1])
}

func TestAcceptance_ProtectingFromTheMailKeepsTheAddressAsReadIMAP(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	patron := incoming{From: "'patron@victime.fr'", Subject: "Du patron"}
	other := incoming{From: "Autre <autre@x.fr>", Subject: "D'un autre"}
	moi, uids := syncedIMAPInbox(app, patron, other)
	row, _ := moi.shownRow(uids[0])
	if got := protectFromMail(app, moi.token, row.From); got.code != http.StatusCreated && got.code != http.StatusOK {
		app.fatalf("« Protéger » sur le mail de %s a répondu %s", row.From, got)
	}

	done := moi.actOnSelection("archive", uids...)
	if n := app.number(done, "protectedSkipped"); n != 1 {
		app.failf("l'archivage devrait épargner le mail de %s, protégé depuis ce mail, il en épargne %d : %v", row.From, n, done)
	}
	moi.expectOnServer(serverInbox, patron.Subject)
	moi.expectOnServer(serverArchive, other.Subject)
}

// A From that names no sender cannot be protected from its mail: the answer
// says so, and nothing is stored.
func TestAcceptance_ProtectingFromAMailWithNoSenderIsRefused(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	got := protectFromMail(app, app.token, twoAuthors)
	if got.code != http.StatusBadRequest || got.said() != unreadableSenderProtectMessage {
		app.failf("« Protéger » sur un mail à deux auteurs devrait être refusé (400, %q), il a répondu %s", unreadableSenderProtectMessage, got)
	}
}

// A mail with two From headers, written the way the harness writes one: the
// first header, then a second. It names no single sender: unsubscribing from
// the first author with "et archiver" does not sweep it, and it cannot be
// protected from the mail. (Over the Gmail API, imap's
// TestTheSameFromNamesTheSameSenderOnBothTransports proves the same reading
// through gmail.ParseEmailHeaders; the fake Gmail holds one From.)
func TestAcceptance_TwoFromHeadersNameNoSenderIMAP(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	letter := incoming{From: "Lettre <lettre@journal.fr>", Subject: "La lettre", Unsubscribe: "<https://desabonnement.x.invalid/lettre>"}
	twoFroms := incoming{From: "Lettre <lettre@journal.fr>\r\nFrom: Patron <patron@victime.fr>", Subject: "Deux en-têtes From"}
	moi, uids := syncedIMAPInbox(app, letter, twoFroms)

	if addr, ok := listedSenderAddress(app, moi.token, twoFroms.Subject); !ok || addr != "" {
		app.failf("la liste ne devrait donner aucune adresse d'expéditeur au mail à deux en-têtes From, elle donne %q (listé : %v)", addr, ok)
	}
	row, _ := moi.shownRow(uids[1])
	if got := protectFromMail(app, moi.token, row.From); got.code != http.StatusBadRequest {
		app.failf("« Protéger » sur le mail à deux en-têtes From devrait être refusé (400), il a répondu %s", got)
	}

	moi.unsubscribeFrom(uids[0], true)
	moi.expectOnServer(serverArchive, letter.Subject)
	moi.expectOnServer(serverInbox, twoFroms.Subject)
	moi.expectShows(uids[1], twoFroms.Subject)
}

// An accented domain is one domain in any of its forms. "café.fr" typed in
// the settings protects contact@xn--caf-dma.fr, the form a header carries;
// a letter from lettre@café.fr unsubscribes at stop@xn--caf-dma.fr (one
// registrable domain), and "et archiver" reaches its mail in both forms.
func TestAcceptance_AccentedDomainIsOneDomainIMAP(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	letter := incoming{From: "Le Café <lettre@café.fr>", Subject: "La lettre du café", Unsubscribe: "<mailto:stop@xn--caf-dma.fr>"}
	again := incoming{From: "lettre@xn--caf-dma.fr", Subject: "La lettre du café, encore"}
	shop := incoming{From: "Contact <contact@xn--caf-dma.fr>", Subject: "Votre commande au café"}
	other := incoming{From: "Autre <autre@x.fr>", Subject: "D'un autre"}
	moi, uids := syncedIMAPInbox(app, letter, again, shop, other)
	moi.withSendingServer()

	code, out := app.callAs(moi.token, http.MethodPost, "/api/protected", map[string]string{"value": "café.fr"})
	if code != http.StatusCreated && code != http.StatusOK {
		app.fatalf("protéger café.fr a répondu %d : %v", code, out)
	}
	done := moi.actOnSelection("archive", uids[2], uids[3])
	if n := app.number(done, "protectedSkipped"); n != 1 {
		app.failf("café.fr protégé devrait épargner le mail de contact@xn--caf-dma.fr, la réponse en épargne %d : %v", n, done)
	}
	moi.expectOnServer(serverInbox, shop.Subject)
	moi.expectOnServer(serverArchive, other.Subject)

	// Unprotected again, to see the sweep reach both forms.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := app.h.db.ProtectedSenders().DeleteMany(ctx, bson.M{"userId": moi.email}); err != nil {
		app.fatalf("retrait de la protection : %v", err)
	}
	got := moi.unsubscribeFrom(uids[0], true)
	sent := moi.sentThroughBox()
	if len(sent) != 1 || !onlyAddress(sent[0].envelopeTo, "stop@xn--caf-dma.fr") {
		app.failf("le mail de désabonnement de lettre@café.fr devrait partir vers stop@xn--caf-dma.fr (même domaine), il est parti : %s (réponse %v)", describeSent(sent), got)
	}
	for _, m := range []incoming{letter, again} {
		moi.expectOnServer(serverArchive, m.Subject)
	}
	moi.expectOnServer(serverInbox, shop.Subject)
}

// What the screens show for a sender whose display name is another address:
// the address read, in the list, the history and "Plus tard".
func TestAcceptance_SenderLabelIsNeverTheAddressOfTheNameGmail(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	const tricky = `"boss@corp.com" <evil@evil.io>`
	ids := app.receiveAndSync(incoming{From: tricky, Subject: "Le faux patron"}, incoming{From: tricky, Subject: "Le faux patron, encore"})

	code, out := app.call(http.MethodGet, "/api/emails?q=in:inbox", nil)
	if code != http.StatusOK {
		app.fatalf("GET /api/emails a répondu %d : %v", code, out)
	}
	list, _ := out["emails"].([]interface{})
	for _, raw := range list {
		m, _ := raw.(map[string]interface{})
		if label, _ := m["senderLabel"].(string); label != "evil@evil.io" {
			app.failf("la liste devrait montrer evil@evil.io pour %s, elle montre %q", tricky, label)
		}
	}

	app.act(ids[0], "archive")
	code, out = app.call(http.MethodGet, "/api/activity/log", nil)
	if code != http.StatusOK {
		app.fatalf("GET /api/activity/log a répondu %d : %v", code, out)
	}
	entries, _ := out["entries"].([]interface{})
	if len(entries) == 0 {
		app.fatalf("l'historique est vide après un archivage")
	}
	if e, _ := entries[0].(map[string]interface{}); e["senderLabel"] != "evil@evil.io" {
		app.failf("l'historique devrait montrer evil@evil.io pour %s, il montre %v", tricky, e["senderLabel"])
	}

	app.snooze(ids[1])
	code, out = app.call(http.MethodGet, "/api/snoozes", nil)
	if code != http.StatusOK {
		app.fatalf("GET /api/snoozes a répondu %d : %v", code, out)
	}
	snoozes, _ := out["snoozes"].([]interface{})
	if len(snoozes) == 0 {
		app.fatalf("« Plus tard » est vide après un report")
	}
	if s, _ := snoozes[0].(map[string]interface{}); s["senderLabel"] != "evil@evil.io" {
		app.failf("« Plus tard » devrait montrer evil@evil.io pour %s, il montre %v", tricky, s["senderLabel"])
	}
}
