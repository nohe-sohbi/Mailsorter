package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/auth"
	"github.com/nohe-sohbi/mailsorter/backend/internal/database"
	"github.com/nohe-sohbi/mailsorter/backend/internal/metrics"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// newRoutedTestServer wires the REAL router (full middleware chain + routes)
// over an httptest server, so these tests exercise routing, auth gating and the
// observability endpoints end-to-end — not just isolated functions. The Mongo
// client points at a dead address on purpose so the /health datastore ping
// fails fast, letting us assert the degraded (503) path for real.
func newRoutedTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	cli, err := mongo.Connect(context.Background(), options.Client().ApplyURI("mongodb://127.0.0.1:1"))
	if err != nil {
		t.Fatalf("connect (no dial yet): %v", err)
	}
	h := &Handler{
		db:        &database.Database{Client: cli},
		auth:      auth.NewManager("integration-test-secret-key-1234567890"),
		metrics:   metrics.New(),
		startedAt: time.Now(),
	}
	srv := httptest.NewServer(h.SetupRoutes())
	t.Cleanup(srv.Close)
	return srv
}

func TestMetricsEndpointLive(t *testing.T) {
	srv := newRoutedTestServer(t)

	// The meter reports requests that COMPLETED before it (a request can't count
	// itself: the middleware records after the handler renders). So warm up with
	// one request, then assert the next snapshot reflects it.
	if r, err := http.Get(srv.URL + "/metrics"); err == nil {
		r.Body.Close()
	}

	res, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("/metrics status = %d, want 200 (must be reachable without auth)", res.StatusCode)
	}

	var body struct {
		Version string           `json:"version"`
		Metrics metrics.Snapshot `json:"metrics"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode /metrics: %v", err)
	}
	if body.Metrics.TotalRequests < 1 || body.Metrics.ByMethod["GET"] < 1 {
		t.Errorf("metrics did not record the prior live request: %#v", body.Metrics)
	}
}

func TestHealthDegradedWhenDatastoreDown(t *testing.T) {
	srv := newRoutedTestServer(t)

	res, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("/health status = %d, want 503 when Mongo is unreachable", res.StatusCode)
	}

	var body struct {
		Status string          `json:"status"`
		Checks map[string]bool `json:"checks"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode /health: %v", err)
	}
	if body.Status != "degraded" || body.Checks["mongo"] {
		t.Errorf("expected degraded health with mongo=false, got %#v", body)
	}
}

func TestProtectedRouteRejectsMissingSession(t *testing.T) {
	srv := newRoutedTestServer(t)

	res, err := http.Get(srv.URL + "/api/usage") // protected route, no token
	if err != nil {
		t.Fatalf("GET /api/usage: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("protected route without a session token = %d, want 401", res.StatusCode)
	}
}

// After mixing a 2xx, a 5xx and a 4xx through the chain, the meter must reflect
// all three status classes — proving the metrics middleware sees final codes.
func TestMetricsAggregatesStatusClassesLive(t *testing.T) {
	srv := newRoutedTestServer(t)

	http.Get(srv.URL + "/metrics")   // 2xx
	http.Get(srv.URL + "/health")    // 5xx (datastore down)
	http.Get(srv.URL + "/api/usage") // 4xx (no auth)

	res, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer res.Body.Close()
	var body struct {
		Metrics metrics.Snapshot `json:"metrics"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, class := range []string{"2xx", "4xx", "5xx"} {
		if body.Metrics.ByStatusClass[class] < 1 {
			t.Errorf("expected at least one %s response recorded, got %#v", class, body.Metrics.ByStatusClass)
		}
	}
}
