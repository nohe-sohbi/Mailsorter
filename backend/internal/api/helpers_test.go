package api

import (
	"strings"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/protect"
	"github.com/nohe-sohbi/mailsorter/backend/internal/rules"
)

func TestExtractDomain(t *testing.T) {
	cases := map[string]string{
		"Acme <hello@acme.com>": "acme.com",
		"hello@acme.com":        "acme.com",
		"no-at-sign":            "",
		`"Big Co" <a@b.co.uk>`:  "b.co.uk",
		// The display name is the sender's to choose.
		`"<patron@victime.fr>" <lettre@journal.fr>`: "journal.fr",
	}
	for in, want := range cases {
		if got := extractDomain(in); got != want {
			t.Errorf("extractDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

// The name of an Expediteurs or Abonnements card is what every screen shows
// (protect.Label): never an address the display name writes.
func TestExtractSenderName(t *testing.T) {
	cases := map[string]string{
		"Acme Inc <hello@acme.com>":                 "Acme Inc",
		`"Acme Inc" <hello@acme.com>`:               "Acme Inc",
		"plain@acme.com":                            "plain@acme.com",
		`"<patron@victime.fr>" <lettre@journal.fr>`: "lettre@journal.fr",
		`"boss@corp.com" <evil@evil.io>`:            "evil@evil.io",
		"\"boss\uFF20corp.com\" <evil@evil.io>":     "evil@evil.io",
		"=?utf-8?q?R=C3=A9seau?= <news@reseau.fr>":  "Réseau",
		"Name <x@y.com> trail":                      protect.UnreadableSenderLabel,
	}
	for in, want := range cases {
		if got := extractSenderName(in); got != want {
			t.Errorf("extractSenderName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtractSenderAddress(t *testing.T) {
	cases := map[string]string{
		"Acme <hello@acme.com>": "hello@acme.com",
		"  bare@acme.com  ":     "bare@acme.com",
		// A header that does not read as one address gives none: nothing is
		// guessed out of it.
		"Name <x@y.com> trail": "",
		// The bug this guards: the first "<...>" is in the display name, which
		// the sender writes, and unsubscribing wrote to that address.
		`"<patron@victime.fr>" <lettre@journal.fr>`:        "lettre@journal.fr",
		`"Patron <patron@victime.fr>" <lettre@journal.fr>`: "lettre@journal.fr",
		`<patron@victime.fr> <lettre@journal.fr>`:          "",
		// A quoted local part is the sender's own address, quoted again.
		`"<patron@victime.fr>"@evil.io`: `"<patron@victime.fr>"@evil.io`,
		`Lettre <Lettre@Journal.fr>`:    "Lettre@Journal.fr",
	}
	for in, want := range cases {
		if got := extractSenderAddress(in); got != want {
			t.Errorf("extractSenderAddress(%q) = %q, want %q", in, got, want)
		}
	}
}

// For every real header that reads (protect's table, both transports), the
// three acts on its sender reach its address: "Se désabonner" (and "et
// archiver") through extractSenderAddress, "Créer une règle" through
// ruleForSender, and the card's domain. A header that does not read gives
// none, and CreateSenderRule answers 400 for it.
func TestSenderActsOnRealHeaders(t *testing.T) {
	cases := map[string]string{
		`"Nom" <a@b.fr>`:             "a@b.fr",
		"a@b.fr":                     "a@b.fr",
		"<a@b.fr>":                   "a@b.fr",
		"Nom <a@b.fr> (commentaire)": "a@b.fr",
		"=?utf-8?q?R=C3=A9seau?= <news@reseau.fr>": "news@reseau.fr",
		"=?windows-1252?q?Caf=E9?= <cafe@b.fr>":    "cafe@b.fr",
		"Nom <A@B.FR>":                             "A@B.FR",
		"john@x.com <john@x.com>":                  "john@x.com",
		"Doe, John <j@x.com>":                      "j@x.com",
		"LinkedIn [Jobs] <jobs@linkedin.com>":      "jobs@linkedin.com",
		"News: daily <n@b.fr>":                     "n@b.fr",
		`"<boss@corp.com>"@evil.io`:                `"<boss@corp.com>"@evil.io`,
		"<patron@victime.fr> <lettre@journal.fr>":  "",
		// Several authors name no sender.
		"patron@victime.fr, Lettre <lettre@journal.fr>": "",
		"Undisclosed: a@x.fr; <b@x.fr>":                 "",
		"patron@victime.fr, Doe, John <j@x.com>":        "",
		`"<boss@corp.com>"@evil.io,`:                    "",
	}
	for from, want := range cases {
		if got := extractSenderAddress(from); got != want {
			t.Errorf("extractSenderAddress(%q) = %q, want %q", from, got, want)
		}
		if want == "" {
			if d := extractDomain(from); d != "" {
				t.Errorf("extractDomain(%q) = %q, want none", from, d)
			}
			continue
		}
		rule := ruleForSender("me@example.com", models.CreateSenderRuleRequest{SenderEmail: from, Action: "archive"})
		if len(rule.Conditions) != 1 || rule.Conditions[0].Value != want {
			t.Errorf("ruleForSender(%q) matches %+v, want From contains %q", from, rule.Conditions, want)
		}
		if d := extractDomain(from); d != want[strings.LastIndex(want, "@")+1:] {
			t.Errorf("extractDomain(%q) = %q, want the domain of %q", from, d, want)
		}
	}
}

func TestRuleForSender(t *testing.T) {
	rule := ruleForSender("me@example.com", models.CreateSenderRuleRequest{
		SenderEmail: "Acme News <news@acme.com>",
		Action:      "archive",
	})

	if rule.UserID != "me@example.com" {
		t.Errorf("rule should be owned by the caller, got %q", rule.UserID)
	}
	// The sender header must be normalized to its bare address.
	if len(rule.Conditions) != 1 || rule.Conditions[0].Value != "news@acme.com" {
		t.Fatalf("expected a single 'from contains news@acme.com' condition, got %+v", rule.Conditions)
	}
	if rule.Conditions[0].Field != rules.FieldFrom || rule.Conditions[0].Operator != rules.OpContains {
		t.Errorf("unexpected condition field/operator: %+v", rule.Conditions[0])
	}
	// A freshly built archive rule must pass the engine's own validation.
	if err := rules.Validate(rule); err != nil {
		t.Errorf("ruleForSender produced an invalid rule: %v", err)
	}

	// "Créer une règle" on the card of a sender who wrote someone else's
	// address in its display name: the rule is about the sender, not about
	// the address in the name, whose mail it would archive for ever.
	trapped := ruleForSender("me@example.com", models.CreateSenderRuleRequest{
		SenderEmail: `"<patron@victime.fr>" <lettre@journal.fr>`,
		Action:      "archive",
	})
	if len(trapped.Conditions) != 1 || trapped.Conditions[0].Value != "lettre@journal.fr" {
		t.Errorf("a rule from \"<patron@victime.fr>\" <lettre@journal.fr> should match lettre@journal.fr, got %+v", trapped.Conditions)
	}

	// A label rule with no label name must be rejected by validation.
	bad := ruleForSender("me@example.com", models.CreateSenderRuleRequest{
		SenderEmail: "news@acme.com",
		Action:      "label",
	})
	if err := rules.Validate(bad); err == nil {
		t.Error("a label rule without a label name should fail validation")
	}
}

// A sender rule's name shows its address in the one form addresses are
// shown in, also for a rule saved before names were written so; any other
// name only has what hides made visible.
func TestShownRuleName(t *testing.T) {
	cases := map[string]string{
		"Expéditeur : y@pаypal.com":  "Expéditeur : y@xn--pypal-4ve.com",
		"Expéditeur : x@pay‮pal.com": "Expéditeur : x@pay�pal.com",
		"Factures ‮etc":              "Factures �etc",
		"Expéditeur : sans adresse":  "Expéditeur : sans adresse",
	}
	for name, want := range cases {
		if got := shownRuleName(name); got != want {
			t.Errorf("shownRuleName(%q) = %q, want %q", name, got, want)
		}
	}
}
