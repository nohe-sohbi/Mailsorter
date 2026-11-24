package unsubscribe

import (
	"errors"
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
			want: Request{To: "list-request@host.com", Subject: "unsubscribe", Body: DefaultBody},
		},
		{
			name: "an address alone takes the default subject and body",
			raw:  "mailto:leave@lists.example.org",
			want: Request{To: "leave@lists.example.org", Subject: DefaultSubject, Body: DefaultBody},
		},
		{
			name: "the scheme in capitals",
			raw:  "MAILTO:leave@lists.example.org",
			want: Request{To: "leave@lists.example.org", Subject: DefaultSubject, Body: DefaultBody},
		},
		{
			name: "subject and body are percent-decoded, and a plus stays a plus",
			raw:  "mailto:stop@example.com?subject=D%C3%A9sabonnement%20lettre+1&body=STOP%0D%0Amerci",
			want: Request{To: "stop@example.com", Subject: "Désabonnement lettre+1", Body: "STOP\r\nmerci"},
			why:  "RFC 6068 is not a form encoding: + is not a space",
		},
		{
			name: "field names are matched case-insensitively",
			raw:  "mailto:stop@example.com?Subject=remove&BODY=please",
			want: Request{To: "stop@example.com", Subject: "remove", Body: "please"},
		},
		{
			name: "the address may come from a to field",
			raw:  "mailto:?to=stop@example.com&subject=remove",
			want: Request{To: "stop@example.com", Subject: "remove", Body: DefaultBody},
		},
		{
			name: "an encoded address",
			raw:  "mailto:stop%40example.com",
			want: Request{To: "stop@example.com", Subject: DefaultSubject, Body: DefaultBody},
		},
		{
			name: "a line break in the subject becomes a space",
			raw:  "mailto:stop@example.com?subject=a%0D%0ABcc:%20x@evil.example",
			want: Request{To: "stop@example.com", Subject: "a Bcc: x@evil.example", Body: DefaultBody},
			why:  "the subject is a header: a line break there would start another one",
		},
		{
			name: "the first subject wins",
			raw:  "mailto:stop@example.com?subject=one&subject=two",
			want: Request{To: "stop@example.com", Subject: "one", Body: DefaultBody},
		},
		{
			name: "an empty subject takes the default",
			raw:  "mailto:stop@example.com?subject=",
			want: Request{To: "stop@example.com", Subject: DefaultSubject, Body: DefaultBody},
		},
		{
			name: "an unknown field is ignored",
			raw:  "mailto:stop@example.com?in-reply-to=%3C1@x%3E",
			want: Request{To: "stop@example.com", Subject: DefaultSubject, Body: DefaultBody},
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
