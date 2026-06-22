package gmail

import (
	"context"
	"errors"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"google.golang.org/api/googleapi"
)

// Gmail is the second flakiest dependency in the request path after the LLM: it
// rate-limits aggressively (HTTP 429 with a Retry-After) and has the occasional
// 5xx blip. Before this, a single 429 mid-sync would abort the whole inbox read
// or drop a rule application on the floor. The helpers below wrap each Gmail API
// call so transient failures are retried with exponential backoff + jitter,
// mirroring the resilience already in internal/ai (Mistral). The classifier and
// backoff math are pure so they can be tested without the network.

// retryConfig holds the resilience knobs for the Gmail client. maxRetries is the
// number of EXTRA attempts after the first (total attempts = maxRetries+1).
// baseDelay seeds the exponential backoff; maxDelay caps any single wait so a
// hostile Retry-After can't stall a request past the server's write timeout.
// sleep is injectable so tests don't actually wait.
type retryConfig struct {
	maxRetries int
	baseDelay  time.Duration
	maxDelay   time.Duration
	sleep      func(time.Duration)
}

// defaultRetryConfig is what NewService installs. Three extra attempts with a
// 400ms seed and an 8s ceiling keeps a noisy minute of 429s survivable while
// staying comfortably inside the API handlers' 60–90s context budgets.
func defaultRetryConfig() retryConfig {
	return retryConfig{
		maxRetries: 3,
		baseDelay:  400 * time.Millisecond,
		maxDelay:   8 * time.Second,
		sleep:      time.Sleep,
	}
}

// shouldRetry classifies a Gmail API error: it reports whether the failure is
// worth retrying and any server-advised Retry-After delay. HTTP 429 and 5xx are
// transient; other 4xx are permanent (a bad request won't fix itself). Context
// cancellation/deadline is never retried — the caller's budget is already spent.
// A non-API (transport) error is treated as transient.
func shouldRetry(err error) (retryable bool, retryAfter time.Duration) {
	if err == nil {
		return false, 0
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false, 0
	}
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		if apiErr.Code == 429 || apiErr.Code >= 500 {
			return true, parseRetryAfterHeader(apiErr.Header.Get("Retry-After"))
		}
		return false, 0
	}
	// Transport-level errors (timeouts, resets, DNS) are transient.
	return true, 0
}

// parseRetryAfterHeader interprets the delta-seconds form of a Retry-After
// header. Non-numeric or non-positive values yield 0.
func parseRetryAfterHeader(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return 0
}

// backoff computes the wait before the next attempt: exponential in the attempt
// number with full jitter (random in [d/2, d]) to avoid synchronized retries,
// never shorter than the server's Retry-After and never longer than maxDelay.
func (c retryConfig) backoff(attempt int, retryAfter time.Duration) time.Duration {
	d := c.baseDelay << attempt // baseDelay * 2^attempt
	if d > 0 {
		half := d / 2
		d = half + time.Duration(rand.Int63n(int64(half)+1))
	}
	if retryAfter > d {
		d = retryAfter
	}
	if c.maxDelay > 0 && d > c.maxDelay {
		d = c.maxDelay
	}
	return d
}

// withRetry runs a value-returning Gmail call, retrying transient failures with
// backoff. It is a free function (not a method) because Go methods can't be
// generic; the error-only retryErr wraps it for void calls.
func withRetry[T any](c retryConfig, fn func() (T, error)) (T, error) {
	var (
		val T
		err error
	)
	for attempt := 0; ; attempt++ {
		val, err = fn()
		if err == nil {
			return val, nil
		}
		retryable, retryAfter := shouldRetry(err)
		if !retryable || attempt >= c.maxRetries {
			return val, err
		}
		c.sleep(c.backoff(attempt, retryAfter))
	}
}

// retryErr runs a void Gmail call (one that only reports an error), retrying
// transient failures with backoff.
func (s *Service) retryErr(fn func() error) error {
	_, err := withRetry(s.retry, func() (struct{}, error) {
		return struct{}{}, fn()
	})
	return err
}
