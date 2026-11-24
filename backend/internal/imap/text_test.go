package imap

import (
	"context"
	"strings"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
)

// uidsIn lists the UIDs of folder, newest first, the way the listing names
// them.
func uidsIn(t *testing.T, c *Client, folder string) []string {
	t.Helper()
	emails, err := c.List(context.Background(), folder, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	uids := make([]string, 0, len(emails))
	for _, e := range emails {
		uids = append(uids, e.MessageID)
	}
	return uids
}

// What a rule on the text reads: the plain part when there is one, the text of
// the HTML part when there is none, and the preview line the list would show.
func TestTextsReadsTheTextOfEachMessage(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	appendRaw(t, c, "INBOX", alternativeMessage)
	appendRaw(t, c, "INBOX", "From: news@acme.com\r\nTo: "+testUser+"\r\nSubject: Promo\r\n"+
		"MIME-Version: 1.0\r\nContent-Type: text/html; charset=utf-8\r\n\r\n"+
		"<html><body><h1>Soldes</h1><p>Jusqu'a -50% sur le remboursement</p></body></html>\r\n")
	uids := uidsIn(t, c, "INBOX") // newest first: the HTML one, then the invoice

	got, err := c.Texts(context.Background(), "INBOX", uids)
	if err != nil {
		t.Fatalf("Texts: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Texts read %d messages, want 2: %v", len(got), got)
	}
	invoice := got[uids[1]]
	if strings.TrimSpace(invoice.Body) != "Bonjour, voici votre facture." {
		t.Errorf("Body of the alternative message = %q, want its text part", invoice.Body)
	}
	if invoice.Snippet == "" {
		t.Error("Snippet of the alternative message is empty, want the preview line")
	}
	promo := got[uids[0]]
	if !strings.Contains(promo.Body, "remboursement") || strings.Contains(promo.Body, "<p>") {
		t.Errorf("Body of the HTML-only message = %q, want the text of its HTML, without the markup", promo.Body)
	}
}

// The reason it peeks: applying the rules must not mark the inbox read.
// Read back off the server, not off what Texts returned.
func TestTextsDoesNotMarkTheMessagesRead(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	appendRaw(t, c, "INBOX", alternativeMessage)
	appendRaw(t, c, "INBOX", message("a@b.com", "Deux"))
	uids := uidsIn(t, c, "INBOX")

	if _, err := c.Texts(context.Background(), "INBOX", uids); err != nil {
		t.Fatalf("Texts: %v", err)
	}
	for _, uid := range uids {
		if flags := flagsOf(t, c, "INBOX", uid); has(flags, mailbox.FlagSeen) {
			t.Errorf("after Texts, message %s carries %s on the server: the read must peek", uid, mailbox.FlagSeen)
		}
	}
}

// Only the beginning of a message is read: its text, not the file attached
// after it. And a text longer than what is read is kept up to where the read
// stopped, rather than dropped for ending mid-way.
func TestTextsReadsTheBeginningOfALargeMessage(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	file := strings.Repeat("QUJDRA==", textBytes/8*2) // twice what is read, base64
	appendRaw(t, c, "INBOX", "From: a@b.com\r\nTo: "+testUser+"\r\nSubject: Avec un fichier\r\n"+
		"MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=\"sep\"\r\n\r\n"+
		"--sep\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nVotre remboursement est en route.\r\n"+
		"--sep\r\nContent-Type: application/pdf; name=\"a.pdf\"\r\nContent-Disposition: attachment; filename=\"a.pdf\"\r\n"+
		"Content-Transfer-Encoding: base64\r\n\r\n"+file+"\r\n--sep--\r\n")
	long := "Le remboursement arrive. " + strings.Repeat("Encore du texte. ", textBytes/16)
	appendRaw(t, c, "INBOX", "From: a@b.com\r\nTo: "+testUser+"\r\nSubject: Un long texte\r\n"+
		"MIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=\"sep\"\r\n\r\n"+
		"--sep\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n"+long+"\r\n"+
		"--sep\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>"+long+"</p>\r\n--sep--\r\n")
	uids := uidsIn(t, c, "INBOX") // the long text, then the one with a file

	got, err := c.Texts(context.Background(), "INBOX", uids)
	if err != nil {
		t.Fatalf("Texts: %v", err)
	}
	if body := got[uids[1]].Body; strings.TrimSpace(body) != "Votre remboursement est en route." {
		t.Errorf("Body of the message with a file = %q, want its text alone", body)
	}
	body := got[uids[0]].Body
	if !strings.HasPrefix(body, "Le remboursement arrive.") {
		t.Errorf("Body of the long message starts %q, want its text, kept though the read stopped inside it", firstRunes(body, 40))
	}
	if len(body) > textBytes {
		t.Errorf("Body of the long message is %d bytes, want at most the %d read", len(body), textBytes)
	}
}

// A number the folder no longer holds is not in the answer, and what is not
// a number at all is refused, as everywhere on this transport.
func TestTextsLeavesOutWhatTheFolderDoesNotHold(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	appendRaw(t, c, "INBOX", alternativeMessage)
	uids := uidsIn(t, c, "INBOX")

	got, err := c.Texts(context.Background(), "INBOX", []string{uids[0], "99999"})
	if err != nil {
		t.Fatalf("Texts: %v", err)
	}
	if _, ok := got["99999"]; ok || len(got) != 1 {
		t.Errorf("Texts answered %v, want only message %s", got, uids[0])
	}
	if _, err := c.Texts(context.Background(), "INBOX", []string{"18c8c1f2a3b4d5e6"}); err == nil {
		t.Error("Texts accepted a Gmail message id as a UID")
	}
	if got, err := c.Texts(context.Background(), "INBOX", nil); err != nil || len(got) != 0 {
		t.Errorf("Texts(no uid) = %v, %v, want nothing and no error", got, err)
	}
}

// A message larger than what is read, with a file before its text: the read
// stops inside the file, no text part found. It comes back NotReached, with
// no text, rather than as a message whose text is empty: "does not contain"
// would match it on a text nobody read. One as large whose text comes first is
// read, and a small one with no text at all is a message without text.
func TestTextsSaysWhenTheTextIsNotReached(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	file := strings.Repeat("QUJDRA==", textBytes/8*2) // twice what is read, base64
	appendRaw(t, c, "INBOX", "From: a@b.com\r\nTo: "+testUser+"\r\nSubject: Le fichier d'abord\r\n"+
		"MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=\"sep\"\r\n\r\n"+
		"--sep\r\nContent-Type: application/pdf; name=\"a.pdf\"\r\nContent-Disposition: attachment; filename=\"a.pdf\"\r\n"+
		"Content-Transfer-Encoding: base64\r\n\r\n"+file+"\r\n"+
		"--sep\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nVotre remboursement est en route.\r\n--sep--\r\n")
	appendRaw(t, c, "INBOX", "From: a@b.com\r\nTo: "+testUser+"\r\nSubject: Un fichier seul\r\n"+
		"MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=\"sep\"\r\n\r\n"+
		"--sep\r\nContent-Type: application/pdf; name=\"b.pdf\"\r\nContent-Disposition: attachment; filename=\"b.pdf\"\r\n"+
		"Content-Transfer-Encoding: base64\r\n\r\nQUJDRA==\r\n--sep--\r\n")
	uids := uidsIn(t, c, "INBOX") // the small file alone, then the large one

	got, err := c.Texts(context.Background(), "INBOX", uids)
	if err != nil {
		t.Fatalf("Texts: %v", err)
	}
	if large := got[uids[1]]; !large.NotReached || large.Body != "" {
		t.Errorf("Texts of a large message whose text is after a file = %+v, want NotReached and no text", large)
	}
	if small := got[uids[0]]; small.NotReached || small.Body != "" {
		t.Errorf("Texts of a small message without text = %+v, want read, with no text", small)
	}
}

// A context already done reads nothing: the rules stop at the next batch
// when their request ran out of time.
func TestTextsStopsOnADoneContext(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	appendRaw(t, c, "INBOX", alternativeMessage)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := c.Texts(ctx, "INBOX", uidsIn(t, c, "INBOX")); err == nil || len(got) != 0 {
		t.Errorf("Texts on a done context = %v, %v, want nothing and an error", got, err)
	}
}

func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}
