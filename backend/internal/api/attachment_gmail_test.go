package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/attachment"
	"github.com/nohe-sohbi/mailsorter/backend/internal/gmail"
	"golang.org/x/oauth2"
	gmailapi "google.golang.org/api/gmail/v1"
)

// The PDF of the fake Gmail's invoice, and a file over the download cap.
var (
	gmailInvoicePDF = []byte("%PDF-1.4 facture\x00\xff")
	gmailScanPDF    = []byte("%PDF-1.4 scan")
	gmailLogoPNG    = []byte("\x89PNG\r\n\x1a\n logo")
	gmailDotPNG     = []byte("\x89PNG\r\n\x1a\n point")
)

// rotatingGmail is a Gmail API whose files get a new attachment id at every
// messages.get when rotate is set, and whose bytes are served only under an
// id the latest messages.get gave: the least Gmail's documentation promises.
type rotatingGmail struct {
	rotate bool

	mu     sync.Mutex
	gets   int
	issued map[string][]byte // attachment id -> bytes, from the latest get
	asked  []string          // the attachment ids attachments.get was called with
}

func (g *rotatingGmail) id(part string) string {
	if !g.rotate {
		return "ANGjdJ-stable-" + part
	}
	return fmt.Sprintf("ANGjdJ-%d-%s", g.gets, part)
}

// message builds one message, giving its files the ids of this get.
func (g *rotatingGmail) message(id string) *gmailapi.Message {
	g.issued = map[string][]byte{}
	file := func(partID, name string, data []byte, size int64) *gmailapi.MessagePart {
		aid := g.id(partID)
		g.issued[aid] = data
		return &gmailapi.MessagePart{
			PartId: partID, Filename: name, MimeType: "application/pdf",
			Headers: []*gmailapi.MessagePartHeader{{Name: "Content-Disposition", Value: "attachment; filename=" + name}},
			Body:    &gmailapi.MessagePartBody{AttachmentId: aid, Size: size},
		}
	}
	switch id {
	case "facture":
		return &gmailapi.Message{Id: id, Payload: &gmailapi.MessagePart{
			MimeType: "multipart/mixed",
			Parts: []*gmailapi.MessagePart{
				{PartId: "0", MimeType: "text/plain", Body: &gmailapi.MessagePartBody{Data: base64.URLEncoding.EncodeToString([]byte("Bonjour"))}},
				file("1", "facture.pdf", gmailInvoicePDF, int64(len(gmailInvoicePDF))),
				file("2", "enorme.pdf", nil, maxAttachmentBytes+1),
			},
		}}
	case "newsletter":
		image := func(partID, cid string, data []byte, inline bool) *gmailapi.MessagePart {
			part := &gmailapi.MessagePart{
				PartId: partID, MimeType: "image/png",
				Headers: []*gmailapi.MessagePartHeader{{Name: "Content-ID", Value: "<" + cid + ">"}},
				Body:    &gmailapi.MessagePartBody{Size: int64(len(data))},
			}
			if inline {
				part.Body.Data = base64.URLEncoding.EncodeToString(data)
			} else {
				part.Body.AttachmentId = g.id(partID)
				g.issued[part.Body.AttachmentId] = data
			}
			return part
		}
		html := `<p>Promo</p><img src="cid:logo@acme"><img src="cid:petit@acme">`
		return &gmailapi.Message{Id: id, Payload: &gmailapi.MessagePart{
			MimeType: "multipart/related",
			Parts: []*gmailapi.MessagePart{
				{PartId: "0", MimeType: "text/html", Body: &gmailapi.MessagePartBody{Data: base64.URLEncoding.EncodeToString([]byte(html))}},
				image("1", "logo@acme", gmailLogoPNG, false),
				// The HTML does not point at this one.
				image("2", "pixel@acme", []byte("GIF"), false),
				// Small enough for Gmail to send it with the message.
				image("3", "petit@acme", gmailDotPNG, true),
			},
		}}
	case "thread":
		// A thread quoting signatures: more pictures than the screen draws,
		// the parts in the reverse order of the HTML.
		var html strings.Builder
		var parts []*gmailapi.MessagePart
		for i := 1; i <= attachment.MaxDrawn+1; i++ {
			fmt.Fprintf(&html, `<img src="cid:img%d@x">`, i)
			partID := fmt.Sprint(i)
			aid := g.id(partID)
			g.issued[aid] = []byte("PNG" + partID)
			parts = append([]*gmailapi.MessagePart{{
				PartId: partID, MimeType: "image/png",
				Headers: []*gmailapi.MessagePartHeader{{Name: "Content-ID", Value: fmt.Sprintf("<img%d@x>", i)}},
				Body:    &gmailapi.MessagePartBody{AttachmentId: aid, Size: int64(len("PNG" + partID))},
			}}, parts...)
		}
		return &gmailapi.Message{Id: id, Payload: &gmailapi.MessagePart{
			MimeType: "multipart/related",
			Parts: append([]*gmailapi.MessagePart{
				{PartId: "0", MimeType: "text/html", Body: &gmailapi.MessagePartBody{Data: base64.URLEncoding.EncodeToString([]byte(html.String()))}},
			}, parts...),
		}}
	case "scan":
		// A message that is a file and nothing else: the payload itself,
		// which Gmail gives no part number.
		payload := file("", "scan.pdf", gmailScanPDF, int64(len(gmailScanPDF)))
		return &gmailapi.Message{Id: id, Payload: payload}
	}
	return nil
}

