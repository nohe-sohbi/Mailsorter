package unsubscribe

import (
	"errors"
	"net/mail"
	"net/url"
	"strings"
)

// Request is the mail a mailto: of List-Unsubscribe asks for (RFC 6068): the
// one address it goes to, its subject and its body. Over IMAP Mailsorter
// writes it itself, from the person's own address, through their sending
// server.
type Request struct {
	To      string
	Subject string
	Body    string
}

// DefaultSubject and DefaultBody are what the mail says when the mailto: does
// not: the word RFC 2369's own example uses, and the one a list program that
// reads commands understands.
const (
	DefaultSubject = "unsubscribe"
	DefaultBody    = "unsubscribe"
)

var (
	// ErrNotMailto is a link that is not a mailto: at all.
	ErrNotMailto = errors.New("unsubscribe: not a mailto link")
	// ErrNoRecipient is a mailto: that names no address.
	ErrNoRecipient = errors.New("unsubscribe: the mailto link names no address")
	// ErrManyRecipients is a mailto: that names more than one address, in
	// its path or in to, cc or bcc fields. The mail leaves from the person's
	// own address, to an address a stranger chose: one list, one address.
	// Anything more is not sent on their behalf.
	ErrManyRecipients = errors.New("unsubscribe: the mailto link names more than one address")
	// ErrBadRecipient is an address that is not a bare addr-spec: a display
	// name, a comment, quotes or anything else a header would carry
	// differently from the way it was written.
	ErrBadRecipient = errors.New("unsubscribe: the mailto address is not a plain address")
	// ErrBadEncoding is a percent-encoding that does not decode.
	ErrBadEncoding = errors.New("unsubscribe: the mailto link is not correctly encoded")
)

// ParseMailto reads a mailto: link (RFC 6068) into the mail it asks for.
//
// "+" stays a plus, as RFC 6068 says (it is not a form encoding). A subject or
// a body the link does not give takes DefaultSubject or DefaultBody. The
// subject is a header: any line break in it becomes a space.
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

	var subject, body string
	var haveSubject, haveBody bool
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
			case "body":
				if haveBody {
					continue
				}
				if body, err = url.PathUnescape(value); err != nil {
					return Request{}, ErrBadEncoding
				}
				haveBody = true
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

	subject = strings.Join(strings.Fields(subject), " ")
	if subject == "" {
		subject = DefaultSubject
	}
	if strings.TrimSpace(body) == "" {
		body = DefaultBody
	}
	return Request{To: to, Subject: subject, Body: body}, nil
}
