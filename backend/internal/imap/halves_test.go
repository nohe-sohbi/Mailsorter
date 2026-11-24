package imap

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"reflect"
	"sort"
	"strconv"
	"testing"

	imapv2 "github.com/emersion/go-imap/v2"
)

// fakeFolder is a folder read through ReadInHalves without a server: a batch
// holding a bad message fails whole with the error go-imap gives an answer it
// cannot parse, and closes the "connection" until it is opened again, as
// go-imap does. A batch holding a refused message fails with that refusal
// instead, the connection left open, as a server answering NO does.
type fakeFolder struct {
	bad     map[string]bool
	refused map[string]error
	closed  bool
	reads   [][]string
	reopens int
	// maxReopens is how many reopens the unit of work allows, and down when
	// opening again fails.
	maxReopens int
	down       bool
}

func (f *fakeFolder) read(uids []string) (map[string]string, error) {
	f.reads = append(f.reads, append([]string(nil), uids...))
	if f.closed {
		return nil, errors.New("connection closed")
	}
	out := map[string]string{}
	for _, uid := range uids {
		if err := f.refused[uid]; err != nil {
			return nil, err
		}
		if f.bad[uid] {
			f.closed = true
			return nil, errors.New(`imap: read the parts of 2 messages in "INBOX": in response-data: in FETCH: in body-type-mpart: in body-type-1part: imapwire: expected string, got "("`)
		}
		out[uid] = "lu " + uid
	}
	return out, nil
}

func (f *fakeFolder) reopen() error {
	if f.down {
		return errors.New("dial: connection refused")
	}
	if f.reopens == f.maxReopens {
		return ErrReopenLimit
	}
	f.reopens++
	f.closed = false
	return nil
}

func numbered(from, to int) []string {
	var out []string
	for i := from; i <= to; i++ {
		out = append(out, strconv.Itoa(i))
	}
	return out
}

func readKeys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { a, _ := strconv.Atoi(out[i]); b, _ := strconv.Atoi(out[j]); return a < b })
	return out
}

func without(uids []string, drop ...string) []string {
	skip := map[string]bool{}
	for _, d := range drop {
		skip[d] = true
	}
	var out []string
	for _, u := range uids {
		if !skip[u] {
			out = append(out, u)
		}
	}
	return out
}

// A batch with an unreadable message in the middle: every other message is
// read, the bad one alone is named, and it took a reopen per halving, not one
// per message.
func TestReadInHalvesIsolatesTheUnreadableMessage(t *testing.T) {
	uids := numbered(1, 100)
	f := &fakeFolder{bad: map[string]bool{"37": true}, maxReopens: 20}

	got, err := ReadInHalves(uids, 100, f.read, f.reopen)
	if err != nil {
		t.Fatalf("ReadInHalves = %v, want no error: one message is not the mailbox", err)
	}
	if want := without(uids, "37"); !reflect.DeepEqual(readKeys(got.Read), want) {
		t.Errorf("Read = %v, want every message but 37", readKeys(got.Read))
	}
	if _, ok := got.Unreadable["37"]; !ok || len(got.Unreadable) != 1 {
		t.Errorf("Unreadable = %v, want 37 alone", got.Unreadable)
	}
	if len(got.Left) != 0 || got.Stopped != nil {
		t.Errorf("Left = %v, Stopped = %v; want nothing left", got.Left, got.Stopped)
	}
	// 100, 50, 25, 13, 7, 4, 2, 1: one reopen per level.
	if f.reopens > 8 {
		t.Errorf("%d reopens, want at most 8 (one per halving)", f.reopens)
	}
}

// Two bad messages, in two batches: both found, the batches around them read.
func TestReadInHalvesFindsEachUnreadableMessage(t *testing.T) {
	uids := numbered(1, 30)
	f := &fakeFolder{bad: map[string]bool{"3": true, "28": true}, maxReopens: 20}

	got, err := ReadInHalves(uids, 10, f.read, f.reopen)
	if err != nil {
		t.Fatal(err)
	}
	if want := without(uids, "3", "28"); !reflect.DeepEqual(readKeys(got.Read), want) {
		t.Errorf("Read = %v, want every message but 3 and 28", readKeys(got.Read))
	}
	if len(got.Unreadable) != 2 || got.Unreadable["3"] == nil || got.Unreadable["28"] == nil {
		t.Errorf("Unreadable = %v, want 3 and 28", got.Unreadable)
	}
	// The second batch never failed: it was read in one go.
	for _, r := range f.reads {
		if r[0] == "11" && len(r) != 10 {
			t.Errorf("the batch 11-20 was read as %v, want in one go", r)
		}
	}
}

