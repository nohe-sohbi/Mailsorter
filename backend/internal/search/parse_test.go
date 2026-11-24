package search

import (
	"reflect"
	"testing"
	"time"
)

func TestParseReadsTheQueriesTheAppProduces(t *testing.T) {
	// Every one of these is a string the SPA actually sends: the default query,
	// the filter chips, the stats tile, and hand-typed ones.
	cases := []struct {
		name  string
		query string
		want  Criteria
	}{
		{
			name:  "the default query on every page load",
			query: "in:inbox",
			want:  Criteria{Folder: FolderInbox},
		},
		{
			name:  "the unread chip",
			query: "in:inbox is:unread",
			want:  Criteria{Folder: FolderInbox, Unread: boolPtr(false)},
		},
		{
			name:  "the today chip",
			query: "in:inbox newer_than:1d",
			want:  Criteria{Folder: FolderInbox, NewerThan: 24 * time.Hour},
		},
		{
			name:  "the favorites chip",
			query: "in:inbox is:starred",
			want:  Criteria{Folder: FolderInbox, Starred: true},
		},
		{
			name:  "the attachments chip",
			query: "in:inbox has:attachment",
			want:  Criteria{Folder: FolderInbox, HasAttachment: true},
		},
		{
			name:  "the large mail chip",
			query: "in:inbox larger:5M",
			want:  Criteria{Folder: FolderInbox, Larger: 5 << 20},
		},
		{
			name:  "a size window",
			query: "larger:100K smaller:2M",
			want:  Criteria{Larger: 100 << 10, Smaller: 2 << 20},
		},
		{
			name:  "two bounds of one kind keep the narrower",
			query: "larger:1M larger:5M smaller:3M smaller:4M",
			want:  Criteria{Larger: 5 << 20, Smaller: 3 << 20},
		},
		{
			name:  "a hand-typed one",
			query: "in:inbox from:linkedin.com older_than:7d",
			want: Criteria{
				Folder:    FolderInbox,
				From:      []string{"linkedin.com"},
				OlderThan: 7 * 24 * time.Hour,
			},
		},
		{
			name:  "free text",
			query: "facture",
			want:  Criteria{Text: []string{"facture"}},
		},
		{
			name:  "a quoted value stays one term",
			query: `from:"Acme Corp"`,
			want:  Criteria{From: []string{"Acme Corp"}},
		},
		{
			name:  "an empty query constrains nothing",
			query: "",
			want:  Criteria{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, unsupported := Parse(tc.query)
			if len(unsupported) != 0 {
				t.Errorf("Parse(%q) called %v unsupported, want none", tc.query, unsupported)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Parse(%q) = %+v, want %+v", tc.query, got, tc.want)
			}
		})
	}
}

// The reason this function returns two values instead of one.
//
// A dropped filter does not look like a failure, it looks like a result: the
// user clicks "Pieces jointes" and gets their whole inbox, with nothing on
// screen saying the filter did nothing. Naming the term is what lets the caller
// say so.
func TestParseNamesWhatAStoredMailboxCannotAnswer(t *testing.T) {
	cases := map[string][]string{
		"in:inbox has:drive":        {"has:drive"},
		"in:inbox is:important":     {"is:important"},
		"in:sent":                   {"in:sent"},
		"label:Promotions":          {"label:Promotions"},
		"smaller:1M filename:pdf":   {"filename:pdf"},
		"larger:5G":                 {"larger:5G"},
		"larger:big smaller:0":      {"larger:big", "smaller:0"},
		"has:attachment category:x": {"category:x"},
	}
	for query, want := range cases {
		_, got := Parse(query)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Parse(%q) unsupported = %v, want %v", query, got, want)
		}
	}
}

// And the supported half of the same query still comes back, so a caller that
// chooses to ignore the unsupported term is not left with nothing.
func TestParseKeepsWhatItUnderstandsAlongsideWhatItCannot(t *testing.T) {
	crit, unsupported := Parse("in:inbox is:unread label:Factures")

	if len(unsupported) != 1 || unsupported[0] != "label:Factures" {
		t.Errorf("unsupported = %v, want [label:Factures]", unsupported)
	}
	if crit.Folder != FolderInbox {
		t.Errorf("Folder = %q, want %q", crit.Folder, FolderInbox)
	}
	if crit.Unread == nil || *crit.Unread {
		t.Errorf("Unread = %v, want a pointer to false", crit.Unread)
	}
}

