package gmail

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/egress"
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

// The one-click POST is the only place in Mailsorter where a URL chosen by a
// stranger reaches the server's own HTTP client, so these are the tests that
// say what it refuses. Each one failed before internal/egress existed: the old
// check looked at the scheme and nothing else, so every address below was
// requested by the server, from inside the deployment's own network.

// A name that resolves to loopback is the SSRF in its plainest form: the scheme
// is https and the host looks ordinary, and the request lands on the app's own
// API. The verdict has to come from the dialer, on the resolved address.
func TestOneClickUnsubscribeRefusesAResolvedLoopbackHost(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits++
	}))
	defer srv.Close()

	_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("test server address: %v", err)
	}

	// localhost resolves to 127.0.0.1, so the URL passes every check that looks
	// only at the text and fails the one that looks at the address.
	err = (&Service{}).OneClickUnsubscribe("https://localhost:" + port + "/unsub")
	if !errors.Is(err, egress.ErrPrivateAddress) {
		t.Errorf("OneClickUnsubscribe(https://localhost:%s/unsub) = %v, want egress.ErrPrivateAddress", port, err)
	}
	if hits != 0 {
		t.Errorf("the endpoint was reached %d time(s); the request must never leave", hits)
	}
}

func TestOneClickUnsubscribeRefusesTheAddressesThatMatter(t *testing.T) {
	cases := map[string]string{
		"https://127.0.0.1:8080/api/metrics":        "the app's own API on loopback",
		"https://10.0.0.5:27017/":                   "the datastore on the Docker bridge",
		"https://169.254.169.254/latest/meta-data/": "the cloud metadata endpoint that hands out credentials",
		"https://[::1]/unsub":                       "IPv6 loopback",
		"https://192.168.1.1/":                      "the local network",
	}
	for raw, why := range cases {
		if err := (&Service{}).OneClickUnsubscribe(raw); !errors.Is(err, egress.ErrPrivateAddress) {
			t.Errorf("OneClickUnsubscribe(%q) = %v, want egress.ErrPrivateAddress (%s)", raw, err, why)
		}
	}
}

// http is refused before any connection: RFC 8058 requires https, and the old
// code accepting it is what put every http-only internal service in reach.
func TestOneClickUnsubscribeRefusesCleartext(t *testing.T) {
	for _, raw := range []string{"http://example.com/unsub", "ftp://example.com/unsub"} {
		if err := (&Service{}).OneClickUnsubscribe(raw); !errors.Is(err, egress.ErrNotHTTPS) {
			t.Errorf("OneClickUnsubscribe(%q) = %v, want egress.ErrNotHTTPS", raw, err)
		}
	}
}

// The request the sender receives has to be the one RFC 8058 describes, or the
// user is not unsubscribed no matter how well the address was checked. Built
// without a network so the shape is pinned on its own.
func TestNewUnsubscribeRequestIsTheRFC8058POST(t *testing.T) {
	req, err := newUnsubscribeRequest("https://newsletter.example.com/unsub?id=abc")
	if err != nil {
		t.Fatalf("newUnsubscribeRequest: %v", err)
	}
	if req.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", req.Method)
	}
	if got := req.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q, want application/x-www-form-urlencoded", got)
	}
	if got := req.Header.Get("User-Agent"); got == "" {
		t.Error("User-Agent is empty; senders use it to tell a real unsubscribe from a crawler")
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "List-Unsubscribe=One-Click" {
		t.Errorf("body = %q, want %q", body, "List-Unsubscribe=One-Click")
	}
}

// Every hop is judged, not just the one the header carried: a 302 is how an
// endpoint that looks public reaches something that is not.
func TestCheckUnsubscribeRedirect(t *testing.T) {
	hop := func(raw string) *http.Request {
		req, err := http.NewRequest(http.MethodPost, raw, nil)
		if err != nil {
			t.Fatalf("test data %q: %v", raw, err)
		}
		return req
	}

	if err := checkUnsubscribeRedirect(hop("https://example.com/step2"), []*http.Request{hop("https://example.com/step1")}); err != nil {
		t.Errorf("a public https hop was refused: %v", err)
	}
	if err := checkUnsubscribeRedirect(hop("https://127.0.0.1/admin"), nil); !errors.Is(err, egress.ErrPrivateAddress) {
		t.Errorf("redirect to loopback = %v, want egress.ErrPrivateAddress", err)
	}
	if err := checkUnsubscribeRedirect(hop("http://example.com/step2"), nil); !errors.Is(err, egress.ErrNotHTTPS) {
		t.Errorf("redirect downgraded to http = %v, want egress.ErrNotHTTPS", err)
	}

	var via []*http.Request
	for i := 0; i < maxUnsubscribeRedirects; i++ {
		via = append(via, hop("https://example.com/step"))
	}
	if err := checkUnsubscribeRedirect(hop("https://example.com/again"), via); err == nil {
		t.Errorf("a chain of %d redirects was allowed to continue; want it stopped", maxUnsubscribeRedirects)
	}
}
