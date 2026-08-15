package snooze

import (
	"testing"
	"time"
)

// ref is a fixed reference instant: Wednesday 2026-06-17, 10:00 local UTC.
func ref() time.Time {
	return time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)
}

func TestResolvePresets(t *testing.T) {
	now := ref() // Wednesday 10:00
	cases := []struct {
		preset string
		want   time.Time
	}{
		// +3h = 13:00, but evening (18:00) is later, so 18:00 wins.
		{PresetLaterToday, time.Date(2026, 6, 17, 18, 0, 0, 0, time.UTC)},
		{PresetThisEvening, time.Date(2026, 6, 17, 18, 0, 0, 0, time.UTC)},
		{PresetTomorrow, time.Date(2026, 6, 18, 8, 0, 0, 0, time.UTC)},
		// Coming Saturday is 2026-06-20.
		{PresetThisWeekend, time.Date(2026, 6, 20, 8, 0, 0, 0, time.UTC)},
		// Coming Monday is 2026-06-22.
		{PresetNextWeek, time.Date(2026, 6, 22, 8, 0, 0, 0, time.UTC)},
	}
	for _, tc := range cases {
		got, err := Resolve(tc.preset, now)
		if err != nil {
			t.Fatalf("Resolve(%q) unexpected error: %v", tc.preset, err)
		}
		if !got.Equal(tc.want) {
			t.Errorf("Resolve(%q) = %v, want %v", tc.preset, got, tc.want)
		}
		if !got.After(now) {
			t.Errorf("Resolve(%q) = %v is not in the future of %v", tc.preset, got, now)
		}
	}
}

func TestLaterTodayUsesThreeHoursWhenAfterEvening(t *testing.T) {
	// At 20:00 the evening anchor is already past, so +3h (23:00) applies.
	now := time.Date(2026, 6, 17, 20, 0, 0, 0, time.UTC)
	got, err := Resolve(PresetLaterToday, now)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 6, 17, 23, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("laterToday at 20:00 = %v, want %v", got, want)
	}
}

func TestThisEveningRollsToTomorrowWhenPast(t *testing.T) {
	now := time.Date(2026, 6, 17, 19, 0, 0, 0, time.UTC) // past 18:00
	got, err := Resolve(PresetThisEvening, now)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 6, 18, 18, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("thisEvening past 18:00 = %v, want %v", got, want)
	}
}

func TestWeekendRollsAFullWeekOnSaturdayMorning(t *testing.T) {
	// Saturday 2026-06-20 at 09:00: 08:00 has passed, so next Saturday.
	now := time.Date(2026, 6, 20, 9, 0, 0, 0, time.UTC)
	got, err := Resolve(PresetThisWeekend, now)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 6, 27, 8, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("weekend on Saturday morning = %v, want %v", got, want)
	}
}

func TestResolveUnknownPreset(t *testing.T) {
	if _, err := Resolve("someday", ref()); err == nil {
		t.Error("expected error for unknown preset")
	}
}

func TestValidateWake(t *testing.T) {
	now := ref()
	cases := []struct {
		name    string
		wakeAt  time.Time
		wantErr bool
	}{
		{"an hour out", now.Add(time.Hour), false},
		{"a month out", now.AddDate(0, 1, 0), false},
		{"the last supported day", now.Add(MaxHorizon - time.Minute), false},
		{"unset", time.Time{}, true},
		{"in the past", now.Add(-time.Minute), true},
		{"right now", now, true},
		// A mistyped year is the realistic failure: it removes the email from
		// the inbox for a decade and nothing would ever surface it again.
		{"a decade out", now.AddDate(10, 0, 0), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateWake(tc.wakeAt, now)
			if tc.wantErr && err == nil {
				t.Errorf("ValidateWake(%v, %v) = nil, want an error", tc.wakeAt, now)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("ValidateWake(%v, %v) = %v, want nil", tc.wakeAt, now, err)
			}
		})
	}
}

// Every preset must satisfy the explicit-time validator: the two paths land in
// the same stored field, so a preset the guard would reject means the guard is
// wrong about what a legitimate wake time looks like.
func TestPresetsPassValidateWake(t *testing.T) {
	now := ref()
	for _, preset := range []string{
		PresetLaterToday, PresetThisEvening, PresetTomorrow, PresetThisWeekend, PresetNextWeek,
	} {
		wakeAt, err := Resolve(preset, now)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", preset, err)
		}
		if err := ValidateWake(wakeAt, now); err != nil {
			t.Errorf("preset %q resolves to %v, which ValidateWake rejects: %v", preset, wakeAt, err)
		}
	}
}
