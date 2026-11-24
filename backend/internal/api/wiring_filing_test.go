//go:build acceptance

package api

// Filing into folders on an IMAP mailbox, around the locked sentences of
// scenarios_8_imap_ai_test.go: what the model is NOT told (no label, no
// folder: its verdict goes into a cache every account shares), how its
// proposal is matched with the user's folders afterwards, what the analysis
// proposes for a protected sender or a special folder, how many folders one
// "Tout ranger" makes, "Annuler" right after a single "Appliquer", and the
// direct action without a folder. Wiring, like wiring_imap_test.go:
// on the acceptance harness (a real MongoDB, a real IMAP server over TLS, the
// fake model), not in the report. `scripts/scenarios.sh -run '^TestAcceptance'`
// runs them, and so does CI, in the scenarios job.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/ai"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
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

// all is every prompt the model received, joined.
func (s *promptSpy) all() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.prompts, "\n")
}

// expectNothingOf checks that no prompt names any of these, nor lists labels
// at all: the model sees the sender, the subject and an excerpt, nothing of
// the person's mailbox (AI_READS in landing/features.js, Privacy.js).
func (s *promptSpy) expectNothingOf(t *testing.T, names ...string) {
	t.Helper()
	all := s.all()
	if all == "" {
		t.Fatal("the model received no prompt: the test proves nothing")
	}
	if strings.Contains(all, "Labels existants") {
		t.Errorf("a prompt lists the person's labels: %q", all)
	}
	for _, name := range names {
		if strings.Contains(all, name) {
			t.Errorf("a prompt names %q, which is the person's: %q", name, all)
		}
	}
}

// The model is told nothing of the person's folders; its "Facture" is matched
// afterwards, for this account, with the existing "Factures", and its "Pro"
// does not take the folder "Promotions" (a name, not a part of one).
func TestAcceptance_IMAPFilingModelIsToldNoFolder(t *testing.T) {
	app := newTestApp(t, &scenarioResult{theme: "hors rapport"})
	spy := &promptSpy{}
	spy.watch(app)
	moi := app.imapPerson(filingOwner)
	moi.usualSpecialFolders()
	moi.newFolderElsewhere("Factures")
	moi.newFolderElsewhere("Promotions")
	moi.newFolderElsewhere("Projets/Dossier secret")
	shop := incoming{From: "Boutique <offres@boutique.fr>", Subject: "Nos offres pros"}
	uids := moi.receive(electricityBill, shop)
	moi.sync()

	app.ai.willLabel("Facture", 0.95)
	moi.analyze(uids[0])
	app.ai.willLabel("Pro", 0.95)
	moi.analyze(uids[1])

	// "Factures" is in the prompt already, as one of its examples: the
	// person's other folders say whether theirs went out.
	spy.expectNothingOf(t, "Promotions", "Dossier secret")
	if s := moi.proposalFor(uids[0]); s.Action != "label" || s.LabelName != "Factures" {
		t.Errorf("the proposal for a model saying \"Facture\" is %q in %q, want label in the existing \"Factures\"", s.Action, s.LabelName)
	}
	if s := moi.proposalFor(uids[1]); s.Action != "label" || s.LabelName != "Pro" {
		t.Errorf("the proposal for a model saying \"Pro\" is %q in %q, want label in a new \"Pro\", not \"Promotions\"", s.Action, s.LabelName)
	}
}

// Over the Gmail API too, the labels Mailsorter made are not told to the
// model: the same promise holds on both transports.
func TestAcceptance_GmailAnalysisIsToldNoLabel(t *testing.T) {
	app := newTestApp(t, &scenarioResult{theme: "hors rapport"})
	spy := &promptSpy{}
	spy.watch(app)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := app.h.db.SmartLabels().InsertOne(ctx, models.SmartLabel{UserID: scenarioUser, Name: "Dossier secret", GmailLabelID: "Label_1", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("storing a label of the person: %v", err)
	}
	ids := app.receiveAndSync(incoming{From: "Banque <releves@banque.fr>", Subject: "Votre relevé"})
	app.ai.willLabel("Dossiers secrets", 0.95)
	app.analyze(ids...)

	spy.expectNothingOf(t, "Dossier secret")
	if s := app.suggestionFor(ids[0]); s.LabelName != "Dossier secret" {
		t.Errorf("the proposal names %q, want the person's own \"Dossier secret\", matched afterwards", s.LabelName)
	}
}

// A proposal that names a special folder, or a name no folder is made under,
// is turned into "keep" by the analysis: the plan holds nothing "Appliquer"
// would refuse. A name that holds the server's delimiter is cleaned there, on
// a server whose delimiter is "/", nothing to clean in "Amazon.fr".
func TestAcceptance_IMAPFilingAnalysisKeepsWhatCannotBeFiled(t *testing.T) {
	app := newTestApp(t, &scenarioResult{theme: "hors rapport"})
	moi := app.imapPerson(filingOwner)
	moi.usualSpecialFolders()
	mails := []incoming{
		{From: "A <a@exemple.fr>", Subject: "Un"},
		{From: "B <b@exemple.fr>", Subject: "Deux"},
		{From: "C <c@exemple.fr>", Subject: "Trois"},
	}
	uids := moi.receive(mails...)
	moi.sync()

	for i, verdict := range []string{serverTrash, "Fact*", "Trash/Factures"} {
		app.ai.willLabel(verdict, 0.95)
		moi.analyze(uids[i])
		if s := moi.proposalFor(uids[i]); s.Action != "keep" || s.LabelName != "" {
			t.Errorf("the proposal for a model saying label %q is %q in %q, want keep", verdict, s.Action, s.LabelName)
		}
	}
}

// One "Tout ranger" makes at most ten new folders: the eleventh mail stays in
// the inbox, counted as failed, and the answer says why.
func TestAcceptance_IMAPFilingMakesAtMostTenFoldersAtOnce(t *testing.T) {
	app := newTestApp(t, &scenarioResult{theme: "hors rapport"})
	moi := app.imapPerson(filingOwner)
	mails := numberedMails("Lettre n°%d", 11)
	uids := moi.receive(mails...)
	moi.sync()
	ids := make([]string, 0, len(uids))
	for i, uid := range uids {
		app.ai.willLabel(fmt.Sprintf("Dossier %d", i+1), 0.95)
		moi.analyze(uid)
		ids = append(ids, moi.proposalFor(uid).ID)
	}

	got := moi.tidyAll(ids...)
	if got.code != http.StatusOK {
		t.Fatalf("Tout ranger answered %s", got)
	}
	if n := app.number(got.body, "applied"); n != 10 {
		t.Errorf("Tout ranger filed %d mails, want 10, one per new folder", n)
	}
	if n := app.number(got.body, "failed"); n != 1 {
		t.Errorf("Tout ranger counts %d failed, want the one past ten new folders", n)
	}
	if msg, _ := got.body["message"].(string); msg != tooManyNewFoldersMessage {
		t.Errorf("Tout ranger says %q, want %q", msg, tooManyNewFoldersMessage)
	}
	if moi.hasFolder("Dossier 11") {
		t.Error("an eleventh folder was made")
	}
	moi.expectOnServer(serverInbox, mails[10].Subject)
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

	// "unlabel", as undoToast in Inbox.js sends it after a filing; the
	// harness's undoRightAfter sends "untrash" for anything but an archive.
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
