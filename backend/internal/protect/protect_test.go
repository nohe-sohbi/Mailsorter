package protect

import (
	"strings"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/protect/protecttest"
)

func TestAddressOfRealHeaders(t *testing.T) {
	for _, tc := range protecttest.Headers {
		if tc.Second != "" {
			// Two From headers: the transports' rule (no single sender), not
			// the reading of one header.
			continue
		}
		t.Run(tc.Name, func(t *testing.T) {
			got := Address(tc.From)
			if got != tc.Want {
				t.Errorf("Address(%q) = %q, want %q", tc.From, got, tc.Want)
			}
			if got != "" && Address(got) != got {
				t.Errorf("Address(Address(%q)) = %q, want it to read back as %q", tc.From, Address(got), got)
			}
			if n := NormalizeAddress(tc.From); n != addressForm(tc.Want) {
				t.Errorf("NormalizeAddress(%q) = %q, want %q", tc.From, n, addressForm(tc.Want))
			}
		})
	}
}

// "Protéger" on a mail stores EntryFromMail of its From, and the shield
// compares senders with Match: for every header that names a sender on a
// domain IDNA takes, the one must find the other. 'patron'@victime.fr is such
// an address, apostrophes included: trimmed (the typed-entry path), it
// protected nobody. A sender on a domain IDNA refuses ('patron@victime.fr',
// whose domain ends with the apostrophe, or x@pay<U+202E>pal.com) cannot be
// protected from its mail.
func TestProtectingAMailShieldsItsSender(t *testing.T) {
	froms := []string{`"a b"@x.com`, `"Ami" <"a b"@x.com>`, `"<boss@corp.com>"@evil.io`, `"jean dupont"@exemple.fr`, "'patron'@victime.fr"}
	refused := []string{"'patron@victime.fr'", "x@pay\u202Epal.com", "x@paypal\u2024com"}
	for _, tc := range protecttest.Headers {
		if tc.Want == "" || tc.Second != "" {
			continue
		}
		if _, ok := ASCIIDomain(tc.Want[strings.LastIndex(tc.Want, "@")+1:]); ok {
			froms = append(froms, tc.From)
		} else {
			refused = append(refused, tc.From)
		}
	}
	for _, from := range froms {
		value := EntryFromMail(from)
		if value == "" || !Match(from, []string{value}) {
			t.Errorf("EntryFromMail(%q) = %q, and Match(%q) on it = %v, want an entry that matches", from, value, from, Match(from, []string{value}))
		}
	}
	for _, from := range refused {
		if value := EntryFromMail(from); value != "" {
			t.Errorf("EntryFromMail(%q) = %q, want none: IDNA refuses its domain", from, value)
		}
	}
	// A From that names no sender cannot be protected from its mail.
	for _, tc := range protecttest.Headers {
		if tc.Want == "" && tc.Second == "" && EntryFromMail(tc.From) != "" {
			t.Errorf("EntryFromMail(%q) = %q, want none: it names no sender", tc.From, EntryFromMail(tc.From))
		}
	}
}

// A From that names no sender is stored without its control bytes, and
// still names no sender, even when cleaning would have given it one.
func TestNoSenderText(t *testing.T) {
	cases := map[string]string{
		"Broken\x01 <unterminated":           "Broken  <unterminated",
		"No\x7fAddress":                      "No Address",
		"tab\tkept, a@x.fr, b@x.fr":          "tab\tkept, a@x.fr, b@x.fr",
		"Lettre\x01 <lettre@journal.fr>":     `"Lettre  <lettre@journal.fr>"`,
		"\"Lettre\x01\" <lettre@journal.fr>": `"\"Lettre \" <lettre@journal.fr>"`,
	}
	for raw, want := range cases {
		got := NoSenderText(raw)
		if got != want {
			t.Errorf("NoSenderText(%q) = %q, want %q", raw, got, want)
		}
		if a := Address(got); a != "" {
			t.Errorf("NoSenderText(%q) = %q, which reads as %q, want no sender", raw, got, a)
		}
	}
}

