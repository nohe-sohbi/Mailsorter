package provider

import (
	"errors"
	"strings"
	"testing"
)

// THE invariant. A shared OAuth client is capped by Google at 100
// authorizations for the lifetime of the Cloud project, and the cap cannot be
// reset. The hosted edition therefore must never resolve to the Gmail API,
// whatever anyone later writes in the catalog.
func TestHostedEditionNeverReachesTheGmailAPI(t *testing.T) {
	for _, p := range ForEdition(EditionHosted) {
		for _, r := range p.Routes {
			if r.Transport == TransportGmailAPI {
				t.Errorf("provider %q offers the Gmail API in the hosted edition: that route is capped at 100 authorizations for the life of the Cloud project", p.Key)
			}
		}
	}

	// And the two Google providers must still be reachable there, by IMAP.
	for _, key := range []string{KeyGmail, KeyWorkspace} {
		route, err := Pick(key, EditionHosted)
		if err != nil {
			t.Fatalf("Pick(%q, hosted) = %v, want a usable route: the hosted edition must still serve Google users", key, err)
		}
		if route.Transport != TransportIMAP || route.Auth != AuthAppPassword {
			t.Errorf("Pick(%q, hosted) = %s/%s, want imap/app-password", key, route.Transport, route.Auth)
		}
	}
}

// The self-hosted edition exists to unlock what the hosted one cannot do. If
// these two stop being true, it has no reason to exist.
func TestSelfHostedUnlocksTheGmailAPIAndProton(t *testing.T) {
	route, err := Pick(KeyGmail, EditionSelfHosted)
	if err != nil {
		t.Fatalf("Pick(gmail, self-hosted) = %v, want the full API route", err)
	}
	if route.Transport != TransportGmailAPI {
		t.Errorf("Pick(gmail, self-hosted) transport = %s, want %s: the user's own Cloud project is what buys full fidelity", route.Transport, TransportGmailAPI)
	}
	if !route.Caps.Has(CapLabels | CapProviderSearch | CapStableIDs) {
		t.Error("the Gmail API route must declare labels, provider search and stable ids: they are the reason to prefer it")
	}

	if _, err := Pick(KeyProton, EditionSelfHosted); err != nil {
		t.Errorf("Pick(proton, self-hosted) = %v, want a route: Bridge runs on the same machine", err)
	}
	if _, err := Pick(KeyProton, EditionHosted); !errors.Is(err, ErrNotInEdition) {
		t.Errorf("Pick(proton, hosted) = %v, want ErrNotInEdition: Bridge binds to 127.0.0.1 and no remote server can dial it", err)
	}
}

// Taking Gmail over IMAP with OAuth would require the mail.google.com scope,
// the broadest restricted scope Google publishes: every audit obligation the
// project is escaping, plus wider access than it has today. The catalog must
// not contain that combination anywhere.
func TestNoOAuthOverIMAPForGoogle(t *testing.T) {
	for _, key := range []string{KeyGmail, KeyWorkspace} {
		p, err := Lookup(key)
		if err != nil {
			t.Fatalf("Lookup(%q): %v", key, err)
		}
		for _, r := range p.Routes {
			if r.Transport == TransportIMAP && r.Auth == AuthOAuth {
				t.Errorf("%q declares an OAuth-over-IMAP route: that needs the restricted mail.google.com scope, which is strictly worse than the current position", key)
			}
		}
	}
}

