package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/auth"
)

// testSessionToken mints a session for the identity the routed test server
// trusts, so a request gets past authMiddleware and reaches the handler (which
// then fails on the deliberately dead Mongo/Gmail, exercising the error paths
// for real).
func testSessionToken(t *testing.T) string {
	t.Helper()
	return auth.NewManager("integration-test-secret-key-1234567890").IssueSession("user@example.com")
}

// Every /api failure must be a JSON envelope, never bare text.
//
// The SPA parses errors in one place (apiError in services/api.js). When a
// handler answered through http.Error the body arrived as text/plain and the
// real reason was replaced by a generic "Réessayez", so the user was told
// nothing while the server had said exactly what was wrong. This walks the
// three layers that can reject a request (middleware, routing, handler) and
// holds all of them to the same envelope.
func TestAPIErrorsAreAlwaysJSON(t *testing.T) {
	srv := newRoutedTestServer(t)
	token := testSessionToken(t)

	cases := []struct {
		name   string
		method string
		path   string
		body   string
		auth   bool
		want   int
	}{
		// Middleware: no session at all.
		{"missing session", http.MethodGet, "/api/usage", "", false, http.StatusUnauthorized},
		// Handler: valid session, malformed payload.
		{"bad json", http.MethodPost, "/api/emails/action", "{", true, http.StatusBadRequest},
		// Handler: valid session, payload rejected on its merits.
		{"empty batch", http.MethodPost, "/api/emails/batch-action", `{"messageIds":[],"action":"archive"}`, true, http.StatusBadRequest},
		{"bad rule", http.MethodPost, "/api/rules", `{"name":"","conditions":[]}`, true, http.StatusBadRequest},
		{"bad snooze", http.MethodPost, "/api/emails/snooze", `{"messageId":"abc","preset":"jamais"}`, true, http.StatusBadRequest},
		{"bad id", http.MethodPost, "/api/snoozes/not-an-objectid/wake", "", true, http.StatusBadRequest},
		// Handler: valid session and payload, but the datastore is down.
		{"datastore down", http.MethodGet, "/api/rules", "", true, http.StatusInternalServerError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, srv.URL+tc.path, strings.NewReader(tc.body))
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.Header.Set("Content-Type", "application/json")
			if tc.auth {
				req.Header.Set("Authorization", "Bearer "+token)
			}

			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("%s %s: %v", tc.method, tc.path, err)
			}
			defer res.Body.Close()

			if res.StatusCode != tc.want {
				raw, _ := io.ReadAll(res.Body)
				t.Fatalf("%s %s = %d, want %d (body %q)", tc.method, tc.path, res.StatusCode, tc.want, raw)
			}
			if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("%s %s Content-Type = %q, want application/json", tc.method, tc.path, ct)
			}

			var env struct {
				Error  string `json:"error"`
				Status int    `json:"status"`
			}
			if err := json.NewDecoder(res.Body).Decode(&env); err != nil {
				t.Fatalf("%s %s body is not a JSON error envelope: %v", tc.method, tc.path, err)
			}
			if strings.TrimSpace(env.Error) == "" {
				t.Errorf("%s %s error envelope carries no message: %#v", tc.method, tc.path, env)
			}
			if env.Status != tc.want {
				t.Errorf("%s %s envelope status = %d, want %d", tc.method, tc.path, env.Status, tc.want)
			}
		})
	}
}

// A client that hangs up must not leave the server working on its behalf.
//
// Handlers used to open their own context.Background(), so a cancelled request
// kept querying Mongo and mutating Gmail (burning the user's quota) with nobody
// left to answer. Every request-scoped context now descends from r.Context().
//
// The assertion is the timing: GetRules budgets 10s: run it with an already
// cancelled request and it must give up at once. Under context.Background() the
// cancellation is invisible to the Mongo call and the handler sits out its whole
// budget instead.
func TestHandlerStopsWhenClientDisconnects(t *testing.T) {
	h := newTestHandler(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/rules", nil).WithContext(ctx)
	req.Header.Set("X-User-Email", "user@example.com")
	rec := httptest.NewRecorder()

	start := time.Now()
	h.GetRules(rec, req)
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Errorf("handler kept working %s after the client hung up; it must inherit r.Context()", elapsed.Round(time.Millisecond))
	}
	if rec.Code == http.StatusOK {
		t.Errorf("a cancelled request should not report success, got %d", rec.Code)
	}
}
