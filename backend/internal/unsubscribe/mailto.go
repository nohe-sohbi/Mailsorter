package unsubscribe

import (
	"errors"
	"net"
	"net/mail"
	"net/url"
	"regexp"
	"strings"

	"github.com/nohe-sohbi/mailsorter/backend/internal/protect"
	"golang.org/x/net/publicsuffix"
)

// Request is the mail Mailsorter may write, on the person's behalf, for a
// mailto: of List-Unsubscribe (RFC 6068): the one address it goes to and its
// subject. Its body is always Body.
//
// Over IMAP Mailsorter sends it itself, from the person's own address,
// through their own sending server, and nobody sees it before it leaves. The
// address, the subject and the body all come from a header a stranger wrote,
// so each of them is bounded here: the body is never theirs, the subject is
// a command word or one token (commandSubject), and the address must be on
// the sender's own domain (see ForSender). Anything else is not sent by the server: the client opens the
// mailto: in the person's own mail program, where they see it first.
type Request struct {
	To      string
	Subject string
}

// Body is the text of every unsubscribe mail Mailsorter writes. A body the
// mailto: asks for is ignored: it would let a stranger write any text from
// the person's address.
const Body = "unsubscribe"

// DefaultSubject is the subject when the mailto: gives none: the word RFC
// 2369's own example uses.
const DefaultSubject = "unsubscribe"

