package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
)

// A well-formed ObjectID, so each case below fails on the thing it is about.
const validSuggestionID = "662f1f77bcf86cd799439011"

func TestRejectBatchRequiresAuth(t *testing.T) {
	srv := newRoutedTestServer(t)

	res := postBatch(t, srv.URL, "/api/ai/reject-batch", "", models.RejectBatchRequest{
		SuggestionIDs: []string{validSuggestionID},
	})
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("POST /api/ai/reject-batch unauthenticated = %d, want 401", res.StatusCode)
	}
}

// Everything is validated before the datastore, which the harness points at a
// dead address: a case that reached Mongo would come back as a 5xx, not a 400.
func TestRejectBatchRejectsBadRequests(t *testing.T) {
	srv := newRoutedTestServer(t)
	token := newTestAuth(t).IssueSession("nohe@example.com")

	tooMany := make([]string, maxRejectBatchSize+1)
	for i := range tooMany {
		tooMany[i] = validSuggestionID
	}

	cases := []struct {
		name string
		ids  []string
	}{
		{"no ids", nil},
		{"empty list", []string{}},
		{"a malformed id among valid ones", []string{validSuggestionID, "not-an-object-id"}},
		{"over the size cap", tooMany},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := postBatch(t, srv.URL, "/api/ai/reject-batch", token, models.RejectBatchRequest{SuggestionIDs: tc.ids})
			if res.StatusCode != http.StatusBadRequest {
				t.Errorf("POST /api/ai/reject-batch with %s = %d, want 400", tc.name, res.StatusCode)
			}
		})
	}
}

// A write that did not happen must be reported as such. A 200 here would be the
// original bug in a new place: rows hidden on screen that the next refresh
// brings back.
func TestRejectBatchReportsADatastoreFailure(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/ai/reject-batch",
		strings.NewReader(`{"suggestionIds":["`+validSuggestionID+`"]}`))
	req.Header.Set("X-User-Email", "user@example.com")
	req = req.WithContext(cancelledContext())
	rec := httptest.NewRecorder()

	h.RejectBatch(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("RejectBatch with no reachable datastore = %d, want 500: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Error == "" {
		t.Errorf("RejectBatch did not answer a JSON error envelope: %s", rec.Body.String())
	}
}
