//go:build acceptance

package api

// What becomes of the AI's proposals on a mailbox reached over IMAP.
//
// An IMAP mailbox has folders and flags, and no labels. What stands for a
// label there is a folder: a proposal to label a mail "Factures" files it in
// the folder "Factures" of the mailbox, made if it is missing, and the mail
// leaves the inbox the way an archive takes it out, and can be undone the
// same way, since Mailsorter keeps track of where it put it. These sentences
// say where the mail goes, what the history and "Annuler" make of it, and
// which folders Mailsorter offers to file into. Each mailbox is a real IMAP
// server over TLS (acceptance_imap_test.go); the model's opinion is faked
// (acceptance_fakeai_test.go), everything Mailsorter does with it is real.
//
// A proposal can also outlive its point: the person files the mail by hand,
// from its row, before answering the plan. Over IMAP that mail stays stored,
// hidden, so "Annuler" can bring it back; two sentences say its proposal does
// not stay with it, and that "Tout ranger" from a screen that still shows it
// moves, counts and records nothing more for that mail. The same goes for a
// mail another mail program took out of the inbox, even an old one a sync no
// longer reads, and for "Analyser" on a mail already archived, from a screen
// that still shows it.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
)

// imapLabelOwner is the person whose IMAP mailbox these scenarios run on.
const imapLabelOwner = "boite-imap@exemple.fr"

// invoice is the mail the AI proposes to file, and invoiceFolder the folder
// it names: one of the labels the prompt itself suggests
// (internal/ai/prompts.go). shopInvoice (scenarios_6_imap_mirror_test.go) is a
// second one, from another sender.
var invoice = incoming{From: "Énergie <factures@energie.fr>", Subject: "Votre facture d'électricité"}

const invoiceFolder = "Factures"

// filingProposalOnIMAP brings a person on an IMAP mailbox to the moment the
// sentences start from: the statement of their bank and the invoice in their
// inbox, synced, and the AI's plan proposing to file the invoice in
// "Factures". The statement is not analyzed: it is the control. It returns the
// person, the two mails' numbers (statement, invoice) and that proposal.
func filingProposalOnIMAP(app *testApp) (*imapPerson, []string, models.AISuggestion) {
	app.t.Helper()
	moi := app.imapPerson(imapLabelOwner)
	uids := moi.receive(bankStatement, invoice)
	moi.sync()
	app.ai.willLabel(invoiceFolder, 0.95)
	moi.analyze(uids[1])
	s := moi.proposalFor(uids[1])
	if s.Action != "label" || s.LabelName != invoiceFolder {
		app.fatalf("le plan de l'IA devrait proposer de classer dans %q le mail n°%s %q, il propose %q (dossier %q) : le scénario ne prouve rien", invoiceFolder, uids[1], invoice.Subject, s.Action, s.LabelName)
	}
	return moi, uids, s
}

// specialFolders are the folders of a mailbox that are not the person's own,
// by the names the test server gives them, and what the sentence calls them.
var specialFolders = []struct{ name, called string }{
	{serverInbox, "la boîte de réception"},
	{serverSent, "les envoyés"},
	{serverDrafts, "les brouillons"},
	{serverTrash, "la corbeille"},
	{serverJunk, "les indésirables"},
	{serverArchive, "l'archive"},
}

