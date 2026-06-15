package api

import (
	"net/http"
	"testing"
)

func TestIsPublicPath(t *testing.T) {
	public := []string{
		"/health",
		"/api/auth/url",
		"/api/auth/callback",
		"/api/config/status",
		"/api/config/gmail",
		"/api/billing/webhook",
	}
	for _, p := range public {
		if !isPublicPath(p) {
			t.Errorf("expected %q to be public", p)
		}
	}

	protected := []string{
		"/api/emails",
		"/api/ai/analyze",
		"/api/billing/checkout",
		"/api/usage",
		"/api/subscriptions",
	}
	for _, p := range protected {
		if isPublicPath(p) {
			t.Errorf("expected %q to be protected", p)
		}
	}
}

func TestBearerToken(t *testing.T) {
	cases := map[string]string{
		"Bearer abc123": "abc123",
		"bearer abc123": "abc123", // case-insensitive scheme
		"abc123":        "abc123", // bare token tolerated
		"":              "",
	}
	for header, want := range cases {
		r, _ := http.NewRequest("GET", "/", nil)
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		if got := bearerToken(r); got != want {
			t.Errorf("bearerToken(%q) = %q, want %q", header, got, want)
		}
	}
}

func TestRateLimiterAllowsBurstThenBlocks(t *testing.T) {
	// Effectively no refill during the test window so we measure the burst.
	rl := newRateLimiter(0.0001, 3)
	allowed := 0
	for i := 0; i < 10; i++ {
		if rl.allow("client-1") {
			allowed++
		}
	}
	if allowed != 3 {
		t.Fatalf("expected exactly 3 requests allowed (burst capacity), got %d", allowed)
	}
}

func TestRateLimiterIsolatesClients(t *testing.T) {
	rl := newRateLimiter(0.0001, 1)
	if !rl.allow("a") {
		t.Fatal("first request for client a should be allowed")
	}
	if !rl.allow("b") {
		t.Fatal("client b must have its own independent bucket")
	}
	if rl.allow("a") {
		t.Fatal("client a should be exhausted after its single token")
	}
}
