//go:build acceptance

package api

// What becomes of the AI's proposals on a mailbox reached over IMAP.
//
// An IMAP mailbox has folders and flags, and nothing a label could become
// (internal/mailbox). So a proposal to label a mail cannot be carried out
// there, and these sentences say what the person is told instead, and what
// the plan and the history show afterwards. Each mailbox is a real IMAP
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
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
)

// noLabelsOnIMAP is what the server answers when asked to label a mail on a
// mailbox reached over IMAP.
const noLabelsOnIMAP = "Les étiquettes ne sont pas encore disponibles sur les boîtes IMAP."

// imapLabelOwner is the person whose IMAP mailbox these scenarios run on.
const imapLabelOwner = "boite-imap@exemple.fr"

// parcel is the mail the AI proposes to label, and parcelLabel the label it
// names: one of those the prompt itself suggests.
var parcel = incoming{From: "Transporteur <suivi@colis.fr>", Subject: "Votre colis est en route"}

const parcelLabel = "Livraison"

// labelProposalOnIMAP brings a person on an IMAP mailbox to the moment the
// sentences start from: the parcel mail is in their inbox, synced, and the
// AI's plan proposes to label it. It returns the person, the mail's number
// and that proposal.
func labelProposalOnIMAP(app *testApp) (*imapPerson, string, models.AISuggestion) {
	app.t.Helper()
	moi := app.imapPerson(imapLabelOwner)
	uids := moi.receive(parcel)
	moi.sync()
	app.ai.willLabel(parcelLabel, 0.95)
	moi.analyze(uids...)
	s := moi.proposalFor(uids[0])
	if s.Action != "label" || s.LabelName != parcelLabel {
		app.fatalf("le plan de l'IA devrait proposer d'étiqueter %q le mail n°%s %q, il propose %q (étiquette %q) : le scénario ne prouve rien", parcelLabel, uids[0], parcel.Subject, s.Action, s.LabelName)
	}
	return moi, uids[0], s
}

func TestScenarios_IMAPAI(t *testing.T) {
	playScenarios(t, "L'IA sur une boîte IMAP", []scenario{
		{
			phrase: "Sur une boîte IMAP, appliquer une proposition de l'IA d'étiqueter un mail répond \"Les étiquettes ne sont pas encore disponibles sur les boîtes IMAP.\" et la proposition reste en attente.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uid, s := labelProposalOnIMAP(app)

				got := moi.applyProposal(s.ID)
				if got.code != http.StatusNotImplemented || got.said() != noLabelsOnIMAP {
					app.failf("sur une boîte IMAP, appliquer la proposition d'étiqueter %q le mail n°%s %q devrait répondre %d %q, le serveur a répondu %s", parcelLabel, uid, parcel.Subject, http.StatusNotImplemented, noLabelsOnIMAP, got)
				}
				if !moi.inPlan(s.ID) {
					app.failf("la proposition d'étiqueter %q le mail n°%s %q devrait rester en attente dans le plan de l'IA, elle n'y est plus (statut enregistré : %q)", parcelLabel, uid, parcel.Subject, moi.proposalStatus(s.ID))
				}
			},
		},
		{
			phrase: "Dans ce cas, rien n'est inscrit dans l'historique.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uid, s := labelProposalOnIMAP(app)

				// Whatever it answers: the answer is the sentence above.
				moi.applyProposal(s.ID)

				if rows := moi.history(uid); len(rows) > 0 {
					last := rows[len(rows)-1]
					app.failf("rien ne devrait être inscrit dans l'historique de %s pour le mail n°%s %q, sur lequel aucune étiquette %q n'a pu être posée : il montre %d ligne(s), la dernière : %q (%s)", moi.email, uid, parcel.Subject, parcelLabel, len(rows), last.Action, last.Source)
				}
			},
		},
		{
			phrase: "Sur une boîte IMAP, \"Tout ranger\" n'applique pas les propositions d'étiquette : elles restent en attente et ne sont pas comptées comme rangées.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi := app.imapPerson(imapLabelOwner)
				mails := []incoming{
					parcel,
					{From: "Boutique <info@boutique.com>", Subject: "Votre commande est expédiée"},
					{From: "Voyages <promo@voyages.fr>", Subject: "Offre"},
				}
				uids := moi.receive(mails...)
				moi.sync()

				// Two proposals to label, and one to archive: the plan as
				// "Tout ranger" usually finds it, with more than one verb.
				app.ai.willLabel(parcelLabel, 0.95)
				moi.analyze(uids[0], uids[1])
				app.ai.willSay("archive", 0.95)
				moi.analyze(uids[2])

				labels := make([]models.AISuggestion, 0, 2)
				for i, uid := range uids[:2] {
					s := moi.proposalFor(uid)
					if s.Action != "label" {
						app.fatalf("le plan de l'IA devrait proposer d'étiqueter le mail n°%s %q, il propose %q : le scénario ne prouve rien", uid, mails[i].Subject, s.Action)
					}
					labels = append(labels, s)
				}
				archive := moi.proposalFor(uids[2])
				if archive.Action != "archive" {
					app.fatalf("le plan de l'IA devrait proposer d'archiver le mail n°%s %q, il propose %q : le scénario ne prouve rien", uids[2], mails[2].Subject, archive.Action)
				}

				got := moi.tidyAll(labels[0].ID, labels[1].ID, archive.ID)
				if got.code != http.StatusOK {
					app.failf("\"Tout ranger\" devrait répondre 200 avec le compte des mails rangés, le serveur a répondu %s", got)
				} else {
					counted := map[string]bool{}
					list, _ := got.body["appliedIds"].([]interface{})
					for _, v := range list {
						if id, ok := v.(string); ok {
							counted[id] = true
						}
					}
					for i, s := range labels {
						if counted[s.ID] {
							app.failf("\"Tout ranger\" compte parmi les mails rangés le mail n°%s %q, alors que sa proposition d'étiquette %q n'est pas appliquée sur une boîte IMAP", uids[i], mails[i].Subject, s.LabelName)
						}
					}
					if n := app.number(got.body, "applied"); n > 1 {
						app.failf("\"Tout ranger\" annonce %d mails rangés sur ces 3 propositions, alors que seule celle d'archiver le mail n°%s %q pouvait être comptée : les propositions d'étiquette le sont aussi", n, uids[2], mails[2].Subject)
					}
				}

				for i, s := range labels {
					if !moi.inPlan(s.ID) {
						app.failf("après \"Tout ranger\", la proposition d'étiqueter %q le mail n°%s %q devrait rester en attente dans le plan de l'IA, elle n'y est plus (statut enregistré : %q)", s.LabelName, uids[i], mails[i].Subject, moi.proposalStatus(s.ID))
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
