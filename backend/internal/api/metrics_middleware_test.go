package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/metrics"
)

// The metrics middleware must record the FINAL status a handler writes, not the
// 200 default, and must count the request against the right method.
func TestMetricsMiddlewareRecordsStatus(t *testing.T) {
	h := &Handler{metrics: metrics.New()}

	mw := h.metricsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot) // 418 -> "4xx"
	}))

	req := httptest.NewRequest("POST", "/api/whatever", nil)
	mw.ServeHTTP(httptest.NewRecorder(), req)

	snap := h.metrics.Snapshot()
	if snap.TotalRequests != 1 {
		t.Fatalf("total = %d, want 1", snap.TotalRequests)
	}
	if snap.ByMethod["POST"] != 1 {
		t.Errorf("byMethod[POST] = %d, want 1", snap.ByMethod["POST"])
	}
	if snap.ByStatusClass["4xx"] != 1 {
		t.Errorf("byStatusClass[4xx] = %d, want 1 (status passed through recorder)", snap.ByStatusClass["4xx"])
	}
}

func TestMetricsAndHealthArePublic(t *testing.T) {
	for _, p := range []string{"/health", "/metrics"} {
		if !isPublicPath(p) {
			t.Errorf("%q should be reachable without a session token", p)
		}
	}
}
