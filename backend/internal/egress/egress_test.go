package egress

import (
	"errors"
	"net"
	"net/url"
	"testing"
)

// The address policy is the whole security value of this package, so every
// family is pinned rather than sampled. A regression here does not fail loudly:
// it quietly lets the server reach its own network again.
func TestAllowedIPRefusesEveryPrivateFamily(t *testing.T) {
	blocked := map[string]string{
		"127.0.0.1":        "loopback: the app's own API",
		"127.1.2.3":        "the rest of 127/8 is loopback too",
		"::1":              "IPv6 loopback",
		"0.0.0.0":          "unspecified",
		"0.1.2.3":          "0.0.0.0/8 is another spelling of this host on Linux",
		"10.0.0.1":         "RFC 1918",
		"172.16.0.1":       "RFC 1918, low end",
		"172.31.255.255":   "RFC 1918, high end",
		"192.168.1.1":      "RFC 1918",
		"169.254.169.254":  "the cloud metadata endpoint that hands out credentials",
		"169.254.0.1":      "link-local",
		"fe80::1":          "IPv6 link-local",
		"fc00::1":          "IPv6 unique-local",
		"fd12:3456::1":     "IPv6 unique-local",
		"100.64.0.1":       "carrier-grade NAT, where mesh VPNs put their peers",
		"100.127.255.255":  "carrier-grade NAT, high end",
		"224.0.0.1":        "multicast",
		"ff02::1":          "IPv6 multicast",
		"255.255.255.255":  "broadcast",
		"::ffff:127.0.0.1": "IPv4-mapped loopback, the bypass if the mapping is not unwrapped",
		"::ffff:10.0.0.1":  "IPv4-mapped RFC 1918",
	}
	for addr, why := range blocked {
		ip := net.ParseIP(addr)
		if ip == nil {
			t.Fatalf("test data: %q is not an IP", addr)
		}
		if err := AllowedIP(ip); !errors.Is(err, ErrPrivateAddress) {
			t.Errorf("AllowedIP(%s) = %v, want ErrPrivateAddress (%s)", addr, err, why)
		}
	}
}

// The other half: a policy that refuses everything would pass the test above
// and break every legitimate unsubscribe.
func TestAllowedIPAcceptsPublicAddresses(t *testing.T) {
	for _, addr := range []string{
		"8.8.8.8",
		"1.1.1.1",
		"93.184.216.34",
		"172.32.0.1",  // just outside RFC 1918
		"172.15.0.1",  // just below RFC 1918
		"100.63.0.1",  // just below carrier-grade NAT
		"100.128.0.1", // just above carrier-grade NAT
		"2606:2800:220:1:248:1893:25c8:1946",
	} {
		ip := net.ParseIP(addr)
		if ip == nil {
			t.Fatalf("test data: %q is not an IP", addr)
		}
		if err := AllowedIP(ip); err != nil {
			t.Errorf("AllowedIP(%s) = %v, want nil", addr, err)
		}
	}
}

func TestAllowedIPRefusesNothing(t *testing.T) {
	if err := AllowedIP(nil); !errors.Is(err, ErrPrivateAddress) {
		t.Errorf("AllowedIP(nil) = %v, want ErrPrivateAddress", err)
	}
}

func TestParseAcceptsAPublicHTTPSURL(t *testing.T) {
	for _, raw := range []string{
		"https://newsletter.example.com/unsub?id=abc",
		"https://example.com",
		"https://example.com:8443/unsub",
		"https://8.8.8.8/unsub",
	} {
		u, err := Parse(raw)
		if err != nil {
			t.Errorf("Parse(%q) = %v, want no error", raw, err)
			continue
		}
		if u == nil || u.String() != raw {
			t.Errorf("Parse(%q) returned %v, want the same url back", raw, u)
		}
	}
}

// http is refused on purpose, not by omission: RFC 8058 requires https, and
// allowing http would send the POST in the clear and put every http-only
// service on the internal network back within reach.
func TestParseRefusesEverySchemeButHTTPS(t *testing.T) {
	for _, raw := range []string{
		"http://example.com/unsub",
		"ftp://example.com/unsub",
		"file:///etc/passwd",
		"gopher://example.com",
		"mailto:unsub@example.com",
		"//example.com/unsub",
		"example.com/unsub",
	} {
		if _, err := Parse(raw); !errors.Is(err, ErrNotHTTPS) {
			t.Errorf("Parse(%q) = %v, want ErrNotHTTPS", raw, err)
		}
	}
}

func TestParseRefusesAURLWithNoHost(t *testing.T) {
	for _, raw := range []string{"https://", "https:///unsub"} {
		if _, err := Parse(raw); !errors.Is(err, ErrNoHost) {
			t.Errorf("Parse(%q) = %v, want ErrNoHost", raw, err)
		}
	}
}

// A literal private address must be refused by the URL check as well, so the
// caller gets the same verdict whether the header carried a name or an address.
func TestParseRefusesAPrivateIPLiteral(t *testing.T) {
	for _, raw := range []string{
		"https://127.0.0.1/unsub",
		"https://127.0.0.1:8080/api/metrics",
		"https://[::1]/unsub",
		"https://169.254.169.254/latest/meta-data/",
		"https://10.0.0.5:27017/",
	} {
		if _, err := Parse(raw); !errors.Is(err, ErrPrivateAddress) {
			t.Errorf("Parse(%q) = %v, want ErrPrivateAddress", raw, err)
		}
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := Parse("https://exa mple.com/\x7f"); err == nil {
		t.Error("Parse accepted an unparseable url, want an error")
	}
}

// Allowed is what every redirect hop goes through. A sender that cannot reach
// an internal service directly would otherwise reach it with a 302.
func TestAllowedJudgesARedirectHop(t *testing.T) {
	cases := []struct {
		raw     string
		wantErr error
		why     string
	}{
		{"https://example.com/step2", nil, ""},
		{"http://example.com/step2", ErrNotHTTPS, "a downgrade to cleartext is still a downgrade on hop two"},
		{"https://127.0.0.1/admin", ErrPrivateAddress, "the redirect is how an internal address is reached without naming it"},
		{"https://169.254.169.254/", ErrPrivateAddress, "metadata endpoint via redirect"},
	}
	for _, tc := range cases {
		u, err := url.Parse(tc.raw)
		if err != nil {
			t.Fatalf("test data: %v", err)
		}
		got := Allowed(u)
		if tc.wantErr == nil {
			if got != nil {
				t.Errorf("Allowed(%q) = %v, want nil", tc.raw, got)
			}
			continue
		}
		if !errors.Is(got, tc.wantErr) {
			msg := ""
			if tc.why != "" {
				msg = ": " + tc.why
			}
			t.Errorf("Allowed(%q) = %v, want %v%s", tc.raw, got, tc.wantErr, msg)
		}
	}
}

func TestAllowedRefusesNilURL(t *testing.T) {
	if err := Allowed(nil); !errors.Is(err, ErrNoHost) {
		t.Errorf("Allowed(nil) = %v, want ErrNoHost", err)
	}
}
