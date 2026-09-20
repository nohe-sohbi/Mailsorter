package imap

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
)

// A real multipart/alternative, the shape almost every real sender produces:
// the same content twice, text first, HTML second.
const alternativeMessage = "From: Acme <news@acme.com>\r\n" +
	"To: " + testUser + "\r\n" +
	"Subject: Votre facture\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/alternative; boundary=\"sep\"\r\n" +
	"\r\n" +
	"--sep\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"\r\n" +
	"Bonjour, voici votre facture.\r\n" +
	"--sep\r\n" +
	"Content-Type: text/html; charset=utf-8\r\n" +
	"\r\n" +
	"<html><body><p>Bonjour, voici votre facture.</p></body></html>\r\n" +
	"--sep--\r\n"

// The route this exists for: a reader opens a message and needs the body the
// listing deliberately did not download.
func TestFetchReturnsBothRenderingsOfTheBody(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	appendRaw(t, c, "INBOX", alternativeMessage)

	emails, err := c.List(context.Background(), "INBOX", 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	uid := emails[0].MessageID

	msg, err := c.Fetch(context.Background(), "INBOX", uid)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !strings.Contains(msg.Body, "voici votre facture") {
		t.Errorf("Body = %q, want the text part", msg.Body)
	}
	if !strings.Contains(msg.HTML, "<p>") {
		t.Errorf("HTML = %q, want the html part with its markup intact", msg.HTML)
	}
	if msg.Subject != "Votre facture" {
		t.Errorf("Subject = %q, want the envelope carried over like the listing does", msg.Subject)
	}
	// Half of the message's identity on this transport. A Message that came back
	// without it cannot be acted on afterwards.
	if msg.Folder != "INBOX" {
		t.Errorf("Folder = %q, want INBOX", msg.Folder)
	}
	if msg.Snippet == "" {
		t.Error("Snippet is empty; IMAP hands none over, so it is derived here or the list shows a blank line")
	}
}

// The one that would be discovered in production, and the reason the fetch
// spells out Peek. Opening a message is not the same act as reading it: the
// reader decides, with markRead. A fetch that set \Seen on its own would make a
// preview, or a mis-click, indistinguishable from reading, and nothing in the
// ledger would explain the unread count falling.
func TestFetchDoesNotMarkTheMessageRead(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	appendRaw(t, c, "INBOX", alternativeMessage)

	emails, err := c.List(context.Background(), "INBOX", 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	uid := emails[0].MessageID

	if _, err := c.Fetch(context.Background(), "INBOX", uid); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	// Read back off the server, not off the struct the fetch just built.
	if flags := flagsOf(t, c, "INBOX", uid); has(flags, mailbox.FlagSeen) {
		t.Errorf("after Fetch, the message carries %s on the server: the fetch must peek", mailbox.FlagSeen)
	}
}

// Fetch is folder-scoped like everything else on this transport. Asking for a
// UID in the wrong folder must not silently answer with whatever message holds
// that number there.
func TestFetchRefusesWhatItCannotName(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	appendRaw(t, c, "INBOX", alternativeMessage)

	// A Gmail message id, which is digits followed by letters. parseUID reads
	// the whole string or refuses; a parser that stopped at the first letter
	// would fetch UID 18 and hand back a different message entirely.
	if _, err := c.Fetch(context.Background(), "INBOX", "18c8c1f2a3b4d5e6"); err == nil {
		t.Error("Fetch accepted a Gmail message id as a UID")
	}
	if _, err := c.Fetch(context.Background(), "Nowhere", "1"); err == nil {
		t.Error("Fetch accepted a folder that does not exist")
	}
	if _, err := c.Fetch(context.Background(), "INBOX", "99999"); err == nil {
		t.Error("Fetch of a UID nothing holds returned no error")
	}
}

// Marketing mail routinely ships HTML and nothing else. Reading only the plain
// part would render those messages blank, which is most of what a triage app
// looks at.
func TestFetchOnAnHTMLOnlyMessage(t *testing.T) {
	c := newTestServer(t, standardMailboxes())
	raw := "From: news@acme.com\r\n" +
		"To: " + testUser + "\r\n" +
		"Subject: Promo\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"\r\n" +
		"<html><body><h1>Soldes</h1><p>Jusqu'a -50%</p></body></html>\r\n"
	appendRaw(t, c, "INBOX", raw)

	emails, err := c.List(context.Background(), "INBOX", 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	msg, err := c.Fetch(context.Background(), "INBOX", emails[0].MessageID)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !strings.Contains(msg.HTML, "Soldes") {
		t.Errorf("HTML = %q, want the html body", msg.HTML)
	}
	// And the preview still says something, taken from the markup.
	if !strings.Contains(msg.Snippet, "Soldes") {
		t.Errorf("Snippet = %q, want it derived from the HTML when there is no text part", msg.Snippet)
	}
}

func TestBodies(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		wantPlain string
		wantHTML  string
	}{
		{
			name:      "both parts",
			raw:       alternativeMessage,
			wantPlain: "Bonjour, voici votre facture.",
			wantHTML:  "<html><body><p>Bonjour, voici votre facture.</p></body></html>",
		},
		{
			name: "plain only",
			raw: "From: a@b.com\r\nSubject: S\r\nContent-Type: text/plain\r\n\r\n" +
				"Juste du texte.\r\n",
			wantPlain: "Juste du texte.",
			wantHTML:  "",
		},
		{
			name: "html only",
			raw: "From: a@b.com\r\nSubject: S\r\nContent-Type: text/html\r\n\r\n" +
				"<p>Juste du html.</p>\r\n",
			wantPlain: "",
			wantHTML:  "<p>Juste du html.</p>",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plain, html := bodies([]byte(tc.raw))
			if strings.TrimSpace(plain) != tc.wantPlain {
				t.Errorf("bodies(...) plain = %q, want %q", strings.TrimSpace(plain), tc.wantPlain)
			}
			if strings.TrimSpace(html) != tc.wantHTML {
				t.Errorf("bodies(...) html = %q, want %q", strings.TrimSpace(html), tc.wantHTML)
			}
		})
	}
}

// Not every sender produces something the MIME parser accepts, and a message
// that cannot be walked is still worth showing as text. Returning an error
// instead would turn one malformed sender into an unopenable message.
func TestBodiesFallsBackToTheRawPayload(t *testing.T) {
	plain, html := bodies([]byte("this is not a message at all"))
	if plain == "" {
		t.Error("an unparseable payload produced no text at all; the raw bytes are the last resort")
	}
	if html != "" {
		t.Errorf("html = %q, want empty: nothing said this was markup", html)
	}
}

// An attachment's bytes are not the body. Treating them as one would paste a
// base64 blob into the reader.
func TestBodiesIgnoresAttachments(t *testing.T) {
	raw := "From: a@b.com\r\n" +
		"To: " + testUser + "\r\n" +
		"Subject: Avec piece jointe\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"sep\"\r\n" +
		"\r\n" +
		"--sep\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"Voici le document.\r\n" +
		"--sep\r\n" +
		"Content-Type: application/pdf\r\n" +
		"Content-Disposition: attachment; filename=\"facture.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"\r\n" +
		"JVBERi0xLjQKJSVFT0Y=\r\n" +
		"--sep--\r\n"

	plain, html := bodies([]byte(raw))
	if strings.TrimSpace(plain) != "Voici le document." {
		t.Errorf("plain = %q, want only the text part", strings.TrimSpace(plain))
	}
	if strings.Contains(plain, "JVBERi") || strings.Contains(html, "JVBERi") {
		t.Error("the attachment's bytes landed in the body")
	}
}

func TestSnippet(t *testing.T) {
	cases := []struct {
		name  string
		plain string
		html  string
		want  string
	}{
		{"prefers the text", "Bonjour.", "<p>Autre</p>", "Bonjour."},
		{"falls back to the markup", "", "<p>Bonjour</p>", "Bonjour"},
		{"collapses whitespace", "Un\r\n\tdeux   trois\n", "", "Un deux trois"},
		{"entities are read, not shown", "", "<p>Caf&amp;bar</p>", "Caf&bar"},
		{"nothing at all", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := snippet(tc.plain, tc.html); got != tc.want {
				t.Errorf("snippet(%q, %q) = %q, want %q", tc.plain, tc.html, got, tc.want)
			}
		})
	}
}

