package rules

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
)

func portableRef() time.Time { return time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC) }

func sampleRule() models.SortingRule {
	return models.SortingRule{
		ID:           "6650000000000000000000a1",
		UserID:       "someone-else@example.com",
		Name:         "Newsletters",
		Enabled:      true,
		MatchAll:     true,
		Conditions:   []models.RuleCondition{{Field: FieldFrom, Operator: OpContains, Value: "news@"}},
		Actions:      []models.RuleAction{{Type: ActionLabel, LabelName: "Veille"}, {Type: ActionArchive}},
		Action:       ActionLabel,
		LabelName:    "Veille",
		Priority:     3,
		AppliedCount: 412,
	}
}

// An export must carry intent and nothing else: an id, an owner or a counter
// travelling with the file would either collide on import or hand the importer
// someone else's statistics.
func TestToPortableStripsAccountState(t *testing.T) {
	got := ToPortable([]models.SortingRule{sampleRule()})
	if len(got) != 1 {
		t.Fatalf("ToPortable returned %d rules, want 1", len(got))
	}

	raw, err := json.Marshal(got[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, leaked := range []string{"6650000000000000000000a1", "someone-else@example.com", "412"} {
		if strings.Contains(string(raw), leaked) {
			t.Errorf("export leaks account state %q: %s", leaked, raw)
		}
	}
	if got[0].Name != "Newsletters" || got[0].Priority != 3 || len(got[0].Actions) != 2 {
		t.Errorf("export lost the rule's intent: %#v", got[0])
	}
}

// A rule authored before multi-action carries only Action/LabelName. Exporting
// it must still describe what it does, or the round trip loses the action.
func TestToPortableResolvesLegacySingleAction(t *testing.T) {
	legacy := models.SortingRule{
		Name:       "Vieux",
		Enabled:    true,
		Conditions: []models.RuleCondition{{Field: FieldSubject, Operator: OpContains, Value: "facture"}},
		Action:     ActionArchive,
	}
	got := ToPortable([]models.SortingRule{legacy})
	if len(got[0].Actions) != 1 || got[0].Actions[0].Type != ActionArchive {
		t.Errorf("legacy rule exported as %#v, want a single archive action", got[0].Actions)
	}
}

// Export then import must produce a rule that behaves identically.
func TestExportImportRoundTripPreservesBehaviour(t *testing.T) {
	now := portableRef()
	original := sampleRule()

	doc := BuildExport([]models.SortingRule{original}, now)
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded Export
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	imported, err := ValidateImport(decoded, "me@example.com", now)
	if err != nil {
		t.Fatalf("ValidateImport: %v", err)
	}
	if len(imported) != 1 {
		t.Fatalf("imported %d rules, want 1", len(imported))
	}

	got := imported[0]
	if got.UserID != "me@example.com" {
		t.Errorf("imported rule owner = %q, want the importer", got.UserID)
	}
	if got.ID != "" || got.AppliedCount != 0 {
		t.Errorf("imported rule carries foreign state: id=%q appliedCount=%d", got.ID, got.AppliedCount)
	}
	if got.Action != ActionLabel || got.LabelName != "Veille" {
		t.Errorf("legacy mirror not backfilled: action=%q label=%q", got.Action, got.LabelName)
	}

	// The real assertion: the copy matches the same mail as the original.
	email := models.Email{From: "Acme <news@acme.com>", Subject: "Hello"}
	if Matches(email, original) != Matches(email, got) {
		t.Errorf("round trip changed matching: original=%v imported=%v",
			Matches(email, original), Matches(email, got))
	}
}

func TestValidateImportRejectsBadDocuments(t *testing.T) {
	now := portableRef()
	valid := PortableRule{
		Name:       "OK",
		Enabled:    true,
		Conditions: []models.RuleCondition{{Field: FieldFrom, Operator: OpContains, Value: "x"}},
		Actions:    []models.RuleAction{{Type: ActionArchive}},
	}

	tooMany := make([]PortableRule, MaxImportRules+1)
	for i := range tooMany {
		tooMany[i] = valid
	}

	cases := []struct {
		name string
		doc  Export
		want string // substring the message must carry
	}{
		{"no version", Export{Rules: []PortableRule{valid}}, "export de règles"},
		{"future version", Export{Version: ExportVersion + 1, Rules: []PortableRule{valid}}, "plus récente"},
		{"empty", Export{Version: ExportVersion}, "aucune règle"},
		{"too many", Export{Version: ExportVersion, Rules: tooMany}, "limite"},
		{
			"a rule with no action",
			Export{Version: ExportVersion, Rules: []PortableRule{{
				Name:       "Cassée",
				Conditions: []models.RuleCondition{{Field: FieldFrom, Operator: OpContains, Value: "x"}},
			}}},
			"Cassée",
		},
		{
			"a rule with a bad operator",
			Export{Version: ExportVersion, Rules: []PortableRule{{
				Name:       "Bizarre",
				Conditions: []models.RuleCondition{{Field: FieldFrom, Operator: "sounds-like", Value: "x"}},
				Actions:    []models.RuleAction{{Type: ActionArchive}},
			}}},
			"Bizarre",
		},
		{
			"an unnamed rule still gets a readable error",
			Export{Version: ExportVersion, Rules: []PortableRule{{
				Conditions: []models.RuleCondition{{Field: FieldFrom, Operator: OpContains, Value: "x"}},
				Actions:    []models.RuleAction{{Type: ActionArchive}},
			}}},
			"sans nom",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateImport(tc.doc, "me@example.com", now)
			if err == nil {
				t.Fatalf("ValidateImport accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
			if got != nil {
				t.Errorf("a rejected import must create nothing, got %d rules", len(got))
			}
		})
	}
}

// One bad rule must abort the whole file: a half-applied import leaves a
// ruleset that is neither the old one nor the one in the file.
func TestValidateImportIsAllOrNothing(t *testing.T) {
	doc := Export{Version: ExportVersion, Rules: []PortableRule{
		{
			Name:       "Bonne",
			Conditions: []models.RuleCondition{{Field: FieldFrom, Operator: OpContains, Value: "x"}},
			Actions:    []models.RuleAction{{Type: ActionArchive}},
		},
		{Name: "Mauvaise"}, // no conditions, no actions
	}}

	if got, err := ValidateImport(doc, "me@example.com", portableRef()); err == nil || got != nil {
		t.Errorf("ValidateImport = (%d rules, %v), want (nil, error)", len(got), err)
	}
}

func TestReorder(t *testing.T) {
	ruleset := []models.SortingRule{
		{ID: "a", Priority: 0},
		{ID: "b", Priority: 1},
		{ID: "c", Priority: 2},
	}

	t.Run("full order", func(t *testing.T) {
		got := Reorder(ruleset, []string{"c", "a", "b"})
		want := map[string]int{"c": 0, "a": 1, "b": 2}
		for id, p := range want {
			if got[id] != p {
				t.Errorf("priority of %q = %d, want %d (got %v)", id, got[id], p, got)
			}
		}
	})

	t.Run("unmentioned rules keep their order behind the rest", func(t *testing.T) {
		got := Reorder(ruleset, []string{"c"})
		if got["c"] != 0 || got["a"] != 1 || got["b"] != 2 {
			t.Errorf("partial reorder = %v, want c=0 a=1 b=2", got)
		}
	})

	t.Run("foreign ids are ignored", func(t *testing.T) {
		got := Reorder(ruleset, []string{"someone-elses-rule", "b"})
		if _, ok := got["someone-elses-rule"]; ok {
			t.Error("Reorder assigned a priority to a rule the account does not own")
		}
		if got["b"] != 0 {
			t.Errorf("b = %d, want 0 once the foreign id is skipped (got %v)", got["b"], got)
		}
	})

	t.Run("a repeated id is placed once", func(t *testing.T) {
		got := Reorder(ruleset, []string{"a", "a", "b", "c"})
		if len(got) != 3 {
			t.Fatalf("Reorder produced %d priorities, want 3: %v", len(got), got)
		}
		if got["a"] != 0 || got["b"] != 1 || got["c"] != 2 {
			t.Errorf("reorder with a duplicate id = %v, want a=0 b=1 c=2", got)
		}
	})

	// Two rules sharing a priority makes which one wins depend on the database
	// tie-break, which is exactly the ambiguity ordering exists to remove.
	t.Run("priorities are unique", func(t *testing.T) {
		got := Reorder(ruleset, []string{"b"})
		seen := map[int]string{}
		for id, p := range got {
			if other, clash := seen[p]; clash {
				t.Errorf("%q and %q both got priority %d", id, other, p)
			}
			seen[p] = id
		}
	})
}

func TestDuplicateName(t *testing.T) {
	cases := []struct {
		name     string
		original string
		taken    []string
		want     string
	}{
		{"first copy", "Factures", []string{"Factures"}, "Factures (copie)"},
		{"second copy", "Factures", []string{"Factures", "Factures (copie)"}, "Factures (copie 2)"},
		{"third copy", "Factures", []string{"Factures", "Factures (copie)", "Factures (copie 2)"}, "Factures (copie 3)"},
		{"nothing taken", "Factures", nil, "Factures (copie)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DuplicateName(tc.original, tc.taken); got != tc.want {
				t.Errorf("DuplicateName(%q, %v) = %q, want %q", tc.original, tc.taken, got, tc.want)
			}
		})
	}
}
