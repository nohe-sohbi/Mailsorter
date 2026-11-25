package unsubscribe

import (
	"errors"
	"strings"
	"testing"
)

func TestParseMailto(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want Request
		why  string
	}{
		{
			name: "the shape of RFC 2369's example",
			raw:  "mailto:list-request@host.com?subject=unsubscribe",
			want: Request{To: "list-request@host.com", Subject: "unsubscribe"},
		},
		{
			name: "an address alone takes the default subject",
			raw:  "mailto:leave@lists.example.org",
			want: Request{To: "leave@lists.example.org", Subject: DefaultSubject},
		},
		{
			name: "the scheme in capitals",
			raw:  "MAILTO:leave@lists.example.org",
			want: Request{To: "leave@lists.example.org", Subject: DefaultSubject},
		},
		{
			name: "the subject is percent-decoded, and a plus stays a plus",
			raw:  "mailto:stop@example.com?subject=unsubscribe%20lettre+1",
			want: Request{To: "stop@example.com", Subject: "unsubscribe lettre+1"},
			why:  "RFC 6068 is not a form encoding: + is not a space",
		},
		{
			name: "a command word in capitals, with a key after a colon",
			raw:  "mailto:stop@example.com?subject=UNSUBSCRIBE:news-8f3a",
			want: Request{To: "stop@example.com", Subject: "UNSUBSCRIBE:news-8f3a"},
		},
		{
			name: "the French command word, accented",
			raw:  "mailto:stop@example.com?subject=D%C3%A9sinscription",
			want: Request{To: "stop@example.com", Subject: "Désinscription"},
		},
		{
			name: "a command word and its token, glued",
			raw:  "mailto:stop@example.com?subject=unsub_8f3a@list",
			want: Request{To: "stop@example.com", Subject: "unsub_8f3a@list"},
		},
		{
			name: "one token alone",
			raw:  "mailto:stop@example.com?subject=leave-8f3a.list:42",
			want: Request{To: "stop@example.com", Subject: "leave-8f3a.list:42"},
		},
		{
			name: "field names are matched case-insensitively",
			raw:  "mailto:stop@example.com?Subject=remove",
			want: Request{To: "stop@example.com", Subject: "remove"},
		},
		{
			name: "the address may come from a to field",
			raw:  "mailto:?to=stop@example.com&subject=remove",
			want: Request{To: "stop@example.com", Subject: "remove"},
		},
		{
			name: "an encoded address",
			raw:  "mailto:stop%40example.com",
			want: Request{To: "stop@example.com", Subject: DefaultSubject},
		},
		{
			name: "the body the link asks for is ignored",
			raw:  "mailto:stop@example.com?body=Je%20d%C3%A9missionne",
			want: Request{To: "stop@example.com", Subject: DefaultSubject},
			why:  "the text of the mail is always Body: a stranger never writes it",
		},
		{
			name: "the first subject wins",
			raw:  "mailto:stop@example.com?subject=one&subject=two",
			want: Request{To: "stop@example.com", Subject: "one"},
		},
		{
			name: "an empty subject takes the default",
			raw:  "mailto:stop@example.com?subject=",
			want: Request{To: "stop@example.com", Subject: DefaultSubject},
		},
		{
			name: "a token of 100 characters",
			raw:  "mailto:stop@example.com?subject=" + strings.Repeat("a", 100),
			want: Request{To: "stop@example.com", Subject: strings.Repeat("a", 100)},
		},
		{
			name: "an unknown field is ignored",
			raw:  "mailto:stop@example.com?in-reply-to=%3C1@x%3E",
			want: Request{To: "stop@example.com", Subject: DefaultSubject},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseMailto(tc.raw)
			if err != nil || got != tc.want {
				msg := ""
				if tc.why != "" {
					msg = ": " + tc.why
				}
				t.Errorf("ParseMailto(%q) = %+v, %v, want %+v%s", tc.raw, got, err, tc.want, msg)
			}
		})
	}
}

