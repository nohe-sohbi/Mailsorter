//go:build acceptance

package api

// A service on the server's own internal network, for the scenarios about
// where Mailsorter may send a request of its own.
//
// The danger these scenarios pin is a NAME: an address that passes every check
// on its shape (https, not an IP literal, not "localhost") and still leads to
// an internal address once resolved. A scenario that proves the app refuses it
// therefore needs exactly that, a public-looking name that leads to 127.0.0.1
// behind a certificate the app trusts, and it needs it with NO seam in the
// production code, or it would prove the seam rather than the app. So the
// harness changes what the process sees of the network, the way an operator's
// network would:
//
//   - a tiny DNS responder answers intranetHost with 127.0.0.1, and
//     net.DefaultResolver becomes the pure-Go resolver dialing that responder:
//     every http.Client in the process that does not bring its own resolver
//     resolves names through it, as it would through the host's DNS;
//   - a CA generated at startup signs the service's certificate, and
//     SSL_CERT_FILE points at a bundle holding it, which is how any Go program
//     on Linux is told to trust a certificate. crypto/x509 reads the system
//     roots ONCE, so this must happen before the first TLS verification;
//   - the name is added to NO_PROXY, because a proxy in the environment (a dev
//     sandbox has one) would otherwise carry the request away to a place that
//     cannot reach the internal service. A server in production dials directly.
//
// All three are process-wide, so intranetSetup runs once from TestMain, before
// any scenario: swapping them while requests are in flight would race.
//
// Every scenario that uses the service first proves the harness works (a plain
// Go client reaches it by its name, see probe). Without that proof, a broken
// harness would make "nothing reached the internal service" pass for the wrong
// reason. On macOS, Go verifies certificates with the system keychain and
// ignores SSL_CERT_FILE: the proof fails there, loudly, instead of passing.
//
// Everything here is named intranet*, and the CA trust appends to whatever
// SSL_CERT_FILE already holds, so another harness helper trusting its own CA
// the same way (an IMAP server, say) composes with this one in either order.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

const (
	// intranetHost is the name the internal service is reached by. It looks
	// public (egress.Parse accepts it) and resolves, in this process only, to
	// 127.0.0.1. ".test" is reserved (RFC 2606): it never exists on the real DNS.
	intranetHost = "ia-interne.example.test"

	// intranetSecret is in everything the internal service answers. It must
	// never come back to whoever chose the address.
	intranetSecret = "SECRET-INTERNE-42"

	// intranetProbePath is what the harness itself calls to prove the service
	// is reachable by its name. It is not recorded as a request the app sent.
	intranetProbePath = "/__banc-d-essai/joignable"
)

var (
	// intranetErr is why the internal network could not be simulated, if it
	// could not. Only the scenarios that need it fail on it.
	intranetErr error
	// intranetCert is the service's certificate, signed by the generated CA.
	intranetCert tls.Certificate
)

// intranetSetup installs the simulated internal network for the whole test
// process and returns what undoes the parts that outlive it.
func intranetSetup() (cleanup func()) {
	dir, err := os.MkdirTemp("", "mailsorter-intranet-")
	if err != nil {
		intranetErr = fmt.Errorf("dossier temporaire : %w", err)
		return func() {}
	}
	cleanups := []func(){func() { _ = os.RemoveAll(dir) }}
	cleanup = func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}

	caPEM, cert, err := intranetCertificates()
	if err != nil {
		intranetErr = fmt.Errorf("génération des certificats : %w", err)
		return cleanup
	}
	intranetCert = cert
	if err := intranetTrustCA(dir, caPEM); err != nil {
		intranetErr = fmt.Errorf("confiance dans la CA de test : %w", err)
		return cleanup
	}

	stop, err := intranetDNS()
	if err != nil {
		intranetErr = fmt.Errorf("serveur DNS de test : %w", err)
		return cleanup
	}
	cleanups = append(cleanups, stop)

	intranetBypassProxy()
	return cleanup
}

