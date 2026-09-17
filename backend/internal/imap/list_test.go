package imap

import (
	"context"
	"testing"

	imapv2 "github.com/emersion/go-imap/v2"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
)

// appendRaw puts a specific message in a folder, so a test can say what the
// headers are instead of asserting against a fixed sample.
func appendRaw(t *testing.T, c *Client, folder, raw string) {
	t.Helper()
	cmd := c.c.Append(folder, int64(len(raw)), nil)
	if _, err := cmd.Write([]byte(raw)); err != nil {
		t.Fatalf("append write: %v", err)
	}
	if err := cmd.Close(); err != nil {
		t.Fatalf("append close: %v", err)
	}
	if _, err := cmd.Wait(); err != nil {
		t.Fatalf("append: %v", err)
	}
	c.selected = ""
}

func message(from, subject string, extra ...string) string {
	raw := "From: " + from + "\r\n" +
		"To: " + testUser + "\r\n" +
		"Subject: " + subject + "\r\n"
	for _, h := range extra {
		raw += h + "\r\n"
	}
	return raw + "\r\nBody text.\r\n"
}

// The listing is what the inbox screen renders, so the mapping onto the app's
// Email is what the user actually sees.
func TestListMapsTheEnvelopeOntoAnEmail(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	appendRaw(t, c, "INBOX", message("Acme News <news@acme.com>", "Votre facture"))

	emails, err := c.List(context.Background(), "INBOX", 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(emails) != 1 {
		t.Fatalf("List returned %d message(s), want 1", len(emails))
	}
	e := emails[0]
	if e.Subject != "Votre facture" {
		t.Errorf("Subject = %q, want %q", e.Subject, "Votre facture")
	}
	// The header form is what the rest of the app parses back with
	// extractSenderAddress and extractSenderName, so it has to be that form.
	if e.From != `"Acme News" <news@acme.com>` {
		t.Errorf("From = %q, want the header form with the display name", e.From)
	}
	if len(e.To) != 1 || e.To[0] != testUser {
		t.Errorf("To = %v, want [%s]", e.To, testUser)
	}
	if e.MessageID == "" {
		t.Error("MessageID is empty; the UID is how every later action finds this message")
	}
	if e.ReceivedDate.IsZero() {
		t.Error("ReceivedDate is zero; the inbox is ordered by it")
	}
	if e.IsRead {
		t.Error("a freshly delivered message reads as read")
	}
	// Left empty on purpose rather than filled with something that looks right.
	if e.Body != "" {
		t.Error("the listing downloaded a body; it is fetched when a reader opens the message")
	}
	if len(e.LabelIDs) != 0 {
		t.Errorf("LabelIDs = %v, want none: a message on IMAP is in one folder, not a set of labels", e.LabelIDs)
	}
}

// The one that would be discovered in production. A plain BODY[...] fetch sets
// \Seen, so a listing without Peek marks the whole inbox read just by rendering
// it, and the user watches their unread count fall to zero for no reason they
// did.
func TestListDoesNotMarkMessagesRead(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	appendRaw(t, c, "INBOX", message("a@b.com", "Un"))
	appendRaw(t, c, "INBOX", message("c@d.com", "Deux"))

	emails, err := c.List(context.Background(), "INBOX", 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, e := range emails {
		if e.IsRead {
			t.Errorf("message %q came back read straight out of the listing", e.Subject)
		}
	}

	// And the server agrees, which is the half that matters: the field above is
	// what we decided, this is what the mailbox now says.
	for _, e := range emails {
		if flags := flagsOf(t, c, "INBOX", e.MessageID); has(flags, mailbox.FlagSeen) {
			t.Errorf("after listing, %q carries %s on the server; the fetch must peek",
				e.Subject, mailbox.FlagSeen)
		}
	}
}

// IMAP numbers messages in arrival order, so the newest are the highest
// sequence numbers. Getting the direction wrong shows the user their oldest
// mail first, which on a long-lived mailbox looks like the app is broken.
func TestListReturnsTheNewestFirst(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	for _, subject := range []string{"Premier", "Deuxieme", "Troisieme"} {
		appendRaw(t, c, "INBOX", message("a@b.com", subject))
	}

	emails, err := c.List(context.Background(), "INBOX", 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"Troisieme", "Deuxieme", "Premier"}
	if len(emails) != len(want) {
		t.Fatalf("List returned %d message(s), want %d", len(emails), len(want))
	}
	for i, subject := range want {
		if emails[i].Subject != subject {
			t.Errorf("position %d = %q, want %q", i, emails[i].Subject, subject)
		}
	}
}

// A page is the newest N, not the oldest N. Taking the range from the wrong end
// would page through a mailbox backwards in time and never reach today.
func TestListTakesThePageFromTheNewEnd(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	for _, subject := range []string{"Un", "Deux", "Trois", "Quatre"} {
		appendRaw(t, c, "INBOX", message("a@b.com", subject))
	}

	emails, err := c.List(context.Background(), "INBOX", 2)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(emails) != 2 {
		t.Fatalf("List(limit 2) returned %d message(s), want 2", len(emails))
	}
	if emails[0].Subject != "Quatre" || emails[1].Subject != "Trois" {
		t.Errorf("page = [%q %q], want the two newest [Quatre Trois]", emails[0].Subject, emails[1].Subject)
	}
}

// The unsubscribe affordance is what the subscriptions screen is built on, and
// it has to survive the trip over a different transport.
func TestListReadsTheUnsubscribeHeaders(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	appendRaw(t, c, "INBOX", message("news@acme.com", "Newsletter",
		"List-Unsubscribe: <https://acme.com/u?id=abc>, <mailto:u@acme.com>",
		"List-Unsubscribe-Post: List-Unsubscribe=One-Click"))

	emails, err := c.List(context.Background(), "INBOX", 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(emails) != 1 {
		t.Fatalf("List returned %d message(s), want 1", len(emails))
	}
	e := emails[0]
	if e.UnsubURL != "https://acme.com/u?id=abc" {
		t.Errorf("UnsubURL = %q, want the https endpoint", e.UnsubURL)
	}
	if e.UnsubMailto != "mailto:u@acme.com" {
		t.Errorf("UnsubMailto = %q, want the mailto", e.UnsubMailto)
	}
	if !e.UnsubOneClick {
		t.Error("UnsubOneClick is false although the sender advertises RFC 8058")
	}
}

// A message with no such header must come back with the fields empty rather
// than with whatever the previous message had, which is the failure a shared
// buffer would produce.
func TestListLeavesTheUnsubscribeFieldsEmptyWhenThereIsNone(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	appendRaw(t, c, "INBOX", message("news@acme.com", "Avec",
		"List-Unsubscribe: <https://acme.com/u>"))
	appendRaw(t, c, "INBOX", message("friend@example.com", "Sans"))

	emails, err := c.List(context.Background(), "INBOX", 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(emails) != 2 {
		t.Fatalf("List returned %d message(s), want 2", len(emails))
	}
	// Newest first, so the one without the header comes back first.
	if emails[0].Subject != "Sans" {
		t.Fatalf("first message is %q, want Sans", emails[0].Subject)
	}
	if emails[0].UnsubURL != "" || emails[0].UnsubMailto != "" || emails[0].UnsubOneClick {
		t.Errorf("a message with no List-Unsubscribe came back with %+v", emails[0])
	}
	if emails[1].UnsubURL != "https://acme.com/u" {
		t.Errorf("the message that does have one lost it: %q", emails[1].UnsubURL)
	}
}

// An empty folder is a normal state. Returning an error would make a new
// account look broken.
func TestListOnAnEmptyFolder(t *testing.T) {
	c := newTestServer(t, standardMailboxes())

	emails, err := c.List(context.Background(), "INBOX", 10)
	if err != nil {
		t.Fatalf("List on an empty folder = %v, want no error", err)
	}
	if len(emails) != 0 {
		t.Errorf("List returned %d message(s) from an empty folder", len(emails))
	}
}

func TestListRefusesAFolderThatDoesNotExist(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	if _, err := c.List(context.Background(), "Nowhere", 10); err == nil {
		t.Error("List on a missing folder returned no error")
	}
}

// A caller asking for everything would hold the connection, and the user, for
// as long as the folder takes.
func TestListCapsThePageSize(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	appendRaw(t, c, "INBOX", message("a@b.com", "Un"))

	for _, limit := range []int{0, -5, maxListLimit + 1000} {
		emails, err := c.List(context.Background(), "INBOX", limit)
		if err != nil {
			t.Fatalf("List(limit %d): %v", limit, err)
		}
		if len(emails) != 1 {
			t.Errorf("List(limit %d) returned %d message(s), want the folder's 1", limit, len(emails))
		}
	}
}

// The flags come off the server, so the read state the screen shows is the
// mailbox's, not a guess.
func TestListReportsReadState(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	appendRaw(t, c, "INBOX", message("a@b.com", "Un"))

	emails, err := c.List(context.Background(), "INBOX", 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	uid := emails[0].MessageID
	if err := c.Apply(context.Background(), mailbox.InFolder("INBOX", uid), mailbox.Mutation{Action: mailbox.ActionMarkRead}); err != nil {
		t.Fatalf("markRead: %v", err)
	}

	emails, err = c.List(context.Background(), "INBOX", 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !emails[0].IsRead {
		t.Error("a message marked read still lists as unread")
	}
}

func TestFormatAddress(t *testing.T) {
	cases := []struct {
		in   []imapv2.Address
		want string
	}{
		{[]imapv2.Address{{Name: "Acme News", Mailbox: "news", Host: "acme.com"}}, `"Acme News" <news@acme.com>`},
		{[]imapv2.Address{{Mailbox: "bare", Host: "acme.com"}}, "bare@acme.com"},
		{[]imapv2.Address{{Name: "No Address"}}, "No Address"},
		{nil, ""},
	}
	for _, tc := range cases {
		if got := formatAddress(tc.in); got != tc.want {
			t.Errorf("formatAddress(%+v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
