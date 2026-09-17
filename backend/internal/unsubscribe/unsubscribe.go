// Package unsubscribe reads the headers a mailing list uses to offer a way out:
// List-Unsubscribe (RFC 2369) and List-Unsubscribe-Post (RFC 8058).
//
// It is pure, and it is its own package rather than a helper inside a transport
// because the headers belong to the MESSAGE, not to the way the message was
// fetched. The same newsletter carries the same List-Unsubscribe whether it
// arrives through the Gmail API or over IMAP, and the parsing has exactly one
// correct answer either way. Leaving it inside internal/gmail meant the IMAP
// listing would have grown a second copy, and a second copy of a parser is how
// two transports start disagreeing about the same email.
//
// What it does NOT do is follow any of it. Acting on the https endpoint is a
// server-side request to an address a stranger chose, which is internal/egress
// territory.
package unsubscribe

import "strings"

// Links is what a message offers as a way out.
type Links struct {
	// URL is the https endpoint, empty when the sender offers none.
	URL string
	// Mailto is the mailto: address, empty when the sender offers none.
	Mailto string
	// OneClick reports RFC 8058 support: the URL can be POSTed server-side and
	// the user never leaves the app. It requires BOTH a URL and the
	// List-Unsubscribe-Post header, because a sender advertising the header
	// without an endpoint has nothing to post to.
	OneClick bool
}

// Any reports whether the message offers a way out at all.
func (l Links) Any() bool { return l.URL != "" || l.Mailto != "" }

// Parse reads the two header values. Callers pass them however their transport
// hands headers over, which is the only thing the two differ on.
//
// The first entry of each kind wins. A List-Unsubscribe may carry several, and
// senders put the one they prefer first; picking the last would quietly route
// users to the fallback.
func Parse(listUnsubscribe, listUnsubscribePost string) Links {
	var links Links
	for _, token := range SplitAngleList(listUnsubscribe) {
		low := strings.ToLower(token)
		switch {
		case (strings.HasPrefix(low, "https://") || strings.HasPrefix(low, "http://")) && links.URL == "":
			links.URL = token
		case strings.HasPrefix(low, "mailto:") && links.Mailto == "":
			links.Mailto = token
		}
	}
	links.OneClick = links.URL != "" && strings.Contains(strings.ToLower(listUnsubscribePost), "one-click")
	return links
}

// SplitAngleList parses a comma-separated list of <...>-wrapped URIs, which is
// the shape RFC 2369 defines.
func SplitAngleList(v string) []string {
	out := make([]string, 0, 2)
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		part = strings.TrimPrefix(part, "<")
		part = strings.TrimSuffix(part, ">")
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
