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
	})
}
