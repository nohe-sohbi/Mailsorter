package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/imap"
	"go.mongodb.org/mongo-driver/mongo"
)

func TestDecodeJSONValid(t *testing.T) {
	body := `{"messageId":"abc","action":"archive"}`
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	w := httptest.NewRecorder()

	var dst struct {
		MessageID string `json:"messageId"`
		Action    string `json:"action"`
	}
	if !decodeJSON(w, r, &dst) {
		t.Fatalf("decodeJSON returned false for valid body (status %d)", w.Code)
	}
	if dst.MessageID != "abc" || dst.Action != "archive" {
		t.Fatalf("decoded unexpected value: %+v", dst)
	}
}

func TestDecodeJSONInvalid(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("not json"))
	w := httptest.NewRecorder()

	var dst map[string]any
	if decodeJSON(w, r, &dst) {
		t.Fatal("decodeJSON returned true for malformed body")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	var env map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("error response is not JSON: %v", err)
	}
	if env["error"] == nil {
		t.Fatalf("error envelope missing 'error' field: %s", w.Body.String())
	}
}

func TestDecodeJSONTooLarge(t *testing.T) {
	// A body just over the 1 MiB cap must be rejected with 413, not read in full.
	big := strings.Repeat("a", maxRequestBody+1024)
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"v":"`+big+`"}`))
	w := httptest.NewRecorder()

	var dst map[string]any
	if decodeJSON(w, r, &dst) {
		t.Fatal("decodeJSON returned true for oversized body")
	}
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestWriteAuthError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"reauth required", errReauthRequired, http.StatusUnauthorized},
		{"wrapped reauth", fmt.Errorf("get token: %w", errReauthRequired), http.StatusUnauthorized},
		{"user gone", mongo.ErrNoDocuments, http.StatusNotFound},
		{"other", errors.New("boom"), http.StatusInternalServerError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeAuthError(w, c.err)
			if w.Code != c.want {
				t.Fatalf("status = %d, want %d", w.Code, c.want)
			}
			var env map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
				t.Fatalf("not JSON: %v", err)
			}
			if env["error"] == nil {
				t.Fatalf("missing error field: %s", w.Body.String())
			}
		})
	}
}

// A mailbox that cannot be opened says why, in French, and with a status the
// SPA does not take for a dead Mailsorter session (a 401 signs out): a
// password the provider refuses is the connect screen's sentence and 428 (to
// be connected again), a mailbox that did not answer is 502, anything else
// a French 500.
func TestWriteAuthErrorWordsAMailboxThatCannotBeOpened(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code int
		want string
	}{
		{"password refused", fmt.Errorf("open: %w: NO [AUTHENTICATIONFAILED]", imap.ErrAuth), http.StatusPreconditionRequired, mailboxRejectedMessage},
		{"did not answer", fmt.Errorf("open: %w: login: EOF", imap.ErrUnreachable), http.StatusBadGateway, mailboxNoAnswerMessage},
		{"no mailbox", errNoMailboxConnected, http.StatusPreconditionRequired, "Aucune boîte mail n'est branchée à ce compte."},
		{"anything else", errors.New("boom"), http.StatusInternalServerError, "Impossible d'ouvrir votre boîte pour le moment, réessayez dans un instant."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeAuthError(w, c.err)
			var env map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
				t.Fatalf("not JSON: %v", err)
			}
			if w.Code != c.code || env["error"] != c.want {
				t.Errorf("writeAuthError(%v) = %d %q, want %d %q", c.err, w.Code, env["error"], c.code, c.want)
			}
		})
	}
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusConflict, "nope")

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusConflict)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	var env struct {
		Error  string `json:"error"`
		Status int    `json:"status"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if env.Error != "nope" || env.Status != http.StatusConflict {
		t.Fatalf("unexpected envelope: %+v", env)
	}
}
