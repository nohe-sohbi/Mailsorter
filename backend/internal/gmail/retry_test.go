package gmail

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"google.golang.org/api/googleapi"
)

func TestShouldRetry(t *testing.T) {
	cases := []struct {
		name           string
		err            error
		wantRetry      bool
		wantRetryAfter time.Duration
	}{
		{"nil", nil, false, 0},
		{"429", &googleapi.Error{Code: 429}, true, 0},
		{"429 with retry-after", &googleapi.Error{Code: 429, Header: http.Header{"Retry-After": {"3"}}}, true, 3 * time.Second},
		{"500", &googleapi.Error{Code: 500}, true, 0},
		{"503", &googleapi.Error{Code: 503}, true, 0},
		{"400", &googleapi.Error{Code: 400}, false, 0},
		{"401", &googleapi.Error{Code: 401}, false, 0},
		{"403", &googleapi.Error{Code: 403}, false, 0},
		{"404", &googleapi.Error{Code: 404}, false, 0},
		{"wrapped 429", fmt.Errorf("modify failed: %w", &googleapi.Error{Code: 429}), true, 0},
		{"network error", errors.New("connection reset by peer"), true, 0},
		{"context canceled", context.Canceled, false, 0},
		{"context deadline", context.DeadlineExceeded, false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotRetry, gotAfter := shouldRetry(tc.err)
			if gotRetry != tc.wantRetry {
				t.Errorf("shouldRetry(%v) retryable = %v, want %v", tc.err, gotRetry, tc.wantRetry)
			}
			if gotAfter != tc.wantRetryAfter {
				t.Errorf("shouldRetry(%v) retryAfter = %v, want %v", tc.err, gotAfter, tc.wantRetryAfter)
			}
		})
	}
}

func TestBackoffBounds(t *testing.T) {
	c := retryConfig{baseDelay: 100 * time.Millisecond, maxDelay: 2 * time.Second}
	// Full jitter keeps each wait within [d/2, d] of the exponential target, and
	// never above maxDelay.
	for attempt := 0; attempt < 8; attempt++ {
		d := c.backoff(attempt, 0)
		if d < 0 {
			t.Fatalf("attempt %d: negative backoff %v", attempt, d)
		}
		if d > c.maxDelay {
			t.Errorf("attempt %d: backoff %v exceeds maxDelay %v", attempt, d, c.maxDelay)
		}
	}
	// A server-advised Retry-After is honored as a floor.
	if d := c.backoff(0, 5*time.Second); d != c.maxDelay {
		t.Errorf("retry-after above ceiling should clamp to maxDelay, got %v", d)
	}
}

func TestWithRetrySucceedsAfterTransient(t *testing.T) {
	c := retryConfig{maxRetries: 3, baseDelay: time.Millisecond, maxDelay: time.Millisecond, sleep: func(time.Duration) {}}
	calls := 0
	val, err := withRetry(c, func() (int, error) {
		calls++
		if calls < 3 {
			return 0, &googleapi.Error{Code: 503}
		}
		return 42, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != 42 {
		t.Errorf("value = %d, want 42", val)
	}
	if calls != 3 {
		t.Errorf("expected 3 attempts, got %d", calls)
	}
}

func TestWithRetryGivesUpAfterMax(t *testing.T) {
	c := retryConfig{maxRetries: 2, baseDelay: time.Millisecond, maxDelay: time.Millisecond, sleep: func(time.Duration) {}}
	calls := 0
	_, err := withRetry(c, func() (int, error) {
		calls++
		return 0, &googleapi.Error{Code: 429}
	})
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if calls != 3 { // 1 initial + 2 retries
		t.Errorf("expected 3 attempts, got %d", calls)
	}
}

func TestWithRetryDoesNotRetryPermanent(t *testing.T) {
	c := retryConfig{maxRetries: 3, baseDelay: time.Millisecond, maxDelay: time.Millisecond, sleep: func(time.Duration) {}}
	calls := 0
	_, err := withRetry(c, func() (int, error) {
		calls++
		return 0, &googleapi.Error{Code: 403}
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Errorf("permanent error must not be retried, got %d attempts", calls)
	}
}
