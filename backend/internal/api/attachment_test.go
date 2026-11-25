package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/attachment"
	"github.com/nohe-sohbi/mailsorter/backend/internal/imap"
	gmailapi "google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
)

// The name a download is saved under is cleaned the way the reader's list
// is (attachment.SafeName): a bidirectional control does not reach the
// header, in either parameter.
func TestContentDispositionCleansTheName(t *testing.T) {
	got := contentDisposition("../facture\u202Efdp.exe")
	want := `attachment; filename="facturefdp.exe"; filename*=UTF-8''facturefdp.exe`
	if got != want {
		t.Errorf("contentDisposition(...) = %q, want %q", got, want)
	}
	if got := contentDisposition(""); !strings.Contains(got, `filename="`+attachment.Unnamed+`"`) {
		t.Errorf("contentDisposition(\"\") = %q, want it named %q", got, attachment.Unnamed)
	}
}

// A header injected through a filename would let a sender rewrite the response.
func TestContentDispositionIsHeaderSafe(t *testing.T) {
	got := contentDisposition("evil\r\nSet-Cookie: a=b\".pdf")
	if strings.ContainsAny(got, "\r\n") {
		t.Fatalf("disposition carries a line break: %q", got)
	}
	if strings.Count(got, `"`) != 2 {
		t.Errorf("quoted filename is not balanced: %q", got)
	}
}

// A non-ASCII name must survive in filename* and degrade in filename.
func TestContentDispositionEncodesUnicode(t *testing.T) {
	got := contentDisposition("reçu.pdf")
	if !strings.Contains(got, "filename*=UTF-8''re%C3%A7u.pdf") {
		t.Errorf("missing RFC 5987 form: %q", got)
	}
	if !strings.Contains(got, `filename="re_u.pdf"`) {
		t.Errorf("missing ASCII fallback: %q", got)
	}
}

func TestAttachmentContentType(t *testing.T) {
	cases := map[string]string{
		"application/pdf":           "application/pdf",
		"":                          "application/octet-stream",
		"not a mime type":           "application/octet-stream",
		"text/plain; charset=UTF-8": "text/plain; charset=UTF-8",
	}
	for in, want := range cases {
		if got := attachmentContentType(in); got != want {
			t.Errorf("attachmentContentType(%q) = %q, want %q", in, got, want)
		}
	}
}

// The reader must publish an id the download can take back (the part
// number, never Gmail's own handle, which is kept for the download alone),
// keep the unnamed images a newsletter draws out of the list, and say where
// they go in the text.
func TestReadGmailPartsExposesIDs(t *testing.T) {
	msg := &gmailapi.Message{Payload: &gmailapi.MessagePart{
		MimeType: "multipart/mixed",
		Parts: []*gmailapi.MessagePart{
			{
				PartId:   "0",
				MimeType: "text/html",
				Body:     &gmailapi.MessagePartBody{Data: "PGltZyBzcmM9ImNpZDpsb2dvIj4"},
			},
			{
				PartId:   "1",
				Filename: "facture.pdf",
				MimeType: "application/pdf",
				Body:     &gmailapi.MessagePartBody{AttachmentId: "att-1", Size: 2048},
			},
			{
				PartId:   "2",
				MimeType: "image/png",
				Headers:  []*gmailapi.MessagePartHeader{{Name: "Content-ID", Value: "<logo>"}},
				Body:     &gmailapi.MessagePartBody{AttachmentId: "att-2", Size: 10},
			},
			{
				// Its bytes travel with the message: served from there.
				PartId:   "3",
				Filename: "petit.txt",
				MimeType: "text/plain",
				Headers:  []*gmailapi.MessagePartHeader{{Name: "Content-Disposition", Value: "attachment"}},
				Body:     &gmailapi.MessagePartBody{Data: "eA", Size: 1},
			},
		},
	}}

	parts := readGmailParts(msg, `<img src="cid:logo">`)
	got := parts.files
	if len(got) != 2 {
		t.Fatalf("readGmailParts listed %d parts, want the two files: %#v", len(got), got)
	}
	if got[0].AttachmentID != "1" || got[0].gmailID != "att-1" || got[0].Size != 2048 {
		t.Errorf("attachment = %#v, want id 1 (Gmail's att-1 kept aside) and size 2048", got[0])
	}
	if got[1].Filename != "petit.txt" || got[1].AttachmentID != "3" || got[1].inlineData != "eA" {
		t.Errorf("attachment = %#v, want petit.txt under 3, its bytes from the message", got[1])
	}
	if images := parts.inlineImages(); !reflect.DeepEqual(images, map[string]string{"logo": "2"}) {
		t.Errorf("inline images = %v, want the logo under 2", images)
	}
	if body, _ := json.Marshal(messageView{InlineImages: parts.inlineImages()}); !strings.Contains(string(body), `"inlineImages":{"logo":"2"}`) {
		t.Errorf("the reader sends %s, want the screen's key inlineImages", body)
	}
	if body, _ := json.Marshal(got[0]); strings.Contains(string(body), "att-1") {
		t.Errorf("the reader sends %s: Gmail's handle must not reach the screen", body)
	}

	// The logo is served by its number, for the screen; never by Gmail's
	// handle, which no reader ever handed out for it.
	if a, ok := parts.find("2"); !ok || a.gmailID != "att-2" {
		t.Errorf("find(\"2\") = %#v, %v, want the logo", a, ok)
	}
	if _, ok := parts.find("att-2"); ok {
		t.Error("find(\"att-2\") found the logo by Gmail's handle")
	}
	for _, id := range []string{"1", "att-1"} {
		if a, ok := parts.find(id); !ok || a.Filename != "facture.pdf" {
			t.Errorf("find(%q) = %#v, %v, want facture.pdf", id, a, ok)
		}
	}
	for _, id := range []string{"0", "9", ""} {
		if a, ok := parts.find(id); ok {
			t.Errorf("find(%q) = %#v, want nothing", id, a)
		}
	}
}

