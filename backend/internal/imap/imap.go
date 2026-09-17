// Package imap is the IMAP transport: the outbound client that performs, over a
// real mail server, the actions internal/mailbox describes.
//
// It is the second implementation of the same intent, and it exists because of
// what the edition rule decided: the hosted service can never use the Gmail API
// (a shared OAuth client is capped by Google at 100 authorizations for the
// lifetime of the Cloud project, and that cap does not reset), so it reaches
// Gmail, and every other provider, over IMAP with an app password.
//
// The division of labour with internal/mailbox is the point of both packages.
// mailbox says WHAT a verb means over IMAP: a flag change, or a move, and to
// which kind of folder. This package does the I/O and answers the one question
// that needs a live connection, which is what that kind of folder is CALLED on
// this particular server. Nothing here knows the word "archive"; nothing there
// opens a socket.
package imap

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	imapv2 "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
)

// ErrAuth means the server rejected the credentials. It is typed because it
// maps to a different answer than anything else that can go wrong here: the
// user has to supply a new app password, which is a 401 and a reconnect prompt,
// not a 500 and a retry. It is the IMAP counterpart of errReauthRequired.
var ErrAuth = errors.New("imap: authentication rejected")

// ErrNoSuchFolder means the server has no mailbox for a special folder the
// action needs. Typed for the same reason: it is a fact about the account, not
// a transient failure, and retrying cannot fix it.
var ErrNoSuchFolder = errors.New("imap: no mailbox matches the required special folder")

// dialTimeout bounds the connection itself. Mail servers under load are slow to
// accept, but a user waiting on a screen is not, and every caller here is on a
// request path.
const dialTimeout = 15 * time.Second

// Credentials is what it takes to open a mailbox. The endpoint comes from
// internal/provider (resolved from the user's address), so nothing here
// hardcodes a host or a port.
type Credentials struct {
	Endpoint provider.Endpoint
	Username string
	Password string
}

// Client is one authenticated IMAP connection with its folder map resolved.
//
// It is not safe for concurrent use, and deliberately so: IMAP is a stateful
// protocol where commands act on the currently selected mailbox, so two
// goroutines sharing a connection would silently act on each other's folder.
// One connection per unit of work, closed when done.
type Client struct {
	c *imapclient.Client
	// folders maps a special folder onto its real name on THIS server. Resolved
	// once at connect, because it costs a LIST and cannot change mid-session in
	// any way that matters.
	folders map[mailbox.Folder]string
	// selected is the mailbox currently selected, so repeated actions in the
	// same folder do not re-SELECT.
	selected string
}

// Connect opens, encrypts and authenticates a connection, then resolves the
// server's special folders.
//
// The TLS mode is not a guess: implicit means the socket is encrypted before a
// byte is exchanged (993), STARTTLS means it is upgraded after the greeting
// (143). Getting it backwards does not fail cleanly, it hangs, which is why the
// catalog carries the mode per route rather than leaving it to be sniffed here.
// There is no third branch: an unencrypted connection would send the user's app
// password in the clear and is not offered at all.
func Connect(ctx context.Context, creds Credentials) (*Client, error) {
	addr := net.JoinHostPort(creds.Endpoint.Host, fmt.Sprint(creds.Endpoint.Port))
	options := &imapclient.Options{
		Dialer:    &net.Dialer{Timeout: dialTimeout},
		TLSConfig: &tls.Config{ServerName: creds.Endpoint.Host, MinVersion: tls.VersionTLS12},
	}

	var (
		raw *imapclient.Client
		err error
	)
	switch creds.Endpoint.TLS {
	case provider.TLSImplicit:
		raw, err = imapclient.DialTLS(addr, options)
	case provider.TLSSTARTTLS:
		raw, err = imapclient.DialStartTLS(addr, options)
	default:
		return nil, fmt.Errorf("imap: unsupported TLS mode %q", creds.Endpoint.TLS)
	}
	if err != nil {
		return nil, fmt.Errorf("imap: dial %s: %w", addr, err)
	}
	return newClient(raw, creds.Username, creds.Password)
}

