// Package protect implements Mailsorter's safety net: a per-user list of
// protected senders (VIP) whose emails must never be archived, trashed or
// deleted by an automated triage pass, be it the AI, a deterministic rule, the
// sender auto-pilot, or a bulk action. On a mailbox made of folders (IMAP),
// where a label is a folder, they are not filed into one either
// (AllowedOnFolders).
//
// The whole package is pure (no I/O) so the guard logic can be tested
// exhaustively. Callers load the user's protected values once and ask Allowed
// before performing any destructive Gmail mutation.
//
// It also says which mails a sweep that names a sender by its ADDRESS may reach
// (SameAddress). That lives here because it is the same question the shield
// answers for an address entry, read through the same NormalizeAddress: the
// sweep and the guard in front of it cannot disagree about who the sender is.
package protect

import (
	"io"
	"mime"
	"net/mail"
	"strings"
)

// Destructive actions remove an email from the user's attention (out of the
// inbox or into the trash). These are the only actions the protection vetoes:
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

// ActionLabel is filing an email under a label.
const ActionLabel = "label"

// IsDestructiveOnFolders is IsDestructive for a mailbox made of folders (IMAP),
// where a label is the folder of that name: labelling an email files it there,
// out of the inbox, the way archiving does. So there it is shielded too.
func IsDestructiveOnFolders(action string) bool {
	return IsDestructive(action) || strings.ToLower(strings.TrimSpace(action)) == ActionLabel
}

// AllowedOnFolders is Allowed for a mailbox made of folders (IMAP): a
// protected sender's email is not filed away there either.
func AllowedOnFolders(action, from string, entries []string) bool {
	if !IsDestructiveOnFolders(action) {
		return true
	}
	return !shielded(from, entries)
}

// Kind classifies a protected entry as a full address (contains "@" with a local
// part) or a bare domain. Used at write time to store a normalized value.
const (
	KindAddress = "address"
	KindDomain  = "domain"
)

// fromParser reads a From header the way RFC 5322 says (net/mail: quoted
// names, comments, encoded words). A name encoded in a charset net/mail does
// not decode (windows-1252, iso-8859-15) is kept as its bytes: the name is
// not what is read here, and refusing the whole header for it would leave a
// French sender with no address.
var fromParser = mail.AddressParser{WordDecoder: &mime.WordDecoder{
	CharsetReader: func(_ string, input io.Reader) (io.Reader, error) { return input, nil },
}}

// Address is the address a From header names: the addr-spec, never the
// display name, which the sender chooses and may fill with another address
// ("<boss@corp.com>" <spam@evil.io>). It is written the way net/mail reads it
// back, its local part quoted when it is not a dot-atom ("<boss@corp.com>"@
// evil.io is a real, if odd, address on evil.io), so that reading it again
// gives itself.
//
// Mail programs and the Gmail API hand over plenty of headers whose display
// name RFC 5322 would have quoted and nobody did: "Doe, John <j@x.com>",
// "LinkedIn [Jobs] <jobs@linkedin.com>", "News: daily <n@b.fr>",
// "john@x.com <john@x.com>". One net/mail refuses is read again with its
// name in quotes (withQuotedName), when nothing in it could be taken for an
// address of its own. Any other header that does not read as exactly one
// address gives no address at all, "": no address is guessed out of it, so
// nothing is swept, unsubscribed or made a rule of in its name. That includes
// a From naming several authors ("patron@victime.fr, Lettre
// <lettre@journal.fr>", a group): taking one of them for the sender would
// let the others sweep, or unprotect, the mail.
func Address(raw string) string {
	s := strings.TrimSpace(raw)
	a, err := fromParser.Parse(s)
	if err != nil {
		if list, lErr := fromParser.ParseList(s); lErr == nil && len(list) > 1 {
			return ""
		}
		quoted, ok := withQuotedName(s)
		if !ok {
			return ""
		}
		if a, err = fromParser.Parse(quoted); err != nil {
			return ""
		}
	}
	spec := (&mail.Address{Address: a.Address}).String()
	return strings.TrimSuffix(strings.TrimPrefix(spec, "<"), ">")
}

