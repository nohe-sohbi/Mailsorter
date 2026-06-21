// Package digest renders Mailsorter's action-ledger recap into a ready-to-send
// daily email digest (subject + plain-text body + HTML body).
//
// The data already exists (see internal/activity, surfaced by
// GET /api/stats/activity); what was missing to ship "Digest quotidien par
// email" is the rendering of that data into something a human reads in their
// inbox. Keeping the rendering pure (no DB, no clock beyond the `now` argument,
// no network) makes the output deterministic and cheap to test — actual
// delivery (a gmail.send scope + a scheduler) can sit on top of this without
// touching the formatting.
package digest

import (
	"fmt"
	"html"
	"sort"
	"strings"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/activity"
)

// Digest is a rendered recap, ready to drop into an email.
type Digest struct {
	Subject string `json:"subject"`
	Text    string `json:"text"`
	HTML    string `json:"html"`
}

// actionLabels maps the canonical action buckets to human French labels. The
// order is the headline-priority order the UI uses (archive, delete, label,
// keep), so the digest reads consistently.
var actionOrder = []struct {
	key   string
	label string
}{
	{"archive", "archivés"},
	{"delete", "supprimés"},
	{"label", "étiquetés"},
	{"keep", "gardés"},
}

// sourceLabels maps ledger sources to human French labels.
var sourceLabels = map[string]string{
	"direct":      "à la main",
	"rule":        "par vos règles",
	"ai":          "par l'IA",
	"ai-auto":     "par l'auto-pilote IA",
	"bulk":        "en masse",
	"snooze":      "reportés",
	"unsubscribe": "désabonnements",
}

// pluralize returns "email" or "emails" depending on count (French rule: plural
// from 2; 0 and 1 stay singular).
func pluralize(n int) string {
	if n > 1 {
		return "emails"
	}
	return "email"
}

// todayCount is the count for the most recent day in the trailing-7 window,
// which Summarize guarantees is the last element of Days.
func todayCount(s activity.Summary) int {
	if len(s.Days) == 0 {
		return 0
	}
	return s.Days[len(s.Days)-1].Count
}

// Render turns a 7-day activity summary into a digest. `now` dates the recap
// (its day is treated as "today"). The subject leads with today's number so the
// recipient sees the value before opening.
func Render(s activity.Summary, now time.Time) Digest {
	today := todayCount(s)
	date := now.UTC().Format("02/01/2006")

	subject := fmt.Sprintf("Mailsorter — %d %s triés aujourd'hui", today, pluralize(today))
	if today == 0 {
		subject = "Mailsorter — votre récap de la semaine"
	}

	return Digest{
		Subject: subject,
		Text:    renderText(s, today, date),
		HTML:    renderHTML(s, today, date),
	}
}

// breakdownByAction returns the non-zero action buckets in headline order.
func breakdownByAction(s activity.Summary) []string {
	out := []string{}
	for _, a := range actionOrder {
		if n := s.ByAction[a.key]; n > 0 {
			out = append(out, fmt.Sprintf("%d %s", n, a.label))
		}
	}
	return out
}

// breakdownBySource returns the source buckets, busiest first, with a stable
// tie-break on the source key so the output is deterministic.
func breakdownBySource(s activity.Summary) []string {
	keys := make([]string, 0, len(s.BySource))
	for k := range s.BySource {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if s.BySource[keys[i]] != s.BySource[keys[j]] {
			return s.BySource[keys[i]] > s.BySource[keys[j]]
		}
		return keys[i] < keys[j]
	})
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		label := sourceLabels[k]
		if label == "" {
			label = k
		}
		out = append(out, fmt.Sprintf("%d %s", s.BySource[k], label))
	}
	return out
}

func renderText(s activity.Summary, today int, date string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Votre récap Mailsorter — %s\n\n", date)
	fmt.Fprintf(&b, "Aujourd'hui : %d %s triés.\n", today, pluralize(today))
	fmt.Fprintf(&b, "Cette semaine : %d %s triés.\n", s.Total, pluralize(s.Total))

	if actions := breakdownByAction(s); len(actions) > 0 {
		fmt.Fprintf(&b, "\nDétail : %s.\n", strings.Join(actions, ", "))
	}
	if sources := breakdownBySource(s); len(sources) > 0 {
		fmt.Fprintf(&b, "Sources : %s.\n", strings.Join(sources, ", "))
	}

	b.WriteString("\nBoîte plus légère, esprit plus clair. — Mailsorter\n")
	return b.String()
}

func renderHTML(s activity.Summary, today int, date string) string {
	var b strings.Builder
	b.WriteString(`<div style="font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;color:#1f2937">`)
	fmt.Fprintf(&b, `<p style="color:#6b7280;font-size:13px;margin:0 0 8px">Votre récap Mailsorter — %s</p>`, html.EscapeString(date))
	fmt.Fprintf(&b, `<h2 style="margin:0 0 4px;font-size:22px">%d %s triés aujourd'hui</h2>`, today, pluralize(today))
	fmt.Fprintf(&b, `<p style="margin:0 0 16px;color:#374151">%d %s triés cette semaine.</p>`, s.Total, pluralize(s.Total))

	if actions := breakdownByAction(s); len(actions) > 0 {
		b.WriteString(`<p style="margin:0 0 4px;font-weight:600">Détail</p><ul style="margin:0 0 16px;padding-left:18px;color:#374151">`)
		for _, a := range actions {
			fmt.Fprintf(&b, `<li>%s</li>`, html.EscapeString(a))
		}
		b.WriteString(`</ul>`)
	}
	if sources := breakdownBySource(s); len(sources) > 0 {
		b.WriteString(`<p style="margin:0 0 4px;font-weight:600">Sources</p><ul style="margin:0 0 16px;padding-left:18px;color:#374151">`)
		for _, src := range sources {
			fmt.Fprintf(&b, `<li>%s</li>`, html.EscapeString(src))
		}
		b.WriteString(`</ul>`)
	}

	b.WriteString(`<p style="color:#6b7280;font-size:13px;margin:0">Boîte plus légère, esprit plus clair. — Mailsorter</p></div>`)
	return b.String()
}
