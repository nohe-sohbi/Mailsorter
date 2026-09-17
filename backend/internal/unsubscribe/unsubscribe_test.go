package unsubscribe

import "testing"

func TestSplitAngleList(t *testing.T) {
	got := SplitAngleList("<https://x.com/u>, <mailto:u@x.com>")
	if len(got) != 2 || got[0] != "https://x.com/u" || got[1] != "mailto:u@x.com" {
		t.Fatalf("SplitAngleList parsed %#v", got)
	}
	if len(SplitAngleList("")) != 0 {
		t.Error("empty input should yield no entries")
	}
	// Senders are inconsistent about whitespace and about wrapping at all.
	got = SplitAngleList("  <https://x.com/u>  ,  mailto:u@x.com  ")
	if len(got) != 2 || got[0] != "https://x.com/u" || got[1] != "mailto:u@x.com" {
		t.Errorf("SplitAngleList parsed %#v, want both entries trimmed and unwrapped", got)
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		name   string
		header string
		post   string
		want   Links
		why    string
	}{
		{
			name:   "both kinds, one-click advertised",
			header: "<https://x.com/u>, <mailto:u@x.com>",
			post:   "List-Unsubscribe=One-Click",
			want:   Links{URL: "https://x.com/u", Mailto: "mailto:u@x.com", OneClick: true},
		},
		{
			name:   "mailto only cannot be one-click",
			header: "<mailto:u@x.com>",
			post:   "List-Unsubscribe=One-Click",
			want:   Links{Mailto: "mailto:u@x.com"},
			why:    "a sender advertising the header without an endpoint has nothing to post to",
		},
		{
			name:   "a url without the post header is a link to open, not to post",
			header: "<https://x.com/u>",
			want:   Links{URL: "https://x.com/u"},
		},
		{
			name:   "the first url wins",
			header: "<https://preferred.example/u>, <https://fallback.example/u>",
			want:   Links{URL: "https://preferred.example/u"},
			why:    "senders put the endpoint they prefer first; taking the last routes users to the fallback",
		},
		{
			name:   "nothing at all",
			header: "",
			want:   Links{},
		},
		{
			name:   "an unrelated scheme is neither",
			header: "<ftp://x.com/u>",
			want:   Links{},
		},
		{
			name:   "the post header is matched case-insensitively",
			header: "<https://x.com/u>",
			post:   "list-unsubscribe=one-click",
			want:   Links{URL: "https://x.com/u", OneClick: true},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Parse(tc.header, tc.post)
			if got != tc.want {
				msg := ""
				if tc.why != "" {
					msg = ": " + tc.why
				}
				t.Errorf("Parse(%q, %q) = %+v, want %+v%s", tc.header, tc.post, got, tc.want, msg)
			}
		})
	}
}

func TestAny(t *testing.T) {
	cases := map[bool]Links{
		true:  {URL: "https://x.com/u"},
		false: {},
	}
	for want, links := range cases {
		if got := links.Any(); got != want {
			t.Errorf("Links%+v.Any() = %v, want %v", links, got, want)
		}
	}
	if !(Links{Mailto: "mailto:u@x.com"}).Any() {
		t.Error("a mailto-only message still offers a way out")
	}
}
