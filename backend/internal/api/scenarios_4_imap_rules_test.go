//go:build acceptance

package api

// What your sorting rules do on a mailbox reached over IMAP.
//
// The rules are the same on both transports: the same editor, the same
// engine (internal/rules), the same three ways to run them ("Appliquer
// maintenant" on the Règles screen, the "Aperçu" that moves nothing, and each
// sync once "appliquer mes règles automatiquement" is on). These sentences
// are the IMAP counterpart of "Tes règles" (scenarios_4_rules_test.go). Over
// IMAP a mail a rule archives, deletes or labels MOVES to another folder of
// the server, so each sentence is read on the server itself, as another mail
// program sees it, and on the inbox screen, which is served from the stored
// copy of the inbox (mirror.go). Each mailbox is a real IMAP server over TLS
// (acceptance_imap_test.go).
//
// A rule on the text of a mail needs that text, and a sync does not download
// it (the IMAP listing reads the envelope only): the sentence about it puts
// the word in the body alone, never in the subject nor the sender.
//
// Two answers of Nohé's shape the rest. The rules a sync applies on its own
// concern the mails that arrive, so a mail put back by "Annuler" after a rule
// stays in the inbox through the syncs that follow. And a mail that leaves
// the inbox takes a new number in its new folder, so a rule that ranges and
// stars a mail must do both whichever comes first.
//
// A mail ARRIVES when its server received it (INTERNALDATE) after the later
// of two moments: the option turned on, the mailbox connected. receive dates
// its mails minutes back, before both; a sentence about mails the option
// ranges has them arrive (arrive), and one about mails it leaves alone has
// them arrive between the two moments where it can, so that only the moment
// the sentence names keeps them in the inbox. And the very first sync of a
// mailbox judges none: it starts as the mailbox is connected, and what it
// finds was already there, whatever its date. So a sentence about what a sync
// does with mails that arrive syncs once before they do.
//
// A rule that labels ("Étiqueter Factures") files the mail, over IMAP, in
// the folder of that name, made when it is missing, as a proposal of the AI
// to label it does (scenarios_8_imap_ai_test.go): there the folder stands
// for the label, and the mail leaves the inbox for it.

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/rules"
)

// The actions of the rules these sentences save.
var (
	toArchive  = models.RuleAction{Type: rules.ActionArchive}
	toTrash    = models.RuleAction{Type: rules.ActionTrash}
	toStar     = models.RuleAction{Type: rules.ActionStar}
	toMarkRead = models.RuleAction{Type: rules.ActionMarkRead}
)

// The mails of these sentences besides those of scenarios_6_imap_mirror_test.go.
// Their subjects differ because a mail is found by its subject on the server.
var (
	// shopSale is a third mail from the sender of shopOrder.
	shopSale = incoming{From: "Boutique <info@boutique.com>", Subject: "Les soldes commencent"}
	// bankOffer is a second mail from the sender of bankStatement, at the
	// same address, so protecting that sender protects both.
	bankOffer = incoming{From: "Ma Banque <info@banque.fr>", Subject: "Une offre pour vous"}
	// shopPoints says "relevé" in its subject, as bankStatement does, from a
	// sender nobody protects.
	shopPoints = incoming{From: "Boutique <info@boutique.com>", Subject: "Votre relevé de points"}
	// refundDone and refundLater say "remboursement" in their text only.
	refundDone  = incoming{From: "Boutique <info@boutique.com>", Subject: "Suite à votre demande", Body: "Bonjour, votre remboursement de 42 euros a bien été effectué."}
	refundLater = incoming{From: "Boutique <info@boutique.com>", Subject: "Votre avoir", Body: "Le remboursement apparaîtra sur votre compte sous trois jours."}
	// phoneInvoice says "facture" in its subject, as shopInvoice and invoice
	// (scenarios_8_imap_ai_test.go) do, from a third sender.
	phoneInvoice = incoming{From: "Mobile <factures@mobile.fr>", Subject: "Votre facture de téléphone"}
	// gasInvoice is a second mail from the sender of invoice, at the same
	// address, so protecting that sender protects both.
	gasInvoice = incoming{From: "Énergie <factures@energie.fr>", Subject: "Votre facture de gaz"}
)

// newMailboxOwner is the person of a second mailbox in one sentence: one
// where a sync, not "Appliquer mes règles", is the first to file a mail.
const newMailboxOwner = "boite-neuve@exemple.fr"

// ruleLineFor returns the newest line of the person's Historique screen that a
// rule wrote for the mail with this number, with one of these actions, and
// whether there is one. A rule that deletes writes "trash", the rules
// engine's spelling, where a row's "Supprimer" writes "delete": the screen
// shows both as deleted and undoes both (History.js, activity.Inverse).
func ruleLineFor(moi *imapPerson, uid string, actions ...string) (historyLine, bool) {
	moi.app.t.Helper()
	for _, l := range moi.historyLinesOf(uid) {
		if l.Source != SourceRule {
			continue
		}
		for _, a := range actions {
			if l.Action == a {
				return l, true
			}
		}
	}
	return historyLine{}, false
}

// previewedMails is what the "Aperçu" of the Règles screen shows, from its
// answer: the subject of each mail a rule would act on (preview.samples in
// Rules.js), and how many mails it announces ("Aperçu : N emails",
// willApply).
func previewedMails(app *testApp, got answer) ([]string, int) {
	app.t.Helper()
	var subjects []string
	list, _ := got.body["samples"].([]interface{})
	for _, v := range list {
		item, _ := v.(map[string]interface{})
		subject, _ := item["subject"].(string)
		subjects = append(subjects, subject)
	}
	return subjects, app.number(got.body, "willApply")
}

// heldOnServer reports whether this folder of the person's server holds a
// mail with this subject, as another mail program lists it. For reading a
// flag only where the mail is: hasFlagOnServer stops the scenario on a mail
// the folder does not hold.
func heldOnServer(moi *imapPerson, folder, subject string) bool {
	moi.app.t.Helper()
	for _, s := range moi.onServer(folder) {
		if s == subject {
			return true
		}
	}
	return false
}

