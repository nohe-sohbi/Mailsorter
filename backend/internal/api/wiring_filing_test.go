//go:build acceptance

package api

// Filing into folders on an IMAP mailbox, around the locked sentences of
// scenarios_8_imap_ai_test.go: what the model is told, what the analysis
// proposes for a protected sender, "Annuler" right after a single "Appliquer",
// and the direct action without a folder. Wiring, like wiring_imap_test.go:
// on the acceptance harness (a real MongoDB, a real IMAP server over TLS, the
// fake model), not in the report. `scripts/scenarios.sh -run '^TestAcceptance'`
// runs them, and so does CI, in the scenarios job.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/ai"
)

// filingOwner is the person whose IMAP mailbox these tests run on.
const filingOwner = "classement@exemple.fr"

// electricityBill is the mail the fake model proposes to file.
var electricityBill = incoming{From: "Énergie <factures@energie.fr>", Subject: "Votre facture de novembre"}

// promptSpy sits between Mailsorter and the fake model: it keeps the prompt
// of every call, then lets the fake model answer it.
type promptSpy struct {
	mu      sync.Mutex
	prompts []string
}

// watch points the app's AI at a spy in front of its fake model.
func (s *promptSpy) watch(app *testApp) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, `{"error":{"message":"unreadable"}}`, http.StatusBadRequest)
			return
		}
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if json.Unmarshal(body, &req) == nil && len(req.Messages) > 0 {
			s.mu.Lock()
			s.prompts = append(s.prompts, req.Messages[len(req.Messages)-1].Content)
			s.mu.Unlock()
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		app.ai.serve(w, r)
	}))
	app.t.Cleanup(server.Close)
	app.h.aiRegistry = ai.NewRegistry([]ai.ProviderConfig{{
		Name:    "openai-compatible",
		APIKey:  "fake-ai-key",
		Model:   "fake-model",
		BaseURL: server.URL,
		Enabled: true,
	}})
}

// labelsLine is the line of the last prompt that lists the user's labels,
// or "" when it lists none.
func (s *promptSpy) labelsLine() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.prompts) == 0 {
		return ""
	}
	for _, line := range strings.Split(s.prompts[len(s.prompts)-1], "\n") {
		if strings.HasPrefix(line, "Labels existants de l'utilisateur:") {
			return line
		}
	}
	return ""
}

// The model is told the person's own folders, and only those, so a proposal
// reuses "Factures" rather than invent "Facture" next to it.
func TestAcceptance_IMAPFilingAnalysisIsToldTheUsersFolders(t *testing.T) {
	app := newTestApp(t, &scenarioResult{theme: "hors rapport"})
	spy := &promptSpy{}
	spy.watch(app)
	moi := app.imapPerson(filingOwner)
	moi.usualSpecialFolders()
	moi.newFolderElsewhere("Factures")
	moi.newFolderElsewhere("Projets/Maison")
	uids := moi.receive(electricityBill)
	moi.sync()

	app.ai.willLabel("Facture", 0.95)
	moi.analyze(uids[0])

	line := spy.labelsLine()
	if line == "" {
		t.Fatalf("the model was told no folder of %s: %q", moi.email, spy.prompts)
	}
	for _, want := range []string{"Factures", "Projets/Maison"} {
		if !strings.Contains(line, want) {
			t.Errorf("the model was not told the folder %q: %q", want, line)
		}
	}
	for _, special := range []string{"INBOX", serverArchive, serverTrash, serverSent, serverDrafts, serverJunk} {
		for _, name := range strings.Split(strings.TrimPrefix(line, "Labels existants de l'utilisateur:"), ",") {
			if strings.EqualFold(strings.TrimSpace(name), special) {
				t.Errorf("the model was told the special folder %q: %q", special, line)
			}
		}
	}
	if s := moi.proposalFor(uids[0]); s.Action != "label" || s.LabelName != "Factures" {
		t.Errorf("the proposal for a model saying \"Facture\" is %q in %q, want label in the existing \"Factures\"", s.Action, s.LabelName)
	}
}

// Over IMAP filing takes the mail out of the inbox, so the analysis does not
// propose it for a protected sender, as it does not propose an archive: the
// plan holds nothing "Tout ranger" would refuse.
func TestAcceptance_IMAPFilingAnalysisKeepsAProtectedSendersMail(t *testing.T) {
	app := newTestApp(t, &scenarioResult{theme: "hors rapport"})
	moi := app.imapPerson(filingOwner)
	control := incoming{From: "Boutique <commandes@boutique.fr>", Subject: "Votre facture de commande"}
	uids := moi.receive(electricityBill, control)
	moi.sync()
	moi.protect(electricityBill.From)

	app.ai.willLabel("Factures", 0.95)
	moi.analyze(uids...)

	if s := moi.proposalFor(uids[0]); s.Action != "keep" || s.LabelName != "" {
		t.Errorf("the proposal for the protected sender's mail is %q in %q, want keep", s.Action, s.LabelName)
	}
	if s := moi.proposalFor(uids[1]); s.Action != "label" || s.LabelName != "Factures" {
		t.Errorf("the proposal for the control is %q in %q, want label in Factures", s.Action, s.LabelName)
	}
}

// "Annuler" in the message that follows "Appliquer" on a filing sends
// "unlabel" on the number the mail had (undoToast in Inbox.js): the mail is
// back in the inbox, out of the folder, and the history line is undone.
func TestAcceptance_IMAPFilingUndoRightAfterApplying(t *testing.T) {
	app := newTestApp(t, &scenarioResult{theme: "hors rapport"})
	moi := app.imapPerson(filingOwner)
	uids := moi.receive(electricityBill)
	moi.sync()
	app.ai.willLabel("Factures", 0.95)
	moi.analyze(uids[0])
	s := moi.proposalFor(uids[0])
	if got := moi.applyProposal(s.ID); got.code != http.StatusOK {
		t.Fatalf("Appliquer answered %s", got)
	}
	moi.requireOnServer("Factures", electricityBill.Subject, "it was just filed")

	// Sent as Inbox.js sends it: undoRightAfter knows only archive and delete.
	code, out := app.callAs(moi.token, http.MethodPost, "/api/emails/action", map[string]string{"messageId": uids[0], "action": "unlabel"})
	if code != http.StatusOK {
		t.Fatalf("Annuler (unlabel) answered %d: %v", code, out)
	}
	moi.expectOnServer(serverInbox, electricityBill.Subject)
	moi.expectNotOnServer("Factures", electricityBill.Subject, "Annuler brought it back")
	moi.expectBackInInbox(electricityBill.Subject, "right after Annuler")
	moi.expectMarkedUndone(moi.historyLineFor(uids[0], "label"), true, "of the filing")
}

// The direct action takes no folder name: "label" there is a request that
// cannot be served, said with a 400, and the mail does not move.
func TestAcceptance_IMAPFilingDirectLabelWithoutAFolderIsRefused(t *testing.T) {
	app := newTestApp(t, &scenarioResult{theme: "hors rapport"})
	moi := app.imapPerson(filingOwner)
	uids := moi.receive(electricityBill)
	moi.sync()

	got := moi.actAnswer(uids[0], "label")
	if got.code != http.StatusBadRequest {
		t.Errorf("label without a folder answered %s, want 400", got)
	}
	if msg, _ := got.body["error"].(string); msg != labelRequiredMessage {
		t.Errorf("label without a folder said %q, want %q", msg, labelRequiredMessage)
	}
	moi.expectOnServer(serverInbox, electricityBill.Subject)
}
