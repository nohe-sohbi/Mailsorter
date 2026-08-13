package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/snooze"
)

func TestResolveWakeAt(t *testing.T) {
	now := time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)

	t.Run("preset resolves", func(t *testing.T) {
		got, err := resolveWakeAt(snooze.PresetTomorrow, time.Time{}, now)
		if err != nil {
			t.Fatalf("resolveWakeAt(tomorrow) = %v", err)
		}
		want := time.Date(2026, 6, 18, 8, 0, 0, 0, time.UTC)
		if !got.Equal(want) {
			t.Errorf("resolveWakeAt(tomorrow) = %v, want %v", got, want)
		}
	})

	t.Run("explicit wins over preset", func(t *testing.T) {
		explicit := now.Add(90 * time.Minute)
		got, err := resolveWakeAt(snooze.PresetTomorrow, explicit, now)
		if err != nil {
			t.Fatalf("resolveWakeAt(explicit) = %v", err)
		}
		if !got.Equal(explicit) {
			t.Errorf("resolveWakeAt with an explicit time = %v, want %v", got, explicit)
		}
	})

	t.Run("unknown preset", func(t *testing.T) {
		if _, err := resolveWakeAt("jamais", time.Time{}, now); err == nil {
			t.Error("an unknown preset must be rejected")
		}
	})

	t.Run("explicit out of horizon", func(t *testing.T) {
		if _, err := resolveWakeAt("", now.AddDate(5, 0, 0), now); err == nil {
			t.Error("a wake time five years out must be rejected")
		}
	})

	t.Run("explicit in the past", func(t *testing.T) {
		if _, err := resolveWakeAt("", now.Add(-time.Hour), now); err == nil {
			t.Error("a wake time in the past must be rejected")
		}
	})
}

// The batch route must reject a bad selection before it ever reaches Gmail, and
// it must apply exactly the same wake-time rules as the single-message one.
func TestBatchSnoozeRejectsBadRequests(t *testing.T) {
	h := newTestHandler(t)

	cases := []struct {
		name string
		body string
		want int
	}{
		{"empty selection", `{"messageIds":[],"preset":"tomorrow"}`, http.StatusBadRequest},
		{"unknown preset", `{"messageIds":["a"],"preset":"jamais"}`, http.StatusBadRequest},
		{"past wake time", `{"messageIds":["a"],"wakeAt":"2020-01-01T00:00:00Z"}`, http.StatusBadRequest},
		{"beyond the horizon", `{"messageIds":["a"],"wakeAt":"2999-01-01T00:00:00Z"}`, http.StatusBadRequest},
		{"malformed body", `{`, http.StatusBadRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/emails/batch-snooze", strings.NewReader(tc.body))
			req.Header.Set("X-User-Email", "user@example.com")
			rec := httptest.NewRecorder()

			h.BatchSnooze(rec, req)

			if rec.Code != tc.want {
				t.Fatalf("BatchSnooze(%s) = %d, want %d (body %s)", tc.body, rec.Code, tc.want, rec.Body.String())
			}
			var env struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Error == "" {
				t.Errorf("BatchSnooze(%s) did not answer a JSON error envelope: %s", tc.body, rec.Body.String())
			}
		})
	}
}

// Over the batch limit the request must be refused outright rather than
// truncated: a caller that asked to snooze 500 emails and got 200 back would
// believe the other 300 were handled.
func TestBatchSnoozeRefusesOversizedSelection(t *testing.T) {
	h := newTestHandler(t)

	ids := make([]string, maxBatchActionSize+1)
	for i := range ids {
		ids[i] = "msg"
	}
	body, err := json.Marshal(map[string]interface{}{"messageIds": ids, "preset": snooze.PresetTomorrow})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/emails/batch-snooze", strings.NewReader(string(body)))
	req.Header.Set("X-User-Email", "user@example.com")
	rec := httptest.NewRecorder()

	h.BatchSnooze(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("BatchSnooze with %d ids = %d, want 400", len(ids), rec.Code)
	}
}

func TestBatchSnoozeRequiresASession(t *testing.T) {
	srv := newRoutedTestServer(t)

	res, err := http.Post(srv.URL+"/api/emails/batch-snooze", "application/json",
		strings.NewReader(`{"messageIds":["a"],"preset":"tomorrow"}`))
	if err != nil {
		t.Fatalf("POST batch-snooze: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("batch-snooze without a session = %d, want 401", res.StatusCode)
	}
}
