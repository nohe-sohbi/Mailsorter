package mailer

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
	"time"
)

// readBuilt parses a message the way a mail program does: the headers, then
// each part of the alternative with its transfer encoding undone.
func readBuilt(t *testing.T, raw []byte) (*mail.Message, map[string]string) {
	t.Helper()
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("mail.ReadMessage: %v\n%s", err, raw)
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/alternative" {
		t.Fatalf("Content-Type = %q (%v), want multipart/alternative", msg.Header.Get("Content-Type"), err)
	}
	parts := map[string]string{}
	r := multipart.NewReader(msg.Body, params["boundary"])
	for {
		part, err := r.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextPart: %v", err)
		}
		body, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		ct, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		parts[ct] = string(body)
	}
	return msg, parts
}

func TestBuildIsAMessageAMailProgramReads(t *testing.T) {
	date := time.Date(2026, 11, 24, 7, 0, 0, 0, time.UTC)
	raw := Build(Message{
		From:    "me@example.com",
		To:      "me@example.com",
		Subject: "Récap quotidien : 3 triés",
		Text:    "Bonjour à vous\nDeux lignes.",
		HTML:    "<p>Bonjour à vous</p>",
		Date:    date,
	})

	// The accented subject must be MIME-encoded (not a raw 8-bit header).
	if bytes.Contains(raw, []byte("Subject: Récap")) {
		t.Error("subject with accents must be MIME-encoded in the header")
	}

	msg, parts := readBuilt(t, raw)
	for key, want := range map[string]string{"From": "me@example.com", "To": "me@example.com", "MIME-Version": "1.0"} {
		if got := msg.Header.Get(key); got != want {
			t.Errorf("header %s = %q, want %q", key, got, want)
		}
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil || subject != "Récap quotidien : 3 triés" {
		t.Errorf("decoded Subject = %q (%v), want %q", subject, err, "Récap quotidien : 3 triés")
	}
	if got, err := msg.Header.Date(); err != nil || !got.Equal(date) {
		t.Errorf("Date = %v (%v), want %v", got, err, date)
	}
	if got, want := parts["text/plain"], "Bonjour à vous\r\nDeux lignes.\r\n"; got != want {
		t.Errorf("text part = %q, want %q", got, want)
	}
	if got, want := parts["text/html"], "<p>Bonjour à vous</p>\r\n"; got != want {
		t.Errorf("html part = %q, want %q", got, want)
	}
}

// Without a date, the message carries no Date header rather than year 1.
func TestBuildWithoutADateHasNoDateHeader(t *testing.T) {
	raw := Build(Message{From: "me@example.com", To: "me@example.com", Subject: "s", Text: "t", HTML: "h"})
	if bytes.Contains(raw, []byte("Date:")) {
		t.Errorf("Build without a Date wrote a Date header:\n%s", raw)
	}
}

// The HTML recap is one long line. Sent as is, a sending server may refuse a
// line over 998 characters, or cut it inside a tag: every line of the built
// message stays short, and the HTML still comes back whole.
func TestBuildKeepsEveryLineShort(t *testing.T) {
	long := "<div>" + strings.Repeat(`<p style="margin:0;color:#6b7280">tri&eacute;s cette semaine</p>`, 200) + "</div>"
	raw := Build(Message{From: "me@example.com", To: "me@example.com", Subject: "s", Text: "t", HTML: long})

	for i, line := range strings.Split(string(raw), "\r\n") {
		if len(line) > 78 {
			t.Fatalf("line %d is %d characters long, want at most 78: %q", i+1, len(line), line)
		}
	}
	_, parts := readBuilt(t, raw)
	if got := strings.TrimSuffix(parts["text/html"], "\r\n"); got != long {
		t.Errorf("html part does not come back whole: got %d characters, want %d", len(got), len(long))
	}
}

func TestForGmailIsBase64URLOfTheMessage(t *testing.T) {
	raw := Build(Message{From: "me@example.com", To: "me@example.com", Subject: "Récap ?", Text: "t", HTML: "h"})
	encoded := ForGmail(raw)
	decoded, err := base64.URLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("ForGmail output must be valid base64url: %v", err)
	}
	if !bytes.Equal(decoded, raw) {
		t.Errorf("ForGmail(raw) decodes to %q, want %q", decoded, raw)
	}
}

func TestDueAt(t *testing.T) {
	mk := func(h int) time.Time { return time.Date(2026, 6, 21, h, 30, 0, 0, time.UTC) }
	yesterday := time.Date(2026, 6, 20, 7, 0, 0, 0, time.UTC)
	earlierToday := time.Date(2026, 6, 21, 7, 5, 0, 0, time.UTC)

	cases := []struct {
		name string
		last time.Time
		now  time.Time
		hour int
		want bool
	}{
		{"never sent, before hour", time.Time{}, mk(6), 7, false},
		{"never sent, after hour", time.Time{}, mk(8), 7, true},
		{"sent yesterday, after hour today", yesterday, mk(9), 7, true},
		{"already sent earlier today", earlierToday, mk(9), 7, false},
		{"out-of-range hour falls back to 7 (before)", time.Time{}, mk(6), 99, false},
		{"out-of-range hour falls back to 7 (after)", time.Time{}, mk(8), 99, true},
		{"exactly at the hour is due", time.Time{}, time.Date(2026, 6, 21, 7, 0, 0, 0, time.UTC), 7, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DueAt(tc.last, tc.now, tc.hour); got != tc.want {
				t.Errorf("DueAt(last=%v, now=%v, hour=%d) = %v, want %v", tc.last, tc.now, tc.hour, got, tc.want)
			}
		})
	}
}