// An accented domain is one domain in any of its forms: typed accented or in
// punycode, in full-width letters, as an entry or in a From. The settings
// store it readable; Match compares in ASCII.
func TestInternationalDomainsAreOneDomain(t *testing.T) {
	cases := []struct {
		typed, stored string
		froms         []string
	}{
		{"café.fr", "café.fr", []string{"contact@xn--caf-dma.fr", "contact@café.fr", "Café <contact@CAFÉ.fr>", "news@mail.xn--caf-dma.fr"}},
		{"xn--caf-dma.fr", "café.fr", []string{"contact@café.fr", "contact@xn--caf-dma.fr"}},
		{"ｅｘａｍｐｌｅ.fr", "example.fr", []string{"a@example.fr", "a@EXAMPLE.fr"}},
		{"Contact@Café.fr", "contact@café.fr", []string{"contact@xn--caf-dma.fr", "Café <contact@café.fr>"}},
		{"contact@xn--caf-dma.fr", "contact@café.fr", []string{"contact@café.fr"}},
	}
	for _, tc := range cases {
		value, _ := NormalizeEntry(tc.typed)
		if value != tc.stored {
			t.Errorf("NormalizeEntry(%q) = %q, want %q", tc.typed, value, tc.stored)
		}
		for _, from := range tc.froms {
			if !Match(from, []string{value}) {
				t.Errorf("the entry %q (typed %q) does not protect a mail from %q", value, tc.typed, from)
			}
		}
	}
	if Match("contact@cafe.fr", []string{"café.fr"}) {
		t.Error("café.fr protects cafe.fr, another domain")
	}
	for _, refused := range []string{"xn--zz.fr", "x_y.fr", "a@xn--zz.fr"} {
		if v, _ := NormalizeEntry(refused); v != "" {
			t.Errorf("NormalizeEntry(%q) = %q, want it refused: IDNA does not take it", refused, v)
		}
	}
	if !SameAddress("Café <contact@café.fr>", "contact@xn--caf-dma.fr") {
		t.Error("SameAddress does not see contact@café.fr and contact@xn--caf-dma.fr as one address")
	}
	if got := DomainForms("Café.fr"); len(got) != 2 || got[0] != "café.fr" || got[1] != "xn--caf-dma.fr" {
		t.Errorf("DomainForms(Café.fr) = %q, want café.fr and xn--caf-dma.fr, each once", got)
	}
	if got := DomainForms("xn--caf-dma.fr"); len(got) != 2 || got[0] != "xn--caf-dma.fr" || got[1] != "café.fr" {
		t.Errorf("DomainForms(xn--caf-dma.fr) = %q, want xn--caf-dma.fr and café.fr, each once", got)
	}
}

// A domain is read as IDNA reads it as written, never lowered by Go first:
// Go lowers "İ" (U+0130) to "i" and "ẞ" (U+1E9E) to "ß", UTS 46 maps them to
// "i̇" and, in the tables x/net carries, "ss". news@lİnkedin.com is not
// linkedin.com's, and x@STRAẞE.de is strasse.de's, not straße.de's.
func TestDomainsAreReadAsWrittenByIDNA(t *testing.T) {
	if Match("news@lİnkedin.com", []string{"linkedin.com"}) || Match("news@lİnkedin.com", []string{"news@linkedin.com"}) {
		t.Error("linkedin.com protects news@lİnkedin.com, another domain")
	}
	if Match("news@linkedin.com", []string{"lİnkedin.com"}) {
		t.Error("lİnkedin.com protects news@linkedin.com, another domain")
	}
	if Match("x@STRAẞE.de", []string{"straße.de"}) {
		t.Error("straße.de protects x@STRAẞE.de, which IDNA reads strasse.de")
	}
	if !Match("x@STRAẞE.de", []string{"strasse.de"}) {
		t.Error("strasse.de does not protect x@STRAẞE.de, which IDNA reads strasse.de")
	}
	if SameAddress("news@lİnkedin.com", "news@linkedin.com") || SameAddress("x@STRAẞE.de", "x@straße.de") {
		t.Error("SameAddress takes two domains IDNA tells apart for one")
	}
	if !SameAddress("News@LİNKEDIN.com", "news@lİnkedin.com") {
		t.Error("SameAddress does not see one domain in News@LİNKEDIN.com and news@lİnkedin.com")
	}
	// The local part alone goes to lowercase.
	if !SameAddress("Jean.Dupont@x.fr", "jean.dupont@x.fr") {
		t.Error("SameAddress tells Jean.Dupont@x.fr from jean.dupont@x.fr")
	}
}

