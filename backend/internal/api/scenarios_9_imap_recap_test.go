//go:build acceptance

package api

// The daily recap on a mailbox reached over IMAP.
//
// Over the Gmail API the recap leaves through Gmail itself. An IMAP mailbox
// has a sending server next to its IMAP one, and that is where the recap
// leaves from: logged in with the address and the app password the person
// connected with, from their address to their address, like a message they
// would write themselves. These sentences say it leaves, from "Envoyer un
// test" and from the daily loop, once a day at the chosen hour, and what the
// person reads when it cannot: Mailsorter does not know the sending server,
// or that server refuses the password. Each sending server is a real SMTP
// server over TLS (acceptance_smtp_test.go). What the recap says is not
// theirs to pin (internal/digest has its own tests): they compare the subject
// received with the one the recap's preview shows.

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// recapOwner is the person whose IMAP mailbox these scenarios run on. An
// address the catalog does not know, so it has no sending server to fall back
// on: where the recap leaves from is what their row says, and nothing else.
const recapOwner = "recap-imap@exemple.fr"

// unknownSendingServer is what "Envoyer un test" answers when Mailsorter does
// not know where the mailbox sends from.
const unknownSendingServer = "Mailsorter ne connaît pas le serveur d'envoi de cette boîte : le récap ne peut pas en partir."

// sendingPasswordRefused is what "Envoyer un test" answers when the sending
// server refuses the app password.
const sendingPasswordRefused = "Le serveur d'envoi de votre boîte a refusé le mot de passe d'application."

// recapWorthSending brings a person to where the sentences about a recap that
// leaves start from: one mail archived today, so the recap has something to
// sum up. It returns the subject the recap's preview shows.
func recapWorthSending(moi *imapPerson) string {
	moi.app.t.Helper()
	uids := moi.receive(shopOrder)
	moi.sync()
	moi.act(uids[0], "archive")
	moi.expectSomethingToRecap()
	return moi.recapSubject()
}

// describeSent is the messages a sending server took, in a failure message.
func describeSent(sent []sentMail) string {
	if len(sent) == 0 {
		return "aucun message"
	}
	parts := make([]string, 0, len(sent))
	for _, m := range sent {
		parts = append(parts, m.String())
	}
	return strings.Join(parts, " ; ")
}

