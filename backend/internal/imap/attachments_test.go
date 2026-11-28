package imap

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	imapv2 "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/nohe-sohbi/mailsorter/backend/internal/attachment"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
)

// everyByte is a binary file: every byte value, twice, NUL and the line
// breaks among them. A file that went through a text conversion, or that was
// not decoded from its base64, does not come back equal.
func everyByte() []byte {
	var b bytes.Buffer
	for round := 0; round < 2; round++ {
		for c := 0; c < 256; c++ {
			b.WriteByte(byte(c))
		}
	}
	return b.Bytes()
}

// base64Body is data in base64, 76 characters a line, as RFC 2045 asks.
func base64Body(data []byte) string {
	encoded := base64.StdEncoding.EncodeToString(data)
	var b strings.Builder
	for len(encoded) > 76 {
		b.WriteString(encoded[:76] + "\r\n")
		encoded = encoded[76:]
	}
	b.WriteString(encoded)
	return b.String()
}

// mixed is a multipart/mixed message whose parts are given whole, header and
// body, in order.
func mixed(parts ...string) string {
	return "From: Boutique <factures@boutique.com>\r\n" +
		"To: " + testUser + "\r\n" +
		"Subject: Votre facture\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"sep\"\r\n" +
		"\r\n" +
		"--sep\r\n" + strings.Join(parts, "\r\n--sep\r\n") + "\r\n--sep--\r\n"
}

const textPart = "Content-Type: text/plain; charset=utf-8\r\n" +
	"\r\n" +
	"Bonjour, voici votre facture."

// pdfPart is a binary file in base64, its accented name an RFC 2047 word, the
// way Gmail and Outlook write it.
func pdfPart() string {
	word := mime.BEncoding.Encode("utf-8", "Facture décembre.pdf")
	return "Content-Type: application/pdf; name=\"" + word + "\"\r\n" +
		"Content-Disposition: attachment; filename=\"" + word + "\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"\r\n" +
		base64Body(everyByte())
}

// The statement, in quoted-printable and windows-1252, its accented name an
// RFC 2231 parameter, the way Thunderbird and Apple Mail write it.
const csvPart = "Content-Type: text/csv; charset=windows-1252\r\n" +
	"Content-Disposition: attachment; filename*=utf-8''Relev%C3%A9%20d%27achats.csv\r\n" +
	"Content-Transfer-Encoding: quoted-printable\r\n" +
	"\r\n" +
	"Date;Libell=E9\r\n" +
	"02/12;Caf=E9"

// csvData is csvPart's file, byte for byte: still windows-1252, line breaks
// as sent.
var csvData = []byte("Date;Libell\xe9\r\n02/12;Caf\xe9")

// csvUTF8 is a statement in UTF-8, for the sizes the reader lists: the
// in-memory test server converts a text part's charset when it measures it
// (BINARY.SIZE), which RFC 3516 does not ask and a real server does not do.
const csvUTF8 = "Content-Type: text/csv; charset=utf-8\r\n" +
	"Content-Disposition: attachment; filename*=utf-8''Relev%C3%A9%20d%27achats.csv\r\n" +
	"Content-Transfer-Encoding: quoted-printable\r\n" +
	"\r\n" +
	"Date;Libell=C3=A9\r\n" +
	"02/12;Caf=C3=A9"

// testLimit is a download cap no file of these tests reaches.
const testLimit = 30 << 20

// noBinary is a server without BINARY, like Gmail's: it does not measure a
// part decoded, so the reader estimates.
var noBinary = imapv2.CapSet{imapv2.CapIMAP4rev1: {}}

// stored puts raw in the inbox of the server c talks to and returns its UID.
func stored(t *testing.T, c *Client, raw string) string {
	t.Helper()
	appendRaw(t, c, "INBOX", raw)
	emails, err := c.List(context.Background(), "INBOX", 100)
	if err != nil || len(emails) == 0 {
		t.Fatalf("List after append = %d mails, %v", len(emails), err)
	}
	return emails[0].MessageID // newest first
}

// listed is what the reader lists of the message uid: part number to file.
func listed(t *testing.T, c *Client, uid string) map[string]Attachment {
	t.Helper()
	msg, err := c.Fetch(context.Background(), "INBOX", uid)
	if err != nil {
		t.Fatalf("Fetch(%s): %v", uid, err)
	}
	out := map[string]Attachment{}
	for _, a := range msg.Attachments {
		out[a.Part] = a
	}
	return out
}

// The reader lists what was attached, under the part number IMAP gives it,
// with the name decoded and the size of the file itself, as the server
// measures it.
func TestFetchListsTheFilesOfAMessage(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	uid := stored(t, c, mixed(textPart, pdfPart(), csvUTF8))

	msg, err := c.Fetch(context.Background(), "INBOX", uid)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	want := []Attachment{
		{Part: "2", Filename: "Facture décembre.pdf", MimeType: "application/pdf", Size: int64(len(everyByte()))},
		{Part: "3", Filename: "Relevé d'achats.csv", MimeType: "text/csv", Size: int64(len("Date;Libellé\r\n02/12;Café"))},
	}
	if !reflect.DeepEqual(msg.Attachments, want) {
		t.Errorf("Fetch(...).Attachments = %+v, want %+v", msg.Attachments, want)
	}
	if strings.TrimSpace(msg.Body) != "Bonjour, voici votre facture." {
		t.Errorf("Fetch(...).Body = %q, want the text part alone", msg.Body)
	}
}

