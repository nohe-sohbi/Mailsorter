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

func TestApplyOrderOnFolders(t *testing.T) {
	archive := models.RuleAction{Type: ActionArchive}
	trash := models.RuleAction{Type: ActionTrash}
	star := models.RuleAction{Type: ActionStar}
	read := models.RuleAction{Type: ActionMarkRead}
	bills := models.RuleAction{Type: ActionLabel, LabelName: "Factures"}
	news := models.RuleAction{Type: ActionLabel, LabelName: "Newsletters"}

	cases := []struct {
		name string
		in   []models.RuleAction
		want []models.RuleAction
	}{
		{"a label is a move: after the flags", []models.RuleAction{bills, star}, []models.RuleAction{star, bills}},
		{"label then archive: one move, the last written", []models.RuleAction{bills, archive}, []models.RuleAction{archive}},
		{"archive then label: files in the folder", []models.RuleAction{archive, bills}, []models.RuleAction{bills}},
		{"two labels: the last one", []models.RuleAction{bills, read, news}, []models.RuleAction{read, news}},
		{"trash among them, last written", []models.RuleAction{star, bills, trash}, []models.RuleAction{star, trash}},
		{"flags only: unchanged", []models.RuleAction{read, star}, []models.RuleAction{read, star}},
		{"none", nil, []models.RuleAction{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ApplyOrderOnFolders(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ApplyOrderOnFolders(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// Over IMAP the preview shows what applying a rule does there: the flags,
// then one move, a label being one (the folder it files in).
func TestPreviewOnFolders(t *testing.T) {
	bills := models.RuleAction{Type: ActionLabel, LabelName: "Factures"}
	star := models.RuleAction{Type: ActionStar}
	ruleset := []models.SortingRule{
		{Name: "Classer les factures", Enabled: true, MatchAll: true, Actions: []models.RuleAction{bills, star},
			Conditions: []models.RuleCondition{{Field: FieldSubject, Operator: OpContains, Value: "facture"}}},
	}
	emails := []models.Email{
		{MessageID: "1", From: "a@b.com", Subject: "Votre facture"},
		{MessageID: "2", From: "a@b.com", Subject: "Bonjour"},
	}
	items, hits := PreviewOnFolders(emails, ruleset)
	want := []models.RuleAction{star, bills}
	if len(items) != 1 || items[0].MessageID != "1" || !reflect.DeepEqual(items[0].Actions, want) || items[0].Action != ActionStar {
		t.Fatalf("PreviewOnFolders items = %+v, want message 1 with the star then the folder", items)
	}
	if len(hits) != 1 || hits[0].Matched != 1 || !reflect.DeepEqual(hits[0].Actions, want) {
		t.Errorf("PreviewOnFolders hits = %+v, want one tally, 1 mail, star then the folder", hits)
	}
	// On the Gmail API the preview keeps the order written.
	plain, _ := Preview(emails, ruleset)
	if len(plain) != 1 || !reflect.DeepEqual(plain[0].Actions, []models.RuleAction{bills, star}) {
		t.Errorf("Preview items = %+v, want the actions as written", plain)
	}
}
