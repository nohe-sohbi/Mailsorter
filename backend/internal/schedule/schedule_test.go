package schedule

import (
	"testing"
	"time"
)

func TestDue(t *testing.T) {
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	interval := 30 * time.Minute

	cases := []struct {
		name string
		last time.Time
		want bool
	}{
		{"never run is due", time.Time{}, true},
		{"just ran is not due", now.Add(-5 * time.Minute), false},
		{"exactly one interval ago is due", now.Add(-interval), true},
		{"well past the interval is due", now.Add(-2 * time.Hour), true},
		{"one second short is not due", now.Add(-interval + time.Second), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Due(c.last, now, interval); got != c.want {
				t.Fatalf("Due(%v, now, %v) = %v, want %v", c.last, interval, got, c.want)
			}
		})
	}
}

func TestDueNonPositiveInterval(t *testing.T) {
	now := time.Now()
	if !Due(now, now, 0) {
		t.Fatal("a zero interval should always be due")
	}
	if !Due(now, now, -time.Minute) {
		t.Fatal("a negative interval should always be due")
	}
}