// Every route must be reachable by somebody, carry a transport and an auth
// method, and explain itself. A route nobody can use is dead data that reads
// like a supported provider.
func TestEveryRouteIsWellFormed(t *testing.T) {
	seenKeys := map[string]bool{}

	for _, p := range All() {
		if p.Key == "" || p.Name == "" {
			t.Errorf("provider %+v has no key or no name", p)
		}
		if seenKeys[p.Key] {
			t.Errorf("duplicate provider key %q", p.Key)
		}
		seenKeys[p.Key] = true

		if len(p.Routes) == 0 {
			t.Errorf("provider %q has no route", p.Key)
		}

		for i, r := range p.Routes {
			where := p.Key + " route " + string(rune('0'+i))
			if len(r.Editions) == 0 {
				t.Errorf("%s is offered in no edition, so it can never be used", where)
			}
			if r.Transport == "" || r.Auth == "" {
				t.Errorf("%s has no transport or no auth method", where)
			}
			if strings.TrimSpace(r.Note) == "" {
				t.Errorf("%s has no note: every route carries something a reader would otherwise learn from an incident", where)
			}
			// The digest sends through the user's own account, so every route
			// has to be able to send. A route that cannot is a silently broken
			// feature rather than a degraded one.
			if !r.Caps.Has(CapServerSend) {
				t.Errorf("%s cannot send: the daily digest has no other way out", where)
			}
			// An API transport resolves its own endpoints; an IMAP one either
			// carries both or is resolved by autodiscovery. Carrying only one
			// is the state that fails at connect time.
			if r.Transport != TransportIMAP && (r.IMAP != nil || r.SMTP != nil) {
				t.Errorf("%s is an API transport but carries IMAP or SMTP endpoints", where)
			}
			if r.Transport == TransportIMAP && !r.Autodiscover() {
				checkEndpoint(t, where+" IMAP", r.IMAP)
				checkEndpoint(t, where+" SMTP", r.SMTP)
			}
		}
	}
}

func checkEndpoint(t *testing.T, where string, e *Endpoint) {
	t.Helper()
	if e == nil {
		t.Errorf("%s endpoint is nil on a non-autodiscovering route", where)
		return
	}
	if e.Host == "" {
		t.Errorf("%s has no host", where)
	}
	if e.Port <= 0 || e.Port > 65535 {
		t.Errorf("%s port = %d, want a real port", where, e.Port)
	}
	if e.TLS != TLSImplicit && e.TLS != TLSSTARTTLS {
		t.Errorf("%s TLS mode = %q, want implicit or starttls: an unset mode dials plaintext", where, e.TLS)
	}
}

// Getting the TLS mode wrong is the classic connect failure that looks like a
// bad password, so the conventional port-to-mode pairing is pinned.
func TestEndpointPortsMatchTheirTLSMode(t *testing.T) {
	implicitPorts := map[int]bool{993: true, 465: true}
	starttlsPorts := map[int]bool{143: true, 587: true, 25: true}

	for _, p := range All() {
		for _, r := range p.Routes {
			for name, e := range map[string]*Endpoint{"IMAP": r.IMAP, "SMTP": r.SMTP} {
				if e == nil {
					continue
				}
				// Proton Bridge listens on its own local ports and follows no
				// convention; it is exempt by design.
				if e.Host == "127.0.0.1" {
					continue
				}
				if e.TLS == TLSImplicit && starttlsPorts[e.Port] {
					t.Errorf("%s %s %s:%d declares implicit TLS on a STARTTLS port", p.Key, name, e.Host, e.Port)
				}
				if e.TLS == TLSSTARTTLS && implicitPorts[e.Port] {
					t.Errorf("%s %s %s:%d declares STARTTLS on an implicit-TLS port", p.Key, name, e.Host, e.Port)
				}
			}
		}
	}
}

// A user types an address; they should not also have to pick from a list.
func TestDetectFromAddress(t *testing.T) {
	cases := map[string]string{
		"nohe@gmail.com":           KeyGmail,
		"NOHE@GMail.COM":           KeyGmail,
		"x@googlemail.com":         KeyGmail,
		"x@orange.fr":              KeyOrange,
		"x@wanadoo.fr":             KeyOrange,
		"x@laposte.net":            KeyLaPoste,
		"x@hotmail.fr":             KeyOutlook,
		"x@outlook.com":            KeyOutlook,
		"x@icloud.com":             KeyICloud,
		"x@proton.me":              KeyProton,
		"x@une-boite-inconnue.tld": KeyGeneric,
		"pas-une-adresse":          KeyGeneric,
		"trailing@":                KeyGeneric,
		"":                         KeyGeneric,
	}
	for address, want := range cases {
		if got := Detect(address).Key; got != want {
			t.Errorf("Detect(%q) = %q, want %q", address, got, want)
		}
	}
}

