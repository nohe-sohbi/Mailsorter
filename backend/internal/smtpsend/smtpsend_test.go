package smtpsend

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
)

const (
	testUser = "someone@example.com"
	testPass = "app-password"
)

var (
	// trustedTLS is a certificate for 127.0.0.1 signed by the authority the
	// test process trusts through SSL_CERT_FILE.
	trustedTLS *tls.Config
	// untrustedTLS is a certificate for 127.0.0.1 that nothing trusts.
	untrustedTLS *tls.Config
)

// TestMain makes this process trust a throwaway authority, through
// SSL_CERT_FILE, before anything verifies a certificate: on Linux Go reads the
// system roots once, on first use. Send itself has no way to trust anything
// else, and that is the point.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "smtpsend-test-ca-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "temp dir:", err)
		os.Exit(1)
	}
	code := func() int {
		defer os.RemoveAll(dir)
		caCert, caKey, caDER, err := newAuthority()
		if err != nil {
			fmt.Fprintln(os.Stderr, "test authority:", err)
			return 1
		}
		caFile := filepath.Join(dir, "ca.pem")
		if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "write the test authority:", err)
			return 1
		}
		if err := os.Setenv("SSL_CERT_FILE", caFile); err != nil {
			fmt.Fprintln(os.Stderr, "SSL_CERT_FILE:", err)
			return 1
		}
		if trustedTLS, err = leafConfig(caCert, caKey); err != nil {
			fmt.Fprintln(os.Stderr, "trusted certificate:", err)
			return 1
		}
		if untrustedTLS, err = leafConfig(nil, nil); err != nil {
			fmt.Fprintln(os.Stderr, "untrusted certificate:", err)
			return 1
		}
		return m.Run()
	}()
	os.Exit(code)
}

func newAuthority() (*x509.Certificate, *ecdsa.PrivateKey, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "smtpsend test CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	return cert, key, der, err
}

// leafConfig is a server certificate for 127.0.0.1, signed by the given
// authority, or by itself when there is none.
func leafConfig(ca *x509.Certificate, caKey *ecdsa.PrivateKey) (*tls.Config, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	parent, signer := template, key
	if ca != nil {
		parent, signer = ca, caKey
	}
	der, err := x509.CreateCertificate(rand.Reader, template, parent, &key.PublicKey, signer)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// ---------------------------------------------------------------------------
// A sending server
// ---------------------------------------------------------------------------

// testServer speaks the part of SMTP a mail program uses to hand over a
// message, over implicit TLS or with STARTTLS, and keeps what it saw.
type testServer struct {
	ln       net.Listener
	endpoint provider.Endpoint
	tls      *tls.Config
	// offerSTARTTLS is whether EHLO announces STARTTLS before the upgrade.
	offerSTARTTLS bool
	// silent is a server that accepts the connection and never says a word.
	silent bool
	// refusal is the reply to a refused login, one line per element.
	refusal []string

	mu          sync.Mutex
	connections int
	logins      []testLogin
	received    []testMessage
	wg          sync.WaitGroup
}

type testLogin struct {
	user     string
	accepted bool
	overTLS  bool
}

type testMessage struct {
	from string
	to   []string
	data string
}

type serverOption func(*testServer)

func withCertificate(cfg *tls.Config) serverOption { return func(s *testServer) { s.tls = cfg } }
func withoutSTARTTLS() serverOption                { return func(s *testServer) { s.offerSTARTTLS = false } }
func silent() serverOption                         { return func(s *testServer) { s.silent = true } }
func refusingWith(lines ...string) serverOption    { return func(s *testServer) { s.refusal = lines } }

func startServer(t *testing.T, mode provider.TLSMode, opts ...serverOption) *testServer {
	t.Helper()
	s := &testServer{tls: trustedTLS, offerSTARTTLS: true, refusal: []string{"535 5.7.8 Authentication credentials invalid"}}
	for _, o := range opts {
		o(s)
	}
	var (
		ln  net.Listener
		err error
	)
	if mode == provider.TLSImplicit {
		ln, err = tls.Listen("tcp", "127.0.0.1:0", s.tls)
	} else {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s.ln = ln
	s.endpoint = provider.Endpoint{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, TLS: mode}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.connections++
			s.mu.Unlock()
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(10 * time.Second))
				s.serve(c)
			}()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		s.wg.Wait()
	})
	return s
}

