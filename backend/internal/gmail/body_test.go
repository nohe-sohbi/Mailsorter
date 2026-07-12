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
