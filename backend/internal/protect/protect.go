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
	"unicode/utf8"

	"github.com/emersion/go-message/charset"
	"golang.org/x/net/idna"
	"golang.org/x/text/unicode/norm"
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
	return NewShield(entries).AllowsOnFolders(action, from)
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

// NoSenderText is how a From that names no sender (Address reads "") is
// stored, for the screen: as written, made valid UTF-8 and with each control
// character (but a tab) turned into a space, so no control byte reaches a
// screen. Cleaning must not give it a sender: a From that only failed to read
// for a control byte ("Lettre\x01 <lettre@journal.fr>") would read once
// cleaned, so that one is stored as quoted text instead (QuotedText), which
// never reads as an address.
func NoSenderText(raw string) string {
	cleaned := withoutControls(strings.ToValidUTF8(raw, "\uFFFD"))
	if Address(cleaned) == "" {
		return cleaned
	}
	return QuotedText(raw)
}

// FromText is how one From header is stored for the screen. One that names
// no sender: NoSenderText. One that names a sender: without its control bytes
// and as valid UTF-8 ("a@b.fr (x\x01y)" shown "a@b.fr (x y)"), as long as
// that reads as the same sender; otherwise as written, since what is read is
// what matters.
func FromText(raw string) string {
	address := Address(raw)
	if address == "" {
		return NoSenderText(raw)
	}
	cleaned := withoutControls(strings.ToValidUTF8(raw, "\uFFFD"))
	if Address(cleaned) == address {
		return cleaned
	}
	return raw
}

// Label is what a screen shows for the sender of a mail out of its stored
// From. The display name, unless it holds an "@", in any width ("boss@corp.com",
// "boss＠corp.com" or "boss﹫corp.com" <evil@evil.io> shows evil@evil.io: a
// name is the sender's to write, and cutting the header at its first "<"
// showed boss@corp.com). The address when there is no name, or none that
// shows (a name of zero-width spaces, of blank Braille or Hangul fillers),
// in the one form an address is shown in (ShownAddress). When the From names
// no sender, its text, without the quotes it was stored in, each character
// that hides or reorders made visible (markedText), since it may hold
// addresses.
//
// A name shows without control or format characters (bidi overrides and
// isolates, marks, zero-width spaces: shown, they reordered or hid what
// follows) but the joiners letters and emoji need: shownText.
func Label(from string) string {
	label, _ := LabelOf(from)
	return label
}

// LabelOf is Label, and whether that label is the sender's address (shown
// in its one form, ShownAddress) rather than a display name or the text of
// a From that names no sender: a screen lays an address out so its domain
// is never cut, and needs not guess which it was given.
func LabelOf(from string) (label string, isAddress bool) {
	name, address := Sender(from)
	if address == "" {
		return unquoted(strings.TrimSpace(markedText(strings.ToValidUTF8(from, "\uFFFD")))), false
	}
	name = shownText(name)
	if !visible(name) || strings.Contains(norm.NFKC.String(name), "@") {
		return ShownAddress(address), true
	}
	return name, false
}

// ShownAddress is the one form a screen shows an address in, whatever screen:
// never cleaned into another address, never one that reads as another.
//
// The local part as written when each character is in a closed list,
// U+FFFD for any other one (shownLocalPart): x@pay<U+202E>pal.com read
// "x@paypal.com" with its override dropped, and a Cyrillic "ѕ" in
// "ѕupport" is no "s". The domain in Unicode only for labels in the same
// closed list when IDNA takes it (ShownDomain: y@pаypal.com, with a Cyrillic
// "а", is y@xn--pypal-4ve.com), each non-ASCII character made visible when
// IDNA refuses it (x@paypal<U+2024>com, a one dot leader drawn like a dot, is
// x@paypal<U+FFFD>com). The domain never reads as another one: it is the one
// the app compares, or visibly not a name.
func ShownAddress(address string) string {
	at := strings.LastIndex(address, "@")
	if at < 0 {
		return markedText(address)
	}
	return shownLocalPart(address[:at]) + "@" + ShownDomain(address[at+1:])
}