// An empty id must never match a part that has none, or a message whose parts
// carry inline data would become addressable with an empty path segment.
func TestFindAttachmentRejectsEmptyID(t *testing.T) {
	if _, ok := findAttachment([]attachmentView{{Filename: "inline.png"}}, ""); ok {
		t.Error("findAttachment matched an empty attachment id")
	}
}

// The route must exist and be gated: an anonymous caller gets 401, not 404.
func TestAttachmentRouteRequiresASession(t *testing.T) {
	srv := newRoutedTestServer(t)

	res, err := http.Get(srv.URL + "/api/emails/msg-1/attachments/att-1")
	if err != nil {
		t.Fatalf("GET attachment: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("attachment route without a session = %d, want 401", res.StatusCode)
	}
}

// The fixed /api/emails/* routes must keep winning over the {id} pattern, and
// the attachment path must not swallow the single-message route.
func TestAttachmentRouteDoesNotShadowMessageRoutes(t *testing.T) {
	h := newTestHandler(t)
	router := h.SetupRoutes()

	for _, tc := range []struct{ path, want string }{
		{"/api/emails/msg-1", "message"},
		{"/api/emails/msg-1/attachments/att-1", "attachment"},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		// Both are gated, so 401 proves the path matched a real route rather
		// than falling through to 404.
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s (%s route) = %d, want 401", tc.path, tc.want, rec.Code)
		}
	}
}

