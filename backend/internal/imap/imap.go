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
	"log"
	"mime"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	imapv2 "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/charset"
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

// ErrMessageGone means the message a reference names is no longer in its
// folder: another mail client moved it elsewhere or deleted it.
var ErrMessageGone = errors.New("imap: the message is no longer in its folder")

// ErrNotOwnFolder means a mail was to be filed into a folder that is not one
// of the user's own: the inbox, a special folder (the archive, the trash, sent
// mail, drafts, junk), or one that cannot hold mail. Those have actions of
// their own, and filing there would archive or trash a mail under another name
// (or, for the inbox, move it nowhere while it is recorded as gone).
var ErrNotOwnFolder = errors.New("imap: not a folder of the user's own")

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
	// listed is every folder the server listed at connect, plus those this
	// session made since: what OwnFolders offers, and where filing looks for
	// the folder it names before making it.
	listed []folderInfo
	// ns is where the user's own folders live on this server and how their
	// names nest, read at connect.
	ns namespace
	// created counts the folders this connection made (maxNewFolders).
	created int
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
// clientOptions are the connection options for a server named host.
func clientOptions(host string) *imapclient.Options {
	return &imapclient.Options{
		Dialer:    &net.Dialer{Timeout: dialTimeout},
		TLSConfig: &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12},
		// Without it go-imap decodes only UTF-8, ISO-8859-1 and US-ASCII
		// encoded words, and leaves a windows-1252 or ISO-8859-15 sender
		// name or subject (common in French mail) as raw RFC 2047.
		WordDecoder: &mime.WordDecoder{CharsetReader: charset.Reader},
	}
}

