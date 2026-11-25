// Package protecttest is test data: the From headers the reading of a
// sender (protect.Address) is held to, shared by the tests of protect, which
// read each header as the Gmail API hands it over, and of imap, which drops
// each one in a real IMAP server and lists it back, so that the two
// transports answer to one table. No production code imports it.
package protecttest

// Header is one From header, as its sender wrote it (what the Gmail API hands
// over), and the address protect.Address reads out of it ("" for none). Both
// transports must name that sender for a mail carrying it (imap's
// TestTheSameFromNamesTheSameSenderOnBothTransports): no row differs.
//
// Before is header lines written before From in the mail (CRLF-ended), for
// the rows about a block another header spoils. Second, when set, is a second
// From header after the first: then Want is what the transports read for the
// mail, which is no sender, whatever protect.Address reads of From alone.
type Header struct {
	Name, From, Want string
	Before, Second   string
}

// Headers is the table.
var Headers = []Header{
	// Ordinary headers, both transports.
	{Name: "quoted name", From: `"Nom" <a@b.fr>`, Want: "a@b.fr"},
	{Name: "bare address", From: "a@b.fr", Want: "a@b.fr"},
	{Name: "angle address alone", From: "<a@b.fr>", Want: "a@b.fr"},
	{Name: "a comment after", From: "Nom <a@b.fr> (commentaire)", Want: "a@b.fr"},
	{Name: "RFC 2047 name in UTF-8", From: "=?utf-8?q?R=C3=A9seau?= <news@reseau.fr>", Want: "news@reseau.fr"},
	{Name: "RFC 2047 name in windows-1252", From: "=?windows-1252?q?Caf=E9?= <cafe@b.fr>", Want: "cafe@b.fr"},
	{Name: "RFC 2047 name with a comma inside", From: "=?utf-8?q?Doe=2C_John?= <j@x.com>", Want: "j@x.com"},
	{Name: "case kept", From: "Nom <A@B.FR>", Want: "A@B.FR"},
	{Name: "surrounding spaces", From: "  bare@acme.com  ", Want: "bare@acme.com"},
	{Name: "escaped quote in the name", From: `"a\" <b@c.fr" <real@journal.fr>`, Want: "real@journal.fr"},
	// Gmail headers whose name nobody quoted: read again with it in quotes.
	{Name: "the address as the name", From: "john@x.com <john@x.com>", Want: "john@x.com"},
	{Name: "a comma in the name", From: "Doe, John <j@x.com>", Want: "j@x.com"},
	{Name: "brackets in the name", From: "LinkedIn [Jobs] <jobs@linkedin.com>", Want: "jobs@linkedin.com"},
	{Name: "a colon in the name", From: "News: daily <n@b.fr>", Want: "n@b.fr"},
	{Name: "another address as the name", From: "boss@corp.com <spam@evil.io>", Want: "spam@evil.io"},
	// The traps: never the address of the display name.
	{Name: "an address in a quoted name", From: `"<patron@victime.fr>" <lettre@journal.fr>`, Want: "lettre@journal.fr"},
	{Name: "a name and address in a quoted name", From: `"Boss <boss@corp.com>" <spam@evil.io>`, Want: "spam@evil.io"},
	{Name: "an address in a comment", From: "<vrai@x.fr> (<boss@corp.com>)", Want: "vrai@x.fr"},
	{Name: "a quoted local part", From: `"<boss@corp.com>"@evil.io`, Want: `"<boss@corp.com>"@evil.io`},
	{Name: "a quoted local part in angles", From: `Spam <"<boss@corp.com>"@evil.io>`, Want: `"<boss@corp.com>"@evil.io`},
	{Name: "a space in a quoted local part", From: `"a b"@x.com`, Want: `"a b"@x.com`},
	// What does not read as one address gives none.
	{Name: "two angle addresses", From: "<patron@victime.fr> <lettre@journal.fr>", Want: ""},
	{Name: "an unquoted name holding an angle address", From: "Patron <patron@victime.fr> <lettre@journal.fr>", Want: ""},
	{Name: "a quoted local part, then a comma", From: `"<boss@corp.com>"@evil.io,`, Want: ""},
	{Name: "a quoted local part unquoted", From: "<boss@corp.com>@evil.io", Want: ""},
	{Name: "trailing words", From: "Name <x@y.com> trail", Want: ""},
	{Name: "unterminated", From: "Broken <unterminated", Want: ""},
	{Name: "no address", From: "No Address Here", Want: ""},
	{Name: "two addresses", From: "a@x.fr, b@x.fr", Want: ""},
	// Several authors name no sender: none of them is taken for it.
	{Name: "two authors, the second with a name", From: "patron@victime.fr, Lettre <lettre@journal.fr>", Want: ""},
	{Name: "two authors with names", From: "Patron <patron@victime.fr>, Lettre <lettre@journal.fr>", Want: ""},
	{Name: "a group, then an author", From: "Undisclosed: a@x.fr; <b@x.fr>", Want: ""},
	{Name: "an author, then a name nobody quoted", From: "patron@victime.fr, Doe, John <j@x.com>", Want: ""},
	{Name: "a group in a name nobody quoted", From: "Liste; a@x.fr <b@x.fr>", Want: ""},
	{Name: "the IMAP listing's list of two", From: `patron@victime.fr, "Lettre" <lettre@journal.fr>`, Want: ""},
	{Name: "the IMAP listing's name without an address", From: `"patron@victime.fr"`, Want: ""},
	// From the reviews.
	{Name: "apostrophes around the address", From: "'patron@victime.fr'", Want: "'patron@victime.fr'"},
	{Name: "a group of one", From: "Undisclosed: a@x.fr;", Want: ""},
	{Name: "a group of one with a name", From: "Equipe: Lettre <lettre@journal.fr>;", Want: ""},
	{Name: "an empty group", From: "Undisclosed recipients:;", Want: ""},
	{Name: "an empty group, then an author", From: "G:;, Lettre <lettre@journal.fr>", Want: ""},
	{Name: "a group's colon in a name nobody quoted", From: "Liste: a@x.fr <b@x.fr>", Want: ""},
	{Name: "a raw latin-1 name", From: "Caf\xe9 <cafe@b.fr>", Want: "cafe@b.fr"},
	{Name: "an encoded name holding a line break", From: "=?utf-8?q?a=0Ab?= <a@b.fr>", Want: "a@b.fr"},
	// A block another header spoils, and control bytes in From itself.
	{Name: "a control byte in a List-Unsubscribe before From", Before: "List-Unsubscribe: <mailto:stop\x01@journal.fr>\r\n", From: "Lettre <lettre@journal.fr>", Want: "lettre@journal.fr"},
	{Name: "a DEL byte in a List-Unsubscribe before From", Before: "List-Unsubscribe: <https://journal.fr/stop\x7f>\r\n", From: "Lettre <lettre@journal.fr>", Want: "lettre@journal.fr"},
	{Name: "a folded List-Unsubscribe before From", Before: "List-Unsubscribe: <mailto:stop@journal.fr>,\r\n <https://journal.fr/stop>\r\n", From: "Lettre <lettre@journal.fr>", Want: "lettre@journal.fr"},
	{Name: "a control byte in the name", From: "Lettre\x01 <lettre@journal.fr>", Want: ""},
	{Name: "a DEL byte in the name", From: "Lettre\x7f <lettre@journal.fr>", Want: ""},
	{Name: "a control byte in a quoted name", From: "\"Lettre\x01\" <lettre@journal.fr>", Want: ""},
	{Name: "a control byte in a From that names nobody", From: "Broken\x01 <unterminated", Want: ""},
	// Two From headers name no single sender.
	{Name: "two From headers", From: "Lettre <lettre@journal.fr>", Second: "Patron <patron@victime.fr>", Want: ""},
	{Name: "two From headers, the same", From: "lettre@journal.fr", Second: "lettre@journal.fr", Want: ""},
	{Name: "empty", From: "", Want: ""},
}