// What is refused, and why: the mail leaves from the person's own address,
// to an address a stranger wrote in a header.
func TestParseMailtoRefuses(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want error
	}{
		{"an https link", "https://example.com/u", ErrNotMailto},
		{"nothing", "", ErrNotMailto},
		{"no address", "mailto:?subject=unsubscribe", ErrNoRecipient},
		{"two addresses in the path", "mailto:a@example.com,b@example.com", ErrManyRecipients},
		{"a second one in to", "mailto:a@example.com?to=b@example.com", ErrManyRecipients},
		{"a copy in cc", "mailto:a@example.com?cc=b@example.com", ErrManyRecipients},
		{"a hidden copy in bcc", "mailto:a@example.com?bcc=b@example.com", ErrManyRecipients},
		{"a display name", "mailto:Liste%20%3Ca@example.com%3E", ErrBadRecipient},
		{"a quoted local part", "mailto:%22a%20b%22@example.com", ErrBadRecipient},
		{"not an address", "mailto:nobody", ErrBadRecipient},
		{"a line break in the address", "mailto:a@example.com%0D%0ABcc:b@example.com", ErrBadRecipient},
		{"a line break in the subject", "mailto:a@example.com?subject=a%0D%0ABcc:%20x@evil.example", ErrBadSubject},
		{"a tab in the subject", "mailto:a@example.com?subject=a%09b", ErrBadSubject},
		{"a right-to-left override in the subject", "mailto:a@example.com?subject=a%E2%80%AEb", ErrBadSubject},
		{"a zero-width space in the subject", "mailto:a@example.com?subject=a%E2%80%8Bb", ErrBadSubject},
		{"a zero-width joiner in the subject", "mailto:a@example.com?subject=a%E2%80%8Db", ErrBadSubject},
		{"a byte order mark in the subject", "mailto:a@example.com?subject=%EF%BB%BFstop", ErrBadSubject},
		{"a line separator in the subject", "mailto:a@example.com?subject=a%E2%80%A8b", ErrBadSubject},
		{"a paragraph separator in the subject", "mailto:a@example.com?subject=a%E2%80%A9b", ErrBadSubject},
		{"a token over 100 characters", "mailto:a@example.com?subject=" + strings.Repeat("a", 101), ErrBadSubject},
		{"a command and a token over 80 characters", "mailto:a@example.com?subject=stop%20" + strings.Repeat("a", 81), ErrBadSubject},
		{"a sentence", "mailto:a@example.com?subject=Je%20d%C3%A9missionne%20avec%20effet%20imm%C3%A9diat", ErrBadSubject},
		{"a command word, then a sentence", "mailto:a@example.com?subject=unsubscribe%20I%20quit", ErrBadSubject},
		{"a confirmation of someone else's token", "mailto:a@example.com?subject=confirm%20a1b2c3d4", ErrBadSubject},
		{"accents in a token", "mailto:a@example.com?subject=d%C3%A9mission", ErrBadSubject},
		{"a broken escape", "mailto:a@example.com?subject=%zz", ErrBadEncoding},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseMailto(tc.raw)
			if !errors.Is(err, tc.want) {
				t.Errorf("ParseMailto(%q) = %+v, %v, want %v", tc.raw, got, err, tc.want)
			}
		})
	}
}

