package config

import (
	"strings"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
)

func TestValidateEncryptionKey(t *testing.T) {
	strong := "this-is-a-sufficiently-long-random-key-123"

	tests := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{"empty", "", true},
		{"compose default", "default-dev-key-change-in-production", true},
		{"env.example placeholder", "change-this-to-a-secure-random-string-32chars", true},
		{"too short", "short-key", true},
		{"exactly minimum length", "0123456789012345678901234567890a", false},
		{"strong key", strong, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Everything but the key is valid: this case is about the key, and
			// Validate refuses a config that could not boot for any reason.
			c := &Config{EncryptionKey: tt.key, Edition: provider.EditionSelfHosted}
			err := c.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestGetEnvListDefault(t *testing.T) {
	def := []string{"a", "b"}
	if got := getEnvList("MAILSORTER_NONEXISTENT_ENV", def); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("getEnvList fallback = %v, want %v", got, def)
	}
}

func TestGetEnvListParsing(t *testing.T) {
	t.Setenv("MAILSORTER_TEST_ORIGINS", " https://a.com , ,https://b.com ")
	got := getEnvList("MAILSORTER_TEST_ORIGINS", nil)
	want := []string{"https://a.com", "https://b.com"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("getEnvList = %v, want %v", got, want)
	}
}

// The edition decides which mailbox routes exist, so a typo must stop the boot
// rather than silently offer the wrong set of providers. A wrong EDITION shows
// up much later, as a connection the user cannot make and nobody can explain.
func TestValidateRejectsAnUnknownEdition(t *testing.T) {
	base := func() *Config {
		return &Config{EncryptionKey: strings.Repeat("k", minEncryptionKeyLen)}
	}

	for _, good := range []provider.Edition{provider.EditionSelfHosted, provider.EditionHosted} {
		c := base()
		c.Edition = good
		if err := c.Validate(); err != nil {
			t.Errorf("Validate() with EDITION=%q = %v, want nil", good, err)
		}
	}

	for _, bad := range []provider.Edition{"", "selfhosted", "SELF-HOSTED", "saas", "hoted"} {
		c := base()
		c.Edition = bad
		err := c.Validate()
		if err == nil {
			t.Errorf("Validate() with EDITION=%q = nil, want an error", bad)
			continue
		}
		if !strings.Contains(err.Error(), "EDITION") {
			t.Errorf("Validate() with EDITION=%q said %q, want it to name EDITION so the operator knows what to fix", bad, err)
		}
	}
}

// Load must normalize what an operator actually types in a .env file.
func TestLoadNormalizesTheEdition(t *testing.T) {
	cases := map[string]provider.Edition{
		"":            provider.EditionSelfHosted, // unset: the safe default
		"hosted":      provider.EditionHosted,
		"  Hosted  ":  provider.EditionHosted,
		"SELF-HOSTED": provider.EditionSelfHosted,
	}
	for raw, want := range cases {
		t.Setenv("EDITION", raw)
		if got := Load().Edition; got != want {
			t.Errorf("Load() with EDITION=%q = %q, want %q", raw, got, want)
		}
	}
}