func (s *testServer) serve(c net.Conn) {
	if s.silent {
		// Hold the line without a greeting until the client hangs up.
		buf := make([]byte, 1)
		for {
			if _, err := c.Read(buf); err != nil {
				return
			}
		}
	}
	_, overTLS := c.(*tls.Conn)
	tp := textproto.NewConn(c)
	say := func(line string) { _ = tp.PrintfLine("%s", line) }
	say("220 127.0.0.1 ESMTP test")

	var (
		loggedIn bool
		from     string
		to       []string
	)
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		verb, arg, _ := strings.Cut(line, " ")
		switch strings.ToUpper(verb) {
		case "EHLO":
			say("250-127.0.0.1")
			if !overTLS && s.offerSTARTTLS {
				say("250-STARTTLS")
			}
			say("250 AUTH PLAIN")
		case "STARTTLS":
			if overTLS || !s.offerSTARTTLS {
				say("502 5.5.1 Not offered")
				continue
			}
			say("220 2.0.0 Ready to start TLS")
			tc := tls.Server(c, s.tls)
			if err := tc.Handshake(); err != nil {
				return
			}
			c, overTLS = tc, true
			tp = textproto.NewConn(tc)
			loggedIn, from, to = false, "", nil
		case "AUTH":
			mech, initial, _ := strings.Cut(arg, " ")
			if !strings.EqualFold(mech, "PLAIN") || initial == "" {
				say("504 5.5.4 Unsupported")
				continue
			}
			raw, err := base64.StdEncoding.DecodeString(initial)
			parts := strings.Split(string(raw), "\x00")
			if err != nil || len(parts) != 3 {
				say("501 5.5.2 Cannot decode")
				continue
			}
			ok := parts[1] == testUser && parts[2] == testPass
			s.mu.Lock()
			s.logins = append(s.logins, testLogin{user: parts[1], accepted: ok, overTLS: overTLS})
			s.mu.Unlock()
			if !ok {
				for _, line := range s.refusal {
					say(line)
				}
				continue
			}
			loggedIn = true
			say("235 2.7.0 Authentication successful")
		case "MAIL":
			if !loggedIn {
				say("530 5.7.0 Authentication required")
				continue
			}
			from = pathOf(arg)
			say("250 2.1.0 OK")
		case "RCPT":
			to = append(to, pathOf(arg))
			say("250 2.1.5 OK")
		case "DATA":
			say("354 Go ahead")
			data, err := tp.ReadDotBytes()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.received = append(s.received, testMessage{from: from, to: to, data: string(data)})
			s.mu.Unlock()
			say("250 2.0.0 OK queued")
		case "QUIT":
			say("221 2.0.0 Bye")
			return
		case "*":
			say("501 5.7.0 Authentication cancelled")
		default:
			say("502 5.5.2 Command not recognized")
		}
	}
}

// pathOf is the address of "FROM:<a> ..." or "TO:<a> ...".
func pathOf(arg string) string {
	start, end := strings.Index(arg, "<"), strings.Index(arg, ">")
	if start < 0 || end < start {
		return ""
	}
	return arg[start+1 : end]
}

func (s *testServer) seen() (connections int, logins []testLogin, received []testMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connections, append([]testLogin(nil), s.logins...), append([]testMessage(nil), s.received...)
}

func creds(e provider.Endpoint, password string) Credentials {
	return Credentials{Endpoint: e, Username: testUser, Password: password}
}

