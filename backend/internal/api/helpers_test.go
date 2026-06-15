package api

import "testing"

func TestExtractDomain(t *testing.T) {
	cases := map[string]string{
		"Acme <hello@acme.com>": "acme.com",
		"hello@acme.com":        "acme.com",
		"no-at-sign":            "no-at-sign",
		`"Big Co" <a@b.co.uk>`:  "b.co.uk",
	}
	for in, want := range cases {
		if got := extractDomain(in); got != want {
			t.Errorf("extractDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtractSenderName(t *testing.T) {
	cases := map[string]string{
		"Acme Inc <hello@acme.com>":   "Acme Inc",
		`"Acme Inc" <hello@acme.com>`: "Acme Inc",
		"plain@acme.com":              "plain@acme.com",
	}
	for in, want := range cases {
		if got := extractSenderName(in); got != want {
			t.Errorf("extractSenderName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtractSenderAddress(t *testing.T) {
	cases := map[string]string{
		"Acme <hello@acme.com>": "hello@acme.com",
		"  bare@acme.com  ":     "bare@acme.com",
		"Name <x@y.com> trail":  "x@y.com",
	}
	for in, want := range cases {
		if got := extractSenderAddress(in); got != want {
			t.Errorf("extractSenderAddress(%q) = %q, want %q", in, got, want)
		}
	}
}
