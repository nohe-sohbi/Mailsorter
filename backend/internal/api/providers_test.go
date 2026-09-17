package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
)

type providersPayload struct {
	Edition   string `json:"edition"`
	Providers []struct {
		Key    string `json:"key"`
		Name   string `json:"name"`
		Routes []struct {
			Transport    string   `json:"transport"`
			Auth         string   `json:"auth"`
			Autodiscover bool     `json:"autodiscover"`
			Blockers     []string `json:"blockers"`
			Note         string   `json:"note"`
			IMAP         *struct {
				Host string `json:"host"`
				Port int    `json:"port"`
				TLS  string `json:"tls"`
			} `json:"imap"`
			Capabilities struct {
				Labels         bool `json:"labels"`
				ProviderSearch bool `json:"providerSearch"`
				Send           bool `json:"send"`
			} `json:"capabilities"`
		} `json:"routes"`
	} `json:"providers"`
}

// withEdition runs fn with the package-level Edition set, then restores it.
// Edition is process-wide (like Version and AllowedOrigins), so a test that
// changes it must put it back or it leaks into every later test.
func withEdition(t *testing.T, e provider.Edition, fn func()) {
	t.Helper()
	previous := Edition
	Edition = e
	defer func() { Edition = previous }()
	fn()
}

func fetchProviders(t *testing.T, e provider.Edition) providersPayload {
	t.Helper()
	var payload providersPayload
	withEdition(t, e, func() {
		rec := httptest.NewRecorder()
		newTestHandler(t).GetProviders(rec, httptest.NewRequest(http.MethodGet, "/api/providers", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/providers = %d, want 200", rec.Code)
		}
		if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
			t.Fatalf("decode /api/providers: %v", err)
		}
	})
	return payload
}

// The connect screen renders whatever this returns, so what it returns must be
// exactly what the running edition can reach. A provider on screen that the
// backend cannot dial is the failure this endpoint exists to prevent.
func TestProvidersFollowTheRunningEdition(t *testing.T) {
	hosted := fetchProviders(t, provider.EditionHosted)
	if hosted.Edition != string(provider.EditionHosted) {
		t.Errorf("edition = %q, want %q", hosted.Edition, provider.EditionHosted)
	}
	for _, p := range hosted.Providers {
		if p.Key == provider.KeyProton {
			t.Error("the hosted catalog offers Proton, whose Bridge binds to 127.0.0.1 and cannot be dialed remotely")
		}
		for _, r := range p.Routes {
			if r.Transport == string(provider.TransportGmailAPI) {
				t.Errorf("the hosted catalog offers the Gmail API on %q: that route is capped at 100 authorizations for the life of the Cloud project", p.Key)
			}
		}
	}

	selfHosted := fetchProviders(t, provider.EditionSelfHosted)
	if len(selfHosted.Providers) <= len(hosted.Providers) {
		t.Errorf("self-hosted lists %d providers and hosted %d: self-hosted must offer strictly more, it is the edition that unlocks Proton and the Gmail API",
			len(selfHosted.Providers), len(hosted.Providers))
	}
	if !listsProvider(selfHosted, provider.KeyProton) {
		t.Error("the self-hosted catalog hides Proton, one of its few exclusive reasons to exist")
	}
}

func listsProvider(p providersPayload, key string) bool {
	for _, item := range p.Providers {
		if item.Key == key {
			return true
		}
	}
	return false
}

// The frontend must not reimplement bit arithmetic or guess a hostname, so the
// payload has to carry both spelled out.
func TestProvidersPayloadIsUsableByTheScreen(t *testing.T) {
	payload := fetchProviders(t, provider.EditionHosted)

	for _, p := range payload.Providers {
		if p.Key == "" || p.Name == "" || len(p.Routes) == 0 {
			t.Errorf("provider %+v is not renderable", p)
		}
		for _, r := range p.Routes {
			if r.Note == "" {
				t.Errorf("%q route has no note: the screen has nothing to explain with", p.Key)
			}
			if !r.Capabilities.Send {
				t.Errorf("%q route reports it cannot send, which no catalog route does", p.Key)
			}
			// A route that names no endpoint and does not admit to
			// autodiscovering would leave the screen promising settings it
			// cannot show.
			if r.Transport == string(provider.TransportIMAP) && r.IMAP == nil && !r.Autodiscover {
				t.Errorf("%q IMAP route has no host and is not marked autodiscover", p.Key)
			}
			if r.IMAP != nil && (r.IMAP.Host == "" || r.IMAP.Port == 0 || r.IMAP.TLS == "") {
				t.Errorf("%q IMAP endpoint is incomplete: %+v", p.Key, r.IMAP)
			}
		}
	}

	// Gmail over IMAP is the route that has to keep labels and Gmail search
	// through the X-GM-* extensions, and the screen decides which tabs to show
	// from exactly these two booleans.
	for _, p := range payload.Providers {
		if p.Key != provider.KeyGmail {
			continue
		}
		r := p.Routes[0]
		if !r.Capabilities.Labels || !r.Capabilities.ProviderSearch {
			t.Errorf("Gmail over IMAP reports labels=%v providerSearch=%v, want both true", r.Capabilities.Labels, r.Capabilities.ProviderSearch)
		}
		if len(r.Blockers) == 0 {
			t.Error("Gmail over IMAP reports no blocker, but it needs 2FA and can be refused from a datacenter IP")
		}
	}
}

// The SPA reads the edition at boot, before any login, to know whether there is
// anything to bill and which providers exist.
func TestConfigStatusCarriesTheEdition(t *testing.T) {
	withEdition(t, provider.EditionHosted, func() {
		rec := httptest.NewRecorder()
		newTestHandler(t).GetConfigStatus(rec, httptest.NewRequest(http.MethodGet, "/api/config/status", nil))

		var body map[string]interface{}
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode /api/config/status: %v", err)
		}
		if body["edition"] != string(provider.EditionHosted) {
			t.Errorf("edition = %v, want %q", body["edition"], provider.EditionHosted)
		}
	})
}

// The connect screen is the first thing a visitor sees in the self-hosted
// edition, so the catalog has to be readable without a session.
func TestProvidersIsPublic(t *testing.T) {
	srv := newRoutedTestServer(t)

	res, err := http.Get(srv.URL + "/api/providers")
	if err != nil {
		t.Fatalf("GET /api/providers: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("GET /api/providers without a session = %d, want 200: the connect screen runs before any login", res.StatusCode)
	}
}
