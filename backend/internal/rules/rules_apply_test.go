package rules

import (
	"reflect"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
)

func TestApplyOrder(t *testing.T) {
	archive := models.RuleAction{Type: ActionArchive}
	trash := models.RuleAction{Type: ActionTrash}
	star := models.RuleAction{Type: ActionStar}
	read := models.RuleAction{Type: ActionMarkRead}
	news := models.RuleAction{Type: ActionLabel, LabelName: "Newsletters"}
	bills := models.RuleAction{Type: ActionLabel, LabelName: "Factures"}

	cases := []struct {
		name string
		in   []models.RuleAction
		want []models.RuleAction
	}{
		{"archive then star: the star first", []models.RuleAction{archive, star}, []models.RuleAction{star, archive}},
		{"star then archive: unchanged", []models.RuleAction{star, archive}, []models.RuleAction{star, archive}},
		{"trash before two flags: last, the flags in their order", []models.RuleAction{trash, read, star}, []models.RuleAction{read, star, trash}},
		{"labels keep their order before the move", []models.RuleAction{archive, news, bills}, []models.RuleAction{news, bills, archive}},
		{"two moves keep theirs, after the rest", []models.RuleAction{trash, star, archive}, []models.RuleAction{star, trash, archive}},
		{"no move: unchanged", []models.RuleAction{read, news}, []models.RuleAction{read, news}},
		{"a move alone", []models.RuleAction{archive}, []models.RuleAction{archive}},
		{"none", nil, []models.RuleAction{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ApplyOrder(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ApplyOrder(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// ApplyOrder hands back a list of its own: the rule it was read from keeps
// the order the user wrote, which is what the editor and the preview show.
func TestApplyOrderLeavesTheRuleAsWritten(t *testing.T) {
	written := []models.RuleAction{{Type: ActionArchive}, {Type: ActionStar}}
	ApplyOrder(written)
	if written[0].Type != ActionArchive || written[1].Type != ActionStar {
		t.Errorf("ApplyOrder changed the list it was given: %v, want archive then star", written)
	}
}

func TestReadsText(t *testing.T) {
	on := func(field, op, value string) models.SortingRule {
		return models.SortingRule{Enabled: true, Conditions: []models.RuleCondition{{Field: field, Operator: op, Value: value}}}
	}
	off := on(FieldBody, OpContains, "remboursement")
	off.Enabled = false
	mixed := models.SortingRule{Enabled: true, Conditions: []models.RuleCondition{
		{Field: FieldFrom, Operator: OpContains, Value: "boutique.com"},
		{Field: FieldBody, Operator: OpNotContains, Value: "facture"},
	}}

	cases := []struct {
		name    string
		ruleset []models.SortingRule
		want    bool
	}{
		{"a rule on the body", []models.SortingRule{on(FieldBody, OpContains, "remboursement")}, true},
		{"a rule on the snippet", []models.SortingRule{on(FieldSnippet, OpStartsWith, "Bonjour")}, true},
		{"the field in capitals", []models.SortingRule{on("Body", OpContains, "remboursement")}, true},
		{"a negation on the body, among other conditions", []models.SortingRule{mixed}, true},
		{"one rule of several", []models.SortingRule{on(FieldFrom, OpContains, "a"), on(FieldBody, OpRegex, "x+")}, true},
		{"sender, subject and recipients only", []models.SortingRule{on(FieldFrom, OpContains, "a"), on(FieldSubject, OpEquals, "b"), on(FieldTo, OpEndsWith, "c")}, false},
		{"a disabled rule on the body", []models.SortingRule{off}, false},
		{"an age on the body field reads the date", []models.SortingRule{on(FieldBody, OpOlderThan, "30")}, false},
		{"a blank value matches nothing", []models.SortingRule{on(FieldBody, OpNotContains, "  ")}, false},
		{"no rules", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ReadsText(tc.ruleset); got != tc.want {
				t.Errorf("ReadsText(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// Over IMAP a rule cannot label: the preview leaves the label out of what a
// rule would do, and does not count a mail whose rule would only label it.
// That mail stays its rule's, as applying it would: it does not fall through
// to the next rule.
func TestPreviewWithout(t *testing.T) {
	newsletters := models.RuleAction{Type: ActionLabel, LabelName: "Newsletters"}
	archive := models.RuleAction{Type: ActionArchive}
	star := models.RuleAction{Type: ActionStar}
	ruleset := []models.SortingRule{
		{Name: "Étiqueter Acme", Enabled: true, MatchAll: true, Actions: []models.RuleAction{newsletters},
			Conditions: []models.RuleCondition{{Field: FieldFrom, Operator: OpContains, Value: "acme"}}},
		{Name: "Ranger la boutique", Enabled: true, MatchAll: true, Actions: []models.RuleAction{newsletters, star, archive},
			Conditions: []models.RuleCondition{{Field: FieldFrom, Operator: OpContains, Value: "boutique"}}},
		{Name: "Tout le reste", Enabled: true, MatchAll: true, Actions: []models.RuleAction{archive},
			Conditions: []models.RuleCondition{{Field: FieldFrom, Operator: OpContains, Value: "@"}}},
	}
	emails := []models.Email{
		{MessageID: "1", From: "news@acme.com", Subject: "Acme"},
		{MessageID: "2", From: "info@boutique.com", Subject: "Boutique"},
	}

	items, hits := PreviewWithout(emails, ruleset, ActionLabel)
	if len(items) != 1 || items[0].MessageID != "2" {
		t.Fatalf("PreviewWithout(label) items = %+v, want only message 2 (message 1's rule only labels, and stays its rule's)", items)
	}
	want := []models.RuleAction{star, archive}
	if !reflect.DeepEqual(items[0].Actions, want) || items[0].Action != ActionStar || items[0].LabelName != "" {
		t.Errorf("PreviewWithout(label) item = %+v, want actions %v, primary star, no label", items[0], want)
	}
	if len(hits) != 1 || hits[0].RuleName != "Ranger la boutique" || hits[0].Matched != 1 || hits[0].Action != ActionStar || !reflect.DeepEqual(hits[0].Actions, want) {
		t.Errorf("PreviewWithout(label) hits = %+v, want one tally for \"Ranger la boutique\", 1 mail, star then archive", hits)
	}

	// Nothing skipped: the preview as Preview gives it.
	all, allHits := PreviewWithout(emails, ruleset)
	plain, plainHits := Preview(emails, ruleset)
	if !reflect.DeepEqual(all, plain) || !reflect.DeepEqual(allHits, plainHits) {
		t.Errorf("PreviewWithout() = %+v %+v, want Preview's %+v %+v", all, allHits, plain, plainHits)
	}
}