// intranetCertificates generates a CA and a certificate for intranetHost
// signed by it. They live in memory and in a temp file for the run only.
func intranetCertificates() (caPEM []byte, leaf tls.Certificate, err error) {
	now := time.Now()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, leaf, err
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Mailsorter scenarios intranet CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, leaf, err
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, leaf, err
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, leaf, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: intranetHost},
		DNSNames:     []string{intranetHost},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, leaf, err
	}
	leaf = tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	return caPEM, leaf, nil
}

// intranetTrustCA makes every Go TLS client in the process trust the CA, the
// way an operator would: SSL_CERT_FILE. What the variable already points at is
// kept (a sandbox's proxy CA, or another harness helper's CA), so trusting one
// more CA never drops one. When it was unset, the system bundle is not copied
// over: the scenarios make no request to the internet.
func intranetTrustCA(dir string, caPEM []byte) error {
	bundle := []byte{}
	if prev := os.Getenv("SSL_CERT_FILE"); prev != "" {
		data, err := os.ReadFile(prev)
		if err != nil {
			return fmt.Errorf("lecture de SSL_CERT_FILE (%s) : %w", prev, err)
		}
		bundle = append(bundle, data...)
		bundle = append(bundle, '\n')
	}
	bundle = append(bundle, caPEM...)
	path := filepath.Join(dir, "intranet-ca-bundle.pem")
	if err := os.WriteFile(path, bundle, 0o600); err != nil {
		return err
	}
	return os.Setenv("SSL_CERT_FILE", path)
}

// intranetDNS starts a DNS responder on 127.0.0.1 that knows one name,
// intranetHost (A 127.0.0.1, no AAAA), and answers "no such name" for any
// other, then points net.DefaultResolver at it. The resolver's Dial ignores the
// nameserver from /etc/resolv.conf: in this process, this responder IS the DNS.
func intranetDNS() (stop func(), err error) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	addr := conn.LocalAddr().String()
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				return // closed by stop
			}
			if reply, err := intranetDNSReply(buf[:n]); err == nil {
				_, _ = conn.WriteTo(reply, from)
			}
		}
	}()

	// Never put back: the process exits right after the run, and writing the
	// variable while the datastore driver's goroutines may still dial would
	// race.
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "udp", addr)
		},
	}
	return func() { _ = conn.Close() }, nil
}

// intranetDNSReply answers one DNS query. Authoritative and RecursionAvailable
// are set because Go's resolver reads an empty answer carrying neither flag as
// a lame referral, an error, rather than as "this name has no IPv6 address".
func intranetDNSReply(query []byte) ([]byte, error) {
	var p dnsmessage.Parser
	h, err := p.Start(query)
	if err != nil {
		return nil, err
	}
	q, err := p.Question()
	if err != nil {
		return nil, err
	}
	known := strings.EqualFold(strings.TrimSuffix(q.Name.String(), "."), intranetHost)
	hdr := dnsmessage.Header{
		ID:                 h.ID,
		Response:           true,
		Authoritative:      true,
		RecursionDesired:   h.RecursionDesired,
		RecursionAvailable: true,
	}
	if !known {
		hdr.RCode = dnsmessage.RCodeNameError
	}
	b := dnsmessage.NewBuilder(nil, hdr)
	if err := b.StartQuestions(); err != nil {
		return nil, err
	}
	if err := b.Question(q); err != nil {
		return nil, err
	}
	if err := b.StartAnswers(); err != nil {
		return nil, err
	}
	if known && q.Type == dnsmessage.TypeA {
		if err := b.AResource(
			dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 60},
			dnsmessage.AResource{A: [4]byte{127, 0, 0, 1}},
		); err != nil {
			return nil, err
		}
	}
	return b.Finish()
}