// What is typed in the settings, or protected from a mail, protects a From
// written the same way, whatever the way: accented, in punycode, full-width,
// with the letters Go and IDNA lower differently, in a script that is shown
// in punycode.
func TestAnEntryProtectsAFromWrittenTheSameWay(t *testing.T) {
	for _, domain := range []string{
		"lİnkedin.com", "LİNKEDIN.com", "STRAẞE.de", "straße.de", "Café.fr",
		"xn--caf-dma.fr", "ｅｘａｍｐｌｅ.fr", "xn--80ak6aa92e.com", "аррӏе.com",
		"pаypal.com", "日本.jp", "ドメイン名例.jp", "ΑΒΓ.gr", "Kelvin.com",
	} {
		from := "Contact <Contact@" + domain + ">"
		typed, kind := NormalizeEntry(domain)
		if typed == "" || kind != KindDomain || !Match(from, []string{typed}) {
			t.Errorf("the domain typed %q is stored %q, which does not protect %q", domain, typed, from)
		}
		address, kind := NormalizeEntry("Contact@" + domain)
		if address == "" || kind != KindAddress || !Match(from, []string{address}) {
			t.Errorf("the address typed %q is stored %q, which does not protect %q", "Contact@"+domain, address, from)
		}
		if entry := EntryFromMail(from); entry == "" || !Match(from, []string{entry}) {
			t.Errorf("Protéger on a mail from %q stores %q, which does not protect it", from, entry)
		}
	}
}

// How a domain is shown, in the settings and on every screen: each label in
// Unicode only when all its characters are in the closed list (ASCII letters,
// digits, hyphen, Latin-1 Supplement and Latin Extended-A letters but those
// drawn like ASCII) and it is stable in NFC; in punycode otherwise.
func TestADomainIsShownFromAClosedList(t *testing.T) {
	cases := map[string]string{
		"café.fr":            "café.fr",
		"xn--caf-dma.fr":     "café.fr",
		"crème-brûlée.fr":    "crème-brûlée.fr",
		"straße.de":          "straße.de",
		"cafe\u0301.fr":      "café.fr",
		"example.com":        "example.com",
		"日本.jp":              "xn--wgv71a.jp",
		"xn--80ak6aa92e.com": "xn--80ak6aa92e.com",
		"аррӏе.com":          "xn--80ak6aa92e.com",
		"пример.рф":          "xn--e1afmkfd.xn--p1ai",
		"αβγ.gr":             "xn--mxacd.gr",
		"pаypal.com":         "xn--pypal-4ve.com",
		"ドメイン名例.jp":          "xn--eckwd4c7cu47r2wf.jp",
		"lİnkedin.com":       "xn--linkedin-p0e.com",
		"tiếng.vn":           "xn--ting-hv5a.vn",
		"हिन्दी.in":          "xn--j2bd4cyah0f.in",
	}
	for typed, want := range cases {
		if got, _ := NormalizeEntry(typed); got != want {
			t.Errorf("NormalizeEntry(%q) = %q, want %q", typed, got, want)
		}
	}
	if got, _ := NormalizeEntry("Jean@пример.рф"); got != "jean@xn--e1afmkfd.xn--p1ai" {
		t.Errorf("NormalizeEntry(Jean@пример.рф) = %q, want jean@xn--e1afmkfd.xn--p1ai", got)
	}
	if got := EntryFromMail("Apple <id@xn--80ak6aa92e.com>"); got != "id@xn--80ak6aa92e.com" {
		t.Errorf("EntryFromMail(Apple <id@xn--80ak6aa92e.com>) = %q, want id@xn--80ak6aa92e.com", got)
	}
}

