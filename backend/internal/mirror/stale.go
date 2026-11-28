// Package mirror holds the pure decisions about Mailsorter's local copy of an
// IMAP inbox, the copy the message list is served from. No I/O: the api layer
// reads and writes the copy, this package only says what it should hold.
package mirror

import "time"

// Row is a stored inbox message, as the sync judges it.
type Row struct {
	UID string
	// Left is true once Mailsorter claimed the message for a move out of the
	// inbox; LeftAt is when, and MovedTo the folder it landed in ("" while
	// the move is on its way, or when the server never said).
	Left    bool
	LeftAt  time.Time
	MovedTo string
}

// Rules are the time limits a judgement applies.
type Rules struct {
	// InFlight is how long a claimed row with no destination yet is left
	// alone: the move it announces may still be on its way.
	InFlight time.Duration
	// UndoWindow is how long a message Mailsorter moved out stays stored,
	// hidden, so "Annuler" knows where it went.
	UndoWindow time.Duration
}

// Verdict says what the sync does with each stored row it judged.
type Verdict struct {
	Gone   []string // deleted: the message is not in the inbox any more
	Back   []string // unflagged: the inbox still holds it
	Hidden []string // kept for "Annuler", out of the list
}

// Judge brings the stored rows in line with the inbox: present is every UID
// the inbox holds now, listedAt when the sync started reading it.
//
// A row Mailsorter flagged after listedAt was moved after the server
// answered, so the answer says nothing about it; nor does it about a row
// whose move is still in flight. Both wait for the next sync. A row the
// inbox does not hold goes, however old it is: present covers the whole
// inbox, not a page of it.
func Judge(rows []Row, present map[string]bool, listedAt, now time.Time, r Rules) Verdict {
	var v Verdict
	for _, row := range rows {
		if !row.Left {
			if !present[row.UID] {
				v.Gone = append(v.Gone, row.UID)
			}
			continue
		}
		inFlight := row.MovedTo == "" && now.Sub(row.LeftAt) < r.InFlight
		switch {
		case inFlight || !row.LeftAt.Before(listedAt):
		case present[row.UID]:
			v.Back = append(v.Back, row.UID)
		case row.MovedTo == "" || now.Sub(row.LeftAt) > r.UndoWindow:
			v.Gone = append(v.Gone, row.UID)
		default:
			v.Hidden = append(v.Hidden, row.UID)
		}
	}
	return v
}
