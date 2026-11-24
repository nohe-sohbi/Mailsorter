package imap

import (
	"context"
	"testing"
)

// A sender name or subject encoded in windows-1252 or ISO-8859-15, both
// common in French mail, is stored readable, like a UTF-8 one.
func TestListDecodesLegacyCharsets(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	raw := "From: =?windows-1252?Q?Soci=E9t=E9_G=E9n=E9rale?= <info@banque.example>\r\n" +
		"To: " + testUser + "\r\n" +
		"Subject: =?iso-8859-15?Q?Votre_relev=E9_=E0_100_=A4?=\r\n" +
		"Message-ID: <legacy@example.com>\r\n" +
		"\r\n" +
		"Body.\r\n"
	cmd := c.c.Append("INBOX", int64(len(raw)), nil)
	if _, err := cmd.Write([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	c.selected = ""

	emails, err := c.List(context.Background(), "INBOX", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(emails) != 1 {
		t.Fatalf("listed %d mails, want 1", len(emails))
	}
	if got, want := emails[0].From, `"Société Générale" <info@banque.example>`; got != want {
		t.Errorf("From = %q, want %q", got, want)
	}
	if got, want := emails[0].Subject, "Votre relevé à 100 €"; got != want {
		t.Errorf("Subject = %q, want %q", got, want)
	}
}