// What makes a part a file, and what name it is listed under, as a part's
// MIME header gives them to internal/attachment's rule. The images the HTML
// shows are TestTheListAndTheDownloadFollowOneRule's: a part alone has no
// HTML.
func TestPartOf(t *testing.T) {
	cases := []struct {
		name     string
		header   string
		wantOK   bool
		wantName string
		wantType string
	}{
		{
			name:   "the text of the mail has no name",
			header: "Content-Type: text/plain; charset=utf-8\r\n",
		},
		{
			name:     "an unnamed attachment is listed under a name of its own",
			header:   "Content-Type: application/pdf\r\nContent-Disposition: attachment\r\n",
			wantOK:   true,
			wantName: attachment.Unnamed,
			wantType: "application/pdf",
		},
		{
			name:     "an unnamed forwarded mail",
			header:   "Content-Type: message/rfc822\r\n",
			wantOK:   true,
			wantName: attachment.UnnamedMail,
			wantType: "message/rfc822",
		},
		{
			name:     "an RFC 2231 name in ISO-8859-1",
			header:   "Content-Type: application/pdf\r\nContent-Disposition: attachment; filename*=iso-8859-1''Factur%E9.pdf\r\n",
			wantOK:   true,
			wantName: "Facturé.pdf",
			wantType: "application/pdf",
		},
		{
			name:     "RFC 2231 in windows-1252, cut in two",
			header:   "Content-Type: application/pdf\r\nContent-Disposition: attachment;\r\n filename*0*=windows-1252''%80%20Re;\r\n filename*1*=%E7u.pdf\r\n",
			wantOK:   true,
			wantName: "€ Reçu.pdf",
			wantType: "application/pdf",
		},
		{
			name:     "RFC 2231 in ISO-8859-15, with a language, in the name of Content-Type",
			header:   "Content-Type: application/pdf; name*=ISO-8859-15'fr'%A4uros.pdf\r\n",
			wantOK:   true,
			wantName: "€uros.pdf",
			wantType: "application/pdf",
		},
		{
			name:     "RFC 2231 in ISO-8859-1, quoted",
			header:   "Content-Type: application/pdf\r\nContent-Disposition: attachment; filename*=\"iso-8859-1''Factur%E9.pdf\"\r\n",
			wantOK:   true,
			wantName: "Facturé.pdf",
			wantType: "application/pdf",
		},
		{
			name:     "RFC 2231 in ISO-8859-1 wins over the ASCII name beside it",
			header:   "Content-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"Facture.pdf\"; filename*=iso-8859-1''Factur%E9.pdf\r\n",
			wantOK:   true,
			wantName: "Facturé.pdf",
			wantType: "application/pdf",
		},
		{
			name:     "RFC 2231 in a charset nobody knows gives no name",
			header:   "Content-Type: application/pdf\r\nContent-Disposition: attachment; filename*=x-inconnu''Factur%E9.pdf\r\n",
			wantOK:   true,
			wantName: attachment.Unnamed,
			wantType: "application/pdf",
		},
		{
			name:     "a name turned around by a bidirectional control is cleaned",
			header:   "Content-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=\"" + mime.BEncoding.Encode("utf-8", "facture\u202Efdp.exe") + "\"\r\n",
			wantOK:   true,
			wantName: "facturefdp.exe",
			wantType: "application/octet-stream",
		},
		{
			name:     "a name with a directory is cleaned",
			header:   "Content-Type: application/pdf; name=\"../../facture.pdf\"\r\n",
			wantOK:   true,
			wantName: "facture.pdf",
			wantType: "application/pdf",
		},
		{
			name:     "a plain ASCII filename",
			header:   "Content-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"facture.pdf\"\r\n",
			wantOK:   true,
			wantName: "facture.pdf",
			wantType: "application/pdf",
		},
		{
			name:     "the name of Content-Type, which older programs write alone",
			header:   "Content-Type: application/pdf; name=\"facture.pdf\"\r\n",
			wantOK:   true,
			wantName: "facture.pdf",
			wantType: "application/pdf",
		},
		{
			name:     "the filename of Content-Disposition wins over the name of Content-Type",
			header:   "Content-Type: application/pdf; name=\"ancien.pdf\"\r\nContent-Disposition: attachment; filename=\"facture.pdf\"\r\n",
			wantOK:   true,
			wantName: "facture.pdf",
			wantType: "application/pdf",
		},
		{
			name:     "an RFC 2047 word in base64",
			header:   "Content-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"=?utf-8?b?RmFjdHVyZSBkw6ljZW1icmUucGRm?=\"\r\n",
			wantOK:   true,
			wantName: "Facture décembre.pdf",
			wantType: "application/pdf",
		},
		{
			name:     "an RFC 2047 word in Q and ISO-8859-1",
			header:   "Content-Type: application/pdf; name=\"=?iso-8859-1?q?R=E9sum=E9.pdf?=\"\r\n",
			wantOK:   true,
			wantName: "Résumé.pdf",
			wantType: "application/pdf",
		},
		{
			name:     "an RFC 2231 parameter",
			header:   "Content-Type: text/csv\r\nContent-Disposition: attachment; filename*=utf-8''Relev%C3%A9%20d%27achats%20%C3%A9t%C3%A9.csv\r\n",
			wantOK:   true,
			wantName: "Relevé d'achats été.csv",
			wantType: "text/csv",
		},
		{
			name:     "an RFC 2231 parameter cut in two",
			header:   "Content-Type: text/csv\r\nContent-Disposition: attachment;\r\n filename*0*=utf-8''Relev%C3%A9%20;\r\n filename*1*=d%27achats.csv\r\n",
			wantOK:   true,
			wantName: "Relevé d'achats.csv",
			wantType: "text/csv",
		},
		{
			name:     "a type is listed without its parameters",
			header:   "Content-Type: Text/CSV; charset=utf-8; name=\"achats.csv\"\r\n",
			wantOK:   true,
			wantName: "achats.csv",
			wantType: "text/csv",
		},
		{
			name:     "a file attached from Gmail's web interface carries a Content-ID",
			header:   "Content-Type: application/pdf; name=\"facture.pdf\"\r\nContent-Disposition: attachment; filename=\"facture.pdf\"\r\nContent-ID: <f_abc123>\r\nX-Attachment-Id: f_abc123\r\n",
			wantOK:   true,
			wantName: "facture.pdf",
			wantType: "application/pdf",
		},
		{
			name:     "Apple Mail sends a PDF inline, with nothing in the HTML pointing at it",
			header:   "Content-Type: application/pdf; name=\"facture.pdf\"\r\nContent-Disposition: inline; filename=\"facture.pdf\"\r\n",
			wantOK:   true,
			wantName: "facture.pdf",
			wantType: "application/pdf",
		},
		{
			name:     "a type that does not parse is saved as bytes",
			header:   "Content-Type: application/pdf; ;;\r\nContent-Disposition: attachment; filename=\"facture.pdf\"\r\n",
			wantOK:   true,
			wantName: "facture.pdf",
			wantType: "application/octet-stream",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			header, err := parseHeader([]byte(tc.header + "\r\n"))
			if err != nil {
				t.Fatalf("parseHeader(%q): %v", tc.header, err)
			}
			part := partOf(header)
			var got Attachment
			got.MimeType = part.MediaType
			var ok bool
			got.Filename, ok = attachment.Listed(part, nil)
			if ok != tc.wantOK {
				t.Fatalf("Listed(partOf(%q)) ok = %v, want %v", tc.header, ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if got.Filename != tc.wantName {
				t.Errorf("Listed(partOf(%q)) name = %q, want %q", tc.header, got.Filename, tc.wantName)
			}
			if got.MimeType != tc.wantType {
				t.Errorf("partOf(%q) type = %q, want %q", tc.header, got.MimeType, tc.wantType)
			}
		})
	}
}

// Part numbers follow IMAP's: a single-part message is part 1, the children
// of a multipart are numbered from 1, and a nested multipart numbers its own
// under its number.
func TestFetchNumbersPartsTheWayIMAPDoes(t *testing.T) {
	file := func(name string) string {
		return "Content-Type: application/pdf\r\n" +
			"Content-Disposition: attachment; filename=\"" + name + "\"\r\n" +
			"\r\n" +
			"%PDF-1.4 " + name
	}
	alternative := "Content-Type: multipart/alternative; boundary=\"alt\"\r\n" +
		"\r\n" +
		"--alt\r\n" + textPart + "\r\n" +
		"--alt\r\n" + "Content-Type: text/html\r\n\r\n<p>Bonjour</p>\r\n" +
		"--alt--"
	related := "Content-Type: multipart/related; boundary=\"rel\"\r\n" +
		"\r\n" +
		"--rel\r\n" + "Content-Type: text/html\r\n\r\n<img src=\"cid:logo@acme\">\r\n" +
		"--rel\r\n" + "Content-Type: image/png\r\nContent-ID: <logo@acme>\r\n\r\nPNG\r\n" +
		"--rel\r\n" + file("dans-related.pdf") + "\r\n" +
		"--rel--"

	cases := []struct {
		name string
		raw  string
		want map[string]string // part number -> filename
	}{
		{
			name: "a message that is a file and nothing else",
			raw: "From: a@b.com\r\nSubject: S\r\nMIME-Version: 1.0\r\n" +
				"Content-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"seul.pdf\"\r\n\r\n%PDF",
			want: map[string]string{"1": "seul.pdf"},
		},
		{
			name: "a text then two files",
			raw:  mixed(textPart, file("a.pdf"), file("b.pdf")),
			want: map[string]string{"2": "a.pdf", "3": "b.pdf"},
		},
		{
			name: "text and html, then a file",
			raw:  mixed(alternative, file("a.pdf")),
			want: map[string]string{"2": "a.pdf"},
		},
		{
			name: "a file inside a related, beside an unnamed image the html shows",
			raw:  mixed(related, file("b.pdf")),
			want: map[string]string{"1.3": "dans-related.pdf", "2": "b.pdf"},
		},
		{
			name: "no file at all",
			raw:  mixed(alternative),
			want: map[string]string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestServer(t, standardMailboxes())
			got := map[string]string{}
			for part, a := range listed(t, c, stored(t, c, tc.raw)) {
				got[part] = a.Filename
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Fetch(...).Attachments = %v, want %v", got, tc.want)
			}
		})
	}
}

