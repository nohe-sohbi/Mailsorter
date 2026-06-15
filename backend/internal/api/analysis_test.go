package api

import "testing"

func TestAnalysisCacheKeyIsStable(t *testing.T) {
	a := analysisCacheKey("Sender <s@x.com>", "Hello")
	b := analysisCacheKey("Sender <s@x.com>", "Hello")
	if a != b {
		t.Fatal("cache key should be deterministic for identical input")
	}
}

func TestAnalysisCacheKeyNormalizesCaseAndSpace(t *testing.T) {
	a := analysisCacheKey("Sender <s@x.com>", "Hello")
	b := analysisCacheKey("  sender <S@X.COM>  ", "  HELLO  ")
	if a != b {
		t.Fatal("cache key should ignore case and surrounding whitespace")
	}
}

func TestAnalysisCacheKeyDiffersForDifferentInput(t *testing.T) {
	if analysisCacheKey("a@x.com", "Subject A") == analysisCacheKey("a@x.com", "Subject B") {
		t.Fatal("different subjects must produce different keys")
	}
}

func TestLocalMatchLabel(t *testing.T) {
	existing := []string{"Newsletters", "Factures", "Travail"}
	cases := []struct {
		suggested string
		want      string
	}{
		{"newsletters", "Newsletters"}, // case-insensitive exact
		{"Newsletter", "Newsletters"},  // substring (s ⊂ e)
		{"Factures", "Factures"},       // exact
		{"Voyages", "Voyages"},         // no match -> returned as-is
		{"  travail ", "Travail"},      // trimmed + case
	}
	for _, c := range cases {
		if got := localMatchLabel(c.suggested, existing); got != c.want {
			t.Errorf("localMatchLabel(%q) = %q, want %q", c.suggested, got, c.want)
		}
	}
}

func TestLocalMatchLabelNoExisting(t *testing.T) {
	if got := localMatchLabel("Anything", nil); got != "Anything" {
		t.Fatalf("with no existing labels, want passthrough, got %q", got)
	}
}