// Nothing bad: one read per batch, no reopen.
func TestReadInHalvesReadsGoodBatchesOnce(t *testing.T) {
	f := &fakeFolder{maxReopens: 20}
	got, err := ReadInHalves(numbered(1, 25), 10, f.read, f.reopen)
	if err != nil || len(got.Read) != 25 || len(got.Unreadable) != 0 {
		t.Fatalf("ReadInHalves = %d read, %v unreadable, %v", len(got.Read), got.Unreadable, err)
	}
	if len(f.reads) != 3 || f.reopens != 0 {
		t.Errorf("%d reads and %d reopens, want 3 and 0", len(f.reads), f.reopens)
	}
}

// The connection cannot be opened again: that is the mailbox, not a message.
// Typed, with what was read before kept, and no message called unreadable.
func TestReadInHalvesReportsALostConnection(t *testing.T) {
	f := &fakeFolder{bad: map[string]bool{"15": true}, maxReopens: 20, down: true}
	got, err := ReadInHalves(numbered(1, 20), 10, f.read, f.reopen)
	if !errors.Is(err, ErrConnectionLost) {
		t.Fatalf("err = %v, want ErrConnectionLost", err)
	}
	if want := numbered(1, 10); !reflect.DeepEqual(readKeys(got.Read), want) {
		t.Errorf("Read = %v, want the first batch, read before the loss", readKeys(got.Read))
	}
	if len(got.Unreadable) != 0 {
		t.Errorf("Unreadable = %v, want none: a lost connection says nothing of a message", got.Unreadable)
	}
}

// Out of reopens: stop, leave the rest for next time, no message condemned
// and no error, but the failure that stopped it is kept for the log.
func TestReadInHalvesLeavesTheRestWhenItMayNotReopen(t *testing.T) {
	f := &fakeFolder{bad: map[string]bool{"5": true}, maxReopens: 1}
	got, err := ReadInHalves(numbered(1, 20), 10, f.read, f.reopen)
	if err != nil {
		t.Fatalf("err = %v, want none", err)
	}
	if len(got.Unreadable) != 0 {
		t.Errorf("Unreadable = %v, want none: 5 was not isolated", got.Unreadable)
	}
	if got.Stopped == nil {
		t.Error("Stopped = nil, want the failure that ran into the limit")
	}
	all := append(readKeys(got.Read), got.Left...)
	sort.Slice(all, func(i, j int) bool { a, _ := strconv.Atoi(all[i]); b, _ := strconv.Atoi(all[j]); return a < b })
	if !reflect.DeepEqual(all, numbered(1, 20)) {
		t.Errorf("read %v and left %v, want every message in one of the two", readKeys(got.Read), got.Left)
	}
}

