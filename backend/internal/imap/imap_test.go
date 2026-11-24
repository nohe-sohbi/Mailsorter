package imap

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strconv"
	"strings"
	"testing"

	imapv2 "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
)

const (
	testUser = "someone@example.com"
	testPass = "app-password"
)

// newTestServer runs a real in-memory IMAP server and returns a connected,
// authenticated Client. The conversation is a genuine one: LOGIN, LIST, SELECT,
// STORE and MOVE all go over a socket and are answered by a server that
// implements the protocol, which is the only way to find out whether this
// package speaks it correctly.
//
// The connection is plaintext because the server holds a test certificate no
// client should trust. Connect's TLS branch is therefore not exercised here on
// purpose: see newClient.
func newTestServer(t *testing.T, mailboxes map[string][]imapv2.MailboxAttr) *Client {
	t.Helper()
	return dialTestServer(t, startTestServer(t, mailboxes))
}

// startTestServer runs the in-memory server and returns its address, for a
// test that connects more than once (or through something in between).
func startTestServer(t *testing.T, mailboxes map[string][]imapv2.MailboxAttr) string {
	t.Helper()
	return serveTestServer(t, mailboxes, nil)
}

// serveTestServer is startTestServer for a test that needs the server to
// behave in a way of its own: wrap, when set, stands between the server and
// each session.
func serveTestServer(t *testing.T, mailboxes map[string][]imapv2.MailboxAttr, wrap func(imapserver.SessionIMAP4rev2) imapserver.Session) string {
	t.Helper()
	return serveTestServerWith(t, mailboxes, wrap, imapv2.CapSet{
		imapv2.CapIMAP4rev1: {},
		imapv2.CapIMAP4rev2: {},
	})
}

// serveTestServerWith is serveTestServer for a server with these
// capabilities: IMAP4rev1 alone is a server without BINARY, like Gmail's.
func serveTestServerWith(t *testing.T, mailboxes map[string][]imapv2.MailboxAttr, wrap func(imapserver.SessionIMAP4rev2) imapserver.Session, caps imapv2.CapSet) string {
	t.Helper()

	user := imapmemserver.NewUser(testUser, testPass)
	for name, attrs := range mailboxes {
		var options *imapv2.CreateOptions
		if len(attrs) > 0 {
			options = &imapv2.CreateOptions{SpecialUse: attrs}
		}
		if err := user.Create(name, options); err != nil {
			t.Fatalf("create mailbox %q: %v", name, err)
		}
	}

	mem := imapmemserver.New()
	mem.AddUser(user)

	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			if wrap != nil {
				return wrap(mem.NewSession().(imapserver.SessionIMAP4rev2)), nil, nil
			}
			return mem.NewSession(), nil, nil
		},
		InsecureAuth: true,
		Caps:         caps,
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() {
		srv.Close()
		ln.Close()
	})
	return ln.Addr().String()
}

