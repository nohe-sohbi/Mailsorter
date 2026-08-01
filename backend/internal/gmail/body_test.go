package gmail

import (
	"encoding/base64"
	"testing"

	gmailapi "google.golang.org/api/gmail/v1"
)

func TestGetEmailBodyDecodesTopLevel(t *testing.T) {
	plain := "Cliquez ici pour vous désabonner (unsubscribe)."
	encoded := base64.URLEncoding.EncodeToString([]byte(plain))
	m := &gmailapi.Message{Payload: &gmailapi.MessagePart{
		Body: &gmailapi.MessagePartBody{Data: encoded},
	}}
	if got := GetEmailBody(m); got != plain {
		t.Fatalf("GetEmailBody = %q, want %q", got, plain)
	}
}

func TestGetEmailBodyDecodesPartWithoutPadding(t *testing.T) {
	plain := "hello world body content"
	// Gmail frequently returns URL-safe base64 without padding.
	encoded := base64.RawURLEncoding.EncodeToString([]byte(plain))
	m := &gmailapi.Message{Payload: &gmailapi.MessagePart{
		// Multipart messages carry an empty top-level Body plus child parts,
		// mirroring what the Gmail API returns.
		Body: &gmailapi.MessagePartBody{},
		Parts: []*gmailapi.MessagePart{{
			MimeType: "text/plain",
			Body:     &gmailapi.MessagePartBody{Data: encoded},
		}},
	}}
	if got := GetEmailBody(m); got != plain {
		t.Fatalf("GetEmailBody = %q, want %q", got, plain)
	}
}

func TestGetEmailBodyEmpty(t *testing.T) {
	m := &gmailapi.Message{Payload: &gmailapi.MessagePart{Body: &gmailapi.MessagePartBody{}}}
	if got := GetEmailBody(m); got != "" {
		t.Fatalf("GetEmailBody = %q, want empty", got)
	}
}

func enc(s string) string { return base64.URLEncoding.EncodeToString([]byte(s)) }

// The shape almost every newsletter actually has: multipart/mixed wrapping a
// multipart/alternative wrapping the text parts. A single-level scan sees only
// the multipart/alternative container and reports an empty body, which is what
// made rules on `body` and the reader come up blank.
func TestGetEmailBodiesWalksNestedMultipart(t *testing.T) {
	m := &gmailapi.Message{Payload: &gmailapi.MessagePart{
		MimeType: "multipart/mixed",
		Body:     &gmailapi.MessagePartBody{},
		Parts: []*gmailapi.MessagePart{{
			MimeType: "multipart/alternative",
			Body:     &gmailapi.MessagePartBody{},
			Parts: []*gmailapi.MessagePart{
				{MimeType: "text/plain", Body: &gmailapi.MessagePartBody{Data: enc("version texte")}},
				{MimeType: "text/html", Body: &gmailapi.MessagePartBody{Data: enc("<p>version html</p>")}},
			},
		}},
	}}

	plain, html := GetEmailBodies(m)
	if plain != "version texte" {
		t.Errorf("plain = %q, want %q", plain, "version texte")
	}
	if html != "<p>version html</p>" {
		t.Errorf("html = %q, want %q", html, "<p>version html</p>")
	}
	// The rule engine matches on plain text, so that is what GetEmailBody yields.
	if got := GetEmailBody(m); got != "version texte" {
		t.Errorf("GetEmailBody = %q, want the plain-text part", got)
	}
}

// An HTML-only message must still produce a body rather than nothing.
func TestGetEmailBodyFallsBackToHTML(t *testing.T) {
	m := &gmailapi.Message{Payload: &gmailapi.MessagePart{
		MimeType: "multipart/alternative",
		Body:     &gmailapi.MessagePartBody{},
		Parts: []*gmailapi.MessagePart{
			{MimeType: "text/html; charset=UTF-8", Body: &gmailapi.MessagePartBody{Data: enc("<b>que du html</b>")}},
		},
	}}
	if got := GetEmailBody(m); got != "<b>que du html</b>" {
		t.Fatalf("GetEmailBody = %q, want the HTML part", got)
	}
}

// A text attachment must never be mistaken for the message body.
func TestGetEmailBodiesSkipsAttachments(t *testing.T) {
	m := &gmailapi.Message{Payload: &gmailapi.MessagePart{
		MimeType: "multipart/mixed",
		Body:     &gmailapi.MessagePartBody{},
		Parts: []*gmailapi.MessagePart{
			{MimeType: "text/plain", Filename: "facture.txt", Body: &gmailapi.MessagePartBody{Data: enc("PIECE JOINTE")}},
			{MimeType: "text/plain", Body: &gmailapi.MessagePartBody{Data: enc("le vrai corps")}},
		},
	}}
	plain, _ := GetEmailBodies(m)
	if plain != "le vrai corps" {
		t.Fatalf("plain = %q, want the non-attachment part", plain)
	}
}

// A cyclic or absurdly deep tree must terminate rather than blow the stack.
func TestGetEmailBodiesIsDepthBounded(t *testing.T) {
	deepest := &gmailapi.MessagePart{
		MimeType: "text/plain",
		Body:     &gmailapi.MessagePartBody{Data: enc("trop profond")},
	}
	node := deepest
	for i := 0; i < maxMIMEDepth+5; i++ {
		node = &gmailapi.MessagePart{
			MimeType: "multipart/mixed",
			Body:     &gmailapi.MessagePartBody{},
			Parts:    []*gmailapi.MessagePart{node},
		}
	}
	plain, _ := GetEmailBodies(&gmailapi.Message{Payload: node})
	if plain != "" {
		t.Fatalf("plain = %q, want empty: the walk must stop at maxMIMEDepth", plain)
	}
}