// withQuotedName is the header s with its display name in quotes, for a
// header net/mail refused, and whether it is one this applies to: a name
// before its last "<", holding no "<", ">" or quote of its own (so nothing in
// it is an address or a quoted string the sender wrote), nor an "@" next to a
// "," or a ";" (an address, then another author: "patron@victime.fr, Doe,
// John <j@x.com>"), and after it one "<...>" closing the header, with no
// other angle bracket inside. Anything else ("<boss@corp.com>
// <spam@evil.io>", a quoted local part followed by more) is not read at all.
func withQuotedName(s string) (string, bool) {
	open := strings.LastIndex(s, "<")
	if open <= 0 || !strings.HasSuffix(s, ">") {
		return "", false
	}
	name := strings.TrimSpace(s[:open])
	inner := s[open+1 : len(s)-1]
	if name == "" || strings.ContainsAny(name, `<>"`) || strings.ContainsAny(inner, "<>") {
		return "", false
	}
	if strings.Contains(name, "@") && strings.ContainsAny(name, ",;") {
		return "", false
	}
	return `"` + strings.ReplaceAll(name, `\`, `\\`) + `" ` + s[open:], true
}

// NormalizeAddress extracts the bare, lower-cased email address from a raw From
// header (Address), returning "" when no plausible address is present.
func NormalizeAddress(raw string) string {
	return strings.ToLower(Address(raw))
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
	// Apostrophes around what was typed ('jean@x.fr') are the typist's, not
	// part of the address: kept, the entry matched nobody.
	if len(s) >= 2 && strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	// An entry that reads as an address (a raw From header "Display Name
	// <addr@host>", a bare address) is kept exactly as NormalizeAddress reads
	// it: that is what Match compares the senders with. Its quotes, when the
	// local part has some ("jean dupont"@exemple.fr), are part of it; trimmed,
	// the entry matched nobody while the API said "protégé".
	if addr := NormalizeAddress(s); addr != "" {
		return addr, KindAddress
	}
	// A header that does not read gives nothing: no address is guessed.
	if strings.Contains(s, "<") {
		return "", ""
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

// SameAddress reports whether an email whose raw From header is `from` was sent
// from `addr` (a bare address, or a From header of its own). Both sides are
// reduced to the bare, lower-cased address and compared whole, whatever display
// name either carries.
//
// Whole, never as a substring: "info@boutique.com" is inside
// "facturation-info@boutique.com" and "info@boutique.community", which are
// other senders, and a sweep that matched on containment archived or trashed
// their mail too. An addr with no usable address (empty, or no local part or
// domain) matches nothing, because an empty pattern contains every sender.
func SameAddress(from, addr string) bool {
	want := NormalizeAddress(addr)
	at := strings.LastIndex(want, "@")
	if at <= 0 || at == len(want)-1 {
		return false
	}
	return NormalizeAddress(from) == want
}

// Allowed reports whether action may be applied to an email from `from`, given
// the user's protected entries. Non-destructive actions are always allowed; a
// destructive action is blocked when the sender is protected.
//
// Fail safe: when the user protects anyone, a mail whose sender cannot be read
// (Address gives nothing) is spared rather than assumed harmless. The
// protected list must not be bypassed by a header nobody can read.
func Allowed(action, from string, entries []string) bool {
	if !IsDestructive(action) {
		return true
	}
	return !shielded(from, entries)
}

// shielded says whether a mail from `from` is out of reach of a destructive
// action: its sender is protected, or cannot be read while someone is.
func shielded(from string, entries []string) bool {
	if len(entries) > 0 && NormalizeAddress(from) == "" {
		return true
	}
	return Match(from, entries)
}
