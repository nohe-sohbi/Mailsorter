package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gmailapi "google.golang.org/api/gmail/v1"
)

func TestSafeAttachmentName(t *testing.T) {
	cases := map[string]string{
		"facture.pdf":            "facture.pdf",
		"  facture.pdf  ":        "facture.pdf",
		"../../../etc/passwd":    "passwd",
		`C:\Users\bob\rapport.x`: "rapport.x",
		"quo\"te.txt":            "quote.txt",
		"line\r\nbreak.txt":      "linebreak.txt",
		"":                       fallbackAttachmentName,
		"..":                     fallbackAttachmentName,
		"/":                      fallbackAttachmentName,
		"reçu-été.pdf":           "reçu-été.pdf",
	}
	for in, want := range cases {
		if got := safeAttachmentName(in); got != want {
			t.Errorf("safeAttachmentName(%q) = %q, want %q", in, got, want)
		}
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

// listAttachments must publish the attachment id (the handle the bytes live
// behind) and must keep ignoring the inline images a newsletter references.
func TestListAttachmentsExposesAttachmentID(t *testing.T) {
	msg := &gmailapi.Message{Payload: &gmailapi.MessagePart{
		MimeType: "multipart/mixed",
		Parts: []*gmailapi.MessagePart{
			{
				Filename: "facture.pdf",
				MimeType: "application/pdf",
				Body:     &gmailapi.MessagePartBody{AttachmentId: "att-1", Size: 2048},
			},
			{
				Filename: "logo.png",
				MimeType: "image/png",
				Headers:  []*gmailapi.MessagePartHeader{{Name: "Content-ID", Value: "<logo>"}},
				Body:     &gmailapi.MessagePartBody{AttachmentId: "att-2", Size: 10},
			},
		},
	}}

	got := listAttachments(msg)
	if len(got) != 1 {
		t.Fatalf("listAttachments returned %d parts, want only the real attachment: %#v", len(got), got)
	}
	if got[0].AttachmentID != "att-1" || got[0].Size != 2048 {
		t.Errorf("attachment = %#v, want id att-1 and size 2048", got[0])
	}

	if _, ok := findAttachment(got, "att-2"); ok {
		t.Error("an inline part must not be downloadable through the attachment route")
	}
	if _, ok := findAttachment(got, "att-1"); !ok {
		t.Error("findAttachment did not resolve the attachment it just listed")
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
