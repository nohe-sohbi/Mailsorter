package imap

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"reflect"
	"strings"
	"testing"

	gomessage "github.com/emersion/go-message"
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

// What makes a part a file, and what name it is listed under.
func TestAttachmentOf(t *testing.T) {
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
			name:   "an unnamed attachment is not listed, it would be a blank chip",
			header: "Content-Type: application/pdf\r\nContent-Disposition: attachment\r\n",
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
			name:   "an image the HTML shows through cid:",
			header: "Content-Type: image/png; name=\"logo.png\"\r\nContent-Disposition: inline; filename=\"logo.png\"\r\nContent-ID: <logo@acme>\r\n",
		},
		{
			name:   "an image the HTML shows, without a disposition",
			header: "Content-Type: image/png; name=\"logo.png\"\r\nContent-ID: <logo@acme>\r\n",
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
			if err := walkLeaves([]byte(raw), func(number string, header gomessage.Header, _ io.Reader) error {
				got, ok = attachmentOf(number, header)
				return nil
			}); err != nil {
				t.Fatalf("walkLeaves: %v", err)
			}
			if ok != tc.wantOK {
				t.Fatalf("attachmentOf(%q) ok = %v, want %v", tc.header, ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if got.Filename != tc.wantName {
				t.Errorf("attachmentOf(%q) filename = %q, want %q", tc.header, got.Filename, tc.wantName)
			}
			if got.MimeType != tc.wantType {
				t.Errorf("attachmentOf(%q) type = %q, want %q", tc.header, got.MimeType, tc.wantType)
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
		att, data, err := attachmentIn(raw, tc.part)
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
			_, data, err := attachmentIn(tc.raw, tc.part)
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
		_, data, err := attachmentIn(raw, part)
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

	att, data, err := c.Attachment(context.Background(), "INBOX", uid, "2")
	if err != nil {
		t.Fatalf("Attachment(2): %v", err)
	}
	if !bytes.Equal(data, everyByte()) || att.Filename != "Facture décembre.pdf" {
		t.Errorf("Attachment(2) = %q (%d bytes), want the PDF as sent", att.Filename, len(data))
	}
	if _, _, err := c.Attachment(context.Background(), "INBOX", uid, "1"); !errors.Is(err, ErrNoSuchAttachment) {
		t.Errorf("Attachment(1) error = %v, want ErrNoSuchAttachment: part 1 is the text", err)
	}

	// Read back off the server, not off what the fetch returned.
	if flags := flagsOf(t, c, "INBOX", uid); has(flags, mailbox.FlagSeen) {
		t.Errorf("after Attachment, the message carries %s on the server: the download must peek", mailbox.FlagSeen)
	}
}
