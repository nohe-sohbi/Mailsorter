package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
	"github.com/nohe-sohbi/mailsorter/backend/internal/smtpsend"
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

// What "Envoyer un test" says when the recap did not leave. On IMAP every
// answer is about the mailbox's sending server: the Gmail sentence there sent
// people to a "Compte Gmail" card their screen does not have. And none of
// them is a 401, on which the SPA signs the person out before they read it.
func TestWriteDigestSendErrorAnswersInTheMailboxsWords(t *testing.T) {
	throughBox := func(err error) error { return fmt.Errorf("%w: %w", errSendThroughBox, err) }
	const gmail = "Envoi impossible. Reconnectez Gmail depuis la carte « Compte Gmail » pour réautoriser l'envoi."

	cases := []struct {
		name   string
		err    error
		status int
		want   string
	}{
		{"IMAP, no known sending server", throughBox(provider.ErrNoSendingServer), http.StatusUnprocessableEntity,
			"Mailsorter ne connaît pas le serveur d'envoi de cette boîte : le récap ne peut pas en partir."},
		{"IMAP, app password refused", throughBox(fmt.Errorf("%w: 535", smtpsend.ErrAuth)), http.StatusBadGateway,
			"Le serveur d'envoi de votre boîte a refusé le mot de passe d'application."},
		{"IMAP, server unreachable", throughBox(fmt.Errorf("%w: dial", smtpsend.ErrUnreachable)), http.StatusBadGateway,
			"Le serveur d'envoi de votre boîte ne répond pas. Réessayez dans quelques minutes."},
		{"IMAP, anything else", throughBox(errors.New("smtpsend: 554 rejected")), http.StatusBadGateway,
			"Le récap n'a pas pu partir du serveur d'envoi de votre boîte. Réessayez dans quelques minutes."},
		{"Gmail API, send refused", errors.New("envoi du digest impossible: 403"), http.StatusBadGateway, gmail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeDigestSendError(rec, tc.err)

			var body struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("writeDigestSendError(%v) body is not JSON: %v: %s", tc.err, err, rec.Body.String())
			}
			if rec.Code != tc.status || body.Error != tc.want {
				t.Errorf("writeDigestSendError(%v) = %d %q, want %d %q", tc.err, rec.Code, body.Error, tc.status, tc.want)
			}
		})
	}
}
