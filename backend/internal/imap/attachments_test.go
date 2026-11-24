package imap

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
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
	gomessage "github.com/emersion/go-message"
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

// testLimit is a download cap no file of these tests reaches.
const testLimit = 30 << 20

// The reader lists what was attached, under the part number IMAP gives it,
// with the name decoded and the size of the file itself.
func TestAttachmentsListsTheFilesOfAMessage(t *testing.T) {
	raw := mixed(textPart, pdfPart(), csvPart)

	got, err := Attachments([]byte(raw))
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	want := []Attachment{
		{Part: "2", Filename: "Facture décembre.pdf", MimeType: "application/pdf", Size: int64(len(everyByte()))},
		{Part: "3", Filename: "Relevé d'achats.csv", MimeType: "text/csv", Size: int64(len(csvData))},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Attachments(...) = %+v, want %+v", got, want)
	}
}

// What makes a part a file, and what name it is listed under, as the walk
// reads a part for internal/attachment's rule. The images the HTML shows are
// TestTheListAndTheDownloadFollowOneRule's: a part alone has no HTML.
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
			raw := tc.header + "\r\nbody"
			var got Attachment
			ok := false
			if err := walkLeaves(strings.NewReader(raw), func(number string, header gomessage.Header, _ io.Reader) error {
				part := partOf(header)
				got.MimeType = part.MediaType
				got.Filename, ok = attachment.Listed(part, nil)
				return nil
			}); err != nil {
				t.Fatalf("walkLeaves: %v", err)
			}
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
func TestAttachmentsNumbersPartsTheWayIMAPDoes(t *testing.T) {
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
		"--rel\r\n" + "Content-Type: image/png; name=\"logo.png\"\r\nContent-ID: <logo@acme>\r\n\r\nPNG\r\n" +
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
			name: "a file inside a related, beside an image the html shows",
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
			list, err := Attachments([]byte(tc.raw))
			if err != nil {
				t.Fatalf("Attachments: %v", err)
			}
			got := map[string]string{}
			for _, a := range list {
				got[a.Part] = a.Filename
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Attachments(...) parts = %v, want %v", got, tc.want)
			}
		})
	}
}

// The download hands back the file as it was sent: decoded from its transfer
// encoding, and from nothing else. A text file in windows-1252 stays in
// windows-1252.
func TestAttachmentInGivesTheFileByteForByte(t *testing.T) {
	raw := []byte(mixed(textPart, pdfPart(), csvPart))

	cases := []struct {
		part     string
		wantName string
		wantData []byte
	}{
		{"2", "Facture décembre.pdf", everyByte()},
		{"3", "Relevé d'achats.csv", csvData},
	}
	for _, tc := range cases {
		att, data, err := attachmentIn(bytes.NewReader(raw), tc.part, testLimit)
		if err != nil {
			t.Fatalf("attachmentIn(%q): %v", tc.part, err)
		}
		if !bytes.Equal(data, tc.wantData) {
			t.Errorf("attachmentIn(%q) = %q, want %q", tc.part, data, tc.wantData)
		}
		if att.Filename != tc.wantName {
			t.Errorf("attachmentIn(%q) filename = %q, want %q", tc.part, att.Filename, tc.wantName)
		}
		if att.Size != int64(len(tc.wantData)) {
			t.Errorf("attachmentIn(%q) size = %d, want %d", tc.part, att.Size, len(tc.wantData))
		}
	}
}

// A part number is easy to guess. Only one the reader lists as a file is
// served: never the text of the mail, never a section IMAP knows by name.
func TestAttachmentInRefusesWhatIsNotAFile(t *testing.T) {
	invoice := []byte(mixed(textPart, pdfPart(), csvPart))
	plain := []byte("From: a@b.com\r\nSubject: S\r\nContent-Type: text/plain\r\n\r\nBonjour.\r\n")

	cases := []struct {
		name string
		raw  []byte
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
			_, data, err := attachmentIn(bytes.NewReader(tc.raw), tc.part, testLimit)
			if !errors.Is(err, ErrNoSuchAttachment) {
				t.Errorf("attachmentIn(%q) error = %v, want ErrNoSuchAttachment", tc.part, err)
			}
			if data != nil {
				t.Errorf("attachmentIn(%q) handed out %d bytes, want none", tc.part, len(data))
			}
		})
	}
}

