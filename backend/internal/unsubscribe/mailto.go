package unsubscribe

import (
	"errors"
	"net"
	"net/mail"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

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
// one short line, and the address must be on the sender's own domain (see
// ForSender). Anything else is not sent by the server: the client opens the
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

// MaxSubject bounds the subject, in characters. An unsubscribe subject is a
// word or a short command; a longer one is not written for the person.
const MaxSubject = 100

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
	// ErrBadSubject is a subject longer than MaxSubject characters, or one
	// that holds a control character, a line or paragraph separator, or an
	// invisible format character (a bidirectional override such as U+202E,
	// a zero-width space): what a person would not see in it.
	ErrBadSubject = errors.New("unsubscribe: the mailto subject is not one short line")
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
)

// ForSender reads the mailto: of a mail whose From header is from into the
// mail Mailsorter may write for it, or says why it may not.
//
// The sender is the address of the From header as RFC 5322 reads it
// (net/mail), never an address in its display name, which the sender writes:
// "<patron@victime.fr>" <lettre@journal.fr> is lettre@journal.fr. A header
// that does not read as exactly one address is refused (ErrBadSender) rather
// than guessed at. The mailto: address must be on the same registrable domain
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
	sender, err := mail.ParseAddress(strings.TrimSpace(from))
	if err != nil {
		return Request{}, ErrBadSender
	}
	if !SameRegistrableDomain(req.To, sender.Address) {
		return Request{}, ErrOtherDomain
	}
	return req, nil
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
	domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(address[at+1:])), ".")
	// An IP is no name: the Public Suffix List would read 192.168.0.1 and
	// 10.0.0.1 as the same "0.1".
	if domain == "" || strings.ContainsAny(domain, "[]:") || net.ParseIP(domain) != nil {
		return "", false
	}
	site, err := publicsuffix.EffectiveTLDPlusOne(domain)
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
// DefaultSubject, and one that is not one short line is refused.
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
	if utf8.RuneCountInString(subject) > MaxSubject || strings.IndexFunc(subject, unseen) >= 0 {
		return Request{}, ErrBadSubject
	}
	if subject == "" {
		subject = DefaultSubject
	}
	return Request{To: to, Subject: subject}, nil
}

// unseen is a character a subject must not hold: a control character, a line
// or paragraph separator (U+2028, U+2029), or an invisible format character
// (U+202E and the other bidirectional controls, zero-width characters).
func unseen(r rune) bool {
	return unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp)
}
