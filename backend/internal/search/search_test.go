package search

import (
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		label     string
		name      string
		query     string
		wantName  string
		wantQuery string
		wantErr   bool
	}{
		{"plain", "Recrutement", "in:inbox from:linkedin.com", "Recrutement", "in:inbox from:linkedin.com", false},
		{"trims", "  Factures  ", "  in:inbox facture  ", "Factures", "in:inbox facture", false},
		{"collapses inner whitespace in the name", "Mes\t\tfactures", "in:inbox", "Mes factures", "in:inbox", false},
		{"no name", "   ", "in:inbox", "", "", true},
		{"no query", "Vide", "  ", "", "", true},
		{"name too long", strings.Repeat("a", MaxNameLength+1), "in:inbox", "", "", true},
		{"query too long", "Long", strings.Repeat("a", MaxQueryLength+1), "", "", true},
		{"multiline query", "Collé", "in:inbox\nfrom:x", "", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			name, query, err := Normalize(tc.name, tc.query)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Normalize(%q, %q) = nil error, want one", tc.name, tc.query)
				}
				return
			}
			if err != nil {
				t.Fatalf("Normalize(%q, %q) = %v", tc.name, tc.query, err)
			}
			if name != tc.wantName || query != tc.wantQuery {
				t.Errorf("Normalize(%q, %q) = (%q, %q), want (%q, %q)",
					tc.name, tc.query, name, query, tc.wantName, tc.wantQuery)
			}
		})
	}
}

// A length limit counted in bytes would reject a shorter accented name than an
// ASCII one, which is a French-first product getting its own alphabet wrong.
func TestNormalizeCountsCharactersNotBytes(t *testing.T) {
	name := strings.Repeat("é", MaxNameLength)
	if _, _, err := Normalize(name, "in:inbox"); err != nil {
		t.Errorf("a %d-character accented name was rejected: %v", MaxNameLength, err)
	}
}

func TestKeyIdentifiesTheSameQuery(t *testing.T) {
	same := []string{
		"in:inbox from:LinkedIn.com",
		"IN:INBOX FROM:linkedin.com",
		"  in:inbox   from:linkedin.com  ",
	}
	want := Key(same[0])
	for _, q := range same[1:] {
		if got := Key(q); got != want {
			t.Errorf("Key(%q) = %q, want %q: the same query must be one shortcut", q, got, want)
		}
	}
	if Key("in:inbox is:unread") == want {
		t.Error("two different queries collapsed onto the same key")
	}
}

func TestSuggestName(t *testing.T) {
	cases := map[string]string{
		"in:inbox from:linkedin.com":  "linkedin.com",
		"in:inbox is:unread":          "unread",
		"from:\"Acme Corp\"":          "Acme Corp",
		"facture":                     "facture",
		"in:inbox":                    "inbox",
		"  in:inbox  older_than:7d  ": "7d",
		"":                            "",
	}
	for query, want := range cases {
		if got := SuggestName(query); got != want {
			t.Errorf("SuggestName(%q) = %q, want %q", query, got, want)
		}
	}
}

// Whatever SuggestName proposes must be accepted by Normalize, or the save
// dialog would open pre-filled with a value the server refuses.
func TestSuggestedNamesAreValid(t *testing.T) {
	for _, query := range []string{
		"in:inbox from:linkedin.com",
		"in:inbox is:unread",
		"in:inbox larger:5M",
		"in:inbox has:attachment newer_than:1d",
	} {
		if _, _, err := Normalize(SuggestName(query), query); err != nil {
			t.Errorf("SuggestName(%q) = %q, which Normalize rejects: %v", query, SuggestName(query), err)
		}
	}
}