// One file that does not decode does not keep the reader from listing the
// others, and the error names it. Its download is an error, not "no such
// file": the mail does have one there.
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
	raw := []byte(mixed(textPart, broken, unknown, pdfPart()))

	list, err := Attachments(raw)
	if err == nil {
		t.Error("Attachments(...) said nothing of the files it could not decode")
	} else {
		for _, name := range []string{"casse.pdf", "vieux.bin"} {
			if !strings.Contains(err.Error(), name) {
				t.Errorf("Attachments(...) error = %q, want it to name %q", err, name)
			}
		}
	}
	if len(list) != 1 || list[0].Filename != "Facture décembre.pdf" {
		t.Errorf("Attachments(...) = %+v, want the one file that decodes", list)
	}

	for _, part := range []string{"2", "3"} {
		_, data, err := attachmentIn(bytes.NewReader(raw), part, testLimit)
		if err == nil || errors.Is(err, ErrNoSuchAttachment) {
			t.Errorf("attachmentIn(%q) error = %v, want a decoding error", part, err)
		}
		if data != nil {
			t.Errorf("attachmentIn(%q) handed out %d bytes, want none", part, len(data))
		}
	}
}

// Over a real server: the reader's fetch lists the files, and saving one
// gives the file without marking the mail read.
func TestClientAttachmentOverIMAP(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	appendRaw(t, c, "INBOX", mixed(textPart, pdfPart(), csvPart))

	emails, err := c.List(context.Background(), "INBOX", 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	uid := emails[0].MessageID

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
	if _, _, err := c.Attachment(context.Background(), "INBOX", uid, "1", testLimit); !errors.Is(err, ErrNoSuchAttachment) {
		t.Errorf("Attachment(1) error = %v, want ErrNoSuchAttachment: part 1 is the text", err)
	}

	// Read back off the server, not off what the fetch returned.
	if flags := flagsOf(t, c, "INBOX", uid); has(flags, mailbox.FlagSeen) {
		t.Errorf("after Attachment, the message carries %s on the server: the download must peek", mailbox.FlagSeen)
	}
}

// A newsletter that is also an invoice, with every kind of part the rule
// tells apart.
func newsletterMail() string {
	related := "Content-Type: multipart/related; boundary=\"rel\"\r\n" +
		"\r\n" +
		"--rel\r\n" + "Content-Type: text/html; charset=utf-8\r\n\r\n" +
		"<p>Bonjour</p><img src=\"cid:logo@acme\"><div style=\"background:url(cid:fond@acme)\">\r\n" +
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
	return mixed(related, gmailWeb, appleInline, photo, unnamed, forwarded)
}

// The list and the download follow internal/attachment's rule, and agree:
// what the reader lists is what the download serves, under the same name,
// and what it does not list, the download does not serve.
func TestTheListAndTheDownloadFollowOneRule(t *testing.T) {
	raw := []byte(newsletterMail())
	want := []Attachment{
		{Part: "2", Filename: "facture.pdf", MimeType: "application/pdf", Size: int64(len("%PDF-gmail"))},
		{Part: "3", Filename: "devis.pdf", MimeType: "application/pdf", Size: int64(len("%PDF-apple"))},
		{Part: "4", Filename: "photo.jpg", MimeType: "image/jpeg", Size: int64(len("JPEG-photo"))},
		{Part: "5", Filename: attachment.Unnamed, MimeType: "application/octet-stream", Size: int64(len("BYTES"))},
		{Part: "6", Filename: attachment.UnnamedMail, MimeType: "message/rfc822", Size: int64(len("From: a@b.com\r\nSubject: Transfert\r\n\r\nLe mail d'origine.\r\n"))},
	}

	got, err := Attachments(raw)
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Attachments(...) = %+v, want %+v", got, want)
	}

	for _, w := range want {
		att, data, err := attachmentIn(bytes.NewReader(raw), w.Part, testLimit)
		if err != nil {
			t.Errorf("attachmentIn(%q) error = %v, want the file the reader lists", w.Part, err)
			continue
		}
		if att != w || int64(len(data)) != w.Size {
			t.Errorf("attachmentIn(%q) = %+v (%d bytes), want %+v", w.Part, att, len(data), w)
		}
	}
	// The two images the HTML shows: the logo, and the background,
	// declared an attachment and unnamed.
	for _, part := range []string{"1.2", "1.3", "1.1"} {
		if _, data, err := attachmentIn(bytes.NewReader(raw), part, testLimit); !errors.Is(err, ErrNoSuchAttachment) || data != nil {
			t.Errorf("attachmentIn(%q) = %d bytes, %v, want ErrNoSuchAttachment", part, len(data), err)
		}
	}
}

