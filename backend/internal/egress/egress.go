// Package egress decides where Mailsorter is allowed to send an outbound
// request of its own.
//
// It exists for one reason: the RFC 8058 one-click unsubscribe is the only
// place in the app where a URL chosen by a STRANGER reaches the server's own
// HTTP client. The address comes from the List-Unsubscribe header of a received
// email, so anyone who can send mail to a Mailsorter user picks it. Before this
// package the only check was the scheme, which meant a crafted header could aim
// the server at its own network: the datastore on the Docker bridge, the API on
// loopback, or 169.254.169.254, the link-local address that on most hosting
// platforms serves instance credentials. That is a server-side request forgery,
// and the damage is done by the server, with the server's network position,
// before any user sees anything.
//
// So the rule lives here, once, as data and predicates rather than as a
// condition rewritten at each call site. Two checks, deliberately separate
// because they happen at different moments:
//
//   - Allowed and Parse judge a URL: the scheme and the shape. They run before
//     anything is resolved, and again on every redirect hop.
//   - AllowedIP judges a resolved address. It runs from the dialer, on the
//     socket's actual peer, which is what closes the DNS rebinding gap: a name
//     that answers with a public address on the first lookup and a private one
//     on the second is caught at the second, because the address checked is the
//     address connected to.
//
// The package is pure. url.Parse and net.ParseIP do no I/O, and nothing here
// opens a connection or resolves a name: that belongs to the caller's dialer,
// which asks this package for the verdict.
package egress

import (
	"errors"
	"fmt"
	"net"
	"net/url"
)

// ErrNotHTTPS is returned for any scheme but https. RFC 8058 requires https for
// the one-click POST, and plain http would both send the request in the clear
// and put every http-only service on the internal network back in reach.
var ErrNotHTTPS = errors.New("egress: only https is allowed")

// ErrNoHost is returned for a URL with nothing to connect to.
var ErrNoHost = errors.New("egress: url has no host")

// ErrPrivateAddress is returned for an address that is not publicly routable.
// It wraps the address so a refusal is readable in the logs: a blocked hop is
// either a broken sender or an attack, and the two are told apart by where it
// was pointing.
var ErrPrivateAddress = errors.New("egress: address is not publicly routable")

// Parse parses raw and returns it when Allowed accepts it.
func Parse(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("egress: %w", err)
	}
	if err := Allowed(u); err != nil {
		return nil, err
	}
	return u, nil
}

// Allowed reports whether Mailsorter may send a request to this URL. It judges
// the scheme and the shape only: the address is judged by AllowedIP once it is
// known. Redirect hops go through here too, because a sender that cannot reach
// an internal service directly would otherwise reach it with a 302.
func Allowed(u *url.URL) error {
	if u == nil {
		return ErrNoHost
	}
	if u.Scheme != "https" {
		return fmt.Errorf("%w: got %q", ErrNotHTTPS, u.Scheme)
	}
	if u.Hostname() == "" {
		return ErrNoHost
	}
	// A bare IP literal is judged now as well as at dial time. Dialing would
	// catch it anyway, but refusing here makes the intent explicit and keeps the
	// error the caller sees the same whether the host was a name or an address.
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		return AllowedIP(ip)
	}
	return nil
}

// AllowedIP reports whether a resolved address may be dialled. Call it from a
// dialer's Control hook so the address judged is the address connected to.
func AllowedIP(ip net.IP) error {
	if ip == nil || !publiclyRoutable(ip) {
		return fmt.Errorf("%w: %v", ErrPrivateAddress, ip)
	}
	return nil
}

// publiclyRoutable is the whole policy: anything that is not plainly on the
// public internet is refused. Erring towards refusal is the right side to err
// on here, because the cost of a false negative is an unsubscribe the user
// finishes by hand in their browser, while the cost of a false positive is the
// server attacking its own network.
//
// Note what each family costs to leave out. Loopback is the app's own API and
// anything else bound locally. The private ranges are the Docker bridge, so the
// datastore. Link-local carries 169.254.169.254, the cloud metadata endpoint
// that hands out credentials. The carrier-grade NAT range is where mesh VPNs
// such as Tailscale put their peers. And 0.0.0.0/8 matters on its own: Linux
// treats 0.x.x.x as this host, so http://0.0.0.0/ is another spelling of
// loopback that IsUnspecified alone does not catch.
//
// IPv4-mapped IPv6 needs no special case: the standard library's predicates
// unwrap it, so ::ffff:127.0.0.1 reads as loopback rather than as a public
// v6 address.
func publiclyRoutable(ip net.IP) bool {
	switch {
	case ip.IsUnspecified(),
		ip.IsLoopback(),
		ip.IsPrivate(),
		ip.IsLinkLocalUnicast(),
		ip.IsLinkLocalMulticast(),
		ip.IsInterfaceLocalMulticast(),
		ip.IsMulticast():
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		// 0.0.0.0/8 ("this network") and 100.64.0.0/10 (carrier-grade NAT).
		if v4[0] == 0 {
			return false
		}
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return false
		}
		// 255.255.255.255 and the rest of the reserved broadcast space.
		if v4.Equal(net.IPv4bcast) {
			return false
		}
	}
	return true
}
