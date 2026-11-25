package mailbox

import "testing"

// What the model proposes is matched with the user's labels by name, case,
// accents and a plural set aside, and never by a part of a name.
func TestMatchLabel(t *testing.T) {
	existing := []string{"Factures", "Pro", "Impôts", "Projets/Maison", "Notes de frais"}
	cases := []struct {
		suggested string
		want      string
		ok        bool
	}{
		{"Facture", "Factures", true},
		{"factures", "Factures", true},
		{"FACTURE", "Factures", true},
		{"Impots", "Impôts", true},
		{"impôt", "Impôts", true},
		{"projet/maisons", "Projets/Maison", true},
		{"Note de frais", "Notes de frais", true},
		{"  Notes   de frais ", "Notes de frais", true},
		{"Pro", "Pro", true},
		{"Promotions", "", false},
		{"Professionnel", "", false},
		{"Fact", "", false},
		{"Facturation", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := MatchLabel(tc.suggested, existing)
		if got != tc.want || ok != tc.ok {
			t.Errorf("MatchLabel(%q) = %q, %v; want %q, %v", tc.suggested, got, ok, tc.want, tc.ok)
		}
	}
	// Spelled exactly as suggested wins over a first one alike.
	if got, _ := MatchLabel("factures", []string{"Factures", "factures"}); got != "factures" {
		t.Errorf("MatchLabel(factures) among Factures and factures = %q, want the exact one", got)
	}
}

func TestLabelKey(t *testing.T) {
	cases := map[string]string{
		"Factures":       "facture",
		"Éléments":       "element",
		"Œuvres":         "oeuvre",
		"Travaux":        "travau",
		"Bus":            "bus",
		"Pro":            "pro",
		"Projets/Maison": "projet/maison",
	}
	for in, want := range cases {
		if got := LabelKey(in); got != want {
			t.Errorf("LabelKey(%q) = %q, want %q", in, got, want)
		}
	}
}
