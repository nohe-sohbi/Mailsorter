package activity

import (
	"testing"
	"time"
)

func TestSummarizeBucketsAndWindow(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC) // Sunday noon
	rows := []Row{
		{At: now, Action: "archive", Source: "direct"},
		{At: now.Add(-1 * time.Hour), Action: "trash", Source: "rule"},     // folds to delete
		{At: now.AddDate(0, 0, -1), Action: "label", Source: "ai"},         // yesterday
		{At: now.AddDate(0, 0, -6), Action: "keep", Source: "ai"},          // oldest in window
		{At: now.AddDate(0, 0, -7), Action: "archive", Source: "direct"},   // out of window
		{At: now.AddDate(0, 0, -30), Action: "delete", Source: "bulk"},     // out of window
		{At: now.Add(-2 * time.Hour), Action: "markRead", Source: "snooze"}, // folds to read
	}

	s := Summarize(rows, now)

	if s.Total != 5 {
		t.Errorf("Total = %d, want 5 (two rows out of window)", s.Total)
	}
	if len(s.Days) != 7 {
		t.Fatalf("Days length = %d, want 7", len(s.Days))
	}
	// Days must be oldest-first and cover the trailing week.
	if s.Days[0].Date != "2026-06-15" || s.Days[6].Date != "2026-06-21" {
		t.Errorf("Days window = %s..%s, want 2026-06-15..2026-06-21", s.Days[0].Date, s.Days[6].Date)
	}
	if s.Days[6].Count != 3 { // archive + delete + read today
		t.Errorf("today count = %d, want 3", s.Days[6].Count)
	}
	if s.Days[0].Count != 1 { // the keep at -6d
		t.Errorf("oldest day count = %d, want 1", s.Days[0].Count)
	}

	if s.ByAction["delete"] != 1 { // trash folded into delete
		t.Errorf("byAction[delete] = %d, want 1", s.ByAction["delete"])
	}
	if s.ByAction["read"] != 1 { // markRead folded into read
		t.Errorf("byAction[read] = %d, want 1", s.ByAction["read"])
	}
	if _, ok := s.ByAction["archive"]; !ok {
		t.Error("byAction should always seed the headline actions")
	}

	if s.BySource["direct"] != 1 || s.BySource["ai"] != 2 || s.BySource["snooze"] != 1 {
		t.Errorf("bySource = %#v, unexpected", s.BySource)
	}
	if _, ok := s.BySource["bulk"]; ok {
		t.Error("out-of-window 'bulk' source should not appear")
	}
}

func TestSummarizeEmpty(t *testing.T) {
	now := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)
	s := Summarize(nil, now)
	if s.Total != 0 || len(s.Days) != 7 {
		t.Errorf("empty summary malformed: total=%d days=%d", s.Total, len(s.Days))
	}
	for _, d := range s.Days {
		if d.Count != 0 {
			t.Errorf("empty summary day %s has count %d", d.Date, d.Count)
		}
	}
}
