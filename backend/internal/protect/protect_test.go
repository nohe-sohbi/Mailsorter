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
			if n := NormalizeAddress(tc.From); n != strings.ToLower(tc.Want) {
				t.Errorf("NormalizeAddress(%q) = %q, want %q", tc.From, n, strings.ToLower(tc.Want))
			}
		})
	}
}

// "Protéger" on a mail stores EntryFromMail of its From, and the shield
// compares senders with Match: for every header that names a sender, the one
// must find the other. 'patron@victime.fr' is such an address, apostrophes
// included: trimmed (the typed-entry path), it protected nobody.
func TestProtectingAMailShieldsItsSender(t *testing.T) {
	froms := []string{`"a b"@x.com`, `"Ami" <"a b"@x.com>`, `"<boss@corp.com>"@evil.io`, `"jean dupont"@exemple.fr`, "'patron@victime.fr'"}
	for _, tc := range protecttest.Headers {
		if tc.Want != "" && tc.Second == "" {
			froms = append(froms, tc.From)
		}
	}
	for _, from := range froms {
		value := EntryFromMail(from)
		if value == "" || !Match(from, []string{value}) {
			t.Errorf("EntryFromMail(%q) = %q, and Match(%q) on it = %v, want an entry that matches", from, value, from, Match(from, []string{value}))
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

// What a screen shows for a sender: never an address of the display name
// that is not the sender's, and no control byte.
func TestLabel(t *testing.T) {
	cases := map[string]string{
		`"boss@corp.com" <evil@evil.io>`:                "evil@evil.io",
		"boss@corp.com <evil@evil.io>":                  "evil@evil.io",
		`"Evil@Evil.io" <evil@evil.io>`:                 "Evil@Evil.io",
		`"<patron@victime.fr>" <lettre@journal.fr>`:     "lettre@journal.fr",
		"Ma Banque <info@banque.fr>":                    "Ma Banque",
		"info@banque.fr":                                "info@banque.fr",
		"=?utf-8?q?R=C3=A9seau?= <news@reseau.fr>":      "Réseau",
		`"Lettre <lettre@journal.fr>, Patron <p@v.fr>"`: "Lettre <lettre@journal.fr>, Patron <p@v.fr>",
		"Broken\x01 <unterminated":                      "Broken  <unterminated",
		"patron@victime.fr, Lettre <lettre@journal.fr>": "patron@victime.fr, Lettre <lettre@journal.fr>",
		"": "",
	}
	for from, want := range cases {
		if got := Label(from); got != want {
			t.Errorf("Label(%q) = %q, want %q", from, got, want)
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