// The HTML that shows an image may come after it in the message: the image
// is still not a file, in the list as in the download.
func TestAnImageTheHTMLShowsAfterIt(t *testing.T) {
	related := "Content-Type: multipart/related; boundary=\"rel\"\r\n" +
		"\r\n" +
		"--rel\r\n" + "Content-Type: image/png; name=\"logo.png\"\r\nContent-ID: <logo@acme>\r\n\r\nPNG\r\n" +
		"--rel\r\n" + "Content-Type: text/html\r\n\r\n<img src=\"cid:logo@acme\">\r\n" +
		"--rel--"
	raw := []byte(mixed(related, pdfPart()))

	got, err := Attachments(raw)
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(got) != 1 || got[0].Part != "2" {
		t.Errorf("Attachments(...) = %+v, want the PDF alone", got)
	}
	if _, _, err := attachmentIn(bytes.NewReader(raw), "1.1", testLimit); !errors.Is(err, ErrNoSuchAttachment) {
		t.Errorf("attachmentIn(1.1) error = %v, want ErrNoSuchAttachment", err)
	}
}

// The download holds no more of a file than the cap: one at the cap is
// served, one byte over is refused and nothing of it handed out.
func TestAttachmentInStopsAtTheLimit(t *testing.T) {
	data := bytes.Repeat([]byte("Libellé;Montant\r\n"), 100)
	var qp bytes.Buffer
	w := quotedprintable.NewWriter(&qp)
	_, _ = w.Write(data)
	_ = w.Close()
	file := "Content-Type: text/csv\r\nContent-Disposition: attachment; filename=\"achats.csv\"\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n\r\n" + qp.String()
	raw := []byte(mixed(textPart, file))

	att, got, err := attachmentIn(bytes.NewReader(raw), "2", int64(len(data)))
	if err != nil || !bytes.Equal(got, data) || att.Size != int64(len(data)) {
		t.Errorf("attachmentIn(limit = its size) = %d bytes, %v, want the file", len(got), err)
	}
	if _, got, err := attachmentIn(bytes.NewReader(raw), "2", int64(len(data))-1); !errors.Is(err, ErrAttachmentTooLarge) || got != nil {
		t.Errorf("attachmentIn(limit = its size - 1) = %d bytes, %v, want ErrAttachmentTooLarge and nothing", len(got), err)
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

// The server's description of a message is enough to refuse a file too
// large, and only a file: never the text of the mail, never a part it does
// not describe.
func TestTooLargeToRead(t *testing.T) {
	structure := &imapv2.BodyStructureMultiPart{
		Subtype: "mixed",
		Children: []imapv2.BodyStructure{
			&imapv2.BodyStructureSinglePart{Type: "text", Subtype: "plain", Encoding: "8bit", Size: 50 << 20},
			&imapv2.BodyStructureSinglePart{
				Type: "application", Subtype: "pdf", Encoding: "base64", Size: 4 << 20,
				Extended: &imapv2.BodyStructureSinglePartExt{Disposition: &imapv2.BodyStructureDisposition{Value: "attachment", Params: map[string]string{"filename*": "utf-8''Re%C3%A7u.pdf"}}},
			},
			&imapv2.BodyStructureMultiPart{Subtype: "related", Children: []imapv2.BodyStructure{
				&imapv2.BodyStructureSinglePart{Type: "text", Subtype: "html", Size: 10},
				&imapv2.BodyStructureSinglePart{Type: "image", Subtype: "png", Params: map[string]string{"name": "logo.png"}, Encoding: "base64", Size: 4 << 20},
			}},
			&imapv2.BodyStructureSinglePart{Type: "message", Subtype: "rfc822", Size: 4 << 20},
		},
	}
	cases := []struct {
		part  string
		limit int64
		want  bool
	}{
		{"2", 2 << 20, true},
		{"2", 3 << 20, false}, // 4 MiB of base64 is about 3 MiB of file
		{"3.2", 2 << 20, true},
		{"4", 3 << 20, true},
		{"1", 1 << 20, false}, // the text of the mail is not a file
		{"3", 1, false},       // a multipart is not a leaf
		{"5", 1, false},
		{"TEXT", 1, false},
		{"", 1, false},
	}
	for _, tc := range cases {
		if got := tooLargeToRead(structure, tc.part, tc.limit); got != tc.want {
			t.Errorf("tooLargeToRead(%q, %d) = %v, want %v", tc.part, tc.limit, got, tc.want)
		}
	}
	if tooLargeToRead(nil, "1", 1) {
		t.Error("tooLargeToRead(nil, ...) = true, want false: no description, no refusal")
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
// comes over the connection.
func TestClientAttachmentRefusesALargeFileBeforeReadingIt(t *testing.T) {
	var read atomic.Int64
	c := dialTestServerThrough(t, startTestServer(t, standardMailboxes()), func(conn net.Conn) net.Conn {
		return countingConn{Conn: conn, read: &read}
	})
	big := bytes.Repeat(everyByte(), 2048) // 1 MiB
	file := "Content-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"gros.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + base64Body(big)
	appendRaw(t, c, "INBOX", mixed(textPart, file))
	emails, err := c.List(context.Background(), "INBOX", 10)
	if err != nil || len(emails) != 1 {
		t.Fatalf("List = %d mails, %v", len(emails), err)
	}

	before := read.Load()
	_, data, err := c.Attachment(context.Background(), "INBOX", emails[0].MessageID, "2", 512<<10)
	if !errors.Is(err, ErrAttachmentTooLarge) || data != nil {
		t.Fatalf("Attachment(1 MiB file, cap 512 KiB) = %d bytes, %v, want ErrAttachmentTooLarge", len(data), err)
	}
	if got := read.Load() - before; got > 16<<10 {
		t.Errorf("Attachment read %d bytes off the connection to refuse it, want the server's description only", got)
	}

	// The same file under a cap it fits in comes whole.
	_, data, err = c.Attachment(context.Background(), "INBOX", emails[0].MessageID, "2", int64(len(big)))
	if err != nil || !bytes.Equal(data, big) {
		t.Errorf("Attachment(1 MiB file, cap 1 MiB) = %d bytes, %v, want the file", len(data), err)
	}
}

// A file whose size the description cannot bound (quoted-printable) is
// read through the cap, and refused there.
func TestClientAttachmentStopsAtTheLimit(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	data := bytes.Repeat([]byte("Café;12,90\r\n"), 2000)
	var qp bytes.Buffer
	w := quotedprintable.NewWriter(&qp)
	_, _ = w.Write(data)
	_ = w.Close()
	appendRaw(t, c, "INBOX", mixed(textPart, "Content-Type: text/csv\r\nContent-Disposition: attachment; filename=\"achats.csv\"\r\n"+
		"Content-Transfer-Encoding: quoted-printable\r\n\r\n"+qp.String()))
	emails, err := c.List(context.Background(), "INBOX", 10)
	if err != nil || len(emails) != 1 {
		t.Fatalf("List = %d mails, %v", len(emails), err)
	}
	if _, got, err := c.Attachment(context.Background(), "INBOX", emails[0].MessageID, "2", 1000); !errors.Is(err, ErrAttachmentTooLarge) || got != nil {
		t.Errorf("Attachment(cap 1000 bytes) = %d bytes, %v, want ErrAttachmentTooLarge", len(got), err)
	}
}

// A mail another client moved since the list showed it is gone, for the
// reader and for the download, which is not a failure of the server.
func TestClientReadsOfAMailThatIsGone(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	appendRaw(t, c, "INBOX", mixed(textPart, pdfPart()))
	emails, err := c.List(context.Background(), "INBOX", 10)
	if err != nil || len(emails) != 1 {
		t.Fatalf("List = %d mails, %v", len(emails), err)
	}
	uid := emails[0].MessageID
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

// heldFetch is a server session whose FETCH of a whole message (BODY[])
// waits for release: a server too slow to hand a mail over. The listing,
// which asks for two header fields, is answered.
type heldFetch struct {
	imapserver.SessionIMAP4rev2
	release <-chan struct{}
}

func (s heldFetch) Fetch(w *imapserver.FetchWriter, numSet imapv2.NumSet, options *imapv2.FetchOptions) error {
	for _, section := range options.BodySection {
		if section.Specifier == imapv2.PartSpecifierNone && len(section.Part) == 0 {
			<-s.release
		}
	}
	return s.SessionIMAP4rev2.Fetch(w, numSet, options)
}

// The download ends with its request: a server that does not answer is
// left when the context ends, not when the client's own timeout does.
func TestClientAttachmentEndsWithItsContext(t *testing.T) {
	release := make(chan struct{})
	addr := serveTestServer(t, standardMailboxes(), func(s imapserver.SessionIMAP4rev2) imapserver.Session {
		return heldFetch{SessionIMAP4rev2: s, release: release}
	})
	t.Cleanup(func() { close(release) })
	c := dialTestServer(t, addr)
	appendRaw(t, c, "INBOX", mixed(textPart, pdfPart()))
	emails, err := c.List(context.Background(), "INBOX", 10)
	if err != nil || len(emails) != 1 {
		t.Fatalf("List = %d mails, %v", len(emails), err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	type answer struct {
		data []byte
		err  error
	}
	done := make(chan answer, 1)
	go func() {
		_, data, err := c.Attachment(ctx, "INBOX", emails[0].MessageID, "2", testLimit)
		done <- answer{data, err}
	}()
	select {
	case got := <-done:
		if !errors.Is(got.err, context.DeadlineExceeded) || got.data != nil {
			t.Errorf("Attachment(server held) = %d bytes, %v, want context.DeadlineExceeded", len(got.data), got.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Attachment(server held) still waits 10s after its context ended at 300ms")
	}

	if _, _, err := c.Attachment(ctx, "INBOX", emails[0].MessageID, "2", testLimit); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Attachment(ended context) error = %v, want context.DeadlineExceeded at once", err)
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
