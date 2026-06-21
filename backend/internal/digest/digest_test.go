package digest

import (
	"strings"
	"testing"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/activity"
)

func sampleSummary() activity.Summary {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	rows := []activity.Row{
		{At: now, Action: "archive", Source: "direct"},
		{At: now, Action: "archive", Source: "rule"},
		{At: now.Add(-1 * time.Hour), Action: "trash", Source: "ai"}, // folds to delete
		{At: now.AddDate(0, 0, -2), Action: "label", Source: "rule"},
		{At: now.AddDate(0, 0, -3), Action: "keep", Source: "ai"},
	}
	return activity.Summarize(rows, now)
}

func TestRenderSubjectLeadsWithToday(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	d := Render(sampleSummary(), now)

	// Today (2026-06-21) had 3 actions: two archives + one trash.
	if !strings.Contains(d.Subject, "3 emails triés aujourd'hui") {
		t.Errorf("subject = %q, want today's count (3) up front", d.Subject)
	}
}

func TestRenderEmptyFallbackSubject(t *testing.T) {
	now := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)
	d := Render(activity.Summarize(nil, now), now)
	if !strings.Contains(d.Subject, "récap de la semaine") {
		t.Errorf("empty subject = %q, want weekly-recap fallback", d.Subject)
	}
	// Even with no activity the bodies must render without panicking.
	if d.Text == "" || d.HTML == "" {
		t.Error("empty digest should still produce non-empty bodies")
	}
}

func TestRenderBodiesContainBreakdowns(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	d := Render(sampleSummary(), now)

	// Week total is 5; today is 3.
	for _, want := range []string{"5 emails triés", "2 archivés", "1 supprimés", "1 étiquetés", "1 gardés"} {
		if !strings.Contains(d.Text, want) {
			t.Errorf("text body missing %q\n--- got ---\n%s", want, d.Text)
		}
	}
	// Source labels are translated, not raw keys.
	if !strings.Contains(d.Text, "par vos règles") {
		t.Errorf("text body should translate the 'rule' source\n%s", d.Text)
	}
	if strings.Contains(d.HTML, "<li>direct</li>") {
		t.Error("HTML body should use the translated source label, not the raw key")
	}
}

func TestSingularPluralization(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	one := activity.Summarize([]activity.Row{{At: now, Action: "archive", Source: "direct"}}, now)
	d := Render(one, now)
	if !strings.Contains(d.Subject, "1 email triés aujourd'hui") {
		// note: French keeps "email" singular at 1; the verb agreement is left simple.
		t.Errorf("subject = %q, want singular 'email' at count 1", d.Subject)
	}
	if strings.Contains(d.Text, "1 emails triés") {
		t.Errorf("count of 1 should not pluralize 'email'\n%s", d.Text)
	}
}

func TestBySourceOrderingIsDeterministic(t *testing.T) {
	s := activity.Summary{
		Total:    6,
		Days:     []activity.DayCount{{Date: "2026-06-21", Count: 6}},
		ByAction: map[string]int{},
		BySource: map[string]int{"ai": 1, "rule": 3, "direct": 3},
	}
	out := breakdownBySource(s)
	// Busiest first; ties broken by source key ("direct" < "rule").
	if len(out) != 3 || !strings.HasPrefix(out[0], "3 à la main") || !strings.HasPrefix(out[1], "3 par vos règles") {
		t.Errorf("unexpected source ordering: %#v", out)
	}
}