// A domain must not point at two providers, or Detect's answer depends on
// catalog order rather than on the address.
func TestDomainsAreUnambiguous(t *testing.T) {
	owner := map[string]string{}
	for _, p := range All() {
		for _, d := range p.Domains {
			if d != strings.ToLower(d) {
				t.Errorf("provider %q declares domain %q with uppercase: Detect lowercases before matching, so it would never hit", p.Key, d)
			}
			if prev, ok := owner[d]; ok {
				t.Errorf("domain %q is claimed by both %q and %q", d, prev, p.Key)
			}
			owner[d] = p.Key
		}
	}
}

// ForEdition feeds the connect screen. It must never show a provider the
// running edition cannot reach, and never show a route it cannot use.
func TestForEditionHidesWhatItCannotServe(t *testing.T) {
	for _, e := range []Edition{EditionSelfHosted, EditionHosted} {
		list := ForEdition(e)
		if len(list) == 0 {
			t.Fatalf("ForEdition(%s) is empty", e)
		}
		for _, p := range list {
			if len(p.Routes) == 0 {
				t.Errorf("ForEdition(%s) returned %q with no route", e, p.Key)
			}
			for _, r := range p.Routes {
				if !r.Offers(e) {
					t.Errorf("ForEdition(%s) returned a %s/%s route on %q that this edition cannot use", e, r.Transport, r.Auth, p.Key)
				}
			}
		}
	}

	// Proton is the visible difference between the two editions.
	if has(ForEdition(EditionHosted), KeyProton) {
		t.Error("the hosted connect screen offers Proton, which it can never actually reach")
	}
	if !has(ForEdition(EditionSelfHosted), KeyProton) {
		t.Error("the self-hosted connect screen hides Proton, which is one of its few exclusive reasons to exist")
	}
}

func has(list []Provider, key string) bool {
	for _, p := range list {
		if p.Key == key {
			return true
		}
	}
	return false
}

// Routes are ordered best first and Pick relies on it, so a route that stores a
// full-mailbox password must never sit ahead of a revocable token.
func TestRoutesAreOrderedBestFirst(t *testing.T) {
	rank := map[AuthMethod]int{AuthOAuth: 0, AuthAppPassword: 1, AuthPassword: 2}
	for _, p := range All() {
		for i := 1; i < len(p.Routes); i++ {
			prev, cur := p.Routes[i-1], p.Routes[i]
			if rank[cur.Auth] < rank[prev.Auth] {
				t.Errorf("provider %q lists %s before %s: Pick takes the first offered route, so the less safe credential would win", p.Key, prev.Auth, cur.Auth)
			}
		}
	}
}

func TestLookupRejectsAnUnknownKey(t *testing.T) {
	if _, err := Lookup("nope"); !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("Lookup(nope) = %v, want ErrUnknownProvider", err)
	}
	if _, err := Pick("nope", EditionHosted); !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("Pick(nope) = %v, want ErrUnknownProvider", err)
	}
}

// The generic entry is the fallback Detect returns for every unrecognized
// domain, so its absence would turn a miss into a provider with no routes.
func TestGenericFallbackExistsInBothEditions(t *testing.T) {
	for _, e := range []Edition{EditionSelfHosted, EditionHosted} {
		r, err := Pick(KeyGeneric, e)
		if err != nil {
			t.Fatalf("Pick(generic, %s) = %v, want the IMAP fallback", e, err)
		}
		if r.Transport != TransportIMAP {
			t.Errorf("the generic fallback is %s, want imap", r.Transport)
		}
		if !r.Autodiscover() {
			t.Error("the generic fallback must autodiscover: it exists precisely for hosts the catalog does not know")
		}
	}
}

func TestCapabilityHas(t *testing.T) {
	both := CapLabels | CapServerSend
	if !both.Has(CapLabels) || !both.Has(CapServerSend) || !both.Has(both) {
		t.Error("Has must be true for each present capability and for the whole set")
	}
	if both.Has(CapPush) || both.Has(CapLabels|CapPush) {
		t.Error("Has must be false as soon as one capability of the set is missing")
	}
	if (Capability(0)).Has(CapLabels) {
		t.Error("the empty capability set has nothing")
	}
}
