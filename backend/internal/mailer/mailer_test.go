package mailer

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestBuildRawStructureAndDecoding(t *testing.T) {
	raw := BuildRaw("me@example.com", "me@example.com", "Récap quotidien — 3 triés", "Bonjour à vous", "<p>Bonjour à vous</p>")

	decoded, err := base64.URLEncoding.DecodeString(raw)
	if err != nil {
		t.Fatalf("output must be valid base64url: %v", err)
	}
	msg := string(decoded)

	for _, want := range []string{
		"From: me@example.com",
		"To: me@example.com",
		"MIME-Version: 1.0",
		"multipart/alternative; boundary=\"" + multipartBoundary + "\"",
		"Content-Type: text/plain; charset=\"UTF-8\"",
		"Content-Type: text/html; charset=\"UTF-8\"",
		"Bonjour à vous",
		"<p>Bonjour à vous</p>",
		"--" + multipartBoundary + "--", // closing delimiter
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("raw message missing %q\n--- got ---\n%s", want, msg)
		}
	}

	// The accented subject must be MIME-encoded (not a raw 8-bit header).
	if strings.Contains(msg, "Subject: Récap") {
		t.Error("subject with accents must be MIME-encoded in the header")
	}
	if !strings.Contains(msg, "Subject: =?UTF-8?b?") && !strings.Contains(msg, "Subject: =?UTF-8?B?") {
		t.Errorf("subject should use a MIME encoded-word\n%s", msg)
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
