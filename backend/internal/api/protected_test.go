package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/ai"
	"github.com/nohe-sohbi/mailsorter/backend/internal/protect"
	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
)

func TestProtectAnalysisDowngradesDestructive(t *testing.T) {
	protectedList := protect.NewShield([]string{"boss@corp.com", "acme.com"})

	// A destructive verdict for a protected sender is forced to "keep".
	got := protectAnalysis(ai.EmailAnalysis{Action: "archive", Confidence: 0.7}, "The Boss <boss@corp.com>", protectedList, provider.TransportGmailAPI)
	if got.Action != "keep" {
		t.Errorf("archive on protected sender → %q, want keep", got.Action)
	}
	if got.Confidence < 0.9 {
		t.Errorf("protected keep should be confident, got %v", got.Confidence)
	}

	// A destructive verdict for a domain-protected sender is also kept.
	if got := protectAnalysis(ai.EmailAnalysis{Action: "delete"}, "deals@mail.acme.com", protectedList, provider.TransportGmailAPI); got.Action != "keep" {
		t.Errorf("delete on domain-protected sender → %q, want keep", got.Action)
	}

	// A label passes through untouched over the Gmail API, where the mail
	// stays in the inbox.
	label := protectAnalysis(ai.EmailAnalysis{Action: "label", LabelName: "Travail"}, "boss@corp.com", protectedList, provider.TransportGmailAPI)
	if label.Action != "label" || label.LabelName != "Travail" {
		t.Errorf("label on protected sender should pass through, got %+v", label)
	}

	// Over IMAP a label files the mail in a folder, out of the inbox: the plan
	// must not propose what "Tout ranger" would refuse.
	filed := protectAnalysis(ai.EmailAnalysis{Action: "label", LabelName: "Travail", Confidence: 0.7}, "The Boss <boss@corp.com>", protectedList, provider.TransportIMAP)
	if filed.Action != "keep" || filed.LabelName != "" || filed.Confidence < 0.9 {
		t.Errorf("label on protected sender over IMAP = %+v, want a confident keep with no label", filed)
	}
	if got := protectAnalysis(ai.EmailAnalysis{Action: "label", LabelName: "Travail"}, "stranger@other.com", protectedList, provider.TransportIMAP); got.Action != "label" {
		t.Errorf("label on unprotected sender over IMAP = %q, want label", got.Action)
	}

	// An unprotected sender's destructive verdict is left alone.
	if got := protectAnalysis(ai.EmailAnalysis{Action: "archive"}, "stranger@other.com", protectedList, provider.TransportGmailAPI); got.Action != "archive" {
		t.Errorf("archive on unprotected sender → %q, want archive", got.Action)
	}
}

func TestAllowsWrapper(t *testing.T) {
	protectedList := protect.NewShield([]string{"vip@corp.com"})
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

// Over IMAP a label files the mail into a folder, out of the inbox: the bulk
// "Tout ranger" must not do that to a protected sender. Over the Gmail API the
// mail stays in the inbox, and labelling stays allowed.
func TestAllowsOnReadsALabelAsAMoveOverIMAP(t *testing.T) {
	protectedList := protect.NewShield([]string{"vip@corp.com"})
	if allowsOn(provider.TransportIMAP, "label", "VIP <vip@corp.com>", protectedList) {
		t.Error("label on a protected sender over IMAP should not be allowed")
	}
	if !allowsOn(provider.TransportIMAP, "label", "anyone@else.com", protectedList) {
		t.Error("label on an unprotected sender over IMAP should be allowed")
	}
	if !allowsOn(provider.TransportGmailAPI, "label", "vip@corp.com", protectedList) {
		t.Error("label on a protected sender over the Gmail API should be allowed")
	}
	if allowsOn(provider.TransportGmailAPI, "archive", "vip@corp.com", protectedList) {
		t.Error("archive on a protected sender over the Gmail API should not be allowed")
	}
}

func TestCreateProtectedInputValidation(t *testing.T) {
	h := &Handler{}

	t.Run("RejectsOversizedValue", func(t *testing.T) {
		oversizedValue := "a" + strings.Repeat("a", 250) + "@example.com" // > 254 bytes
		body := strings.NewReader(`{"value":"` + oversizedValue + `","note":"valid note"}`)
		req := httptest.NewRequest("POST", "/api/protected", body)
		req.Header.Set("X-User-Email", "user@example.com")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		h.CreateProtected(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
		}
	})

	t.Run("RejectsOversizedNote", func(t *testing.T) {
		oversizedNote := strings.Repeat("n", 501) // > 500 bytes
		body := strings.NewReader(`{"value":"vip@example.com","note":"` + oversizedNote + `"}`)
		req := httptest.NewRequest("POST", "/api/protected", body)
		req.Header.Set("X-User-Email", "user@example.com")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		h.CreateProtected(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
		}
	})
}
