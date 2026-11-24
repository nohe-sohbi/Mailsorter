//go:build acceptance

package api

// What happens when you act on a whole sender, on a mailbox reached over
// IMAP: "Tout archiver" on the Expéditeurs tab, "Se désabonner", and the
// auto-pilot of a sender.
//
// The same sentences as scenarios_3_sender_test.go and the auto-pilot ones of
// scenarios_5_ai_test.go, for the Gmail API, read where an IMAP mailbox keeps
// its mail: a mail filed away is MOVED into the Archive folder of the server,
// read the way another mail program reads it (acceptance_imap_test.go). The
// inbox screen is read too, without waiting for a sync.
//
// A list that offers only an address to unsubscribe is written to. Over the
// Gmail API the app hands that address to the person's own mail program. Over
// IMAP the mail leaves through the sending server of the mailbox, the one the
// daily recap leaves through, logged in with the person's address and app
// password (acceptance_smtp_test.go). Then the screen has nothing left to
// open: the answer says it is done.
//
// A list that offers the unsubscribe in one click is called by the server,
// but only at an address the outbound guard allows (internal/egress). No
// public site can run in a scenario, so the sentence is the guard's: a link
// aimed at a private address is not called, and goes back to the person to
// open (acceptance_unsubscribe_test.go).

import (
	"strings"
	"testing"
)

// The mails of the sentence about "Tout archiver". info@boutique.com is
// written with no display name, as in scenarios_3_sender_test.go: the
// sentence is about which ADDRESSES the action reaches, and the two others
// contain that address without being it.
var (
	shopSaleNoName = incoming{From: "info@boutique.com", Subject: "Les soldes commencent"}
	shopNewsNoName = incoming{From: "info@boutique.com", Subject: "Nos nouveautés"}
	shopBilling    = incoming{From: "Facturation <facturation-info@boutique.com>", Subject: "Votre facture du mois"}
	shopCommunity  = incoming{From: "info@boutique.community", Subject: "Le forum de la semaine"}
)

// letterUnsubscribeAddress is where the list of mailtoOnlyLetter takes its
// unsubscribe requests, and letterUnsubscribeSubject the subject it asks
// for: the shape RFC 2369 gives as its example.
const (
	letterUnsubscribeAddress = "desabonnement@journal.fr"
	letterUnsubscribeSubject = "unsubscribe"
)

// mailtoOnlyLetter is a newsletter whose only way out is an address.
var mailtoOnlyLetter = incoming{
	From:        "Le Journal <lettre@journal.fr>",
	Subject:     "La lettre du lundi",
	Unsubscribe: "<mailto:" + letterUnsubscribeAddress + "?subject=" + letterUnsubscribeSubject + ">",
}

// shopUnsubscribePage is the unsubscribe page the shop's newsletter names: a
// page to open in the browser, not offered in one click, so the server calls
// nothing. On a .invalid name (RFC 2606), which no resolver ever answers, so
// that nothing could leave for the internet even if it did.
const shopUnsubscribePage = "https://desabonnement.boutique.invalid/lettre"

// withUnsubscribe is the mail m carrying this List-Unsubscribe link.
func withUnsubscribe(m incoming, link string) incoming {
	m.Unsubscribe = "<" + link + ">"
	return m
}

