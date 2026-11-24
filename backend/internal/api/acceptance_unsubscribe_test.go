//go:build acceptance

package api

// The website of a sender: the page a newsletter names in its
// List-Unsubscribe header for the one-click unsubscribe (RFC 8058).
//
// That POST is the one request Mailsorter sends to an address a stranger
// chose, so it goes through internal/egress, which refuses the server's own
// machine and every private address, at dial time too. So no fake site can
// stand for a real one here: this one runs on 127.0.0.1, an address the guard
// refuses, and a request that reaches it is a request the guard let through.
// That is what the sentences that use it are about. It serves HTTPS with the
// test certificate the process trusts (trustIMAPTestCertificate), so a call
// made without the guard would land here and be counted, rather than fail on
// the certificate and look like a refusal.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"
)

// siteProbePath is the page the harness itself asks for, to check the site
// answers. It is not counted as a call.
const siteProbePath = "/sonde-du-banc"

// fakeSite is one sender's website.
type fakeSite struct {
	server *httptest.Server

	mu sync.Mutex
	// calls is every request the site took, as "METHOD /path", but the probe.
	calls []string
}

// startFakeSite runs a sender's website over HTTPS on 127.0.0.1 and stops it
// when the scenario ends.
func startFakeSite(a *testApp) *fakeSite {
	a.t.Helper()
	if imapTLSErr != nil || imapServerTLS == nil {
		a.fatalf("pas de faux site d'expéditeur possible : %v", imapTLSErr)
	}
	s := &fakeSite{}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(s.serve))
	srv.TLS = imapServerTLS
	srv.StartTLS()
	a.t.Cleanup(srv.Close)
	s.server = srv
	return s
}

func (s *fakeSite) serve(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	if r.URL.Path != siteProbePath {
		s.mu.Lock()
		s.calls = append(s.calls, r.Method+" "+r.URL.RequestURI())
		s.mu.Unlock()
	}
	w.WriteHeader(http.StatusOK)
}

// link is the address of a page of this site, as a header writes it:
// https://127.0.0.1:<port><path>.
func (s *fakeSite) link(path string) string {
	return s.server.URL + path
}

// expectReachable checks, from the test process and with the certificates it
// trusts, that the site answers: a site nobody can reach would pass every
// sentence about a call that must not happen.
func (s *fakeSite) expectReachable(a *testApp) {
	a.t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(s.link(siteProbePath))
	if err != nil {
		a.fatalf("le faux site de l'expéditeur (%s) ne répond pas : le scénario ne prouve rien (à corriger dans acceptance_unsubscribe_test.go) : %v", s.server.URL, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		a.fatalf("le faux site de l'expéditeur (%s) répond %d : le scénario ne prouve rien (à corriger dans acceptance_unsubscribe_test.go)", s.server.URL, resp.StatusCode)
	}
}

// received is every request the site took, oldest first, the probe aside.
func (s *fakeSite) received() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

// describeCalls is requests a site took, in a failure message.
func describeCalls(calls []string) string {
	if len(calls) == 0 {
		return "aucune requête"
	}
	return strings.Join(calls, ", ")
}