// The download hands back the file as it was sent: decoded from its transfer
// encoding, and from nothing else. A text file in windows-1252 stays in
// windows-1252, and a message that is a file and nothing else gives it.
func TestAttachmentGivesTheFileByteForByte(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	invoice := stored(t, c, mixed(textPart, pdfPart(), csvPart))
	alone := stored(t, c, "From: a@b.com\r\nSubject: S\r\nMIME-Version: 1.0\r\n"+
		"Content-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"seul.pdf\"\r\n"+
		"Content-Transfer-Encoding: base64\r\n\r\n"+base64Body(everyByte()))

	cases := []struct {
		uid, part string
		wantName  string
		wantData  []byte
	}{
		{invoice, "2", "Facture décembre.pdf", everyByte()},
		{invoice, "3", "Relevé d'achats.csv", csvData},
		{alone, "1", "seul.pdf", everyByte()},
	}
	for _, tc := range cases {
		att, data, err := c.Attachment(context.Background(), "INBOX", tc.uid, tc.part, testLimit)
		if err != nil {
			t.Fatalf("Attachment(%s, %q): %v", tc.uid, tc.part, err)
		}
		if !bytes.Equal(data, tc.wantData) {
			t.Errorf("Attachment(%s, %q) = %q, want %q", tc.uid, tc.part, data, tc.wantData)
		}
		if att.Filename != tc.wantName || att.Size != int64(len(tc.wantData)) {
			t.Errorf("Attachment(%s, %q) = %q of %d bytes, want %q of %d", tc.uid, tc.part, att.Filename, att.Size, tc.wantName, len(tc.wantData))
		}
	}
}