func TestScenarios_IMAPWholeSender(t *testing.T) {
	playScenarios(t, "Quand tu agis sur tout un expéditeur, sur une boîte IMAP", []scenario{
		{
			phrase: "Sur une boîte IMAP, « Tout archiver » pour un expéditeur range tous ses mails de ta boîte de réception, et seulement les siens.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				const shop = "info@boutique.com"
				moi, uids := syncedIMAPInbox(app, shopSaleNoName, shopBilling, shopNewsNoName, shopCommunity)

				// Where the sentence starts: the tab shows the shop with its
				// two mails, the count its confirmation announces.
				if card := moi.requireSenderCard(shop); card.EmailCount != 2 {
					app.fatalf("après la synchronisation, l'onglet Expéditeurs de %s devrait montrer %s avec ses 2 mails, il en montre %d : le scénario ne prouve rien", moi.email, shop, card.EmailCount)
				}

				moi.archiveAllFrom(shop)

				for _, m := range []incoming{shopSaleNoName, shopNewsNoName} {
					moi.expectOnServer(serverArchive, m.Subject)
					moi.expectNotOnServer(serverInbox, m.Subject, "tu viens de tout archiver pour "+shop)
					moi.expectDoesNotShow(m.Subject, "tu viens de tout archiver pour "+shop)
				}
				// Only his: the two senders whose address contains his stay
				// where they were, on the server and on the screen.
				for i, m := range []incoming{shopBilling, shopCommunity} {
					uid := []string{uids[1], uids[3]}[i]
					moi.expectOnServer(serverInbox, m.Subject)
					moi.expectNotOnServer(serverArchive, m.Subject, "il n'est pas de "+shop)
					moi.expectShows(uid, m.Subject)
				}
			},
		},
		{
			phrase: "Sur une boîte IMAP, se désabonner d'une lettre d'information en demandant de ranger ses mails range tous ses mails de ta boîte de réception, et seulement les siens.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				const shop = "info@boutique.com"
				sale := withUnsubscribe(shopSaleNoName, shopUnsubscribePage)
				news := withUnsubscribe(shopNewsNoName, shopUnsubscribePage)
				moi, uids := syncedIMAPInbox(app, sale, shopBilling, news, shopCommunity)

				// "Se désabonner" on the sale, with "et archiver ses mails".
				moi.unsubscribeFrom(uids[0], true)

				for _, m := range []incoming{sale, news} {
					moi.expectOnServer(serverArchive, m.Subject)
					moi.expectNotOnServer(serverInbox, m.Subject, "tu viens de te désabonner de "+shop+" en demandant de ranger ses mails")
					moi.expectDoesNotShow(m.Subject, "tu viens de te désabonner de "+shop+" en demandant de ranger ses mails")
				}
				// Only his: the two senders whose address contains his stay
				// where they were, on the server and on the screen.
				for i, m := range []incoming{shopBilling, shopCommunity} {
					uid := []string{uids[1], uids[3]}[i]
					moi.expectOnServer(serverInbox, m.Subject)
					moi.expectNotOnServer(serverArchive, m.Subject, "il n'est pas de "+shop)
					moi.expectShows(uid, m.Subject)
				}
			},
		},
		{
			phrase: "Sur une boîte IMAP, se désabonner d'une lettre d'information dont le lien en un clic vise une adresse privée ne l'appelle pas et te donne le lien à ouvrir.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				// The list's site, on the server's own machine: a private
				// address, which a call made without the guard would reach.
				site := startFakeSite(app)
				link := site.link("/desabonnement")
				letter := incoming{
					From:            "Le Journal <lettre@journal.fr>",
					Subject:         "La lettre du mardi",
					Unsubscribe:     "<" + link + ">",
					UnsubscribePost: "List-Unsubscribe=One-Click",
				}
				moi, uids := syncedIMAPInbox(app, letter)
				site.expectReachable(app)

				got := moi.unsubscribeFrom(uids[0], false)

				if calls := site.received(); len(calls) > 0 {
					app.failf("pour %s, se désabonner de lettre@journal.fr ne devrait pas appeler son lien en un clic %s, qui vise une adresse privée : le site a reçu %s", moi.email, link, describeCalls(calls))
				}
				// What the screen does with the answer (handleUnsubscribe in
				// Inbox.js): "done" says it is over; otherwise it opens "url"
				// in a new tab.
				if done, _ := got["done"].(bool); done {
					app.failf("la réponse au désabonnement de %s ne devrait pas dire que c'est fait, puisque le lien n'est pas appelé : elle le dit (%v)", moi.email, got)
				}
				if url, _ := got["url"].(string); url != link {
					app.failf("la réponse au désabonnement de %s devrait donner le lien %s à ouvrir, elle donne %q", moi.email, link, url)
				}
			},
		},
		{
			phrase: "Sur une boîte IMAP, se désabonner d'une lettre d'information qui ne propose qu'une adresse de désabonnement envoie le mail de désabonnement par le serveur d'envoi de ta boîte, depuis ton adresse.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, mailtoOnlyLetter)
				moi.withSendingServer()

				moi.unsubscribeFrom(uids[0], false)

				sent := moi.sentThroughBox()
				if len(sent) != 1 {
					app.failf("le serveur d'envoi de %s devrait avoir reçu un mail de désabonnement, un seul, il a reçu : %s (%s)", moi.email, describeSent(sent), moi.sendingServer().seen())
					return
				}
				m := sent[0]
				// The mail the list asks for: to its address, with its
				// subject.
				if !onlyAddress(m.to, letterUnsubscribeAddress) || !onlyAddress(m.envelopeTo, letterUnsubscribeAddress) {
					app.failf("le mail de désabonnement de %s devrait être adressé à %s, et à lui seul, il est adressé à %s (enveloppe : %s)", moi.email, letterUnsubscribeAddress, strings.Join(m.to, ", "), strings.Join(m.envelopeTo, ", "))
				}
				if m.subject != letterUnsubscribeSubject {
					app.failf("le mail de désabonnement de %s devrait avoir le sujet %q que demande la lettre d'information, il a pour sujet %q", moi.email, letterUnsubscribeSubject, m.subject)
				}
				// From the person, through their own sending server.
				if !strings.EqualFold(m.login, moi.email) {
					app.failf("le mail de désabonnement de %s devrait partir connecté avec son adresse, il est parti connecté en %q", moi.email, m.login)
				}
				if !onlyAddress(m.from, moi.email) || !strings.EqualFold(m.envelopeFrom, moi.email) {
					app.failf("le mail de désabonnement de %s devrait partir de son adresse, il part de %s (enveloppe : %q)", moi.email, strings.Join(m.from, ", "), m.envelopeFrom)
				}
			},
		},
		{
			phrase: "Dans ce cas (désabonnement par adresse sur une boîte IMAP), l'écran n'ouvre pas en plus ta messagerie : la réponse dit que c'est fait.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, mailtoOnlyLetter)
				moi.withSendingServer()

				got := moi.unsubscribeFrom(uids[0], false)

				// "C'est fait" must be true: the mail has left.
				if sent := moi.sentThroughBox(); len(sent) != 1 || !onlyAddress(sent[0].envelopeTo, letterUnsubscribeAddress) {
					app.failf("le serveur d'envoi de %s devrait avoir reçu le mail de désabonnement pour %s, et lui seul, il a reçu : %s (%s)", moi.email, letterUnsubscribeAddress, describeSent(sent), moi.sendingServer().seen())
				}
				// What the screen does with the answer (handleUnsubscribe in
				// Inbox.js): on "done" it says it is over; without it, it
				// opens the person's mail program on "mailto", which would
				// write the same request a second time.
				if done, _ := got["done"].(bool); !done {
					app.failf("la réponse au désabonnement de %s devrait dire que c'est fait (done), elle ne le dit pas : %v", moi.email, got)
				}
				if mailto, _ := got["mailto"].(string); mailto != "" {
					app.failf("la réponse au désabonnement de %s ne devrait plus donner d'adresse à ouvrir dans sa messagerie, elle donne %q", moi.email, mailto)
				}
			},
		},
	})
}