// A refusal of the server, or a time limit, on a message read alone says
// nothing of it: no verdict, the reading stops there, the rest left for next
// time, and the error is typed for the caller. A message marked unreadable on
// a passing [UNAVAILABLE] would stay out of the filters for good.
func TestReadInHalvesGivesNoVerdictOnARefusalOrATimeLimit(t *testing.T) {
	unavailable := &imapv2.Error{Type: imapv2.StatusResponseTypeNo, Code: imapv2.ResponseCodeUnavailable, Text: "try again later"}
	cases := map[string]error{
		"NO [UNAVAILABLE]":          fmt.Errorf("imap: read the parts of 1 messages in %q: %w", "INBOX", unavailable),
		"NO [LIMIT] (throttled)":    &imapv2.Error{Type: imapv2.StatusResponseTypeNo, Code: imapv2.ResponseCodeLimit, Text: "throttled"},
		"SELECT refused":            fmt.Errorf("imap: select %q: %w", "INBOX", &imapv2.Error{Type: imapv2.StatusResponseTypeNo, Text: "mailbox busy"}),
		"time limit":                fmt.Errorf("imap: read: %w", os.ErrDeadlineExceeded),
		"connection reset mid-read": errors.New("in response: cannot read tag: read tcp 127.0.0.1:1->127.0.0.1:2: read: connection reset by peer"),
	}
	for name, refusal := range cases {
		t.Run(name, func(t *testing.T) {
			// 6 is refused, inside the second batch; 3 is bad, inside the
			// first, so the reading is halving when the refusal comes.
			f := &fakeFolder{bad: map[string]bool{"3": true}, refused: map[string]error{"6": refusal}, maxReopens: 20}
			got, err := ReadInHalves(numbered(1, 10), 5, f.read, f.reopen)
			if !errors.Is(err, ErrReadStopped) {
				t.Fatalf("err = %v, want ErrReadStopped", err)
			}
			if _, ok := got.Unreadable["6"]; ok {
				t.Error("6 marked unreadable on a refusal: it would stay out of the filters for good")
			}
			if _, ok := got.Unreadable["3"]; !ok {
				t.Errorf("Unreadable = %v, want 3, whose answer could not be parsed", got.Unreadable)
			}
			left := map[string]bool{}
			for _, u := range got.Left {
				left[u] = true
			}
			for _, u := range numbered(6, 10) {
				if !left[u] {
					t.Errorf("Left = %v, want 6 to 10, not read and not judged", got.Left)
					break
				}
			}
			if got.Stopped == nil {
				t.Error("Stopped = nil, want the refusal, for the log")
			}
		})
	}
}

// What counts as an answer that could not be parsed, and what does not.
func TestIsUnreadableAnswer(t *testing.T) {
	timeout := &net.OpError{Op: "read", Err: os.ErrDeadlineExceeded}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nothing", nil, false},
		{"a structure go-imap cannot parse", errors.New(`in body-type-1part: imapwire: expected string, got "("`), true},
		{"the same, wrapped", fmt.Errorf("imap: read the parts: %w", errors.New("in FETCH: imapwire: expected ')'")), true},
		{"a panic go-imap recovered while reading", errors.New("imapclient: panic reading response: index out of range"), true},
		{"the server says NO", &imapv2.Error{Type: imapv2.StatusResponseTypeNo, Text: "no"}, false},
		{"NO [UNAVAILABLE], wrapped", fmt.Errorf("imap: x: %w", &imapv2.Error{Type: imapv2.StatusResponseTypeNo, Code: imapv2.ResponseCodeUnavailable}), false},
		{"BAD", &imapv2.Error{Type: imapv2.StatusResponseTypeBad, Text: "imapwire: quoted in a refusal"}, false},
		{"a network time limit", timeout, false},
		{"a deadline", fmt.Errorf("x: %w", context.DeadlineExceeded), false},
		{"a connection gone", io.ErrUnexpectedEOF, false},
		{"a connection reset", errors.New("read tcp: connection reset by peer"), false},
	}
	for _, tc := range cases {
		if got := IsUnreadableAnswer(tc.err); got != tc.want {
			t.Errorf("IsUnreadableAnswer(%s: %v) = %v, want %v", tc.name, tc.err, got, tc.want)
		}
	}
}

// corruptingProxy forwards connections to addr, and rewrites every
// occurrence of from into to in what the server answers: a server that
// describes one message in a way go-imap cannot read. It returns the address
// to dial.
func corruptingProxy(t *testing.T, addr, from, to string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer client.Close()
				server, err := net.Dial("tcp", addr)
				if err != nil {
					return
				}
				defer server.Close()
				go func() {
					_, _ = io.Copy(server, client)
					server.Close()
				}()
				answers := bufio.NewReader(server)
				for {
					line, err := answers.ReadBytes('\n')
					if len(line) > 0 {
						if _, werr := client.Write(bytes.ReplaceAll(line, []byte(from), []byte(to))); werr != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			}()
		}
	}()
	return ln.Addr().String()
}

