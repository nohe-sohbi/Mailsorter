// Package smtpsend hands one message to the sending server of a mailbox
// reached over IMAP: the outbound client behind the daily recap on that
// transport.
//
// Over the Gmail API the recap leaves through Gmail itself. An IMAP mailbox
// has a sending server next to its IMAP one, and the recap leaves from there,
// logged in with the address and the app password the user connected with,
// like a message they would write themselves. That is the whole job: connect,
// encrypt, log in, hand over one message, quit.
//
// It holds the same line as internal/imap on the connection. There are two
// ways in, implicit TLS (465) and STARTTLS (587), and no third: a server that
// does not offer STARTTLS on a STARTTLS route is refused before the password
// is sent, never talked to in the clear. The certificate is checked against
// the system roots, with nothing here to relax it (the tests trust their own
// authority through SSL_CERT_FILE, as the acceptance scenarios do).
package smtpsend

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
)

// ErrAuth means the server refused the address and the app password. It is
// typed because it is the user's to fix (a revoked or replaced app password),
// which is a different answer from a server that is down.
var ErrAuth = errors.New("smtpsend: the server refused the credentials")

// ErrBlocked means the provider refused the sign-in for where it came from,
// not for the password: Gmail answers "534 5.7.14" when a connection from a
// hosting provider looks unusual to it. Typed apart from ErrAuth because a
// new app password does not fix it: the person allows the sign-in from their
// account's security page, and the password they have is still the right one.
var ErrBlocked = errors.New("smtpsend: the provider blocked the sign-in from this server")

// ErrUnreachable means no connection could be opened to the server. Typed
// because it is nobody's mistake: the answer is to try again later.
var ErrUnreachable = errors.New("smtpsend: the server could not be reached")

// timeout bounds one send, connection included, whatever the caller's
// context allows. A sending server that stops answering must not hold the
// recap loop, which sends a few users at a time.
const timeout = 30 * time.Second

// Credentials is what it takes to send as the user: where their sending
// server is (from internal/provider, through the stored mailbox), and the
// address and app password they connected with.
type Credentials struct {
	Endpoint provider.Endpoint
	Username string
	Password string
}

// Send hands message (RFC 5322, see internal/mailer) to the server, with
// from as the envelope sender and to as the envelope recipients.
func Send(ctx context.Context, creds Credentials, from string, to []string, message []byte) error {
	host := creds.Endpoint.Host
	addr := net.JoinHostPort(host, strconv.Itoa(creds.Endpoint.Port))
	mode := creds.Endpoint.TLS
	if mode != provider.TLSImplicit && mode != provider.TLSSTARTTLS {
		return fmt.Errorf("smtpsend: unsupported TLS mode %q", mode)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("%w: dial %s: %v", ErrUnreachable, addr, err)
	}
	// Every read and write below is bounded by the context: its deadline as
	// the socket's, and its cancellation by closing the socket.
	if deadline, ok := ctx.Deadline(); ok {
		if err := raw.SetDeadline(deadline); err != nil {
			raw.Close()
			return fmt.Errorf("smtpsend: set deadline on %s: %w", addr, err)
		}
	}
	stop := context.AfterFunc(ctx, func() { raw.Close() })
	defer stop()

	tlsConfig := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	conn := raw
	if mode == provider.TLSImplicit {
		tc := tls.Client(raw, tlsConfig)
		if err := tc.HandshakeContext(ctx); err != nil {
			raw.Close()
			return fmt.Errorf("smtpsend: TLS with %s: %w", addr, err)
		}
		conn = tc
	}

	// NewClient reads the greeting, and takes the session as encrypted when
	// conn is a *tls.Conn: smtp.PlainAuth refuses to send a password
	// otherwise.
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtpsend: greeting from %s: %w", addr, err)
	}
	defer c.Close()

	if mode == provider.TLSSTARTTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return fmt.Errorf("smtpsend: %s does not offer STARTTLS, and nothing is sent in the clear", addr)
		}
		if err := c.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("smtpsend: STARTTLS with %s: %w", addr, err)
		}
	}

	if err := c.Auth(smtp.PlainAuth("", creds.Username, creds.Password, host)); err != nil {
		if refusal := authRefusal(err); refusal != nil {
			return fmt.Errorf("%w: %s: %v", refusal, addr, err)
		}
		return fmt.Errorf("smtpsend: AUTH on %s: %w", addr, err)
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("smtpsend: MAIL FROM on %s: %w", addr, err)
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return fmt.Errorf("smtpsend: RCPT TO on %s: %w", addr, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtpsend: DATA on %s: %w", addr, err)
	}
	if _, err := w.Write(message); err != nil {
		return fmt.Errorf("smtpsend: write the message to %s: %w", addr, err)
	}
	// Close ends the DATA and reads the server's verdict on the message.
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtpsend: %s did not take the message: %w", addr, err)
	}
	// The message is taken at this point: a failed QUIT does not undo it,
	// and reporting the send as failed would make the user send it twice.
	_ = c.Quit()
	return nil
}

// authRefusal names an AUTH failure the user can act on: ErrAuth when the
// server refused the credentials (RFC 4954: 535, and the "534 5.7.9" Gmail
// answers when an account password is given where an app password is
// needed), ErrBlocked when it refused where the sign-in came from (Gmail's
// "534 5.7.14"). Anything else, another 534 included, is nil: calling it a
// refused password would send the person to replace one that is right.
func authRefusal(err error) error {
	var reply *textproto.Error
	if !errors.As(err, &reply) {
		return nil
	}
	// The enhanced status code (RFC 3463) opens the reply's text.
	enhanced := ""
	if fields := strings.Fields(reply.Msg); len(fields) > 0 {
		enhanced = fields[0]
	}
	switch {
	case reply.Code == 535, reply.Code == 534 && enhanced == "5.7.9":
		return ErrAuth
	case reply.Code == 534 && enhanced == "5.7.14":
		return ErrBlocked
	}
	return nil
}
