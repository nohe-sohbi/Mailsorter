package attachment

import "testing"

// The HTML of a newsletter, showing its logo and its banner through cid:
// URLs, the ways mail programs write them.
const newsletterHTML = `<html><body style="background: url(cid:fond@acme)">` +
	`<img src="cid:logo@acme" alt="Acme"><img src='CID:Banniere%40acme'>` +
	`<img src=cid:pied@acme></body></html>`

func TestListed(t *testing.T) {
	shown := ShownBy(newsletterHTML)
	cases := []struct {
		name     string
		part     Part
		wantOK   bool
		wantName string
	}{
		{
			name: "the text of the mail has no name",
			part: Part{MediaType: "text/plain"},
		},
		{
			name: "its HTML neither",
			part: Part{MediaType: "text/html", Disposition: "inline"},
		},
		{
			name:     "a named file",
			part:     Part{Filename: "facture.pdf", MediaType: "application/pdf", Disposition: "attachment"},
			wantOK:   true,
			wantName: "facture.pdf",
		},
		{
			name:     "a name from Content-Type alone, as older programs write it",
			part:     Part{Filename: "facture.pdf", MediaType: "application/pdf"},
			wantOK:   true,
			wantName: "facture.pdf",
		},
		{
			name:     "a file attached from Gmail's web interface carries a Content-ID",
			part:     Part{Filename: "facture.pdf", MediaType: "application/pdf", Disposition: `attachment; filename="facture.pdf"`, ContentID: "<f_abc123>"},
			wantOK:   true,
			wantName: "facture.pdf",
		},
		{
			name:     "Apple Mail sends a PDF inline, without a Content-ID",
			part:     Part{Filename: "facture.pdf", MediaType: "application/pdf", Disposition: "inline"},
			wantOK:   true,
			wantName: "facture.pdf",
		},
		{
			name:     "a PDF with a Content-ID the HTML points at is not an image the HTML shows",
			part:     Part{Filename: "facture.pdf", MediaType: "application/pdf", Disposition: "inline", ContentID: "<logo@acme>"},
			wantOK:   true,
			wantName: "facture.pdf",
		},
		{
			name: "an image the HTML shows",
			part: Part{Filename: "logo.png", MediaType: "image/png", Disposition: "inline", ContentID: "<logo@acme>"},
		},
		{
			name: "an image the HTML shows, without a disposition",
			part: Part{Filename: "logo.png", MediaType: "image/png", ContentID: "<logo@acme>"},
		},
		{
			name: "an image the HTML shows, declared an attachment",
			part: Part{Filename: "pied.png", MediaType: "image/png", Disposition: "attachment", ContentID: "<pied@acme>"},
		},
		{
			name: "an image the HTML shows as a CSS background",
			part: Part{Filename: "fond.jpg", MediaType: "image/jpeg", ContentID: "<fond@acme>"},
		},
		{
			name: "an image the HTML shows, its reference %-escaped and in another case",
			part: Part{Filename: "banniere.png", MediaType: "IMAGE/PNG", ContentID: "<banniere@acme>"},
		},
		{
			name: "an unnamed image the HTML shows",
			part: Part{MediaType: "image/gif", Disposition: "attachment", ContentID: "<logo@acme>"},
		},
		{
			name:     "an image with a Content-ID the HTML does not show",
			part:     Part{Filename: "photo.jpg", MediaType: "image/jpeg", Disposition: "inline", ContentID: "<photo@iphone>"},
			wantOK:   true,
			wantName: "photo.jpg",
		},
		{
			name:     "an unnamed attachment",
			part:     Part{MediaType: "application/pdf", Disposition: "attachment"},
			wantOK:   true,
			wantName: Unnamed,
		},
		{
			name:     "an unnamed attachment whose disposition has parameters",
			part:     Part{MediaType: "application/octet-stream", Disposition: "Attachment; size=12"},
			wantOK:   true,
			wantName: Unnamed,
		},
		{
			name:     "an attachment whose name cleans away to nothing",
			part:     Part{Filename: "../", MediaType: "application/pdf", Disposition: "attachment"},
			wantOK:   true,
			wantName: Unnamed,
		},
		{
			name:     "an unnamed forwarded mail",
			part:     Part{MediaType: "message/rfc822"},
			wantOK:   true,
			wantName: UnnamedMail,
		},
		{
			name:     "an unnamed forwarded mail, inline",
			part:     Part{MediaType: "Message/RFC822", Disposition: "inline"},
			wantOK:   true,
			wantName: UnnamedMail,
		},
		{
			name:     "a named forwarded mail",
			part:     Part{Filename: "Re: devis.eml", MediaType: "message/rfc822", Disposition: "attachment"},
			wantOK:   true,
			wantName: "Re: devis.eml",
		},
		{
			name: "a multipart is a container",
			part: Part{Filename: "dossier", MediaType: "multipart/mixed", Disposition: "attachment"},
		},
		{
			name:     "the name is cleaned",
			part:     Part{Filename: "facture\u202Efdp.exe", MediaType: "application/octet-stream", Disposition: "attachment"},
			wantOK:   true,
			wantName: "facturefdp.exe",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			name, ok := Listed(tc.part, shown)
			if ok != tc.wantOK || name != tc.wantName {
				t.Errorf("Listed(%+v) = %q, %v, want %q, %v", tc.part, name, ok, tc.wantName, tc.wantOK)
			}
		})
	}
}

