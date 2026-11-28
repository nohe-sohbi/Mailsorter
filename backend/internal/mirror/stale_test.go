package mirror

import (
	"reflect"
	"testing"
	"time"
)

func TestJudge(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	listedAt := now.Add(-time.Second)
	rules := Rules{InFlight: 2 * time.Minute, UndoWindow: 30 * 24 * time.Hour}
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	in := func(uids ...string) map[string]bool {
		m := map[string]bool{}
		for _, u := range uids {
			m[u] = true
		}
		return m
	}
	cases := []struct {
		name    string
		rows    []Row
		present map[string]bool
		want    Verdict
	}{
		{"nothing left the inbox", []Row{{UID: "1"}, {UID: "2"}}, in("1", "2"), Verdict{}},
		{"an old message moved by another client goes, whatever its age",
			[]Row{{UID: "3"}, {UID: "50"}, {UID: "120"}}, in("50", "120"), Verdict{Gone: []string{"3"}}},
		{"inbox emptied elsewhere", []Row{{UID: "5"}, {UID: "6"}}, in(), Verdict{Gone: []string{"5", "6"}}},
		{"moved out by Mailsorter: kept hidden for Annuler",
			[]Row{{UID: "7", Left: true, LeftAt: ago(time.Hour), MovedTo: "Archive"}}, in(), Verdict{Hidden: []string{"7"}}},
		{"moved out past the undo window: goes",
			[]Row{{UID: "8", Left: true, LeftAt: ago(31 * 24 * time.Hour), MovedTo: "Archive"}}, in(), Verdict{Gone: []string{"8"}}},
		{"flagged with no destination, past the in-flight time: goes",
			[]Row{{UID: "9", Left: true, LeftAt: ago(time.Hour)}}, in(), Verdict{Gone: []string{"9"}}},
		{"a move still in flight is left alone",
			[]Row{{UID: "10", Left: true, LeftAt: ago(30 * time.Second)}}, in("10"), Verdict{}},
		{"flagged before the listing and still in the inbox: back",
			[]Row{{UID: "11", Left: true, LeftAt: ago(time.Hour), MovedTo: "Archive"}}, in("11"), Verdict{Back: []string{"11"}}},
		{"a claim that never completed, still in the inbox: back",
			[]Row{{UID: "12", Left: true, LeftAt: ago(time.Hour)}}, in("12"), Verdict{Back: []string{"12"}}},
		{"flagged after the listing started: the listing says nothing about it",
			[]Row{{UID: "13", Left: true, LeftAt: now, MovedTo: "Archive"}}, in("13"), Verdict{}},
		{"a non-numeric id the inbox does not hold goes too",
			[]Row{{UID: "abc"}}, in("1"), Verdict{Gone: []string{"abc"}}},
	}
	for _, c := range cases {
		if got := Judge(c.rows, c.present, listedAt, now, rules); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Judge = %+v, want %+v", c.name, got, c.want)
		}
	}
}
