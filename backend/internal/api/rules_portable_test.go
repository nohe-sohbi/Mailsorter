package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/rules"
)

// Every ruleset-wide route must be gated, and must be a route at all: a typo in
// the path would surface as a 404 here rather than as a silent no-op in the UI.
func TestRulesetRoutesExistAndAreGated(t *testing.T) {
	srv := newRoutedTestServer(t)

	cases := []struct{ method, path string }{
		{http.MethodPut, "/api/rules/reorder"},
		{http.MethodGet, "/api/rules/export"},
		{http.MethodPost, "/api/rules/import"},
		{http.MethodPost, "/api/rules/6650000000000000000000a1/duplicate"},
	}

	for _, tc := range cases {
		req, err := http.NewRequest(tc.method, srv.URL+tc.path, strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401 (route must exist and require a session)", tc.method, tc.path, res.StatusCode)
		}
	}
}

// The fixed ruleset paths must keep winning over /api/rules/{id}, or "reorder"
// would be read as a rule id and the request would edit nothing.
func TestRulesetRoutesAreNotSwallowedByTheIDPattern(t *testing.T) {
	h := newTestHandler(t)
	router := h.SetupRoutes()

	// DELETE only exists on /api/rules/{id}: if "export" were matched as an id,
	// this would answer 401 (gated handler) instead of 405/404.
	req := httptest.NewRequest(http.MethodDelete, "/api/rules/export", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Errorf("DELETE /api/rules/export = %d: the fixed path must not be treated as a rule id", rec.Code)
	}
}

// Validation must happen before any datastore call, so a bad payload is cheap
// and the message says what is wrong with the FILE, not with the server.
func TestImportRulesRejectsBadDocumentsBeforeTouchingMongo(t *testing.T) {
	h := newTestHandler(t)

	valid := rules.PortableRule{
		Name:       "OK",
		Enabled:    true,
		Conditions: []models.RuleCondition{{Field: rules.FieldFrom, Operator: rules.OpContains, Value: "x"}},
		Actions:    []models.RuleAction{{Type: rules.ActionArchive}},
	}

	cases := []struct {
		name string
		body string
	}{
		{"not an export at all", `{"rules":[]}`},
		{"empty ruleset", `{"version":1,"rules":[]}`},
		{"future format", `{"version":99,"rules":[{"name":"x"}]}`},
		{"malformed json", `{`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/rules/import", strings.NewReader(tc.body))
			req.Header.Set("X-User-Email", "user@example.com")
			rec := httptest.NewRecorder()

			h.ImportRules(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("ImportRules(%s) = %d, want 400 (body %s)", tc.body, rec.Code, rec.Body.String())
			}
			var env struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Error == "" {
				t.Errorf("ImportRules(%s) did not answer a JSON error envelope: %s", tc.body, rec.Body.String())
			}
		})
	}

	// A well-formed document gets past validation (and only then meets the dead
	// datastore), which is what proves the rejections above are about content.
	body, err := json.Marshal(rules.Export{Version: rules.ExportVersion, Rules: []rules.PortableRule{valid}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/rules/import", strings.NewReader(string(body)))
	req.Header.Set("X-User-Email", "user@example.com")
	req = req.WithContext(cancelledContext())
	rec := httptest.NewRecorder()

	h.ImportRules(rec, req)

	if rec.Code == http.StatusBadRequest {
		t.Errorf("a valid export was rejected as malformed: %s", rec.Body.String())
	}
}

func TestReorderRulesRejectsAnEmptyOrder(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPut, "/api/rules/reorder", strings.NewReader(`{"ids":[]}`))
	req.Header.Set("X-User-Email", "user@example.com")
	rec := httptest.NewRecorder()

	h.ReorderRules(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("reorder with no ids = %d, want 400", rec.Code)
	}
}
