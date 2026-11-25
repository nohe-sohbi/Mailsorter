package api

import (
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/rules"
)

func TestExtractDomain(t *testing.T) {
	cases := map[string]string{
		"Acme <hello@acme.com>": "acme.com",
		"hello@acme.com":        "acme.com",
		"no-at-sign":            "no-at-sign",
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

func TestExtractSenderName(t *testing.T) {
	cases := map[string]string{
		"Acme Inc <hello@acme.com>":                 "Acme Inc",
		`"Acme Inc" <hello@acme.com>`:               "Acme Inc",
		"plain@acme.com":                            "plain@acme.com",
		`"<patron@victime.fr>" <lettre@journal.fr>`: "<patron@victime.fr>",
		"=?utf-8?q?R=C3=A9seau?= <news@reseau.fr>":  "Réseau",
		"Name <x@y.com> trail":                      "Name",
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
		"Name <x@y.com> trail":  "x@y.com",
		// The bug this guards: the first "<...>" is in the display name, which
		// the sender writes, and unsubscribing wrote to that address.
		`"<patron@victime.fr>" <lettre@journal.fr>`:        "lettre@journal.fr",
		`"Patron <patron@victime.fr>" <lettre@journal.fr>`: "lettre@journal.fr",
		`<patron@victime.fr> <lettre@journal.fr>`:          "lettre@journal.fr",
		`Lettre <Lettre@Journal.fr>`:                       "Lettre@Journal.fr",
	}
	for in, want := range cases {
		if got := extractSenderAddress(in); got != want {
			t.Errorf("extractSenderAddress(%q) = %q, want %q", in, got, want)
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
