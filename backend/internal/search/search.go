// Package search owns Gmail's query language as this app uses it: what makes a
// saved search valid, how it is normalized before storage, how many a user may
// keep, and what a query string actually asks for (parse.go).
//
// The inbox already speaks Gmail's query language, which is what makes it
// powerful and also what makes it unusable twice: nobody retypes
// "in:inbox from:linkedin.com is:unread older_than:7d" every morning. Saving one
// turns a query someone worked out once into a filter they click. The deciding
// part (is this name usable, is this query plausible, is the list full, what
// does this query constrain) is pure and lives here; only the api package
// stores anything or looks anything up.
package search

import (
	"fmt"
	"strings"
)

// MaxPerUser bounds how many searches one account may keep. Saved searches are
// a shortcut bar, not an archive: past a couple of dozen the bar itself becomes
// the thing to search through.
const MaxPerUser = 24

// Field length caps. The name is a chip label, the query is a Gmail search
// string; both are bounded so a stored document cannot grow without limit.
const (
	MaxNameLength  = 60
	MaxQueryLength = 512
)

// Normalize trims and validates a name/query pair, returning the values to
// store. Errors are user-facing (French), like rules.Validate.
func Normalize(name, query string) (string, string, error) {
	name = strings.Join(strings.Fields(name), " ")
	query = strings.TrimSpace(query)

	if name == "" {
		return "", "", fmt.Errorf("donnez un nom à cette recherche")
	}
	if len([]rune(name)) > MaxNameLength {
		return "", "", fmt.Errorf("ce nom dépasse %d caractères", MaxNameLength)
	}
	if query == "" {
		return "", "", fmt.Errorf("la recherche est vide")
	}
	if len([]rune(query)) > MaxQueryLength {
		return "", "", fmt.Errorf("cette recherche dépasse %d caractères", MaxQueryLength)
	}
	// A newline in a Gmail query is never meaningful and would render as a
	// broken chip; it usually means something was pasted from an email.
	if strings.ContainsAny(query, "\r\n") {
		return "", "", fmt.Errorf("une recherche tient sur une seule ligne")
	}
	return name, query, nil
}

// Key is the identity of a saved search within an account: the query itself,
// case-folded. Two chips that run the same query are the same shortcut whatever
// they are called, so saving an existing query updates its name rather than
// growing a second, identical button.
func Key(query string) string {
	return strings.ToLower(strings.Join(strings.Fields(query), " "))
}

// SuggestName derives a default label from a query, so the save dialog opens
// with something usable instead of an empty field. It keeps the query's most
// specific-looking term: the first `operator:value` pair that is not the
// mailbox scope, since `in:inbox` sits on nearly every query and says where to
// look rather than what the user is looking for. A query that is only a scope
// still yields its value ("inbox") rather than nothing.
func SuggestName(query string) string {
	tokens := tokenize(query)
	fallback := ""
	for _, tok := range tokens {
		value := tok
		if i := strings.Index(tok, ":"); i > 0 && i < len(tok)-1 {
			value = strings.Trim(tok[i+1:], `"'`)
		}
		if strings.HasPrefix(strings.ToLower(tok), "in:") {
			if fallback == "" {
				fallback = value
			}
			continue
		}
		return value
	}
	if fallback != "" {
		return fallback
	}
	return strings.TrimSpace(query)
}

// tokenize splits a query on whitespace while keeping a double-quoted run
// together, so `from:"Acme Corp"` stays one term. Gmail's own search accepts
// that form, and splitting it would suggest the name "Acme".
func tokenize(query string) []string {
	var (
		out     []string
		current strings.Builder
		quoted  bool
	)
	flush := func() {
		if current.Len() > 0 {
			out = append(out, current.String())
			current.Reset()
		}
	}
	for _, r := range query {
		switch {
		case r == '"':
			quoted = !quoted
			current.WriteRune(r)
		case !quoted && (r == ' ' || r == '\t' || r == '\n' || r == '\r'):
			flush()
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return out
}