const testMessageBody = "From: someone@example.com\r\nTo: someone@example.com\r\nSubject: hello\r\n\r\nBody line\r\n.starts with a dot\r\n"

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestSendHandsTheMessageOverOnBothTLSModes(t *testing.T) {
	for _, mode := range []provider.TLSMode{provider.TLSImplicit, provider.TLSSTARTTLS} {
		t.Run(string(mode), func(t *testing.T) {
			s := startServer(t, mode)

			err := Send(context.Background(), creds(s.endpoint, testPass), testUser, []string{testUser}, []byte(testMessageBody))
			if err != nil {
				t.Fatalf("Send over %s = %v, want nil", mode, err)
			}

			_, logins, received := s.seen()
			if len(logins) != 1 || !logins[0].accepted || logins[0].user != testUser {
				t.Errorf("logins = %+v, want one accepted login for %s", logins, testUser)
			}
			for _, l := range logins {
				if !l.overTLS {
					t.Errorf("login %+v was sent before TLS", l)
				}
			}
			if len(received) != 1 {
				t.Fatalf("received %d messages, want 1", len(received))
			}
			m := received[0]
			if m.from != testUser || len(m.to) != 1 || m.to[0] != testUser {
				t.Errorf("envelope = from %q to %q, want from and to %q", m.from, m.to, testUser)
			}
			// The server reads the DATA back with its dot-stuffing undone and
			// its line ends as LF.
			if want := strings.ReplaceAll(testMessageBody, "\r\n", "\n"); m.data != want {
				t.Errorf("data = %q, want %q", m.data, want)
			}
		})
	}
}

// A STARTTLS route whose server does not offer the upgrade is refused before
// the password is sent: never in the clear, even to a server on 127.0.0.1,
// which net/smtp would otherwise trust with it.
func TestSendRefusesAServerThatDoesNotOfferSTARTTLS(t *testing.T) {
	s := startServer(t, provider.TLSSTARTTLS, withoutSTARTTLS())

	err := Send(context.Background(), creds(s.endpoint, testPass), testUser, []string{testUser}, []byte(testMessageBody))
	if err == nil {
		t.Fatal("Send to a server without STARTTLS = nil, want an error")
	}
	if errors.Is(err, ErrAuth) || errors.Is(err, ErrUnreachable) {
		t.Errorf("Send to a server without STARTTLS = %v, want neither ErrAuth nor ErrUnreachable", err)
	}
	if _, logins, received := s.seen(); len(logins) > 0 || len(received) > 0 {
		t.Errorf("the server saw logins %+v and messages %+v, want none", logins, received)
	}
}

func TestSendRefusesAnUntrustedCertificate(t *testing.T) {
	for _, mode := range []provider.TLSMode{provider.TLSImplicit, provider.TLSSTARTTLS} {
		t.Run(string(mode), func(t *testing.T) {
			s := startServer(t, mode, withCertificate(untrustedTLS))

			err := Send(context.Background(), creds(s.endpoint, testPass), testUser, []string{testUser}, []byte(testMessageBody))
			var unknown x509.UnknownAuthorityError
			if !errors.As(err, &unknown) {
				t.Errorf("Send to a server with an untrusted certificate = %v, want an x509.UnknownAuthorityError", err)
			}
			if _, logins, _ := s.seen(); len(logins) > 0 {
				t.Errorf("the server saw logins %+v, want none", logins)
			}
		})
	}
}

func TestSendReportsRefusedCredentials(t *testing.T) {
	s := startServer(t, provider.TLSImplicit)

	err := Send(context.Background(), creds(s.endpoint, "not-the-password"), testUser, []string{testUser}, []byte(testMessageBody))
	if !errors.Is(err, ErrAuth) {
		t.Errorf("Send with a refused password = %v, want ErrAuth", err)
	}
	if _, logins, received := s.seen(); len(logins) != 1 || logins[0].accepted || len(received) > 0 {
		t.Errorf("the server saw logins %+v and messages %+v, want one refused login and no message", logins, received)
	}
}

