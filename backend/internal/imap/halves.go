package imap

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	imapv2 "github.com/emersion/go-imap/v2"
)

// Reading many messages when one of them may be unreadable.
//
// A FETCH reads a batch of messages in one answer, and go-imap reads that
// answer as a whole: a single message whose description it cannot parse (a
// MIME structure a server wrote its own way) fails the command, and the client
// closes the connection with it. Read in one batch, that message would fail
// every message next to it, at every sync, and say nothing about which one it
// is. ReadInHalves isolates it: on an answer it could not parse it opens the
// connection again and reads each half, down to the message alone, which is
// then reported as unreadable and the rest read.
//
// Only an answer that could not be parsed says something about a message.
// The server refusing (NO, BAD, [UNAVAILABLE], [THROTTLED]), a time limit or
// a lost connection says nothing of it: the reading stops there, and what was
// not read is left for next time, with no verdict on any message.

// ErrReopenLimit is what a reopen function answers once the unit of work may
// not open the connection again: ReadInHalves then stops and leaves what it
// has not read for next time, without an error.
var ErrReopenLimit = errors.New("imap: the connection may not be opened again for this unit of work")

// ErrConnectionLost is an answer that could not be read and a connection that
// could not be opened again: not a message, the mailbox.
var ErrConnectionLost = errors.New("imap: the connection was lost and could not be opened again")

// ErrReadStopped is a read the server refused, or that ran out of time or of
// connection: nothing to do with a message. What was not read is Left.
var ErrReadStopped = errors.New("imap: the server stopped answering the read")

// Halves is what reading a list of messages by halves came to.
type Halves[T any] struct {
	// Read is what was read, by UID.
	Read map[string]T
	// Unreadable is the messages whose answer could not be parsed even
	// alone, with the error it failed with: a fact about the message.
	Unreadable map[string]error
	// Left is the messages not read this time (the reading stopped, see
	// ReadInHalves), and Stopped the error it stopped on. Neither is a
	// verdict on those messages.
	Left    []string
	Stopped error
}

// ReadInHalves reads uids, batch at a time, through read.
//
//   - A batch whose answer could not be parsed (IsUnreadableAnswer) is
//     followed by reopen, then read again as two halves, down to one
//     message, which is then Unreadable.
//   - Any other failure stops the reading: the rest is Left and the error is
//     ErrReadStopped.
//   - A reopen that answers ErrReopenLimit stops the reading too, the rest
//     Left, without an error; one that fails otherwise is ErrConnectionLost.
//
// What was read until then is returned in every case.
func ReadInHalves[T any](uids []string, batch int, read func([]string) (map[string]T, error), reopen func() error) (Halves[T], error) {
	out := Halves[T]{Read: map[string]T{}, Unreadable: map[string]error{}}
	if batch <= 0 {
		batch = len(uids)
	}
	var queue [][]string
	for start := 0; start < len(uids); start += batch {
		queue = append(queue, uids[start:min(start+batch, len(uids))])
	}
	leave := func(current []string, err error) {
		out.Left = append(out.Left, current...)
		for _, q := range queue {
			out.Left = append(out.Left, q...)
		}
		if len(out.Left) > 0 {
			out.Stopped = err
		}
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
		if !IsUnreadableAnswer(err) {
			leave(current, err)
			return out, fmt.Errorf("%w: %v", ErrReadStopped, err)
		}
		// The message failed alone: that is its verdict, whatever happens
		// to the connection next.
		if len(current) == 1 {
			out.Unreadable[current[0]] = err
			current = nil
		}
		// The answer that could not be parsed took the connection with it:
		// nothing more can be asked on it, by this reading or by the next.
		if rerr := reopen(); rerr != nil {
			if errors.Is(rerr, ErrReopenLimit) {
				leave(current, err)
				return out, nil
			}
			leave(current, err)
			return out, fmt.Errorf("%w: %v (after an answer that could not be read: %v)", ErrConnectionLost, rerr, err)
		}
		if len(current) == 0 {
			continue
		}
		// The halves first, before the batches still waiting: the message
		// that failed is found before the next batch costs a reopen.
		mid := len(current) / 2
		queue = append([][]string{current[:mid], current[mid:]}, queue...)
	}
	return out, nil
}

// IsUnreadableAnswer says whether a failed read is an answer of the server
// go-imap could not parse: a description of a message written in a way it
// does not read, which reading it again will not change. A refusal of the
// server (an *imapv2.Error: NO, BAD, [UNAVAILABLE], [THROTTLED]), a time limit
// or a lost connection is not: it says nothing of the message.
//
// go-imap does not type its parse errors (it wraps them with %v). What marks
// one is the "imapwire:" of its decoder, whose first error wins, so a read
// that failed on the network stays an i/o error; and a panic it recovered
// while reading. TestReadInHalvesAgainstAServerWithAnUnreadableStructure pins
// both halves on the pinned version: an upgrade that words them otherwise
// fails there.
func IsUnreadableAnswer(err error) bool {
	if err == nil {
		return false
	}
	var refused *imapv2.Error
	if errors.As(err, &refused) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return false
	}
	// A read its context ended (within closed the connection under it) says
	// nothing of the message either, whatever the decoder reported.
	if errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "imapwire:") || strings.Contains(msg, "panic reading response")
}
