package imap

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	imapv2 "github.com/emersion/go-imap/v2"
)

// namespace is where the user's own folders live on a server, and how their
// names nest: the personal namespace prefix and the hierarchy delimiter, and
// where folders that are not the user's live (other users', shared ones).
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
	// others are the prefixes of the other users' and the shared namespaces
	// ("Other Users/", "Public/", "shared."): a folder under one of them is
	// not the user's, even when the personal prefix is empty and its name
	// looks like any other.
	others []string
}

// readNamespace reads the user's namespace from what the server said: the
// first personal namespace of its NAMESPACE answer when it gave one, or else
// no prefix and the delimiter its folder listing used (listDelim), plus the
// prefixes of its other users' and shared namespaces. A prefix given without
// its trailing delimiter gets it: the folders under "INBOX" with "." are named
// "INBOX.x". An empty other or shared prefix, or one equal to the personal
// one, is left out: it would exclude every folder of the user's own.
func readNamespace(data imapv2.NamespaceData, listDelim rune) namespace {
	ns := namespace{delim: listDelim}
	if len(data.Personal) > 0 {
		ns.prefix, ns.delim = withDelim(data.Personal[0], listDelim)
	}
	for _, list := range [][]imapv2.NamespaceDescriptor{data.Other, data.Shared} {
		for _, d := range list {
			prefix, _ := withDelim(d, listDelim)
			if prefix != "" && !strings.EqualFold(prefix, ns.prefix) {
				ns.others = append(ns.others, prefix)
			}
		}
	}
	return ns
}

// withDelim is a namespace's prefix completed with its delimiter (the
// listing's when it names none), and that delimiter.
func withDelim(d imapv2.NamespaceDescriptor, listDelim rune) (string, rune) {
	delim := d.Delim
	if delim == 0 {
		delim = listDelim
	}
	prefix := d.Prefix
	if prefix != "" && delim != 0 && !strings.HasSuffix(prefix, string(delim)) {
		prefix += string(delim)
	}
	return prefix, delim
}

// hasPrefixFold reports whether name starts with prefix, whatever its case,
// as INBOX is matched.
func hasPrefixFold(name, prefix string) bool {
	return len(name) >= len(prefix) && strings.EqualFold(name[:len(prefix)], prefix)
}

// display is how the user reads the folder the server calls name: without the
// prefix, levels split by "/". ok is false for a folder outside the personal
// namespace (under another user's or a shared prefix, outside the personal
// prefix, or the prefix itself), which is not the user's own.
func (ns namespace) display(name string) (string, bool) {
	for _, other := range ns.others {
		if hasPrefixFold(name, other) {
			return name, false
		}
	}
	rest := name
	if ns.prefix != "" {
		if len(name) <= len(ns.prefix) || !hasPrefixFold(name, ns.prefix) {
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

// ErrBadFolderName means a label is not a name Mailsorter makes a folder
// under: a character the protocol reserves or cannot carry, an empty level,
// a level that climbs ("." or ".."), or a name too long. Typed, so the user
// is told the name is the problem rather than the mailbox.
var ErrBadFolderName = errors.New("imap: not a name to make a folder under")

// The longest folder name Mailsorter makes, in characters, level by level and
// whole. Servers cap names near 255 bytes; a label is a few words.
const (
	maxFolderLevel = 100
	maxFolderName  = 200
)

// cleanName is the name, as the user reads it, of the folder Mailsorter makes
// for the label name: levels split by "/", each trimmed. It refuses
// (ErrBadFolderName) a control character, the LIST wildcards "*" and "%", an
// empty level ("Factures/", "/Factures", "a//b"), "." and "..", and a level or
// a name too long. The server's delimiter inside a level, when it is not "/",
// becomes "-": "Amazon.fr" is "Amazon-fr" on a server whose delimiter is ".",
// rather than "fr" inside "Amazon". That cleaned name is the folder's real
// name, the one the screens show and the history records.
func (ns namespace) cleanName(name string) (string, error) {
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%w: a control character in %q", ErrBadFolderName, name)
		}
		if r == '*' || r == '%' {
			return "", fmt.Errorf("%w: %q holds %q", ErrBadFolderName, name, r)
		}
	}
	levels := strings.Split(strings.TrimSpace(name), "/")
	for i, level := range levels {
		level = strings.TrimSpace(level)
		if level == "" || level == "." || level == ".." {
			return "", fmt.Errorf("%w: an empty or climbing level in %q", ErrBadFolderName, name)
		}
		if ns.delim != 0 && ns.delim != '/' {
			level = strings.ReplaceAll(level, string(ns.delim), "-")
		}
		if len([]rune(level)) > maxFolderLevel {
			return "", fmt.Errorf("%w: a level longer than %d characters", ErrBadFolderName, maxFolderLevel)
		}
		levels[i] = level
	}
	clean := strings.Join(levels, "/")
	if len([]rune(clean)) > maxFolderName {
		return "", fmt.Errorf("%w: longer than %d characters", ErrBadFolderName, maxFolderName)
	}
	return clean, nil
}
