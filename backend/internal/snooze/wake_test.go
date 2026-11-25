package snooze

import (
	"reflect"
	"testing"
)

func TestSettle(t *testing.T) {
	const max = 5
	cases := []struct {
		name        string
		outcome     Outcome
		attempts    int
		wantStatus  string
		wantCounted bool
	}{
		{"back in the inbox", Woken, 0, StatusDone, false},
		{"back after failed tries", Woken, 3, StatusDone, false},
		// Another mail program took it out of the folder it waited in:
		// nothing to bring back, and nothing to try again.
		{"gone from where it waited", Gone, 0, StatusCancelled, false},
		{"gone after failed tries", Gone, 4, StatusCancelled, false},
		// "Annuler" brought the mail back: the report ends before its hour.
		{"undone by the user", Undone, 0, StatusCancelled, false},
		{"undone after failed tries", Undone, 2, StatusCancelled, false},
		// A failure is tried again at the next pass, and counts.
		{"first failure", Failed, 0, StatusScheduled, true},
		{"failure before the last", Failed, max - 2, StatusScheduled, true},
		// The one that reaches the limit gives the report up.
		{"failure that reaches the limit", Failed, max - 1, StatusFailed, true},
		{"failure past the limit", Failed, max, StatusFailed, true},
		// Nothing attempted on the mail: left as it is, not counted, so a
		// mailbox out of reach for a while loses no report.
		{"untried", Untried, 0, "", false},
		{"untried at the limit", Untried, max - 1, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, counted := Settle(tc.outcome, tc.attempts, max)
			if status != tc.wantStatus || counted != tc.wantCounted {
				t.Errorf("Settle(%v, %d, %d) = (%q, %v), want (%q, %v)",
					tc.outcome, tc.attempts, max, status, counted, tc.wantStatus, tc.wantCounted)
			}
		})
	}
}

func TestSettleUnknownOutcomeChangesNothing(t *testing.T) {
	if status, counted := Settle(Outcome(0), 2, 5); status != "" || counted {
		t.Errorf("Settle(0, 2, 5) = (%q, %v), want (\"\", false)", status, counted)
	}
}

func TestByAccount(t *testing.T) {
	cases := []struct {
		name     string
		accounts []string
		want     [][]int
	}{
		{"nothing due", nil, nil},
		{"one report", []string{"a@x.fr"}, [][]int{{0}}},
		// Two reports of one account are one group, so one connection.
		{"one account", []string{"a@x.fr", "a@x.fr", "a@x.fr"}, [][]int{{0, 1, 2}}},
		// Interleaved as the database hands them over: grouped, each
		// account in the order it first appears, each group in order.
		{"interleaved accounts", []string{"a@x.fr", "b@y.fr", "a@x.fr", "c@z.fr", "b@y.fr"}, [][]int{{0, 2}, {1, 4}, {3}}},
		// An account is its exact address: no folding of case here, the
		// rows carry the userId as stored.
		{"exact addresses", []string{"a@x.fr", "A@x.fr"}, [][]int{{0}, {1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ByAccount(tc.accounts); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ByAccount(%q) = %v, want %v", tc.accounts, got, tc.want)
			}
		})
	}
}
