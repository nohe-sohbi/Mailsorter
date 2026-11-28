package mailbox

import (
	"strings"
	"unicode"
)

// Matching a label the model proposed with one the user already has.
//
// The model is told nothing of the user's labels or folders: the prompt
// carries the sender, the subject and an excerpt, which is what the public
// pages promise, and its verdict goes into a cache shared by every account,
// where a name taken from one person's mailbox must never surface for
// another. So the model proposes a name of its own ("Facture"), and it is
// matched here, per account, after the fact, with the labels the user has
// ("Factures").
//
// The match is exact once case, accents and a plural mark are set aside, and
// nothing looser. A substring match would let a folder "Pro" take every
// "Promotions", and a mail filed in the wrong folder is worse than a new one.

// MatchLabel returns the label of existing that names the same thing as
// suggested (LabelKey), and false when none does. Of several, the one spelled
// exactly as suggested wins, then the first.
func MatchLabel(suggested string, existing []string) (string, bool) {
	want := LabelKey(suggested)
	if want == "" {
		return "", false
	}
	found, ok := "", false
	for _, e := range existing {
		if LabelKey(e) != want {
			continue
		}
		if e == suggested {
			return e, true
		}
		if !ok {
			found, ok = e, true
		}
	}
	return found, ok
}

// LabelKey is what two names of the same label share: lower case, no accent,
// spaces collapsed, and each word without a final plural "s" or "x" when it
// is longer than three letters ("Factures" and "facture" share "facture";
// "Pro" keeps its own key, so it is not "Promotions"). "/" stays a level.
func LabelKey(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		b.WriteString(unaccent(r))
	}
	words := strings.FieldsFunc(b.String(), unicode.IsSpace)
	for i, w := range words {
		words[i] = unplural(w)
	}
	return strings.Join(words, " ")
}

// unplural drops a final plural mark from a word, level by level ("notes/frais"
// is "note/frai"), when the level is longer than three letters.
func unplural(word string) string {
	levels := strings.Split(word, "/")
	for i, l := range levels {
		if n := len([]rune(l)); n > 3 && (strings.HasSuffix(l, "s") || strings.HasSuffix(l, "x")) {
			levels[i] = l[:len(l)-1]
		}
	}
	return strings.Join(levels, "/")
}

// unaccent spells a lower-case letter without its accent, for the Latin
// letters French and its neighbours use. Kept to a table rather than a
// Unicode normalisation library: the project adds no dependency for this.
func unaccent(r rune) string {
	switch r {
	case 'à', 'â', 'ä', 'á', 'ã', 'å':
		return "a"
	case 'ç':
		return "c"
	case 'é', 'è', 'ê', 'ë':
		return "e"
	case 'î', 'ï', 'í', 'ì':
		return "i"
	case 'ô', 'ö', 'ó', 'ò', 'õ':
		return "o"
	case 'ù', 'û', 'ü', 'ú':
		return "u"
	case 'ÿ', 'ý':
		return "y"
	case 'ñ':
		return "n"
	case 'œ':
		return "oe"
	case 'æ':
		return "ae"
	}
	return string(r)
}
