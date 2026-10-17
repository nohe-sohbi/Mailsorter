// Package digest renders Mailsorter's action-ledger recap into a ready-to-send
// daily email digest (subject + plain-text body + HTML body).
//
// The data already exists (see internal/activity, surfaced by
// GET /api/stats/activity); what was missing to ship "Digest quotidien par
// email" is the rendering of that data into something a human reads in their
// inbox. Keeping the rendering pure (no DB, no clock beyond the `now` argument,
// no network) makes the output deterministic and cheap to test. Actual
// delivery (a gmail.send scope + a scheduler) can sit on top of this without
// touching the formatting.
//
// The HTML body comes in two styles, one per look the instance wears
// (UI_THEME): the classic one, and the Grand Hotel one, the dashboard's
// palette and type drawn with what an email client accepts (tables, inline
// styles, web-safe fallbacks). The subject and the text body are the same in
// both: a recap must read the same whatever it looks like.
package digest

import (
	"fmt"
	"html"
	"sort"
	"strings"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/activity"
)

// Style is the look of the HTML body.
type Style string

const (
	// StyleClassic is the plain recap the digest always had.
	StyleClassic Style = "classic"
	// StyleHotel is the Grand Hotel recap: the sign over the door, the week as
	// brass bars, the breakdown as a register.
	StyleHotel Style = "hotel"
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

// Render turns a 7-day activity summary into a digest, in the classic style.
// See RenderStyle.
func Render(s activity.Summary, now time.Time) Digest {
	return RenderStyle(s, now, StyleClassic)
}

// RenderStyle turns a 7-day activity summary into a digest. `now` dates the
// recap (its day is treated as "today"). The subject leads with today's number
// so the recipient sees the value before opening. A style this package does
// not know renders as classic: UI_THEME is validated at boot, so the fallback
// only guards a caller passing a zero value.
func RenderStyle(s activity.Summary, now time.Time, style Style) Digest {
	today := todayCount(s)
	date := now.UTC().Format("02/01/2006")

	subject := fmt.Sprintf("Mailsorter : %d %s triés aujourd'hui", today, pluralize(today))
	if today == 0 {
		subject = "Mailsorter : votre récap de la semaine"
	}

	body := renderHTML(s, today, date)
	if style == StyleHotel {
		body = renderHotelHTML(s, today, date)
	}

	return Digest{
		Subject: subject,
		Text:    renderText(s, today, date),
		HTML:    body,
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
	fmt.Fprintf(&b, "Votre récap Mailsorter du %s\n\n", date)
	fmt.Fprintf(&b, "Aujourd'hui : %d %s triés.\n", today, pluralize(today))
	fmt.Fprintf(&b, "Cette semaine : %d %s triés.\n", s.Total, pluralize(s.Total))

	if actions := breakdownByAction(s); len(actions) > 0 {
		fmt.Fprintf(&b, "\nDétail : %s.\n", strings.Join(actions, ", "))
	}
	if sources := breakdownBySource(s); len(sources) > 0 {
		fmt.Fprintf(&b, "Sources : %s.\n", strings.Join(sources, ", "))
	}

	b.WriteString("\nBoîte plus légère, esprit plus clair.\nMailsorter\n")
	return b.String()
}

func renderHTML(s activity.Summary, today int, date string) string {
	var b strings.Builder
	b.WriteString(`<div style="font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;color:#1f2937">`)
	fmt.Fprintf(&b, `<p style="color:#6b7280;font-size:13px;margin:0 0 8px">Votre récap Mailsorter du %s</p>`, html.EscapeString(date))
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

	b.WriteString(`<p style="color:#6b7280;font-size:13px;margin:0">Boîte plus légère, esprit plus clair. Mailsorter</p></div>`)
	return b.String()
}

// The Grand Hotel palette, as the dashboard draws it by day (see
// frontend/src/styles/hotel.css). Email clients have no CSS variables and
// mostly no night mode worth designing for, so the day values are inlined.
const (
	hotelInk     = "#2B1B1E"
	hotelMuted   = "#6E4B52"
	hotelPlum    = "#7A2E3B"
	hotelMustard = "#E3A93B"
	hotelCream   = "#FBF1E4"
	hotelPaper   = "#FFFAF2"
	hotelPage    = "#F6E3D9"
	hotelHair    = "#E4CEC6"

	// Bodoni Moda and Jost are web fonts no client loads from a message: the
	// stacks fall back to the closest faces mail clients actually have.
	hotelDisplay = "'Bodoni Moda',Didot,'Bodoni 72','Bodoni MT','Times New Roman',serif"
	hotelUI      = "Jost,Futura,'Century Gothic','Trebuchet MS',Arial,sans-serif"

	// The tallest bar of the week, in pixels.
	hotelBarMax = 64
)

// emailsWord is "e-mail" or "e-mails", the hotel's spelling (the landing and
// the dashboard write "e-mail"), with the same plural rule as pluralize.
func emailsWord(n int) string {
	if n > 1 {
		return "e-mails"
	}
	return "e-mail"
}

// dayOfMonth is the "21" of "2026-06-21". The day keys come from
// activity.Summarize, always YYYY-MM-DD; anything shorter is shown as is
// rather than sliced into nonsense.
func dayOfMonth(date string) string {
	if len(date) == len("2006-01-02") {
		return strings.TrimPrefix(date[8:], "0")
	}
	return date
}

// barHeight scales a day's count against the busiest day. A day with nothing
// still draws a sliver, so the week reads as seven days and not as gaps.
func barHeight(count, max int) int {
	if count <= 0 || max <= 0 {
		return 2
	}
	h := count * hotelBarMax / max
	if h < 6 {
		return 6
	}
	return h
}

func renderHotelHTML(s activity.Summary, today int, date string) string {
	var b strings.Builder
	esc := html.EscapeString

	fmt.Fprintf(&b, `<div style="margin:0;padding:24px 12px;background:%s;font-family:%s;color:%s">`, hotelPage, hotelUI, hotelInk)
	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width:560px;margin:0 auto;border-collapse:collapse">`)

	// The sign over the door, and the slab under it.
	fmt.Fprintf(&b, `<tr><td style="padding:12px 16px;background:%s;border:2px solid %s;text-align:center;color:%s;font-size:12px;font-weight:600;letter-spacing:7px">MAILSORTER</td></tr>`, hotelPlum, hotelInk, hotelCream)
	fmt.Fprintf(&b, `<tr><td style="height:5px;padding:0;background:%s;border-left:2px solid %s;border-right:2px solid %s;font-size:0;line-height:0">&nbsp;</td></tr>`, hotelMustard, hotelInk, hotelInk)

	fmt.Fprintf(&b, `<tr><td style="padding:28px 28px 26px;background:%s;border:2px solid %s;border-top:0">`, hotelPaper, hotelInk)
	fmt.Fprintf(&b, `<p style="margin:0 0 10px;color:%s;font-size:11px;font-weight:600;letter-spacing:3px;text-transform:uppercase">Votre r&eacute;cap du %s</p>`, hotelMuted, esc(date))

	headline := fmt.Sprintf("%d %s tri&eacute;s aujourd'hui.", today, emailsWord(today))
	if today == 0 {
		headline = "Rien &agrave; trier aujourd'hui."
	}
	// opsz 18: where a client does have Bodoni Moda (the app's own preview
	// does), its display optical size draws the hyphen of "e-mails" as a
	// hairline that vanishes. Ignored by every client without the font.
	fmt.Fprintf(&b, `<h1 style="margin:0;font-family:%s;font-variation-settings:'opsz' 18;font-weight:400;font-size:34px;line-height:1.1;color:%s">%s</h1>`, hotelDisplay, hotelInk, headline)
	fmt.Fprintf(&b, `<p style="margin:8px 0 0;color:%s;font-size:15px">%d %s tri&eacute;s cette semaine. On s'en occupe, vous validez.</p>`, hotelMuted, s.Total, emailsWord(s.Total))

	// The week, as brass bars standing on a double rule.
	if len(s.Days) > 0 {
		max := 0
		for _, d := range s.Days {
			if d.Count > max {
				max = d.Count
			}
		}
		// Fixed column widths: left to content, a "29" column would be wider
		// than a "1" and the week would lean.
		fmt.Fprintf(&b, `<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" style="margin:24px 0 0;border-collapse:collapse;table-layout:fixed"><tr>`)
		for _, d := range s.Days {
			fmt.Fprintf(&b, `<td valign="bottom" style="height:%dpx;padding:0 4px;vertical-align:bottom;border-bottom:3px double %s">`, hotelBarMax+4, hotelInk)
			fmt.Fprintf(&b, `<div title="%d" style="height:%dpx;background:%s;border:1.5px solid %s;border-bottom:0;font-size:0;line-height:0">&nbsp;</div></td>`, d.Count, barHeight(d.Count, max), hotelMustard, hotelInk)
		}
		b.WriteString(`</tr><tr>`)
		for _, d := range s.Days {
			fmt.Fprintf(&b, `<td style="padding:6px 4px 0;text-align:center;color:%s;font-size:11px">%s</td>`, hotelMuted, esc(dayOfMonth(d.Date)))
		}
		b.WriteString(`</tr></table>`)
	}

	// The breakdown, as a register: the figure, then what was done.
	rows := []struct{ n, label string }{}
	for _, a := range actionOrder {
		if n := s.ByAction[a.key]; n > 0 {
			rows = append(rows, struct{ n, label string }{fmt.Sprint(n), a.label})
		}
	}
	if len(rows) > 0 {
		fmt.Fprintf(&b, `<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" style="margin:22px 0 0;border-collapse:collapse">`)
		for _, r := range rows {
			fmt.Fprintf(&b, `<tr><td style="width:56px;padding:9px 0;border-bottom:1px dotted %s;font-family:%s;font-size:24px;line-height:1;color:%s">%s</td>`, hotelHair, hotelDisplay, hotelInk, esc(r.n))
			fmt.Fprintf(&b, `<td style="padding:9px 0;border-bottom:1px dotted %s;font-family:%s;font-style:italic;font-size:18px;color:%s">%s</td></tr>`, hotelHair, hotelDisplay, hotelPlum, esc(r.label))
		}
		b.WriteString(`</table>`)
	}
	if sources := breakdownBySource(s); len(sources) > 0 {
		fmt.Fprintf(&b, `<p style="margin:16px 0 0;color:%s;font-size:13px;line-height:1.5">Dont %s.</p>`, hotelMuted, esc(strings.Join(sources, ", ")))
	}

	fmt.Fprintf(&b, `<p style="margin:26px 0 0;font-family:%s;font-style:italic;font-size:17px;color:%s">Bo&icirc;te plus l&eacute;g&egrave;re, esprit plus clair.</p>`, hotelDisplay, hotelPlum)
	b.WriteString(`</td></tr></table>`)
	fmt.Fprintf(&b, `<p style="margin:14px 0 0;text-align:center;color:%s;font-size:11px">Envoy&eacute; par Mailsorter, depuis votre propre bo&icirc;te. Le r&eacute;cap se coupe dans les r&eacute;glages.</p>`, hotelMuted)
	b.WriteString(`</div>`)
	return b.String()
}
