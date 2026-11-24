package imap

import (
	"context"
	"reflect"
	"strings"
	"testing"

	imapv2 "github.com/emersion/go-imap/v2"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
)

// States answers for the whole folder, not a page of it: it is how the sync
// tells which old stored messages are still in the inbox, what another mail
// program did to them, and how large each is. Read from the server after real
// STOREs.
func TestStatesReadsEveryMessageOfTheFolder(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	plain := appendMessage(t, c, "INBOX")
	read := appendMessage(t, c, "INBOX")
	starred := appendMessage(t, c, "INBOX")
	both := appendMessage(t, c, "INBOX")
	appendMessage(t, c, "Archive")
	for _, step := range []struct {
		uid    string
		action mailbox.Action
	}{
		{read, mailbox.ActionMarkRead},
		{starred, mailbox.ActionStar},
		{both, mailbox.ActionMarkRead},
		{both, mailbox.ActionStar},
	} {
		if err := c.Apply(context.Background(), mailbox.InFolder("INBOX", step.uid), mailbox.Mutation{Action: step.action}); err != nil {
			t.Fatalf("%s on %s: %v", step.action, step.uid, err)
		}
	}

	got, err := c.States("INBOX")
	if err != nil {
		t.Fatal(err)
	}
	// appendMessage appends the same text each time.
	size := int64(len("From: sender@example.com\r\nTo: " + testUser + "\r\nSubject: Test\r\n\r\nBody.\r\n"))
	want := map[string]State{
		plain:   {Size: size},
		read:    {Read: true, Size: size},
		starred: {Starred: true, Size: size},
		both:    {Read: true, Starred: true, Size: size},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("States(INBOX) = %v, want %v", got, want)
	}
}

// An empty folder is an empty answer, not an error: a FETCH of 1:* on it is
// one on some servers.
func TestStatesOfAnEmptyFolder(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	got, err := c.States("INBOX")
	if err != nil || len(got) != 0 {
		t.Errorf("States of an empty INBOX = %v, %v; want an empty map", got, err)
	}
}

// A mail built for the attachment tests: its MIME body, after the headers.
func mimeMessage(subject, contentType, body string) string {
	return "From: a@b.com\r\nTo: " + testUser + "\r\nSubject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: " + contentType + "\r\n\r\n" + body
}