// The real thing: a real server, a real go-imap client, one message whose
// structure the client cannot read in the middle of a batch. Read in one
// FETCH, the batch fails and the connection is gone; read in halves, every
// other message gets its answer and the bad one is named.
func TestReadInHalvesAgainstAServerWithAnUnreadableStructure(t *testing.T) {
	addr := startTestServer(t, standardMailboxes())
	// The bad message's file name, in the structure the server writes, turns
	// into an open parenthesis where a string is expected.
	proxy := corruptingProxy(t, addr, `"illisible.pdf"`, `(`)

	c, err := dialTestClient(proxy)
	if err != nil {
		t.Fatalf("connect through the proxy: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	appendRaw(t, c, "INBOX", invoiceMessage("Facture", "facture.pdf"))
	appendRaw(t, c, "INBOX", message("a@b.com", "Texte"))
	appendRaw(t, c, "INBOX", invoiceMessage("Illisible", "illisible.pdf"))
	appendRaw(t, c, "INBOX", invoiceMessage("Contrat", "contrat.pdf"))
	appendRaw(t, c, "INBOX", message("a@b.com", "Encore du texte"))
	emails, err := c.List(t.Context(), "INBOX", 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// Newest first.
	bySubject := map[string]string{}
	var uids []string
	for i := len(emails) - 1; i >= 0; i-- {
		bySubject[emails[i].Subject] = emails[i].MessageID
		uids = append(uids, emails[i].MessageID)
	}

	// Where the test starts: the batch read whole fails, and the failure is
	// one IsUnreadableAnswer recognises on this version of go-imap. A proxy
	// that corrupts nothing proves nothing.
	_, err = c.Attachments("INBOX", uids)
	if err == nil {
		t.Fatal("the batch with the corrupted structure was read whole: the test proves nothing")
	}
	if !IsUnreadableAnswer(err) {
		t.Fatalf("go-imap's failure on an unparseable structure is not recognised by IsUnreadableAnswer: %v", err)
	}
	if !c.Closed() {
		t.Error("the connection is still open after an answer go-imap could not parse: Closed() does not see it")
	}

	// The probe above closed it: start, as the sync does, on an open one.
	current, err := dialTestClient(proxy)
	if err != nil {
		t.Fatalf("connect again through the proxy: %v", err)
	}
	reopens := 0
	read := func(batch []string) (map[string]bool, error) { return current.Attachments("INBOX", batch) }
	reopen := func() error {
		_ = current.Close()
		next, err := dialTestClient(proxy)
		if err != nil {
			return err
		}
		reopens++
		current = next
		return nil
	}
	t.Cleanup(func() { current.Close() })

	got, err := ReadInHalves(uids, 10, read, reopen)
	if err != nil {
		t.Fatalf("ReadInHalves = %v, want no error: one message is not the mailbox", err)
	}
	want := map[string]bool{
		bySubject["Facture"]:         true,
		bySubject["Texte"]:           false,
		bySubject["Contrat"]:         true,
		bySubject["Encore du texte"]: false,
	}
	if !reflect.DeepEqual(got.Read, want) {
		t.Errorf("Read = %v, want %v", got.Read, want)
	}
	if _, ok := got.Unreadable[bySubject["Illisible"]]; !ok || len(got.Unreadable) != 1 {
		t.Errorf("Unreadable = %v, want the message %s alone", got.Unreadable, bySubject["Illisible"])
	}
	if reopens == 0 {
		t.Error("no reopen: the connection the failure closed was used again")
	}
	// And the connection left open works.
	if _, err := current.Attachments("INBOX", []string{bySubject["Texte"]}); err != nil {
		t.Errorf("the connection opened again does not answer: %v", err)
	}
}

// A real refusal of a real server is not an unparseable answer: a SELECT the
// server answers NO stops the reading without a verdict on any message.
func TestReadInHalvesAgainstAServerThatRefuses(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	uid := appendMessage(t, c, "INBOX")
	reopens := 0
	got, err := ReadInHalves([]string{uid}, 10,
		func(batch []string) (map[string]bool, error) { return c.Attachments("Nowhere", batch) },
		func() error { reopens++; return nil })
	if !errors.Is(err, ErrReadStopped) {
		t.Fatalf("err = %v, want ErrReadStopped", err)
	}
	if len(got.Unreadable) != 0 || len(got.Left) != 1 || reopens != 0 {
		t.Errorf("Unreadable = %v, Left = %v, %d reopens; want no verdict, the message left, no reopen", got.Unreadable, got.Left, reopens)
	}
	if c.Closed() {
		t.Error("a refused SELECT closed the connection")
	}
}