func TestScenarios_IMAPAutopilot(t *testing.T) {
	playScenarios(t, "Le pilote automatique sur une boîte IMAP", []scenario{
		{
			phrase: "Sur une boîte IMAP, le pilote automatique d'un expéditeur range ses mails dès l'analyse, sans te demander et sans consulter l'IA.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				const shop = "info@boutique.com"
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder, shopInvoice)
				// Were the model asked, it would say to keep them: a mail
				// that leaves the inbox left by the auto-pilot.
				app.ai.willSay("keep", 0.95)
				moi.autopilot(shop, "archive")

				// "Trier avec l'IA" on the shop's two mails. The first mail
				// is not analyzed: it is the control.
				moi.analyze(uids[1], uids[2])

				for i, m := range []incoming{shopOrder, shopInvoice} {
					uid := uids[i+1]
					moi.expectOnServer(serverArchive, m.Subject)
					moi.expectNotOnServer(serverInbox, m.Subject, "le pilote automatique de "+shop+" l'a rangé à l'analyse")
					moi.expectDoesNotShow(m.Subject, "le pilote automatique de "+shop+" l'a rangé à l'analyse")

					// Without asking: nothing waits for an answer in the plan.
					for _, s := range moi.plan() {
						if s.EmailID == uid {
							app.failf("le plan de l'IA de %s ne devrait rien demander pour le mail n°%s %q, que le pilote automatique range, il propose %q, en attente", moi.email, uid, m.Subject, s.Action)
						}
					}
					// The Historique screen says who filed it.
					lines := moi.historyLinesOf(uid)
					found := false
					for _, l := range lines {
						if l.Action == "archive" && l.Source == SourceAIAuto {
							found = true
						}
					}
					if !found {
						app.failf("l'historique de %s devrait montrer l'archivage du mail n°%s %q par le pilote automatique (%q, %s), il montre : %s", moi.email, uid, m.Subject, "archive", SourceAIAuto, describeHistory(lines))
					}
				}
				if n := app.ai.callCount(); n != 0 {
					app.failf("l'IA ne devrait pas être consultée pour un expéditeur en pilote automatique, elle l'a été %d fois", n)
				}
				// The control: the mail nobody analyzed is still there.
				moi.expectShows(uids[0], bankStatement.Subject)
			},
		},
		{
			phrase: "Sur une boîte IMAP, le pilote automatique ne range jamais le mail d'un expéditeur protégé.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				const bank, shop = "info@banque.fr", "info@boutique.com"
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder)
				moi.protect(bankStatement.From)
				moi.autopilot(bank, "archive")
				moi.autopilot(shop, "archive")

				moi.analyze(uids...)

				// The control first: the auto-pilot of the shop, which is not
				// protected, filed its mail. An auto-pilot that files nothing
				// at all would pass the lines below.
				moi.requireOnServer(serverArchive, shopOrder.Subject, "il est de "+shop+", en pilote automatique et non protégé")

				moi.expectOnServer(serverInbox, bankStatement.Subject)
				moi.expectNotOnServer(serverArchive, bankStatement.Subject, "son expéditeur est protégé")
				moi.expectShows(uids[0], bankStatement.Subject)
			},
		},
	})
}