// Over the Gmail API the files follow the rule IMAP follows
// (attachment.Listed). It used to hide every part with a Content-ID, an
// X-Attachment-Id or an inline disposition: the file attached from Gmail's
// web interface and the PDF Apple Mail sends inline were not shown. A named
// image the HTML draws is listed as well, to be saved.
func TestListAttachmentsFollowsTheSharedRule(t *testing.T) {
	header := func(name, value string) *gmailapi.MessagePartHeader {
		return &gmailapi.MessagePartHeader{Name: name, Value: value}
	}
	body := func(id string) *gmailapi.MessagePartBody {
		return &gmailapi.MessagePartBody{AttachmentId: id, Size: 100}
	}
	msg := &gmailapi.Message{Payload: &gmailapi.MessagePart{
		MimeType: "multipart/mixed",
		Parts: []*gmailapi.MessagePart{
			{MimeType: "multipart/related", Parts: []*gmailapi.MessagePart{
				{MimeType: "text/html", Body: &gmailapi.MessagePartBody{Data: "eA"}},
				{Filename: "logo.png", MimeType: "image/png", Body: body("logo"),
					Headers: []*gmailapi.MessagePartHeader{header("Content-Disposition", "inline; filename=logo.png"), header("Content-ID", "<logo@acme>"), header("X-Attachment-Id", "logo")}},
			}},
			{Filename: "facture.pdf", MimeType: "application/pdf", Body: body("gmail-web"),
				Headers: []*gmailapi.MessagePartHeader{header("Content-Disposition", `attachment; filename="facture.pdf"`), header("Content-ID", "<f_abc>"), header("X-Attachment-Id", "f_abc")}},
			{Filename: "devis.pdf", MimeType: "application/pdf", Body: body("apple-inline"),
				Headers: []*gmailapi.MessagePartHeader{header("Content-Disposition", `inline; filename="devis.pdf"`)}},
			{Filename: "photo.jpg", MimeType: "image/jpeg", Body: body("photo"),
				Headers: []*gmailapi.MessagePartHeader{header("Content-Disposition", "inline"), header("Content-ID", "<photo@iphone>")}},
			{MimeType: "application/octet-stream", Body: body("unnamed"),
				Headers: []*gmailapi.MessagePartHeader{header("Content-Disposition", "attachment")}},
			{MimeType: "message/rfc822", Body: body("forwarded")},
			{Filename: "facture\u202Efdp.exe", MimeType: "application/octet-stream", Body: body("bidi"),
				Headers: []*gmailapi.MessagePartHeader{header("Content-Disposition", "attachment")}},
			{MimeType: "text/plain", Body: &gmailapi.MessagePartBody{Data: "eA"}},
		},
	}}

	got := readGmailParts(msg, `<p>Bonjour</p><img src="cid:logo@acme">`).files
	var names []string
	for _, a := range got {
		names = append(names, a.Filename+" ("+a.AttachmentID+")")
	}
	want := []string{
		// Drawn in the text, and listed so it can be saved: it has a name.
		"logo.png (logo)",
		"facture.pdf (gmail-web)",
		"devis.pdf (apple-inline)",
		"photo.jpg (photo)",
		attachment.Unnamed + " (unnamed)",
		attachment.UnnamedMail + " (forwarded)",
		"facturefdp.exe (bidi)",
	}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("listAttachments(...) = %q, want %q", names, want)
	}
}

// A read of the mailbox that fails answers a French sentence and nothing
// of the error, which goes to the log; a mail that is no longer there is a
// 404 of its own, on both transports.
func TestWriteMailboxFailure(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code int
		said string
	}{
		{"imap, a mail moved from the phone", fmt.Errorf("%w: uid 12 in \"INBOX\"", imap.ErrMessageGone), http.StatusNotFound, attachmentGoneMessage},
		{"gmail, a mail deleted", &googleapi.Error{Code: http.StatusNotFound, Message: "Requested entity was not found."}, http.StatusNotFound, attachmentGoneMessage},
		{"gmail, wrapped", fmt.Errorf("get: %w", &googleapi.Error{Code: http.StatusNotFound}), http.StatusNotFound, attachmentGoneMessage},
		{"gmail, down", &googleapi.Error{Code: http.StatusServiceUnavailable, Message: "Backend Error"}, http.StatusBadGateway, attachmentFailedMessage},
		{"imap, the connection dropped", errors.New("imap: fetch message 12 in \"INBOX\": EOF"), http.StatusBadGateway, attachmentFailedMessage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeMailboxFailure(rec, tc.err, "test", attachmentGoneMessage, attachmentFailedMessage)
			var body struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("body %q is not the JSON envelope: %v", rec.Body.String(), err)
			}
			if rec.Code != tc.code || body.Error != tc.said {
				t.Errorf("writeMailboxFailure(%v) = %d %q, want %d %q", tc.err, rec.Code, body.Error, tc.code, tc.said)
			}
			for _, leak := range []string{"imap", "googleapi", "uid", "EOF", "Backend", "entity"} {
				if strings.Contains(rec.Body.String(), leak) {
					t.Errorf("writeMailboxFailure(%v) sent %q, which names the error (%q)", tc.err, rec.Body.String(), leak)
				}
			}
		})
	}
}
