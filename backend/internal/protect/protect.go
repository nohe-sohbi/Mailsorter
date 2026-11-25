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
	"unicode"

	"github.com/emersion/go-message/charset"
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
// names, comments, encoded words). A name encoded in another charset than
// net/mail's own (windows-1252, iso-8859-15, the French mail of a bank) is
// decoded by go-message's charset tables, the ones the IMAP client uses, so a
// screen shows "Société Générale" on both transports. A charset nobody knows
// is kept as its bytes: the name is not the address, and refusing the whole
// header for it would leave the sender with no address.
var fromParser = mail.AddressParser{WordDecoder: &mime.WordDecoder{
	CharsetReader: func(label string, input io.Reader) (io.Reader, error) {
		if r, err := charset.Reader(label, input); err == nil {
			return r, nil
		}
		return input, nil
	},
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
// <lettre@journal.fr>") and a From in group syntax, whatever its members
// ("Undisclosed: a@x.fr;", "G:;, Lettre <lettre@journal.fr>": groupSyntax):
// taking one of them for the sender would let the others sweep, or
// unprotect, the mail.
func Address(raw string) string {
	_, address := Sender(raw)
	return address
}

// Sender is Address with the display name the header gives it, decoded (an
// RFC 2047 name read, a name in a charset net/mail does not decode kept as
// its bytes): what a screen shows next to the address. Both are "" when the
// header names no sender.
func Sender(raw string) (name, address string) {
	// A header passed on in latin-1 is no valid UTF-8, which net/mail refuses
	// whole: its bad bytes (in a name, as a rule) are replaced first, so the
	// address is still read.
	s := strings.ToValidUTF8(strings.TrimSpace(raw), "\uFFFD")
	if groupSyntax(s) {
		return "", ""
	}
	a, err := fromParser.Parse(s)
	if err != nil {
		if list, lErr := fromParser.ParseList(s); lErr == nil && len(list) > 1 {
			return "", ""
		}
		quoted, ok := withQuotedName(s)
		if !ok {
			return "", ""
		}
		if a, err = fromParser.Parse(quoted); err != nil {
			return "", ""
		}
	}
	spec := (&mail.Address{Address: a.Address}).String()
	return a.Name, strings.TrimSuffix(strings.TrimPrefix(spec, "<"), ">")
}

// QuotedText is text as an RFC 5322 quoted string: valid UTF-8, a control
// character (but a tab) turned into a space, "\" and quotes escaped. A From
// stored as QuotedText names no sender (Address reads ""): it is how a
// transport keeps, for the screen, the text of a From that names none (two
// From headers, a header that does not read), and how a display name is
// written before its address.
func QuotedText(text string) string {
	text = strings.ToValidUTF8(text, "\uFFFD")
	text = strings.Map(func(r rune) rune {
		if r != '\t' && unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
	return `"` + strings.ReplaceAll(strings.ReplaceAll(text, `\`, `\\`), `"`, `\"`) + `"`
}

// groupSyntax reports whether a header uses RFC 5322's group syntax
// ("phrase: members;"): a ":" and, after it, a ";", outside quoted strings
// and comments. net/mail reads a group of one as its member, so it is caught
// before. "News: daily <n@b.fr>" has no ";" and is no group.
func groupSyntax(s string) bool {
	colon, inQuote, depth := false, false, 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inQuote:
			if c == '\\' {
				i++
			} else if c == '"' {
				inQuote = false
			}
		case depth > 0:
			if c == '\\' {
				i++
			} else if c == '(' {
				depth++
			} else if c == ')' {
				depth--
			}
		case c == '"':
			inQuote = true
		case c == '(':
			depth++
		case c == ':':
			colon = true
		case c == ';' && colon:
			return true
		}
	}
	return false
}

// withQuotedName is the header s with its display name in quotes, for a
// header net/mail refused, and whether it is one this applies to: a name
// before its last "<", holding no "<", ">" or quote of its own (so nothing in
// it is an address or a quoted string the sender wrote), nor an "@" next to a
// ",", a ";" or a ":" (an address, then another author: "patron@victime.fr,
// Doe, John <j@x.com>", or a group: "Liste: a@x.fr <b@x.fr>"), and after it
// one "<...>" closing the header, with no
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
	if strings.Contains(name, "@") && strings.ContainsAny(name, ",;:") {
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

// EntryFromMail is what "Protéger" on a mail stores (POST /api/protected with
// fromMail, which only the reader sends): the sender's address exactly as
// NormalizeAddress reads it out of the mail's From, which is what Match reads
// the next mails with. Nothing else
// is touched ('patron@victime.fr' is an address, apostrophes included). ""
// when From names no sender: it cannot be protected from that mail.
func EntryFromMail(from string) string {
	return NormalizeAddress(from)
}

// NormalizeEntry canonicalizes a value the user typed into the protection list
// (the settings screen) and classifies it. An input with a local part
// ("a@b.com") becomes an address entry; a bare domain ("b.com" or "@b.com")
// becomes a domain entry. Returns an empty value when nothing usable remains.
// "Protéger" on a mail does not come here: EntryFromMail.
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
	// Otherwise only a well-formed domain is taken ("acme.com", "@acme.com").
	// Anything else is refused rather than stored as an entry that protects
	// nobody: a header that does not read, two addresses, a space, a stray
	// ">", two "@", a name ("Jean Dupont"), a final dot.
	d := strings.ToLower(strings.TrimPrefix(s, "@"))
	if !wellFormedDomain(d) {
		return "", ""
	}
	return d, KindDomain
}

// wellFormedDomain says whether d is a domain name: labels of letters,
// digits and hyphens (no hyphen first or last), 63 characters at most each,
// 253 in all, no empty label (so no final dot).
func wellFormedDomain(d string) bool {
	if d == "" || len(d) > 253 {
		return false
	}
	for _, label := range strings.Split(d, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			if r != '-' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
				return false
			}
		}
	}
	return true
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