// dialTestServer is one authenticated Client on the server at addr.
func dialTestServer(t *testing.T, addr string) *Client {
	t.Helper()
	client, err := dialTestClient(addr)
	if err != nil {
		t.Fatalf("connect to the test server: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

// dialTestServerThrough is dialTestServer with wrap standing between the
// client and its socket, for a test that watches what goes over it.
func dialTestServerThrough(t *testing.T, addr string, wrap func(net.Conn) net.Conn) *Client {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial test server: %v", err)
	}
	client, err := newClient(imapclient.New(wrap(conn), clientOptions("127.0.0.1")), testUser, testPass)
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

// dialTestClient connects and authenticates, and says why it could not.
func dialTestClient(addr string) (*Client, error) {
	raw, err := imapclient.DialInsecure(addr, clientOptions("127.0.0.1"))
	if err != nil {
		return nil, err
	}
	return newClient(raw, testUser, testPass)
}

// standardMailboxes is a server that advertises RFC 6154 special-use, the happy
// case this package resolves folders from.
func standardMailboxes() map[string][]imapv2.MailboxAttr {
	return map[string][]imapv2.MailboxAttr{
		"INBOX":   nil,
		"Archive": {imapv2.MailboxAttrArchive},
		"Trash":   {imapv2.MailboxAttrTrash},
		"Sent":    {imapv2.MailboxAttrSent},
	}
}

// appendMessage puts a message in a mailbox and returns its UID.
func appendMessage(t *testing.T, c *Client, folder string) string {
	t.Helper()
	const raw = "From: sender@example.com\r\n" +
		"To: " + testUser + "\r\n" +
		"Subject: Test\r\n" +
		"\r\n" +
		"Body.\r\n"

	cmd := c.c.Append(folder, int64(len(raw)), nil)
	if _, err := cmd.Write([]byte(raw)); err != nil {
		t.Fatalf("append write: %v", err)
	}
	if err := cmd.Close(); err != nil {
		t.Fatalf("append close: %v", err)
	}
	data, err := cmd.Wait()
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	// The selected mailbox is now stale for the caller's purposes: a new message
	// landed in it. Forget the selection so the next action re-selects.
	c.selected = ""
	return fmtUID(data.UID)
}

func fmtUID(uid imapv2.UID) string {
	return strconv.FormatUint(uint64(uid), 10)
}

// flagsOf reads a message's flags back from the server, which is how these
// tests check what happened rather than trusting the command's own reply.
func flagsOf(t *testing.T, c *Client, folder, uid string) []string {
	t.Helper()
	if err := c.selectFolder(folder); err != nil {
		t.Fatalf("select %q: %v", folder, err)
	}
	num, err := parseUID(uid)
	if err != nil {
		t.Fatalf("parse uid: %v", err)
	}
	msgs, err := c.c.Fetch(imapv2.UIDSetNum(num), &imapv2.FetchOptions{Flags: true}).Collect()
	if err != nil {
		t.Fatalf("fetch flags: %v", err)
	}
	if len(msgs) == 0 {
		return nil
	}
	out := make([]string, 0, len(msgs[0].Flags))
	for _, f := range msgs[0].Flags {
		out = append(out, string(f))
	}
	return out
}

func countIn(t *testing.T, c *Client, folder string) int {
	t.Helper()
	data, err := c.c.Select(folder, nil).Wait()
	if err != nil {
		t.Fatalf("select %q: %v", folder, err)
	}
	c.selected = folder
	return int(data.NumMessages)
}

func has(flags []string, want string) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}

// The flag half of the table, checked by reading the flags back off the server.
// Marking read has to SET \Seen here, the opposite of the Gmail API where it
// removes a label, so this is the assertion that catches the two transports
// being confused for one another.
func TestApplyChangesFlagsOnTheServer(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	uid := appendMessage(t, c, "INBOX")

	if err := c.Apply(context.Background(), mailbox.InFolder("INBOX", uid), mailbox.Mutation{Action: mailbox.ActionMarkRead}); err != nil {
		t.Fatalf("markRead: %v", err)
	}
	if flags := flagsOf(t, c, "INBOX", uid); !has(flags, mailbox.FlagSeen) {
		t.Errorf("after markRead, flags = %v, want %s present", flags, mailbox.FlagSeen)
	}

	if err := c.Apply(context.Background(), mailbox.InFolder("INBOX", uid), mailbox.Mutation{Action: mailbox.ActionMarkUnread}); err != nil {
		t.Fatalf("markUnread: %v", err)
	}
	if flags := flagsOf(t, c, "INBOX", uid); has(flags, mailbox.FlagSeen) {
		t.Errorf("after markUnread, flags = %v, want %s gone", flags, mailbox.FlagSeen)
	}

	if err := c.Apply(context.Background(), mailbox.InFolder("INBOX", uid), mailbox.Mutation{Action: mailbox.ActionStar}); err != nil {
		t.Fatalf("star: %v", err)
	}
	if flags := flagsOf(t, c, "INBOX", uid); !has(flags, mailbox.FlagFlagged) {
		t.Errorf("after star, flags = %v, want %s present", flags, mailbox.FlagFlagged)
	}

	if err := c.Apply(context.Background(), mailbox.InFolder("INBOX", uid), mailbox.Mutation{Action: mailbox.ActionUnstar}); err != nil {
		t.Fatalf("unstar: %v", err)
	}
	if flags := flagsOf(t, c, "INBOX", uid); has(flags, mailbox.FlagFlagged) {
		t.Errorf("after unstar, flags = %v, want %s gone", flags, mailbox.FlagFlagged)
	}
}

// Archiving over IMAP moves the message. Asserting on both folders is the
// point: a move that copied without removing would leave the message in the
// inbox, which is the failure the user would notice and the command's own reply
// would not reveal.
func TestApplyArchiveMovesTheMessageOutOfTheInbox(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	uid := appendMessage(t, c, "INBOX")

	if err := c.Apply(context.Background(), mailbox.InFolder("INBOX", uid), mailbox.Mutation{Action: mailbox.ActionArchive}); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if n := countIn(t, c, "INBOX"); n != 0 {
		t.Errorf("INBOX holds %d message(s) after archiving, want 0", n)
	}
	if n := countIn(t, c, "Archive"); n != 1 {
		t.Errorf("Archive holds %d message(s) after archiving, want 1", n)
	}
}

func TestApplyTrashMovesTheMessageToTrash(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	uid := appendMessage(t, c, "INBOX")

	if err := c.Apply(context.Background(), mailbox.InFolder("INBOX", uid), mailbox.Mutation{Action: mailbox.ActionTrash}); err != nil {
		t.Fatalf("trash: %v", err)
	}
	if n := countIn(t, c, "INBOX"); n != 0 {
		t.Errorf("INBOX holds %d message(s) after trashing, want 0", n)
	}
	if n := countIn(t, c, "Trash"); n != 1 {
		t.Errorf("Trash holds %d message(s) after trashing, want 1", n)
	}
}

// Undo has to work, and over IMAP it is another move rather than the reverse of
// a flag. This is the round trip the ledger promises the user.
func TestApplyUnarchiveBringsTheMessageBack(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	uid := appendMessage(t, c, "INBOX")

	if err := c.Apply(context.Background(), mailbox.InFolder("INBOX", uid), mailbox.Mutation{Action: mailbox.ActionArchive}); err != nil {
		t.Fatalf("archive: %v", err)
	}
	// The move gave the message a new UID in its new folder, which is the IMAP
	// fact the caller has to live with: the id it held is no longer valid.
	archivedUID := firstUID(t, c, "Archive")

	if err := c.Apply(context.Background(), mailbox.InFolder("Archive", archivedUID), mailbox.Mutation{Action: mailbox.ActionUnarchive}); err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	if n := countIn(t, c, "INBOX"); n != 1 {
		t.Errorf("INBOX holds %d message(s) after unarchiving, want 1", n)
	}
	if n := countIn(t, c, "Archive"); n != 0 {
		t.Errorf("Archive holds %d message(s) after unarchiving, want 0", n)
	}
}

func firstUID(t *testing.T, c *Client, folder string) string {
	t.Helper()
	if err := c.selectFolder(folder); err != nil {
		t.Fatalf("select %q: %v", folder, err)
	}
	msgs, err := c.c.Fetch(imapv2.SeqSetNum(1), &imapv2.FetchOptions{UID: true}).Collect()
	if err != nil || len(msgs) == 0 {
		t.Fatalf("fetch uid in %q: %v (%d messages)", folder, err, len(msgs))
	}
	return fmtUID(msgs[0].UID)
}

// Archiving something already archived must stay harmless: the ledger replays
// actions, and a server asked to move a message into its own folder either
// errors or duplicates it.
func TestApplyIsHarmlessWhenTheMessageIsAlreadyThere(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	appendMessage(t, c, "Archive")
	uid := firstUID(t, c, "Archive")

	if err := c.Apply(context.Background(), mailbox.InFolder("Archive", uid), mailbox.Mutation{Action: mailbox.ActionArchive}); err != nil {
		t.Fatalf("archiving an already archived message = %v, want no error", err)
	}
	if n := countIn(t, c, "Archive"); n != 1 {
		t.Errorf("Archive holds %d message(s), want the one that was there", n)
	}
}

// A label over IMAP is the folder of that name: labelling files the message
// there, out of the inbox, and makes the folder the first time. Read back off
// the server, folders and counts, rather than trusting the command's reply.
func TestApplyLabelFilesTheMessageIntoItsFolder(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	uid := appendMessage(t, c, "INBOX")

	if err := c.Apply(context.Background(), mailbox.InFolder("INBOX", uid), mailbox.Mutation{Action: mailbox.ActionLabel, LabelID: "Factures"}); err != nil {
		t.Fatalf("label Factures: %v", err)
	}
	if n := countIn(t, c, "Factures"); n != 1 {
		t.Errorf("Factures holds %d message(s) after labelling, want 1", n)
	}
	if n := countIn(t, c, "INBOX"); n != 0 {
		t.Errorf("INBOX holds %d message(s) after labelling, want 0", n)
	}
	if n := countIn(t, c, "Archive"); n != 0 {
		t.Errorf("Archive holds %d message(s) after labelling, want 0: filed is not archived", n)
	}
	// Made subscribed: most mail programs show no other folder.
	subscribed, err := c.c.List("", "Factures", &imapv2.ListOptions{SelectSubscribed: true}).Collect()
	if err != nil || len(subscribed) != 1 {
		t.Errorf("Factures subscribed = %v (%v), want it listed among the subscribed folders", subscribed, err)
	}
}

// The user's own folder is found again rather than made twice, whatever the
// case the label is spelled in, and a folder made in this session is found by
// the next filing in it.
func TestDestinationFindsTheUsersFolder(t *testing.T) {
	mailboxes := standardMailboxes()
	mailboxes["factures"] = nil
	c := newTestServer(t, mailboxes)

	got, err := c.Destination(mailbox.IMAPOp{Kind: mailbox.IMAPMove, Into: "Factures"})
	if err != nil || got != "factures" {
		t.Errorf("Destination(Factures) with a folder \"factures\" = %q, %v; want the user's \"factures\"", got, err)
	}
	made, err := c.Destination(mailbox.IMAPOp{Kind: mailbox.IMAPMove, Into: "Banque"})
	if err != nil || made != "Banque" {
		t.Fatalf("Destination(Banque) = %q, %v; want it made", made, err)
	}
	if again, err := c.Destination(mailbox.IMAPOp{Kind: mailbox.IMAPMove, Into: "Banque"}); err != nil || again != "Banque" {
		t.Errorf("Destination(Banque) a second time = %q, %v; want the folder just made", again, err)
	}
	if got := c.OwnFolders(); !reflect.DeepEqual(got, []string{"Banque", "factures"}) {
		t.Errorf("OwnFolders = %v, want [Banque factures]", got)
	}
}

// Filing never lands in a folder that has an action of its own: the inbox
// (the message would be recorded as gone while it stays), the archive, the
// trash or sent mail. Nothing is made under such a name either.
func TestDestinationRefusesAFolderThatIsNotTheUsers(t *testing.T) {
	c := newTestServer(t, map[string][]imapv2.MailboxAttr{
		"INBOX":    nil,
		"Archives": nil,
		"Trash":    {imapv2.MailboxAttrTrash},
		"Sent":     {imapv2.MailboxAttrSent},
	})
	for _, name := range []string{"INBOX", "inbox", "Archives", "trash", "Sent", "Brouillons"} {
		if got, err := c.Destination(mailbox.IMAPOp{Kind: mailbox.IMAPMove, Into: name}); !errors.Is(err, ErrNotOwnFolder) {
			t.Errorf("Destination(%q) = %q, %v; want ErrNotOwnFolder", name, got, err)
		}
	}
	listed, err := c.c.List("", "Brouillons", nil).Collect()
	if err != nil || len(listed) != 0 {
		t.Errorf("a refused filing made the folder Brouillons: %v (%v)", listed, err)
	}
}

// The folders a mail can be filed into, against a live server: the user's,
// a folder inside another by its full name, and none of the special ones,
// told by their names when the server does not mark them.
func TestOwnFoldersLeavesOutTheSpecialFolders(t *testing.T) {
	c := newTestServer(t, map[string][]imapv2.MailboxAttr{
		"INBOX":          nil,
		"Archive":        nil,
		"Trash":          nil,
		"Sent":           nil,
		"Drafts":         nil,
		"Junk":           nil,
		"Projets":        nil,
		"Projets/Maison": nil,
	})
	if got := c.OwnFolders(); !reflect.DeepEqual(got, []string{"Projets", "Projets/Maison"}) {
		t.Errorf("OwnFolders = %v, want [Projets Projets/Maison]", got)
	}
}

// notOwn is where the choice lives, so it is pinned on its own, with what a
// live in-memory server cannot send (special-use attributes, \Noselect).
func TestNotOwn(t *testing.T) {
	courier := namespace{prefix: "INBOX.", delim: '.'}
	cases := []struct {
		name    string
		ns      namespace
		folders []folderInfo
		notOwn  []string
	}{
		{
			name: "Gmail marks every special folder, so a label named like one is the user's",
			ns:   namespace{delim: '/'},
			folders: []folderInfo{
				{Name: "INBOX"},
				{Name: "[Gmail]", Attrs: []string{"\\Noselect", "\\HasChildren"}},
				{Name: "[Gmail]/Tous les messages", Attrs: []string{"\\All"}},
				{Name: "[Gmail]/Corbeille", Attrs: []string{"\\Trash"}},
				{Name: "[Gmail]/Messages envoyés", Attrs: []string{"\\Sent"}},
				{Name: "[Gmail]/Brouillons", Attrs: []string{"\\Drafts"}},
				{Name: "[Gmail]/Spam", Attrs: []string{"\\Junk"}},
				{Name: "[Gmail]/Suivis", Attrs: []string{"\\Flagged"}},
				{Name: "[Gmail]/Important", Attrs: []string{"\\Important"}},
				{Name: "Archives"},
				{Name: "Factures"},
			},
			notOwn: []string{"INBOX", "[Gmail]", "[Gmail]/Tous les messages", "[Gmail]/Corbeille", "[Gmail]/Messages envoyés", "[Gmail]/Brouillons", "[Gmail]/Spam", "[Gmail]/Suivis", "[Gmail]/Important"},
		},
		{
			name: "a server silent about special use: the special folders by their names",
			ns:   namespace{delim: '/'},
			folders: []folderInfo{
				{Name: "INBOX"}, {Name: "Archives"}, {Name: "Corbeille"}, {Name: "Éléments envoyés"},
				{Name: "Brouillons"}, {Name: "Courrier indésirable"}, {Name: "Spam"}, {Name: "Factures"},
			},
			notOwn: []string{"INBOX", "Archives", "Corbeille", "Éléments envoyés", "Brouillons", "Courrier indésirable", "Spam"},
		},
		{
			name: "under an INBOX. prefix the special folders are known by the name the user reads, and a shared folder is not the user's",
			ns:   courier,
			folders: []folderInfo{
				{Name: "INBOX"}, {Name: "INBOX.Sent"}, {Name: "INBOX.Trash"}, {Name: "INBOX.Drafts"},
				{Name: "INBOX.Junk"}, {Name: "INBOX.Archives"}, {Name: "INBOX.INBOX"},
				{Name: "INBOX.Factures"}, {Name: "INBOX.Projets.Maison"}, {Name: "shared.Equipe"},
			},
			notOwn: []string{"INBOX", "INBOX.Sent", "INBOX.Trash", "INBOX.Drafts", "INBOX.Junk", "INBOX.Archives", "INBOX.INBOX", "shared.Equipe"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := notOwn(tc.folders, tc.ns)
			want := map[string]bool{}
			for _, n := range tc.notOwn {
				want[n] = true
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("notOwn = %v, want %v", got, want)
			}
		})
	}
}

// A server with no archive folder and no name that looks like one must say so
// as a fact about the account, not as a transient failure.
func TestApplyReportsAMissingFolder(t *testing.T) {
	c := newTestServer(t, map[string][]imapv2.MailboxAttr{"INBOX": nil})
	uid := appendMessage(t, c, "INBOX")

	err := c.Apply(context.Background(), mailbox.InFolder("INBOX", uid), mailbox.Mutation{Action: mailbox.ActionArchive})
	if !errors.Is(err, ErrNoSuchFolder) {
		t.Errorf("archive with no archive folder = %v, want ErrNoSuchFolder", err)
	}
}

// Plenty of servers never send SPECIAL-USE, and on those the folder is still
// right there under its ordinary name. Without the fallback, archiving would
// fail on them for no reason the user could act on.
func TestFoldersResolveByNameWhenTheServerIsSilentAboutSpecialUse(t *testing.T) {
	c := newTestServer(t, map[string][]imapv2.MailboxAttr{
		"INBOX":     nil,
		"Archives":  nil,
		"Corbeille": nil,
	})
	if got := c.folders[mailbox.FolderArchive]; got != "Archives" {
		t.Errorf("archive folder resolved to %q, want %q", got, "Archives")
	}
	if got := c.folders[mailbox.FolderTrash]; got != "Corbeille" {
		t.Errorf("trash folder resolved to %q, want %q", got, "Corbeille")
	}
}

func TestConnectRefusesAnUnencryptedEndpoint(t *testing.T) {
	_, err := Connect(context.Background(), Credentials{
		Endpoint: provider.Endpoint{Host: "imap.example.com", Port: 143, TLS: "none"},
		Username: testUser, Password: testPass,
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported TLS mode") {
		t.Errorf("Connect with TLS mode \"none\" = %v, want it refused", err)
	}
}

func TestConnectReportsABadPasswordAsAnAuthFailure(t *testing.T) {
	user := imapmemserver.NewUser(testUser, testPass)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatalf("create INBOX: %v", err)
	}
	mem := imapmemserver.New()
	mem.AddUser(user)
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		InsecureAuth: true,
		Caps:         imapv2.CapSet{imapv2.CapIMAP4rev1: {}, imapv2.CapIMAP4rev2: {}},
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go srv.Serve(ln)
	defer func() { srv.Close(); ln.Close() }()

	raw, err := imapclient.DialInsecure(ln.Addr().String(), clientOptions("127.0.0.1"))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if _, err := newClient(raw, testUser, "not-the-password"); !errors.Is(err, ErrAuth) {
		t.Errorf("newClient with a bad password = %v, want ErrAuth", err)
	}
}

// pickFolder is where the resolution bugs live, so it is pinned on its own as
// well as through a live server.
func TestPickFolder(t *testing.T) {
	cases := []struct {
		name       string
		want       mailbox.Folder
		candidates []folderInfo
		ns         namespace
		expect     string
		why        string
	}{
		{
			name: "special-use wins over a name that merely looks right",
			want: mailbox.FolderArchive,
			candidates: []folderInfo{
				{Name: "Archive", Attrs: nil},
				{Name: "Vieux courrier", Attrs: []string{"\\Archive"}},
			},
			expect: "Vieux courrier",
			why:    "a server that says which folder is the archive is telling the truth; a name is a guess",
		},
		{
			name:       "Gmail marks All Mail as \\All, and that is where an archived message lives",
			want:       mailbox.FolderArchive,
			candidates: []folderInfo{{Name: "[Gmail]/All Mail", Attrs: []string{"\\All"}}},
			expect:     "[Gmail]/All Mail",
		},
		{
			name:       "name match is case-insensitive",
			want:       mailbox.FolderTrash,
			candidates: []folderInfo{{Name: "TRASH"}},
			expect:     "TRASH",
		},
		{
			name:       "French names resolve",
			want:       mailbox.FolderTrash,
			candidates: []folderInfo{{Name: "Corbeille"}},
			expect:     "Corbeille",
		},
		{
			name:       "the ordered list picks the most specific first",
			want:       mailbox.FolderArchive,
			candidates: []folderInfo{{Name: "Archive"}, {Name: "[Gmail]/All Mail"}},
			expect:     "[Gmail]/All Mail",
		},
		{
			name:       "under an INBOX. prefix the trash is known by the name the user reads",
			want:       mailbox.FolderTrash,
			candidates: []folderInfo{{Name: "INBOX"}, {Name: "INBOX.Factures"}, {Name: "INBOX.Trash"}},
			ns:         namespace{prefix: "INBOX.", delim: '.'},
			expect:     "INBOX.Trash",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := pickFolder(tc.want, tc.candidates, tc.ns)
			if !ok {
				t.Fatalf("pickFolder(%q) found nothing, want %q", tc.want, tc.expect)
			}
			if got != tc.expect {
				msg := ""
				if tc.why != "" {
					msg = ": " + tc.why
				}
				t.Errorf("pickFolder(%q) = %q, want %q%s", tc.want, got, tc.expect, msg)
			}
		})
	}

	if _, ok := pickFolder(mailbox.FolderArchive, []folderInfo{{Name: "INBOX"}, {Name: "Projets"}}, namespace{}); ok {
		t.Error("pickFolder invented an archive folder out of unrelated names")
	}
}

func TestParseUIDRejectsNonsense(t *testing.T) {
	for _, in := range []string{"", "  ", "abc", "0", "18c8c1f2a3b4d5e6"} {
		if _, err := parseUID(in); err == nil {
			t.Errorf("parseUID(%q) was accepted, want an error", in)
		}
	}
	got, err := parseUID(" 42 ")
	if err != nil || got != imapv2.UID(42) {
		t.Errorf("parseUID(\" 42 \") = %v, %v; want 42", got, err)
	}
}

// A reference with no folder is a caller holding a Gmail id, or a UID that lost
// its folder on the way. Either way the number would be resolved against
// whatever mailbox happens to be selected, so it is refused rather than
// defaulted to the inbox: defaulting turns a wiring mistake into an action on
// somebody's unrelated mail, reported as a success.
func TestApplyRefusesAnAccountWideReference(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	appendMessage(t, c, "INBOX")

	err := c.Apply(context.Background(), mailbox.OnAccount("1"), mailbox.Mutation{Action: mailbox.ActionArchive})
	if !errors.Is(err, mailbox.ErrWrongRefKind) {
		t.Errorf("Apply with an account-wide ref = %v, want mailbox.ErrWrongRefKind", err)
	}
	if n := countIn(t, c, "INBOX"); n != 1 {
		t.Errorf("INBOX holds %d message(s); the refused action must not have moved anything", n)
	}
}

// appendWithMessageID drops a message carrying a Message-ID header into folder
// and returns its UID.
func appendWithMessageID(t *testing.T, c *Client, folder, messageID string) string {
	t.Helper()
	raw := "From: sender@example.com\r\n" +
		"To: " + testUser + "\r\n" +
		"Subject: Test\r\n" +
		"Message-ID: " + messageID + "\r\n" +
		"\r\n" +
		"Body.\r\n"
	cmd := c.c.Append(folder, int64(len(raw)), nil)
	if _, err := cmd.Write([]byte(raw)); err != nil {
		t.Fatalf("append write: %v", err)
	}
	if err := cmd.Close(); err != nil {
		t.Fatalf("append close: %v", err)
	}
	data, err := cmd.Wait()
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	c.selected = ""
	return fmtUID(data.UID)
}

// MoveTo says where the message landed: the server's folder and the new UID,
// which is what "Annuler" fetches it back by.
func TestMoveToSaysWhereTheMessageLanded(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	uid := appendWithMessageID(t, c, "INBOX", "<move-1@example.com>")

	landed, err := c.MoveTo(context.Background(), mailbox.InFolder("INBOX", uid), "Archive", "<move-1@example.com>")
	if err != nil {
		t.Fatalf("MoveTo: %v", err)
	}
	if landed.Folder != "Archive" || landed.ID == "" {
		t.Fatalf("landed = %+v, want Archive with a UID", landed)
	}
	back, err := c.MoveTo(context.Background(), landed, "INBOX", "<move-1@example.com>")
	if err != nil {
		t.Fatalf("MoveTo back: %v", err)
	}
	if back.Folder != "INBOX" || back.ID == "" {
		t.Fatalf("back = %+v, want INBOX with a UID", back)
	}
	if n := countIn(t, c, "INBOX"); n != 1 {
		t.Errorf("INBOX holds %d message(s) after the round trip, want 1", n)
	}
}

// A UID that is no longer in its folder is reported as gone, not "moved": a
// MOVE on a missing UID would answer OK and do nothing.
func TestMoveToReportsAMessageThatIsGone(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	appendWithMessageID(t, c, "INBOX", "<here@example.com>")
	_, err := c.MoveTo(context.Background(), mailbox.InFolder("INBOX", "999"), "Archive", "")
	if !errors.Is(err, ErrMessageGone) {
		t.Errorf("MoveTo on a missing uid = %v, want ErrMessageGone", err)
	}
}

// The fallback when a server does not say where a message landed: find it
// again by its Message-ID header.
func TestFindByMessageIDFindsTheMessageAgain(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	appendWithMessageID(t, c, "Archive", "<other@example.com>")
	want := appendWithMessageID(t, c, "Archive", "<wanted@example.com>")
	if got, err := c.FindByMessageID("Archive", "<wanted@example.com>"); err != nil || got != want {
		t.Errorf("FindByMessageID = %q, %v, want %q", got, err, want)
	}
	if got, err := c.FindByMessageID("Archive", "<absent@example.com>"); err != nil || got != "" {
		t.Errorf("FindByMessageID on an absent id = %q, %v, want empty", got, err)
	}
}

func TestHasSaysWhetherTheMessageIsStillThere(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	uid := appendMessage(t, c, "INBOX")
	ref := mailbox.InFolder("INBOX", uid)
	if there, err := c.Has(ref); err != nil || !there {
		t.Fatalf("Has(%s) = %v, %v; want true", ref, there, err)
	}
	if _, err := c.MoveTo(context.Background(), ref, "Archive", ""); err != nil {
		t.Fatal(err)
	}
	if there, err := c.Has(ref); err != nil || there {
		t.Errorf("Has(%s) after the move = %v, %v; want false", ref, there, err)
	}
}

// IDAt reads the Message-ID a number carries, the check that keeps a stored
// number from moving another mail after a server renumbered its folders.
func TestIDAtReadsTheMessageIDOfANumber(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	raw := "From: a@example.com\r\nSubject: Tagged\r\nMessage-ID: <tagged@example.com>\r\n\r\nBody.\r\n"
	cmd := c.c.Append("INBOX", int64(len(raw)), nil)
	if _, err := cmd.Write([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := cmd.Wait()
	if err != nil {
		t.Fatal(err)
	}
	c.selected = ""
	tagged := fmtUID(data.UID)
	other := appendMessage(t, c, "INBOX")

	id, exists, err := c.IDAt("INBOX", tagged)
	if err != nil || !exists || !strings.Contains(id, "tagged@example.com") {
		t.Fatalf("IDAt(tagged) = %q, %v, %v; want its Message-ID", id, exists, err)
	}
	if otherID, _, _ := c.IDAt("INBOX", other); otherID == id {
		t.Errorf("IDAt(other) = %q, the same as the tagged mail's", otherID)
	}
	if _, err := c.MoveTo(context.Background(), mailbox.InFolder("INBOX", tagged), "Archive", ""); err != nil {
		t.Fatal(err)
	}
	if _, exists, err := c.IDAt("INBOX", tagged); err != nil || exists {
		t.Errorf("IDAt after the move: exists=%v, %v; want a number that names nothing", exists, err)
	}
}

// appendWithID puts a message carrying a Message-ID in folder and returns its UID.
func appendWithID(t *testing.T, c *Client, folder, subject, id string) string {
	t.Helper()
	raw := "From: a@example.com\r\nSubject: " + subject + "\r\nMessage-ID: <" + id + ">\r\n\r\nBody.\r\n"
	cmd := c.c.Append(folder, int64(len(raw)), nil)
	if _, err := cmd.Write([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := cmd.Wait()
	if err != nil {
		t.Fatal(err)
	}
	c.selected = ""
	return fmtUID(data.UID)
}

// A recorded number that now names another mail (a server that renumbered
// its folders) must not move that mail: the right one is found by its
// Message-ID, and when it is nowhere, nothing moves.
func TestMoveRecordedNeverMovesAnotherMail(t *testing.T) {
	ctx := context.Background()
	c := newTestServer(t, standardMailboxes())
	stranger := appendWithID(t, c, "Archive", "Stranger", "stranger@example.com")
	ours := appendWithID(t, c, "Archive", "Ours", "ours@example.com")
	oursID, _, err := c.IDAt("Archive", ours)
	if err != nil {
		t.Fatal(err)
	}

	// The recorded number names the stranger: ours is found by Message-ID.
	landed, err := c.MoveRecorded(ctx, "Archive", stranger, "INBOX", oursID)
	if err != nil {
		t.Fatalf("MoveRecorded = %v", err)
	}
	if id, _, _ := c.IDAt("INBOX", landed.ID); id != oursID {
		t.Errorf("the inbox received %q, want our mail %q", id, oursID)
	}
	if _, there, _ := c.IDAt("Archive", stranger); !there {
		t.Error("the stranger left the archive: another mail was moved")
	}

	// Our mail is nowhere in the archive now: nothing moves, and it says so.
	if _, err := c.MoveRecorded(ctx, "Archive", stranger, "INBOX", oursID); !errors.Is(err, ErrMessageGone) {
		t.Errorf("MoveRecorded of a mail no longer there = %v, want ErrMessageGone", err)
	}
	if _, there, _ := c.IDAt("Archive", stranger); !there {
		t.Error("the stranger left the archive on the second call")
	}
}

// Where the user's own folders live: the first personal namespace the server
// gives, its prefix completed with the delimiter, or, without NAMESPACE, no
// prefix and the delimiter of the listing; and the prefixes of the other
// users' and shared namespaces, which are not the user's.
func TestReadNamespace(t *testing.T) {
	type nd = imapv2.NamespaceDescriptor
	cases := []struct {
		name      string
		data      imapv2.NamespaceData
		listDelim rune
		want      namespace
	}{
		{"no NAMESPACE: the listing's delimiter", imapv2.NamespaceData{}, '/', namespace{delim: '/'}},
		{"most servers: no prefix", imapv2.NamespaceData{Personal: []nd{{Delim: '/'}}}, '/', namespace{delim: '/'}},
		{"Courier: INBOX. and dots", imapv2.NamespaceData{Personal: []nd{{Prefix: "INBOX.", Delim: '.'}}}, '.', namespace{prefix: "INBOX.", delim: '.'}},
		{"a prefix given without its delimiter gets it", imapv2.NamespaceData{Personal: []nd{{Prefix: "INBOX", Delim: '.'}}}, '.', namespace{prefix: "INBOX.", delim: '.'}},
		{"no delimiter in the answer: the listing's", imapv2.NamespaceData{Personal: []nd{{Prefix: "INBOX."}}}, '.', namespace{prefix: "INBOX.", delim: '.'}},
		{"the first personal namespace wins", imapv2.NamespaceData{Personal: []nd{{Prefix: "INBOX.", Delim: '.'}, {Prefix: "Mail/", Delim: '/'}}}, '.', namespace{prefix: "INBOX.", delim: '.'}},
		{
			"Dovecot: other users and shared folders beside an empty personal prefix",
			imapv2.NamespaceData{Personal: []nd{{Delim: '/'}}, Other: []nd{{Prefix: "Other Users/", Delim: '/'}}, Shared: []nd{{Prefix: "Public", Delim: '/'}, {Prefix: "shared/", Delim: '/'}}},
			'/',
			namespace{delim: '/', others: []string{"Other Users/", "Public/", "shared/"}},
		},
		{
			"an empty shared prefix, or the personal one again, is left out",
			imapv2.NamespaceData{Personal: []nd{{Prefix: "INBOX.", Delim: '.'}}, Shared: []nd{{Delim: '.'}, {Prefix: "INBOX.", Delim: '.'}}},
			'.',
			namespace{prefix: "INBOX.", delim: '.'},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := readNamespace(tc.data, tc.listDelim); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("readNamespace(%+v, %q) = %+v, want %+v", tc.data, tc.listDelim, got, tc.want)
			}
		})
	}
}

// A folder under another user's or a shared prefix is not the user's, even
// with an empty personal prefix: nothing is filed there, nothing made there.
func TestSharedFoldersAreNotTheUsers(t *testing.T) {
	ns := namespace{delim: '/', others: []string{"Public/", "shared/"}}
	folders := []folderInfo{{Name: "INBOX"}, {Name: "Factures"}, {Name: "Public/Annonces"}, {Name: "shared/alice/Equipe"}, {Name: "public/Bureau"}}
	want := map[string]bool{"INBOX": true, "Public/Annonces": true, "shared/alice/Equipe": true, "public/Bureau": true}
	if got := notOwn(folders, ns); !reflect.DeepEqual(got, want) {
		t.Errorf("notOwn = %v, want %v", got, want)
	}
	c := &Client{listed: folders, ns: ns}
	if got := c.OwnFolders(); !reflect.DeepEqual(got, []string{"Factures"}) {
		t.Errorf("OwnFolders = %v, want [Factures]", got)
	}
	for _, name := range []string{"Public/Annonces", "shared/alice/Equipe", "Public/Nouveau", "shared/bob"} {
		if got, err := c.Destination(mailbox.IMAPOp{Kind: mailbox.IMAPMove, Into: name}); !errors.Is(err, ErrNotOwnFolder) {
			t.Errorf("Destination(%q) = %q, %v; want ErrNotOwnFolder", name, got, err)
		}
	}
}

// A name Mailsorter would make a folder under: refused when the protocol
// reserves or cannot carry it, when a level is empty or climbs, when it is too
// long, and when its first level is not the user's; the server's delimiter
// inside a level becomes "-". No connection: none of it reaches the server.
func TestNewFolderName(t *testing.T) {
	listed := []folderInfo{{Name: "INBOX"}, {Name: "Trash"}, {Name: "Junk"}, {Name: "Sent"}, {Name: "Factures"}}
	plain := &Client{listed: listed, ns: namespace{delim: '/'}}
	dotted := &Client{listed: []folderInfo{{Name: "INBOX"}, {Name: "INBOX.Trash"}}, ns: namespace{prefix: "INBOX.", delim: '.'}}
	cases := []struct {
		name string
		c    *Client
		in   string
		want string
		err  error
	}{
		{"a plain name", plain, "Banque", "Banque", nil},
		{"a nested one", plain, "Projets/Maison", "Projets/Maison", nil},
		{"levels trimmed", plain, " Projets / Maison ", "Projets/Maison", nil},
		{"under the trash", plain, "Trash/Factures", "", ErrNotOwnFolder},
		{"under junk", plain, "Junk/x", "", ErrNotOwnFolder},
		{"under sent mail", plain, "Sent/Old", "", ErrNotOwnFolder},
		{"under the inbox", plain, "INBOX/Factures", "", ErrNotOwnFolder},
		{"under the inbox, any case", plain, "inbox/Factures", "", ErrNotOwnFolder},
		{"a special name", plain, "Drafts", "", ErrNotOwnFolder},
		{"a trailing empty level", plain, "Factures/", "", ErrBadFolderName},
		{"a leading empty level", plain, "/Factures", "", ErrBadFolderName},
		{"an empty level inside", plain, "a//b", "", ErrBadFolderName},
		{"a climbing level", plain, "a/../b", "", ErrBadFolderName},
		{"a dot level", plain, "a/./b", "", ErrBadFolderName},
		{"LIST wildcards", plain, "Fact*%", "", ErrBadFolderName},
		{"a percent alone", plain, "100%", "", ErrBadFolderName},
		{"a control character", plain, "Fact\x00ures", "", ErrBadFolderName},
		{"a tab", plain, "Fact\tures", "", ErrBadFolderName},
		{"3000 characters", plain, strings.Repeat("a", 3000), "", ErrBadFolderName},
		{"a level of 101 characters", plain, strings.Repeat("a", 101), "", ErrBadFolderName},
		{"201 characters in all", plain, strings.Repeat("a", 100) + "/" + strings.Repeat("b", 100), "", ErrBadFolderName},
		{"the dot of a domain on a dotted server", dotted, "Amazon.fr", "Amazon-fr", nil},
		{"a dot on a slashed server stays", plain, "Amazon.fr", "Amazon.fr", nil},
		{"a level on a dotted server", dotted, "Projets/Maison", "Projets/Maison", nil},
		{"the trash under the prefix", dotted, "Trash/Vieux", "", ErrNotOwnFolder},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.c.newFolderName(tc.in)
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Errorf("newFolderName(%q) = %q, %v; want %v", tc.in, got, err, tc.err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Errorf("newFolderName(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
			}
		})
	}
}

// What the analysis stores on a proposal: the user's folder named alike, the
// cleaned new name, or a refusal for a special folder or a bad name.
func TestFilingName(t *testing.T) {
	c := &Client{
		listed: []folderInfo{{Name: "INBOX"}, {Name: "Trash"}, {Name: "Factures"}, {Name: "Pro"}, {Name: "Impôts"}},
		ns:     namespace{prefix: "", delim: '.'},
	}
	cases := []struct {
		in   string
		want string
		err  error
	}{
		{"Facture", "Factures", nil},
		{"factures", "Factures", nil},
		{"Impots", "Impôts", nil},
		{"Promotions", "Promotions", nil},
		{"Amazon.fr", "Amazon-fr", nil},
		{"Trash", "", ErrNotOwnFolder},
		{"corbeille", "", ErrNotOwnFolder},
		{"INBOX", "", ErrNotOwnFolder},
		{"Fact*", "", ErrBadFolderName},
	}
	for _, tc := range cases {
		got, err := c.FilingName(tc.in)
		if tc.err != nil {
			if !errors.Is(err, tc.err) {
				t.Errorf("FilingName(%q) = %q, %v; want %v", tc.in, got, err, tc.err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("FilingName(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}

// One request makes at most maxNewFolders folders: past it, the filing is
// refused and nothing more is made; a folder that exists is still found.
func TestDestinationMakesAtMostTenFolders(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	for i := 1; i <= maxNewFolders; i++ {
		name := fmt.Sprintf("Dossier %d", i)
		if got, err := c.Destination(mailbox.IMAPOp{Kind: mailbox.IMAPMove, Into: name}); err != nil || got != name {
			t.Fatalf("Destination(%q) = %q, %v; want it made", name, got, err)
		}
	}
	if got, err := c.Destination(mailbox.IMAPOp{Kind: mailbox.IMAPMove, Into: "Un de trop"}); !errors.Is(err, ErrTooManyNewFolders) {
		t.Errorf("Destination(one more) = %q, %v; want ErrTooManyNewFolders", got, err)
	}
	if listed, err := c.c.List("", "Un de trop", nil).Collect(); err != nil || len(listed) != 0 {
		t.Errorf("the folder past the limit was made: %v (%v)", listed, err)
	}
	if got, err := c.Destination(mailbox.IMAPOp{Kind: mailbox.IMAPMove, Into: "dossier 3"}); err != nil || got != "Dossier 3" {
		t.Errorf("Destination(dossier 3) past the limit = %q, %v; want the folder made earlier", got, err)
	}
}

// The name the user reads and the server's name, both ways: the prefix comes
// off and goes back on, the server's delimiter reads "/" and is written back.
func TestNamespaceNames(t *testing.T) {
	courier := namespace{prefix: "INBOX.", delim: '.'}
	plain := namespace{delim: '/'}
	flat := namespace{}
	shown := []struct {
		ns     namespace
		server string
		want   string
		inside bool
	}{
		{courier, "INBOX.Factures", "Factures", true},
		{courier, "INBOX.Projets.Maison", "Projets/Maison", true},
		{courier, "inbox.Factures", "Factures", true},
		{courier, "INBOX", "INBOX", false},
		{courier, "INBOX.", "INBOX.", false},
		{courier, "shared.Equipe", "shared.Equipe", false},
		{plain, "Projets/Maison", "Projets/Maison", true},
		{plain, "INBOX", "INBOX", true},
		{flat, "Factures", "Factures", true},
	}
	for _, tc := range shown {
		if got, inside := tc.ns.display(tc.server); got != tc.want || inside != tc.inside {
			t.Errorf("%+v.display(%q) = %q, %v; want %q, %v", tc.ns, tc.server, got, inside, tc.want, tc.inside)
		}
	}
	made := []struct {
		ns      namespace
		display string
		want    string
	}{
		{courier, "Factures", "INBOX.Factures"},
		{courier, "Projets/Maison", "INBOX.Projets.Maison"},
		{plain, "Projets/Maison", "Projets/Maison"},
		{flat, "Factures", "Factures"},
	}
	for _, tc := range made {
		if got := tc.ns.serverName(tc.display); got != tc.want {
			t.Errorf("%+v.serverName(%q) = %q, want %q", tc.ns, tc.display, got, tc.want)
		}
		if back, inside := tc.ns.display(tc.ns.serverName(tc.display)); !inside || back != tc.display {
			t.Errorf("%+v: %q made as %q reads back %q, %v", tc.ns, tc.display, tc.ns.serverName(tc.display), back, inside)
		}
	}
}

// The in-memory server answers NAMESPACE with no prefix and "/": the client
// reads it at connect, and the folders keep the names the user knows.
func TestConnectReadsTheNamespace(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	if !reflect.DeepEqual(c.ns, namespace{delim: '/'}) {
		t.Errorf("namespace read at connect = %+v, want no prefix and \"/\"", c.ns)
	}
}

// On a server that keeps the user's folders under a prefix, the user reads
// them without it, and a folder Mailsorter makes goes under it. The in-memory
// server announces no prefix, so the one read at connect is replaced by the
// one such a server would give, with its "/" delimiter.
func TestFilingUnderAPersonalPrefix(t *testing.T) {
	c := newTestServer(t, map[string][]imapv2.MailboxAttr{
		"INBOX":          nil,
		"INBOX/Sent":     nil,
		"INBOX/Trash":    nil,
		"INBOX/Factures": nil,
		"Partage/Equipe": nil,
	})
	c.ns = namespace{prefix: "INBOX/", delim: '/'}

	if got := c.OwnFolders(); !reflect.DeepEqual(got, []string{"Factures"}) {
		t.Errorf("OwnFolders = %v, want [Factures]: no prefix, no special folder, no shared one", got)
	}
	if got, err := c.Destination(mailbox.IMAPOp{Kind: mailbox.IMAPMove, Into: "factures"}); err != nil || got != "INBOX/Factures" {
		t.Errorf("Destination(factures) = %q, %v; want the user's INBOX/Factures", got, err)
	}
	if got, err := c.Destination(mailbox.IMAPOp{Kind: mailbox.IMAPMove, Into: "Sent"}); !errors.Is(err, ErrNotOwnFolder) {
		t.Errorf("Destination(Sent) = %q, %v; want ErrNotOwnFolder for INBOX/Sent", got, err)
	}

	uid := appendMessage(t, c, "INBOX")
	if err := c.Apply(context.Background(), mailbox.InFolder("INBOX", uid), mailbox.Mutation{Action: mailbox.ActionLabel, LabelID: "Banque"}); err != nil {
		t.Fatalf("label Banque: %v", err)
	}
	if n := countIn(t, c, "INBOX/Banque"); n != 1 {
		t.Errorf("INBOX/Banque holds %d message(s), want the one filed under the prefix", n)
	}
	if got := c.OwnFolders(); !reflect.DeepEqual(got, []string{"Banque", "Factures"}) {
		t.Errorf("OwnFolders after filing = %v, want [Banque Factures]", got)
	}
}
