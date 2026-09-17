package api

import (
	"context"
	"errors"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
	gmailapi "google.golang.org/api/gmail/v1"
)

// recordingGmail is the smallest thing that satisfies gmailService: it keeps
// what it was asked to do instead of doing it. Naming the interface in the
// adapter is what makes this possible without a live client or a mock library.
type recordingGmail struct {
	calls []modifyCall
	err   error
}

type modifyCall struct {
	messageID string
	add       []string
	remove    []string
}

func (r *recordingGmail) ModifyMessage(_ *gmailapi.Service, messageID string, add, remove []string) error {
	r.calls = append(r.calls, modifyCall{messageID: messageID, add: add, remove: remove})
	return r.err
}

// A folder-scoped reference reaching the Gmail adapter means a caller is holding
// an IMAP UID and is about to send it to Gmail as a message id. Gmail has no
// folders, so the ref did not come from this transport. Refusing loudly is the
// point: passing it through would act on whatever message carries that number,
// and the ledger would record a success.
func TestGmailMailboxRefusesAFolderScopedReference(t *testing.T) {
	rec := &recordingGmail{}
	mb := gmailMailbox{svc: rec}

	err := mb.Apply(context.Background(), mailbox.InFolder("INBOX", "42"), mailbox.Mutation{Action: mailbox.ActionArchive})
	if !errors.Is(err, mailbox.ErrWrongRefKind) {
		t.Errorf("Apply with a folder-scoped ref = %v, want mailbox.ErrWrongRefKind", err)
	}
	if len(rec.calls) != 0 {
		t.Errorf("the adapter made %d call(s) to Gmail; a refused reference must reach nothing", len(rec.calls))
	}
}

func TestGmailMailboxSendsTheAccountIDThrough(t *testing.T) {
	rec := &recordingGmail{}
	mb := gmailMailbox{svc: rec}

	if err := mb.Apply(context.Background(), mailbox.OnAccount("18c8c1f2a3b4d5e6"), mailbox.Mutation{Action: mailbox.ActionArchive}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("got %d modify call(s), want 1", len(rec.calls))
	}
	call := rec.calls[0]
	if call.messageID != "18c8c1f2a3b4d5e6" {
		t.Errorf("messageID = %q, want the account id unchanged", call.messageID)
	}
	if len(call.add) != 0 || len(call.remove) != 1 || call.remove[0] != "INBOX" {
		t.Errorf("archive sent add %v remove %v, want add [] remove [INBOX]", call.add, call.remove)
	}
}

// The compound path is why ApplyAll exists: snooze parks a message by labelling
// AND archiving it, and splitting that into two requests would double the
// traffic on a sweeper that runs every minute and could leave the message
// visibly half-moved.
func TestGmailMailboxFoldsACompoundActIntoOneCall(t *testing.T) {
	rec := &recordingGmail{}
	mb := gmailMailbox{svc: rec}

	err := mb.ApplyAll(context.Background(), mailbox.OnAccount("m1"),
		mailbox.Mutation{Action: mailbox.ActionLabel, LabelID: "Label_snooze"},
		mailbox.Mutation{Action: mailbox.ActionArchive})
	if err != nil {
		t.Fatalf("ApplyAll: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("got %d modify call(s), want the pair folded into 1", len(rec.calls))
	}
	call := rec.calls[0]
	if len(call.add) != 1 || call.add[0] != "Label_snooze" {
		t.Errorf("add = %v, want [Label_snooze]", call.add)
	}
	if len(call.remove) != 1 || call.remove[0] != "INBOX" {
		t.Errorf("remove = %v, want [INBOX]", call.remove)
	}
}

// applyVerb, the bridge from a wire verb to a mutation, is not tested here:
// Handler.gmailService is the concrete *gmail.Service, so the recorder above
// cannot be injected into a Handler. Its two halves are covered where they
// live. mailbox.Parse is pinned verb by verb in internal/mailbox, including
// every spelling the ledger holds, and what the resolved action becomes on the
// wire is the subject of the three tests above.
