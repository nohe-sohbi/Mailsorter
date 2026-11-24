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
			// Drawn in the text, and listed so it can be saved.
			name:     "a named image the HTML shows",
			part:     Part{Filename: "logo.png", MediaType: "image/png", Disposition: "inline", ContentID: "<logo@acme>"},
			wantOK:   true,
			wantName: "logo.png",
		},
		{
			name:     "a named image the HTML shows, declared an attachment",
			part:     Part{Filename: "pied.png", MediaType: "image/png", Disposition: "attachment", ContentID: "<pied@acme>"},
			wantOK:   true,
			wantName: "pied.png",
		},
		{
			name: "an unnamed image the HTML shows",
			part: Part{MediaType: "image/png", Disposition: "inline", ContentID: "<logo@acme>"},
		},
		{
			name: "an unnamed image the HTML shows, without a disposition",
			part: Part{MediaType: "image/png", ContentID: "<logo@acme>"},
		},
		{
			name: "an unnamed image the HTML shows as a CSS background",
			part: Part{MediaType: "image/jpeg", ContentID: "<fond@acme>"},
		},
		{
			name: "an unnamed image the HTML shows, its reference %-escaped and in another case",
			part: Part{MediaType: "IMAGE/PNG", ContentID: "<banniere@acme>"},
		},
		{
			name: "an unnamed image the HTML shows, declared an attachment",
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

// Without the HTML (the filter "Pièces jointes"), an inline part with a
// Content-ID is taken for an image the HTML shows; everything else is
// decided as the reader decides it.
func TestListedWithoutHTML(t *testing.T) {
	cases := []struct {
		name string
		part Part
		want bool
	}{
		{"the text of the mail", Part{MediaType: "text/plain"}, false},
		{"a named file", Part{Filename: "facture.pdf", MediaType: "application/pdf", Disposition: "attachment"}, true},
		{"a named file without a disposition", Part{Filename: "photos.zip", MediaType: "application/zip"}, true},
		{"a named text file", Part{Filename: "notes.txt", MediaType: "text/plain"}, true},
		{"Apple Mail's inline PDF", Part{Filename: "devis.pdf", MediaType: "application/pdf", Disposition: "inline"}, true},
		{"a named photo inline, without a Content-ID", Part{Filename: "photo.jpg", MediaType: "image/jpeg", Disposition: "inline"}, true},
		{"an unnamed attachment", Part{MediaType: "application/octet-stream", Disposition: "attachment"}, true},
		{"an unnamed forwarded mail", Part{MediaType: "message/rfc822"}, true},
		{"a picture the HTML draws, inline", Part{Filename: "logo.png", MediaType: "image/png", Disposition: "inline", ContentID: "<logo@x>"}, false},
		{"a picture the HTML draws, without a disposition", Part{Filename: "image001.png", MediaType: "image/png", ContentID: "<image001@x>"}, false},
		{"a photo attached from Gmail's web interface", Part{Filename: "photo.jpg", MediaType: "image/jpeg", Disposition: `attachment; filename="photo.jpg"`, ContentID: "<f_abc>"}, true},
		{"a PDF inline with a Content-ID is not a picture", Part{Filename: "devis.pdf", MediaType: "application/pdf", Disposition: "inline", ContentID: "<f_abc>"}, true},
		{"an unnamed inline image", Part{MediaType: "image/png", Disposition: "inline"}, false},
	}
	for _, tc := range cases {
		if got := ListedWithoutHTML(tc.part); got != tc.want {
			t.Errorf("ListedWithoutHTML(%s: %+v) = %v, want %v", tc.name, tc.part, got, tc.want)
		}
	}
}

// The filter and the reader agree on every part but the two kinds of image
// written in ListedWithoutHTML's comment.
func TestListedWithoutHTMLAgreesWithTheReader(t *testing.T) {
	parts := []Part{
		{MediaType: "text/plain"},
		{Filename: "facture.pdf", MediaType: "application/pdf", Disposition: "attachment", ContentID: "<f_1>"},
		{Filename: "devis.pdf", MediaType: "application/pdf", Disposition: "inline"},
		{Filename: "devis.pdf", MediaType: "application/pdf", Disposition: "inline", ContentID: "<logo@x>"},
		{Filename: "photo.jpg", MediaType: "image/jpeg", Disposition: "inline"},
		{Filename: "photo.jpg", MediaType: "image/jpeg", Disposition: "attachment", ContentID: "<f_2>"},
		{MediaType: "image/png", Disposition: "inline", ContentID: "<logo@x>"},
		{MediaType: "message/rfc822"},
		{MediaType: "application/octet-stream", Disposition: "attachment"},
	}
	for _, html := range []string{"", `<img src="cid:logo@x">`} {
		shown := ShownBy(html)
		for _, p := range parts {
			_, reader := Listed(p, shown)
			if filter := ListedWithoutHTML(p); filter != reader {
				t.Errorf("for %+v (HTML %q) the filter says %v and the reader %v", p, html, filter, reader)
			}
		}
	}
	// The two differences.
	for _, html := range []string{"<p>x</p>", `<img src="cid:logo@x">`} {
		named := Part{Filename: "logo.png", MediaType: "image/png", Disposition: "inline", ContentID: "<logo@x>"}
		if _, reader := Listed(named, ShownBy(html)); !reader || ListedWithoutHTML(named) {
			t.Errorf("a named image with a Content-ID, not declared an attachment (HTML %q): reader %v, filter %v, want true and false", html, reader, ListedWithoutHTML(named))
		}
	}
	drawn := Part{MediaType: "image/png", Disposition: "attachment", ContentID: "<pied@x>"}
	if _, reader := Listed(drawn, ShownBy(`<img src="cid:pied@x">`)); reader || !ListedWithoutHTML(drawn) {
		t.Errorf("an unnamed image declared an attachment that the HTML shows: reader %v, filter %v, want false and true", reader, ListedWithoutHTML(drawn))
	}
}

// The pictures the screen draws in the text: an image type a browser shows
// without running anything, that the HTML points at.
func TestInlineImage(t *testing.T) {
	shown := ShownBy(`<img src="cid:logo@acme"><img src="cid:Photo%40iPhone"><img src="cid:carte@acme"><img src="cid:devis@acme">`)
	cases := []struct {
		name    string
		part    Part
		wantCID string
		wantOK  bool
	}{
		{"a logo", Part{MediaType: "image/png", ContentID: "<logo@acme>"}, "logo@acme", true},
		{"a named photo, declared an attachment", Part{Filename: "IMG_0001.JPG", MediaType: "image/jpeg", Disposition: "attachment", ContentID: "<photo@iphone>"}, "photo@iphone", true},
		{"an image the HTML does not point at", Part{MediaType: "image/png", ContentID: "<autre@acme>"}, "", false},
		{"an image without a Content-ID", Part{Filename: "photo.jpg", MediaType: "image/jpeg"}, "", false},
		{"an SVG, which can carry script", Part{MediaType: "image/svg+xml", ContentID: "<carte@acme>"}, "", false},
		{"a PDF the HTML points at is no picture", Part{MediaType: "application/pdf", ContentID: "<devis@acme>"}, "", false},
	}
	for _, tc := range cases {
		cid, ok := InlineImage(tc.part, shown)
		if cid != tc.wantCID || ok != tc.wantOK {
			t.Errorf("InlineImage(%s) = %q, %v, want %q, %v", tc.name, cid, ok, tc.wantCID, tc.wantOK)
		}
	}
}

// Without HTML, no image is shown in the text, so none is hidden.
func TestListedForAMailWithoutHTML(t *testing.T) {
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