// A part number is easy to guess. Only one the reader lists as a file is
// served: never the text of the mail, never a section IMAP knows by name.
func TestAttachmentRefusesWhatIsNotAFile(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	invoice := stored(t, c, mixed(textPart, pdfPart(), csvPart))
	plain := stored(t, c, "From: a@b.com\r\nSubject: S\r\nContent-Type: text/plain\r\n\r\nBonjour.\r\n")

	cases := []struct {
		name string
		uid  string
		part string
	}{
		{"the text of the mail", invoice, "1"},
		{"past the last part", invoice, "4"},
		{"the whole body, as IMAP names it", invoice, "TEXT"},
		{"the header, as IMAP names it", invoice, "HEADER"},
		{"the header of a file part", invoice, "2.MIME"},
		{"a number written otherwise", invoice, "02"},
		{"inside a part that is not a multipart", invoice, "2.1"},
		{"nothing", invoice, ""},
		{"the text of a mail without files", plain, "1"},
		{"a file of another mail", plain, "2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, data, err := c.Attachment(context.Background(), "INBOX", tc.uid, tc.part, testLimit)
			if !errors.Is(err, ErrNoSuchAttachment) {
				t.Errorf("Attachment(%q) error = %v, want ErrNoSuchAttachment", tc.part, err)
			}
			if data != nil {
				t.Errorf("Attachment(%q) handed out %d bytes, want none", tc.part, len(data))
			}
		})
	}
}

// A file in an encoding nobody knows is left out of the list, named in why,
// and not served. One whose base64 is broken cannot be told without reading
// it: it is listed, and its download is an error, not "no such file".
func TestAnUndecodableFile(t *testing.T) {
	broken := "Content-Type: application/pdf\r\n" +
		"Content-Disposition: attachment; filename=\"casse.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"\r\n" +
		"JVBERi0x!!!pas du base64"
	unknown := "Content-Type: application/octet-stream\r\n" +
		"Content-Disposition: attachment; filename=\"vieux.bin\"\r\n" +
		"Content-Transfer-Encoding: x-uuencode\r\n" +
		"\r\n" +
		"begin 644 vieux.bin"
	c := newTestServer(t, standardMailboxes())
	uid := stored(t, c, mixed(textPart, broken, unknown, pdfPart()))

	described, err := c.fetchOne("INBOX", uid, &imapv2.FetchOptions{UID: true, BodyStructure: &imapv2.FetchItemBodyStructure{Extended: true}})
	if err != nil {
		t.Fatalf("fetchOne: %v", err)
	}
	inside, err := c.readParts("INBOX", uid, described.BodyStructure, true)
	if err != nil {
		t.Fatalf("readParts: %v", err)
	}
	if inside.skipped == nil || !strings.Contains(inside.skipped.Error(), "vieux.bin") {
		t.Errorf("readParts(...).skipped = %v, want it to name vieux.bin", inside.skipped)
	}
	var names []string
	for _, f := range inside.files {
		names = append(names, f.Filename)
	}
	if !reflect.DeepEqual(names, []string{"casse.pdf", "Facture décembre.pdf"}) {
		t.Errorf("readParts(...).files = %q, want casse.pdf and the PDF", names)
	}

	if _, data, err := c.Attachment(context.Background(), "INBOX", uid, "3", testLimit); !errors.Is(err, ErrNoSuchAttachment) || data != nil {
		t.Errorf("Attachment(vieux.bin) = %d bytes, %v, want ErrNoSuchAttachment", len(data), err)
	}
	if _, data, err := c.Attachment(context.Background(), "INBOX", uid, "2", testLimit); err == nil || errors.Is(err, ErrNoSuchAttachment) || data != nil {
		t.Errorf("Attachment(casse.pdf) = %d bytes, %v, want a decoding error", len(data), err)
	}
}

// Over a real server: the reader's fetch lists the files, and saving one
// gives the file without marking the mail read.
func TestClientAttachmentOverIMAP(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	uid := stored(t, c, mixed(textPart, pdfPart(), csvPart))

	msg, err := c.Fetch(context.Background(), "INBOX", uid)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(msg.Attachments) != 2 || msg.Attachments[0].Part != "2" || msg.Attachments[1].Part != "3" {
		t.Fatalf("Fetch(...).Attachments = %+v, want the PDF as part 2 and the CSV as part 3", msg.Attachments)
	}

	att, data, err := c.Attachment(context.Background(), "INBOX", uid, "2", testLimit)
	if err != nil {
		t.Fatalf("Attachment(2): %v", err)
	}
	if !bytes.Equal(data, everyByte()) || att.Filename != "Facture décembre.pdf" {
		t.Errorf("Attachment(2) = %q (%d bytes), want the PDF as sent", att.Filename, len(data))
	}

	// Read back off the server, not off what the fetch returned.
	if flags := flagsOf(t, c, "INBOX", uid); has(flags, mailbox.FlagSeen) {
		t.Errorf("after Fetch and Attachment, the message carries %s on the server: both must peek", mailbox.FlagSeen)
	}
}

