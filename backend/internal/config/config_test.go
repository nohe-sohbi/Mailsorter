package config

import "testing"

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
			c := &Config{EncryptionKey: tt.key}
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
