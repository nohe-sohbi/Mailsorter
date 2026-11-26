package imap

import (
	"reflect"
	"testing"
)

// The tolerant reader reads every header of a block whatever another one
// holds: a control byte, a line with no colon, a fold, LF alone.
func TestReadHeadersIsTolerant(t *testing.T) {
	raw := []byte("List-Unsubscribe: <mailto:stop\x01@journal.fr>,\r\n" +
		"\t<https://journal.fr/stop\x7f>\r\n" +
		"not a header line\r\n" +
		": no name\r\n" +
		"From: Lettre\r\n <lettre@journal.fr>\r\n" +
		"FROM: Patron <patron@victime.fr>\n" +
		"X-Bytes: caf\xe9\r\n" +
		"\r\n" +
		"From: after the block <x@y.fr>\r\n")
	got := readHeaders(raw)
	want := headerFields{
		"list-unsubscribe": {"<mailto:stop\x01@journal.fr>,\t<https://journal.fr/stop\x7f>"},
		"from":             {"Lettre <lettre@journal.fr>", "Patron <patron@victime.fr>"},
		"x-bytes":          {"caf\xe9"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("readHeaders = %q, want %q", got, want)
	}
	if v := got.first("From"); v != "Lettre <lettre@journal.fr>" {
		t.Errorf("first(From) = %q, want the first one", v)
	}
	if v := got.first("Missing"); v != "" {
		t.Errorf("first(Missing) = %q, want none", v)
	}
}