// newClient authenticates an already-connected socket and resolves its folders.
//
// Split from Connect so everything after the handshake can be exercised against
// a real IMAP server in the tests. The TLS dial above is the only part that is
// not covered that way, because trusting a test certificate would mean giving
// this package a way to relax its own TLS, and a seam that can weaken
// production TLS is worth more than the coverage it buys.
func newClient(raw *imapclient.Client, username, password string) (*Client, error) {
	if err := raw.Login(username, password).Wait(); err != nil {
		raw.Close()
		return nil, fmt.Errorf("%w: %v", ErrAuth, err)
	}

	client := &Client{c: raw}
	if err := client.resolveFolders(); err != nil {
		client.Close()
		return nil, err
	}
	return client, nil
}

// Close ends the session. Logout is attempted first so the server can release
// the mailbox cleanly, but a failure there is not worth reporting: the
// connection is being dropped either way.
func (c *Client) Close() error {
	if c.c == nil {
		return nil
	}
	_ = c.c.Logout().Wait()
	return c.c.Close()
}

// folderInfo is one mailbox as the server described it.
type folderInfo struct {
	Name  string
	Attrs []string
}

// resolveFolders asks the server what it calls the folders the actions need.
func (c *Client) resolveFolders() error {
	listed, err := c.c.List("", "*", &imapv2.ListOptions{ReturnSpecialUse: true}).Collect()
	if err != nil {
		return fmt.Errorf("imap: list mailboxes: %w", err)
	}

	infos := make([]folderInfo, 0, len(listed))
	for _, data := range listed {
		attrs := make([]string, 0, len(data.Attrs))
		for _, a := range data.Attrs {
			attrs = append(attrs, string(a))
		}
		infos = append(infos, folderInfo{Name: data.Mailbox, Attrs: attrs})
	}

	c.folders = map[mailbox.Folder]string{mailbox.FolderInbox: "INBOX"}
	for _, want := range []mailbox.Folder{mailbox.FolderArchive, mailbox.FolderTrash} {
		if name, ok := pickFolder(want, infos); ok {
			c.folders[want] = name
		}
	}
	return nil
}

// Client is a mailbox.Mailbox, asserted here so the contract is checked by the
// compiler rather than by whoever wires it up.
var _ mailbox.Mailbox = (*Client)(nil)

// Apply performs one mutation on one message.
//
// The reference must be folder-scoped. An IMAP UID identifies a message inside
// a mailbox, not on the account, so a UID without its folder points at whatever
// happens to hold that number in whatever happens to be selected. Defaulting to
// the inbox would turn a wiring mistake into a silent action on the wrong mail,
// so an account-wide reference is refused instead.
//
// A move also ends the validity of the UID the caller holds: the same message
// has a different one in its new folder. That is IMAP, not a shortcoming here,
// and the caller has to re-resolve after a move.
func (c *Client) Apply(ctx context.Context, ref mailbox.Ref, m mailbox.Mutation) error {
	if !ref.Scoped() {
		return fmt.Errorf("%w: an imap uid needs its folder, got %s", mailbox.ErrWrongRefKind, ref)
	}
	op, err := mailbox.IMAPOpFor(m)
	if err != nil {
		return err
	}
	num, err := parseUID(ref.ID)
	if err != nil {
		return err
	}
	if err := c.selectFolder(ref.Folder); err != nil {
		return err
	}

	set := imapv2.UIDSetNum(num)
	switch op.Kind {
	case mailbox.IMAPFlagChange:
		return c.storeFlags(set, op)
	case mailbox.IMAPMove:
		name, ok := c.folders[op.To]
		if !ok {
			return fmt.Errorf("%w: %s", ErrNoSuchFolder, op.To)
		}
		if name == c.selected {
			// Moving a message into the folder it is already in is a no-op the
			// server would either reject or duplicate. Archiving something
			// already archived must stay harmless.
			return nil
		}
		if _, err := c.c.Move(set, name).Wait(); err != nil {
			return fmt.Errorf("imap: move to %q: %w", name, err)
		}
		return nil
	}
	return fmt.Errorf("imap: unsupported operation kind %v", op.Kind)
}