// shownLocalPart is the local part of an address as a screen shows it: each
// character as written when it is printable ASCII (the dots, the quotes, the
// "+" and the space of a quoted local part) or in the closed list of the
// domains (listedRune: "jürgen" reads as it is), U+FFFD for any other one,
// a U+FFFD already there included: ѕupport@paypal.com, with a Cyrillic "ѕ",
// reads "<U+FFFD>upport@paypal.com", and so does a hidden or reordering
// character, or an "@" of another width.
func shownLocalPart(local string) string {
	return strings.Map(func(r rune) rune {
		if (0x20 <= r && r <= 0x7E) || listedRune(r) {
			return r
		}
		return '\uFFFD'
	}, local)
}

// ShownDomain is a domain as a screen shows it: entryDomain of its ASCII form
// when IDNA takes it, as written with each non-ASCII or control character
// made visible (U+FFFD) otherwise.
func ShownDomain(domain string) string {
	if ascii, ok := ASCIIDomain(domain); ok {
		return entryDomain(ascii)
	}
	return strings.Map(func(r rune) rune {
		if r >= utf8.RuneSelf || unicode.IsControl(r) {
			return '\uFFFD'
		}
		return r
	}, domain)
}

// ShownEntry is a protected entry as the settings show it: an address
// (ShownAddress) or a domain (ShownDomain).
func ShownEntry(value string) string {
	if strings.Contains(value, "@") {
		return ShownAddress(value)
	}
	return ShownDomain(value)
}

// ShownText is text a screen shows that may hold an address (a rule's
// condition, the text of a From that names no sender): markedText.
func ShownText(text string) string {
	return markedText(text)
}

// markedText is text with each character that would not show, or would move
// what follows, made visible as U+FFFD: a control character (a tab is a
// space), a format character (bidi overrides, embeddings, isolates and
// marks, zero-width spaces, the zero-width joiner and non-joiner, the soft
// hyphen), a character Unicode says to draw as nothing (a Hangul filler, a
// variation selector); and each "@" of another width (U+FF20, U+FE6B, read
// through NFKC), which draws like the one that ends a local part. Never
// removed: removed, they made one address read as another.
func markedText(text string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r),
			unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r),
			unicode.Is(unicode.Variation_Selector, r):
			return '\uFFFD'
		case r >= utf8.RuneSelf && norm.NFKC.String(string(r)) == "@":
			return '\uFFFD'
		}
		return r
	}, text)
}

// shownText is text for a screen: each control character but a tab turned
// into a space, each format character (Unicode Cf: the bidi overrides,
// embeddings, isolates and marks U+202A-202E, U+2066-2069, U+200E/F, the
// zero-width space, the soft hyphen) dropped, the space around trimmed. The
// zero-width joiner and non-joiner (U+200C/D) are kept: they join or part the
// letters of Persian and Indic words and the parts of an emoji, and move
// nothing.
func shownText(text string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		switch {
		case r == '\u200C' || r == '\u200D':
			return r
		case unicode.Is(unicode.Cf, r):
			return -1
		case r != '\t' && unicode.IsControl(r):
			return ' '
		}
		return r
	}, text))
}

// visible says whether text shows anything: one character that is not a
// space, a control or format character, a mark with nothing to sit on, a
// character Unicode says to draw as nothing (the Hangul fillers U+3164,
// U+FFA0, U+115F, U+1160, the variation selectors), or the blank Braille
// pattern U+2800.
func visible(text string) bool {
	for _, r := range text {
		switch {
		case unicode.IsSpace(r), unicode.IsControl(r), unicode.Is(unicode.Cf, r),
			unicode.Is(unicode.Mn, r), unicode.Is(unicode.Me, r),
			unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r),
			unicode.Is(unicode.Variation_Selector, r), r == '\u2800':
			continue
		}
		return true
	}
	return false
}

// unquoted is s without the quotes of a quoted string around it, its escapes
// undone; s itself otherwise.
func unquoted(s string) string {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return s
	}
	var b strings.Builder
	inner := s[1 : len(s)-1]
	for i := 0; i < len(inner); i++ {
		if inner[i] == '\\' && i+1 < len(inner) {
			i++
		}
		b.WriteByte(inner[i])
	}
	return b.String()
}

