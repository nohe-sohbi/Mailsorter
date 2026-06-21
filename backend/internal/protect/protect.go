// Package protect implements Mailsorter's safety net: a per-user list of
// protected senders (VIP) whose emails must never be archived, trashed or
// deleted by an automated triage pass — be it the AI, a deterministic rule, the
// sender auto-pilot, or a bulk action.
//
// The whole package is pure (no I/O) so the guard logic can be tested
// exhaustively. Callers load the user's protected values once and ask Allowed
// before performing any destructive Gmail mutation.
package protect

import "strings"

// Destructive actions remove an email from the user's attention (out of the
// inbox or into the trash). These are the only actions the protection vetoes —
// labelling, starring or keeping a VIP's mail is always fine.
const (
	ActionArchive = "archive"
	ActionTrash   = "trash"
	ActionDelete  = "delete"
)

// destructive is the set of action verbs (across the AI, rules and direct-action
// vocabularies) that a protected sender shields against.
var destructive = map[string]bool{
	ActionArchive: true,
	ActionTrash:   true,
	ActionDelete:  true,
}

// IsDestructive reports whether an action would take a protected sender's email
// out of the inbox or trash it. Marking read, starring, labelling and keeping
// leave the email in place and are therefore never blocked.
func IsDestructive(action string) bool {
	return destructive[strings.ToLower(strings.TrimSpace(action))]
}

// Kind classifies a protected entry as a full address (contains "@" with a local
// part) or a bare domain. Used at write time to store a normalized value.
const (
	KindAddress = "address"
	KindDomain  = "domain"
)

// NormalizeAddress extracts the bare, lower-cased email address from a raw From
// header. It handles the "Display Name <addr@host>" form and trims stray
// punctuation, returning "" when no plausible address is present.
func NormalizeAddress(raw string) string {
	s := strings.TrimSpace(raw)
	if i := strings.LastIndex(s, "<"); i >= 0 {
		if j := strings.Index(s[i:], ">"); j >= 0 {
			s = s[i+1 : i+j]
		} else {
			s = s[i+1:]
		}
	}
	s = strings.TrimFunc(s, func(r rune) bool {
		return r == '"' || r == '\'' || r == ' ' || r == '\t'
	})
	return strings.ToLower(strings.TrimSpace(s))
}

// Domain returns the lower-cased domain part of an address (or of a bare domain
// value), or "" if none can be derived.
func Domain(raw string) string {
	addr := NormalizeAddress(raw)
	if i := strings.LastIndex(addr, "@"); i >= 0 {
		return strings.TrimSpace(addr[i+1:])
	}
	return strings.Trim(addr, "@")
}

// NormalizeEntry canonicalizes a value the user typed into the protection list
// and classifies it. An input with a local part ("a@b.com") becomes an address
// entry; a bare domain ("b.com" or "@b.com") becomes a domain entry. Returns an
// empty value when nothing usable remains.
func NormalizeEntry(raw string) (value, kind string) {
	s := strings.TrimSpace(raw)
	// Accept a raw From header ("Display Name <addr@host>") by stripping the
	// display-name wrapper down to the bare address first.
	if strings.Contains(s, "<") {
		s = NormalizeAddress(s)
	}
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimFunc(s, func(r rune) bool { return r == '"' || r == '\'' || r == ' ' })
	if s == "" {
		return "", ""
	}
	// A leading '@' or no local part means "whole domain".
	if strings.HasPrefix(s, "@") {
		d := strings.TrimPrefix(s, "@")
		if d == "" {
			return "", ""
		}
		return d, KindDomain
	}
	at := strings.Index(s, "@")
	switch {
	case at < 0:
		// No '@' at all → treat as a domain (e.g. "newsletter.com").
		return s, KindDomain
	case at == 0:
		return "", ""
	default:
		// Has a local part → full address.
		return s, KindAddress
	}
}

// Match reports whether the sender of an email is covered by any protected
// entry. Address entries match the exact address; domain entries match the
// address's domain or any subdomain of it (so "acme.com" also shields
// "mail.acme.com"). Entries are assumed already normalized via NormalizeEntry.
func Match(from string, entries []string) bool {
	if len(entries) == 0 {
		return false
	}
	addr := NormalizeAddress(from)
	if addr == "" {
		return false
	}
	domain := Domain(addr)
	for _, e := range entries {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		if strings.Contains(e, "@") {
			if e == addr {
				return true
			}
			continue
		}
		// Domain entry: exact domain or subdomain match.
		if domain == e || strings.HasSuffix(domain, "."+e) {
			return true
		}
	}
	return false
}

// Allowed reports whether action may be applied to an email from `from`, given
// the user's protected entries. Non-destructive actions are always allowed; a
// destructive action is blocked only when the sender is protected.
func Allowed(action, from string, entries []string) bool {
	if !IsDestructive(action) {
		return true
	}
	return !Match(from, entries)
}