// A newsletter that is also an invoice, with every kind of part the rule
// tells apart.
func newsletterMail() string {
	related := "Content-Type: multipart/related; boundary=\"rel\"\r\n" +
		"\r\n" +
		"--rel\r\n" + "Content-Type: text/html; charset=utf-8\r\n\r\n" +
		"<p>Bonjour</p><img src=\"cid:logo@acme\"><div style=\"background:url(cid:fond@acme)\"><img src=\"cid:carte@acme\">\r\n" +
		"--rel\r\n" + "Content-Type: image/png; name=\"logo.png\"\r\nContent-Disposition: inline; filename=\"logo.png\"\r\nContent-ID: <logo@acme>\r\n\r\nPNG-logo\r\n" +
		"--rel\r\n" + "Content-Type: image/jpeg\r\nContent-ID: <fond@acme>\r\nContent-Disposition: attachment\r\n\r\nJPEG-fond\r\n" +
		"--rel--"
	gmailWeb := "Content-Type: application/pdf; name=\"facture.pdf\"\r\n" +
		"Content-Disposition: attachment; filename=\"facture.pdf\"\r\n" +
		"Content-ID: <f_abc123>\r\nX-Attachment-Id: f_abc123\r\n\r\n%PDF-gmail"
	appleInline := "Content-Type: application/pdf; name=\"devis.pdf\"\r\n" +
		"Content-Disposition: inline; filename=\"devis.pdf\"\r\n\r\n%PDF-apple"
	photo := "Content-Type: image/jpeg; name=\"photo.jpg\"\r\n" +
		"Content-Disposition: inline; filename=\"photo.jpg\"\r\nContent-ID: <photo@iphone>\r\n\r\nJPEG-photo"
	unnamed := "Content-Type: application/octet-stream\r\nContent-Disposition: attachment\r\n\r\nBYTES"
	forwarded := "Content-Type: message/rfc822\r\n\r\n" +
		"From: a@b.com\r\nSubject: Transfert\r\n\r\nLe mail d'origine.\r\n"
	// An unnamed picture the HTML does not point at, and an unnamed SVG it
	// does: neither is a file, neither is drawn.
	pixel := "Content-Type: image/gif\r\nContent-ID: <pixel@acme>\r\n\r\nGIF89a"
	svg := "Content-Type: image/svg+xml\r\nContent-ID: <carte@acme>\r\n\r\n<svg xmlns=\"http://www.w3.org/2000/svg\"><script>alert(1)</script></svg>"
	return mixed(related, gmailWeb, appleInline, photo, unnamed, forwarded, pixel, svg)
}

// The list and the download follow internal/attachment's rule, and agree:
// what the reader lists is what the download serves, under the same name,
// and the pictures the HTML draws are served to the screen too; what it
// neither lists nor draws, the download does not serve.
func TestTheListAndTheDownloadFollowOneRule(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	uid := stored(t, c, newsletterMail())
	forwarded := "From: a@b.com\r\nSubject: Transfert\r\n\r\nLe mail d'origine.\r\n"
	want := []Attachment{
		// Drawn in the text, and listed so it can be saved: it has a name.
		{Part: "1.2", Filename: "logo.png", MimeType: "image/png", Size: int64(len("PNG-logo"))},
		// Only a CSS background points at it, which the screen does not
		// draw: declared an attachment, it is a file.
		{Part: "1.3", Filename: attachment.Unnamed, MimeType: "image/jpeg", Size: int64(len("JPEG-fond"))},
		{Part: "2", Filename: "facture.pdf", MimeType: "application/pdf", Size: int64(len("%PDF-gmail"))},
		{Part: "3", Filename: "devis.pdf", MimeType: "application/pdf", Size: int64(len("%PDF-apple"))},
		{Part: "4", Filename: "photo.jpg", MimeType: "image/jpeg", Size: int64(len("JPEG-photo"))},
		{Part: "5", Filename: attachment.Unnamed, MimeType: "application/octet-stream", Size: int64(len("BYTES"))},
		{Part: "6", Filename: attachment.UnnamedMail, MimeType: "message/rfc822", Size: int64(len(forwarded))},
	}

	msg, err := c.Fetch(context.Background(), "INBOX", uid)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !reflect.DeepEqual(msg.Attachments, want) {
		t.Errorf("Fetch(...).Attachments = %+v, want %+v", msg.Attachments, want)
	}
	// The logo; not the background, which an image does not draw, nor the
	// SVG, which an image does.
	wantImages := map[string]string{"logo@acme": "1.2"}
	if !reflect.DeepEqual(msg.InlineImages, wantImages) {
		t.Errorf("Fetch(...).InlineImages = %v, want %v", msg.InlineImages, wantImages)
	}

	for _, w := range want {
		att, data, err := c.Attachment(context.Background(), "INBOX", uid, w.Part, testLimit)
		if err != nil {
			t.Errorf("Attachment(%q) error = %v, want the file the reader lists", w.Part, err)
			continue
		}
		if att != w || int64(len(data)) != w.Size {
			t.Errorf("Attachment(%q) = %+v (%d bytes), want %+v", w.Part, att, len(data), w)
		}
	}
	// The HTML itself, a picture the HTML does not point at, an SVG it does.
	for _, part := range []string{"1.1", "7", "8"} {
		if _, data, err := c.Attachment(context.Background(), "INBOX", uid, part, testLimit); !errors.Is(err, ErrNoSuchAttachment) || data != nil {
			t.Errorf("Attachment(%q) = %d bytes, %v, want ErrNoSuchAttachment", part, len(data), err)
		}
	}
}

// The HTML that shows an image may come after it in the message: the image
// is still drawn rather than listed, and served for the screen to draw.
func TestAnImageTheHTMLShowsAfterIt(t *testing.T) {
	related := "Content-Type: multipart/related; boundary=\"rel\"\r\n" +
		"\r\n" +
		"--rel\r\n" + "Content-Type: image/png\r\nContent-ID: <logo@acme>\r\n\r\nPNG\r\n" +
		"--rel\r\n" + "Content-Type: text/html\r\n\r\n<img src=\"cid:logo@acme\">\r\n" +
		"--rel--"
	c := newTestServer(t, standardMailboxes())
	uid := stored(t, c, mixed(related, pdfPart()))

	msg, err := c.Fetch(context.Background(), "INBOX", uid)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(msg.Attachments) != 1 || msg.Attachments[0].Part != "2" {
		t.Errorf("Fetch(...).Attachments = %+v, want the PDF alone", msg.Attachments)
	}
	if !reflect.DeepEqual(msg.InlineImages, map[string]string{"logo@acme": "1.1"}) {
		t.Errorf("Fetch(...).InlineImages = %v, want the logo under 1.1", msg.InlineImages)
	}
	if _, data, err := c.Attachment(context.Background(), "INBOX", uid, "1.1", testLimit); err != nil || string(data) != "PNG" {
		t.Errorf("Attachment(1.1) = %q, %v, want the logo", data, err)
	}
}

