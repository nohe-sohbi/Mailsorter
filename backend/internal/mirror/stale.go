// Package mirror holds the pure decisions about Mailsorter's local copy of an
// IMAP inbox, the copy the message list is served from. No I/O: the api layer
// reads and writes the copy, this package only says what it should hold.
package mirror

import "strconv"

// StaleUIDs returns the stored UIDs that are no longer in the inbox, given the
// UIDs a listing of that inbox just returned.
//
// A listing may cover only the most recent messages (complete is false when
// it stopped at its limit). Then only stored UIDs at or above the smallest
// listed one can be judged: the older ones were simply not looked at, and
// dropping them would empty the list of anyone with a large inbox. A UID that
// is not a number is never judged stale.
func StaleUIDs(stored, listed []string, complete bool) []string {
	seen := make(map[string]bool, len(listed))
	var floor uint64
	haveFloor := false
	for _, id := range listed {
		seen[id] = true
		if n, err := strconv.ParseUint(id, 10, 32); err == nil && (!haveFloor || n < floor) {
			floor, haveFloor = n, true
		}
	}
	if !complete && !haveFloor {
		return nil
	}
	var stale []string
	for _, id := range stored {
		if seen[id] {
			continue
		}
		n, err := strconv.ParseUint(id, 10, 32)
		if err != nil {
			continue
		}
		if complete || n >= floor {
			stale = append(stale, id)
		}
	}
	return stale
}
