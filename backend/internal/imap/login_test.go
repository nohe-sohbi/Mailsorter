package imap

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	imapv2 "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
)

func TestRefusedCredentials(t *testing.T) {
	no := func(code imapv2.ResponseCode, text string) error {
		return &imapv2.Error{Type: imapv2.StatusResponseTypeNo, Code: code, Text: text}
	}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"authentication failed", no(imapv2.ResponseCodeAuthenticationFailed, "Invalid credentials (Failure)"), true},
		{"a NO that says nothing more", no("", "LOGIN failed."), true},
		{"a NO with a code that is not about availability", no(imapv2.ResponseCodeAlert, "Application-specific password required"), true},
		{"wrapped", fmt.Errorf("login: %w", no(imapv2.ResponseCodeAuthenticationFailed, "nope")), true},
		{"unavailable", no(imapv2.ResponseCodeUnavailable, "Temporary authentication failure"), false},
		{"server bug", no(imapv2.ResponseCodeServerBug, "Internal error"), false},
		{"limit", no(imapv2.ResponseCodeLimit, "Maximum number of connections from user+IP exceeded"), false},
		{"in use", no(imapv2.ResponseCodeInUse, "Mailbox locked"), false},
		{"too many, as a code", no(imapv2.ResponseCodeTooMany, "Slow down"), false},
		{"too many connections, as Gmail says it", no(imapv2.ResponseCodeAlert, "Too many simultaneous connections. (Failure)"), false},
		{"a BYE", &imapv2.Error{Type: imapv2.StatusResponseTypeBye, Text: "Too many connections"}, false},
		{"a BAD", &imapv2.Error{Type: imapv2.StatusResponseTypeBad, Text: "Command syntax error"}, false},
		{"a cut connection", io.ErrUnexpectedEOF, false},
		{"a time limit", context.DeadlineExceeded, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := refusedCredentials(tc.err); got != tc.want {
				t.Errorf("refusedCredentials(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// fakeLoginServer is a server that greets, then answers the first LOGIN with
// answer (given the command's tag), on plain TCP. It returns its address.
func fakeLoginServer(t *testing.T, answer func(tag string, conn net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				fmt.Fprint(conn, "* OK [CAPABILITY IMAP4rev1 AUTH=PLAIN] ready\r\n")
				lines := bufio.NewReader(conn)
				for {
					line, err := lines.ReadString('\n')
					if err != nil {
						return
					}
					fields := strings.Fields(line)
					if len(fields) >= 2 && strings.EqualFold(fields[1], "LOGIN") {
						answer(fields[0], conn)
						return
					}
					if len(fields) >= 2 {
						fmt.Fprintf(conn, "%s BAD unexpected\r\n", fields[0])
					}
				}
			}()
		}
	}()
	return ln.Addr().String()
}

// A failed LOGIN is a refused password only when the server says so: a
// server that is unavailable, over its connection limit, or that drops the
// line is unreachable, which the user cannot fix by typing a new password.
func TestNewClientTellsARefusedPasswordFromAnUnavailableServer(t *testing.T) {
	cases := []struct {
		name   string
		answer func(tag string, conn net.Conn)
		auth   bool
	}{
		{"authentication failed", func(tag string, c net.Conn) {
			fmt.Fprintf(c, "%s NO [AUTHENTICATIONFAILED] Invalid credentials\r\n", tag)
			time.Sleep(200 * time.Millisecond)
		}, true},
		{"a bare NO", func(tag string, c net.Conn) {
			fmt.Fprintf(c, "%s NO Login failed\r\n", tag)
			time.Sleep(200 * time.Millisecond)
		}, true},
		{"unavailable", func(tag string, c net.Conn) {
			fmt.Fprintf(c, "%s NO [UNAVAILABLE] Temporary authentication failure\r\n", tag)
			time.Sleep(200 * time.Millisecond)
		}, false},
		{"too many connections", func(tag string, c net.Conn) {
			fmt.Fprintf(c, "%s NO [ALERT] Too many simultaneous connections. (Failure)\r\n", tag)
			time.Sleep(200 * time.Millisecond)
		}, false},
		{"a BYE", func(tag string, c net.Conn) {
			fmt.Fprint(c, "* BYE Too many connections\r\n")
		}, false},
		{"a dropped line", func(tag string, c net.Conn) {}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := imapclient.DialInsecure(fakeLoginServer(t, tc.answer), clientOptions("127.0.0.1"))
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := newClient(raw, testUser, testPass)
				done <- err
			}()
			select {
			case err = <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("newClient still waits 10 s after the server answered LOGIN")
			}
			if tc.auth {
				if !errors.Is(err, ErrAuth) || errors.Is(err, ErrUnreachable) {
					t.Errorf("newClient = %v, want ErrAuth only", err)
				}
				return
			}
			if !errors.Is(err, ErrUnreachable) || errors.Is(err, ErrAuth) {
				t.Errorf("newClient = %v, want ErrUnreachable only", err)
			}
		})
	}
}

// The opening runs under the caller's context: a server that takes the TCP
// connection and never starts TLS holds Connect only until the context ends,
// not for the dial's own 15 s, and the answer is ErrUnreachable.
func TestConnectStopsWhenItsContextEnds(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { conn.Close() })
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	for _, mode := range []provider.TLSMode{provider.TLSImplicit, provider.TLSSTARTTLS} {
		t.Run(string(mode), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			start := time.Now()
			_, err := Connect(ctx, Credentials{
				Endpoint: provider.Endpoint{Host: "127.0.0.1", Port: addr.Port, TLS: mode},
				Username: testUser, Password: testPass,
			})
			if took := time.Since(start); took > 5*time.Second {
				t.Errorf("Connect to a silent server took %v with a 300 ms context, want it to stop with the context", took)
			}
			if !errors.Is(err, ErrUnreachable) || errors.Is(err, ErrAuth) {
				t.Errorf("Connect to a silent server = %v, want ErrUnreachable", err)
			}
		})
	}
}
