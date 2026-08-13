package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The digest recap and the test send are per-account data: both must be gated,
// and both must exist as routes (a 404 here would mean the Settings buttons
// point at nothing).
func TestDigestRoutesExistAndAreGated(t *testing.T) {
	srv := newRoutedTestServer(t)

	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/stats/digest"},
		{http.MethodPost, "/api/account/digest/test"},
	}
	for _, tc := range cases {
		req, err := http.NewRequest(tc.method, srv.URL+tc.path, nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401", tc.method, tc.path, res.StatusCode)
		}
	}
}

// A test send that cannot reach the datastore must report a failure, never a
// cheerful "sent": the whole point of the button is to tell the user the truth
// about whether delivery works.
func TestSendTestDigestReportsFailure(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/account/digest/test", strings.NewReader(""))
	req.Header.Set("X-User-Email", "user@example.com")
	req = req.WithContext(cancelledContext())
	rec := httptest.NewRecorder()

	h.SendTestDigest(rec, req)

	if rec.Code == http.StatusOK {
		t.Errorf("SendTestDigest with no reachable datastore = 200: %s", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}
