// Package activity aggregates Mailsorter's action ledger into the weekly recap
// shown in-app. Every mutating Gmail action (a direct archive, a rule firing,
// an AI suggestion applied, a bulk sweep, a snooze, an unsubscribe) is appended
// to a ledger; this package turns those raw rows into a 7-day series plus
// breakdowns by action and by source.
//
// Keeping the aggregation pure (no DB, no clock beyond the `now` argument) makes
// the recap deterministic and cheap to test, and means the truthfulness of the
// numbers no longer depends on what the client happened to observe.
package activity

import "time"

// Row is a single ledger entry, projected down to what the recap needs.
type Row struct {
	At     time.Time
	Action string
	Source string
}

// DayCount is one bucket of the 7-day series.
type DayCount struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

// Summary is the aggregated recap over the trailing 7 days.
type Summary struct {
	Total    int            `json:"total"`
	Days     []DayCount     `json:"days"`
	ByAction map[string]int `json:"byAction"`
	BySource map[string]int `json:"bySource"`
}

// canonicalAction folds the various action vocabularies onto the four buckets
// the UI renders, so "trash" and "delete" (and "markRead"/"read") read as one.
func canonicalAction(a string) string {
	switch a {
	case "trash", "delete":
		return "delete"
	case "markRead", "read":
		return "read"
	default:
		return a
	}
}

// Summarize buckets ledger rows over the 7 calendar days ending on now (UTC).
// The Days slice is always exactly 7 entries, oldest first, with zero-filled
// gaps. ByAction is seeded with the headline triage actions so the UI can rely
// on their presence. Rows outside the window are ignored.
func Summarize(rows []Row, now time.Time) Summary {
	startDay := now.UTC().Truncate(24 * time.Hour).AddDate(0, 0, -6)

	dayCounts := map[string]int{}
	byAction := map[string]int{"archive": 0, "delete": 0, "label": 0, "keep": 0}
	bySource := map[string]int{}
	total := 0

	for _, r := range rows {
		at := r.At.UTC()
		if at.Before(startDay) {
			continue
		}
		dayCounts[at.Format("2006-01-02")]++
		byAction[canonicalAction(r.Action)]++
		if r.Source != "" {
			bySource[r.Source]++
		}
		total++
	}

	days := make([]DayCount, 0, 7)
	for i := 6; i >= 0; i-- {
		key := now.UTC().Truncate(24 * time.Hour).AddDate(0, 0, -i).Format("2006-01-02")
		days = append(days, DayCount{Date: key, Count: dayCounts[key]})
	}

	return Summary{Total: total, Days: days, ByAction: byAction, BySource: bySource}
}