// An operator nobody has ever heard of is text, which is what Gmail does with
// it. Calling it unsupported would refuse a query that merely contains a colon,
// and colons turn up in subjects constantly ("Re: votre commande").
func TestParseTreatsAnUnknownOperatorAsText(t *testing.T) {
	crit, unsupported := Parse("wibble:wobble")
	if len(unsupported) != 0 {
		t.Errorf("unsupported = %v, want none: an unknown operator is text", unsupported)
	}
	if !reflect.DeepEqual(crit.Text, []string{"wibble:wobble"}) {
		t.Errorf("Text = %v, want the whole token", crit.Text)
	}
}

func TestParseAge(t *testing.T) {
	const day = 24 * time.Hour
	cases := map[string]struct {
		want time.Duration
		ok   bool
	}{
		"1d":  {day, true},
		"7d":  {7 * day, true},
		"2m":  {60 * day, true},
		"1y":  {365 * day, true},
		"1D":  {day, true},
		"0d":  {0, false},
		"-1d": {0, false},
		"d":   {0, false},
		"":    {0, false},
		"7":   {0, false},
		"7w":  {0, false},
		"abc": {0, false},
	}
	for in, tc := range cases {
		got, ok := parseAge(in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseAge(%q) = %v, %v; want %v, %v", in, got, ok, tc.want, tc.ok)
		}
	}
}

// A size is bytes, or kilobytes and megabytes of 1024: what the filter
// "Volumineux" (larger:5M) and a hand-typed bound send.
func TestParseSize(t *testing.T) {
	cases := map[string]struct {
		want int64
		ok   bool
	}{
		"5M":                   {5 << 20, true},
		"5m":                   {5 << 20, true},
		"100K":                 {100 << 10, true},
		"100k":                 {100 << 10, true},
		"5000000":              {5_000_000, true},
		"1":                    {1, true},
		"0":                    {0, false},
		"0M":                   {0, false},
		"-1M":                  {0, false},
		"M":                    {0, false},
		"":                     {0, false},
		"5G":                   {0, false},
		"5MB":                  {0, false},
		"1.5M":                 {0, false},
		"abc":                  {0, false},
		"9223372036854775807M": {0, false},
		"9223372036854775807":  {9223372036854775807, true},
	}
	for in, tc := range cases {
		got, ok := parseSize(in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseSize(%q) = %v, %v; want %v, %v", in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestCriteriaEmpty(t *testing.T) {
	if crit, _ := Parse(""); !crit.Empty() {
		t.Errorf("Parse(\"\") = %+v, want Empty()", crit)
	}
	// An unsupported term alone constrains nothing either, which is exactly why
	// a caller must not treat "no criteria" as "no filter asked for".
	if crit, _ := Parse("label:Factures"); !crit.Empty() {
		t.Errorf("Parse(%q) = %+v, want Empty()", "label:Factures", crit)
	}
	for _, query := range []string{"in:inbox", "is:starred", "has:attachment", "larger:5M", "smaller:1K"} {
		if crit, _ := Parse(query); crit.Empty() {
			t.Errorf("Parse(%q) reads as empty although it constrained something", query)
		}
	}
}

func TestSplitTerm(t *testing.T) {
	cases := []struct {
		token        string
		wantOperator string
		wantValue    string
	}{
		{"from:a@b.com", "from", "a@b.com"},
		{"FROM:a@b.com", "from", "a@b.com"},
		{`from:"Acme Corp"`, "from", "Acme Corp"},
		{"facture", "", "facture"},
		{":leading", "", ":leading"},
		{"trailing:", "", "trailing:"},
		{"", "", ""},
	}
	for _, tc := range cases {
		operator, value := splitTerm(tc.token)
		if operator != tc.wantOperator || value != tc.wantValue {
			t.Errorf("splitTerm(%q) = %q, %q; want %q, %q",
				tc.token, operator, value, tc.wantOperator, tc.wantValue)
		}
	}
}