// storeFlags issues the add and the remove as separate STORE commands, because
// IMAP has no single command that does both and pretending otherwise would
// silently drop one half.
func (c *Client) storeFlags(set imapv2.UIDSet, op mailbox.IMAPOp) error {
	for _, step := range []struct {
		flags  []string
		imapOp imapv2.StoreFlagsOp
	}{
		{op.AddFlags, imapv2.StoreFlagsAdd},
		{op.DropFlags, imapv2.StoreFlagsDel},
	} {
		if len(step.flags) == 0 {
			continue
		}
		flags := make([]imapv2.Flag, 0, len(step.flags))
		for _, f := range step.flags {
			flags = append(flags, imapv2.Flag(f))
		}
		store := &imapv2.StoreFlags{Op: step.imapOp, Flags: flags, Silent: true}
		if err := c.c.Store(set, store, nil).Close(); err != nil {
			return fmt.Errorf("imap: store flags %v: %w", step.flags, err)
		}
	}
	return nil
}

// selectFolder selects a mailbox unless it is already the selected one.
func (c *Client) selectFolder(folder string) error {
	if folder == "" {
		folder = "INBOX"
	}
	if folder == c.selected {
		return nil
	}
	if _, err := c.c.Select(folder, nil).Wait(); err != nil {
		return fmt.Errorf("imap: select %q: %w", folder, err)
	}
	c.selected = folder
	return nil
}

// parseUID reads a UID, and refuses anything that is not ENTIRELY one.
//
// The whole-string requirement is the point. A Gmail API message id such as
// "18c8c1f2a3b4d5e6" begins with digits, so a parser that stops at the first
// non-digit turns it into the UID 18 and the action lands on a different
// message: the wrong mail archived, silently, with a ledger entry saying it
// went well.
func parseUID(uid string) (imapv2.UID, error) {
	n, err := strconv.ParseUint(strings.TrimSpace(uid), 10, 32)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("imap: %q is not a message uid", uid)
	}
	return imapv2.UID(n), nil
}

// conventionalNames are the mailbox names to fall back on, per special folder,
// when the server does not advertise RFC 6154 special-use attributes.
//
// The fallback is not a nicety: SPECIAL-USE is an extension, and plenty of
// servers in the catalog never send it. Without this, archiving would fail on
// them with "no such folder" even though the folder is right there, named in
// the user's own language. Matching is case-insensitive, and the list is
// ordered: the first match wins, so the most specific name comes first.
var conventionalNames = map[mailbox.Folder][]string{
	mailbox.FolderArchive: {
		"[Gmail]/All Mail", "[Google Mail]/All Mail", // Gmail, and its old brand in some locales
		"Archive", "Archives",
		"Tous les messages", // Gmail in French
		"Archiv", "Archivio", "Archivo",
	},
	mailbox.FolderTrash: {
		"[Gmail]/Trash", "[Google Mail]/Trash",
		"Trash", "Deleted Items", "Deleted Messages",
		"Corbeille", // French, including Gmail in French
		"Papierkorb", "Cestino", "Papelera",
	},
}

// pickFolder chooses the real mailbox name for a wanted special folder.
//
// Pure, and separated from the LIST that feeds it, because the choice is where
// the bugs live: an attribute match must beat a name match (a server that says
// which folder is the archive is telling the truth, while a name that merely
// looks like one may be a folder the user created), and a name match must not
// be case-sensitive.
func pickFolder(want mailbox.Folder, candidates []folderInfo) (string, bool) {
	for _, f := range candidates {
		for _, attr := range f.Attrs {
			if strings.EqualFold(attr, string(want)) {
				return f.Name, true
			}
		}
	}
	// Gmail marks All Mail as \All rather than \Archive, and All Mail is where
	// an archived message lives there, so it is the right destination.
	if want == mailbox.FolderArchive {
		for _, f := range candidates {
			for _, attr := range f.Attrs {
				if strings.EqualFold(attr, "\\All") {
					return f.Name, true
				}
			}
		}
	}
	for _, name := range conventionalNames[want] {
		for _, f := range candidates {
			if strings.EqualFold(f.Name, name) {
				return f.Name, true
			}
		}
	}
	return "", false
}
