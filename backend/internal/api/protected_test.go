package api

import (
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/ai"
)

func TestProtectAnalysisDowngradesDestructive(t *testing.T) {
	protectedList := []string{"boss@corp.com", "acme.com"}

	// A destructive verdict for a protected sender is forced to "keep".
	got := protectAnalysis(ai.EmailAnalysis{Action: "archive", Confidence: 0.7}, "The Boss <boss@corp.com>", protectedList)
	if got.Action != "keep" {
		t.Errorf("archive on protected sender → %q, want keep", got.Action)
	}
	if got.Confidence < 0.9 {
		t.Errorf("protected keep should be confident, got %v", got.Confidence)
	}

	// A destructive verdict for a domain-protected sender is also kept.
	if got := protectAnalysis(ai.EmailAnalysis{Action: "delete"}, "deals@mail.acme.com", protectedList); got.Action != "keep" {
		t.Errorf("delete on domain-protected sender → %q, want keep", got.Action)
	}

	// A non-destructive verdict passes through untouched.
	label := protectAnalysis(ai.EmailAnalysis{Action: "label", LabelName: "Travail"}, "boss@corp.com", protectedList)
	if label.Action != "label" || label.LabelName != "Travail" {
		t.Errorf("label on protected sender should pass through, got %+v", label)
	}

	// An unprotected sender's destructive verdict is left alone.
	if got := protectAnalysis(ai.EmailAnalysis{Action: "archive"}, "stranger@other.com", protectedList); got.Action != "archive" {
		t.Errorf("archive on unprotected sender → %q, want archive", got.Action)
	}
}

func TestAllowsWrapper(t *testing.T) {
	protectedList := []string{"vip@corp.com"}
	if allows("archive", "vip@corp.com", protectedList) {
		t.Error("archive on protected sender should not be allowed")
	}
	if !allows("label", "vip@corp.com", protectedList) {
		t.Error("label on protected sender should be allowed")
	}
	if !allows("archive", "anyone@else.com", protectedList) {
		t.Error("archive on unprotected sender should be allowed")
	}
}