// intranetBypassProxy adds intranetHost to NO_PROXY (both spellings, since Go
// reads the first one set). net/http reads the proxy variables once, at the
// first proxied request, which is why this runs from TestMain.
func intranetBypassProxy() {
	list := os.Getenv("NO_PROXY")
	if list == "" {
		list = os.Getenv("no_proxy")
	}
	if list != "" {
		list += ","
	}
	list += intranetHost
	_ = os.Setenv("NO_PROXY", list)
	_ = os.Setenv("no_proxy", list)
}

// ---------------------------------------------------------------------------
// The internal service
// ---------------------------------------------------------------------------

// intranetService is a service on the server's internal network: TLS, bound
// to 127.0.0.1, reached by the name intranetHost. It records every request it
// receives, and answers each one with an error whose body carries
// intranetSecret, the kind of internal answer that must never travel back to
// whoever chose the address.
type intranetService struct {
	server *httptest.Server
	// url is the service as an outsider names it: https://intranetHost:port.
	url string

	mu       sync.Mutex
	received []string // request bodies, in order
}

// internalService starts an internal service for this scenario and proves the
// harness reaches it by its name, the way any Go program would. A scenario
// that uses it then cannot pass merely because the harness is broken.
func (a *testApp) internalService() *intranetService {
	a.t.Helper()
	if intranetErr != nil {
		a.fatalf("le banc d'essai n'a pas pu simuler le réseau interne du serveur : %v", intranetErr)
	}
	s := &intranetService{}
	s.server = httptest.NewUnstartedServer(http.HandlerFunc(s.serve))
	s.server.TLS = &tls.Config{Certificates: []tls.Certificate{intranetCert}}
	s.server.StartTLS()
	a.t.Cleanup(s.server.Close)
	_, port, err := net.SplitHostPort(s.server.Listener.Addr().String())
	if err != nil {
		a.fatalf("adresse du service interne illisible : %v", err)
	}
	s.url = "https://" + net.JoinHostPort(intranetHost, port)

	if err := s.probe(); err != nil {
		a.fatalf("le banc d'essai ne joint pas le service interne par son nom %s, comme le ferait n'importe quel programme Go : %v. Sans cette preuve, ce scénario pourrait passer sans rien prouver", intranetHost, err)
	}
	return s
}

func (s *intranetService) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == intranetProbePath {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.received = append(s.received, string(body))
	s.mu.Unlock()
	// 400 rather than 401 or 403: an OpenAI-compatible client reports any
	// other status with the body it got, so an app that relays its provider's
	// errors relays this one.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = fmt.Fprintf(w, `{"service":"base-de-donnees-interne","jetonAdmin":%q}`, intranetSecret)
}

// probe calls the service through its name with a client built like the
// standard library's default one (environment proxy, default resolver, system
// roots) but with its own connection pool, so the probe leaves no open
// connection behind for the app to reuse.
func (s *intranetService) probe() error {
	tr := &http.Transport{
		Proxy:       http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 10 * time.Second}
	resp, err := client.Get(s.url + intranetProbePath)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("réponse %d au lieu de 204 : la requête n'est pas arrivée au service interne", resp.StatusCode)
	}
	return nil
}

// requests returns the bodies of the requests the app sent to the service.
func (s *intranetService) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.received...)
}

// expectNothingSentTo fails the scenario if anything reached the internal
// service. what names the user's action; mails are subjects the scenario
// delivered, so the failure can say which of them left for the internal
// network.
func (a *testApp) expectNothingSentTo(s *intranetService, what string, mails ...string) {
	a.t.Helper()
	got := s.requests()
	if len(got) == 0 {
		return
	}
	var leaked []string
	for _, subject := range mails {
		for _, body := range got {
			if strings.Contains(body, subject) {
				leaked = append(leaked, fmt.Sprintf("%q", subject))
				break
			}
		}
	}
	msg := fmt.Sprintf("%s a envoyé %d requête(s) au service interne derrière %s, rien ne devait partir vers cette adresse", what, len(got), s.url)
	if len(leaked) > 0 {
		msg += " ; elles contiennent tes mails " + strings.Join(leaked, ", ")
	}
	a.failf("%s", msg)
}