// The download holds no more of a file than the cap: one at the cap is
// read whole, one byte over is refused and nothing of it handed out.
func TestReadFileStopsAtTheLimit(t *testing.T) {
	data := bytes.Repeat([]byte("Libellé;Montant\r\n"), 100)
	var qp bytes.Buffer
	w := quotedprintable.NewWriter(&qp)
	_, _ = w.Write(data)
	_ = w.Close()
	header, _ := parseHeader([]byte("Content-Type: text/csv; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n"))

	got, err := readFile(header, bytes.NewReader(qp.Bytes()), int64(len(data)))
	if err != nil || !bytes.Equal(got, data) {
		t.Errorf("readFile(limit = its size) = %d bytes, %v, want the file", len(got), err)
	}
	if got, err := readFile(header, bytes.NewReader(qp.Bytes()), int64(len(data))-1); !errors.Is(err, ErrAttachmentTooLarge) || got != nil {
		t.Errorf("readFile(limit = its size - 1) = %d bytes, %v, want ErrAttachmentTooLarge and nothing", len(got), err)
	}
	broken, _ := parseHeader([]byte("Content-Transfer-Encoding: base64\r\n\r\n"))
	if got, err := readFile(broken, strings.NewReader("JVBERi0x!!!"), testLimit); err == nil || errors.Is(err, ErrAttachmentTooLarge) || got != nil {
		t.Errorf("readFile(broken base64) = %d bytes, %v, want a decoding error", len(got), err)
	}
}

// What a part's stored size says of its size once decoded: never more than
// the real one, whatever line length the base64 was written in.
func TestMinDecodedSize(t *testing.T) {
	file := bytes.Repeat(everyByte(), 40) // 20 KiB
	encoded := base64.StdEncoding.EncodeToString(file)
	lines := func(width int) string {
		var b strings.Builder
		for s := encoded; s != ""; {
			n := min(width, len(s))
			b.WriteString(s[:n] + "\r\n")
			s = s[n:]
		}
		return b.String()
	}
	for _, body := range []string{lines(76), lines(64), encoded} {
		got := minDecodedSize(int64(len(body)), "base64")
		if got > int64(len(file)) {
			t.Errorf("minDecodedSize(%d, base64) = %d, more than the file's %d bytes", len(body), got, len(file))
		}
		if got < int64(len(file))*95/100 {
			t.Errorf("minDecodedSize(%d, base64) = %d, too far under the file's %d bytes to refuse anything", len(body), got, len(file))
		}
	}
	cases := []struct {
		encoding string
		want     int64
	}{
		{"", 1000}, {"7bit", 1000}, {"8BIT", 1000}, {"binary", 1000},
		{"quoted-printable", 0}, {"x-uuencode", 0},
	}
	for _, tc := range cases {
		if got := minDecodedSize(1000, tc.encoding); got != tc.want {
			t.Errorf("minDecodedSize(1000, %q) = %d, want %d", tc.encoding, got, tc.want)
		}
	}
}

// Without the server's measure, the size the reader shows is the stored
// size read as base64 in lines of 76: a few bytes off at most, whatever the
// file.
func TestEstimatedSize(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 56, 57, 58, 1000, 20480, 1 << 20} {
		body := base64Body(bytes.Repeat([]byte{0xab}, n))
		got := estimatedSize(int64(len(body)), "base64")
		if d := got - int64(n); d < -3 || d > 3 {
			t.Errorf("estimatedSize(base64 of %d bytes) = %d", n, got)
		}
	}
	for _, encoding := range []string{"", "7bit", "8bit", "binary", "quoted-printable"} {
		if got := estimatedSize(1000, encoding); got != 1000 {
			t.Errorf("estimatedSize(1000, %q) = %d, want 1000", encoding, got)
		}
	}
}

