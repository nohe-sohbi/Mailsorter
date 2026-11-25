package protect

import "testing"

func TestNormalizeAddress(t *testing.T) {
	cases := map[string]string{
		"Acme <hi@acme.com>":            "hi@acme.com",
		`"Jane Doe" <Jane@Example.COM>`: "jane@example.com",
		"  bob@host.org  ":              "bob@host.org",
		"No Address Here":               "no address here",
		"Broken <unterminated":          "unterminated",
		"":                              "",
		// The display name is the sender's: an address in it is not theirs.
		`"<boss@corp.com>" <spam@evil.io>`:         "spam@evil.io",
		`"Boss <boss@corp.com>" <spam@evil.io>`:    "spam@evil.io",
		`<boss@corp.com> <spam@evil.io>`:           "spam@evil.io",
		`boss@corp.com <spam@evil.io>`:             "spam@evil.io",
		`Spam <spam@evil.io> (boss@corp.com)`:      "spam@evil.io",
		`=?utf-8?q?R=C3=A9seau?= <news@reseau.fr>`: "news@reseau.fr",
	}
	for in, want := range cases {
		if got := NormalizeAddress(in); got != want {
			t.Errorf("NormalizeAddress(%q) = %q, want %q", in, got, want)
		}
	}
}

// Address keeps the address as written (NormalizeAddress lowercases it), and
// never takes it from the display name.
func TestAddress(t *testing.T) {
	cases := map[string]string{
		"Lettre <Lettre@Journal.fr>":                "Lettre@Journal.fr",
		`"<patron@victime.fr>" <lettre@journal.fr>`: "lettre@journal.fr",
		`<patron@victime.fr> <lettre@journal.fr>`:   "lettre@journal.fr",
		"  bare@acme.com  ":                         "bare@acme.com",
		"Name <x@y.com> trail":                      "x@y.com",
		`"a\" <b@c.fr" <real@journal.fr>`:           "real@journal.fr",
	}
	for in, want := range cases {
		if got := Address(in); got != want {
			t.Errorf("Address(%q) = %q, want %q", in, got, want)
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
		{`"boss@corp.com"`, "boss@corp.com", KindAddress},
		{"", "", ""},
		{"@", "", ""},
		{"@bad", "bad", KindDomain},
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
