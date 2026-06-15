package gmail

import (
	"testing"

	gmailapi "google.golang.org/api/gmail/v1"
)

func TestSplitAngleList(t *testing.T) {
	in := "<https://x.com/u>, <mailto:u@x.com>"
	got := splitAngleList(in)
	if len(got) != 2 || got[0] != "https://x.com/u" || got[1] != "mailto:u@x.com" {
		t.Fatalf("splitAngleList parsed %#v", got)
	}
	if len(splitAngleList("")) != 0 {
		t.Fatal("empty input should yield no entries")
	}
}

func msg(headers map[string]string) *gmailapi.Message {
	m := &gmailapi.Message{Payload: &gmailapi.MessagePart{}}
	for name, value := range headers {
		m.Payload.Headers = append(m.Payload.Headers, &gmailapi.MessagePartHeader{Name: name, Value: value})
	}
	return m
}

func TestParseUnsubscribeOneClick(t *testing.T) {
	m := msg(map[string]string{
		"List-Unsubscribe":      "<https://x.com/u>, <mailto:u@x.com>",
		"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
	})
	url, mailto, oneClick := ParseUnsubscribe(m)
	if url != "https://x.com/u" {
		t.Errorf("url = %q", url)
	}
	if mailto != "mailto:u@x.com" {
		t.Errorf("mailto = %q", mailto)
	}
	if !oneClick {
		t.Error("expected oneClick=true when List-Unsubscribe-Post advertises One-Click")
	}
}

func TestParseUnsubscribeMailtoOnly(t *testing.T) {
	m := msg(map[string]string{"List-Unsubscribe": "<mailto:u@x.com>"})
	url, mailto, oneClick := ParseUnsubscribe(m)
	if url != "" {
		t.Errorf("expected no http url, got %q", url)
	}
	if mailto != "mailto:u@x.com" {
		t.Errorf("mailto = %q", mailto)
	}
	if oneClick {
		t.Error("oneClick must be false without an https endpoint")
	}
}

func TestParseUnsubscribeNoHeaders(t *testing.T) {
	url, mailto, oneClick := ParseUnsubscribe(msg(nil))
	if url != "" || mailto != "" || oneClick {
		t.Errorf("expected empty result, got url=%q mailto=%q oneClick=%v", url, mailto, oneClick)
	}
	// nil message must not panic.
	if _, _, oc := ParseUnsubscribe(nil); oc {
		t.Error("nil message should report oneClick=false")
	}
}
