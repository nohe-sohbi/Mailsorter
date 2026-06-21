// Package metrics is a tiny, dependency-free, in-process request meter.
//
// It answers the operational questions a single-binary deployment actually has
// — "is the service taking traffic?", "what share of responses are errors?",
// "how slow are we?" — without pulling in Prometheus or a metrics backend. The
// registry is concurrency-safe and bounded (it never grows with cardinality:
// requests are bucketed by HTTP method and status class, not by URL), so it is
// safe to feed from the request hot path. Keeping the aggregation here and pure
// makes it cheap to test; the HTTP surface (a /metrics endpoint) sits on top.
package metrics

import (
	"sort"
	"sync"
	"time"
)

// Registry accumulates request counters and latency. The zero value is not
// usable; construct with New so the maps and start time are initialized.
type Registry struct {
	mu sync.Mutex

	started time.Time

	total      int64
	byMethod   map[string]int64 // GET, POST, …
	byStatus   map[string]int64 // "2xx", "4xx", …
	totalNanos int64            // sum of all request durations, for a mean
	maxNanos   int64            // slowest single request observed
}

// New returns a ready Registry, stamping the start time used for uptime.
func New() *Registry {
	return &Registry{
		started:  time.Now(),
		byMethod: map[string]int64{},
		byStatus: map[string]int64{},
	}
}

// Observe records one completed request: its HTTP method, response status code
// and how long it took. It is safe to call concurrently.
func (r *Registry) Observe(method string, status int, dur time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.total++
	if method != "" {
		r.byMethod[method]++
	}
	r.byStatus[statusClass(status)]++

	ns := dur.Nanoseconds()
	if ns < 0 {
		ns = 0
	}
	r.totalNanos += ns
	if ns > r.maxNanos {
		r.maxNanos = ns
	}
}

// statusClass folds a status code into its class bucket ("2xx", "5xx", …),
// keeping the status map bounded regardless of how many distinct codes appear.
func statusClass(status int) string {
	switch {
	case status >= 500:
		return "5xx"
	case status >= 400:
		return "4xx"
	case status >= 300:
		return "3xx"
	case status >= 200:
		return "2xx"
	case status >= 100:
		return "1xx"
	default:
		return "other"
	}
}

// Snapshot is an immutable, JSON-friendly view of the registry at one instant.
type Snapshot struct {
	UptimeSeconds int64            `json:"uptimeSeconds"`
	TotalRequests int64            `json:"totalRequests"`
	ByMethod      map[string]int64 `json:"byMethod"`
	ByStatusClass map[string]int64 `json:"byStatusClass"`
	AvgLatencyMs  float64          `json:"avgLatencyMs"`
	MaxLatencyMs  float64          `json:"maxLatencyMs"`
}

// SnapshotAt renders the registry as of `now` (so uptime is deterministic to
// test). Snapshot uses the wall clock.
func (r *Registry) SnapshotAt(now time.Time) Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()

	avgMs := 0.0
	if r.total > 0 {
		avgMs = float64(r.totalNanos) / float64(r.total) / 1e6
	}

	return Snapshot{
		UptimeSeconds: int64(now.Sub(r.started).Seconds()),
		TotalRequests: r.total,
		ByMethod:      copyCounts(r.byMethod),
		ByStatusClass: copyCounts(r.byStatus),
		AvgLatencyMs:  round2(avgMs),
		MaxLatencyMs:  round2(float64(r.maxNanos) / 1e6),
	}
}

// Snapshot renders the registry as of now.
func (r *Registry) Snapshot() Snapshot { return r.SnapshotAt(time.Now()) }

// copyCounts returns a defensive copy so callers can't mutate registry state
// through the snapshot's maps.
func copyCounts(m map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(m))
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys) // stable iteration if a caller ranges the copy
	for _, k := range keys {
		out[k] = m[k]
	}
	return out
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
