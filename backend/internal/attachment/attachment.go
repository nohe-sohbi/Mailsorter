// Package attachment decides which parts of a mail the reader lists as the
// files attached to it, and under which name, whichever way the mailbox is
// reached.
//
// Over the Gmail API, Google walks the MIME tree and hands each part over
// with its headers; over IMAP, internal/imap walks the message itself. Each
// used to decide on its own, and they disagreed on the same mail: Gmail hid
// every part with a Content-ID, an X-Attachment-Id or an inline disposition,
// which hid the files Gmail's own web interface attaches (it gives each one a
// Content-ID) and the PDF Apple Mail sends inline, while IMAP listed both.
// The rule is here once, so a mail shows the same files on both transports,
// and the download, which serves only what the list shows, agrees with it.
//
// It is pure, like the rest of the decision logic: it reads what a part says
// about itself and what the mail's HTML points at, never a mailbox.
package attachment

import (
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode"
)

// Unnamed is the name of a file whose sender gave it none, or one that
// cleans away to nothing. A download must always land with a usable name.
const Unnamed = "piece-jointe"

// UnnamedMail is the name of a forwarded mail (message/rfc822) without one:
// saved under it, it opens in a mail program.
const UnnamedMail = "message.eml"

// Part is what one MIME part of a mail says about itself, as either
// transport reads it.
type Part struct {
	// Filename is the part's name, decoded: the filename parameter of
	// Content-Disposition, else the name parameter of Content-Type. Empty
	// when it has none.
	Filename string
	// MediaType is the declared type, without its parameters.
	MediaType string
	// Disposition is the Content-Disposition header as sent, or its type
	// alone ("attachment", "inline"). Empty when there is none.
	Disposition string
	// ContentID is the Content-ID header as sent, angle brackets included.
	ContentID string
}

// Listed says whether the reader lists p as a file attached to its mail,
// given the Content-IDs the mail's HTML shows, and under which name.
//
// The rule, the same on both transports:
//
//   - A part with a name is a file. So is a part without one that its sender
//     declared an attachment, listed as Unnamed, and a forwarded mail
//     (message/rfc822), listed as UnnamedMail: neither is shown anywhere
//     else. Any other part without a name is not listed: it is the text of
//     the mail in one form or another, and a chip named after nothing would
//     only be noise.
//   - Except an image the mail's HTML shows through a cid: URL, whatever its
//     disposition: listing those turns a newsletter into a row of logo.png
//     chips. A Content-ID alone hides nothing, and neither does an
//     X-Attachment-Id or an inline disposition: Gmail's web interface puts a
//     Content-ID on every file it attaches, Apple Mail sends a PDF inline,
//     and hiding them hid the very file the mail was sent for.
//
// A multipart is a container, never a file.
//
// The name is cleaned by SafeName, so the name the reader shows, the one the
// browser saves the file under and the one the download's
// Content-Disposition carries are the same.
func Listed(p Part, shown Shown) (string, bool) {
	mediaType := strings.ToLower(strings.TrimSpace(p.MediaType))
	forwarded := mediaType == "message/rfc822"
	switch {
	case strings.HasPrefix(mediaType, "multipart/"):
		return "", false
	case p.Filename == "" && DispositionType(p.Disposition) != "attachment" && !forwarded:
		return "", false
	case strings.HasPrefix(mediaType, "image/") && shown.Has(p.ContentID):
		return "", false
	}
	fallback := Unnamed
	if forwarded {
		fallback = UnnamedMail
	}
	return SafeName(p.Filename, fallback), true
}

// ListedWithoutHTML says whether p counts as a file of its mail when the
// mail's HTML is not read: what the filter "Pièces jointes" decides from the
// server's description of a message (its BODYSTRUCTURE), which lists the
// parts but not what the HTML points at.
//
// It is Listed, with one guess in place of the HTML: a part with a
// Content-ID that its sender did not declare an attachment (inline, or no
// disposition at all) is taken for one the HTML shows. So the filter agrees
// with the reader on every part but two kinds of image, which only the HTML
// tells apart: an image with a Content-ID, not declared an attachment, that
// the HTML does not point at (the reader lists it, the filter does not count
// it), and an image declared an attachment that the HTML does point at (the
// reader hides it, the filter counts it).
func ListedWithoutHTML(p Part) bool {
	shown := Shown{}
	if DispositionType(p.Disposition) != "attachment" {
		if id := contentID(p.ContentID); id != "" {
			shown[id] = true
		}
	}
	_, ok := Listed(p, shown)
	return ok
}

// DispositionType is the type of a Content-Disposition value alone
// ("attachment", "inline"), in lower case, read before its first parameter
// so a malformed parameter does not hide it.
func DispositionType(value string) string {
	value, _, _ = strings.Cut(value, ";")
	return strings.ToLower(strings.TrimSpace(value))
}

// Shown is the set of Content-IDs a mail's HTML points at, normalized by
// contentID.
type Shown map[string]bool

// cidURL is a cid: URL (RFC 2392) in HTML: in an attribute, quoted or not,
// or in a CSS url(). It ends where the attribute or the url() ends.
var cidURL = regexp.MustCompile(`(?i)cid:([^\s"'<>()\\]+)`)

// ShownBy reads the cid: URLs of the HTML the reader renders for a mail.
// A mail without HTML shows no part this way.
func ShownBy(html string) Shown {
	shown := Shown{}
	for _, m := range cidURL.FindAllStringSubmatch(html, -1) {
		id := m[1]
		// A cid: URL is the Content-ID with its URL-unsafe characters
		// %-escaped. One that does not unescape is compared as written.
		if unescaped, err := url.PathUnescape(id); err == nil {
			id = unescaped
		}
		if id = contentID(id); id != "" {
			shown[id] = true
		}
	}
	return shown
}

// Has says whether the HTML shows the part whose Content-ID header is
// header. A part without a Content-ID is never shown this way.
func (s Shown) Has(header string) bool {
	id := contentID(header)
	return id != "" && s[id]
}

// contentID is a Content-ID as a cid: URL names it: without its angle
// brackets. In lower case, because a mail program may write the reference
// in another case than the header, while two parts of one mail never differ
// only by the case of their Content-ID.
func contentID(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "<")
	value = strings.TrimSuffix(value, ">")
	return strings.ToLower(strings.TrimSpace(value))
}

// SafeName reduces a sender-supplied file name to one that is safe to show,
// to put in a header and to save on a disk: no directory (a part named
// "../../.bashrc" lands as ".bashrc"), no control character, no quote that
// would break out of a quoted-string, and no bidirectional control
// character, which would let "facture" U+202E "fdp.exe" display as
// "factureexe.pdf". A name that cleans away to nothing becomes fallback.
func SafeName(filename, fallback string) string {
	name := strings.TrimSpace(filename)
	// Both separators, because the name is authored on the sender's machine
	// and path.Base alone leaves a Windows path intact.
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(name)
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) || r == '"' {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || name == "/" {
		return fallback
	}
	return name
}