// Attachments reads off the server whether each message asked carries a file,
// for those only, without marking them read.
func TestAttachmentsReadsTheStructure(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	appendRaw(t, c, "INBOX", message("a@b.com", "Texte"))
	appendRaw(t, c, "INBOX", invoiceMessage("Facture", "facture.pdf"))
	appendRaw(t, c, "INBOX", message("a@b.com", "Pas demandé"))

	emails, err := c.List(context.Background(), "INBOX", 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// Newest first: the one not asked, the invoice, the text.
	notAsked, invoiceUID, textUID := emails[0].MessageID, emails[1].MessageID, emails[2].MessageID

	got, err := c.Attachments("INBOX", []string{textUID, invoiceUID, "999"})
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	want := map[string]bool{textUID: false, invoiceUID: true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Attachments = %v, want %v (and nothing for %s, not asked, nor 999, not there)", got, want, notAsked)
	}
	for _, uid := range []string{textUID, invoiceUID} {
		if flags := flagsOf(t, c, "INBOX", uid); has(flags, mailbox.FlagSeen) {
			t.Errorf("message %s is read after Attachments: flags %v", uid, flags)
		}
	}
}

// invoiceMessage is a mail with a PDF attached under this file name.
func invoiceMessage(subject, filename string) string {
	return mimeMessage(subject, `multipart/mixed; boundary="b"`,
		"--b\r\nContent-Type: text/plain\r\n\r\nVoici la facture.\r\n"+
			"--b\r\nContent-Type: application/pdf; name=\""+filename+"\"\r\nContent-Disposition: attachment; filename=\""+filename+"\"\r\nContent-Transfer-Encoding: base64\r\n\r\nJVBERi0xLjQK\r\n"+
			"--b--\r\n")
}

func TestAttachmentsOfNothingAsksNothing(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	if got, err := c.Attachments("INBOX", nil); err != nil || len(got) != 0 {
		t.Errorf("Attachments(nil) = %v, %v; want an empty map", got, err)
	}
}

// A Gmail id is not a UID: refused, never read as the number it starts with.
func TestAttachmentsRefusesSomethingThatIsNotAUID(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	if _, err := c.Attachments("INBOX", []string{"18c8c1f2a3b4d5e6"}); err == nil {
		t.Error("Attachments accepted a Gmail message id as a UID")
	}
}

// Envelopes reads old messages by number the way the listing reads a page,
// star included, for those asked only, without marking them read.
func TestEnvelopesReadsMessagesByNumber(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	old := appendMessage(t, c, "INBOX")
	appendMessage(t, c, "INBOX")
	if err := c.Apply(context.Background(), mailbox.InFolder("INBOX", old), mailbox.Mutation{Action: mailbox.ActionStar}); err != nil {
		t.Fatalf("star: %v", err)
	}

	got, err := c.Envelopes("INBOX", []string{old, "999"})
	if err != nil {
		t.Fatalf("Envelopes: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Envelopes = %v, want the one message asked that is there", got)
	}
	e := got[old]
	if e.MessageID != old || e.Subject != "Test" || e.From != "sender@example.com" || !e.IsStarred || e.IsRead {
		t.Errorf("Envelopes[%s] = %+v, want subject Test, from sender@example.com, starred, unread", old, e)
	}
	if flags := flagsOf(t, c, "INBOX", old); has(flags, mailbox.FlagSeen) {
		t.Errorf("message %s is read after Envelopes: flags %v", old, flags)
	}
}

// single is one part of a structure, as BODYSTRUCTURE describes it.
func single(typ, subtype string, opts ...func(*imapv2.BodyStructureSinglePart)) *imapv2.BodyStructureSinglePart {
	p := &imapv2.BodyStructureSinglePart{Type: typ, Subtype: subtype, Params: map[string]string{}}
	for _, o := range opts {
		o(p)
	}
	return p
}

func disposition(value, filename string) func(*imapv2.BodyStructureSinglePart) {
	return func(p *imapv2.BodyStructureSinglePart) {
		params := map[string]string{}
		if filename != "" {
			params["filename"] = filename
		}
		p.Extended = &imapv2.BodyStructureSinglePartExt{Disposition: &imapv2.BodyStructureDisposition{Value: value, Params: params}}
	}
}

func named(name string) func(*imapv2.BodyStructureSinglePart) {
	return func(p *imapv2.BodyStructureSinglePart) { p.Params["name"] = name }
}

func contentID(id string) func(*imapv2.BodyStructureSinglePart) {
	return func(p *imapv2.BodyStructureSinglePart) { p.ID = id }
}

func multi(subtype string, children ...imapv2.BodyStructure) *imapv2.BodyStructureMultiPart {
	return &imapv2.BodyStructureMultiPart{Subtype: subtype, Children: children}
}

// What counts as an attachment: the shapes real mail takes, the two that look
// like one and are not (a text in two versions, the pictures an HTML mail
// draws) included.
func TestHasAttachment(t *testing.T) {
	text := single("text", "plain")
	html := single("text", "html")
	cases := []struct {
		name string
		bs   imapv2.BodyStructure
		want bool
	}{
		{"nothing read", nil, false},
		{"a text alone", text, false},
		{"text and HTML of a newsletter", multi("alternative", text, html), false},
		{"a file marked attachment", multi("mixed", text, single("application", "pdf", disposition("attachment", "facture.pdf"))), true},
		{"a file next to the two versions of the text", multi("mixed", multi("alternative", text, html), single("application", "pdf", disposition("attachment", "facture.pdf"))), true},
		{"a text file marked attachment", multi("mixed", text, single("text", "csv", disposition("attachment", "export.csv"))), true},
		{"a named file without a disposition", multi("mixed", text, single("application", "zip", named("photos.zip"))), true},
		{"a named file marked inline", multi("mixed", text, single("image", "jpeg", disposition("inline", "photo.jpg"))), true},
		{"a picture the HTML draws, named", multi("related", html, single("image", "png", named("image001.png"), contentID("<logo@x>"))), false},
		{"a picture the HTML draws, inline", multi("related", html, single("image", "png", disposition("inline", "logo.png"), contentID("<logo@x>"))), false},
		{"an inline part without a name", multi("mixed", text, single("image", "png", disposition("inline", ""))), false},
		{"a text part with a name", multi("mixed", text, single("text", "plain", named("notes.txt"))), false},
		{"a picture drawn, and a file", multi("mixed", multi("related", html, single("image", "png", contentID("<logo@x>"), named("logo.png"))), single("application", "pdf", disposition("ATTACHMENT", "contrat.pdf"))), true},
		{"a single part that is a file", single("application", "pdf", disposition("attachment", "facture.pdf")), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasAttachment(tc.bs); got != tc.want {
				t.Errorf("hasAttachment(%s) = %v, want %v", describe(tc.bs), got, tc.want)
			}
		})
	}
}

// describe is a structure in a failure message.
func describe(bs imapv2.BodyStructure) string {
	if bs == nil {
		return "nil"
	}
	var parts []string
	bs.Walk(func(_ []int, part imapv2.BodyStructure) bool {
		parts = append(parts, part.MediaType())
		return true
	})
	return strings.Join(parts, " ")
}
