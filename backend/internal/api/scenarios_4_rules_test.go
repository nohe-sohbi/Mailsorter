//go:build acceptance

package api

// What your sorting rules do, and when.

import (
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/rules"
)

func TestScenarios_Rules(t *testing.T) {
	archive := models.RuleAction{Type: rules.ActionArchive}
	promoLabel := models.RuleAction{Type: rules.ActionLabel, LabelName: "Promo"}

	playScenarios(t, "Tes règles", []scenario{
		{
			phrase: "Tant que l'option \"appliquer mes règles automatiquement\" est coupée, synchroniser ta boîte ne déplace aucun mail.",
			status: observed,
			run: func(t *testing.T, app *testApp) {
				app.rule("Promos", 1, rules.FieldFrom, rules.OpContains, "boutique.com", archive)
				ids := app.receiveAndSync(incoming{From: "Boutique <info@boutique.com>", Subject: "Soldes"})
				app.expectInInbox(ids[0])
				app.expectNoHistory(ids[0])
			},
		},
		{
			phrase: "Avec cette option activée, chaque synchronisation applique tes règles aux mails de ta boîte, et c'est noté dans l'historique.",
			status: observed,
			run: func(t *testing.T, app *testApp) {
				app.rule("Promos", 1, rules.FieldFrom, rules.OpContains, "boutique.com", archive)
				app.setAutoApplyRules(true)
				ids := app.receiveAndSync(
					incoming{From: "Boutique <info@boutique.com>", Subject: "Soldes"},
					incoming{From: "Garage <rdv@garage.fr>", Subject: "Rendez-vous"},
				)
				app.expectNotInInbox(ids[0])
				app.expectInInbox(ids[1])
				app.expectHistory(ids[0], "archive", SourceRule)
			},
		},
		{
			phrase: "Quand plusieurs règles correspondent à un mail, seule la première dans ton ordre s'applique.",
			status: observed,
			run: func(t *testing.T, app *testApp) {
				app.rule("Étiqueter les promos", 1, rules.FieldFrom, rules.OpContains, "boutique.com", promoLabel)
				app.rule("Archiver la boutique", 2, rules.FieldFrom, rules.OpContains, "boutique.com", archive)
				ids := app.receiveAndSync(incoming{From: "Boutique <info@boutique.com>", Subject: "Soldes"})
				app.applyRules()
				app.expectLabel(ids[0], "Promo")
				app.expectInInbox(ids[0])
			},
		},
		{
			phrase: "Une règle n'archive ni ne supprime jamais le mail d'un expéditeur protégé, mais ses autres actions (libellé, lu, étoile) s'appliquent quand même.",
			status: observed,
			run: func(t *testing.T, app *testApp) {
				app.protect("banque.fr")
				app.rule("Ranger la banque", 1, rules.FieldFrom, rules.OpContains, "banque.fr", promoLabel, archive)
				ids := app.receiveAndSync(incoming{From: "Ma Banque <news@banque.fr>", Subject: "Offre"})
				app.applyRules()
				app.expectLabel(ids[0], "Promo")
				app.expectInInbox(ids[0])
			},
		},
		{
			phrase: "\"Appliquer mes règles\" ne traite que les mails encore dans ta boîte de réception, pas ceux déjà archivés.",
			status: observed,
			run: func(t *testing.T, app *testApp) {
				app.rule("Étiqueter les promos", 1, rules.FieldFrom, rules.OpContains, "boutique.com", promoLabel)
				ids := app.receiveAndSync(
					incoming{From: "Boutique <info@boutique.com>", Subject: "Ancienne promo"},
					incoming{From: "Boutique <info@boutique.com>", Subject: "Nouvelle promo"},
				)
				app.act(ids[0], "archive")
				app.applyRules()
				app.expectNoLabel(ids[0], "Promo")
				app.expectLabel(ids[1], "Promo")
			},
		},
	})
}