// A file is refused before it is read on the server's measure, exactly, or,
// without one, only when its stored size is sure to be over.
func TestCannotFit(t *testing.T) {
	base64Leaf := func(stored uint32, size int64, exact bool) leaf {
		return leaf{desc: &imapv2.BodyStructureSinglePart{Encoding: "base64", Size: stored}, size: size, exact: exact}
	}
	cases := []struct {
		name  string
		leaf  leaf
		limit int64
		want  bool
	}{
		{"measured, at the cap", base64Leaf(1400, 1000, true), 1000, false},
		{"measured, one byte over", base64Leaf(1400, 1001, true), 1000, true},
		{"estimated, surely over", base64Leaf(4<<20, 3<<20, false), 2 << 20, true},
		{"estimated, maybe under", base64Leaf(4<<20, 3<<20, false), 3 << 20, false},
		{"quoted-printable, not measured, is left to the read", leaf{desc: &imapv2.BodyStructureSinglePart{Encoding: "quoted-printable", Size: 50 << 20}, size: 50 << 20}, 1 << 20, false},
	}
	for _, tc := range cases {
		if got := cannotFit(tc.leaf, tc.limit); got != tc.want {
			t.Errorf("cannotFit(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// countingConn counts what the client reads off its socket.
type countingConn struct {
	net.Conn
	read *atomic.Int64
}

func (c countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.read.Add(int64(n))
	return n, err
}

// A file over the cap is refused on the server's word, before a byte of it
// comes over the connection: measured (BINARY), or told by its stored size
// on a server without BINARY.
func TestClientAttachmentRefusesALargeFileBeforeReadingIt(t *testing.T) {
	big := bytes.Repeat(everyByte(), 2048) // 1 MiB
	file := "Content-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"gros.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + base64Body(big)
	for _, server := range []struct {
		name string
		caps imapv2.CapSet
	}{
		{"with BINARY", nil},
		{"without BINARY", noBinary},
	} {
		t.Run(server.name, func(t *testing.T) {
			c, read := countingServer(t, server.caps)
			uid := stored(t, c, mixed(textPart, file))

			before := read.Load()
			_, data, err := c.Attachment(context.Background(), "INBOX", uid, "2", 512<<10)
			if !errors.Is(err, ErrAttachmentTooLarge) || data != nil {
				t.Fatalf("Attachment(1 MiB file, cap 512 KiB) = %d bytes, %v, want ErrAttachmentTooLarge", len(data), err)
			}
			if got := read.Load() - before; got > 16<<10 {
				t.Errorf("Attachment read %d bytes off the connection to refuse it, want the server's description only", got)
			}

			// The same file under a cap it fits in comes whole.
			_, data, err = c.Attachment(context.Background(), "INBOX", uid, "2", int64(len(big)))
			if err != nil || !bytes.Equal(data, big) {
				t.Errorf("Attachment(1 MiB file, cap 1 MiB) = %d bytes, %v, want the file", len(data), err)
			}
		})
	}
}

// countingServer is a test server, with caps when they are set, and a client
// whose socket counts what it reads.
func countingServer(t *testing.T, caps imapv2.CapSet) (*Client, *atomic.Int64) {
	t.Helper()
	var read atomic.Int64
	addr := serveTestServer(t, standardMailboxes(), nil)
	if caps != nil {
		addr = serveTestServerWith(t, standardMailboxes(), nil, caps)
	}
	c := dialTestServerThrough(t, addr, func(conn net.Conn) net.Conn {
		return countingConn{Conn: conn, read: &read}
	})
	return c, &read
}

// Opening a mail with a large file reads its text and the list of its files,
// never the file: on a server with BINARY, which gives its size exactly, and
// on one without, where it is estimated.
func TestFetchDoesNotReadTheFiles(t *testing.T) {
	big := bytes.Repeat(everyByte(), 4096) // 2 MiB
	file := "Content-Type: video/mp4\r\nContent-Disposition: attachment; filename=\"film.mp4\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + base64Body(big)
	for _, server := range []struct {
		name  string
		caps  imapv2.CapSet
		exact bool
	}{
		{"with BINARY", nil, true},
		{"without BINARY", noBinary, false},
	} {
		t.Run(server.name, func(t *testing.T) {
			c, read := countingServer(t, server.caps)
			uid := stored(t, c, mixed(textPart, file))

			before := read.Load()
			msg, err := c.Fetch(context.Background(), "INBOX", uid)
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if got := read.Load() - before; got > 32<<10 {
				t.Errorf("Fetch read %d bytes off the connection for a mail with a 2 MiB file, want its text and headers only", got)
			}
			if strings.TrimSpace(msg.Body) != "Bonjour, voici votre facture." {
				t.Errorf("Fetch(...).Body = %q, want the text", msg.Body)
			}
			if len(msg.Attachments) != 1 || msg.Attachments[0].Filename != "film.mp4" {
				t.Fatalf("Fetch(...).Attachments = %+v, want film.mp4", msg.Attachments)
			}
			size := msg.Attachments[0].Size
			if server.exact && size != int64(len(big)) {
				t.Errorf("film.mp4 is listed at %d bytes, want %d, as the server measures it", size, len(big))
			}
			if d := size - int64(len(big)); d < -3 || d > 3 {
				t.Errorf("film.mp4 is listed at %d bytes, want about %d", size, len(big))
			}
		})
	}
}

// The text the reader shows is bounded, in what is read off the connection
// as in what is kept.
func TestFetchBoundsTheText(t *testing.T) {
	c, read := countingServer(t, nil)
	html := "<p>" + strings.Repeat("Soldes ", (8<<20)/7) + "</p>"
	uid := stored(t, c, "From: news@acme.com\r\nTo: "+testUser+"\r\nSubject: Promo\r\nMIME-Version: 1.0\r\n"+
		"Content-Type: text/html; charset=utf-8\r\n\r\n"+html)

	before := read.Load()
	msg, err := c.Fetch(context.Background(), "INBOX", uid)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(msg.HTML) != maxBodyBytes {
		t.Errorf("Fetch(...).HTML is %d bytes, want it cut at %d", len(msg.HTML), maxBodyBytes)
	}
	if got := read.Load() - before; got > maxTextFetch+64<<10 {
		t.Errorf("Fetch read %d bytes off the connection for an 8 MiB HTML, want at most about %d", got, maxTextFetch)
	}
}

// A mail another client moved since the list showed it is gone, for the
// reader and for the download, which is not a failure of the server.
func TestClientReadsOfAMailThatIsGone(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	uid := stored(t, c, mixed(textPart, pdfPart()))
	if err := c.Apply(context.Background(), mailbox.InFolder("INBOX", uid), mailbox.Mutation{Action: mailbox.ActionArchive}); err != nil {
		t.Fatalf("archive: %v", err)
	}

	if _, err := c.Fetch(context.Background(), "INBOX", uid); !errors.Is(err, ErrMessageGone) {
		t.Errorf("Fetch(a moved mail) error = %v, want ErrMessageGone", err)
	}
	if _, _, err := c.Attachment(context.Background(), "INBOX", uid, "2", testLimit); !errors.Is(err, ErrMessageGone) {
		t.Errorf("Attachment(a moved mail) error = %v, want ErrMessageGone", err)
	}
}

// heldFetch is a server session whose FETCH of a message's parts waits for
// release: a server too slow to hand a mail over. A FETCH of the server's
// description, or of named header fields (the listing's), is answered.
type heldFetch struct {
	imapserver.SessionIMAP4rev2
	release <-chan struct{}
}

func (s heldFetch) Fetch(w *imapserver.FetchWriter, numSet imapv2.NumSet, options *imapv2.FetchOptions) error {
	for _, section := range options.BodySection {
		if len(section.HeaderFields) == 0 {
			<-s.release
			break
		}
	}
	return s.SessionIMAP4rev2.Fetch(w, numSet, options)
}

// Opening a mail, downloading a file and reading texts for the rules end
// with their request: a server
// that does not answer is left when the context ends, not when the client's
// own timeout would.
func TestReadsEndWithTheirContext(t *testing.T) {
	reads := map[string]func(c *Client, ctx context.Context, uid string) error{
		"Fetch": func(c *Client, ctx context.Context, uid string) error {
			_, err := c.Fetch(ctx, "INBOX", uid)
			return err
		},
		"Attachment": func(c *Client, ctx context.Context, uid string) error {
			_, _, err := c.Attachment(ctx, "INBOX", uid, "2", testLimit)
			return err
		},
		// The rules reading the text of the mails they judge.
		"Texts": func(c *Client, ctx context.Context, uid string) error {
			_, err := c.Texts(ctx, "INBOX", []string{uid})
			return err
		},
	}
	for name, read := range reads {
		t.Run(name, func(t *testing.T) {
			release := make(chan struct{})
			addr := serveTestServer(t, standardMailboxes(), func(s imapserver.SessionIMAP4rev2) imapserver.Session {
				return heldFetch{SessionIMAP4rev2: s, release: release}
			})
			t.Cleanup(func() { close(release) })
			c := dialTestServer(t, addr)
			uid := stored(t, c, mixed(textPart, pdfPart()))

			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- read(c, ctx, uid) }()
			select {
			case err := <-done:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("%s(server held) error = %v, want context.DeadlineExceeded", name, err)
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("%s(server held) still waits 10s after its context ended at 300ms", name)
			}

			if err := read(c, ctx, uid); !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("%s(ended context) error = %v, want context.DeadlineExceeded at once", name, err)
			}
		})
	}
}

// The parameters of a header, decoded however an accented name was
// written.
func TestHeaderParams(t *testing.T) {
	cases := []struct {
		value string
		key   string
		want  string
	}{
		{`attachment; filename="facture.pdf"`, "filename", "facture.pdf"},
		{`attachment; filename*=utf-8''Re%C3%A7u.pdf`, "filename", "Reçu.pdf"},
		{`attachment; filename*=UTF-8''Re%C3%A7u.pdf`, "filename", "Reçu.pdf"},
		{`attachment; filename*=iso-8859-1''Factur%E9.pdf`, "filename", "Facturé.pdf"},
		{`attachment; filename*=ISO-8859-1'fr'Factur%E9.pdf; size=12`, "filename", "Facturé.pdf"},
		{"attachment; filename*0*=iso-8859-1''Fac; filename*1=tur; filename*2*=%E9.pdf", "filename", "Facturé.pdf"},
		{`application/pdf; name*=windows-1252''%80.pdf`, "name", "€.pdf"},
		{`attachment; filename="=?iso-8859-1?q?R=E9sum=E9.pdf?="`, "filename", "Résumé.pdf"},
		{`attachment; filename*=x-inconnu''Factur%E9.pdf`, "filename", ""},
		// A title in another charset does not move the name.
		{`attachment; title*=iso-8859-1''%E9t%E9; filename="a.pdf"`, "filename", "a.pdf"},
		{`attachment; title*=iso-8859-1''%E9t%E9; filename="a.pdf"`, "title", "été"},
	}
	for _, tc := range cases {
		params, err := headerParams(tc.value)
		if err != nil {
			t.Errorf("headerParams(%q): %v", tc.value, err)
			continue
		}
		if got := params[tc.key]; got != tc.want {
			t.Errorf("headerParams(%q)[%q] = %q, want %q", tc.value, tc.key, got, tc.want)
		}
	}
	if _, err := headerParams(""); err == nil {
		t.Error(`headerParams("") error = nil, want one: no value, no parameters`)
	}
}

// The screen draws the first attachment.MaxDrawn pictures of the HTML, in its
// order: the reader hands out those, and the download serves no other.
func TestOnlyTheFirstPicturesAreDrawn(t *testing.T) {
	n := attachment.MaxDrawn + 1
	var html strings.Builder
	var parts []string
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&html, `<img src="cid:img%d@x">`, i)
		// The parts in the reverse order of the HTML.
		parts = append([]string{fmt.Sprintf("Content-Type: image/png\r\nContent-ID: <img%d@x>\r\n\r\nPNG%d", i, i)}, parts...)
	}
	related := "Content-Type: multipart/related; boundary=\"rel\"\r\n\r\n" +
		"--rel\r\nContent-Type: text/html\r\n\r\n" + html.String() + "\r\n" +
		"--rel\r\n" + strings.Join(parts, "\r\n--rel\r\n") + "\r\n--rel--"
	c := newTestServer(t, standardMailboxes())
	uid := stored(t, c, mixed(related))

	msg, err := c.Fetch(context.Background(), "INBOX", uid)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(msg.InlineImages) != attachment.MaxDrawn {
		t.Fatalf("Fetch(...).InlineImages holds %d pictures, want %d", len(msg.InlineImages), attachment.MaxDrawn)
	}
	// Part 1.2 is the last in the HTML (img31), 1.3 the 30th.
	last, thirtieth := fmt.Sprintf("img%d@x", n), fmt.Sprintf("img%d@x", n-1)
	if _, ok := msg.InlineImages[last]; ok {
		t.Errorf("Fetch(...).InlineImages holds %s, the %dth picture of the HTML", last, n)
	}
	if msg.InlineImages[thirtieth] != "1.3" || msg.InlineImages["img1@x"] != fmt.Sprintf("1.%d", n+1) {
		t.Errorf("Fetch(...).InlineImages = %v, want the first %d of the HTML under their parts", msg.InlineImages, attachment.MaxDrawn)
	}
	if _, data, err := c.Attachment(context.Background(), "INBOX", uid, "1.3", testLimit); err != nil || string(data) != fmt.Sprintf("PNG%d", n-1) {
		t.Errorf("Attachment(1.3, the %dth picture) = %q, %v, want it", n-1, data, err)
	}
	if _, data, err := c.Attachment(context.Background(), "INBOX", uid, "1.2", testLimit); !errors.Is(err, ErrNoSuchAttachment) || data != nil {
		t.Errorf("Attachment(1.2, the %dth picture) = %d bytes, %v, want ErrNoSuchAttachment", n, len(data), err)
	}
}
