package ai

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// newTestClient returns a client pointed at srv with instant, deterministic
// backoff so retry logic can be exercised without real waits.
func newTestClient(url string) *MistralClient {
	c := NewMistralClient("test-key", "test-model")
	c.baseURL = url
	c.baseDelay = 0
	c.maxDelay = 0
	c.sleep = func(time.Duration) {}
	return c
}

const okBody = `{"choices":[{"message":{"content":"hello"}}]}`

func TestChatRetriesOn429ThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(okBody))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	c.SetMaxRetries(3)

	got, err := c.chat("ping")
	if err != nil {
		t.Fatalf("expected success after retries, got error: %v", err)
	}
	if got != "hello" {
		t.Errorf("content = %q, want %q", got, "hello")
	}
	if calls != 3 {
		t.Errorf("expected 3 attempts (429, 429, 200), got %d", calls)
	}
}

func TestChatExhaustsRetriesOn500(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	c.SetMaxRetries(2)

	if _, err := c.chat("ping"); err == nil {
		t.Fatal("expected an error after exhausting retries")
	}
	// 1 initial attempt + 2 retries = 3 calls.
	if calls != 3 {
		t.Errorf("expected 3 attempts, got %d", calls)
	}
}

func TestChatDoesNotRetryClientError(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	c.SetMaxRetries(5)

	if _, err := c.chat("ping"); err == nil {
		t.Fatal("expected an error on a 400 response")
	}
	if calls != 1 {
		t.Errorf("a 4xx must not be retried, expected 1 attempt, got %d", calls)
	}
}

func TestParseRetryAfter(t *testing.T) {
	cases := map[string]time.Duration{
		"":             0,
		"2":            2 * time.Second,
		"0":            0,
		"-5":           0,
		"  3  ":        3 * time.Second,
		"not-a-number": 0,
	}
	for in, want := range cases {
		if got := parseRetryAfter(in); got != want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestBackoffHonorsRetryAfterAndCap(t *testing.T) {
	c := NewMistralClient("k", "m")
	c.baseDelay = 1 * time.Second
	c.maxDelay = 8 * time.Second

	// Retry-After (30s) exceeds the computed backoff but is capped at maxDelay.
	if d := c.backoff(0, 30*time.Second); d != 8*time.Second {
		t.Errorf("backoff should be capped at maxDelay (8s), got %v", d)
	}
	// With jitter, attempt 1 wait stays within (baseDelay*2^1)/2 .. maxDelay.
	d := c.backoff(1, 0)
	if d < 1*time.Second || d > 8*time.Second {
		t.Errorf("jittered backoff out of expected range: %v", d)
	}
}
