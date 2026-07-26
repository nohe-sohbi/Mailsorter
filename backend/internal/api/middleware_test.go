package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/auth"
)

func TestIsPublicPath(t *testing.T) {
	public := []string{
		"/health",
		"/api/auth/url",
		"/api/auth/callback",
		"/api/config/status",
		// Cold traffic has no session, and capturing it is the whole point.
		"/api/waitlist",
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
		// The Gmail credentials are instance-wide: an unauthenticated write
		// there would hijack the OAuth flow for every user. They have no HTTP
		// surface at all now, and the /api/config/ prefix must never be public
		// again as a whole.
		"/api/config/gmail",
		"/api/config/",
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

// A public route must never demand a session, but it should still know who the
// caller is when they happen to have one: the waitlist uses that to tell an
// existing user apart from an anonymous visitor. Anything less than a valid
// signature must leave the request anonymous rather than reject it.
func TestAuthMiddlewareIdentifiesOnPublicRoutesWithoutRequiringIt(t *testing.T) {
	mgr := auth.NewManager("middleware-test-secret-key-1234567890")
	h := &Handler{auth: mgr}

	var seen string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("X-User-Email")
		w.WriteHeader(http.StatusOK)
	})
	handler := h.authMiddleware(next)

	cases := []struct {
		name       string
		authHeader string
		spoof      string
		wantEmail  string
		wantStatus int
	}{
		{"no token stays anonymous", "", "", "", http.StatusOK},
		{"valid token is identified", "Bearer " + mgr.IssueSession("nohe@example.com"), "", "nohe@example.com", http.StatusOK},
		{"garbage token stays anonymous", "Bearer not-a-real-token", "", "", http.StatusOK},
		{"spoofed header is dropped", "", "attacker@example.com", "", http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seen = "sentinel"
			req := httptest.NewRequest(http.MethodPost, "/api/waitlist", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			if tc.spoof != "" {
				req.Header.Set("X-User-Email", tc.spoof)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if seen != tc.wantEmail {
				t.Errorf("X-User-Email seen by handler = %q, want %q", seen, tc.wantEmail)
			}
		})
	}
}

// The same leniency must NOT leak onto protected routes.
func TestAuthMiddlewareStillRejectsProtectedRoutes(t *testing.T) {
	h := &Handler{auth: auth.NewManager("middleware-test-secret-key-1234567890")}
	handler := h.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler must not be reached without a session")
	}))

	for _, header := range []string{"", "Bearer not-a-real-token"} {
		req := httptest.NewRequest(http.MethodGet, "/api/usage", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Authorization=%q -> %d, want 401", header, rec.Code)
		}
	}
}
