package imap

import (
	"context"
	"fmt"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/gmail"
	"github.com/nohe-sohbi/mailsorter/backend/internal/protect"
	"github.com/nohe-sohbi/mailsorter/backend/internal/protect/protecttest"
	gmailv1 "google.golang.org/api/gmail/v1"
)

// appendWithHeaders drops into folder a message with these header lines
// (CRLF-ended, bytes and all), then a From header for each of froms, under
// this subject.
func appendWithHeaders(t *testing.T, c *Client, folder, before string, froms []string, subject string) {
	t.Helper()
	raw := before
	for _, from := range froms {
		raw += "From: " + from + "\r\n"
	}
	raw += "To: " + testUser + "\r\n" +
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
		t.Fatalf("append %q: %v", froms, err)
	}
	c.selected = ""
}

// gmailFrom is the From the Gmail path stores for a message whose From
// headers are froms, as the Gmail API hands them over.
func gmailFrom(froms []string) string {
	headers := make([]*gmailv1.MessagePartHeader, 0, len(froms))
	for _, from := range froms {
		headers = append(headers, &gmailv1.MessagePartHeader{Name: "From", Value: from})
	}
	from, _, _, _ := gmail.ParseEmailHeaders(&gmailv1.Message{Payload: &gmailv1.MessagePart{Headers: headers}})
	return from
}

// The same mail names the same sender on both transports. Each row of the
// table goes into a real IMAP server, with the headers it writes before From
// and a second From when it has one, and comes back through the listing (the
// From header as written, read by readHeaders and senderFrom, never the
// ENVELOPE); the same From headers go through the Gmail path
// (gmail.ParseEmailHeaders). protect.Address must read the table's address
// out of what each stores. Then, for every sender read, "Protéger" from the
// mail (protect.EntryFromMail) protects its mail on each transport.
func TestTheSameFromNamesTheSameSenderOnBothTransports(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	for i, h := range protecttest.Headers {
		appendWithHeaders(t, c, "INBOX", h.Before, fromsOf(h), fmt.Sprintf("h%02d", i))
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
			imapFrom, ok := stored[fmt.Sprintf("h%02d", i)]
			if !ok {
				t.Fatalf("the listing does not hold the mail from %q", h.From)
			}
			viaGmail := gmailFrom(fromsOf(h))
			gmailReads, imapReads := protect.Address(viaGmail), protect.Address(imapFrom)
			t.Logf("From %q | Gmail stores %q, reads %q | IMAP stores %q, reads %q", fromsOf(h), viaGmail, gmailReads, imapFrom, imapReads)
			if gmailReads != h.Want || imapReads != h.Want {
				t.Errorf("From %q: the Gmail path reads %q (stores %q), IMAP reads %q (stores %q), want %q on both", fromsOf(h), gmailReads, viaGmail, imapReads, imapFrom, h.Want)
			}
			for transport, f := range map[string]string{"Gmail": viaGmail, "IMAP": imapFrom} {
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

// fromsOf is the From headers of a row: one, or two.
func fromsOf(h protecttest.Header) []string {
	if h.Second != "" {
		return []string{h.From, h.Second}
	}
	return []string{h.From}
}
