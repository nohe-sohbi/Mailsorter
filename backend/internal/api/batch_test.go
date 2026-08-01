package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/activity"
	"github.com/nohe-sohbi/mailsorter/backend/internal/auth"
)

// The batch endpoints validate the request fully before they touch Mongo or
// Gmail, so these run against the real router with a real session token and a
// dead datastore: anything that reaches the database would surface as a 5xx and
// fail the assertion.
// Must stay in sync with the secret newRoutedTestServer hands to its Handler,
// or every token issued here verifies as garbage and the tests assert 401s
// instead of the validation they are actually about.
const batchTestSecret = "integration-test-secret-key-1234567890"

func newTestAuth(t *testing.T) *auth.Manager {
	t.Helper()
	return auth.NewManager(batchTestSecret)
}

func postBatch(t *testing.T, srvURL, path, token string, body interface{}) *http.Response {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srvURL+path, bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func TestBatchActionRequiresAuth(t *testing.T) {
	srv := newRoutedTestServer(t)

	for _, path := range []string{"/api/emails/batch-action", "/api/emails/batch-undo"} {
		res := postBatch(t, srv.URL, path, "", BatchActionRequest{
			MessageIDs: []string{"abc"},
			Action:     "archive",
		})
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("POST %s unauthenticated = %d, want 401", path, res.StatusCode)
		}
	}
}

func TestBatchActionRejectsBadRequests(t *testing.T) {
	srv := newRoutedTestServer(t)
	token := newTestAuth(t).IssueSession("nohe@example.com")

	tooMany := make([]string, maxBatchActionSize+1)
	for i := range tooMany {
		tooMany[i] = "m"
	}

	cases := []struct {
		name string
		body BatchActionRequest
	}{
		{"empty selection", BatchActionRequest{Action: "archive"}},
		{"unknown action", BatchActionRequest{MessageIDs: []string{"a"}, Action: "detonate"}},
		{"empty action", BatchActionRequest{MessageIDs: []string{"a"}}},
		{"label without a name", BatchActionRequest{MessageIDs: []string{"a"}, Action: "label"}},
		{"over the size cap", BatchActionRequest{MessageIDs: tooMany, Action: "archive"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := postBatch(t, srv.URL, "/api/emails/batch-action", token, tc.body)
			if res.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 (the request must be rejected before any Gmail call)", res.StatusCode)
			}
		})
	}
}

func TestBatchUndoOnlyAcceptsReversibleActions(t *testing.T) {
	srv := newRoutedTestServer(t)
	token := newTestAuth(t).IssueSession("nohe@example.com")

	// Labelling and read-marking have no clean inverse the client can replay, so
	// offering to undo them would be a lie.
	for _, action := range []string{"label", "read", "unread", "star", "unstar", ""} {
		res := postBatch(t, srv.URL, "/api/emails/batch-undo", token, BatchActionRequest{
			MessageIDs: []string{"a"},
			Action:     action,
			LabelName:  "Factures",
		})
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("batch-undo of %q = %d, want 400", action, res.StatusCode)
		}
	}
}

// batchInverse is what the client uses to decide whether to offer "Annuler" on a
// batch, so every inverse it claims must be one the undo path can actually
// replay through applyInverseAction, and must agree with the per-entry history
// undo. A mismatch would surface as an undo button that silently does nothing.
func TestBatchInverseMatchesLedgerInverse(t *testing.T) {
	if len(batchInverse) == 0 {
		t.Fatal("batchInverse must not be empty")
	}
	for action, inverse := range batchInverse {
		ledgerInverse, ok := activity.Inverse(action)
		if !ok {
			t.Errorf("batch offers to undo %q but the ledger has no inverse for it", action)
			continue
		}
		if ledgerInverse != inverse {
			t.Errorf("inverse of %q: batch says %q, ledger says %q", action, inverse, ledgerInverse)
		}
		// applyInverseAction is the single place a reversal is turned into a Gmail
		// mutation; an inverse it does not know is a silent no-op.
		if !isHandledInverse(inverse) {
			t.Errorf("applyInverseAction does not handle inverse %q", inverse)
		}
	}
}

// isHandledInverse mirrors the switch in applyInverseAction. Kept next to the
// test that depends on it so adding a case there without adding it here is loud.
func isHandledInverse(inverse string) bool {
	switch inverse {
	case "unarchive", "untrash", "unread":
		return true
	}
	return false
}

func TestBatchActionRejectsMalformedJSON(t *testing.T) {
	srv := newRoutedTestServer(t)
	token := newTestAuth(t).IssueSession("nohe@example.com")

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/emails/batch-action",
		strings.NewReader("{not json"))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("malformed JSON = %d, want 400", res.StatusCode)
	}
}
