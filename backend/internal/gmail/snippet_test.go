package gmail

import (
	"testing"

	gmailapi "google.golang.org/api/gmail/v1"
)

// Gmail escapes snippets for HTML. The apostrophe is the case that reached the
// screen ("Suivi de l&#39;avancement"); the others are the entities Gmail emits
// for text that would otherwise read as markup.
func TestSnippetIsPlainText(t *testing.T) {
	cases := map[string]string{
		"Suivi de l&#39;avancement de votre dossier": "Suivi de l'avancement de votre dossier",
		"&quot;Offre&quot; &amp; conditions":         `"Offre" & conditions`,
		"5 &lt; 6 &gt; 4":                            "5 < 6 > 4",
		"Rien à décoder ici":                         "Rien à décoder ici",
		"":                                           "",
	}
	for in, want := range cases {
		if got := Snippet(&gmailapi.Message{Snippet: in}); got != want {
			t.Errorf("Snippet(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Snippet(nil); got != "" {
		t.Errorf("Snippet(nil) = %q, want empty", got)
	}
}
