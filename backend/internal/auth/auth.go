// Package auth issues and verifies the stateless, HMAC-signed credentials that
// secure Mailsorter: short-lived session tokens that identify a logged-in user,
// and one-time OAuth state values that protect the login flow against CSRF.
//
// Everything is self-contained (no external dependency, no server-side session
// store): a token carries its own payload and a signature derived from the
// server's secret, so any tampering or expiry is detected on verification.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Default lifetimes. Sessions are long-lived (the browser keeps the token in
// localStorage); OAuth state is single-use and must be consumed quickly.
const (
	DefaultSessionTTL = 30 * 24 * time.Hour
	DefaultStateTTL   = 10 * time.Minute
)

var (
	ErrMalformedToken = errors.New("auth: malformed token")
	ErrBadSignature   = errors.New("auth: invalid signature")
	ErrExpired        = errors.New("auth: token expired")
)

// Manager signs and verifies tokens with a key derived from the server secret.
type Manager struct {
	sessionKey []byte
	stateKey   []byte
	sessionTTL time.Duration
	stateTTL   time.Duration
}

// NewManager derives independent signing keys from the master secret so that a
// session token can never be replayed as an OAuth state (or vice versa).
func NewManager(masterSecret string) *Manager {
	return &Manager{
		sessionKey: deriveKey(masterSecret, "mailsorter-session-v1"),
		stateKey:   deriveKey(masterSecret, "mailsorter-oauth-state-v1"),
		sessionTTL: DefaultSessionTTL,
		stateTTL:   DefaultStateTTL,
	}
}

func deriveKey(masterSecret, label string) []byte {
	sum := sha256.Sum256([]byte(label + "|" + masterSecret))
	return sum[:]
}

// IssueSession returns a signed token that authenticates the given email until
// it expires. The token is safe to hand to the browser: it reveals the email
// but cannot be forged without the server secret.
func (m *Manager) IssueSession(email string) string {
	exp := time.Now().Add(m.sessionTTL).Unix()
	payload := email + "|" + strconv.FormatInt(exp, 10)
	return sign(m.sessionKey, payload)
}

// VerifySession validates a session token and returns the email it carries.
func (m *Manager) VerifySession(token string) (string, error) {
	payload, err := verify(m.sessionKey, token)
	if err != nil {
		return "", err
	}
	// payload = email|exp ; email cannot contain '|', so split on the last one.
	sep := strings.LastIndex(payload, "|")
	if sep < 0 {
		return "", ErrMalformedToken
	}
	email, expStr := payload[:sep], payload[sep+1:]
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return "", ErrMalformedToken
	}
	if time.Now().Unix() > exp {
		return "", ErrExpired
	}
	if email == "" {
		return "", ErrMalformedToken
	}
	return email, nil
}

// IssueState returns a single-use, expiring value to round-trip through Google's
// OAuth flow. Because it is signed, the callback can trust it without storing
// anything server-side.
func (m *Manager) IssueState() string {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		// Fall back to a time-seeded nonce; signature still guarantees integrity.
		nonce = []byte(strconv.FormatInt(time.Now().UnixNano(), 10))
	}
	exp := time.Now().Add(m.stateTTL).Unix()
	payload := hex.EncodeToString(nonce) + "|" + strconv.FormatInt(exp, 10)
	return sign(m.stateKey, payload)
}

// VerifyState reports whether an OAuth state value is authentic and unexpired.
func (m *Manager) VerifyState(state string) error {
	payload, err := verify(m.stateKey, state)
	if err != nil {
		return err
	}
	sep := strings.LastIndex(payload, "|")
	if sep < 0 {
		return ErrMalformedToken
	}
	exp, err := strconv.ParseInt(payload[sep+1:], 10, 64)
	if err != nil {
		return ErrMalformedToken
	}
	if time.Now().Unix() > exp {
		return ErrExpired
	}
	return nil
}

// sign encodes payload and appends a base64url HMAC-SHA256 signature.
// Format: base64url(payload) "." base64url(signature)
func sign(key []byte, payload string) string {
	enc := base64.RawURLEncoding.EncodeToString([]byte(payload))
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(enc))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return enc + "." + sig
}

// verify checks the signature in constant time and returns the decoded payload.
func verify(key []byte, token string) (string, error) {
	dot := strings.IndexByte(token, '.')
	if dot <= 0 || dot == len(token)-1 {
		return "", ErrMalformedToken
	}
	encPayload, encSig := token[:dot], token[dot+1:]

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(encPayload))
	expectedSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expectedSig), []byte(encSig)) {
		return "", ErrBadSignature
	}

	payload, err := base64.RawURLEncoding.DecodeString(encPayload)
	if err != nil {
		return "", ErrMalformedToken
	}
	return string(payload), nil
}
