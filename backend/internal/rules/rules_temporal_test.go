package rules

import (
	"testing"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
)

func TestNegationOperators(t *testing.T) {
	e := sampleEmail()
	cases := []struct {
		name string
		c    models.RuleCondition
		want bool
	}{
		{"notContains hit (absent term)", cond(FieldFrom, OpNotContains, "spotify"), true},
		{"notContains miss (present term)", cond(FieldFrom, OpNotContains, "acme"), false},
		{"notEquals hit", cond(FieldSubject, OpNotEquals, "something else"), true},
		{"notEquals miss (folds+trims)", cond(FieldSubject, OpNotEquals, "  your weekly digest is here  "), false},
		// An empty value never matches, even for a negated operator — otherwise a
		// blank rule would "not contain" everything and act on the whole inbox.
		{"empty value never matches", cond(FieldFrom, OpNotContains, "  "), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchCondition(e, tc.c); got != tc.want {
				t.Errorf("matchCondition(%+v) = %v, want %v", tc.c, got, tc.want)
			}
		})
	}
}

func TestTemporalOperators(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	old := models.Email{ReceivedDate: now.AddDate(0, 0, -40)}   // 40 days ago
	recent := models.Email{ReceivedDate: now.AddDate(0, 0, -2)} // 2 days ago
	undated := models.Email{}                                   // zero ReceivedDate

	cases := []struct {
		name  string
		email models.Email
		c     models.RuleCondition
		want  bool
	}{
		{"olderThan 30 -> 40d old matches", old, cond(FieldFrom, OpOlderThan, "30"), true},
		{"olderThan 30 -> 2d old does not", recent, cond(FieldFrom, OpOlderThan, "30"), false},
		{"newerThan 7 -> 2d old matches", recent, cond(FieldFrom, OpNewerThan, "7"), true},
		{"newerThan 7 -> 40d old does not", old, cond(FieldFrom, OpNewerThan, "7"), false},
		{"undated never matches olderThan", undated, cond(FieldFrom, OpOlderThan, "1"), false},
		{"undated never matches newerThan", undated, cond(FieldFrom, OpNewerThan, "1000"), false},
		{"malformed days never matches", old, cond(FieldFrom, OpOlderThan, "soon"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchConditionAt(tc.email, tc.c, now); got != tc.want {
				t.Errorf("matchConditionAt(%+v) = %v, want %v", tc.c, got, tc.want)
			}
		})
	}
}

// A temporal rule must forecast and apply deterministically through the
// public MatchesAt / PreviewAt entry points, not just the internal helper.
func TestTemporalThroughMatchesAndPreview(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	rule := models.SortingRule{
		Name:       "Vieilles newsletters",
		Enabled:    true,
		MatchAll:   true,
		Action:     ActionArchive,
		Conditions: []models.RuleCondition{cond(FieldFrom, OpOlderThan, "30")},
	}
	old := models.Email{MessageID: "old", From: "x@y.com", ReceivedDate: now.AddDate(0, 0, -45)}
	fresh := models.Email{MessageID: "fresh", From: "x@y.com", ReceivedDate: now.AddDate(0, 0, -1)}

	if !MatchesAt(old, rule, now) {
		t.Error("a 45-day-old email should match olderThan 30")
	}
	if MatchesAt(fresh, rule, now) {
		t.Error("a 1-day-old email should not match olderThan 30")
	}

	items, hits := PreviewAt([]models.Email{old, fresh}, []models.SortingRule{rule}, now)
	if len(items) != 1 || items[0].MessageID != "old" {
		t.Fatalf("preview should flag only the old email, got %+v", items)
	}
	if len(hits) != 1 || hits[0].Matched != 1 {
		t.Fatalf("expected one rule hit matching one email, got %+v", hits)
	}
}

func TestValidateTemporalAndNegation(t *testing.T) {
	good := []models.SortingRule{
		{Name: "neg", Action: ActionArchive, Conditions: []models.RuleCondition{cond(FieldSubject, OpNotContains, "facture")}},
		{Name: "age", Action: ActionArchive, Conditions: []models.RuleCondition{cond(FieldFrom, OpOlderThan, "30")}},
		{Name: "age0", Action: ActionArchive, Conditions: []models.RuleCondition{cond(FieldFrom, OpNewerThan, "0")}},
	}
	for i, r := range good {
		if err := Validate(r); err != nil {
			t.Errorf("good case %d should validate, got %v", i, err)
		}
	}

	bad := []models.SortingRule{
		{Name: "nan", Action: ActionArchive, Conditions: []models.RuleCondition{cond(FieldFrom, OpOlderThan, "bientôt")}},
		{Name: "neg-days", Action: ActionArchive, Conditions: []models.RuleCondition{cond(FieldFrom, OpNewerThan, "-3")}},
	}
	for i, r := range bad {
		if err := Validate(r); err == nil {
			t.Errorf("bad temporal case %d should fail validation", i)
		}
	}
}
