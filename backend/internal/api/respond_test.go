package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
