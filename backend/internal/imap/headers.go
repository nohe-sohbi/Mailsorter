package imap

import (
	"bytes"
	"strings"

	imapv2 "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/nohe-sohbi/mailsorter/backend/internal/protect"
)

// headerFields is a header block, every header by its lowercase name, its
// values in the order they came.
type headerFields map[string][]string

// readHeaders reads a header block the tolerant way, by hand.
//
// The standard MIME reader stops at the first line it finds wrong (a control
// byte in a List-Unsubscribe written before From, a line with no colon) and
// hands back what it read so far, or nothing: the sender of the mail then
// depended on what another header held. Here lines are split on LF (a CR
// before it dropped), a line that starts with a space or a tab continues the
// one before (unfolding: the line break goes, the whitespace stays), a line
// without a colon is skipped and the reading goes on, a blank line ends the
// block, and no byte is checked: what a value holds is for its reader to
// judge.
func readHeaders(raw []byte) headerFields {
	fields := headerFields{}
	var name string
	var value []byte
	flush := func() {
		if name != "" {
			fields[name] = append(fields[name], strings.TrimSpace(string(value)))
		}
		name, value = "", nil
	}
	for _, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimSuffix(line, []byte("\r"))
		if len(line) == 0 {
			break
		}
		if line[0] == ' ' || line[0] == '\t' {
			if name != "" {
				value = append(value, line...)
			}
			continue
		}
		flush()
		colon := bytes.IndexByte(line, ':')
		if colon <= 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(string(line[:colon])))
		if key == "" || strings.ContainsAny(key, " \t") {
			continue
		}
		name, value = key, append([]byte(nil), line[colon+1:]...)
	}
	flush()
	return fields
}

// fetchedHeaders is the header block the listing or the reader fetched (the
// HEADER.FIELDS section), and whether the server sent it.
func fetchedHeaders(msg *imapclient.FetchMessageBuffer) (headerFields, bool) {
	fields := headerFields{}
	found := false
	for _, section := range msg.BodySection {
		if section.Section == nil || section.Section.Specifier != imapv2.PartSpecifierHeader || len(section.Bytes) == 0 {
			continue
		}
		found = true
		for k, v := range readHeaders(section.Bytes) {
			fields[k] = append(fields[k], v...)
		}
	}
	return fields, found
}

// first is the first value of a header, "" without one.
func (h headerFields) first(name string) string {
	if v := h[strings.ToLower(name)]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// senderFrom is the From the copy stores for a message, read out of its From
// header as written, never out of the server's ENVELOPE: servers read a From
// in their own ways (one takes a group of one for its member, another reads
// "Doe, John <j@x.com>" as no address), and the Gmail API hands over the
// header itself. So the sender is what protect reads out of the header, on
// both transports.
//
// One From header naming a sender is stored as its decoded display name and
// its address ("Réseau" <news@reseau.fr>): readable on the screen, and read
// back by protect.Address as the same address. One naming none is stored as
// written, its control bytes turned into spaces (protect.NoSenderText), which
// still reads as no sender. Two or more name no single sender: stored as one
// quoted text, which reads as none. No header block, or no From in it: no
// sender either; the envelope's text, in quotes, only for the screen. A mail
// with no sender is logged once, when its row is made (api's
// logSenderlessMail), not at every listing.
func senderFrom(msg *imapclient.FetchMessageBuffer) string {
	headers, ok := fetchedHeaders(msg)
	froms := headers["from"]
	switch {
	case !ok || len(froms) == 0:
		var env []imapv2.Address
		if msg.Envelope != nil {
			env = msg.Envelope.From
		}
		if shown := formatAddress(env); shown != "" {
			return protect.QuotedText(shown)
		}
		return ""
	case len(froms) > 1:
		return protect.QuotedText(strings.Join(froms, ", "))
	}
	raw := strings.ToValidUTF8(froms[0], "\uFFFD")
	name, address := protect.Sender(raw)
	switch {
	case address == "":
		return protect.NoSenderText(raw)
	case name == "":
		return address
	}
	return protect.QuotedText(name) + " <" + address + ">"
}