// requireNoFolder stops the scenario when the person's server already has a
// folder by this name: a sentence about a folder made when it is missing
// proves nothing on a server that has it.
func requireNoFolder(moi *imapPerson, folder string) {
	moi.app.t.Helper()
	if moi.hasFolder(folder) {
		moi.app.fatalf("le serveur IMAP de %s ne devrait pas encore avoir de dossier %q : le scénario ne prouve rien (dossiers : %s)", moi.email, folder, strings.Join(moi.folders(), ", "))
	}
}

// filedByRule checks that the rule filed these mails in the folder of this
// name: on the server, as another mail program sees it, and on the inbox
// screen, which no longer shows them. when says at which moment.
func filedByRule(moi *imapPerson, folder, rule, when string, mails ...incoming) {
	app := moi.app
	app.t.Helper()
	has := moi.hasFolder(folder)
	if !has {
		subjects := make([]string, 0, len(mails))
		for _, m := range mails {
			subjects = append(subjects, m.Subject)
		}
		app.failf("%s, le serveur IMAP de %s devrait avoir un dossier %q, créé s'il n'existait pas, où la règle « %s » range %s, il n'en a pas (dossiers : %s)", when, moi.email, folder, rule, quotedList(subjects), strings.Join(moi.folders(), ", "))
	}
	for _, m := range mails {
		why := fmt.Sprintf("la règle « %s » le range dans le dossier %s, %s", rule, folder, when)
		if has {
			moi.expectOnServer(folder, m.Subject)
		}
		moi.expectNotOnServer(serverInbox, m.Subject, why)
		moi.expectDoesNotShow(m.Subject, why)
	}
}

