package imap

import (
	"context"
	"errors"
	"net"
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
			return mem.NewSession(), nil, nil
		},
		InsecureAuth: true,
		Caps: imapv2.CapSet{
			imapv2.CapIMAP4rev1: {},
			imapv2.CapIMAP4rev2: {},
		},
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

	raw, err := imapclient.DialInsecure(ln.Addr().String(), nil)
	if err != nil {
		t.Fatalf("dial test server: %v", err)
	}
	client, err := newClient(raw, testUser, testPass)
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	return client
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

// Labelling has no IMAP equivalent, and the error has to say so in a way the
// caller can tell apart from a failure, because the honest fix is to stop
// offering it rather than to retry.
func TestApplyReportsLabellingAsUnsupported(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	uid := appendMessage(t, c, "INBOX")

	err := c.Apply(context.Background(), mailbox.InFolder("INBOX", uid), mailbox.Mutation{Action: mailbox.ActionLabel, LabelID: "Factures"})
	if !errors.Is(err, mailbox.ErrNoIMAPEquivalent) {
		t.Errorf("label over IMAP = %v, want mailbox.ErrNoIMAPEquivalent", err)
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

	raw, err := imapclient.DialInsecure(ln.Addr().String(), nil)
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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := pickFolder(tc.want, tc.candidates)
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

	if _, ok := pickFolder(mailbox.FolderArchive, []folderInfo{{Name: "INBOX"}, {Name: "Projets"}}); ok {
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
