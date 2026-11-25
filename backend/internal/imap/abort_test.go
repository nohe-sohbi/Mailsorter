package imap

import (
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
)

// stallingProxy forwards connections to addr until stall is set, then stops
// passing on what the server answers while still taking the client's
// commands: a server that goes silent in the middle of a session. It returns
// the address to dial.
func stallingProxy(t *testing.T, addr string, stall *atomic.Bool) string {
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
				buf := make([]byte, 4096)
				for {
					n, err := server.Read(buf)
					if n > 0 && !stall.Load() {
						if _, werr := client.Write(buf[:n]); werr != nil {
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

// A server that stops answering holds go-imap for as long as it likes: there
// is no time limit on an answer. Abort, from another goroutine, is what lets a
// caller whose time is up (the report sweep) go on: the command waiting on the
// connection returns an error, and Close returns at once afterwards instead of
// waiting on a LOGOUT nobody answers.
func TestAbortEndsACommandTheServerDoesNotAnswer(t *testing.T) {
	var stall atomic.Bool
	addr := stallingProxy(t, startTestServer(t, standardMailboxes()), &stall)
	raw, err := imapclient.DialInsecure(addr, clientOptions("127.0.0.1"))
	if err != nil {
		t.Fatalf("dial through the proxy: %v", err)
	}
	c, err := newClient(raw, testUser, testPass)
	if err != nil {
		t.Fatalf("connect through the proxy: %v", err)
	}

	stall.Store(true)
	done := make(chan error, 1)
	go func() {
		_, err := c.Has(mailbox.InFolder("INBOX", "1"))
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("Has on a silent server returned before Abort (%v): the server is not silent, the test proves nothing", err)
	case <-time.After(300 * time.Millisecond):
	}

	c.Abort()
	select {
	case err := <-done:
		if err == nil {
			t.Error("Has cut short by Abort returned no error, want one: nothing was answered")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Has still waits 5 s after Abort, want it to return at once")
	}

	closed := make(chan struct{})
	go func() {
		c.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close after Abort still waits 5 s, want it to return at once")
	}
	if !c.Closed() {
		t.Error("Closed() after Abort = false, want true")
	}
}
