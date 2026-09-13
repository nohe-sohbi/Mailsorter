package gmail

import (
	"testing"
	"time"

	gmailapi "google.golang.org/api/gmail/v1"
)

func TestParseDateHeaderVariants(t *testing.T) {
	want := time.Date(2022, time.November, 5, 9, 30, 0, 0, time.UTC)
	cases := []struct {
		name  string
		value string
	}{
		{"rfc1123z padded", "Sat, 05 Nov 2022 09:30:00 +0000"},
		{"single digit day", "Sat, 5 Nov 2022 09:30:00 +0000"},
		{"named zone", "Sat, 5 Nov 2022 09:30:00 GMT"},
		{"no weekday", "5 Nov 2022 09:30:00 +0000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseDateHeader(c.value, 0)
			if !got.Equal(want) {
				t.Fatalf("parseDateHeader(%q) = %v, want %v", c.value, got, want)
			}
		})
	}
}

func TestParseDateHeaderFallsBackToInternalDate(t *testing.T) {
	// An unparseable header (trailing comment) must not zero the date when
	// Gmail's canonical InternalDate is available.
	internal := time.Date(2023, time.March, 1, 12, 0, 0, 0, time.UTC)
	got := parseDateHeader("garbage -0700 (weird)", internal.UnixMilli())
	if !got.Equal(internal) {
		t.Fatalf("parseDateHeader fallback = %v, want %v", got, internal)
	}
}

func TestParseEmailHeadersUsesInternalDate(t *testing.T) {
	internal := time.Date(2024, time.January, 2, 8, 0, 0, 0, time.UTC)
	m := &gmailapi.Message{
		InternalDate: internal.UnixMilli(),
		Payload: &gmailapi.MessagePart{Headers: []*gmailapi.MessagePartHeader{
			{Name: "From", Value: "a@b.com"},
			{Name: "Date", Value: "not a date"},
		}},
	}
	_, _, _, date := ParseEmailHeaders(m)
	if !date.Equal(internal) {
		t.Fatalf("date = %v, want %v", date, internal)
	}
}

// A metadata listing returns only the headers it asked for, and Gmail omits the
// payload object entirely when it has none to give. This used to be a nil
// dereference, recovered as a 500 on the endpoint the inbox calls on every load.
func TestParseEmailHeadersSurvivesAMissingPayload(t *testing.T) {
	// InternalDate is the one field that still says something without a payload:
	// 2026-09-13T08:30:00Z in Gmail's epoch milliseconds.
	const internal = int64(1789288200000)

	from, subject, to, date := ParseEmailHeaders(&gmailapi.Message{Id: "m0", InternalDate: internal})

	if from != "" || subject != "" || to != nil {
		t.Errorf("headers from a payload-less message = (%q, %q, %v), want all empty", from, subject, to)
	}
	if date.IsZero() {
		t.Error("date = zero, want the InternalDate fallback: a zero date makes the message escape every olderThan/newerThan rule")
	}
	if got := date.UTC().Format("2006-01-02T15:04:05Z"); got != "2026-09-13T08:30:00Z" {
		t.Errorf("date = %s, want 2026-09-13T08:30:00Z", got)
	}

	// A nil message must not panic either: fetchMessages skips those, but the
	// parser is exported and called from several places.
	if _, _, _, d := ParseEmailHeaders(nil); !d.IsZero() {
		t.Errorf("ParseEmailHeaders(nil) date = %v, want the zero time", d)
	}
}