// commandSubject is what an unsubscribe subject may be, case aside: a
// command word, followed at most by one ASCII token without a space (the
// list's id, a key: "unsubscribe 8f3a", "remove:news"), or one such token
// alone ("leave-8f3a"). A list asks for a command; a sentence ("Je
// démissionne avec effet immédiat", "unsubscribe I quit") is a stranger
// writing in the person's name, and is not sent for them.
var commandSubject = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^(unsubscribe|unsub|remove|stop|désinscription|desinscription)([ :_=-]?[A-Za-z0-9._@+=-]{1,80})?$`),
	regexp.MustCompile(`^[A-Za-z0-9._@+=:-]{1,100}$`),
}

var (
	// ErrNotMailto is a link that is not a mailto: at all.
	ErrNotMailto = errors.New("unsubscribe: not a mailto link")
	// ErrNoRecipient is a mailto: that names no address.
	ErrNoRecipient = errors.New("unsubscribe: the mailto link names no address")
	// ErrManyRecipients is a mailto: that names more than one address, in
	// its path or in to, cc or bcc fields: one list, one address.
	ErrManyRecipients = errors.New("unsubscribe: the mailto link names more than one address")
	// ErrBadRecipient is an address that is not a bare addr-spec: a display
	// name, a comment, quotes or anything else a header would carry
	// differently from the way it was written.
	ErrBadRecipient = errors.New("unsubscribe: the mailto address is not a plain address")
	// ErrBadSubject is a subject that is not a command (commandSubject):
	// a sentence, a long text, a character a person would not see.
	ErrBadSubject = errors.New("unsubscribe: the mailto subject is not an unsubscribe command")
	// ErrBadEncoding is a percent-encoding that does not decode.
	ErrBadEncoding = errors.New("unsubscribe: the mailto link is not correctly encoded")
	// ErrOtherDomain is a mailto: address that is not on the registrable
	// domain of the mail's sender: a list unsubscribes at home, and an
	// address elsewhere (a group to join, a third party) is a stranger
	// choosing who the person writes to.
	ErrOtherDomain = errors.New("unsubscribe: the mailto address is not on the sender's domain")
	// ErrBadSender is a From header that is not exactly one address, as RFC
	// 5322 reads it: who sent the mail is not known well enough to write on
	// the person's behalf to an address of theirs.
	ErrBadSender = errors.New("unsubscribe: the sender of the mail is not one plain address")
	// ErrNonASCIIMailbox is a mailto: address whose local part is not ASCII
	// (désabo@journal.fr): a sending server without SMTPUTF8 cannot take it,
	// and no ASCII form of it exists. The person's mail program writes it.
	ErrNonASCIIMailbox = errors.New("unsubscribe: the mailto address has a local part that is not ASCII")
)

// ForSender reads the mailto: of a mail whose From header is from into the
// mail Mailsorter may write for it, or says why it may not. Its To is
// written so any sending server takes it (asciiRecipient).
//
// The sender is the address of the From header as protect.Address reads it
// (RFC 5322, net/mail), never an address in its display name, which the
// sender writes: "<patron@victime.fr>" <lettre@journal.fr> is
// lettre@journal.fr. A header that does not read as exactly one address is
// refused (ErrBadSender) rather than guessed at. The mailto: address must be on the same registrable domain
// as the sender (the public suffix plus one label, from the Public Suffix
// List, private suffixes included): lettre@journal.fr may ask for
// desabonnement@journal.fr or for stop@listes.journal.fr, never for
// anyone@googlegroups.com, and a sender on foo.github.io never for
// bar.github.io. An IP address or a domain literal is no registrable domain.
func ForSender(mailto, from string) (Request, error) {
	req, err := ParseMailto(mailto)
	if err != nil {
		return Request{}, err
	}
	sender := protect.Address(from)
	if sender == "" {
		return Request{}, ErrBadSender
	}
	if !SameRegistrableDomain(req.To, sender) {
		return Request{}, ErrOtherDomain
	}
	to, err := asciiRecipient(req.To)
	if err != nil {
		return Request{}, err
	}
	req.To = to
	return req, nil
}

// asciiRecipient is a mailto: address written so a sending server without
// SMTPUTF8 takes it, in RCPT TO and in the To header: an ASCII address as
// the list wrote it, an accented domain in its ASCII form (IDNA,
// protect.ASCIIDomain: stop@café.fr is written stop@xn--caf-dma.fr). A local
// part that is not ASCII has no such form: ErrNonASCIIMailbox.
func asciiRecipient(address string) (string, error) {
	at := strings.LastIndex(address, "@")
	if at < 0 {
		return "", ErrBadRecipient
	}
	local, domain := address[:at], address[at+1:]
	if !isASCII(local) {
		return "", ErrNonASCIIMailbox
	}
	if isASCII(domain) {
		return address, nil
	}
	ascii, ok := protect.ASCIIDomain(domain)
	if !ok {
		return "", ErrBadRecipient
	}
	return local + "@" + ascii, nil
}

// isASCII says whether s holds ASCII only.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// SameRegistrableDomain reports whether two bare addresses are on the same
// registrable domain (eTLD+1). An address whose domain has none (a bare
// public suffix, no domain at all, an IP address or a domain literal such as
// [192.0.2.1]) matches nothing.
func SameRegistrableDomain(a, b string) bool {
	da, okA := registrableDomain(a)
	db, okB := registrableDomain(b)
	return okA && okB && da == db
}

func registrableDomain(address string) (string, bool) {
	at := strings.LastIndex(address, "@")
	if at < 0 {
		return "", false
	}
	domain := strings.TrimSuffix(strings.TrimSpace(address[at+1:]), ".")
	if domain == "" || strings.ContainsAny(domain, "[]:") {
		return "", false
	}
	// In ASCII, the one form two domains are compared in, and the one the
	// Public Suffix List reads: lettre@café.fr and stop@xn--caf-dma.fr are on
	// one domain. The domain goes to IDNA as written, the way the shield reads
	// it (protect.ASCIIDomain): lowered by Go first, "lİnkedin.com" became
	// linkedin.com.
	ascii, ok := protect.ASCIIDomain(domain)
	if !ok {
		return "", false
	}
	// An IP is no name: the Public Suffix List would read 192.168.0.1 and
	// 10.0.0.1 as the same "0.1". Read once in ASCII too, where full-width
	// digits have become digits.
	if net.ParseIP(domain) != nil || net.ParseIP(ascii) != nil {
		return "", false
	}
	site, err := publicsuffix.EffectiveTLDPlusOne(ascii)
	if err != nil {
		return "", false
	}
	return site, true
}

// ParseMailto reads a mailto: link (RFC 6068) into the mail it asks for,
// whoever sent it: ForSender is what decides whether Mailsorter writes it.
//
// "+" stays a plus, as RFC 6068 says (it is not a form encoding). A body is
// ignored (Body is the text), a subject the link does not give is
// DefaultSubject, and one that is not a command (commandSubject) is refused.
func ParseMailto(raw string) (Request, error) {
	const scheme = "mailto:"
	raw = strings.TrimSpace(raw)
	if len(raw) < len(scheme) || !strings.EqualFold(raw[:len(scheme)], scheme) {
		return Request{}, ErrNotMailto
	}
	path, query, _ := strings.Cut(raw[len(scheme):], "?")

	var recipients []string
	addAll := func(encoded string) error {
		decoded, err := url.PathUnescape(encoded)
		if err != nil {
			return ErrBadEncoding
		}
		for _, addr := range strings.Split(decoded, ",") {
			if addr = strings.TrimSpace(addr); addr != "" {
				recipients = append(recipients, addr)
			}
		}
		return nil
	}
	if err := addAll(path); err != nil {
		return Request{}, err
	}

	subject, haveSubject := "", false
	if query != "" {
		for _, field := range strings.Split(query, "&") {
			name, value, _ := strings.Cut(field, "=")
			name, err := url.PathUnescape(name)
			if err != nil {
				return Request{}, ErrBadEncoding
			}
			switch strings.ToLower(name) {
			case "to", "cc", "bcc":
				if err := addAll(value); err != nil {
					return Request{}, err
				}
			case "subject":
				if haveSubject {
					continue
				}
				if subject, err = url.PathUnescape(value); err != nil {
					return Request{}, ErrBadEncoding
				}
				haveSubject = true
			}
		}
	}

	switch {
	case len(recipients) == 0:
		return Request{}, ErrNoRecipient
	case len(recipients) > 1:
		return Request{}, ErrManyRecipients
	}
	to := recipients[0]
	parsed, err := mail.ParseAddress(to)
	if err != nil || parsed.Name != "" || parsed.Address != to {
		return Request{}, ErrBadRecipient
	}

	subject = strings.TrimSpace(subject)
	if subject == "" {
		subject = DefaultSubject
	}
	if !isCommand(subject) {
		return Request{}, ErrBadSubject
	}
	return Request{To: to, Subject: subject}, nil
}

// isCommand says whether subject is one commandSubject accepts.
func isCommand(subject string) bool {
	for _, re := range commandSubject {
		if re.MatchString(subject) {
			return true
		}
	}
	return false
}