// ForSender is the decision to write the mail without showing it: only to
// the sender's own registrable domain. The three abuses it exists for are
// the first three cases: a stranger making the person join a group, confirm
// someone else's token, and write to their boss.
func TestForSender(t *testing.T) {
	cases := []struct {
		name   string
		mailto string
		sender string
		want   Request
		err    error
	}{
		{
			name:   "joining a group in the person's name",
			mailto: "mailto:groupe+subscribe@googlegroups.com",
			sender: "promo@spam.example",
			err:    ErrOtherDomain,
		},
		{
			// Refused twice over: the address is elsewhere, and the subject is
			// no unsubscribe command. The subject is read first.
			name:   "confirming a token in the person's name",
			mailto: "mailto:groupe+subscribe@googlegroups.com?subject=confirm%20a1b2c3d4",
			sender: "promo@spam.example",
			err:    ErrBadSubject,
		},
		{
			name:   "writing to the person's boss",
			mailto: "mailto:patron@entreprise.fr?body=Je%20d%C3%A9missionne",
			sender: "promo@spam.example",
			err:    ErrOtherDomain,
		},
		{
			name:   "the boss's own domain still gets no body of the stranger's",
			mailto: "mailto:patron@entreprise.fr?body=Je%20d%C3%A9missionne",
			sender: "rh@entreprise.fr",
			want:   Request{To: "patron@entreprise.fr", Subject: DefaultSubject},
		},
		{
			name:   "the list's own address",
			mailto: "mailto:desabonnement@journal.fr?subject=unsubscribe",
			sender: "lettre@journal.fr",
			want:   Request{To: "desabonnement@journal.fr", Subject: "unsubscribe"},
		},
		{
			name:   "a subdomain of the sender's domain, in capitals",
			mailto: "mailto:stop@Listes.Journal.FR",
			sender: "lettre@news.journal.fr",
			want:   Request{To: "stop@Listes.Journal.FR", Subject: DefaultSubject},
		},
		{
			name:   "a two-label public suffix",
			mailto: "mailto:leave@lists.shop.co.uk",
			sender: "news@shop.co.uk",
			want:   Request{To: "leave@lists.shop.co.uk", Subject: DefaultSubject},
		},
		{
			name:   "another site on the same two-label public suffix",
			mailto: "mailto:leave@other.co.uk",
			sender: "news@shop.co.uk",
			err:    ErrOtherDomain,
		},
		{
			name:   "another site on a private public suffix",
			mailto: "mailto:leave@bar.github.io",
			sender: "news@foo.github.io",
			err:    ErrOtherDomain,
		},
		{
			name:   "a look-alike domain",
			mailto: "mailto:stop@journal.fr.evil.example",
			sender: "lettre@journal.fr",
			err:    ErrOtherDomain,
		},
		{
			name:   "a sender without a domain matches nothing",
			mailto: "mailto:stop@journal.fr",
			sender: "lettre",
			err:    ErrBadSender,
		},
		{
			name:   "an address hidden in the display name is not the sender",
			mailto: "mailto:patron@victime.fr?subject=stop",
			sender: `"<patron@victime.fr>" <lettre@journal.fr>`,
			err:    ErrOtherDomain,
		},
		{
			name:   "the same, unquoted",
			mailto: "mailto:patron@victime.fr",
			sender: `Patron <patron@victime.fr> <lettre@journal.fr>`,
			err:    ErrBadSender,
		},
		{
			name:   "the real address behind such a display name still unsubscribes at home",
			mailto: "mailto:desabonnement@journal.fr",
			sender: `"<patron@victime.fr>" <lettre@journal.fr>`,
			want:   Request{To: "desabonnement@journal.fr", Subject: DefaultSubject},
		},
		{
			name:   "an address quoted as a local part is the sender's own, on its domain",
			mailto: "mailto:boss@corp.com",
			sender: `"<boss@corp.com>"@evil.io`,
			err:    ErrOtherDomain,
		},
		{
			name:   "the same, unquoted, reads as no address at all",
			mailto: "mailto:boss@corp.com",
			sender: `<boss@corp.com>@evil.io`,
			err:    ErrBadSender,
		},
		{
			name:   "a From of two addresses",
			mailto: "mailto:stop@journal.fr",
			sender: "Journal <lettre@journal.fr>, Autre <x@journal.fr>",
			err:    ErrBadSender,
		},
		{
			name:   "an empty From",
			mailto: "mailto:stop@journal.fr",
			sender: "",
			err:    ErrBadSender,
		},
		{
			name:   "two IP addresses are no shared domain",
			mailto: "mailto:stop@10.0.0.1",
			sender: "lettre@192.168.0.1",
			err:    ErrOtherDomain,
		},
		{
			name:   "two domain literals are no shared domain",
			mailto: "mailto:stop@[10.0.0.1]",
			sender: "lettre@[10.0.0.1]",
			err:    ErrOtherDomain,
		},
		{
			name:   "a refused mailto stays refused on the right domain",
			mailto: "mailto:a@journal.fr,b@journal.fr",
			sender: "lettre@journal.fr",
			err:    ErrManyRecipients,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ForSender(tc.mailto, tc.sender)
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Errorf("ForSender(%q, %q) = %+v, %v, want %v", tc.mailto, tc.sender, got, err, tc.err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Errorf("ForSender(%q, %q) = %+v, %v, want %+v", tc.mailto, tc.sender, got, err, tc.want)
			}
		})
	}
}

func TestSameRegistrableDomain(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"a@journal.fr", "b@journal.fr", true},
		{"a@x.journal.fr", "b@y.journal.fr", true},
		{"a@journal.fr.", "b@JOURNAL.fr", true},
		{"a@journal.fr", "b@journal.com", false},
		{"a@fr", "b@fr", false},
		{"a@co.uk", "b@co.uk", false},
		{"a@", "b@", false},
		{"a@10.0.0.1", "b@192.168.0.1", false},
		{"a@10.0.0.1", "b@10.0.0.1", false},
		{"a@[10.0.0.1]", "b@[10.0.0.1]", false},
		{"a@[IPv6:2001:db8::1]", "b@[IPv6:2001:db8::1]", false},
		{"a@2001:db8::1", "b@2001:db8::1", false},
		{"", "", false},
	}
	for _, tc := range cases {
		if got := SameRegistrableDomain(tc.a, tc.b); got != tc.want {
			t.Errorf("SameRegistrableDomain(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