func TestScenarios_IMAPRules(t *testing.T) {
	playScenarios(t, "Tes règles sur une boîte IMAP", []scenario{
		{
			phrase: "Sur une boîte IMAP, « Appliquer mes règles » range les mails de ta boîte de réception qui correspondent à une règle, sur le serveur, et c'est noté dans l'historique.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopInvoice, shopOrder, travelOffer)
				moi.rule("Archiver la boutique", 1, rules.FieldFrom, rules.OpContains, "boutique.com", toArchive)
				moi.rule("Jeter les offres de voyage", 2, rules.FieldFrom, rules.OpContains, "voyages.fr", toTrash)

				// The invoice from the shop was archived by hand before: it
				// is no longer in the inbox, so the rules have nothing to do
				// with it.
				moi.act(uids[1], "archive")
				moi.requireOnServer(serverArchive, shopInvoice.Subject, "il vient d'être archivé à la main")

				if got := moi.applyRules(); got.code != http.StatusOK {
					app.failf("« Appliquer mes règles » sur la boîte IMAP de %s devrait répondre 200 et ranger les mails que ses règles concernent, le serveur a répondu %s", moi.email, got)
				}

				// On the server, as another mail program sees it.
				moi.expectOnServer(serverArchive, shopOrder.Subject)
				moi.expectNotOnServer(serverInbox, shopOrder.Subject, "la règle « Archiver la boutique » l'a archivé")
				moi.expectOnServer(serverTrash, travelOffer.Subject)
				moi.expectNotOnServer(serverInbox, travelOffer.Subject, "la règle « Jeter les offres de voyage » l'a mis à la corbeille")
				// On the inbox screen, right after, without a sync: what
				// Mailsorter moves leaves the screen at once.
				moi.expectDoesNotShow(shopOrder.Subject, "la règle « Archiver la boutique » l'a archivé")
				moi.expectDoesNotShow(travelOffer.Subject, "la règle « Jeter les offres de voyage » l'a mis à la corbeille")
				// The control: the mail no rule concerns stays where it was.
				moi.expectOnServer(serverInbox, bankStatement.Subject)
				moi.expectShows(uids[0], bankStatement.Subject)

				// The Historique screen: a line from the rules for each mail
				// they ranged.
				for _, c := range []struct {
					uid     string
					mail    incoming
					rule    string
					did     string
					actions []string
				}{
					{uids[2], shopOrder, "Archiver la boutique", "archivé", []string{"archive"}},
					{uids[3], travelOffer, "Jeter les offres de voyage", "mis à la corbeille", []string{"trash", "delete"}},
				} {
					if _, ok := ruleLineFor(moi, c.uid, c.actions...); !ok {
						app.failf("l'historique de %s devrait noter que la règle « %s » a %s le mail n°%s %q, il ne le note pas (lignes de ce mail : %s)", moi.email, c.rule, c.did, c.uid, c.mail.Subject, describeHistory(moi.historyLinesOf(c.uid)))
					}
				}
				// And only your own archive for the invoice, which was no
				// longer in the inbox.
				lines := moi.historyLinesOf(uids[1])
				if len(lines) != 1 || lines[0].Action != "archive" || lines[0].Source != SourceDirect {
					app.failf("l'historique de %s devrait montrer une seule ligne pour le mail n°%s %q, ton archivage à la main (\"archive\", %s), puisqu'il n'était plus dans ta boîte de réception : il en montre %d (%s)", moi.email, uids[1], shopInvoice.Subject, SourceDirect, len(lines), describeHistory(lines))
				}
			},
		},
		{
			phrase: "Sur une boîte IMAP, l'aperçu d'une règle montre les mails de ta boîte de réception qu'elle rangerait, sans rien déplacer.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopInvoice, shopOrder, shopSale)
				moi.rule("Archiver la boutique", 1, rules.FieldFrom, rules.OpContains, "boutique.com", toArchive)

				// The invoice from the shop was archived by hand before: the
				// rule concerns it, but it is no longer in the inbox.
				moi.act(uids[1], "archive")
				moi.requireOnServer(serverArchive, shopInvoice.Subject, "il vient d'être archivé à la main")

				got := moi.previewRules()
				if got.code != http.StatusOK {
					app.failf("l'aperçu de la règle « Archiver la boutique » sur la boîte IMAP de %s devrait répondre 200 avec les mails qu'elle rangerait, le serveur a répondu %s", moi.email, got)
				} else {
					want := []string{shopOrder.Subject, shopSale.Subject}
					left := map[string]string{
						bankStatement.Subject: "aucune règle ne le concerne",
						shopInvoice.Subject:   "il n'est plus dans ta boîte de réception, tu l'as archivé à la main",
					}
					subjects, announced := previewedMails(app, got)
					count := map[string]int{}
					for _, s := range subjects {
						count[s]++
					}
					wanted := map[string]bool{}
					for _, s := range want {
						wanted[s] = true
						if count[s] != 1 {
							app.failf("l'aperçu de la règle « Archiver la boutique » devrait montrer une fois le mail %q, qu'elle rangerait, il le montre %d fois (il montre : %s)", s, count[s], quotedList(subjects))
						}
					}
					told := map[string]bool{}
					for _, s := range subjects {
						if wanted[s] || told[s] {
							continue
						}
						told[s] = true
						if why, ok := left[s]; ok {
							app.failf("l'aperçu de la règle « Archiver la boutique » ne devrait pas montrer le mail %q (%s), il le montre", s, why)
						} else {
							app.failf("l'aperçu de la règle « Archiver la boutique » ne devrait montrer que %s, il montre aussi le mail %q", quotedList(want), s)
						}
					}
					if announced != len(want) {
						app.failf("l'aperçu de la règle « Archiver la boutique » devrait annoncer %d mails concernés, il en annonce %d", len(want), announced)
					}
				}

				// Nothing moved: the mails it would range are still in the
				// inbox, on the server and on the screen.
				for i, m := range []incoming{shopOrder, shopSale} {
					moi.expectOnServer(serverInbox, m.Subject)
					moi.expectNotOnServer(serverArchive, m.Subject, "l'aperçu ne déplace rien")
					moi.expectShows(uids[2+i], m.Subject)
				}
				moi.expectShows(uids[0], bankStatement.Subject)
			},
		},
		{
			phrase: "Sur une boîte IMAP, avec l'option « appliquer mes règles automatiquement » activée, un mail qui arrive et correspond à une règle est rangé dès la synchronisation qui le reçoit, et c'est noté dans l'historique.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi := app.imapPerson(imapInboxOwner)
				moi.rule("Archiver la boutique", 1, rules.FieldFrom, rules.OpContains, "boutique.com", toArchive)
				moi.rule("Jeter les offres de voyage", 2, rules.FieldFrom, rules.OpContains, "voyages.fr", toTrash)
				moi.setAutoApplyRules(true)

				// The inbox as it was: a mail no rule concerns, synced.
				first := moi.receive(bankStatement)
				moi.sync()

				// Two mails arrive, after the option is on and after the
				// mailbox was connected, each concerned by a rule, then
				// "Synchroniser".
				arrived := moi.arrive(shopOrder, travelOffer)
				moi.sync()

				// Right after that sync: on the server, and on the screen.
				moi.expectOnServer(serverArchive, shopOrder.Subject)
				moi.expectNotOnServer(serverInbox, shopOrder.Subject, "la règle « Archiver la boutique » l'a archivé à la synchronisation qui l'a reçu")
				moi.expectDoesNotShow(shopOrder.Subject, "la règle « Archiver la boutique » l'a archivé à la synchronisation qui l'a reçu")
				moi.expectOnServer(serverTrash, travelOffer.Subject)
				moi.expectNotOnServer(serverInbox, travelOffer.Subject, "la règle « Jeter les offres de voyage » l'a mis à la corbeille à la synchronisation qui l'a reçu")
				moi.expectDoesNotShow(travelOffer.Subject, "la règle « Jeter les offres de voyage » l'a mis à la corbeille à la synchronisation qui l'a reçu")
				// The control: the mail no rule concerns stays where it was.
				moi.expectOnServer(serverInbox, bankStatement.Subject)
				moi.expectShows(first[0], bankStatement.Subject)

				// The Historique screen: a line from the rules for each mail
				// they ranged at that sync.
				for _, c := range []struct {
					uid     string
					mail    incoming
					rule    string
					did     string
					actions []string
				}{
					{arrived[0], shopOrder, "Archiver la boutique", "archivé", []string{"archive"}},
					{arrived[1], travelOffer, "Jeter les offres de voyage", "mis à la corbeille", []string{"trash", "delete"}},
				} {
					if _, ok := ruleLineFor(moi, c.uid, c.actions...); !ok {
						app.failf("l'historique de %s devrait noter que la règle « %s » a %s le mail n°%s %q à la synchronisation qui l'a reçu, il ne le note pas (lignes de ce mail : %s)", moi.email, c.rule, c.did, c.uid, c.mail.Subject, describeHistory(moi.historyLinesOf(c.uid)))
					}
				}
			},
		},
		{
			phrase: "Sur une boîte IMAP, tant que cette option est coupée, synchroniser ne déplace aucun mail.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi := app.imapPerson(imapInboxOwner)
				moi.rule("Archiver la boutique", 1, rules.FieldFrom, rules.OpContains, "boutique.com", toArchive)
				moi.rule("Jeter les offres de voyage", 2, rules.FieldFrom, rules.OpContains, "voyages.fr", toTrash)
				if moi.autoApplyRulesOn() {
					app.fatalf("l'option « appliquer mes règles automatiquement » de %s devrait être coupée tant que personne ne l'a activée, l'écran Règles la montre activée : le scénario ne prouve rien", moi.email)
				}

				// They arrive after the mailbox was connected and after its
				// first sync, which judges no arrival: only the option being
				// off keeps the rules from ranging them.
				moi.sync()
				mails := []incoming{bankStatement, shopOrder, travelOffer}
				uids := moi.arrive(mails...)
				moi.sync()

				for i, m := range mails {
					moi.expectOnServer(serverInbox, m.Subject)
					moi.expectShows(uids[i], m.Subject)
					if lines := moi.historyLinesOf(uids[i]); len(lines) > 0 {
						app.failf("l'historique de %s ne devrait rien montrer pour le mail n°%s %q, que la synchronisation ne devait pas déplacer : il montre %d ligne(s) (%s)", moi.email, uids[i], m.Subject, len(lines), describeHistory(lines))
					}
				}
				moi.expectNotOnServer(serverArchive, shopOrder.Subject, "l'option « appliquer mes règles automatiquement » est coupée")
				moi.expectNotOnServer(serverTrash, travelOffer.Subject, "l'option « appliquer mes règles automatiquement » est coupée")
			},
		},
		{
			phrase: "Sur une boîte IMAP, une règle qui met en favori ou marque lu le fait sur le serveur et dans ta boîte, et le mail reste dans ta boîte de réception.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder, travelOffer)
				moi.rule("Suivre la boutique", 1, rules.FieldFrom, rules.OpContains, "boutique.com", toStar)
				moi.rule("Lire les offres de voyage", 2, rules.FieldFrom, rules.OpContains, "voyages.fr", toMarkRead)

				if got := moi.applyRules(); got.code != http.StatusOK {
					app.failf("« Appliquer mes règles » sur la boîte IMAP de %s devrait répondre 200 et appliquer ses règles, le serveur a répondu %s", moi.email, got)
				}

				// Still in the inbox, on the server and on the screen.
				for i, m := range []incoming{shopOrder, travelOffer} {
					moi.expectOnServer(serverInbox, m.Subject)
					moi.expectShows(uids[1+i], m.Subject)
				}

				// On the server, as another mail program sees it.
				if heldOnServer(moi, serverInbox, shopOrder.Subject) && !moi.flaggedOnServer(serverInbox, shopOrder.Subject) {
					app.failf("le mail %q devrait être en favori sur le serveur IMAP de %s, puisque la règle « Suivre la boutique » le met en favori : il ne l'est pas", shopOrder.Subject, moi.email)
				}
				if heldOnServer(moi, serverInbox, travelOffer.Subject) && !moi.seenOnServer(serverInbox, travelOffer.Subject) {
					app.failf("le mail %q devrait être lu sur le serveur IMAP de %s, puisque la règle « Lire les offres de voyage » le marque lu : il ne l'est pas", travelOffer.Subject, moi.email)
				}

				// On the inbox screen, right after, without a sync.
				if row, ok := moi.shownRow(uids[1]); ok && !row.starred() {
					app.failf("dans la boîte de réception de %s, le mail n°%s %q devrait apparaître en favori, puisque la règle « Suivre la boutique » le met en favori : il n'y est pas en favori", moi.email, uids[1], shopOrder.Subject)
				}
				if row, ok := moi.shownRow(uids[2]); ok && !row.IsRead {
					app.failf("dans la boîte de réception de %s, le mail n°%s %q devrait apparaître lu, puisque la règle « Lire les offres de voyage » le marque lu : il apparaît non lu", moi.email, uids[2], travelOffer.Subject)
				}
				// The control: the mail no rule concerns is left as it came.
				if row, ok := moi.shownRow(uids[0]); !ok {
					app.failf("la boîte de réception de %s devrait toujours montrer le mail n°%s %q, qu'aucune règle ne concerne, elle ne le montre plus", moi.email, uids[0], bankStatement.Subject)
				} else if row.IsRead || row.starred() {
					app.failf("dans la boîte de réception de %s, le mail n°%s %q, qu'aucune règle ne concerne, devrait rester non lu et hors des favoris : il apparaît lu=%v, en favori=%v", moi.email, uids[0], bankStatement.Subject, row.IsRead, row.starred())
				}
			},
		},
		{
			phrase: "Sur une boîte IMAP, une règle ne range jamais le mail d'un expéditeur protégé, ni à l'archive, ni à la corbeille ; « lu » et « favori » s'appliquent.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi := app.imapPerson(imapInboxOwner)
				moi.protect(bankStatement.From)
				moi.rule("Ranger les relevés", 1, rules.FieldSubject, rules.OpContains, "relevé", toMarkRead, toStar, toArchive)
				moi.rule("Jeter les offres", 2, rules.FieldSubject, rules.OpContains, "offre", toMarkRead, toStar, toTrash)
				moi.setAutoApplyRules(true)

				// Both ways a rule runs: at the sync that receives the mails,
				// which arrive after the option is on and after the first
				// sync of the mailbox, which judges no arrival, then
				// "Appliquer mes règles".
				moi.sync()
				uids := moi.arrive(bankStatement, bankOffer, shopPoints, travelOffer)
				moi.sync()
				if got := moi.applyRules(); got.code != http.StatusOK {
					app.failf("« Appliquer mes règles » sur la boîte IMAP de %s devrait répondre 200 et appliquer ses règles, le serveur a répondu %s", moi.email, got)
				}

				for _, c := range []struct {
					uid  string
					mail incoming
					rule string
				}{
					{uids[0], bankStatement, "Ranger les relevés"},
					{uids[1], bankOffer, "Jeter les offres"},
				} {
					why := fmt.Sprintf("son expéditeur est protégé, la règle « %s » ne doit pas le ranger", c.rule)
					moi.expectNotOnServer(serverArchive, c.mail.Subject, why)
					moi.expectNotOnServer(serverTrash, c.mail.Subject, why)
					moi.expectOnServer(serverInbox, c.mail.Subject)
					moi.expectShows(c.uid, c.mail.Subject)

					// "lu" and "favori" all the same: on the server...
					if heldOnServer(moi, serverInbox, c.mail.Subject) {
						if !moi.seenOnServer(serverInbox, c.mail.Subject) {
							app.failf("le mail %q, d'un expéditeur protégé, devrait être lu sur le serveur IMAP de %s : la règle « %s » le marque lu, et « lu » s'applique aussi à un expéditeur protégé ; il n'est pas lu", c.mail.Subject, moi.email, c.rule)
						}
						if !moi.flaggedOnServer(serverInbox, c.mail.Subject) {
							app.failf("le mail %q, d'un expéditeur protégé, devrait être en favori sur le serveur IMAP de %s : la règle « %s » le met en favori, et « favori » s'applique aussi à un expéditeur protégé ; il ne l'est pas", c.mail.Subject, moi.email, c.rule)
						}
					}
					// ...and in your inbox.
					if row, ok := moi.shownRow(c.uid); ok {
						if !row.IsRead {
							app.failf("dans la boîte de réception de %s, le mail n°%s %q, d'un expéditeur protégé, devrait apparaître lu : la règle « %s » le marque lu ; il apparaît non lu", moi.email, c.uid, c.mail.Subject, c.rule)
						}
						if !row.starred() {
							app.failf("dans la boîte de réception de %s, le mail n°%s %q, d'un expéditeur protégé, devrait apparaître en favori : la règle « %s » le met en favori ; il n'y est pas en favori", moi.email, c.uid, c.mail.Subject, c.rule)
						}
					}
				}

				// The controls: the same rules range the mails of a sender
				// nobody protects, so it is the protection that kept the
				// bank's mails in the inbox.
				moi.expectOnServer(serverArchive, shopPoints.Subject)
				moi.expectNotOnServer(serverInbox, shopPoints.Subject, "son expéditeur n'est pas protégé, la règle « Ranger les relevés » l'archive")
				moi.expectOnServer(serverTrash, travelOffer.Subject)
				moi.expectNotOnServer(serverInbox, travelOffer.Subject, "son expéditeur n'est pas protégé, la règle « Jeter les offres » le met à la corbeille")
			},
		},
		{
			phrase: "Sur une boîte IMAP, une règle qui porte sur le texte du mail trouve les mails dont le contenu contient le mot cherché.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				// The word is in the text of refundDone only: not in its
				// subject, and both mails come from the same sender.
				moi, uids := syncedIMAPInbox(app, shopOrder, refundDone)
				moi.rule("Remboursements", 1, rules.FieldBody, rules.OpContains, "remboursement", toArchive)

				// Reading a mail's text for a rule must not mark it read:
				// each mail stays unread on the server, as another mail
				// program shows it, whether the rule acts on it or not.
				unread := func(folder, subject, after string) {
					app.t.Helper()
					if heldOnServer(moi, folder, subject) && moi.seenOnServer(folder, subject) {
						app.failf("%s, le mail %q devrait rester non lu dans le dossier %s du serveur IMAP de %s : lire son texte pour la règle « Remboursements » ne doit pas le marquer lu ; il est marqué lu", after, subject, folder, moi.email)
					}
				}

				// "Aperçu": the mails the rule finds.
				got := moi.previewRules()
				if got.code != http.StatusOK {
					app.failf("l'aperçu de la règle « Remboursements » (le contenu contient « remboursement ») sur la boîte IMAP de %s devrait répondre 200 avec les mails qu'elle trouve, le serveur a répondu %s", moi.email, got)
				} else if subjects, announced := previewedMails(app, got); len(subjects) != 1 || subjects[0] != refundDone.Subject || announced != 1 {
					app.failf("l'aperçu de la règle « Remboursements » devrait trouver le seul mail dont le contenu contient « remboursement », %q : il montre %s, et en annonce %d", refundDone.Subject, quotedList(subjects), announced)
				}
				unread(serverInbox, refundDone.Subject, "après l'aperçu")
				unread(serverInbox, shopOrder.Subject, "après l'aperçu")

				// "Appliquer mes règles": the mail it finds is ranged.
				if got := moi.applyRules(); got.code != http.StatusOK {
					app.failf("« Appliquer mes règles » sur la boîte IMAP de %s devrait répondre 200 et ranger le mail que la règle « Remboursements » trouve, le serveur a répondu %s", moi.email, got)
				}
				moi.expectOnServer(serverArchive, refundDone.Subject)
				moi.expectNotOnServer(serverInbox, refundDone.Subject, "son contenu contient « remboursement », la règle « Remboursements » l'archive")
				moi.expectDoesNotShow(refundDone.Subject, "son contenu contient « remboursement », la règle « Remboursements » l'archive")
				unread(serverArchive, refundDone.Subject, "après « Appliquer mes règles »")
				unread(serverInbox, shopOrder.Subject, "après « Appliquer mes règles »")

				// At the sync that receives a mail arriving after the option
				// is on.
				moi.setAutoApplyRules(true)
				moi.arrive(refundLater)
				moi.sync()
				moi.expectOnServer(serverArchive, refundLater.Subject)
				moi.expectNotOnServer(serverInbox, refundLater.Subject, "son contenu contient « remboursement », la règle « Remboursements » l'archive à la synchronisation qui le reçoit")
				moi.expectDoesNotShow(refundLater.Subject, "son contenu contient « remboursement », la règle « Remboursements » l'archive à la synchronisation qui le reçoit")
				unread(serverArchive, refundLater.Subject, "après la synchronisation qui l'a reçu")

				// The control: the mail whose text does not say it stays.
				moi.expectOnServer(serverInbox, shopOrder.Subject)
				moi.expectShows(uids[0], shopOrder.Subject)
			},
		},
		{
			phrase: "Sur une boîte IMAP, « Annuler » depuis l'historique sur un mail rangé par une règle le remet dans ta boîte de réception.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder, travelOffer)
				moi.rule("Archiver la boutique", 1, rules.FieldFrom, rules.OpContains, "boutique.com", toArchive)
				moi.rule("Jeter les offres de voyage", 2, rules.FieldFrom, rules.OpContains, "voyages.fr", toTrash)

				if got := moi.applyRules(); got.code != http.StatusOK {
					app.failf("« Appliquer mes règles » sur la boîte IMAP de %s devrait répondre 200 et ranger les mails que ses règles concernent, le serveur a répondu %s : aucune règle n'a rangé de mail, il n'y a rien à annuler", moi.email, got)
				}
				moi.requireOnServer(serverArchive, shopOrder.Subject, "une règle vient de l'archiver")
				moi.requireOnServer(serverTrash, travelOffer.Subject, "une règle vient de le mettre à la corbeille")
				// The history is opened later, once a sync has gone by (one
				// every 30 minutes at most, auto_sync.go).
				moi.sync()

				cases := []struct {
					uid     string
					mail    incoming
					folder  string
					what    string
					actions []string
				}{
					{uids[1], shopOrder, serverArchive, "l'archivage", []string{"archive"}},
					{uids[2], travelOffer, serverTrash, "la mise à la corbeille", []string{"trash", "delete"}},
				}
				for _, c := range cases {
					line, ok := ruleLineFor(moi, c.uid, c.actions...)
					if !ok {
						app.fatalf("l'historique de %s ne montre pas %s du mail n°%s %q par une règle : il n'y a rien à annuler, le scénario ne prouve rien (lignes de ce mail : %s)", moi.email, c.what, c.uid, c.mail.Subject, describeHistory(moi.historyLinesOf(c.uid)))
					}
					if !line.Undoable {
						app.failf("l'historique de %s devrait proposer « Annuler » sur %s du mail n°%s %q par une règle, il ne le propose pas", moi.email, c.what, c.uid, c.mail.Subject)
					}
					if got := moi.undoFromHistory(line); got.code != http.StatusOK {
						app.failf("« Annuler » dans l'historique sur %s du mail n°%s %q par une règle devrait répondre 200, le serveur a répondu %s", c.what, c.uid, c.mail.Subject, got)
					}
				}

				for _, c := range cases {
					moi.expectBackInInbox(c.mail.Subject, "après « Annuler » dans l'historique sur "+c.what+" par une règle")
					moi.expectOnServer(serverInbox, c.mail.Subject)
					moi.expectNotOnServer(c.folder, c.mail.Subject, "« Annuler » l'a remis dans la boîte de réception")
				}
				// The control: the mail no rule concerns is still there.
				moi.expectShows(uids[0], bankStatement.Subject)
			},
		},
		{
			phrase: "Sur une boîte IMAP, un mail que tu as remis dans ta boîte avec « Annuler » après une règle n'est pas rangé de nouveau par tes règles aux synchronisations suivantes, même avec l'option activée.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi := app.imapPerson(imapInboxOwner)
				moi.rule("Archiver la boutique", 1, rules.FieldFrom, rules.OpContains, "boutique.com", toArchive)
				moi.rule("Jeter les offres de voyage", 2, rules.FieldFrom, rules.OpContains, "voyages.fr", toTrash)
				moi.setAutoApplyRules(true)

				// Two mails arrive, after the option is on and after the
				// first sync of the mailbox, which judges no arrival, and the
				// rules range them at the sync that receives them.
				moi.sync()
				arrived := moi.arrive(shopOrder, travelOffer)
				moi.sync()
				moi.requireOnServer(serverArchive, shopOrder.Subject, "une règle vient de l'archiver à la synchronisation qui l'a reçu")
				moi.requireOnServer(serverTrash, travelOffer.Subject, "une règle vient de le mettre à la corbeille à la synchronisation qui l'a reçu")

				// "Annuler" on each line of the Historique screen: the only
				// "Annuler" a rule offers.
				cases := []struct {
					uid     string
					mail    incoming
					folder  string
					what    string
					actions []string
				}{
					{arrived[0], shopOrder, serverArchive, "l'archivage", []string{"archive"}},
					{arrived[1], travelOffer, serverTrash, "la mise à la corbeille", []string{"trash", "delete"}},
				}
				for _, c := range cases {
					line, ok := ruleLineFor(moi, c.uid, c.actions...)
					if !ok {
						app.fatalf("l'historique de %s ne montre pas %s du mail n°%s %q par une règle : il n'y a rien à annuler, le scénario ne prouve rien (lignes de ce mail : %s)", moi.email, c.what, c.uid, c.mail.Subject, describeHistory(moi.historyLinesOf(c.uid)))
					}
					if got := moi.undoFromHistory(line); got.code != http.StatusOK {
						app.fatalf("« Annuler » dans l'historique sur %s du mail n°%s %q par une règle devrait répondre 200, le serveur a répondu %s : le mail n'est pas remis dans ta boîte, le scénario ne prouve rien", c.what, c.uid, c.mail.Subject, got)
					}
					moi.requireOnServer(serverInbox, c.mail.Subject, "« Annuler » vient de le remettre dans ta boîte de réception")
				}

				// The syncs that follow, the option still on. A mail that
				// arrives between them is the control: the rules still range
				// what arrives, so it is not their being off that leaves the
				// two mails in the inbox.
				moi.sync()
				moi.arrive(shopSale)
				moi.sync()

				for _, c := range cases {
					why := "tu l'as remis dans ta boîte avec « Annuler » : les règles ne doivent pas le ranger de nouveau"
					moi.expectOnServer(serverInbox, c.mail.Subject)
					moi.expectNotOnServer(c.folder, c.mail.Subject, why)
					moi.expectBackInInbox(c.mail.Subject, "deux synchronisations après « Annuler » sur "+c.what+" par une règle")
				}
				moi.expectOnServer(serverArchive, shopSale.Subject)
				moi.expectNotOnServer(serverInbox, shopSale.Subject, "il est arrivé après « Annuler », la règle « Archiver la boutique » l'archive à la synchronisation qui le reçoit")
			},
		},
		{
			phrase: "Sur une boîte IMAP, une règle qui range et met en favori fait les deux, quel que soit l'ordre de ses actions.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, bankStatement, shopOrder, travelOffer)
				// The two orders: over IMAP the archive moves the mail to
				// another folder, where the server numbers it anew, so the
				// star must reach it there when it comes second.
				moi.rule("Archiver puis suivre la boutique", 1, rules.FieldFrom, rules.OpContains, "boutique.com", toArchive, toStar)
				moi.rule("Suivre puis archiver les voyages", 2, rules.FieldFrom, rules.OpContains, "voyages.fr", toStar, toArchive)

				if got := moi.applyRules(); got.code != http.StatusOK {
					app.failf("« Appliquer mes règles » sur la boîte IMAP de %s devrait répondre 200 et appliquer ses règles, le serveur a répondu %s", moi.email, got)
				}

				for _, c := range []struct {
					uid  string
					mail incoming
					rule string
				}{
					{uids[1], shopOrder, "Archiver puis suivre la boutique"},
					{uids[2], travelOffer, "Suivre puis archiver les voyages"},
				} {
					moi.expectOnServer(serverArchive, c.mail.Subject)
					moi.expectNotOnServer(serverInbox, c.mail.Subject, "la règle « "+c.rule+" » l'a archivé")
					moi.expectDoesNotShow(c.mail.Subject, "la règle « "+c.rule+" » l'a archivé")
					if heldOnServer(moi, serverArchive, c.mail.Subject) && !moi.flaggedOnServer(serverArchive, c.mail.Subject) {
						app.failf("le mail %q devrait être en favori dans le dossier %s du serveur IMAP de %s, puisque la règle « %s » l'archive et le met en favori : il y est, mais pas en favori", c.mail.Subject, serverArchive, moi.email, c.rule)
					}
				}
				// The control: the mail no rule concerns stays where it was.
				moi.expectOnServer(serverInbox, bankStatement.Subject)
				moi.expectShows(uids[0], bankStatement.Subject)
			},
		},
		{
			phrase: "Sur une boîte IMAP, activer l'option « appliquer mes règles automatiquement » ne range pas les mails déjà dans ta boîte de réception : seuls ceux qui arrivent ensuite le sont, et « Appliquer mes règles » range les autres.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				// Two mails the rules concern, already in the inbox and
				// synced while the option was off. They arrived after the
				// mailbox was connected: only the option being turned on
				// after them keeps the rules from ranging them.
				moi := app.imapPerson(imapInboxOwner)
				uids := moi.arrive(bankStatement, shopOrder, travelOffer)
				moi.sync()
				moi.rule("Archiver la boutique", 1, rules.FieldFrom, rules.OpContains, "boutique.com", toArchive)
				moi.rule("Jeter les offres de voyage", 2, rules.FieldFrom, rules.OpContains, "voyages.fr", toTrash)
				moi.setAutoApplyRules(true)

				already := []struct {
					uid    string
					mail   incoming
					folder string
				}{
					{uids[1], shopOrder, serverArchive},
					{uids[2], travelOffer, serverTrash},
				}
				stayed := func(when string) {
					app.t.Helper()
					for _, c := range already {
						why := "il était déjà dans ta boîte de réception quand tu as activé l'option, " + when
						moi.expectOnServer(serverInbox, c.mail.Subject)
						moi.expectNotOnServer(c.folder, c.mail.Subject, why)
						moi.expectShows(c.uid, c.mail.Subject)
					}
				}

				// The sync right after the option is on.
				moi.sync()
				stayed("à la synchronisation qui suit")

				// A mail arrives, then "Synchroniser": that one is ranged.
				moi.arrive(shopSale)
				moi.sync()
				moi.expectOnServer(serverArchive, shopSale.Subject)
				moi.expectNotOnServer(serverInbox, shopSale.Subject, "il est arrivé après que tu as activé l'option, la règle « Archiver la boutique » l'archive à la synchronisation qui le reçoit")
				moi.expectDoesNotShow(shopSale.Subject, "il est arrivé après que tu as activé l'option, la règle « Archiver la boutique » l'archive à la synchronisation qui le reçoit")
				stayed("même à la synchronisation qui a rangé un mail arrivé ensuite")

				// "Appliquer mes règles" ranges the others.
				if got := moi.applyRules(); got.code != http.StatusOK {
					app.failf("« Appliquer mes règles » sur la boîte IMAP de %s devrait répondre 200 et ranger les mails que ses règles concernent, le serveur a répondu %s", moi.email, got)
				}
				for _, c := range already {
					why := "« Appliquer mes règles » range les mails déjà dans ta boîte de réception que tes règles concernent"
					moi.expectOnServer(c.folder, c.mail.Subject)
					moi.expectNotOnServer(serverInbox, c.mail.Subject, why)
					moi.expectDoesNotShow(c.mail.Subject, why)
				}
				// The control: the mail no rule concerns stays where it was.
				moi.expectOnServer(serverInbox, bankStatement.Subject)
				moi.expectShows(uids[0], bankStatement.Subject)
			},
		},
		{
			phrase: "Sur une boîte IMAP branchée alors que l'option « appliquer mes règles automatiquement » est déjà activée, les mails déjà dans la boîte ne sont pas rangés à la première synchronisation : seuls ceux qui arrivent ensuite le sont.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				// The account, its rules and the option on, before its
				// mailbox is connected to Mailsorter.
				moi := app.imapAccount(imapInboxOwner)
				moi.rule("Archiver la boutique", 1, rules.FieldFrom, rules.OpContains, "boutique.com", toArchive)
				moi.rule("Jeter les offres de voyage", 2, rules.FieldFrom, rules.OpContains, "voyages.fr", toTrash)
				moi.setAutoApplyRules(true)

				// What the mailbox holds when it is connected: a mail from
				// before the option was turned on, and two that came after
				// it but before the mailbox was connected. Those two tell
				// the date of the connection from that of the option.
				before := moi.receive(shopInvoice)
				between := moi.arrive(shopOrder, travelOffer)
				moi.connectMailbox()
				// And one that comes after the mailbox is connected, before
				// its first sync: dated after both moments, it is already in
				// the mailbox when that sync lists it, and only the rule of
				// the first sync, which judges no arrival, keeps the rules
				// from ranging it.
				beforeFirstSync := moi.arrive(shopPoints)

				const there = "il était déjà dans ta boîte quand tu l'as branchée, reçu "
				already := []struct {
					uid    string
					mail   incoming
					folder string
					why    string
				}{
					{before[0], shopInvoice, serverArchive, there + "avant que tu actives l'option"},
					{between[0], shopOrder, serverArchive, there + "après que tu as activé l'option, mais avant que tu branches ta boîte"},
					{between[1], travelOffer, serverTrash, there + "après que tu as activé l'option, mais avant que tu branches ta boîte"},
					{beforeFirstSync[0], shopPoints, serverArchive, "il était déjà dans ta boîte à sa première synchronisation, reçu après que tu l'as branchée, mais avant cette synchronisation"},
				}
				stayed := func(at string) {
					app.t.Helper()
					for _, c := range already {
						why := c.why
						moi.expectOnServer(serverInbox, c.mail.Subject)
						moi.expectNotOnServer(c.folder, c.mail.Subject, why+", "+at)
						moi.expectShows(c.uid, c.mail.Subject)
						if lines := moi.historyLinesOf(c.uid); len(lines) > 0 {
							app.failf("%s, l'historique de %s ne devrait rien montrer pour le mail n°%s %q, %s : il montre %d ligne(s) (%s)", at, moi.email, c.uid, c.mail.Subject, why, len(lines), describeHistory(lines))
						}
					}
				}

				// The first sync.
				moi.sync()
				stayed("à la première synchronisation")

				// A mail arrives, then "Synchroniser": that one is ranged.
				moi.arrive(shopSale)
				moi.sync()
				moi.expectOnServer(serverArchive, shopSale.Subject)
				moi.expectNotOnServer(serverInbox, shopSale.Subject, "il est arrivé après que tu as branché ta boîte, l'option activée, la règle « Archiver la boutique » l'archive à la synchronisation qui le reçoit")
				moi.expectDoesNotShow(shopSale.Subject, "il est arrivé après que tu as branché ta boîte, l'option activée, la règle « Archiver la boutique » l'archive à la synchronisation qui le reçoit")
				stayed("même à la synchronisation qui a rangé un mail arrivé ensuite")
			},
		},
		{
			phrase: "Sur une boîte IMAP, une règle qui étiquette « Factures » range les mails qui lui correspondent dans le dossier « Factures » de ta boîte, créé s'il n'existait pas, à « Appliquer » comme à la synchronisation.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				const folder = "Factures"
				const rule = "Étiqueter les factures"
				toInvoices := models.RuleAction{Type: rules.ActionLabel, LabelName: folder}

				// Two invoices from two senders, and a mail the rule does
				// not concern. Where the sentence starts: no folder
				// "Factures" on the server, so filing a mail there has to
				// make it.
				moi, uids := syncedIMAPInbox(app, bankStatement, shopInvoice, invoice)
				requireNoFolder(moi, folder)
				moi.rule(rule, 1, rules.FieldSubject, rules.OpContains, "facture", toInvoices)
				invoices := []incoming{shopInvoice, invoice}

				// "Aperçu": it counts the two invoices, and moves nothing.
				got := moi.previewRules()
				if got.code != http.StatusOK {
					app.failf("l'aperçu de la règle « %s » sur la boîte IMAP de %s devrait répondre 200 avec les mails qu'elle rangerait dans %q, le serveur a répondu %s", rule, moi.email, folder, got)
				} else {
					subjects, announced := previewedMails(app, got)
					count := map[string]int{}
					for _, s := range subjects {
						count[s]++
					}
					for _, m := range invoices {
						if count[m.Subject] != 1 {
							app.failf("l'aperçu de la règle « %s » devrait montrer une fois le mail %q, qu'elle rangerait dans %q, il le montre %d fois (il montre : %s)", rule, m.Subject, folder, count[m.Subject], quotedList(subjects))
						}
					}
					if count[bankStatement.Subject] > 0 {
						app.failf("l'aperçu de la règle « %s » ne devrait pas montrer le mail %q (son sujet ne dit pas « facture »), il le montre", rule, bankStatement.Subject)
					}
					if announced != len(invoices) {
						app.failf("l'aperçu de la règle « %s » devrait annoncer %d mails concernés, ceux qu'elle rangerait dans %q, il en annonce %d", rule, len(invoices), folder, announced)
					}
				}
				for _, m := range invoices {
					moi.requireOnServer(serverInbox, m.Subject, "il y était avant l'aperçu, qui ne déplace rien")
				}

				// "Appliquer mes règles": the folder is made, and the two
				// invoices are filed in it.
				if got := moi.applyRules(); got.code != http.StatusOK {
					app.failf("« Appliquer mes règles » sur la boîte IMAP de %s devrait répondre 200 et ranger dans %q les mails que la règle « %s » concerne, le serveur a répondu %s", moi.email, folder, rule, got)
				}
				filedByRule(moi, folder, rule, "après « Appliquer mes règles »", invoices...)
				// The control: the mail the rule does not concern stays.
				moi.expectOnServer(serverInbox, bankStatement.Subject)
				moi.expectShows(uids[0], bankStatement.Subject)

				// At the sync that receives a mail arriving once the option
				// is on (syncedIMAPInbox ran the first sync, which judges no
				// arrival), into the folder now there.
				moi.setAutoApplyRules(true)
				arrived := moi.arrive(phoneInvoice, travelOffer)
				moi.sync()
				filedByRule(moi, folder, rule, "à la synchronisation qui l'a reçu", phoneInvoice)
				moi.expectOnServer(serverInbox, travelOffer.Subject)
				moi.expectShows(arrived[1], travelOffer.Subject)

				// And a mailbox where a sync is the first to file a mail in
				// "Factures": the folder is made there too.
				neuve := app.imapPerson(newMailboxOwner)
				neuve.rule(rule, 1, rules.FieldSubject, rules.OpContains, "facture", toInvoices)
				neuve.setAutoApplyRules(true)
				neuve.sync()
				requireNoFolder(neuve, folder)
				arrivedThere := neuve.arrive(phoneInvoice, travelOffer)
				neuve.sync()
				filedByRule(neuve, folder, rule, "à la synchronisation qui l'a reçu, sur une boîte sans dossier « Factures »", phoneInvoice)
				neuve.expectOnServer(serverInbox, travelOffer.Subject)
				neuve.expectShows(arrivedThere[1], travelOffer.Subject)
			},
		},
		{
			phrase: "Sur une boîte IMAP, une règle qui étiquette ne range jamais dans un dossier le mail d'un expéditeur protégé : il reste dans ta boîte de réception.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				const folder = "Factures"
				const rule = "Étiqueter les factures"

				// An invoice from a protected sender and one from a sender
				// nobody protects: the control, which the same rule files,
				// so it is the protection that keeps the first one in the
				// inbox.
				moi, uids := syncedIMAPInbox(app, invoice, shopInvoice)
				moi.protect(invoice.From)
				moi.rule(rule, 1, rules.FieldSubject, rules.OpContains, "facture", models.RuleAction{Type: rules.ActionLabel, LabelName: folder})

				// stayed checks that the protected sender's mail is still in
				// the inbox, on the server and on the screen, and not in the
				// folder. when says at which moment.
				stayed := func(uid string, m incoming, when string) {
					app.t.Helper()
					if moi.hasFolder(folder) {
						moi.expectNotOnServer(folder, m.Subject, fmt.Sprintf("son expéditeur est protégé, la règle « %s » ne doit pas le ranger dans un dossier, %s", rule, when))
					}
					moi.expectOnServer(serverInbox, m.Subject)
					moi.expectShows(uid, m.Subject)
				}

				// "Appliquer mes règles".
				if got := moi.applyRules(); got.code != http.StatusOK {
					app.failf("« Appliquer mes règles » sur la boîte IMAP de %s devrait répondre 200 et appliquer la règle « %s », le serveur a répondu %s", moi.email, rule, got)
				}
				stayed(uids[0], invoice, "après « Appliquer mes règles »")
				filedByRule(moi, folder, rule, "après « Appliquer mes règles », son expéditeur n'étant pas protégé", shopInvoice)

				// At the sync that receives two mails arriving once the
				// option is on (syncedIMAPInbox ran the first sync, which
				// judges no arrival): one from the protected sender, one
				// from a sender nobody protects.
				moi.setAutoApplyRules(true)
				arrived := moi.arrive(gasInvoice, phoneInvoice)
				moi.sync()
				stayed(arrived[0], gasInvoice, "à la synchronisation qui l'a reçu")
				filedByRule(moi, folder, rule, "à la synchronisation qui l'a reçu, son expéditeur n'étant pas protégé", phoneInvoice)
			},
		},
	})
}
