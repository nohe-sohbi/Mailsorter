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
