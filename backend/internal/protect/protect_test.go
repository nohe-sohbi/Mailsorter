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
	}
	for in, want := range cases {
		if got := NormalizeAddress(in); got != want {
			t.Errorf("NormalizeAddress(%q) = %q, want %q", in, got, want)
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
		{"The Boss <boss@corp.com>", true},  // exact address
		{"boss@corp.com", true},             // bare address
		{"Boss <BOSS@CORP.COM>", true},      // case-insensitive
		{"colleague@corp.com", false},       // same domain, not listed as domain
		{"Promo <deals@acme.com>", true},    // domain entry
		{"News <news@mail.acme.com>", true}, // subdomain of domain entry
		{"news@notacme.com", false},         // suffix but not a subdomain
		{"someone@evilacme.com", false},     // must not match acme.com as substring
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