// The preview is cut to a length, and the cut is counted in runes. French prose
// is full of multi-byte characters, so a byte-indexed slice lands inside one
// often enough that the preview would ship invalid UTF-8, which the user sees
// as a replacement character at the end of every long preview.
func TestSnippetCutsOnARuneBoundary(t *testing.T) {
	// One ASCII character then nothing but two-byte runes, so byte 200 lands in
	// the MIDDLE of one. A byte-indexed cut there does not merely shorten the
	// preview, it produces a broken rune.
	long := "e" + strings.Repeat("\u00e9", 300)
	got := snippet(long, "")

	if !utf8.ValidString(got) {
		t.Errorf("snippet produced invalid UTF-8: %q", got)
	}
	if n := utf8.RuneCountInString(got); n > 200 {
		t.Errorf("snippet kept %d runes, want at most 200", n)
	}
	if n := utf8.RuneCountInString(got); n < 190 {
		t.Errorf("snippet kept only %d runes of a long text, want it cut near the limit", n)
	}
	// A short text is returned whole, not padded or cut.
	if got := snippet("Court.", ""); got != "Court." {
		t.Errorf("snippet(%q) = %q, want it unchanged", "Court.", got)
	}
}

func TestStripTags(t *testing.T) {
	cases := map[string]string{
		"<p>Bonjour</p>":                     "Bonjour",
		"<a href=\"http://x\">lien</a>":      "lien",
		"sans balise":                        "sans balise",
		"<div><span>imbrique</span></div>":   "imbrique",
		"&lt;pas une balise&gt;":             "<pas une balise>",
		"a &amp; b":                          "a & b",
		"insecable&nbsp;espace":              "insecable espace",
		"<script>alert('x')</script>Bonjour": "alert('x')Bonjour",
	}
	for in, want := range cases {
		if got := stripTags(in); got != want {
			t.Errorf("stripTags(%q) = %q, want %q", in, got, want)
		}
	}
}