func (g *rotatingGmail) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/gmail/v1/users/me/messages/"), "/")
	notFound := func() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":404,"message":"Requested entity was not found."}}`))
	}
	switch {
	case len(parts) == 1:
		g.gets++
		msg := g.message(parts[0])
		if msg == nil {
			notFound()
			return
		}
		_ = json.NewEncoder(w).Encode(msg)
	case len(parts) == 3 && parts[1] == "attachments":
		g.asked = append(g.asked, parts[2])
		data, ok := g.issued[parts[2]]
		if !ok {
			notFound()
			return
		}
		_ = json.NewEncoder(w).Encode(&gmailapi.MessagePartBody{Data: base64.URLEncoding.EncodeToString(data), Size: int64(len(data))})
	default:
		notFound()
	}
}

// gmailHandler is a Handler whose Gmail is g, and a client for it.
func gmailHandler(t *testing.T, g *rotatingGmail) (*Handler, *gmailapi.Service) {
	t.Helper()
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	svc := gmail.NewService("test-client", "test-secret", "http://localhost/auth/callback").WithEndpoint(srv.URL + "/")
	client := svc.GetClient(&oauth2.Token{AccessToken: "test", Expiry: time.Now().Add(time.Hour)})
	return &Handler{gmailService: svc}, client
}

// What the reader shows of a Gmail message: its files, each under the id
// the screen sends back.
func readerIDs(t *testing.T, h *Handler, client *gmailapi.Service, messageID string) map[string]attachmentView {
	t.Helper()
	msg, err := h.gmailService.GetMessage(client, messageID)
	if err != nil {
		t.Fatalf("GetMessage(%s): %v", messageID, err)
	}
	_, html := gmail.GetEmailBodies(msg)
	out := map[string]attachmentView{}
	for _, a := range readGmailParts(msg, html).files {
		out[a.Filename] = a
	}
	return out
}

