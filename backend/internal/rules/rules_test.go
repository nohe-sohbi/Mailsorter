package rules

import (
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
)

func cond(field, op, val string) models.RuleCondition {
	return models.RuleCondition{Field: field, Operator: op, Value: val}
}

func sampleEmail() models.Email {
	return models.Email{
		From:    "Acme Newsletter <news@mailing.acme.com>",
		To:      []string{"me@example.com"},
		Subject: "Your weekly digest is here",
		Snippet: "Top stories and 50% OFF promotions inside",
		Body:    "Unsubscribe at the bottom",
	}
}

func TestMatchConditionOperators(t *testing.T) {
	e := sampleEmail()
	cases := []struct {
		name string
		c    models.RuleCondition
		want bool
	}{
		{"contains case-insensitive", cond(FieldFrom, OpContains, "ACME"), true},
		{"contains miss", cond(FieldFrom, OpContains, "spotify"), false},
		{"equals trims+folds", cond(FieldSubject, OpEquals, "  your weekly digest is here  "), true},
		{"equals miss", cond(FieldSubject, OpEquals, "weekly digest"), false},
		{"startsWith", cond(FieldSubject, OpStartsWith, "Your weekly"), true},
		{"startsWith miss", cond(FieldSubject, OpStartsWith, "weekly"), false},
		{"endsWith", cond(FieldSubject, OpEndsWith, "here"), true},
		{"endsWith miss", cond(FieldSubject, OpEndsWith, "there"), false},
		{"regex digits", cond(FieldSnippet, OpRegex, `\d+% OFF`), true},
		{"regex miss", cond(FieldSnippet, OpRegex, `^free shipping`), false},
		{"to joined", cond(FieldTo, OpContains, "me@example.com"), true},
		{"body field", cond(FieldBody, OpContains, "unsubscribe"), true},
		{"empty value never matches", cond(FieldFrom, OpContains, "   "), false},
		{"unknown field", cond("cc", OpContains, "x"), false},
		{"unknown operator", cond(FieldFrom, "fuzzy", "acme"), false},
		{"invalid regex never matches", cond(FieldSubject, OpRegex, "([a-z"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchCondition(e, tc.c); got != tc.want {
				t.Errorf("matchCondition(%+v) = %v, want %v", tc.c, got, tc.want)
			}
		})
	}
}

func TestMatchesMatchAllVsAny(t *testing.T) {
	e := sampleEmail()

	all := models.SortingRule{
		Enabled:  true,
		MatchAll: true,
		Conditions: []models.RuleCondition{
			cond(FieldFrom, OpContains, "acme"),
			cond(FieldSubject, OpContains, "digest"),
		},
	}
	if !Matches(e, all) {
		t.Error("MatchAll rule with both conditions true should match")
	}

	all.Conditions[1] = cond(FieldSubject, OpContains, "invoice")
	if Matches(e, all) {
		t.Error("MatchAll rule with one false condition should not match")
	}

	any := models.SortingRule{
		Enabled:  true,
		MatchAll: false,
		Conditions: []models.RuleCondition{
			cond(FieldSubject, OpContains, "invoice"), // false
			cond(FieldFrom, OpContains, "acme"),       // true
		},
	}
	if !Matches(e, any) {
		t.Error("OR rule with one true condition should match")
	}
}

func TestMatchesGuards(t *testing.T) {
	e := sampleEmail()

	disabled := models.SortingRule{Enabled: false, Conditions: []models.RuleCondition{cond(FieldFrom, OpContains, "acme")}}
	if Matches(e, disabled) {
		t.Error("disabled rule must never match")
	}

	empty := models.SortingRule{Enabled: true, MatchAll: true}
	if Matches(e, empty) {
		t.Error("rule with no conditions must never match (no accidental match-all)")
	}
}

func TestFirstMatchHonorsOrder(t *testing.T) {
	e := sampleEmail()
	ruleset := []models.SortingRule{
		{Name: "label", Enabled: true, Action: ActionLabel, Conditions: []models.RuleCondition{cond(FieldSubject, OpContains, "invoice")}}, // miss
		{Name: "archive", Enabled: true, Action: ActionArchive, Conditions: []models.RuleCondition{cond(FieldFrom, OpContains, "acme")}},   // hit
		{Name: "trash", Enabled: true, Action: ActionTrash, Conditions: []models.RuleCondition{cond(FieldSnippet, OpContains, "OFF")}},     // also hit
	}
	got := FirstMatch(e, ruleset)
	if got == nil || got.Name != "archive" {
		t.Fatalf("FirstMatch should return the first matching rule in order, got %+v", got)
	}

	none := FirstMatch(e, []models.SortingRule{
		{Name: "x", Enabled: true, Conditions: []models.RuleCondition{cond(FieldSubject, OpContains, "nope")}},
	})
	if none != nil {
		t.Errorf("FirstMatch with no matches should return nil, got %+v", none)
	}
}

func TestValidate(t *testing.T) {
	valid := models.SortingRule{
		Name:       "Archive Acme",
		Action:     ActionArchive,
		Conditions: []models.RuleCondition{cond(FieldFrom, OpContains, "acme")},
	}
	if err := Validate(valid); err != nil {
		t.Errorf("expected valid rule, got error: %v", err)
	}

	bad := []models.SortingRule{
		{Name: "", Action: ActionArchive, Conditions: []models.RuleCondition{cond(FieldFrom, OpContains, "a")}},
		{Name: "n", Action: "explode", Conditions: []models.RuleCondition{cond(FieldFrom, OpContains, "a")}},
		{Name: "n", Action: ActionLabel, Conditions: []models.RuleCondition{cond(FieldFrom, OpContains, "a")}}, // label without name
		{Name: "n", Action: ActionArchive}, // no conditions
		{Name: "n", Action: ActionArchive, Conditions: []models.RuleCondition{cond("cc", OpContains, "a")}},
		{Name: "n", Action: ActionArchive, Conditions: []models.RuleCondition{cond(FieldFrom, "fuzzy", "a")}},
		{Name: "n", Action: ActionArchive, Conditions: []models.RuleCondition{cond(FieldFrom, OpContains, "")}},
		{Name: "n", Action: ActionArchive, Conditions: []models.RuleCondition{cond(FieldFrom, OpRegex, "([a-z")}},
	}
	for i, r := range bad {
		if err := Validate(r); err == nil {
			t.Errorf("case %d: expected validation error, got nil", i)
		}
	}

	withLabel := models.SortingRule{
		Name:       "Tag promos",
		Action:     ActionLabel,
		LabelName:  "Promos",
		Conditions: []models.RuleCondition{cond(FieldSnippet, OpContains, "off")},
	}
	if err := Validate(withLabel); err != nil {
		t.Errorf("label rule with name should be valid, got: %v", err)
	}
}
