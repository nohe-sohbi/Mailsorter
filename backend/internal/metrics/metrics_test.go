package metrics

import (
	"testing"
	"time"
)

func TestStatusClass(t *testing.T) {
	cases := map[int]string{
		100: "1xx", 200: "2xx", 204: "2xx", 301: "3xx",
		404: "4xx", 429: "4xx", 500: "5xx", 503: "5xx", 0: "other",
	}
	for code, want := range cases {
		if got := statusClass(code); got != want {
			t.Errorf("statusClass(%d) = %q, want %q", code, got, want)
		}
	}
}

func TestObserveAndSnapshot(t *testing.T) {
	r := New()
	r.Observe("GET", 200, 10*time.Millisecond)
	r.Observe("GET", 404, 30*time.Millisecond)
	r.Observe("POST", 500, 50*time.Millisecond)

	s := r.Snapshot()
	if s.TotalRequests != 3 {
		t.Fatalf("total = %d, want 3", s.TotalRequests)
	}
	if s.ByMethod["GET"] != 2 || s.ByMethod["POST"] != 1 {
		t.Errorf("byMethod = %#v", s.ByMethod)
	}
	if s.ByStatusClass["2xx"] != 1 || s.ByStatusClass["4xx"] != 1 || s.ByStatusClass["5xx"] != 1 {
		t.Errorf("byStatusClass = %#v", s.ByStatusClass)
	}
	// Mean of 10/30/50ms = 30ms; max = 50ms.
	if s.AvgLatencyMs != 30 {
		t.Errorf("avg = %v, want 30", s.AvgLatencyMs)
	}
	if s.MaxLatencyMs != 50 {
		t.Errorf("max = %v, want 50", s.MaxLatencyMs)
	}
}

func TestSnapshotUptimeAndEmpty(t *testing.T) {
	r := New()
	// No requests yet: averages must be zero, not NaN/Inf.
	empty := r.Snapshot()
	if empty.TotalRequests != 0 || empty.AvgLatencyMs != 0 || empty.MaxLatencyMs != 0 {
		t.Errorf("empty snapshot should be all-zero, got %#v", empty)
	}

	r.started = time.Now().Add(-90 * time.Second)
	s := r.SnapshotAt(time.Now())
	if s.UptimeSeconds < 89 || s.UptimeSeconds > 92 {
		t.Errorf("uptime = %d, want ~90", s.UptimeSeconds)
	}
}

func TestSnapshotMapsAreCopies(t *testing.T) {
	r := New()
	r.Observe("GET", 200, time.Millisecond)
	s := r.Snapshot()
	s.ByMethod["GET"] = 999 // mutate the snapshot
	if again := r.Snapshot(); again.ByMethod["GET"] != 1 {
		t.Errorf("snapshot maps must be defensive copies; registry mutated to %d", again.ByMethod["GET"])
	}
}