// The homographs of the twelfth review: a label with one character out of
// the closed list never shows in Unicode. Taken by IDNA, it shows in
// punycode, the form the app compares; refused, its non-ASCII characters
// show as U+FFFD. Either way the look-alike character is gone from the
// screen, and so is the domain it imitated.
func TestALookAlikeDomainNeverShowsAsAnother(t *testing.T) {
	cases := map[string]string{
		"\u0261oogle.com":     "google.com", // ɡ, IPA
		"xn--oogle-qmc.com":   "google.com", // the same, written in punycode
		"\u0261mail.com":      "gmail.com",
		"\u0261ithub.com":     "github.com",
		"payp\u0251l.com":     "paypal.com", // ɑ, IPA
		"g\u0131thub.com":     "github.com", // ı, dotless, Latin Extended-A
		"yah\u1D0F\u1D0F.com": "yahoo.com",  // ᴏ, small capital
		"app\u029Fe.com":      "apple.com",  // ʟ, small capital
		"paypal\u00B7com":     "paypal.com", // middle dot
		"paypal\u2E31com":     "paypal.com", // word separator middle dot
		"paypal\u16EBcom":     "paypal.com", // runic single punctuation
		"paypal\u02D1com":     "paypal.com", // half triangular colon
		"paypal\u30FBcom":     "paypal.com", // katakana middle dot
		"k\u0138.com":         "kk.com",     // ĸ, kra
	}
	for written, imitated := range cases {
		shown := ShownDomain(written)
		if ascii, ok := ASCIIDomain(written); ok {
			if shown != ascii {
				t.Errorf("ShownDomain(%q) = %q, want its punycode %q", written, shown, ascii)
			}
		} else if !strings.ContainsRune(shown, '\uFFFD') {
			t.Errorf("ShownDomain(%q) = %q, which IDNA refuses: want its non-ASCII characters shown as U+FFFD", written, shown)
		}
		if shown == imitated || strings.ContainsFunc(shown, func(r rune) bool { return r >= 0x80 && r != '\uFFFD' }) {
			t.Errorf("ShownDomain(%q) = %q, which reads as %q or keeps the look-alike", written, shown, imitated)
		}
		if got := ShownAddress("x@" + written); got != "x@"+shown {
			t.Errorf("ShownAddress(x@%q) = %q, want x@%q", written, got, shown)
		}
	}
}

// A Shield puts each entry in its comparison form once, and answers what
// Match, Allowed and AllowedOnFolders answer.
func TestShieldAnswersLikeTheEntries(t *testing.T) {
	entries := []string{"boss@corp.com", "café.fr", "Contact@Ｓhop.fr", "lİnkedin.com"}
	shield := NewShield(entries)
	froms := []string{
		"Boss <BOSS@corp.com>", "a@xn--caf-dma.fr", "x@mail.café.fr", "contact@shop.fr",
		"news@linkedin.com", "news@lİnkedin.com", "stranger@other.com", "not an address",
	}
	for _, from := range froms {
		for _, action := range []string{ActionArchive, ActionLabel, "markRead"} {
			if got, want := shield.Allows(action, from), Allowed(action, from, entries); got != want {
				t.Errorf("Shield.Allows(%q, %q) = %v, Allowed = %v", action, from, got, want)
			}
			if got, want := shield.AllowsOnFolders(action, from), AllowedOnFolders(action, from, entries); got != want {
				t.Errorf("Shield.AllowsOnFolders(%q, %q) = %v, AllowedOnFolders = %v", action, from, got, want)
			}
		}
	}
	if !shield.Protects("a@xn--caf-dma.fr") || shield.Protects("news@linkedin.com") || !shield.Protects("news@lİnkedin.com") {
		t.Error("the shield does not read the entries as written")
	}
	if shield.Allows(ActionArchive, "not an address") {
		t.Error("an unreadable sender is archived while someone is protected")
	}
	empty := NewShield(nil)
	if !empty.Empty() || !empty.Allows(ActionArchive, "not an address") {
		t.Error("an empty shield refuses something")
	}
}

