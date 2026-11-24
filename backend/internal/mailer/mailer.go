// Package mailer writes the daily recap as an email, and decides, purely, when
// a recurring daily message is due.
//
// It deliberately holds no mail client, no clock and no I/O: Build turns a
// subject + text/HTML bodies into an RFC 5322 message, ForGmail encodes that
// message the way Gmail's send API takes it (base64url), and DueAt answers
// "should today's digest go out yet?" given the last send time and a target
// hour. The message itself is the same on both transports: the Gmail API
// takes it encoded, the sending server of an IMAP mailbox takes it as is.
// Keeping all three pure makes the scheduler that drives them trivial to test
// and the formatting deterministic.
package mailer

import (
	"bytes"
	"encoding/base64"
	"mime"
	"mime/quotedprintable"
	"time"
)

// multipartBoundary is fixed (rather than random) so Build's output is
// deterministic, handy for tests and for byte-for-byte reproducibility.
const multipartBoundary = "mailsorter-alt-boundary"

// Message is one email Mailsorter writes: from an address to an address, a
// subject, and the same content as plain text and as HTML.
type Message struct {
	From    string
	To      string
	Subject string
	Text    string
	HTML    string
	// Date is the Date header, the one header besides From that RFC 5322
	// requires. Left zero, the message has none: the caller owns the clock.
	Date time.Time
}

// Build assembles a multipart/alternative email (plain text + HTML), in the
// RFC 5322 format a sending server takes. The subject is MIME-encoded so
// accented characters survive in the header, and the bodies are UTF-8 in
// quoted-printable: the HTML recap is one long line, and a line over 998
// characters is one a sending server may refuse (RFC 5321, 4.5.3.1.6) or cut
// in the middle of a tag. Quoted-printable keeps every line under 77.
func Build(m Message) []byte {
	var b bytes.Buffer
	if !m.Date.IsZero() {
		b.WriteString("Date: " + m.Date.Format(time.RFC1123Z) + "\r\n")
	}
	b.WriteString("From: " + m.From + "\r\n")
	b.WriteString("To: " + m.To + "\r\n")
	b.WriteString("Subject: " + mime.BEncoding.Encode("UTF-8", m.Subject) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: multipart/alternative; boundary=\"" + multipartBoundary + "\"\r\n\r\n")

	writePart(&b, "text/plain", m.Text)
	writePart(&b, "text/html", m.HTML)

	b.WriteString("--" + multipartBoundary + "--\r\n")
	return b.Bytes()
}

// writePart writes one part of the alternative, its body quoted-printable.
func writePart(b *bytes.Buffer, contentType, body string) {
	b.WriteString("--" + multipartBoundary + "\r\n")
	b.WriteString("Content-Type: " + contentType + "; charset=\"UTF-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	qp := quotedprintable.NewWriter(b)
	// A bytes.Buffer does not fail a write, and the encoder only fails when
	// the writer under it does: there is no error here to lose.
	_, _ = qp.Write([]byte(body))
	_ = qp.Close()
	b.WriteString("\r\n\r\n")
}

// ForGmail encodes a message built by Build the way Gmail's send API takes
// it, ready to drop into a Gmail Message.Raw.
func ForGmail(message []byte) string {
	return base64.URLEncoding.EncodeToString(message)
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
