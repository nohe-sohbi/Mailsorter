package api

import "testing"

// The waitlist count is only worth something if one person is one row, so
// normalization has to collapse the ways the same address can be typed, and
// reject anything we could never write to.
func TestNormalizeWaitlistEmail(t *testing.T) {
	same := []string{
		"nohe@example.com",
		"  nohe@example.com  ",
		"Nohe@Example.com",
		"NOHE@EXAMPLE.COM",
		"Nohé Sohbi <nohe@example.com>",
	}
	for _, raw := range same {
		if got := normalizeWaitlistEmail(raw); got != "nohe@example.com" {
			t.Errorf("normalizeWaitlistEmail(%q) = %q, want %q", raw, got, "nohe@example.com")
		}
	}

	rejected := []string{
		"",
		"   ",
		"not-an-email",
		"@example.com",
		"nohe@",
		"nohe example.com",
		"nohe@@example.com",
	}
	for _, raw := range rejected {
		if got := normalizeWaitlistEmail(raw); got != "" {
			t.Errorf("normalizeWaitlistEmail(%q) = %q, want it rejected", raw, got)
		}
	}
}