// The reader names a file by its part number, which a later messages.get
// gives it again; the download asks Gmail for the bytes under the id the
// fresh copy gives, whatever id the reader's copy had.
func TestGmailDownloadNamesAFileByItsPartNumber(t *testing.T) {
	g := &rotatingGmail{rotate: true}
	h, client := gmailHandler(t, g)

	shown := readerIDs(t, h, client, "facture")
	pdf, ok := shown["facture.pdf"]
	if !ok || pdf.AttachmentID != "1" {
		t.Fatalf("the reader shows facture.pdf as %+v, want it under its part number \"1\"", pdf)
	}
	if again := readerIDs(t, h, client, "facture")["facture.pdf"]; again.AttachmentID != pdf.AttachmentID || again.gmailID == pdf.gmailID {
		t.Fatalf("a second read gives facture.pdf %q (Gmail id %q), the first %q (%q): want the same part number under a new Gmail id", again.AttachmentID, again.gmailID, pdf.AttachmentID, pdf.gmailID)
	}

	rec := httptest.NewRecorder()
	h.downloadAttachmentGmail(rec, client, "moi@example.com", "facture", pdf.AttachmentID)
	if rec.Code != http.StatusOK || rec.Body.String() != string(gmailInvoicePDF) {
		t.Fatalf("download of part 1 = %d %q, want 200 and the PDF", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, `filename="facture.pdf"`) {
		t.Errorf("download of part 1 is named %q, want facture.pdf", got)
	}
	if last := g.asked[len(g.asked)-1]; last == pdf.gmailID {
		t.Errorf("attachments.get was asked for %q, the id of the reader's copy, want the fresh one", last)
	}

	// A single-part message that is a file: the payload, which has no part
	// number of its own.
	scan := readerIDs(t, h, client, "scan")["scan.pdf"]
	if scan.AttachmentID != gmailPayloadPart {
		t.Fatalf("the reader shows scan.pdf under %q, want %q", scan.AttachmentID, gmailPayloadPart)
	}
	rec = httptest.NewRecorder()
	h.downloadAttachmentGmail(rec, client, "moi@example.com", "scan", scan.AttachmentID)
	if rec.Code != http.StatusOK || rec.Body.String() != string(gmailScanPDF) {
		t.Errorf("download of the payload = %d %q, want 200 and the scan", rec.Code, rec.Body.String())
	}
}

// A screen opened before part numbers were handed out sends Gmail's id: it
// is served while the fresh copy still gives a file that id, and refused
// once it does not, rather than forwarded to Gmail.
func TestGmailDownloadOfAnOldAttachmentID(t *testing.T) {
	stable := &rotatingGmail{rotate: false}
	h, client := gmailHandler(t, stable)
	old := readerIDs(t, h, client, "facture")["facture.pdf"].gmailID
	rec := httptest.NewRecorder()
	h.downloadAttachmentGmail(rec, client, "moi@example.com", "facture", old)
	if rec.Code != http.StatusOK || rec.Body.String() != string(gmailInvoicePDF) {
		t.Errorf("download under Gmail's id, still given = %d %q, want 200 and the PDF", rec.Code, rec.Body.String())
	}

	rotating := &rotatingGmail{rotate: true}
	h, client = gmailHandler(t, rotating)
	old = readerIDs(t, h, client, "facture")["facture.pdf"].gmailID
	rec = httptest.NewRecorder()
	h.downloadAttachmentGmail(rec, client, "moi@example.com", "facture", old)
	if rec.Code != http.StatusNotFound || len(rotating.asked) != 0 {
		t.Errorf("download under Gmail's id, no longer given = %d (attachments.get asked %v), want 404 and nothing asked of Gmail", rec.Code, rotating.asked)
	}
}

// What is not a file of the message is not asked of Gmail at all, and
// neither is a file over the cap; a message Gmail no longer has says so.
func TestGmailDownloadRefusals(t *testing.T) {
	g := &rotatingGmail{rotate: true}
	h, client := gmailHandler(t, g)
	cases := []struct {
		name, message, id string
		code              int
		said              string
	}{
		{"the text of the mail", "facture", "0", http.StatusNotFound, attachmentMissingMessage},
		{"a part it does not have", "facture", "9", http.StatusNotFound, attachmentMissingMessage},
		{"the payload of a multipart", "facture", gmailPayloadPart, http.StatusNotFound, attachmentMissingMessage},
		{"a file over the cap", "facture", "2", http.StatusRequestEntityTooLarge, "Pièce jointe trop volumineuse"},
		{"a mail deleted since", "efface", "1", http.StatusNotFound, attachmentGoneMessage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g.asked = nil
			rec := httptest.NewRecorder()
			h.downloadAttachmentGmail(rec, client, "moi@example.com", tc.message, tc.id)
			var body struct {
				Error string `json:"error"`
			}
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			if rec.Code != tc.code || body.Error != tc.said {
				t.Errorf("download %q of %q = %d %q, want %d %q", tc.id, tc.message, rec.Code, body.Error, tc.code, tc.said)
			}
			if len(g.asked) != 0 {
				t.Errorf("download %q of %q asked Gmail for %v, want nothing", tc.id, tc.message, g.asked)
			}
		})
	}
}

