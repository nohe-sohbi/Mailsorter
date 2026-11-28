package api

import "testing"

// The settings screen and the scheduler read the digest hour through the same
// rule: midnight is a real choice, only a missing or out-of-range value falls
// back to the server default.
func TestDigestHourOf(t *testing.T) {
	saved := DefaultDigestHourUTC
	DefaultDigestHourUTC = 7
	defer func() { DefaultDigestHourUTC = saved }()

	at := func(h int) *int { return &h }
	cases := []struct {
		name   string
		stored *int
		want   int
	}{
		{"never picked", nil, 7},
		{"midnight", at(0), 0},
		{"evening", at(21), 21},
		{"last hour", at(23), 23},
		{"out of range high", at(24), 7},
		{"negative", at(-1), 7},
	}
	for _, c := range cases {
		if got := digestHourOf(c.stored); got != c.want {
			t.Errorf("%s: digestHourOf = %d, want %d", c.name, got, c.want)
		}
	}
}