// What a screen shows for a sender: never an address of the display name
// that is not the sender's, in any width, no control or format character,
// and the address when the name shows nothing.
func TestLabel(t *testing.T) {
	cases := map[string]string{
		`"boss@corp.com" <evil@evil.io>`:                   "evil@evil.io",
		"boss@corp.com <evil@evil.io>":                     "evil@evil.io",
		`"Evil@Evil.io" <evil@evil.io>`:                    "evil@evil.io",
		"\"boss\uFF20corp.com\" <evil@evil.io>":            "evil@evil.io",
		"\"boss\uFE6Bcorp.com\" <evil@evil.io>":            "evil@evil.io",
		"=?UTF-8?B?Ym9zc++8oGNvcnAuY29t?= <evil@evil.io>":  "evil@evil.io",
		"\"\u202Emoc.lapyap\" <evil@evil.io>":              "moc.lapyap",
		"\"Ma\u200B Banque\u2066\u200E\" <info@banque.fr>": "Ma Banque",
		"<\u202Emoc.lapyap@evil.io>":                       "\uFFFDmoc.lapyap@evil.io",
		"<x@pay\u202Epal.com>":                             "x@pay\uFFFDpal.com",
		"<x@pay\u200Dpal.com>":                             "x@pay\uFFFDpal.com",
		"<x@pay\u200Cpal.com>":                             "x@pay\uFFFDpal.com",
		"<pay\u202Epal@x.com>":                             "pay\uFFFDpal@x.com",
		"<pay\u200Dpal@x.com>":                             "pay\uFFFDpal@x.com",
		"\"boss@corp.com\" <x@pay\u202Epal.com>":           "x@pay\uFFFDpal.com",
		"\"\u200B\" <x@paypa\uFE0Fl.com>":                  "x@paypal.com",
		"y@p\u0430ypal.com":                                "y@xn--pypal-4ve.com",
		"x@paypal\u2024com":                                "x@paypal\uFFFDcom",
		"pay\u2024pal@x.com":                               "pay\uFFFDpal@x.com",
		"\u0455upport@paypal.com":                          "\uFFFDupport@paypal.com",
		"j\u00FCrgen@x.de":                                 "j\u00FCrgen@x.de",
		"\"jean dupont\"@exemple.fr":                       "\"jean dupont\"@exemple.fr",
		"x\uFFFD@x.fr":                                     "x\uFFFD@x.fr",
		"boss\uFF20corp.com@evil.io":                       "boss\uFFFDcorp.com@evil.io",
		"boss\uFE6Bcorp.com@evil.io":                       "boss\uFFFDcorp.com@evil.io",
		"x@EVIL.io":                                        "x@evil.io",
		"x@caf\u00E9.fr":                                   "x@caf\u00E9.fr",
		"\"\u200B\" <evil@evil.io>":                        "evil@evil.io",
		"\"\u2800\u2800\" <evil@evil.io>":                  "evil@evil.io",
		"\"\u3164\" <evil@evil.io>":                        "evil@evil.io",
		"\"\u202E\u2067\" <evil@evil.io>":                  "evil@evil.io",
		"\"\u0301\" <evil@evil.io>":                        "evil@evil.io",
		"\"Équipe \U0001F468\u200D\U0001F4BB\" <dev@x.fr>": "Équipe \U0001F468\u200D\U0001F4BB",
		"\"Lettre\u202E\" <lettre@journal.fr>, x@y.fr":     "\"Lettre\uFFFD\" <lettre@journal.fr>, x@y.fr",
		`"<patron@victime.fr>" <lettre@journal.fr>`:        "lettre@journal.fr",
		"Ma Banque <info@banque.fr>":                       "Ma Banque",
		"info@banque.fr":                                   "info@banque.fr",
		"=?utf-8?q?R=C3=A9seau?= <news@reseau.fr>":         "Réseau",
		`"Lettre <lettre@journal.fr>, Patron <p@v.fr>"`:    "Lettre <lettre@journal.fr>, Patron <p@v.fr>",
		"Broken\x01 <unterminated":                         "Broken\uFFFD <unterminated",
		"tab\tkept, a@x.fr, b@x.fr":                        "tab kept, a@x.fr, b@x.fr",
		"patron@victime.fr, Lettre <lettre@journal.fr>":    "patron@victime.fr, Lettre <lettre@journal.fr>",
		"": "",
	}
	for from, want := range cases {
		if got := Label(from); got != want {
			t.Errorf("Label(%q) = %q, want %q", from, got, want)
		}
	}
	// An address is never cleaned into another one: the character that hid
	// is shown, so what the label shows is never an address free of it
	// (x@paypal.com) while the app compares another.
	for _, from := range []string{"<x@pay\u202Epal.com>", "<x@pay\u200Dpal.com>", "<pay\u202Epal@x.com>", "\"boss@corp.com\" <pay\u200Cpal@x.com>"} {
		if got := Label(from); !strings.ContainsRune(got, '\uFFFD') {
			t.Errorf("Label(%q) = %q: the hidden character was removed, not shown", from, got)
		}
	}
}

