package imap

import (
	"strings"

	imapv2 "github.com/emersion/go-imap/v2"
)

// namespace is where the user's own folders live on a server, and how their
// names nest: the personal namespace prefix and the hierarchy delimiter.
//
// Most servers keep the user's folders at the top ("Factures", nested as
// "Projets/Maison"). Some keep them under a prefix with another delimiter:
// Courier and several Cyrus set-ups put every folder under "INBOX.", so the
// sent mail is "INBOX.Sent" and a folder made at the top is refused, or lands
// outside what the user's mail program shows. The user reads neither the
// prefix nor the server's delimiter: a folder is shown as Gmail shows a label,
// levels split by "/", and its server name is rebuilt from that.
//
// Pure, so the rules (where a name starts, how it nests) are tested on their
// own, for prefixes the in-memory test server never announces.
type namespace struct {
	// prefix starts the name of every folder of the user's own, delimiter
	// included ("INBOX."), and is empty on most servers.
	prefix string
	// delim separates the levels of a name on the server; 0 on a flat server.
	delim rune
}

// personalNamespace reads the user's namespace from what the server said:
// the first personal namespace of its NAMESPACE answer when it gave one
// (personal), or else no prefix and the delimiter its folder listing used
// (listDelim). A prefix given without its trailing delimiter gets it: the
// folders under "INBOX" with "." are named "INBOX.x".
func personalNamespace(personal []imapv2.NamespaceDescriptor, listDelim rune) namespace {
	if len(personal) == 0 {
		return namespace{delim: listDelim}
	}
	ns := namespace{prefix: personal[0].Prefix, delim: personal[0].Delim}
	if ns.delim == 0 {
		ns.delim = listDelim
	}
	if ns.prefix != "" && ns.delim != 0 && !strings.HasSuffix(ns.prefix, string(ns.delim)) {
		ns.prefix += string(ns.delim)
	}
	return ns
}

// display is how the user reads the folder the server calls name: without the
// prefix, levels split by "/". ok is false for a folder outside the personal
// namespace (a shared one, or the prefix itself), which is not the user's own.
// The prefix is matched whatever its case, as INBOX is.
func (ns namespace) display(name string) (string, bool) {
	rest := name
	if ns.prefix != "" {
		if len(name) <= len(ns.prefix) || !strings.EqualFold(name[:len(ns.prefix)], ns.prefix) {
			return name, false
		}
		rest = name[len(ns.prefix):]
	}
	if ns.delim != 0 && ns.delim != '/' {
		rest = strings.ReplaceAll(rest, string(ns.delim), "/")
	}
	return rest, true
}

// serverName is the server's name for a folder of the user's own the user
// reads as display: under the prefix, levels split by the server's delimiter.
// It is how a folder is named when Mailsorter makes it; a folder that exists
// is found by its display name instead (Client.Destination), since a "/" in a
// name on a server whose delimiter is "." is part of the name, not a level.
func (ns namespace) serverName(display string) string {
	name := display
	if ns.delim != 0 && ns.delim != '/' {
		name = strings.ReplaceAll(name, "/", string(ns.delim))
	}
	return ns.prefix + name
}

// readName is the name a special folder is recognised by when the server
// marks none: the one the user reads ("Sent" for "INBOX.Sent"), or the server
// name for a folder outside the personal namespace.
func (ns namespace) readName(name string) string {
	shown, _ := ns.display(name)
	return shown
}
