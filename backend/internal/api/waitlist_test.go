package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
		// Oversized email (> 254 bytes)
		"a" + strings.Repeat("a", 250) + "@example.com",
	}
	for _, raw := range rejected {
		if got := normalizeWaitlistEmail(raw); got != "" {
			t.Errorf("normalizeWaitlistEmail(%q) = %q, want it rejected", raw, got)
		}
	}
}

func TestJoinWaitlistRejectsOversizedSource(t *testing.T) {
	h := &Handler{}
	// source longer than maxWaitlistSourceLength (64)
	body := strings.NewReader(`{"email":"user@example.com","source":"` + strings.Repeat("x", 65) + `"}`)
	req := httptest.NewRequest("POST", "/api/waitlist", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.JoinWaitlist(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
