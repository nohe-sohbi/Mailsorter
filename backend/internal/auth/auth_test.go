package auth

import (
	"strings"
	"testing"
	"time"
)

func TestSessionRoundTrip(t *testing.T) {
	m := NewManager("test-secret")
	tok := m.IssueSession("alice@example.com")

	email, err := m.VerifySession(tok)
	if err != nil {
		t.Fatalf("VerifySession returned error: %v", err)
	}
	if email != "alice@example.com" {
		t.Fatalf("got email %q, want alice@example.com", email)
	}
}

func TestSessionRejectsTamperedPayload(t *testing.T) {
	m := NewManager("test-secret")
	tok := m.IssueSession("alice@example.com")

	// Flip a character in the payload portion; signature must no longer match.
	parts := strings.SplitN(tok, ".", 2)
	if len(parts) != 2 {
		t.Fatalf("unexpected token format: %q", tok)
	}
	tampered := mutate(parts[0]) + "." + parts[1]
	if _, err := m.VerifySession(tampered); err == nil {
		t.Fatal("expected error for tampered token, got nil")
	}
}

func TestSessionRejectsWrongSecret(t *testing.T) {
	tok := NewManager("secret-a").IssueSession("alice@example.com")
	if _, err := NewManager("secret-b").VerifySession(tok); err == nil {
		t.Fatal("expected token signed by another secret to be rejected")
	}
}

func TestSessionRejectsExpired(t *testing.T) {
	m := NewManager("test-secret")
	m.sessionTTL = -time.Minute // already expired
	tok := m.IssueSession("alice@example.com")
	if _, err := m.VerifySession(tok); err != ErrExpired {
		t.Fatalf("got %v, want ErrExpired", err)
	}
}

func TestVerifySessionMalformed(t *testing.T) {
	m := NewManager("test-secret")
	for _, bad := range []string{"", "nodot", ".", "abc.", ".abc"} {
		if _, err := m.VerifySession(bad); err == nil {
			t.Fatalf("expected error for malformed token %q", bad)
		}
	}
}

func TestStateRoundTrip(t *testing.T) {
	m := NewManager("test-secret")
	state := m.IssueState()
	if err := m.VerifyState(state); err != nil {
		t.Fatalf("VerifyState returned error: %v", err)
	}
}

func TestStateRejectsExpired(t *testing.T) {
	m := NewManager("test-secret")
	m.stateTTL = -time.Second
	state := m.IssueState()
	if err := m.VerifyState(state); err != ErrExpired {
		t.Fatalf("got %v, want ErrExpired", err)
	}
}

func TestStateAndSessionUseDistinctKeys(t *testing.T) {
	m := NewManager("test-secret")
	// A session token must not validate as a state value and vice versa.
	if err := m.VerifyState(m.IssueSession("a@b.com")); err == nil {
		t.Fatal("session token should not verify as OAuth state")
	}
	if _, err := m.VerifySession(m.IssueState()); err == nil {
		t.Fatal("OAuth state should not verify as session token")
	}
}

// mutate flips the first character so the result differs but stays a valid length.
func mutate(s string) string {
	if s == "" {
		return "x"
	}
	c := s[0]
	if c == 'A' {
		c = 'B'
	} else {
		c = 'A'
	}
	return string(c) + s[1:]
}
