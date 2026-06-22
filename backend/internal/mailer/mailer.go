// Package mailer renders an email into the wire format Gmail's send API expects
// and decides, purely, when a recurring daily message is due.
//
// It deliberately holds no Gmail client, no clock and no I/O: BuildRaw turns a
// subject + text/HTML bodies into an RFC 2822, base64url-encoded message, and
// DueAt answers "should today's digest go out yet?" given the last send time
// and a target hour. Keeping both pure makes the scheduler that drives them
// trivial to test and the formatting deterministic.
package mailer

import (
	"encoding/base64"
	"mime"
	"strings"
	"time"
)

// multipartBoundary is fixed (rather than random) so BuildRaw's output is
// deterministic — handy for tests and for byte-for-byte reproducibility.
const multipartBoundary = "mailsorter-alt-boundary"

// BuildRaw assembles a multipart/alternative email (plain text + HTML) and
// returns it base64url-encoded, ready to drop into a Gmail Message.Raw. The
// subject is MIME-encoded so accented characters survive in the header, and the
// bodies are sent as UTF-8.
func BuildRaw(from, to, subject, text, htmlBody string) string {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + mime.BEncoding.Encode("UTF-8", subject) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: multipart/alternative; boundary=\"" + multipartBoundary + "\"\r\n\r\n")

	b.WriteString("--" + multipartBoundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(text + "\r\n\r\n")

	b.WriteString("--" + multipartBoundary + "\r\n")
	b.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(htmlBody + "\r\n\r\n")

	b.WriteString("--" + multipartBoundary + "--\r\n")

	return base64.URLEncoding.EncodeToString([]byte(b.String()))
}

// normalizeHour clamps an hour-of-day into [0,23]; an out-of-range value falls
// back to the supplied default so a bad setting never silences the digest.
func normalizeHour(hour, fallback int) int {
	if hour < 0 || hour > 23 {
		if fallback < 0 || fallback > 23 {
			return 7
		}
		return fallback
	}
	return hour
}

// DueAt reports whether a daily digest should be sent now. It is due when the
// current UTC hour has reached sendHourUTC AND no digest has already gone out
// today (UTC). A zero `last` means it has never been sent, so it is due as soon
// as the hour arrives. This makes the scheduler idempotent: it can tick as
// often as it likes and still send at most once per day.
func DueAt(last, now time.Time, sendHourUTC int) bool {
	sendHourUTC = normalizeHour(sendHourUTC, 7)
	now = now.UTC()
	if now.Hour() < sendHourUTC {
		return false
	}
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if last.IsZero() {
		return true
	}
	return last.UTC().Before(startOfToday)
}
