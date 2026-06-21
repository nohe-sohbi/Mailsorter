// Package snooze computes when a snoozed email should return to the inbox.
//
// "Reporter" (snooze) is a classic, beloved inbox feature: pull an email out of
// the way now and have it resurface, marked unread, at a moment that suits you.
// The only tricky part is turning a friendly preset ("ce soir", "demain",
// "ce week-end") into a concrete wake time relative to now — and that is pure,
// so it lives here and is tested exhaustively, independent of any storage.
package snooze

import (
	"fmt"
	"strings"
	"time"
)

// Presets understood by Resolve.
const (
	PresetLaterToday  = "laterToday"  // +3h, but at least 18:00 if that is later
	PresetThisEvening = "thisEvening" // today at 18:00 (or tomorrow if already past)
	PresetTomorrow    = "tomorrow"    // tomorrow at 08:00
	PresetThisWeekend = "weekend"     // the coming Saturday at 08:00
	PresetNextWeek    = "nextWeek"    // the coming Monday at 08:00
)

// morningHour and eveningHour anchor the day-grained presets.
const (
	morningHour = 8
	eveningHour = 18
)

// Resolve turns a preset into an absolute wake time, computed in now's location
// so it lines up with the user's wall clock. It always returns a time strictly
// in the future relative to now. Unknown presets yield an error so callers can
// fall back to an explicit timestamp.
func Resolve(preset string, now time.Time) (time.Time, error) {
	switch strings.TrimSpace(preset) {
	case PresetLaterToday:
		t := now.Add(3 * time.Hour)
		evening := atHour(now, eveningHour)
		if evening.After(t) {
			t = evening
		}
		return t, nil

	case PresetThisEvening:
		t := atHour(now, eveningHour)
		if !t.After(now) {
			t = atHour(now.AddDate(0, 0, 1), eveningHour)
		}
		return t, nil

	case PresetTomorrow:
		return atHour(now.AddDate(0, 0, 1), morningHour), nil

	case PresetThisWeekend:
		return nextWeekday(now, time.Saturday, morningHour), nil

	case PresetNextWeek:
		return nextWeekday(now, time.Monday, morningHour), nil

	default:
		return time.Time{}, fmt.Errorf("preset de report inconnu : %q", preset)
	}
}

// atHour returns the given calendar day at hour:00:00 in the day's location.
func atHour(day time.Time, hour int) time.Time {
	y, m, d := day.Date()
	return time.Date(y, m, d, hour, 0, 0, 0, day.Location())
}

// nextWeekday returns the next occurrence of weekday at hour:00, strictly after
// now. If today is that weekday but the hour has passed (or it is the same
// instant), it rolls forward a full week so the result is always in the future.
func nextWeekday(now time.Time, weekday time.Weekday, hour int) time.Time {
	delta := (int(weekday) - int(now.Weekday()) + 7) % 7
	candidate := atHour(now.AddDate(0, 0, delta), hour)
	if !candidate.After(now) {
		candidate = candidate.AddDate(0, 0, 7)
	}
	return candidate
}
