package api

import (
	"strings"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/crypto"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
)

// A Google access token and a Google refresh token, shaped like the real thing.
// The refresh token matters for the legacy-detection test: it is base64-alphabet
// all the way through, which is exactly the value a "does it look encrypted?"
// heuristic would misread.
const (
	sampleAccess  = "ya29.a0AfH6SMBx-Not-A-Real-Token_0123456789"
	sampleRefresh = "1//0eKq7xNotARealRefreshToken0123456789ABCDEF"
)

func TestSealTokenHidesThePlaintext(t *testing.T) {
	h := newTestHandler(t)

	for _, plain := range []string{sampleAccess, sampleRefresh} {
		sealed, err := h.sealToken(plain)
		if err != nil {
			t.Fatalf("sealToken(%q) returned an error: %v", plain, err)
		}
		if strings.Contains(sealed, plain) {
			t.Errorf("sealToken(%q) = %q, the plaintext is still readable in the stored value", plain, sealed)
		}
		if !strings.HasPrefix(sealed, tokenCipherPrefix) {
			t.Errorf("sealToken(%q) = %q, want the %q marker so the reader knows it is encrypted", plain, sealed, tokenCipherPrefix)
		}

		got, legacy, err := h.openToken(sealed)
		if err != nil {
			t.Fatalf("openToken(sealed %q) returned an error: %v", plain, err)
		}
		if got != plain {
			t.Errorf("openToken(sealToken(%q)) = %q, want %q", plain, got, plain)
		}
		if legacy {
			t.Errorf("openToken(sealToken(%q)) reported legacy plaintext, want false", plain)
		}
	}
}

// Empty means "Google sent no refresh token this time, keep the stored one". It
// must stay empty rather than becoming the ciphertext of "", which would be
// written over a perfectly good stored token.
func TestSealTokenLeavesEmptyEmpty(t *testing.T) {
	h := newTestHandler(t)

	sealed, err := h.sealToken("")
	if err != nil {
		t.Fatalf(`sealToken("") returned an error: %v`, err)
	}
	if sealed != "" {
		t.Errorf(`sealToken("") = %q, want ""`, sealed)
	}
}

// The migration path: accounts created before encryption hold a bare token, and
// they must keep working, flagged so the caller can upgrade them in place.
func TestOpenTokenAcceptsLegacyPlaintext(t *testing.T) {
	h := newTestHandler(t)

	cases := map[string]string{
		"access token":  sampleAccess,
		"refresh token": sampleRefresh,
		"empty":         "",
	}
	for name, stored := range cases {
		t.Run(name, func(t *testing.T) {
			got, legacy, err := h.openToken(stored)
			if err != nil {
				t.Fatalf("openToken(%q) returned an error: %v", stored, err)
			}
			if got != stored {
				t.Errorf("openToken(%q) = %q, want it returned unchanged", stored, got)
			}
			// An empty value is nothing at all, not a legacy token to upgrade.
			if wantLegacy := stored != ""; legacy != wantLegacy {
				t.Errorf("openToken(%q) legacy = %v, want %v", stored, legacy, wantLegacy)
			}
		})
	}
}

// Sealing under one ENCRYPTION_KEY and reading under another must fail loudly.
// Returning the raw ciphertext as if it were a token would send Google an
// unintelligible string and surface as an opaque error far from the cause;
// getUserToken turns this error into a 401 that asks for a fresh grant.
func TestOpenTokenRejectsAnotherKeysCiphertext(t *testing.T) {
	h := newTestHandler(t)

	sealed, err := h.sealToken(sampleAccess)
	if err != nil {
		t.Fatalf("sealToken: %v", err)
	}

	other := newTestHandler(t)
	other.encryptor = crypto.NewEncryptor("a-completely-different-master-key-000000")

	got, _, err := other.openToken(sealed)
	if err == nil {
		t.Fatalf("openToken with the wrong key = %q, nil; want an error", got)
	}
	if got != "" {
		t.Errorf("openToken with the wrong key returned %q alongside its error, want an empty token", got)
	}
}

// userTokens is what every Gmail call goes through, so the pair has to survive
// a round trip together, in either storage state.
func TestUserTokensOpensBothTokens(t *testing.T) {
	h := newTestHandler(t)

	sealedAccess, err := h.sealToken(sampleAccess)
	if err != nil {
		t.Fatalf("sealToken: %v", err)
	}
	sealedRefresh, err := h.sealToken(sampleRefresh)
	if err != nil {
		t.Fatalf("sealToken: %v", err)
	}

	cases := map[string]models.User{
		"both encrypted": {Email: "a@example.com", AccessToken: sealedAccess, RefreshToken: sealedRefresh},
		"both legacy":    {Email: "a@example.com", AccessToken: sampleAccess, RefreshToken: sampleRefresh},
		"mixed":          {Email: "a@example.com", AccessToken: sealedAccess, RefreshToken: sampleRefresh},
	}
	for name, user := range cases {
		t.Run(name, func(t *testing.T) {
			// The re-seal write goes to a dead Mongo and is best effort, so it
			// cannot fail the read: that is the behaviour being pinned here.
			access, refresh, err := h.userTokens(cancelledContext(), user)
			if err != nil {
				t.Fatalf("userTokens returned an error: %v", err)
			}
			if access != sampleAccess {
				t.Errorf("access = %q, want %q", access, sampleAccess)
			}
			if refresh != sampleRefresh {
				t.Errorf("refresh = %q, want %q", refresh, sampleRefresh)
			}
		})
	}
}
