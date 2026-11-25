package imap

import (
	"context"
	"fmt"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/protect"
	"github.com/nohe-sohbi/mailsorter/backend/internal/protect/protecttest"
)

// appendWithFrom drops into folder a message whose From header is from, as
// its sender wrote it (bytes and all), under this subject.
func appendWithFrom(t *testing.T, c *Client, folder, from, subject string) {
	t.Helper()
	raw := "From: " + from + "\r\n" +
		"To: " + testUser + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"Message-ID: <" + subject + "@example.com>\r\n" +
		"\r\n" +
		"Body.\r\n"
	cmd := c.c.Append(folder, int64(len(raw)), nil)
	if _, err := cmd.Write([]byte(raw)); err != nil {
		t.Fatalf("append write: %v", err)
	}
	if err := cmd.Close(); err != nil {
		t.Fatalf("append close: %v", err)
	}
	if _, err := cmd.Wait(); err != nil {
		t.Fatalf("append %q: %v", from, err)
	}
	c.selected = ""
}

// The same From reads as the same sender on both transports. Each header of
// the table goes into a real IMAP server and comes back through the listing
// (the server's ENVELOPE, formatAddress, the From the copy stores), and what
// protect.Address reads out of it is compared with what it reads out of the
// header as the Gmail API hands it over: equal, whatever the server made of
// the header in its ENVELOPE (senderFrom). Then, for every sender read,
// "Protéger" from the mail
// (protect.EntryFromMail) protects its mail on each transport.
func TestTheSameFromNamesTheSameSenderOnBothTransports(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	for i, h := range protecttest.Headers {
		appendWithFrom(t, c, "INBOX", h.From, fmt.Sprintf("h%02d", i))
	}
	listed, err := c.List(context.Background(), "INBOX", len(protecttest.Headers)+10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	stored := map[string]string{}
	for _, e := range listed {
		stored[e.Subject] = e.From
	}

	for i, h := range protecttest.Headers {
		t.Run(h.Name, func(t *testing.T) {
			from, ok := stored[fmt.Sprintf("h%02d", i)]
			if !ok {
				t.Fatalf("the listing does not hold the mail from %q", h.From)
			}
			gmail := protect.Address(h.From)
			imap := protect.Address(from)
			t.Logf("From %q | Gmail API %q | IMAP stores %q | IMAP %q", h.From, gmail, from, imap)
			if imap != gmail {
				t.Errorf("From %q: the Gmail API reads %q, IMAP stores %q and reads %q", h.From, gmail, from, imap)
			}
			for transport, f := range map[string]string{"Gmail": h.From, "IMAP": from} {
				if protect.Address(f) == "" {
					continue
				}
				entry := protect.EntryFromMail(f)
				if !protect.Match(f, []string{entry}) {
					t.Errorf("%s: « Protéger » on the mail from %q stores %q, which does not protect it", transport, f, entry)
				}
			}
		})
	}
}
