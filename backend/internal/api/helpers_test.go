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
	}
	for in, want := range cases {
		if got := extractDomain(in); got != want {
			t.Errorf("extractDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtractSenderName(t *testing.T) {
	cases := map[string]string{
		"Acme Inc <hello@acme.com>":   "Acme Inc",
		`"Acme Inc" <hello@acme.com>`: "Acme Inc",
		"plain@acme.com":              "plain@acme.com",
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

	// A label rule with no label name must be rejected by validation.
	bad := ruleForSender("me@example.com", models.CreateSenderRuleRequest{
		SenderEmail: "news@acme.com",
		Action:      "label",
	})
	if err := rules.Validate(bad); err == nil {
		t.Error("a label rule without a label name should fail validation")
	}
}
