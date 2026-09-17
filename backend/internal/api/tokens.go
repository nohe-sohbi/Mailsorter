package api

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"golang.org/x/oauth2"
)

// Google's OAuth credentials are the most dangerous thing Mailsorter stores: an
// access token plus a refresh token is a durable, unattended key to somebody's
// entire mailbox, and unlike a password its owner cannot rotate it without
// noticing the app has broken. They used to be written to the users collection
// verbatim, so any stray database copy handed over every connected mailbox with
// no key required, while .env.example claimed ENCRYPTION_KEY already protected
// them.
//
// This file closes that gap and is the only place allowed to: nothing writes a
// Google token to Mongo except through sealToken, and nothing reads one back
// except through openToken.

// tokenCipherPrefix marks a stored token as encrypted.
//
// It exists so the migration needs neither downtime nor a backfill script: a
// value without the prefix is a legacy plaintext token, still usable and
// re-sealed the first time it is read. Sniffing the ciphertext shape instead
// would be guesswork, because a Google refresh token is itself base64-shaped and
// would sometimes decode into garbage rather than fail outright.
const tokenCipherPrefix = "enc:v1:"

// sealToken encrypts a token for storage.
//
// An empty token stays empty rather than becoming ciphertext of "": Google hands
// back a refresh token only on the first authorization, so "" means "keep what is
// already on file", never "store this".
func (h *Handler) sealToken(plain string) (string, error) {
	sealed, err := h.sealSecret(plain)
	if err != nil {
		return "", fmt.Errorf("seal oauth token: %w", err)
	}
	return sealed, nil
}

// sealSecret is the same machinery under a name that does not say "oauth",
// because a Google token is no longer the only credential Mailsorter stores: a
// mailbox connected over IMAP is held open by an app password, which is exactly
// as dangerous and needs exactly this treatment.
func (h *Handler) sealSecret(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	ciphertext, err := h.encryptor.Encrypt(plain)
	if err != nil {
		return "", err
	}
	return tokenCipherPrefix + ciphertext, nil
}

// openSecret is openToken WITHOUT the legacy-plaintext path, and that
// difference is the point. A Google token predates the encryption and so an
// unprefixed value is a real, usable legacy token. No app password was ever
// stored in the clear, so an unprefixed value here is corruption or tampering,
// and handing it back as if it were a password would send it to a mail server.
func (h *Handler) openSecret(stored string) (string, error) {
	if stored == "" {
		return "", nil
	}
	if !strings.HasPrefix(stored, tokenCipherPrefix) {
		return "", fmt.Errorf("open secret: stored value is not sealed")
	}
	plain, err := h.encryptor.Decrypt(strings.TrimPrefix(stored, tokenCipherPrefix))
	if err != nil {
		return "", fmt.Errorf("open secret: %w", err)
	}
	return plain, nil
}

// openToken returns the usable token behind a stored value, and reports whether
// that value was still legacy plaintext.
//
// A decryption failure is deliberately NOT passed off as plaintext: it means the
// bytes were sealed under a different ENCRYPTION_KEY, and the only honest answer
// is that this account no longer has usable credentials. Returning the raw
// ciphertext as if it were a token would send Google an unintelligible string and
// surface as an opaque failure much further away.
func (h *Handler) openToken(stored string) (plain string, legacy bool, err error) {
	if stored == "" {
		return "", false, nil
	}
	if !strings.HasPrefix(stored, tokenCipherPrefix) {
		return stored, true, nil
	}
	plain, err = h.encryptor.Decrypt(strings.TrimPrefix(stored, tokenCipherPrefix))
	if err != nil {
		return "", false, fmt.Errorf("open oauth token: %w", err)
	}
	return plain, false, nil
}

// userTokens returns a stored user's Google credentials in the clear, upgrading
// any legacy plaintext value in place. The migration therefore completes on its
// own, one account at a time, on first use.
func (h *Handler) userTokens(ctx context.Context, user models.User) (access, refresh string, err error) {
	access, accessLegacy, err := h.openToken(user.AccessToken)
	if err != nil {
		return "", "", err
	}
	refresh, refreshLegacy, err := h.openToken(user.RefreshToken)
	if err != nil {
		return "", "", err
	}
	if accessLegacy || refreshLegacy {
		h.resealUserTokens(ctx, user.Email, access, refresh)
	}
	return access, refresh, nil
}

// resealUserTokens rewrites a legacy plaintext pair as ciphertext.
//
// Best effort on purpose: failing to upgrade must not break a request that is
// already holding working credentials, and the next read simply tries again. It
// is logged rather than swallowed so a persistent failure is visible.
func (h *Handler) resealUserTokens(ctx context.Context, email, access, refresh string) {
	sealedAccess, err := h.sealToken(access)
	if err != nil {
		log.Printf("reseal oauth tokens for %s: %v", email, err)
		return
	}
	sealedRefresh, err := h.sealToken(refresh)
	if err != nil {
		log.Printf("reseal oauth tokens for %s: %v", email, err)
		return
	}

	set := bson.M{"accessToken": sealedAccess, "updatedAt": time.Now()}
	// Same rule as the OAuth callback: an empty refresh token is never written
	// over a stored one, or the account loses the only thing that can renew it.
	if sealedRefresh != "" {
		set["refreshToken"] = sealedRefresh
	}

	if _, err := h.db.Users().UpdateOne(ctx, bson.M{"email": email}, bson.M{"$set": set}); err != nil {
		log.Printf("reseal oauth tokens for %s: %v", email, err)
	}
}

// getUserToken returns a usable Gmail OAuth token for the user, refreshing and
// persisting it when the stored access token has expired.
func (h *Handler) getUserToken(ctx context.Context, userEmail string) (*oauth2.Token, error) {
	var user models.User
	err := h.db.Users().FindOne(ctx, bson.M{"email": userEmail}).Decode(&user)
	if err != nil {
		return nil, err
	}

	access, refresh, err := h.userTokens(ctx, user)
	if err != nil {
		// The stored credentials cannot be read back at all. Nothing can revive
		// them, so ask for a fresh grant instead of failing with a 500 the user
		// has no way out of.
		log.Printf("getUserToken for %s: %v", userEmail, err)
		return nil, errReauthRequired
	}

	token := &oauth2.Token{
		AccessToken:  access,
		RefreshToken: refresh,
		Expiry:       user.TokenExpiry,
	}

	// Refresh if expired. If we can't mint a fresh token (no refresh token, or the
	// refresh was rejected), surface errReauthRequired so callers answer 401 and
	// the SPA restarts OAuth. Returning the dead token here would instead yield a
	// stream of opaque 500s the user can never escape.
	if token.Expiry.Before(time.Now()) {
		if refresh == "" {
			return nil, errReauthRequired
		}
		newToken, rerr := h.gmailService.RefreshToken(refresh)
		if rerr != nil {
			return nil, errReauthRequired
		}
		token = newToken

		sealed, serr := h.sealToken(newToken.AccessToken)
		if serr != nil {
			// The refreshed token is good for this request; only storing it
			// failed, which costs one extra refresh next time and nothing else.
			log.Printf("getUserToken for %s: %v", userEmail, serr)
			return token, nil
		}
		h.db.Users().UpdateOne(ctx, bson.M{"email": userEmail}, bson.M{
			"$set": bson.M{
				"accessToken": sealed,
				"tokenExpiry": newToken.Expiry,
				"updatedAt":   time.Now(),
			},
		})
	}

	return token, nil
}