// The pictures a Gmail newsletter's HTML draws through cid: are served to the
// screen under the ids the reader gives them, from Gmail or from the message
// when Gmail sent them with it; a picture the HTML does not point at is not.
func TestGmailDownloadServesThePicturesTheHTMLDraws(t *testing.T) {
	g := &rotatingGmail{rotate: true}
	h, client := gmailHandler(t, g)

	msg, err := h.gmailService.GetMessage(client, "newsletter")
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	_, html := gmail.GetEmailBodies(msg)
	parts := readGmailParts(msg, html)
	if len(parts.files) != 0 {
		t.Errorf("the reader lists %+v, want no file: the pictures are drawn", parts.files)
	}
	want := map[string]string{"logo@acme": "1", "petit@acme": "3"}
	if got := parts.inlineImages(); !reflect.DeepEqual(got, want) {
		t.Fatalf("inline images = %v, want %v", got, want)
	}

	for id, data := range map[string][]byte{"1": gmailLogoPNG, "3": gmailDotPNG} {
		g.asked = nil
		rec := httptest.NewRecorder()
		h.downloadAttachmentGmail(rec, client, "moi@example.com", "newsletter", id)
		if rec.Code != http.StatusOK || rec.Body.String() != string(data) || rec.Header().Get("Content-Type") != "image/png" {
			t.Errorf("download %q = %d %q (%s), want 200 and the picture", id, rec.Code, rec.Body.String(), rec.Header().Get("Content-Type"))
		}
		if id == "3" && len(g.asked) != 0 {
			t.Errorf("download of a picture sent with the message asked Gmail for %v", g.asked)
		}
	}

	g.asked = nil
	rec := httptest.NewRecorder()
	h.downloadAttachmentGmail(rec, client, "moi@example.com", "newsletter", "2")
	if rec.Code != http.StatusNotFound || len(g.asked) != 0 || !strings.Contains(rec.Body.String(), attachmentMissingMessage) {
		t.Errorf("download of a picture the HTML does not point at = %d %q (asked %v), want 404 %q and nothing asked", rec.Code, rec.Body.String(), g.asked, attachmentMissingMessage)
	}
}

// The screen draws the first attachment.MaxDrawn pictures of the HTML, in its
// order: the reader hands out those, and the download serves no other,
// without asking Gmail.
func TestGmailDrawsOnlyTheFirstPictures(t *testing.T) {
	g := &rotatingGmail{rotate: true}
	h, client := gmailHandler(t, g)
	msg, err := h.gmailService.GetMessage(client, "thread")
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	_, html := gmail.GetEmailBodies(msg)
	images := readGmailParts(msg, html).inlineImages()
	n := attachment.MaxDrawn + 1
	if len(images) != attachment.MaxDrawn {
		t.Fatalf("inline images = %d pictures, want %d", len(images), attachment.MaxDrawn)
	}
	if _, ok := images[fmt.Sprintf("img%d@x", n)]; ok {
		t.Errorf("inline images hold the %dth picture of the HTML", n)
	}
	thirtieth := fmt.Sprint(n - 1)
	if images[fmt.Sprintf("img%d@x", n-1)] != thirtieth {
		t.Errorf("inline images = %v, want the %dth picture under %s", images, n-1, thirtieth)
	}

	rec := httptest.NewRecorder()
	h.downloadAttachmentGmail(rec, client, "moi@example.com", "thread", thirtieth)
	if rec.Code != http.StatusOK || rec.Body.String() != "PNG"+thirtieth {
		t.Errorf("download of the %dth picture = %d %q, want 200 and it", n-1, rec.Code, rec.Body.String())
	}
	g.asked = nil
	rec = httptest.NewRecorder()
	h.downloadAttachmentGmail(rec, client, "moi@example.com", "thread", fmt.Sprint(n))
	if rec.Code != http.StatusNotFound || len(g.asked) != 0 {
		t.Errorf("download of the %dth picture = %d (asked Gmail %v), want 404 and nothing asked", n, rec.Code, g.asked)
	}
}