func Connect(ctx context.Context, creds Credentials) (*Client, error) {
	addr := net.JoinHostPort(creds.Endpoint.Host, fmt.Sprint(creds.Endpoint.Port))
	options := clientOptions(creds.Endpoint.Host)

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

// Closed says whether the connection is gone: closed here, or by go-imap
// on an answer it could not read. Nothing more can be asked on it.
func (c *Client) Closed() bool {
	return c.c == nil || c.c.State() == imapv2.ConnStateLogout
}

// folderInfo is one mailbox as the server described it.
type folderInfo struct {
	Name  string
	Attrs []string
}

// resolveFolders asks the server what it calls the folders the actions need,
// and where the user's own folders live (NAMESPACE, when the server has it).
func (c *Client) resolveFolders() error {
	listed, err := c.c.List("", "*", &imapv2.ListOptions{ReturnSpecialUse: true}).Collect()
	if err != nil {
		return fmt.Errorf("imap: list mailboxes: %w", err)
	}

	infos := make([]folderInfo, 0, len(listed))
	var listDelim rune
	for _, data := range listed {
		attrs := make([]string, 0, len(data.Attrs))
		for _, a := range data.Attrs {
			attrs = append(attrs, string(a))
		}
		infos = append(infos, folderInfo{Name: data.Mailbox, Attrs: attrs})
		if listDelim == 0 || strings.EqualFold(data.Mailbox, string(mailbox.FolderInbox)) {
			if data.Delim != 0 {
				listDelim = data.Delim
			}
		}
	}

	var spaces imapv2.NamespaceData
	if c.c.Caps().Has(imapv2.CapNamespace) {
		data, err := c.c.Namespace().Wait()
		if err != nil {
			// The folders are still usable as listed: without the prefix,
			// a folder Mailsorter makes lands at the top, as before.
			log.Printf("imap: the server announces NAMESPACE but did not answer it: %v", err)
		} else {
			spaces = *data
		}
	}

	c.ns = readNamespace(spaces, listDelim)
	c.listed = infos
	c.folders = map[mailbox.Folder]string{mailbox.FolderInbox: "INBOX"}
	for _, want := range []mailbox.Folder{mailbox.FolderArchive, mailbox.FolderTrash} {
		if name, ok := pickFolder(want, infos, c.ns); ok {
			c.folders[want] = name
		}
	}
	return nil
}

// OwnFolders returns every folder of the user's own, sorted, by the name the
// user reads: the folders a mail can be filed into ("Factures",
// "Projets/Maison" for a folder inside another), which is what a label is over
// IMAP. Without the personal prefix and with "/" between levels, whatever the
// server's own ("INBOX.Projets.Maison" reads "Projets/Maison"). Not the inbox,
// not a special folder, not a folder that cannot hold mail or lies outside the
// user's namespace (notOwn).
func (c *Client) OwnFolders() []string {
	skip := notOwn(c.listed, c.ns)
	names := make([]string, 0, len(c.listed))
	for _, f := range c.listed {
		if skip[f.Name] {
			continue
		}
		if shown, ok := c.ns.display(f.Name); ok {
			names = append(names, shown)
		}
	}
	sort.Strings(names)
	return names
}

// maxNewFolders is how many folders one connection, so one request, makes at
// most: "Tout ranger" files up to 200 mails, and a model that names a new
// folder for each would otherwise strew the mailbox with them. Past it, the
// filing answers ErrTooManyNewFolders and the mail stays where it is.
const maxNewFolders = 10

// ErrTooManyNewFolders is a filing that would make one folder more than
// maxNewFolders in the same request. Nothing moves; another request can.
var ErrTooManyNewFolders = errors.New("imap: too many new folders for one request")

// Destination returns the server's name for where op moves a message: a
// special folder by what it is (op.To), or one of the user's own folders by
// the name the user reads (op.Into, as OwnFolders and FilingName give it),
// made on the server when it does not exist yet, which is how filing into
// "Factures" works the first time. It is made under the user's prefix, levels
// split by the server's delimiter ("Projets/Maison" is "INBOX.Projets.Maison"
// on Courier), and only under a name newFolderName accepts.
//
// The folder the user already has is found by its exact name first, then by
// the same name in another case: names are case-sensitive on most servers, but
// a user who keeps "factures" wants the mail there, not in a second folder
// next to it. A name that is, or would be taken for, a folder that is not the
// user's own answers ErrNotOwnFolder, and nothing is made.
func (c *Client) Destination(op mailbox.IMAPOp) (string, error) {
	if op.Into == "" {
		name, ok := c.folders[op.To]
		if !ok {
			return "", fmt.Errorf("%w: %s", ErrNoSuchFolder, op.To)
		}
		return name, nil
	}
	want := strings.TrimSpace(op.Into)
	if want == "" {
		return "", mailbox.ErrLabelRequired
	}
	if found, err := c.existing(want); err != nil || found != "" {
		return found, err
	}
	name, err := c.newFolderName(want)
	if err != nil {
		return "", err
	}
	if found, err := c.existing(name); err != nil || found != "" {
		return found, err
	}
	if c.created >= maxNewFolders {
		return "", fmt.Errorf("%w: %q would be folder %d", ErrTooManyNewFolders, name, c.created+1)
	}
	server := c.ns.serverName(name)
	if err := c.c.Create(server, nil).Wait(); err != nil {
		// Another connection may have made it a moment ago: the server then
		// refuses, and the folder is there all the same.
		again, lErr := c.c.List("", server, nil).Collect()
		exists := false
		for _, l := range again {
			exists = exists || l.Mailbox == server
		}
		if lErr != nil || !exists {
			return "", fmt.Errorf("imap: create folder %q: %w", server, err)
		}
	}
	c.created++
	// Most mail programs show only the folders the account subscribes to: a
	// folder made without it holds mail the user's phone never shows. The
	// folder is made either way, so a refusal here is only logged.
	if err := c.c.Subscribe(server).Wait(); err != nil {
		log.Printf("imap: folder %q made, but not subscribed: %v", server, err)
	}
	c.listed = append(c.listed, folderInfo{Name: server})
	return server, nil
}

// FilingName is the name, as the user reads it, of the folder a label the
// model proposed files into, and makes nothing: the analysis stores it on the
// proposal, so the plan shows, "Appliquer" files into and the history records
// the same folder. One of the user's own folders when one is named alike
// (mailbox.MatchLabel: case, accents and a plural set aside, never a part of
// a name), or else the name Destination would make, cleaned (cleanName).
// ErrNotOwnFolder for a special folder, ErrBadFolderName for a name no folder
// is made under.
func (c *Client) FilingName(label string) (string, error) {
	want := strings.TrimSpace(label)
	if want == "" {
		return "", mailbox.ErrLabelRequired
	}
	own := c.OwnFolders()
	if name, ok := mailbox.MatchLabel(want, own); ok {
		return name, nil
	}
	if _, err := c.existing(want); err != nil {
		return "", err
	}
	name, err := c.newFolderName(want)
	if err != nil {
		return "", err
	}
	if mine, ok := mailbox.MatchLabel(name, own); ok {
		return mine, nil
	}
	return name, nil
}

// existing is the server's name of the folder the user reads as want (the
// exact name, then the same in another case), "" when there is none, and
// ErrNotOwnFolder when it is not the user's own.
func (c *Client) existing(want string) (string, error) {
	skip := notOwn(c.listed, c.ns)
	for _, exact := range []bool{true, false} {
		for _, f := range c.listed {
			shown, ok := c.ns.display(f.Name)
			if !ok {
				// Outside the user's namespace: the inbox, a shared
				// folder. Matched by its own name, and refused below.
				shown = f.Name
			}
			if (exact && shown == want) || (!exact && strings.EqualFold(shown, want)) {
				if skip[f.Name] {
					return "", fmt.Errorf("%w: %q", ErrNotOwnFolder, f.Name)
				}
				return f.Name, nil
			}
		}
	}
	return "", nil
}

// newFolderName is the name, as the user reads it, of a folder Mailsorter may
// make for the label want: cleaned (cleanName), and neither under a first
// level that is not the user's own ("INBOX/Factures", "Trash/Factures",
// "Sent/Old", a shared prefix) nor itself taken for one: the next listing
// would leave it out, and filing there again would be refused.
func (c *Client) newFolderName(want string) (string, error) {
	name, err := c.ns.cleanName(want)
	if err != nil {
		return "", err
	}
	first := strings.SplitN(name, "/", 2)[0]
	for _, candidate := range []string{first, name} {
		server := c.ns.serverName(candidate)
		withIt := append(append(make([]folderInfo, 0, len(c.listed)+1), c.listed...), folderInfo{Name: server})
		if notOwn(withIt, c.ns)[server] {
			return "", fmt.Errorf("%w: %q", ErrNotOwnFolder, candidate)
		}
	}
	return name, nil
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
		name, err := c.Destination(op)
		if err != nil {
			return err
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

// MoveTo moves one message into the folder the server calls dest
// (Destination says which) and says where it landed: that folder, and the
// message's new UID there.
//
// The new UID comes from the server's answer to MOVE (COPYUID, which UIDPLUS
// and IMAP4rev2 servers send). A server that does not send it is asked again
// by the message's Message-ID header, when the caller knows it. A UID still
// unknown after that comes back empty: the move happened, but the message
// cannot be named in its new folder.
//
// A message no longer in ref's folder answers ErrMessageGone, checked BEFORE
// moving: a MOVE on a UID that does not exist answers OK and does nothing,
// which is how undo used to "succeed" without anything coming back.
func (c *Client) MoveTo(ctx context.Context, ref mailbox.Ref, dest, headerMessageID string) (mailbox.Ref, error) {
	if !ref.Scoped() {
		return mailbox.Ref{}, fmt.Errorf("%w: an imap uid needs its folder, got %s", mailbox.ErrWrongRefKind, ref)
	}
	num, err := parseUID(ref.ID)
	if err != nil {
		return mailbox.Ref{}, err
	}
	if dest == "" {
		return mailbox.Ref{}, fmt.Errorf("%w: no destination", ErrNoSuchFolder)
	}
	if err := c.selectFolder(ref.Folder); err != nil {
		return mailbox.Ref{}, err
	}
	set := imapv2.UIDSetNum(num)
	found, err := c.c.UIDSearch(&imapv2.SearchCriteria{UID: []imapv2.UIDSet{set}}, nil).Wait()
	if err != nil {
		return mailbox.Ref{}, fmt.Errorf("imap: look for uid %s in %q: %w", ref.ID, ref.Folder, err)
	}
	if len(found.AllUIDs()) == 0 {
		return mailbox.Ref{}, ErrMessageGone
	}
	if dest == c.selected {
		return ref, nil
	}
	data, err := c.c.Move(set, dest).Wait()
	if err != nil {
		// Another connection (a second click, another mail client) may have
		// moved it between the check above and the MOVE: that is "gone",
		// not a failure.
		if again, sErr := c.c.UIDSearch(&imapv2.SearchCriteria{UID: []imapv2.UIDSet{set}}, nil).Wait(); sErr == nil && len(again.AllUIDs()) == 0 {
			return mailbox.Ref{}, ErrMessageGone
		}
		return mailbox.Ref{}, fmt.Errorf("imap: move to %q: %w", dest, err)
	}
	landed := mailbox.InFolder(dest, "")
	if data != nil {
		if uids, ok := data.DestUIDs.(imapv2.UIDSet); ok {
			if nums, ok := uids.Nums(); ok && len(nums) == 1 {
				landed.ID = strconv.FormatUint(uint64(nums[0]), 10)
			}
		}
	}
	if landed.ID == "" && headerMessageID != "" {
		// The move happened either way; a failed lookup only means the new
		// UID stays unknown, which the caller handles.
		id, err := c.FindByMessageID(dest, headerMessageID)
		if err != nil {
			log.Printf("imap: moved to %q, but could not find %s there again: %v", dest, headerMessageID, err)
		}
		landed.ID = id
	}
	return landed, nil
}

// MoveRecorded moves the message Mailsorter recorded as uid in folder into
// the folder the server calls dest. When
// its Message-ID is known, the number is checked first against the one the
// server reports for it (IDAt): a server that renumbered its folders (a
// migration, a rebuilt index) would otherwise hand back another mail. When
// the number no longer names it, it is looked for by that Message-ID once;
// not found, ErrMessageGone.
func (c *Client) MoveRecorded(ctx context.Context, folder, uid, dest, headerMessageID string) (mailbox.Ref, error) {
	if headerMessageID != "" {
		id, exists, err := c.IDAt(folder, uid)
		if err != nil {
			return mailbox.Ref{}, err
		}
		if !exists || id != headerMessageID {
			found, err := c.FindByMessageID(folder, headerMessageID)
			if err != nil {
				return mailbox.Ref{}, err
			}
			if found == "" {
				return mailbox.Ref{}, ErrMessageGone
			}
			uid = found
		}
	}
	return c.MoveTo(ctx, mailbox.InFolder(folder, uid), dest, headerMessageID)
}

// IDAt returns the Message-ID of the message numbered uid in folder, as the
// listing stores it (the envelope's, parsed by the same code), and whether
// that number names a message at all.
func (c *Client) IDAt(folder, uid string) (string, bool, error) {
	num, err := parseUID(uid)
	if err != nil {
		return "", false, err
	}
	c.selected = ""
	if err := c.selectFolder(folder); err != nil {
		return "", false, err
	}
	msgs, err := c.c.Fetch(imapv2.UIDSetNum(num), &imapv2.FetchOptions{UID: true, Envelope: true}).Collect()
	if err != nil {
		return "", false, fmt.Errorf("imap: read uid %s in %q: %w", uid, folder, err)
	}
	for _, m := range msgs {
		if m.UID == num {
			if m.Envelope == nil {
				return "", true, nil
			}
			return m.Envelope.MessageID, true, nil
		}
	}
	return "", false, nil
}

// Has says whether the message ref names is in its folder now.
func (c *Client) Has(ref mailbox.Ref) (bool, error) {
	num, err := parseUID(ref.ID)
	if err != nil {
		return false, err
	}
	c.selected = ""
	if err := c.selectFolder(ref.Folder); err != nil {
		return false, err
	}
	res, err := c.c.UIDSearch(&imapv2.SearchCriteria{UID: []imapv2.UIDSet{imapv2.UIDSetNum(num)}}, nil).Wait()
	if err != nil {
		return false, fmt.Errorf("imap: look for uid %s in %q: %w", ref.ID, ref.Folder, err)
	}
	return len(res.AllUIDs()) > 0, nil
}

// FindByMessageID returns the highest UID in folder whose Message-ID header
// is id, "" when there is none, and an error when the server could not be
// asked. It is how a message is found again after a move when the server did
// not say where it landed.
func (c *Client) FindByMessageID(folder, id string) (string, error) {
	if err := c.selectFolder(folder); err != nil {
		return "", err
	}
	res, err := c.c.UIDSearch(&imapv2.SearchCriteria{
		Header: []imapv2.SearchCriteriaHeaderField{{Key: "Message-ID", Value: id}},
	}, nil).Wait()
	if err != nil {
		return "", fmt.Errorf("imap: search %q by Message-ID: %w", folder, err)
	}
	uids := res.AllUIDs()
	if len(uids) == 0 {
		return "", nil
	}
	best := uids[0]
	for _, u := range uids[1:] {
		if u > best {
			best = u
		}
	}
	return strconv.FormatUint(uint64(best), 10), nil
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
	// No action moves a message to the three below: they are here so that
	// the folders a mail can be filed into leave them out (notOwn).
	mailbox.FolderSent: {
		"[Gmail]/Sent Mail", "[Google Mail]/Sent Mail",
		"Sent", "Sent Items", "Sent Messages", "Sent Mail",
		"Envoyés", "Messages envoyés", "Éléments envoyés",
		"Gesendet", "Gesendete Objekte", "Posta inviata", "Enviados", "Elementos enviados",
	},
	mailbox.FolderDrafts: {
		"[Gmail]/Drafts", "[Google Mail]/Drafts",
		"Drafts", "Brouillons",
		"Entwürfe", "Bozze", "Borradores",
	},
	mailbox.FolderJunk: {
		"[Gmail]/Spam", "[Google Mail]/Spam",
		"Junk", "Junk E-mail", "Junk Email", "Spam", "Bulk Mail",
		"Courrier indésirable", "Indésirables", "Pourriels",
		"Junk-E-Mail", "Posta indesiderata", "Correo no deseado",
	},
}

// specialUses are the RFC 6154 attributes (and \Important, RFC 8457) of the
// folders that are not the user's own. \All, \Flagged and \Important are
// Gmail's views of all, starred and important mail: filing there would not
// file anything.
var specialUses = []string{
	string(mailbox.FolderArchive), string(mailbox.FolderTrash),
	string(mailbox.FolderSent), string(mailbox.FolderDrafts), string(mailbox.FolderJunk),
	"\\All", "\\Flagged", "\\Important",
}

// notOwn returns, among the server's folders, the full names of those that are
// not the user's own: the inbox; a folder outside the user's namespace (ns: a
// shared one, on a server that keeps the user's under a prefix); a folder that
// cannot hold mail (\Noselect, \NonExistent: a branch such as "[Gmail]"); a
// folder the server marks with a special use; and, for a special folder the
// server marks NOWHERE, every folder named like it (conventionalNames, matched
// on the name the user reads, so "INBOX.Sent" is the sent mail under an
// "INBOX." prefix), the way pickFolder finds the archive and the trash on a
// server silent about special use. A name match does not count when the server
// marks that special folder elsewhere: the server is telling the truth, and a
// user's own "Archives" next to Gmail's All Mail is the user's.
//
// Pure, like pickFolder, because the choice is where the bugs live.
func notOwn(infos []folderInfo, ns namespace) map[string]bool {
	out := map[string]bool{}
	marked := map[mailbox.Folder]bool{}
	for _, f := range infos {
		shown, inside := ns.display(f.Name)
		if !inside || strings.EqualFold(f.Name, string(mailbox.FolderInbox)) || strings.EqualFold(shown, string(mailbox.FolderInbox)) {
			out[f.Name] = true
		}
		for _, attr := range f.Attrs {
			if strings.EqualFold(attr, string(imapv2.MailboxAttrNoSelect)) || strings.EqualFold(attr, string(imapv2.MailboxAttrNonExistent)) {
				out[f.Name] = true
			}
			for _, use := range specialUses {
				if strings.EqualFold(attr, use) {
					out[f.Name] = true
				}
			}
			for role := range conventionalNames {
				if strings.EqualFold(attr, string(role)) {
					marked[role] = true
				}
			}
			// Gmail's All Mail stands for the archive (pickFolder).
			if strings.EqualFold(attr, "\\All") {
				marked[mailbox.FolderArchive] = true
			}
		}
	}
	for role, names := range conventionalNames {
		if marked[role] {
			continue
		}
		for _, f := range infos {
			for _, name := range names {
				if strings.EqualFold(ns.readName(f.Name), name) {
					out[f.Name] = true
				}
			}
		}
	}
	return out
}

// pickFolder chooses the real mailbox name for a wanted special folder.
//
// Pure, and separated from the LIST that feeds it, because the choice is where
// the bugs live: an attribute match must beat a name match (a server that says
// which folder is the archive is telling the truth, while a name that merely
// looks like one may be a folder the user created), and a name match must not
// be case-sensitive. A name is matched as the user reads it (ns), so under an
// "INBOX." prefix "INBOX.Trash" is the trash.
func pickFolder(want mailbox.Folder, candidates []folderInfo, ns namespace) (string, bool) {
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
			if strings.EqualFold(ns.readName(f.Name), name) {
				return f.Name, true
			}
		}
	}
	return "", false
}