// Without HTML, no image is shown in the text, so none is hidden.
func TestListedWithoutHTML(t *testing.T) {
	part := Part{Filename: "logo.png", MediaType: "image/png", Disposition: "inline", ContentID: "<logo@acme>"}
	for _, shown := range []Shown{nil, ShownBy(""), ShownBy("<p>Bonjour</p>")} {
		if name, ok := Listed(part, shown); !ok || name != "logo.png" {
			t.Errorf("Listed(%+v, %v) = %q, %v, want \"logo.png\", true", part, shown, name, ok)
		}
	}
}

func TestShownBy(t *testing.T) {
	cases := map[string][]string{
		"":                                       nil,
		`<img src="https://acme.fr/logo.png">`:   nil,
		`<img src="cid:logo@acme">`:              {"logo@acme"},
		`<img src='cid:logo@acme'>`:              {"logo@acme"},
		`<img src=cid:logo@acme>`:                {"logo@acme"},
		`<td style="background:url(cid:f@a)">`:   {"f@a"},
		`<img src="CID:Logo%40Acme">`:            {"logo@acme"},
		`<img src="cid:a@x"><img src="cid:b@x">`: {"a@x", "b@x"},
		`<img src="cid:%zz@x">`:                  {"%zz@x"},
	}
	for html, want := range cases {
		got := ShownBy(html)
		if len(got) != len(want) {
			t.Errorf("ShownBy(%q) = %v, want %v", html, got, want)
			continue
		}
		for _, id := range want {
			if !got[id] {
				t.Errorf("ShownBy(%q) = %v, want it to hold %q", html, got, id)
			}
		}
	}
}

func TestShownHas(t *testing.T) {
	shown := ShownBy(`<img src="cid:logo@acme">`)
	cases := map[string]bool{
		"<logo@acme>":     true,
		" <Logo@Acme> ":   true,
		"logo@acme":       true,
		"<autre@acme>":    false,
		"":                false,
		"<>":              false,
		"<logo@acme.com>": false,
	}
	for header, want := range cases {
		if got := shown.Has(header); got != want {
			t.Errorf("ShownBy(...).Has(%q) = %v, want %v", header, got, want)
		}
	}
}

func TestDispositionType(t *testing.T) {
	cases := map[string]string{
		"":                                 "",
		"attachment":                       "attachment",
		` Attachment ; filename="a.pdf"`:   "attachment",
		"INLINE":                           "inline",
		`attachment; filename="a;b.pdf" ;`: "attachment",
		"attachment;;; =":                  "attachment",
	}
	for in, want := range cases {
		if got := DispositionType(in); got != want {
			t.Errorf("DispositionType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSafeName(t *testing.T) {
	cases := map[string]string{
		"facture.pdf":            "facture.pdf",
		"  facture.pdf  ":        "facture.pdf",
		"../../../etc/passwd":    "passwd",
		`C:\Users\bob\rapport.x`: "rapport.x",
		"quo\"te.txt":            "quote.txt",
		"line\r\nbreak.txt":      "linebreak.txt",
		"tab\there.txt":          "tabhere.txt",
		"del\x7f.txt":            "del.txt",
		"c1\u0085\u009b.txt":     "c1.txt",
		"":                       Unnamed,
		"..":                     Unnamed,
		"/":                      Unnamed,
		".\x01.":                 Unnamed,
		"\u202e":                 Unnamed,
		"reçu-été.pdf":           "reçu-été.pdf",
		"Ελληνικά.pdf":           "Ελληνικά.pdf",
		// Every bidirectional control character: the embeddings and
		// overrides, the isolates, and the marks.
		"facture\u202Efdp.exe":                     "facturefdp.exe",
		"a\u202Ab\u202Bc\u202Cd\u202De\u202Ef.pdf": "abcdef.pdf",
		"a\u2066b\u2067c\u2068d\u2069e.pdf":        "abcde.pdf",
		"a\u200Eb\u200Fc\u061Cd.pdf":               "abcd.pdf",
		"\u2067photo\u2069.jpg":                    "photo.jpg",
		"\u202e../\u202e":                          Unnamed,
		"rapport\u200d.pdf":                        "rapport\u200d.pdf", // a joiner, not a bidi control
	}
	for in, want := range cases {
		if got := SafeName(in, Unnamed); got != want {
			t.Errorf("SafeName(%q) = %q, want %q", in, got, want)
		}
	}
}

// What SafeName returns is already safe: cleaning it again changes nothing,
// so the name the reader lists and the one the download sends agree.
func TestSafeNameIsStable(t *testing.T) {
	for _, in := range []string{"facture.pdf", "../a\u202e.pdf", "\x01..", " x ", `a\b`, "\u2066"} {
		once := SafeName(in, Unnamed)
		if twice := SafeName(once, Unnamed); twice != once {
			t.Errorf("SafeName(SafeName(%q)) = %q, want %q", in, twice, once)
		}
	}
}

func TestSafeNameFallback(t *testing.T) {
	if got := SafeName("", UnnamedMail); got != UnnamedMail {
		t.Errorf("SafeName(%q, %q) = %q, want %q", "", UnnamedMail, got, UnnamedMail)
	}
}
