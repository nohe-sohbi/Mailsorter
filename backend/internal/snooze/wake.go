package snooze

// What becomes of a report once its hour has come. Deciding it is pure: the
// api layer moves the mail and writes the row, this file only says which row
// is written, so the rule reads the same on both transports and is tested
// without a mailbox.

// The statuses a report goes through, as stored.
const (
	// StatusScheduled is a report waiting for its hour, or for another try.
	StatusScheduled = "scheduled"
	// StatusDone is a report whose mail came back to the inbox.
	StatusDone = "done"
	// StatusFailed is a report given up after too many failed tries: the
	// mail is still out of the inbox, and the Reporté screen says so.
	StatusFailed = "failed"
	// StatusCancelled is a report that ended before its hour without the wake
	// bringing the mail back: the user undid it ("Annuler"), which brought
	// the mail back itself, or, on IMAP, another mail program took it out of
	// the folder it waited in, and the mail is where that program put it.
	StatusCancelled = "cancelled"
)

// Outcome is what one attempt at bringing a reported mail back came to.
type Outcome int

const (
	// Woken: the mail is back in the inbox.
	Woken Outcome = iota + 1
	// Gone: the mail is no longer where it waited. Nothing moved.
	Gone
	// Failed: the mailbox refused or broke off the attempt. Tried again at
	// the next pass, and counted toward the limit.
	Failed
	// Untried: nothing was attempted on the mail itself (the mailbox could
	// not be reached, the pass ran out of time, the user is there to try
	// again). Tried again at the next pass, not counted.
	Untried
	// Undone: the user undid the report ("Annuler"), which brought the mail
	// back to the inbox: the report ends there, before its hour.
	Undone
)

// Settle says what an attempt leaves on a report that had made attempts
// tries before it: its new status ("" when it stays as it is), and whether
// this attempt counts. A failed attempt counts, and the one that reaches
// maxAttempts gives the report up (StatusFailed) rather than retry, every
// minute and forever, a wake that cannot succeed.
func Settle(o Outcome, attempts, maxAttempts int) (status string, counted bool) {
	switch o {
	case Woken:
		return StatusDone, false
	case Gone, Undone:
		return StatusCancelled, false
	case Failed:
		if attempts+1 >= maxAttempts {
			return StatusFailed, true
		}
		return StatusScheduled, true
	}
	return "", false
}

// ByAccount groups the due reports by the account they belong to, given the
// account of each in order: one group of positions per account, in the order
// each account first appears, each group in the order given. A wake works
// through one group with one connection to that account's mailbox, rather
// than one connection per mail.
func ByAccount(accounts []string) [][]int {
	at := map[string]int{}
	var groups [][]int
	for i, account := range accounts {
		g, ok := at[account]
		if !ok {
			g = len(groups)
			at[account] = g
			groups = append(groups, nil)
		}
		groups[g] = append(groups[g], i)
	}
	return groups
}
