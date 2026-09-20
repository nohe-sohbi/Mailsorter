package search

import (
	"strconv"
	"strings"
	"time"
)

// Reading a query, as opposed to storing one.
//
// The inbox speaks Gmail's query language on every screen, including the
// default `in:inbox` that goes out on each page load. Over the Gmail API that
// string is handed to Google and Google answers it. Over IMAP there is nobody
// to hand it to: the listing is served from the mirror Mailsorter keeps, so the
// query has to be understood here before it can be turned into a lookup.
//
// The part that can be understood is turned into Criteria. The part that cannot
// is returned as such, term by term, rather than dropped. That distinction is
// the whole point of this file: a dropped `has:attachment` does not produce an
// error, it produces a full inbox under a button labelled "Pieces jointes", and
// the user has no way of telling that the filter did nothing.

// Criteria is a query reduced to what a stored mailbox can answer.
//
// Every field is a conjunction: a query names several constraints and a message
// has to satisfy all of them, which is how Gmail reads the same string.
type Criteria struct {
	// Folder is the mailbox named by `in:`. Empty means the query did not say.
	Folder string
	// Unread is three-valued on purpose: is:unread, is:read, or neither.
	Unread *bool
	// From, To and Subject are substrings to match on the matching field.
	From    []string
	To      []string
	Subject []string
	// Text is the bare words, matched against sender, subject and preview.
	Text []string
	// NewerThan and OlderThan are ages, not dates: the caller owns the clock.
	NewerThan time.Duration
	OlderThan time.Duration
}

// Empty reports whether the query constrained nothing at all.
func (c Criteria) Empty() bool {
	return c.Folder == "" && c.Unread == nil &&
		len(c.From) == 0 && len(c.To) == 0 && len(c.Subject) == 0 && len(c.Text) == 0 &&
		c.NewerThan == 0 && c.OlderThan == 0
}

// FolderInbox is the one mailbox scope a mirror-served listing can answer,
// because the sync mirrors the inbox and nothing else.
const FolderInbox = "INBOX"

// Parse splits a Gmail-style query into what a stored mailbox can answer and
// what it cannot.
//
// The second return is the terms that were recognised as operators but have no
// answer in a mirror: they are the caller's to refuse or to ignore knowingly.
// An operator nobody recognises at all is not in there: it is treated as text,
// which is what Gmail does with it too.
func Parse(query string) (Criteria, []string) {
	var (
		crit        Criteria
		unsupported []string
	)

	for _, token := range tokenize(query) {
		operator, value := splitTerm(token)
		if operator == "" {
			if value != "" {
				crit.Text = append(crit.Text, value)
			}
			continue
		}

		switch operator {
		case "from":
			crit.From = append(crit.From, value)
		case "to":
			crit.To = append(crit.To, value)
		case "subject":
			crit.Subject = append(crit.Subject, value)
		case "in":
			// The mirror holds the inbox. Any other scope would answer from a
			// folder nothing was ever synced into, and an empty list is a lie
			// that looks exactly like a clean mailbox.
			if strings.EqualFold(value, "inbox") {
				crit.Folder = FolderInbox
				continue
			}
			unsupported = append(unsupported, token)
		case "is":
			switch strings.ToLower(value) {
			case "unread":
				crit.Unread = boolPtr(false)
			case "read":
				crit.Unread = boolPtr(true)
			default:
				// is:starred and the rest. A stored message records whether it
				// was read and nothing else about its flags, on purpose: the
				// rest is Gmail label vocabulary that does not cross transports.
				unsupported = append(unsupported, token)
			}
		case "newer_than":
			if d, ok := parseAge(value); ok {
				crit.NewerThan = d
				continue
			}
			unsupported = append(unsupported, token)
		case "older_than":
			if d, ok := parseAge(value); ok {
				crit.OlderThan = d
				continue
			}
			unsupported = append(unsupported, token)
		case "has", "larger", "smaller", "label", "filename", "category":
			// All of them need something the mirror does not keep: the MIME
			// tree, the byte size, or Gmail's labels.
			unsupported = append(unsupported, token)
		default:
			// Not an operator this app has ever produced. Gmail treats an
			// unknown `word:word` as text, and so does this.
			crit.Text = append(crit.Text, token)
		}
	}
	return crit, unsupported
}

// splitTerm cuts `operator:value`, unquoting the value. A token with no colon,
// or with nothing on one side of it, is text.
func splitTerm(token string) (operator, value string) {
	i := strings.Index(token, ":")
	if i <= 0 || i == len(token)-1 {
		return "", unquote(token)
	}
	return strings.ToLower(token[:i]), unquote(token[i+1:])
}

func unquote(s string) string {
	return strings.Trim(s, `"'`)
}

// parseAge reads Gmail's relative age syntax: a count and a unit among days,
// months and years.
//
// The month and the year are approximations, and deliberately so: Gmail's own
// newer_than is documented in days, months and years without ever saying which
// month, and a filter labelled "les 7 derniers jours" does not become wrong
// because February is short.
func parseAge(value string) (time.Duration, bool) {
	if len(value) < 2 {
		return 0, false
	}
	unit := value[len(value)-1]
	count, err := strconv.Atoi(value[:len(value)-1])
	if err != nil || count <= 0 {
		return 0, false
	}
	const day = 24 * time.Hour
	switch unit {
	case 'd', 'D':
		return time.Duration(count) * day, true
	case 'm', 'M':
		return time.Duration(count) * 30 * day, true
	case 'y', 'Y':
		return time.Duration(count) * 365 * day, true
	}
	return 0, false
}

func boolPtr(v bool) *bool { return &v }
