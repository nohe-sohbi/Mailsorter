package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/account"
)

// Every user-owned dataset must resolve to a real collection. This is the
// invariant that keeps the RGPD export and the account erasure honest: a
// dataset added to the catalog without a mapping here would be silently
// skipped by BOTH, so the data would be neither disclosed nor deleted.
func TestEveryDatasetMapsToACollection(t *testing.T) {
	h := newTestHandler(t)

	seen := map[string]bool{}
	for _, ds := range account.Datasets() {
		coll := h.datasetCollection(ds)
		if coll == nil {
			t.Errorf("dataset %q has no collection: it would be exported and erased as nothing", ds)
			continue
		}
		if seen[coll.Name()] {
			t.Errorf("dataset %q reuses collection %q already claimed by another dataset", ds, coll.Name())
		}
		seen[coll.Name()] = true
	}
}

// Saved searches are user-owned data, so they have to be in the catalog: the
// export must hand them back and the erasure must remove them.
func TestSavedSearchesAreInTheGDPRCatalog(t *testing.T) {
	for _, ds := range account.Datasets() {
		if ds == account.DatasetSavedSearches {
			return
		}
	}
	t.Error("savedSearches is missing from account.Datasets(): it would never be exported nor erased")
}

func TestSavedSearchRoutesExistAndAreGated(t *testing.T) {
	srv := newRoutedTestServer(t)

	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/searches"},
		{http.MethodPost, "/api/searches"},
		{http.MethodPost, "/api/searches/6650000000000000000000a1/use"},
		{http.MethodDelete, "/api/searches/6650000000000000000000a1"},
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
			t.Errorf("%s %s = %d, want 401", tc.method, tc.path, res.StatusCode)
		}
	}
}

// A bad payload must be refused on its own terms, before any datastore call,
// with the pure package's wording.
func TestCreateSavedSearchValidatesBeforeStoring(t *testing.T) {
	h := newTestHandler(t)

	cases := []struct{ name, body string }{
		{"no name", `{"name":"  ","query":"in:inbox"}`},
		{"no query", `{"name":"Vide","query":""}`},
		{"multiline query", `{"name":"Collé","query":"in:inbox\nfrom:x"}`},
		{"malformed json", `{`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/searches", strings.NewReader(tc.body))
			req.Header.Set("X-User-Email", "user@example.com")
			rec := httptest.NewRecorder()

			h.CreateSavedSearch(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("CreateSavedSearch(%s) = %d, want 400 (body %s)", tc.body, rec.Code, rec.Body.String())
			}
			var env struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Error == "" {
				t.Errorf("CreateSavedSearch(%s) did not answer a JSON error envelope: %s", tc.body, rec.Body.String())
			}
		})
	}
}