// An address typed between apostrophes protects that address.
func TestATypedEntryInApostrophesProtects(t *testing.T) {
	value, _ := NormalizeEntry("'jean@x.fr'")
	if !Match("Jean <jean@x.fr>", []string{value}) {
		t.Errorf("NormalizeEntry('jean@x.fr') = %q, which does not match Jean <jean@x.fr>", value)
	}
}

// What the settings refuse: a typed value that is neither an address that
// reads nor a well-formed domain. Stored, each of these protected nobody.
func TestNormalizeEntryRefusesWhatProtectsNobody(t *testing.T) {
	for _, in := range []string{
		"jean@x.fr paul@y.fr",
		"jean@x.fr\tpaul@y.fr",
		"jean\t@x.fr",
		"jean dupont@x.fr",
		"jean@x.fr>",
		"jean@@x.fr",
		"Jean Dupont",
		"x.fr.",
		"x..fr",
		"-x.fr",
		"x_y.fr",
		`"boss@corp.com"`,
		"a@x.fr, b@x.fr",
		"Liste: a@x.fr",
		"@",
		"",
	} {
		if v, k := NormalizeEntry(in); v != "" || k != "" {
			t.Errorf("NormalizeEntry(%q) = (%q, %q), want it refused", in, v, k)
		}
	}
}

// A value the settings accept protects what it says: a mail from that
// address, or from that domain or one under it, is matched.
func TestATypedEntryProtectsWhatItSays(t *testing.T) {
	cases := map[string]string{
		"Hi@Acme.com":               "Hi@Acme.com",
		"'jean@x.fr'":               "Jean <jean@x.fr>",
		"Acme News <news@acme.com>": "news@acme.com",
		`"jean dupont"@exemple.fr`:  `"jean dupont"@exemple.fr`,
		"acme.com":                  "Promo <deals@mail.acme.com>",
		"@acme.com":                 "deals@acme.com",
		"café.fr":                   "contact@café.fr",
		"bad":                       "x@bad",
	}
	for in, from := range cases {
		value, kind := NormalizeEntry(in)
		if value == "" || !Match(from, []string{value}) {
			t.Errorf("NormalizeEntry(%q) = (%q, %q), which does not protect a mail from %q", in, value, kind, from)
		}
	}
}