// withoutControls is text with each control character but a tab turned into
// a space.
func withoutControls(text string) string {
	return strings.Map(func(r rune) rune {
		if r != '\t' && unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
}

// QuotedText is text as an RFC 5322 quoted string: valid UTF-8, a control
// character (but a tab) turned into a space, "\" and quotes escaped. A From
// stored as QuotedText names no sender (Address reads ""): it is how a
// transport keeps, for the screen, the text of a From that names none (two
// From headers, a header that does not read), and how a display name is
// written before its address.
func QuotedText(text string) string {
	text = withoutControls(strings.ToValidUTF8(text, "\uFFFD"))
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

// NormalizeAddress is the address a raw From header names (Address) in the
// one form senders are compared in (addressForm): its local part in
// lowercase, its domain in ASCII as IDNA reads it. "" when no plausible
// address is present.
func NormalizeAddress(raw string) string {
	addr := Address(raw)
	if addr == "" {
		return ""
	}
	return addressForm(addr)
}

// Domain returns the domain part of an address (or of a bare domain value) in
// the form it is compared in (domainForm), or "" if none can be derived.
func Domain(raw string) string {
	addr := NormalizeAddress(raw)
	if i := strings.LastIndex(addr, "@"); i >= 0 {
		return strings.TrimSpace(addr[i+1:])
	}
	return strings.Trim(addr, "@")
}

// EntryFromMail is what "Protéger" on a mail stores (POST /api/protected with
// fromMail, which only the reader sends): the sender's address as Address
// reads it out of the mail's From, its local part in lowercase and its domain
// in the form an entry shows (entryDomain), which Match reads back as the
// same address. Nothing else is touched ('patron@victime.fr' is an address,
// apostrophes included). "" when From names no sender, or names one on a
// domain IDNA refuses (x@pay<U+202E>pal.com): neither can be protected from
// that mail, and the second, stored as written, would show in the settings
// as what it is not.
func EntryFromMail(from string) string {
	addr := Address(from)
	if addr == "" {
		return ""
	}
	at := strings.LastIndex(addr, "@")
	ascii, ok := ASCIIDomain(addr[at+1:])
	if !ok {
		return ""
	}
	return strings.ToLower(addr[:at]) + "@" + entryDomain(ascii)
}

// NormalizeEntry canonicalizes a value the user typed into the protection list
// (the settings screen) and classifies it. An input with a local part
// ("a@b.com") becomes an address entry; a bare domain ("b.com" or "@b.com")
// becomes a domain entry. Returns an empty value when nothing usable remains.
// "Protéger" on a mail does not come here: EntryFromMail.
//
// The domain is read as written ("lİnkedin.com" is not linkedin.com, IDNA
// says so), stored in the form an entry shows (entryDomain), and Match reads
// it back the way it reads a From written the same way: what is typed
// protects the mail that writes it so.
func NormalizeEntry(raw string) (value, kind string) {
	s := strings.TrimSpace(raw)
	// Apostrophes around what was typed ('jean@x.fr') are the typist's, not
	// part of the address: kept, the entry matched nobody.
	if len(s) >= 2 && strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	// An entry that reads as an address (a raw From header "Display Name
	// <addr@host>", a bare address) is kept as Address reads it, its local
	// part in lowercase: that is what Match compares the senders with. Its
	// quotes, when the local part has some ("jean dupont"@exemple.fr), are
	// part of it; trimmed, the entry matched nobody while the API said
	// "protégé".
	if addr := Address(s); addr != "" {
		at := strings.LastIndex(addr, "@")
		domain, ok := typedDomain(addr[at+1:])
		if !ok {
			return "", ""
		}
		return strings.ToLower(addr[:at]) + "@" + domain, KindAddress
	}
	// Otherwise only a well-formed domain is taken ("acme.com", "@acme.com").
	// Anything else is refused rather than stored as an entry that protects
	// nobody: a header that does not read, two addresses, a space, a stray
	// ">", two "@", a name ("Jean Dupont"), a final dot, what IDNA refuses.
	d, ok := typedDomain(strings.TrimPrefix(s, "@"))
	if !ok {
		return "", ""
	}
	return d, KindDomain
}

// ASCIIDomain is a domain, as written, in the one form domains are compared
// in: IDNA's Lookup profile (UTS 46), which does its own case mapping
// (never Go's lowercase first: Go lowers "İ" to "i" and "ẞ" to "ß", UTS 46
// does not), folds full-width letters and writes an accented label in
// punycode. "café.fr" and "xn--caf-dma.fr" are one domain, "ＥＸＡＭＰＬＥ.fr"
// is example.fr, "lİnkedin.com" is not linkedin.com. false when IDNA refuses
// it.
func ASCIIDomain(d string) (string, bool) {
	ascii, err := idna.Lookup.ToASCII(strings.TrimSpace(d))
	if err != nil || ascii == "" {
		return "", false
	}
	return ascii, true
}

// domainForm is a domain in the form senders are compared in: ASCIIDomain,
// or, for one IDNA refuses, the domain as written with only its ASCII letters
// in lowercase. That one is matched by an entry that writes it the same way,
// and is never folded into another domain.
func domainForm(d string) string {
	if ascii, ok := ASCIIDomain(d); ok {
		return ascii
	}
	return lowerASCII(strings.TrimSpace(d))
}

// addressForm is an address in the form senders are compared in: its local
// part in lowercase, its domain in domainForm.
func addressForm(addr string) string {
	addr = strings.TrimSpace(addr)
	at := strings.LastIndex(addr, "@")
	if at < 0 {
		return strings.ToLower(addr)
	}
	return strings.ToLower(addr[:at]) + "@" + domainForm(addr[at+1:])
}

// lowerASCII is s with its ASCII letters in lowercase, nothing else changed.
func lowerASCII(s string) string {
	return strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, s)
}

// typedDomain is the domain an entry typed in the settings stores: well-formed
// once in ASCII (wellFormedDomain), in the form an entry shows (entryDomain).
// false when IDNA refuses it or it is not well-formed.
func typedDomain(d string) (string, bool) {
	ascii, ok := ASCIIDomain(d)
	if !ok || !wellFormedDomain(ascii) {
		return "", false
	}
	return entryDomain(ascii), true
}

// entryDomain is how a domain is shown, in the settings and on every screen
// (ShownDomain): each label in Unicode only when every character of it is in
// a closed list (listedRune: ASCII letters, digits and the hyphen, the
// letters of Latin-1 Supplement and Latin Extended-A but those drawn like an
// ASCII letter) and it is stable in NFC; in its ASCII form (punycode)
// otherwise. "café", "crème-brûlée" and "straße" read as they are; "ɡoogle"
// (an IPA "ɡ"), "gıthub" (a dotless "ı"), "yahᴏᴏ" (small capitals),
// "pаypal" (a Cyrillic "а"), "paypal·com" (a dot in disguise), Greek, CJK, a
// combining mark left over: all in punycode, so a domain never reads as
// another one. A label the sender wrote in punycode stays so unless it
// decodes into the list. ascii is an ASCIIDomain; Match reads either form
// back as that domain.
func entryDomain(ascii string) string {
	labels := strings.Split(ascii, ".")
	for i, label := range labels {
		if !strings.HasPrefix(label, "xn--") {
			continue
		}
		readable, err := idna.Lookup.ToUnicode(label)
		if err == nil && listedLabel(readable) {
			labels[i] = readable
		}
	}
	return strings.Join(labels, ".")
}

// listedLabel says whether a domain label may be shown in Unicode: every
// character in the closed list (listedRune), and the label stable in NFC.
func listedLabel(label string) bool {
	if label == "" || !norm.NFC.IsNormalString(label) {
		return false
	}
	for _, r := range label {
		if !listedRune(r) {
			return false
		}
	}
	return true
}

// listedRune is the closed list of characters an address is shown with
// as they are: the ASCII letters and digits, the hyphen, and the letters of
// Latin-1 Supplement (U+00C0-U+00FF but × and ÷) and Latin Extended-A
// (U+0100-U+017F) but those drawn like an ASCII letter (drawnLikeASCII).
func listedRune(r rune) bool {
	switch {
	case 'a' <= r && r <= 'z', 'A' <= r && r <= 'Z', '0' <= r && r <= '9', r == '-':
		return true
	case 0x00C0 <= r && r <= 0x00FF:
		return r != 0x00D7 && r != 0x00F7
	case 0x0100 <= r && r <= 0x017F:
		return !drawnLikeASCII[r]
	}
	return false
}

// drawnLikeASCII are the letters of Latin Extended-A a reader takes for an
// ASCII letter or a dot: ı (a dotless i, "gıthub"), ĸ (a small capital K),
// ŀ (an l with a middle dot), ŉ (an apostrophe and n), ſ (a long s, drawn
// like an f). Out of the list.
var drawnLikeASCII = map[rune]bool{
	'\u0131': true, '\u0138': true, '\u0140': true, '\u0149': true, '\u017F': true,
}

// DomainForms is the forms a domain may be written in, in a header or in a
// stored From: as given (lowercase), in ASCII and in Unicode (IDNA), each
// once. A search for a sender's mail by its domain looks for all of them.
func DomainForms(d string) []string {
	forms := []string{strings.ToLower(d)}
	if ascii, ok := ASCIIDomain(d); ok {
		forms = append(forms, ascii)
		if readable, err := idna.Lookup.ToUnicode(ascii); err == nil {
			forms = append(forms, readable)
		}
	}
	out := forms[:0]
	seen := map[string]bool{}
	for _, f := range forms {
		if f != "" && !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
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

// Shield is a protected list made ready to read many mails with: each entry
// put once, when it is made, in the form senders are compared in
// (addressForm, domainForm), rather than once per mail. The handlers load
// one per call (protectedValues) and ask it about each mail.
type Shield struct {
	count     int
	addresses map[string]bool
	domains   []string
}

// NewShield makes a Shield of the user's protected entries, as stored.
func NewShield(entries []string) Shield {
	s := Shield{count: len(entries), addresses: map[string]bool{}}
	for _, e := range entries {
		e = strings.TrimSpace(e)
		switch {
		case e == "":
		case strings.Contains(e, "@"):
			s.addresses[addressForm(e)] = true
		default:
			s.domains = append(s.domains, domainForm(e))
		}
	}
	return s
}

// Empty says whether the user protects no one.
func (s Shield) Empty() bool {
	return s.count == 0
}

// Protects is Match for this list.
func (s Shield) Protects(from string) bool {
	if s.count == 0 {
		return false
	}
	addr := NormalizeAddress(from)
	return addr != "" && s.protectsAddress(addr)
}

// protectsAddress says whether an address in addressForm is covered.
func (s Shield) protectsAddress(addr string) bool {
	if s.addresses[addr] {
		return true
	}
	domain := addr[strings.LastIndex(addr, "@")+1:]
	for _, d := range s.domains {
		if domain == d || strings.HasSuffix(domain, "."+d) {
			return true
		}
	}
	return false
}

// Allows is Allowed for this list.
func (s Shield) Allows(action, from string) bool {
	if !IsDestructive(action) {
		return true
	}
	return !s.shields(from)
}

// AllowsOnFolders is AllowedOnFolders for this list.
func (s Shield) AllowsOnFolders(action, from string) bool {
	if !IsDestructiveOnFolders(action) {
		return true
	}
	return !s.shields(from)
}

// shields says whether a mail from `from` is out of reach of a destructive
// action: its sender is protected, or cannot be read while someone is.
func (s Shield) shields(from string) bool {
	if s.count == 0 {
		return false
	}
	addr := NormalizeAddress(from)
	if addr == "" {
		return true
	}
	return s.protectsAddress(addr)
}

// Match reports whether the sender of an email is covered by any protected
// entry. Address entries match the exact address; domain entries match the
// address's domain or any subdomain of it (so "acme.com" also shields
// "mail.acme.com"). Both sides are compared in one form (addressForm,
// domainForm), the domain in ASCII as IDNA reads it as written: "café.fr"
// shields contact@xn--caf-dma.fr, the form a header carries it in, as well as
// contact@café.fr, and "linkedin.com" does not shield news@lİnkedin.com.
// Reading many mails, NewShield once.
func Match(from string, entries []string) bool {
	return NewShield(entries).Protects(from)
}

// SameAddress reports whether an email whose raw From header is `from` was sent
// from `addr` (a bare address, or a From header of its own). Both sides are
// reduced to the address in the form senders are compared in
// (NormalizeAddress) and compared whole, whatever display name either carries.
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
// protected list must not be bypassed by a header nobody can read. Reading
// many mails, NewShield once.
func Allowed(action, from string, entries []string) bool {
	return NewShield(entries).Allows(action, from)
}
