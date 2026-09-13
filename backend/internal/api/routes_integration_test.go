package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/auth"
	"github.com/nohe-sohbi/mailsorter/backend/internal/crypto"
	"github.com/nohe-sohbi/mailsorter/backend/internal/database"
	"github.com/nohe-sohbi/mailsorter/backend/internal/gmail"
	"github.com/nohe-sohbi/mailsorter/backend/internal/metrics"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// newRoutedTestServer wires the REAL router (full middleware chain + routes)
// over an httptest server, so these tests exercise routing, auth gating and the
// observability endpoints end-to-end, not just isolated functions. The Mongo
// client points at a dead address on purpose so the /health datastore ping
// fails fast, letting us assert the degraded (503) path for real.
func newRoutedTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(newTestHandler(t).SetupRoutes())
	t.Cleanup(srv.Close)
	return srv
}

// cancelledContext is an already-cancelled request context. Handlers derive
// their own context from r.Context(), so a test that only cares about what
// happens BEFORE the datastore can attach this and have the Mongo call fail
// instantly instead of waiting out the handler's whole timeout budget.
func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// newTestHandler builds the same Handler newRoutedTestServer mounts, for tests
// that call a handler directly instead of going over HTTP. Mongo points at a
// dead address on purpose (see newRoutedTestServer). It deliberately does NOT
// go through NewHandler, which would start the four background loops.
func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	cli, err := mongo.Connect(context.Background(), options.Client().ApplyURI("mongodb://127.0.0.1:1"))
	if err != nil {
		t.Fatalf("connect (no dial yet): %v", err)
	}
	return &Handler{
		db:           &database.Database{Client: cli, DB: cli.Database("mailsorter")},
		gmailService: gmail.NewService("", "", ""),
		encryptor:    crypto.NewEncryptor(testSecret),
		auth:         auth.NewManager(testSecret),
		metrics:      metrics.New(),
		startedAt:    time.Now(),
	}
}

// testSecret stands in for ENCRYPTION_KEY. Both the session signer and the
// at-rest encryptor derive from it in production, so the harness wires it into
// both: a Handler missing its encryptor would panic on the first token write
// rather than fail a test honestly.
const testSecret = "integration-test-secret-key-1234567890"

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

// The Gmail credentials are a single instance-wide OAuth app. When they were
// editable over HTTP under the public /api/config/ prefix, any anonymous
// visitor could rewrite them and hot-reload the client, hijacking the login
// flow for every user. They now come from the environment and must have no
// HTTP surface: both verbs have to be gone from the router, not merely gated.
func TestGmailCredentialsHaveNoHTTPSurface(t *testing.T) {
	srv := newRoutedTestServer(t)

	res, err := http.Get(srv.URL + "/api/config/gmail")
	if err != nil {
		t.Fatalf("GET /api/config/gmail: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("GET /api/config/gmail = %d, want 404 (credentials must not be readable)", res.StatusCode)
	}

	res, err = http.Post(srv.URL+"/api/config/gmail", "application/json",
		strings.NewReader(`{"clientId":"attacker","clientSecret":"attacker","redirectUrl":"https://evil.example/callback"}`))
	if err != nil {
		t.Fatalf("POST /api/config/gmail: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("POST /api/config/gmail = %d, want 404 (credentials must not be writable)", res.StatusCode)
	}
}

// A pre-launch waitlist measures intent from people who have no account yet,
// so the route must answer logged-out callers. Posting an invalid address
// proves both at once: a 400 (not a 401) means the request reached the handler
// and was rejected on its merits, without a session and without touching Mongo.
func TestWaitlistIsReachableWithoutASession(t *testing.T) {
	srv := newRoutedTestServer(t)

	res, err := http.Post(srv.URL+"/api/waitlist", "application/json",
		strings.NewReader(`{"email":"not-an-email","source":"pricing"}`))
	if err != nil {
		t.Fatalf("POST /api/waitlist: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		t.Fatal("POST /api/waitlist = 401: cold traffic can no longer sign up")
	}
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("POST /api/waitlist with a bad address = %d, want 400", res.StatusCode)
	}
}

// The boot probe stays public: the SPA calls it before any login to decide
// whether to show the setup instructions and whether Pro can be bought yet. It
// must expose those two booleans and nothing else, since anyone can read it.
func TestConfigStatusIsPublicAndMinimal(t *testing.T) {
	srv := newRoutedTestServer(t)

	res, err := http.Get(srv.URL + "/api/config/status")
	if err != nil {
		t.Fatalf("GET /api/config/status: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("/api/config/status = %d, want 200 without a session token", res.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode /api/config/status: %v", err)
	}
	if len(body) != 2 {
		t.Errorf("status payload = %#v, want only isConfigured and billingOn", body)
	}
	// The test server has neither credentials nor Stripe, so both are false.
	for _, key := range []string{"isConfigured", "billingOn"} {
		if v, ok := body[key].(bool); !ok || v {
			t.Errorf("%s = %#v, want false on a bare instance", key, body[key])
		}
	}
}

// After mixing a 2xx, a 5xx and a 4xx through the chain, the meter must reflect
// all three status classes, proving the metrics middleware sees final codes.
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