func TestNormalizeEntry(t *testing.T) {
	cases := []struct {
		in        string
		wantValue string
		wantKind  string
	}{
		{"Hi@Acme.com", "hi@acme.com", KindAddress},
		{"@acme.com", "acme.com", KindDomain},
		{"acme.com", "acme.com", KindDomain},
		{"  newsletter.io ", "newsletter.io", KindDomain},
		{"Acme News <news@acme.com>", "news@acme.com", KindAddress},
		// A typed value that does not read and holds what only a header or
		// a list would is refused, quotes included.
		{`"boss@corp.com"`, "", ""},
		{"a@x.fr, b@x.fr", "", ""},
		{"Liste: a@x.fr", "", ""},
		{"a@x.fr; b@x.fr", "", ""},
		{"", "", ""},
		{"@", "", ""},
		{"@bad", "bad", KindDomain},
		// Apostrophes around what was typed are not part of the address.
		{"'jean@x.fr'", "jean@x.fr", KindAddress},
		{" 'Jean@X.fr' ", "jean@x.fr", KindAddress},
		{"o'brien@x.ie", "o'brien@x.ie", KindAddress},
		// A quoted local part keeps its quotes: that is how Match reads it.
		{`"jean dupont"@exemple.fr`, `"jean dupont"@exemple.fr`, KindAddress},
		// Several authors protect nobody.
		{"patron@victime.fr, Lettre <lettre@journal.fr>", "", ""},
	}
	for _, tc := range cases {
		v, k := NormalizeEntry(tc.in)
		if v != tc.wantValue || k != tc.wantKind {
			t.Errorf("NormalizeEntry(%q) = (%q,%q), want (%q,%q)", tc.in, v, k, tc.wantValue, tc.wantKind)
		}
	}
}

func TestMatch(t *testing.T) {
	entries := []string{"boss@corp.com", "acme.com"}
	cases := []struct {
		from string
		want bool
	}{
		{"The Boss <boss@corp.com>", true},          // exact address
		{"boss@corp.com", true},                     // bare address
		{"Boss <BOSS@CORP.COM>", true},              // case-insensitive
		{"colleague@corp.com", false},               // same domain, not listed as domain
		{"Promo <deals@acme.com>", true},            // domain entry
		{`"<boss@corp.com>" <spam@evil.io>`, false}, // the address in a display name is not the sender's
		{`"<boss@corp.com>"@evil.io`, false},        // nor one in a quoted local part
		{"News <news@mail.acme.com>", true},         // subdomain of domain entry
		{"news@notacme.com", false},                 // suffix but not a subdomain
		{"someone@evilacme.com", false},             // must not match acme.com as substring
		{"", false},
	}
	for _, tc := range cases {
		if got := Match(tc.from, entries); got != tc.want {
			t.Errorf("Match(%q) = %v, want %v", tc.from, got, tc.want)
		}
	}
	if Match("anyone@x.com", nil) {
		t.Error("Match with empty entries should be false")
	}
}

func TestSameAddress(t *testing.T) {
	cases := []struct {
		name string
		from string
		addr string
		want bool
	}{
		{"bare address, same", "info@boutique.com", "info@boutique.com", true},
		{"display name on the mail", "Boutique <info@boutique.com>", "info@boutique.com", true},
		{"another display name, same address", "Boutique Soldes <info@boutique.com>", "info@boutique.com", true},
		{"quoted display name", `"Boutique, Paris" <info@boutique.com>`, "info@boutique.com", true},
		{"addr given as a From header", "info@boutique.com", "Boutique <info@boutique.com>", true},
		{"case-insensitive", "Boutique <INFO@Boutique.COM>", "info@boutique.com", true},
		{"surrounding whitespace", "  info@boutique.com  ", "info@boutique.com", true},
		// The bug this function exists for: each of these contains the
		// address as a substring and is another sender.
		{"longer local part", "Facturation <facturation-info@boutique.com>", "info@boutique.com", false},
		{"longer local part, bare", "facturation-info@boutique.com", "info@boutique.com", false},
		{"longer domain", "info@boutique.community", "info@boutique.com", false},
		{"subdomain", "info@boutique.com.evil.io", "info@boutique.com", false},
		{"other sender on the domain", "Service client <contact@boutique.com>", "info@boutique.com", false},
		{"address in the display name only", "info@boutique.com <spoof@evil.io>", "info@boutique.com", false},
		{"address in a quoted display name", `"<info@boutique.com>" <spoof@evil.io>`, "info@boutique.com", false},
		{"the real one behind a quoted address", `"<spoof@evil.io>" <info@boutique.com>`, "info@boutique.com", true},
		// No usable address on the sweep side must reach nothing, not everything.
		{"empty addr", "info@boutique.com", "", false},
		{"empty addr, empty from", "", "", false},
		{"addr without @", "boutique.com", "boutique.com", false},
		{"addr without local part", "@boutique.com", "@boutique.com", false},
		{"addr without domain", "info@", "info@", false},
		{"empty from", "", "info@boutique.com", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SameAddress(tc.from, tc.addr); got != tc.want {
				t.Errorf("SameAddress(%q, %q) = %v, want %v", tc.from, tc.addr, got, tc.want)
			}
		})
	}
}

