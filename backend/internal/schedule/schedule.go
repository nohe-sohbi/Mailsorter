// Package schedule holds pure, clock-injected helpers for deciding when periodic
// background work is due. The background loops (snooze sweep, digest, auto-sync)
// tick on a timer, but *whether* a given user is due for work is a pure function
// of their last run, the current time and a minimum interval. Pulling that
// decision out of the loop keeps the cadence deterministic and cheap to test —
// no real timers, no I/O.
package schedule

import "time"

// Due reports whether work that last ran at `last` is due again at `now`, given
// a minimum `interval` between runs. A zero `last` (never run) is always due, as
// is a non-positive interval. The boundary is inclusive: exactly one interval
// after the last run counts as due.
func Due(last, now time.Time, interval time.Duration) bool {
	if last.IsZero() || interval <= 0 {
		return true
	}
	return !now.Before(last.Add(interval))
}
