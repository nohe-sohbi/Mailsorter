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

func TestRenderIsClassic(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	if got, want := Render(sampleSummary(), now), RenderStyle(sampleSummary(), now, StyleClassic); got != want {
		t.Errorf("Render(...) = %+v, want RenderStyle(..., StyleClassic) = %+v", got, want)
	}
}

func TestRenderStyleUnknownFallsBackToClassic(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	for _, style := range []Style{"", "baroque"} {
		if got, want := RenderStyle(sampleSummary(), now, style).HTML, Render(sampleSummary(), now).HTML; got != want {
			t.Errorf("RenderStyle(..., %q).HTML differs from the classic body", style)
		}
	}
}

func TestHotelStyleChangesOnlyTheHTML(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	classic := RenderStyle(sampleSummary(), now, StyleClassic)
	hotel := RenderStyle(sampleSummary(), now, StyleHotel)
	if hotel.Subject != classic.Subject {
		t.Errorf("hotel subject = %q, want the classic one %q", hotel.Subject, classic.Subject)
	}
	if hotel.Text != classic.Text {
		t.Errorf("hotel text body differs from the classic one:\n%s\n---\n%s", hotel.Text, classic.Text)
	}
	if hotel.HTML == classic.HTML {
		t.Error("hotel HTML body is identical to the classic one")
	}
}

func TestHotelHTMLCarriesTheRecap(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	got := RenderStyle(sampleSummary(), now, StyleHotel).HTML
	for _, want := range []string{
		"MAILSORTER",                          // the sign over the door
		"21/06/2026",                          // the date of the recap
		"3 e-mails tri&eacute;s aujourd'hui.", // today, in the hotel's spelling
		"5 e-mails tri&eacute;s cette semaine",
		"archivés", "supprimés", "étiquetés", "gardés",
		"par vos règles", // sources, translated
		hotelPlum, hotelMustard, hotelPage,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("hotel HTML missing %q\n--- got ---\n%s", want, got)
		}
	}
	// One bar per day of the trailing week.
	if n := strings.Count(got, `<div title="`); n != 7 {
		t.Errorf("hotel HTML draws %d bars, want 7", n)
	}
}

func TestHotelHTMLQuietDay(t *testing.T) {
	now := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)
	got := RenderStyle(activity.Summarize(nil, now), now, StyleHotel).HTML
	if !strings.Contains(got, "Rien &agrave; trier aujourd'hui.") {
		t.Errorf("quiet-day hotel headline missing\n%s", got)
	}
	// An empty week still draws its seven slivers rather than nothing.
	if n := strings.Count(got, `height:2px`); n != 7 {
		t.Errorf("quiet week draws %d slivers, want 7", n)
	}
}

func TestHotelHTMLEscapesUnknownSources(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	s := activity.Summary{
		Total:    1,
		Days:     []activity.DayCount{{Date: "2026-06-21", Count: 1}},
		ByAction: map[string]int{"archive": 1},
		BySource: map[string]int{"<script>": 1},
	}
	got := RenderStyle(s, now, StyleHotel).HTML
	if strings.Contains(got, "<script>") {
		t.Errorf("an unknown source key reached the HTML unescaped\n%s", got)
	}
}

func TestBarHeight(t *testing.T) {
	cases := []struct {
		count, max, want int
	}{
		{0, 10, 2},
		{0, 0, 2},
		{10, 10, hotelBarMax},
		{5, 10, hotelBarMax / 2},
		{1, 100, 6},
	}
	for _, tc := range cases {
		if got := barHeight(tc.count, tc.max); got != tc.want {
			t.Errorf("barHeight(%d, %d) = %d, want %d", tc.count, tc.max, got, tc.want)
		}
	}
}

func TestDayOfMonth(t *testing.T) {
	for in, want := range map[string]string{"2026-06-21": "21", "2026-06-05": "5", "junk": "junk"} {
		if got := dayOfMonth(in); got != want {
			t.Errorf("dayOfMonth(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEmailsWord(t *testing.T) {
	for n, want := range map[int]string{0: "e-mail", 1: "e-mail", 2: "e-mails", 42: "e-mails"} {
		if got := emailsWord(n); got != want {
			t.Errorf("emailsWord(%d) = %q, want %q", n, got, want)
		}
	}
}