func TestIsDestructiveAndAllowed(t *testing.T) {
	for _, a := range []string{"archive", "trash", "delete", "ARCHIVE", " Delete "} {
		if !IsDestructive(a) {
			t.Errorf("IsDestructive(%q) = false, want true", a)
		}
	}
	for _, a := range []string{"label", "star", "keep", "markRead", "read", ""} {
		if IsDestructive(a) {
			t.Errorf("IsDestructive(%q) = true, want false", a)
		}
	}

	entries := []string{"vip@corp.com"}
	// Destructive action on a protected sender is blocked.
	if Allowed("archive", "vip@corp.com", entries) {
		t.Error("archive on protected sender should be blocked")
	}
	// Non-destructive action on a protected sender is allowed.
	if !Allowed("label", "vip@corp.com", entries) {
		t.Error("label on protected sender should be allowed")
	}
	// Destructive action on an unprotected sender is allowed.
	if !Allowed("delete", "other@corp.com", entries) {
		t.Error("delete on unprotected sender should be allowed")
	}
	// Fail safe: a sender that cannot be read is spared while anyone is
	// protected (an unreadable header must not bypass the list), and is
	// fair game when nobody is.
	for _, from := range []string{"", `<vip@corp.com> <x@evil.io>`, "Broken <unterminated"} {
		if Allowed("archive", from, entries) || AllowedOnFolders("label", from, entries) {
			t.Errorf("a destructive action on the unreadable sender %q should be blocked while someone is protected", from)
		}
		if !Allowed("archive", from, nil) {
			t.Errorf("archive on the unreadable sender %q should be allowed when nobody is protected", from)
		}
	}
	// The odd but real address "<vip@corp.com>"@evil.io is not vip@corp.com.
	if !Allowed("archive", `"<vip@corp.com>"@evil.io`, entries) {
		t.Error(`archive on "<vip@corp.com>"@evil.io should be allowed: its sender is on evil.io`)
	}
}

// On a mailbox made of folders (IMAP) labelling files the mail out of the
// inbox, so the shield covers it there, and only there.
func TestOnFoldersLabellingIsShielded(t *testing.T) {
	for _, a := range []string{"archive", "trash", "delete", "label", " Label "} {
		if !IsDestructiveOnFolders(a) {
			t.Errorf("IsDestructiveOnFolders(%q) = false, want true", a)
		}
	}
	for _, a := range []string{"star", "keep", "markRead", "read", "unlabel", ""} {
		if IsDestructiveOnFolders(a) {
			t.Errorf("IsDestructiveOnFolders(%q) = true, want false", a)
		}
	}

	entries := []string{"vip@corp.com"}
	if AllowedOnFolders("label", "VIP <vip@corp.com>", entries) {
		t.Error("label on a protected sender should be blocked on folders")
	}
	if !AllowedOnFolders("label", "other@corp.com", entries) {
		t.Error("label on an unprotected sender should be allowed on folders")
	}
	if !AllowedOnFolders("star", "vip@corp.com", entries) {
		t.Error("star on a protected sender should be allowed on folders")
	}
	// The Gmail reading stays: a label leaves the mail in the inbox there.
	if !Allowed("label", "vip@corp.com", entries) {
		t.Error("label on a protected sender should stay allowed off folders")
	}
}
