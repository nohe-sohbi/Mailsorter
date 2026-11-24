package imap

import (
	"errors"
	"fmt"
)

// Reading many messages when one of them may be unreadable.
//
// A FETCH reads a batch of messages in one answer, and go-imap reads that
// answer as a whole: a single message whose description it cannot parse (a
// MIME structure a server wrote its own way) fails the command, and the client
// closes the connection with it. Read in one batch, that message would fail
// every message next to it, at every sync, and say nothing about which one it
// is. ReadInHalves isolates it: on a failed batch it opens the connection
// again and reads each half, down to the message alone, which is then reported
// as unreadable and the rest read.

// ErrReopenLimit is what a reopen function answers once the unit of work may
// not open the connection again: ReadInHalves then stops and leaves what it
// has not read for next time, without an error.
var ErrReopenLimit = errors.New("imap: the connection may not be opened again for this unit of work")

// ErrConnectionLost is a batch that failed and a connection that could not be
// opened again: not a message, the mailbox.
var ErrConnectionLost = errors.New("imap: the connection was lost and could not be opened again")

// Halves is what reading a list of messages by halves came to.
type Halves[T any] struct {
	// Read is what was read, by UID.
	Read map[string]T
	// Unreadable is the messages that failed alone, on a connection opened
	// again, with the error they failed with.
	Unreadable map[string]error
	// Left is the messages not read because the connection could not be
	// opened again this time (ErrReopenLimit), and Stopped the error of the
	// batch that ran into it. Neither is a verdict on those messages.
	Left    []string
	Stopped error
}

// ReadInHalves reads uids, batch at a time, through read. A batch that fails
// is followed by reopen, then read again as two halves, down to one message;
// a message that fails alone is Unreadable. A reopen that fails is
// ErrConnectionLost, returned with what was read until then; a reopen that
// answers ErrReopenLimit ends the reading with the rest Left.
func ReadInHalves[T any](uids []string, batch int, read func([]string) (map[string]T, error), reopen func() error) (Halves[T], error) {
	out := Halves[T]{Read: map[string]T{}, Unreadable: map[string]error{}}
	if batch <= 0 {
		batch = len(uids)
	}
	var queue [][]string
	for start := 0; start < len(uids); start += batch {
		queue = append(queue, uids[start:min(start+batch, len(uids))])
	}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		got, err := read(current)
		if err == nil {
			for uid, v := range got {
				out.Read[uid] = v
			}
			continue
		}
		// The answer that could not be read took the connection with it:
		// nothing more can be asked on it.
		if rerr := reopen(); rerr != nil {
			if errors.Is(rerr, ErrReopenLimit) {
				out.Left = append(out.Left, current...)
				for _, q := range queue {
					out.Left = append(out.Left, q...)
				}
				out.Stopped = err
				return out, nil
			}
			return out, fmt.Errorf("%w: %v (after a read that failed: %v)", ErrConnectionLost, rerr, err)
		}
		if len(current) == 1 {
			out.Unreadable[current[0]] = err
			continue
		}
		// The halves first, before the batches still waiting: the message
		// that failed is found before the next batch costs a reopen.
		mid := len(current) / 2
		queue = append([][]string{current[:mid], current[mid:]}, queue...)
	}
	return out, nil
}