func TestScenarios_IMAPRecap(t *testing.T) {
	playScenarios(t, "Le récap quotidien sur une boîte IMAP", []scenario{
		{
			phrase: "Sur une boîte IMAP, « Envoyer un test » du récap t'envoie ton récap par le serveur d'envoi de ta boîte, de ton adresse à ton adresse, et répond qu'il est parti.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi := app.imapPerson(recapOwner)
				moi.withSendingServer()
				subject := recapWorthSending(moi)

				got := moi.sendTestRecap()
				if status, _ := got.body["status"].(string); got.code != http.StatusOK || status != "sent" {
					app.failf("« Envoyer un test » du récap de %s devrait répondre 200 avec le statut %q, le serveur a répondu %s", moi.email, "sent", got)
				}

				sent := moi.sentThroughBox()
				if len(sent) != 1 {
					app.failf("le serveur d'envoi de %s devrait avoir reçu un récap, un seul, il a reçu : %s (%s)", moi.email, describeSent(sent), moi.sendingServer().seen())
					return
				}
				m := sent[0]
				if m.subject != subject {
					app.failf("le message reçu par le serveur d'envoi de %s devrait être son récap, au sujet %q que montre l'aperçu, il a pour sujet %q", moi.email, subject, m.subject)
				}
				if !strings.EqualFold(m.login, moi.email) {
					app.failf("le récap de %s devrait partir connecté avec son adresse, il est parti connecté en %q", moi.email, m.login)
				}
				if !onlyAddress(m.from, moi.email) || !strings.EqualFold(m.envelopeFrom, moi.email) {
					app.failf("le récap de %s devrait partir de son adresse, il part de %s (enveloppe : %q)", moi.email, strings.Join(m.from, ", "), m.envelopeFrom)
				}
				if !onlyAddress(m.to, moi.email) || !onlyAddress(m.envelopeTo, moi.email) {
					app.failf("le récap de %s devrait lui être adressé, et à lui seul, il est adressé à %s (enveloppe : %s)", moi.email, strings.Join(m.to, ", "), strings.Join(m.envelopeTo, ", "))
				}
			},
		},
		{
			phrase: "Sur une boîte IMAP, le récap quotidien activé part une fois dans la journée, à l'heure choisie, par le serveur d'envoi de ta boîte.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi := app.imapPerson(recapOwner)
				moi.withSendingServer()
				subject := recapWorthSending(moi)

				now := time.Now().UTC()
				day := now.Format("02/01/2006")

				// Not before the hour chosen: an hour still to come today.
				// There is none from 23:00 UTC on, and then only the half
				// below runs.
				if now.Hour() < 23 {
					later := now.Hour() + 1
					moi.chooseRecap(later)
					app.recapLoopTicks()
					if h := time.Now().UTC().Hour(); h != now.Hour() {
						app.fatalf("l'heure a tourné pendant le scénario (il est maintenant %02d h passées UTC) : il ne prouve rien, relance-le", h)
					}
					if sent := moi.sentThroughBox(); len(sent) > 0 {
						app.failf("le récap de %s ne devrait pas partir avant %02d:00 UTC, l'heure choisie : à %s UTC son serveur d'envoi a reçu %s", moi.email, later, now.Format("15:04"), describeSent(sent))
					}
				}

				// The current hour, whose :00 is behind us: an hour already
				// passed today. The loop ticks twice, as it does every 15
				// minutes all day long.
				moi.chooseRecap(now.Hour())
				app.recapLoopTicks()
				app.recapLoopTicks()
				if d := time.Now().UTC().Format("02/01/2006"); d != day {
					app.fatalf("le scénario a passé minuit UTC (du %s au %s) : il ne prouve rien, relance-le", day, d)
				}

				sent := moi.sentThroughBox()
				switch {
				case len(sent) == 0:
					app.failf("le récap quotidien de %s, activé à %02d:00 UTC, devrait être parti une fois par son serveur d'envoi après deux passages de la boucle à %s UTC, il n'est pas parti (%s)", moi.email, now.Hour(), now.Format("15:04"), moi.sendingServer().seen())
				case len(sent) > 1:
					app.failf("le récap quotidien de %s devrait partir une fois dans la journée, son serveur d'envoi en a reçu %d : %s", moi.email, len(sent), describeSent(sent))
				default:
					m := sent[0]
					if m.subject != subject {
						app.failf("le message reçu par le serveur d'envoi de %s devrait être son récap, au sujet %q que montre l'aperçu, il a pour sujet %q", moi.email, subject, m.subject)
					}
					if !onlyAddress(m.envelopeTo, moi.email) {
						app.failf("le récap quotidien de %s devrait lui être adressé, et à lui seul, il est adressé à %s", moi.email, strings.Join(m.envelopeTo, ", "))
					}
				}
			},
		},
		{
			phrase: "Sur une boîte IMAP dont Mailsorter ne connaît pas le serveur d'envoi, « Envoyer un test » répond « Mailsorter ne connaît pas le serveur d'envoi de cette boîte : le récap ne peut pas en partir. »",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi := app.imapPerson(recapOwner)
				moi.noSendingServerOnFile()

				moi.expectTestRecapRefused(moi.sendTestRecap(), unknownSendingServer)
			},
		},
		{
			phrase: "Sur une boîte IMAP, quand le serveur d'envoi refuse le mot de passe, « Envoyer un test » répond « Le serveur d'envoi de votre boîte a refusé le mot de passe d'application. »",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi := app.imapPerson(recapOwner)
				moi.withSendingServer()
				moi.sendingServerRefusesPassword()

				moi.expectTestRecapRefused(moi.sendTestRecap(), sendingPasswordRefused)
				// The answer is about a refusal: the server must have been
				// offered the password, and nothing must have left.
				moi.expectRefusedLogin()
				if sent := moi.sentThroughBox(); len(sent) > 0 {
					app.failf("rien ne devrait partir du serveur d'envoi de %s, qui refuse le mot de passe, il a reçu : %s", moi.email, describeSent(sent))
				}
			},
		},
	})
}