func TestScenarios_IMAPAI(t *testing.T) {
	playScenarios(t, "L'IA sur une boîte IMAP", []scenario{
		{
			phrase: "Sur une boîte IMAP, appliquer une proposition de l'IA de classer un mail dans « Factures » le range dans le dossier « Factures » de ta boîte, créé s'il n'existait pas, et il quitte ta boîte de réception.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids, s := filingProposalOnIMAP(app)
				// Where the sentence starts: no folder "Factures" yet, so
				// filing the mail has to make it.
				if moi.hasFolder(invoiceFolder) {
					app.fatalf("le serveur IMAP de %s ne devrait pas encore avoir de dossier %q : le scénario ne prouve rien (dossiers : %s)", moi.email, invoiceFolder, strings.Join(moi.folders(), ", "))
				}

				// "Appliquer" on the proposal (handleApplySuggestion in
				// Inbox.js).
				got := moi.applyProposal(s.ID)
				if got.code != http.StatusOK {
					app.failf("sur une boîte IMAP, appliquer la proposition de classer dans %q le mail n°%s %q devrait répondre 200, le serveur a répondu %s", invoiceFolder, uids[1], invoice.Subject, got)
				}

				// The server, as another mail program sees it: the folder,
				// made by Mailsorter, holds the mail, and the inbox no longer
				// does. Not in the archive either: filed is not archived.
				if !moi.hasFolder(invoiceFolder) {
					app.failf("après l'application de la proposition, le serveur IMAP de %s devrait avoir un dossier %q où le mail n°%s %q est rangé, il n'en a pas (dossiers : %s)", moi.email, invoiceFolder, uids[1], invoice.Subject, strings.Join(moi.folders(), ", "))
				} else {
					moi.expectOnServer(invoiceFolder, invoice.Subject)
				}
				moi.expectNotOnServer(serverInbox, invoice.Subject, "il est classé dans "+invoiceFolder)
				moi.expectNotOnServer(serverArchive, invoice.Subject, "il est classé dans "+invoiceFolder+", pas archivé")

				// What the inbox screen shows right after.
				moi.expectDoesNotShow(invoice.Subject, "il est classé dans "+invoiceFolder)
				// The control: the mail the AI never saw stays where it was.
				moi.expectShows(uids[0], bankStatement.Subject)
				moi.expectOnServer(serverInbox, bankStatement.Subject)
			},
		},
		{
			phrase: "Dans ce cas, l'historique inscrit le classement avec le nom du dossier, et propose de l'annuler.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids, s := filingProposalOnIMAP(app)

				// Whatever it answers: the answer is the sentence above.
				moi.applyProposal(s.ID)

				// The Historique screen: one line for the one filing, under
				// the AI's verb and from the AI, as every applied proposal
				// is inscribed. "label" is also the verb the plan's
				// "Annuler" sends back (undo in useTriagePlan), which is how
				// that line gets marked undone (recordUndo).
				lines := moi.historyLinesOf(uids[1])
				if len(lines) != 1 || lines[0].Action != "label" || lines[0].Source != SourceAI {
					app.failf("l'historique de %s devrait inscrire une ligne pour le mail n°%s %q, son classement dans %q (\"label\", %s) : il en montre %d (%s)", moi.email, uids[1], invoice.Subject, invoiceFolder, SourceAI, len(lines), describeHistory(lines))
					return
				}
				line := lines[0]

				// The folder's name, in the line itself: the screen shows
				// what the line holds. Not from the mail's own subject or
				// sender, which say nothing of where it went.
				if !holdsString(moi.historyLineSent(line.ID), invoiceFolder, map[string]bool{"subject": true, "from": true}) {
					app.failf("dans l'historique de %s, la ligne du classement du mail n°%s %q devrait nommer le dossier %q, elle ne le nomme pas (la ligne : %v)", moi.email, uids[1], invoice.Subject, invoiceFolder, moi.historyLineSent(line.ID))
				}
				if line.Undone || !line.Undoable {
					app.failf("dans l'historique de %s, la ligne du classement du mail n°%s %q dans %q devrait proposer « Annuler », elle est marquée annulée=%v, annulable=%v", moi.email, uids[1], invoice.Subject, invoiceFolder, line.Undone, line.Undoable)
				}
			},
		},
		{
			phrase: "Sur une boîte IMAP, « Annuler » juste après avoir classé un mail le remet dans ta boîte de réception, et il n'est plus dans le dossier.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids, s := filingProposalOnIMAP(app)

				// "Classer" on the plan's row for the invoice (applyGroup in
				// TriagePlan.js: the request of "Tout ranger", with this one
				// proposal), which is what offers "Annuler" right after.
				got := moi.tidyAll(s.ID)
				if got.code != http.StatusOK {
					app.fatalf("« Classer » sur la proposition de classer dans %q le mail n°%s %q devrait répondre 200, le serveur a répondu %s : le scénario ne prouve rien", invoiceFolder, uids[1], invoice.Subject, got)
				}
				if !moi.hasFolder(invoiceFolder) {
					app.fatalf("« Classer » devrait avoir rangé le mail n°%s %q dans le dossier %q du serveur IMAP de %s, qui n'a pas de tel dossier (dossiers : %s) : le scénario ne prouve rien", uids[1], invoice.Subject, invoiceFolder, moi.email, strings.Join(moi.folders(), ", "))
				}
				moi.requireOnServer(invoiceFolder, invoice.Subject, "tu viens de le classer")

				// The plan offers "Annuler" only when the answer says the
				// filing can be undone (TriagePlan.js).
				if got.body["reversible"] != true {
					app.failf("juste après le classement, l'écran devrait proposer « Annuler » : la réponse dit reversible=%v, le bouton n'est donc pas proposé", got.body["reversible"])
				}
				// "Annuler" in the message that follows: one
				// POST /api/emails/batch-undo for the verdict, with the folder's
				// name and the number the mail had in the inbox.
				for _, u := range moi.undoTidy([]models.AISuggestion{s}) {
					if u.got.code != http.StatusOK {
						app.failf("« Annuler » juste après le classement du mail n°%s %q dans %q devrait répondre 200, le serveur a répondu %s", uids[1], invoice.Subject, invoiceFolder, u.got)
					}
				}

				// What the screen shows right after: the plan reloads the
				// listing once the undo answers, without a sync. Then the
				// server: back in the inbox, and out of the folder.
				moi.expectBackInInbox(invoice.Subject, "juste après « Annuler »")
				moi.expectOnServer(serverInbox, invoice.Subject)
				if moi.hasFolder(invoiceFolder) {
					moi.expectNotOnServer(invoiceFolder, invoice.Subject, "« Annuler » l'a remis dans ta boîte de réception")
				}
				// The control: the mail nobody touched is still there, under
				// its number.
				moi.expectShows(uids[0], bankStatement.Subject)
			},
		},
		{
			phrase: "Sur une boîte IMAP, « Tout ranger » classe aussi les mails que l'IA propose de classer, et les compte comme rangés.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi := app.imapPerson(imapLabelOwner)
				// The folder exists already: the person made it on their
				// phone. Filing into it must not trip on that.
				moi.newFolderElsewhere(invoiceFolder)
				mails := []incoming{bankStatement, invoice, shopInvoice, travelOffer}
				uids := moi.receive(mails...)
				moi.sync()

				// Two proposals to file in "Factures", and one to archive:
				// the plan as "Tout ranger" usually finds it, with more than
				// one verb. The bank statement is not analyzed: it is the
				// control.
				app.ai.willLabel(invoiceFolder, 0.95)
				moi.analyze(uids[1], uids[2])
				app.ai.willSay("archive", 0.95)
				moi.analyze(uids[3])

				filings := make([]models.AISuggestion, 0, 2)
				for i := 1; i <= 2; i++ {
					s := moi.proposalFor(uids[i])
					if s.Action != "label" || s.LabelName != invoiceFolder {
						app.fatalf("le plan de l'IA devrait proposer de classer dans %q le mail n°%s %q, il propose %q (dossier %q) : le scénario ne prouve rien", invoiceFolder, uids[i], mails[i].Subject, s.Action, s.LabelName)
					}
					filings = append(filings, s)
				}
				archive := moi.proposalFor(uids[3])
				if archive.Action != "archive" {
					app.fatalf("le plan de l'IA devrait proposer d'archiver le mail n°%s %q, il propose %q : le scénario ne prouve rien", uids[3], mails[3].Subject, archive.Action)
				}

				got := moi.tidyAll(filings[0].ID, filings[1].ID, archive.ID)
				if got.code != http.StatusOK {
					app.fatalf("« Tout ranger » devrait répondre 200 avec le compte des mails rangés, le serveur a répondu %s", got)
				}

				// What the plan announces: the three mails tidied, none failed.
				counted := map[string]bool{}
				list, _ := got.body["appliedIds"].([]interface{})
				for _, v := range list {
					if id, ok := v.(string); ok {
						counted[id] = true
					}
				}
				for i, s := range filings {
					if !counted[s.ID] {
						app.failf("« Tout ranger » devrait compter parmi les mails rangés le mail n°%s %q, que l'IA proposait de classer dans %q, il ne le compte pas", uids[i+1], mails[i+1].Subject, invoiceFolder)
					}
				}
				if !counted[archive.ID] {
					app.failf("« Tout ranger » devrait compter parmi les mails rangés le mail n°%s %q, que l'IA proposait d'archiver, il ne le compte pas", uids[3], mails[3].Subject)
				}
				if n := app.number(got.body, "applied"); n != 3 {
					app.failf("« Tout ranger » annonce %d mails rangés, alors que les 3 mails proposés l'ont été : 2 classés dans %q, 1 archivé", n, invoiceFolder)
				}
				if n := app.number(got.body, "failed"); n != 0 {
					app.failf("« Tout ranger » compte %d mail(s) en échec, alors que les 3 mails proposés pouvaient tous être rangés", n)
				}

				// The server, as another mail program sees it.
				for i := 1; i <= 2; i++ {
					moi.expectOnServer(invoiceFolder, mails[i].Subject)
					moi.expectNotOnServer(serverInbox, mails[i].Subject, "« Tout ranger » l'a classé dans "+invoiceFolder)
				}
				moi.expectOnServer(serverArchive, travelOffer.Subject)
				moi.expectNotOnServer(serverInbox, travelOffer.Subject, "« Tout ranger » l'a archivé")

				// The inbox screen: the three are gone, the control stays.
				for i := 1; i <= 3; i++ {
					moi.expectDoesNotShow(mails[i].Subject, "« Tout ranger » l'a rangé")
				}
				moi.expectShows(uids[0], bankStatement.Subject)
				moi.expectOnServer(serverInbox, bankStatement.Subject)
			},
		},
		{
			phrase: "Sur une boîte IMAP, la liste des dossiers où classer propose tes dossiers à toi, et pas la boîte de réception, les envoyés, les brouillons, la corbeille, les indésirables ni l'archive.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi := app.imapPerson(imapLabelOwner)
				moi.usualSpecialFolders()
				// Two folders of the person's own, made on their phone, one
				// inside the other.
				own := []string{"Projets", "Projets/Maison"}
				for _, name := range own {
					moi.newFolderElsewhere(name)
				}
				// Where the sentence starts: the server has them all, the
				// special ones and the person's own.
				for _, f := range specialFolders {
					if !moi.hasFolder(f.name) {
						app.fatalf("le serveur IMAP de %s devrait avoir le dossier %q (%s) : le scénario ne prouve rien (dossiers : %s)", moi.email, f.name, f.called, strings.Join(moi.folders(), ", "))
					}
				}
				for _, name := range own {
					if !moi.hasFolder(name) {
						app.fatalf("le serveur IMAP de %s devrait avoir le dossier %q : le scénario ne prouve rien (dossiers : %s)", moi.email, name, strings.Join(moi.folders(), ", "))
					}
				}

				list := moi.foldersToFileInto()

				// The person's own folders, each offered by the screens. The
				// one inside another goes by its full name, as a label inside
				// another does on Gmail: that is the folder to file into.
				for _, name := range own {
					found := false
					for _, f := range list {
						if f.Name != name {
							continue
						}
						found = true
						if !f.offeredByScreens() {
							app.failf("la liste des dossiers où classer de %s contient ton dossier %q, mais avec le type %q : les écrans ne proposent que les entrées de type \"user\" (LabelPicker dans Inbox.js), il n'est donc pas proposé", moi.email, name, f.Type)
						}
					}
					if !found {
						app.failf("la liste des dossiers où classer de %s devrait proposer ton dossier %q, elle ne le propose pas (elle contient : %s)", moi.email, name, describeFolders(list))
					}
				}

				// None of the special folders, whatever its type.
				for _, s := range specialFolders {
					for _, f := range list {
						if strings.EqualFold(f.Name, s.name) {
							app.failf("la liste des dossiers où classer de %s ne devrait pas contenir %q (%s), elle le contient (type %q)", moi.email, f.Name, s.called, f.Type)
						}
					}
				}

				// And nothing the person does not have.
				for _, f := range list {
					if !f.offeredByScreens() {
						continue
					}
					mine := false
					for _, name := range own {
						if f.Name == name {
							mine = true
						}
					}
					special := false
					for _, s := range specialFolders {
						if strings.EqualFold(f.Name, s.name) {
							special = true
						}
					}
					if !mine && !special {
						app.failf("la liste des dossiers où classer de %s propose %q, qui n'est aucun des dossiers de sa boîte (dossiers : %s)", moi.email, f.Name, strings.Join(moi.folders(), ", "))
					}
				}
			},
		},
		{
			phrase: "Sur une boîte IMAP, quand tu as déjà rangé à la main un mail que l'IA proposait de ranger, sa proposition quitte le tri proposé à la synchronisation suivante, et « Tout ranger » ne la compte pas.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, shopOrder, travelOffer)

				// The AI proposes to archive both mails. The second one nobody
				// touches: it is the control.
				app.ai.willSay("archive", 0.95)
				moi.analyze(uids...)
				byHand := moi.proposalFor(uids[0])
				control := moi.proposalFor(uids[1])
				if byHand.Action != "archive" || control.Action != "archive" {
					app.fatalf("le plan de l'IA devrait proposer d'archiver le mail n°%s %q et le mail n°%s %q, il propose %q et %q : le scénario ne prouve rien", uids[0], shopOrder.Subject, uids[1], travelOffer.Subject, byHand.Action, control.Action)
				}

				// "Archiver" on the row of the first mail, then "Synchroniser".
				moi.act(uids[0], "archive")
				moi.requireOnServer(serverArchive, shopOrder.Subject, "tu viens de l'archiver à la main")
				moi.sync()

				if moi.inPlan(byHand.ID) {
					app.failf("après la synchronisation, la proposition d'archiver le mail n°%s %q, que tu as déjà archivé à la main, devrait avoir quitté le tri proposé de l'IA : elle y est encore (statut enregistré : %q)", uids[0], shopOrder.Subject, moi.proposalStatus(byHand.ID))
				}
				if !moi.inPlan(control.ID) {
					app.failf("après la synchronisation, la proposition d'archiver le mail n°%s %q, que personne n'a touché, devrait rester dans le tri proposé de l'IA : elle n'y est plus (statut enregistré : %q)", uids[1], travelOffer.Subject, moi.proposalStatus(control.ID))
				}

				// "Tout ranger" on the plan as a screen loaded before the sync
				// still shows it (a tab left open): both ids, as TriagePlan.js
				// sends them. The control is there so that a "Tout ranger"
				// that counts nothing at all cannot pass for one that leaves
				// out the mail already archived. Not counting it means in
				// neither count of the answer: the plan also reads "failed",
				// and would say "1 email n'a pas pu être traité." about a
				// mail already tidied (TriagePlan.js).
				got := moi.tidyAll(byHand.ID, control.ID)
				if got.code != http.StatusOK {
					app.fatalf("« Tout ranger » devrait répondre 200 avec le compte des mails rangés, le serveur a répondu %s : le scénario ne prouve rien", got)
				}
				counted := map[string]bool{}
				list, _ := got.body["appliedIds"].([]interface{})
				for _, v := range list {
					if id, ok := v.(string); ok {
						counted[id] = true
					}
				}
				if !counted[control.ID] {
					app.fatalf("« Tout ranger » devrait compter parmi les mails rangés le mail n°%s %q, que personne n'a touché, il ne le compte pas (%v) : le scénario ne prouve rien", uids[1], travelOffer.Subject, got.body)
				}
				if counted[byHand.ID] {
					app.failf("« Tout ranger » compte parmi les mails rangés le mail n°%s %q, alors que tu l'avais déjà archivé à la main et qu'il n'a rien rangé de plus", uids[0], shopOrder.Subject)
				}
				if n := app.number(got.body, "applied"); n != 1 {
					app.failf("« Tout ranger » annonce %d mails rangés, alors que seul le mail n°%s %q pouvait être rangé : le mail n°%s %q était déjà archivé à la main", n, uids[1], travelOffer.Subject, uids[0], shopOrder.Subject)
				}
				if n := app.number(got.body, "failed"); n != 0 {
					app.failf("« Tout ranger » compte %d mail(s) en échec, alors que le mail n°%s %q, que tu avais déjà archivé à la main, n'avait plus rien à ranger et que le mail n°%s %q a été rangé : l'écran annonce qu'un mail n'a pas pu être traité à propos d'un mail déjà rangé", n, uids[0], shopOrder.Subject, uids[1], travelOffer.Subject)
				}

				// The Historique screen: the archive by hand, and nothing from
				// a "Tout ranger" that moved nothing.
				lines := moi.historyLinesOf(uids[0])
				if len(lines) != 1 || lines[0].Action != "archive" || lines[0].Source != SourceDirect {
					app.failf("l'historique de %s devrait montrer une seule ligne pour le mail n°%s %q, ton archivage à la main (\"archive\", %s) : il en montre %d (%s)", moi.email, uids[0], shopOrder.Subject, SourceDirect, len(lines), describeHistory(lines))
				}
			},
		},
		{
			phrase: "Sur une boîte IMAP, « Tout ranger » sur un écran resté ouvert après que tu as rangé un mail à la main ne range rien de plus pour ce mail : il n'est compté ni rangé ni en échec, et l'historique n'a que ton rangement.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, shopOrder, travelOffer)

				// The AI proposes to archive both mails. The second one nobody
				// touches: it is the control.
				app.ai.willSay("archive", 0.95)
				moi.analyze(uids...)
				byHand := moi.proposalFor(uids[0])
				control := moi.proposalFor(uids[1])
				if byHand.Action != "archive" || control.Action != "archive" {
					app.fatalf("le plan de l'IA devrait proposer d'archiver le mail n°%s %q et le mail n°%s %q, il propose %q et %q : le scénario ne prouve rien", uids[0], shopOrder.Subject, uids[1], travelOffer.Subject, byHand.Action, control.Action)
				}

				// "Archiver" on the row of the first mail, and no
				// "Synchroniser" after it: the plan still waits for an answer
				// on its proposal, the way another tab left open on the inbox
				// still shows it.
				moi.act(uids[0], "archive")
				moi.requireOnServer(serverArchive, shopOrder.Subject, "tu viens de l'archiver à la main")

				// "Tout ranger" on that screen: both ids, as TriagePlan.js
				// sends them. The control is there so that a "Tout ranger"
				// that tidies nothing at all cannot pass for one that leaves
				// out the mail already archived.
				got := moi.tidyAll(byHand.ID, control.ID)
				if got.code != http.StatusOK {
					app.fatalf("« Tout ranger » devrait répondre 200 avec le compte des mails rangés, le serveur a répondu %s : le scénario ne prouve rien", got)
				}
				counted := map[string]bool{}
				list, _ := got.body["appliedIds"].([]interface{})
				for _, v := range list {
					if id, ok := v.(string); ok {
						counted[id] = true
					}
				}
				if !counted[control.ID] {
					app.fatalf("« Tout ranger » devrait compter parmi les mails rangés le mail n°%s %q, que personne n'a touché, il ne le compte pas (%v) : le scénario ne prouve rien", uids[1], travelOffer.Subject, got.body)
				}
				moi.requireOnServer(serverArchive, travelOffer.Subject, "« Tout ranger » l'a compté parmi les mails rangés")

				// What the plan announces: the control, and nothing for the
				// mail already archived, neither among the mails tidied nor
				// among those that failed ("n'a pas pu être traité").
				if counted[byHand.ID] {
					app.failf("« Tout ranger » compte parmi les mails rangés le mail n°%s %q, alors que tu l'avais déjà archivé à la main et qu'il n'a rien rangé de plus", uids[0], shopOrder.Subject)
				}
				if n := app.number(got.body, "applied"); n != 1 {
					app.failf("« Tout ranger » annonce %d mails rangés, alors que seul le mail n°%s %q pouvait être rangé : le mail n°%s %q était déjà archivé à la main", n, uids[1], travelOffer.Subject, uids[0], shopOrder.Subject)
				}
				if n := app.number(got.body, "failed"); n != 0 {
					app.failf("« Tout ranger » compte %d mail(s) en échec, alors que le mail n°%s %q, que tu avais déjà archivé à la main, n'avait plus rien à ranger et que le mail n°%s %q a été rangé : l'écran annonce qu'un mail n'a pas pu être traité à propos d'un mail déjà rangé", n, uids[0], shopOrder.Subject, uids[1], travelOffer.Subject)
				}

				// The server, as another mail program sees it: the mail stays
				// where you put it, once, and nowhere else.
				copies := 0
				for _, s := range moi.onServer(serverArchive) {
					if s == shopOrder.Subject {
						copies++
					}
				}
				if copies != 1 {
					app.failf("après « Tout ranger », le dossier %s du serveur IMAP de %s devrait contenir le mail %q une fois, celle où tu l'as archivé à la main : il le contient %d fois", serverArchive, moi.email, shopOrder.Subject, copies)
				}
				moi.expectNotOnServer(serverInbox, shopOrder.Subject, "tu l'as archivé à la main avant « Tout ranger »")
				moi.expectNotOnServer(serverTrash, shopOrder.Subject, "tu l'as archivé à la main avant « Tout ranger »")

				// The Historique screen: the archive by hand, and nothing from
				// a "Tout ranger" that moved nothing for that mail.
				lines := moi.historyLinesOf(uids[0])
				if len(lines) != 1 || lines[0].Action != "archive" || lines[0].Source != SourceDirect {
					app.failf("l'historique de %s devrait montrer une seule ligne pour le mail n°%s %q, ton archivage à la main (\"archive\", %s) : il en montre %d (%s)", moi.email, uids[0], shopOrder.Subject, SourceDirect, len(lines), describeHistory(lines))
				}
			},
		},
		{
			phrase: "Sur une boîte IMAP de plus de 100 mails, quand un autre logiciel a sorti de ta boîte de réception un ancien mail que l'IA proposait de ranger, sa proposition quitte le tri proposé à la synchronisation suivante.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi := app.imapPerson(imapInboxOwner)
				// 100 letters, synced, then 10 more, synced: more than one
				// sync reads (the 100 most recent), and the 10 oldest letters
				// are no longer among them.
				letters := numberedMails("Lettre n°%d", 100)
				uids := moi.receive(letters...)
				moi.sync()
				moi.receive(numberedMails("Nouvelle lettre n°%d", 10)...)
				moi.sync()

				// The AI proposes to archive three of those old letters. The
				// middle one nobody touches: it is the control, as old as the
				// other two, so a sync that drops every proposal about a mail
				// it does not read cannot pass.
				app.ai.willSay("archive", 0.95)
				moi.analyze(uids[2], uids[3], uids[4])
				archived := moi.proposalFor(uids[2])
				control := moi.proposalFor(uids[3])
				deleted := moi.proposalFor(uids[4])
				for _, s := range []models.AISuggestion{archived, control, deleted} {
					if s.Action != "archive" {
						app.fatalf("le plan de l'IA devrait proposer d'archiver le mail n°%s, il propose %q : le scénario ne prouve rien", s.EmailID, s.Action)
					}
				}

				// The phone takes two of them out of the inbox, the two ways:
				// archived (moved to another folder) and deleted for good.
				moi.movedElsewhere(uids[2], serverArchive)
				moi.deletedElsewhere(uids[4])

				moi.sync()

				for _, c := range []struct {
					s       models.AISuggestion
					subject string
					how     string
				}{
					{archived, letters[2].Subject, "archivé"},
					{deleted, letters[4].Subject, "supprimé définitivement"},
				} {
					if moi.inPlan(c.s.ID) {
						app.failf("après la synchronisation, la proposition d'archiver le mail n°%s %q, qu'un autre logiciel a %s, devrait avoir quitté le tri proposé de l'IA : elle y est encore (statut enregistré : %q)", c.s.EmailID, c.subject, c.how, moi.proposalStatus(c.s.ID))
					}
				}
				if !moi.inPlan(control.ID) {
					app.failf("après la synchronisation, la proposition d'archiver le mail n°%s %q, que personne n'a touché, devrait rester dans le tri proposé de l'IA : elle n'y est plus (statut enregistré : %q)", uids[3], letters[3].Subject, moi.proposalStatus(control.ID))
				}
			},
		},
		{
			phrase: "Sur une boîte IMAP, « Tout ranger » sur un écran resté ouvert, après qu'un autre logiciel a sorti de ta boîte de réception un des mails proposés, ne compte pas ce mail parmi les rangés ni en échec, et n'ajoute rien à l'historique pour lui.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, shopOrder, travelOffer)

				// The AI proposes to archive both mails. The second one nobody
				// touches: it is the control.
				app.ai.willSay("archive", 0.95)
				moi.analyze(uids...)
				byPhone := moi.proposalFor(uids[0])
				control := moi.proposalFor(uids[1])
				if byPhone.Action != "archive" || control.Action != "archive" {
					app.fatalf("le plan de l'IA devrait proposer d'archiver le mail n°%s %q et le mail n°%s %q, il propose %q et %q : le scénario ne prouve rien", uids[0], shopOrder.Subject, uids[1], travelOffer.Subject, byPhone.Action, control.Action)
				}

				// Another mail program archives the first mail, and no
				// "Synchroniser" after it: the plan on the screen still shows
				// its proposal.
				moi.movedElsewhere(uids[0], serverArchive)

				// "Tout ranger" on that screen: both ids, as TriagePlan.js
				// sends them. The control is there so that a "Tout ranger"
				// that tidies nothing at all cannot pass for one that leaves
				// out the mail already gone.
				got := moi.tidyAll(byPhone.ID, control.ID)
				if got.code != http.StatusOK {
					app.fatalf("« Tout ranger » devrait répondre 200 avec le compte des mails rangés, le serveur a répondu %s : le scénario ne prouve rien", got)
				}
				counted := map[string]bool{}
				list, _ := got.body["appliedIds"].([]interface{})
				for _, v := range list {
					if id, ok := v.(string); ok {
						counted[id] = true
					}
				}
				if !counted[control.ID] {
					app.fatalf("« Tout ranger » devrait compter parmi les mails rangés le mail n°%s %q, que personne n'a touché, il ne le compte pas (%v) : le scénario ne prouve rien", uids[1], travelOffer.Subject, got.body)
				}
				moi.requireOnServer(serverArchive, travelOffer.Subject, "« Tout ranger » l'a compté parmi les mails rangés")

				// What the plan announces: the control, and nothing for the
				// mail the other program took away, neither among the mails
				// tidied nor among those that failed ("n'a pas pu être
				// traité", TriagePlan.js).
				if counted[byPhone.ID] {
					app.failf("« Tout ranger » compte parmi les mails rangés le mail n°%s %q, alors qu'un autre logiciel l'avait déjà sorti de ta boîte de réception et que « Tout ranger » n'a rien rangé pour lui", uids[0], shopOrder.Subject)
				}
				if n := app.number(got.body, "applied"); n != 1 {
					app.failf("« Tout ranger » annonce %d mails rangés, alors que seul le mail n°%s %q pouvait être rangé : un autre logiciel avait déjà sorti le mail n°%s %q de ta boîte de réception", n, uids[1], travelOffer.Subject, uids[0], shopOrder.Subject)
				}
				if n := app.number(got.body, "failed"); n != 0 {
					app.failf("« Tout ranger » compte %d mail(s) en échec, alors que le mail n°%s %q, qu'un autre logiciel avait déjà sorti de ta boîte de réception, n'avait plus rien à ranger et que le mail n°%s %q a été rangé : l'écran annonce qu'un mail n'a pas pu être traité à propos d'un mail déjà rangé", n, uids[0], shopOrder.Subject, uids[1], travelOffer.Subject)
				}

				// The Historique screen: nothing for that mail, since
				// Mailsorter moved nothing.
				if lines := moi.historyLinesOf(uids[0]); len(lines) > 0 {
					app.failf("l'historique de %s ne devrait rien montrer pour le mail n°%s %q, qu'un autre logiciel avait sorti de ta boîte de réception avant « Tout ranger » : il montre %d ligne(s) (%s)", moi.email, uids[0], shopOrder.Subject, len(lines), describeHistory(lines))
				}
			},
		},
		{
			phrase: "Sur une boîte IMAP, « Analyser » depuis un écran resté ouvert un mail que tu viens d'archiver ne crée pas de proposition dans le tri proposé.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uids := syncedIMAPInbox(app, shopOrder, travelOffer)

				// "Archiver" on the row of the first mail.
				moi.act(uids[0], "archive")
				moi.requireOnServer(serverArchive, shopOrder.Subject, "tu viens de l'archiver")

				// "Analyser" on that mail, from a screen loaded before the
				// archive (the reader's analysis, handleAiAnalyzeSingle in
				// Inbox.js): its number, whatever the answer. The sentence
				// says what the plan shows, not what the server answers.
				app.ai.willSay("archive", 0.95)
				moi.analyzeAnswer(uids[0])

				// The plan as the inbox loads it next.
				for _, s := range moi.plan() {
					if s.EmailID == uids[0] {
						app.failf("le tri proposé de %s ne devrait rien proposer pour le mail n°%s %q, que tu venais d'archiver avant « Analyser » : il propose %q (confiance %.2f)", moi.email, uids[0], shopOrder.Subject, s.Action, s.Confidence)
					}
				}

				// The control: the same analysis on the mail still in the
				// inbox makes its proposal. Without it, an analysis that
				// proposes nothing at all would pass.
				moi.analyze(uids[1])
				moi.proposalFor(uids[1])
			},
		},
	})
}