// What a refused login means depends on the reply, not only on its code.
// Gmail answers 534 both for a password that is not an app password (5.7.9,
// the user's to fix) and for a sign-in it blocked because of where it came
// from (5.7.14, where the password is right): calling the second a refused
// password sends the person to replace one that works. The replies are
// Gmail's own, multi-line as it sends them.
func TestSendTellsARefusedPasswordFromABlockedSignIn(t *testing.T) {
	cases := []struct {
		name  string
		reply []string
		want  error
	}{
		{"535, wrong credentials", []string{
			"535-5.7.8 Username and Password not accepted. For more information, go to",
			"535 5.7.8  https://support.google.com/mail/?p=BadCredentials - gsmtp",
		}, ErrAuth},
		{"534 5.7.9, an app password is needed", []string{
			"534-5.7.9 Application-specific password required. Learn more at",
			"534 5.7.9  https://support.google.com/mail/?p=InvalidSecondFactor - gsmtp",
		}, ErrAuth},
		{"534 5.7.14, the sign-in was blocked", []string{
			"534-5.7.14 <https://accounts.google.com/signin/continue?sarp=1&scc=1>",
			"534-5.7.14 Please log in via your web browser and then try again.",
			"534-5.7.14  Learn more at",
			"534 5.7.14  https://support.google.com/mail/answer/78754 - gsmtp",
		}, ErrBlocked},
		{"534 without an enhanced code", []string{"534 Authentication mechanism is too weak"}, nil},
		{"534 5.7.11, something else", []string{"534 5.7.11 Encryption required for requested authentication mechanism"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := startServer(t, provider.TLSImplicit, refusingWith(tc.reply...))

			err := Send(context.Background(), creds(s.endpoint, "not-the-password"), testUser, []string{testUser}, []byte(testMessageBody))
			if err == nil {
				t.Fatal("Send with a refused login = nil, want an error")
			}
			for _, sentinel := range []error{ErrAuth, ErrBlocked, ErrUnreachable} {
				if got := errors.Is(err, sentinel); got != (sentinel == tc.want) {
					t.Errorf("Send answered %q: errors.Is(%v, %v) = %v, want %v", tc.reply[0], err, sentinel, got, !got)
				}
			}
			if _, _, received := s.seen(); len(received) > 0 {
				t.Errorf("the server received %+v after refusing the login, want nothing", received)
			}
		})
	}
}

func TestSendReportsAnUnreachableServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	err = Send(context.Background(), creds(provider.Endpoint{Host: "127.0.0.1", Port: port, TLS: provider.TLSImplicit}, testPass),
		testUser, []string{testUser}, []byte(testMessageBody))
	if !errors.Is(err, ErrUnreachable) {
		t.Errorf("Send to a closed port = %v, want ErrUnreachable", err)
	}
}

// There is no third TLS mode: an endpoint without one is refused before any
// connection is opened.
func TestSendRefusesAnEndpointWithoutATLSMode(t *testing.T) {
	s := startServer(t, provider.TLSSTARTTLS)
	e := s.endpoint
	e.TLS = ""

	if err := Send(context.Background(), creds(e, testPass), testUser, []string{testUser}, []byte(testMessageBody)); err == nil {
		t.Fatal("Send without a TLS mode = nil, want an error")
	}
	if connections, _, _ := s.seen(); connections > 0 {
		t.Errorf("the server saw %d connections, want none", connections)
	}
}

// A server that accepts and never answers does not hold the caller past its
// deadline.
func TestSendGivesUpOnASilentServer(t *testing.T) {
	s := startServer(t, provider.TLSSTARTTLS, silent())

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := Send(ctx, creds(s.endpoint, testPass), testUser, []string{testUser}, []byte(testMessageBody))
	if err == nil {
		t.Fatal("Send to a silent server = nil, want an error")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("Send to a silent server took %v, want it to give up at its deadline", elapsed)
	}
}
