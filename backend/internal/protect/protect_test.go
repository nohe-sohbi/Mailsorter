package protect

import "testing"

func TestNormalizeAddress(t *testing.T) {
	cases := map[string]string{
		"Acme <hi@acme.com>":            "hi@acme.com",
		`"Jane Doe" <Jane@Example.COM>`: "jane@example.com",
		"  bob@host.org  ":             "bob@host.org",
		"No Address Here":               "no address here",
		"Broken <unterminated":         "unterminated",
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
		{"The Boss <boss@corp.com>", true},     // exact address
		{"boss@corp.com", true},                // bare address
		{"Boss <BOSS@CORP.COM>", true},         // case-insensitive
		{"colleague@corp.com", false},          // same domain, not listed as domain
		{"Promo <deals@acme.com>", true},       // domain entry
		{"News <news@mail.acme.com>", true},    // subdomain of domain entry
		{"news@notacme.com", false},            // suffix but not a subdomain
		{"someone@evilacme.com", false},        // must not match acme.com as substring
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
