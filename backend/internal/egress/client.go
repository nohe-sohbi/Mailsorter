package egress

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// GuardedTransport is a transport that may only connect where AllowedIP says.
//
// This is the one part of the package that is not pure, and it is here so the
// dialer is written once: every client that sends a request to an address a
// stranger or a user chose (the one-click unsubscribe, a personal AI
// provider's base URL) uses this transport, and none of them can forget the
// check or get it subtly different.
//
// The Control hook is the load-bearing part. It runs after the name is
// resolved and before the socket connects, on the address actually being
// dialled, which is the only place the check cannot be raced: a host that
// answers with a public address when it is validated and a private one when it
// is connected to (DNS rebinding) is caught here, and again on every redirect
// hop, because each hop dials again.
//
// It sets no Proxy on purpose. A proxy would be the address dialled, so the
// check would judge the proxy and wave through whatever it is asked to reach.
func GuardedTransport() *http.Transport {
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 5 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			return AllowedIP(net.ParseIP(host))
		},
	}
	return &http.Transport{
		DialContext:         dialer.DialContext,
		TLSHandshakeTimeout: 5 * time.Second,
	}
}

// NewClient is an HTTP client for a URL that is not a constant: it connects
// only through GuardedTransport, re-judges every redirect hop with Allowed, and
// stops after maxRedirects of them.
func NewClient(timeout time.Duration, maxRedirects int) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: GuardedTransport(),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("egress: stopped after %d redirects", maxRedirects)
			}
			return Allowed(req.URL)
		},
	}
}
