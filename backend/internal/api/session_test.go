package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
)

// The most dangerous line in the routing, and the reason it is a switch on the
// error rather than an `if err != nil`.
//
// Resolving the transport means reading mail_accounts. If a datastore failure
// resolved to "Gmail" the way a missing row does, then a Mongo hiccup would
// silently turn an IMAP user into a Gmail user for the duration, and their UIDs
// would be sent to Gmail as message ids. Only the ABSENCE of a row may mean
// Gmail. Everything else has to fail.
//
// The test handler points Mongo at a dead address on purpose, so this is the
// real failure path, not a simulated one.
func TestTransportForDoesNotFallBackToGmailOnADatastoreError(t *testing.T) {
	h := newTestHandler(t)

	transport, _, err := h.transportFor(cancelledContext(), "someone@example.com")
	if err == nil {
		t.Fatalf("transportFor with an unreachable datastore returned %q and no error; want the error", transport)
	}
	if transport == provider.TransportGmailAPI {
		t.Error("an unreachable datastore resolved to the Gmail transport; an IMAP user's UIDs would go to Gmail")
	}
}

// The guard that makes every unported path safe at once. Most of the app was
// written when every user was a Gmail user; refusing here is what stops those
// paths from acting on the wrong mailbox rather than requiring each to be
// ported before any of it is safe.
func TestGmailClientForRefusesWhenTheTransportIsNotResolved(t *testing.T) {
	h := newTestHandler(t)

	client, err := h.gmailClientFor(cancelledContext(), "someone@example.com")
	if err == nil {
		t.Fatal("gmailClientFor handed out a client without knowing the user's transport")
	}
	if client != nil {
		t.Error("gmailClientFor returned both an error and a client")
	}
}

// A path reached by a mailbox it does not support is not a broken request and
// not a broken server: it is a capability that does not exist there yet. 501
// says that; 400 blames the caller and 500 blames the server.
func TestWriteAuthErrorAnswersTheTransportCaseWith501(t *testing.T) {
	cases := map[string]struct {
		err  error
		want int
	}{
		"not ported to this transport": {errWrongTransport, http.StatusNotImplemented},
		"dead google grant":            {errReauthRequired, http.StatusUnauthorized},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeAuthError(rec, tc.err)
			if rec.Code != tc.want {
				t.Errorf("writeAuthError(%v) = %d, want %d", tc.err, rec.Code, tc.want)
			}
			var body map[string]interface{}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Errorf("the answer is not the JSON envelope every error uses: %v", err)
			}
		})
	}

	// Wrapped, which is how gmailClientFor returns it.
	rec := httptest.NewRecorder()
	writeAuthError(rec, errors.New("resolve: "+errWrongTransport.Error()))
	if rec.Code == http.StatusNotImplemented {
		t.Error("writeAuthError matched on the message text rather than on the error identity")
	}
}

// The session names a message the way its transport names one. This is the
// whole reason a session exists rather than a bare client: the handler holds a
// string the client sent and has no opinion about what kind of id it is.
func TestSessionRefForNamesAMessageTheWayTheTransportDoes(t *testing.T) {
	h := newTestHandler(t)

	gmailSession := &mailSession{Transport: provider.TransportGmailAPI, h: h, userEmail: "a@b.com"}
	ref := gmailSession.RefFor(context.Background(), "18c8c1f2a3b4d5e6")
	if ref.Scoped() {
		t.Errorf("a Gmail session produced a folder-scoped reference: %+v", ref)
	}
	if ref.ID != "18c8c1f2a3b4d5e6" {
		t.Errorf("ref.ID = %q, want the id unchanged", ref.ID)
	}

	imapSession := &mailSession{Transport: provider.TransportIMAP, h: h, userEmail: "a@b.com"}
	ref = imapSession.RefFor(cancelledContext(), "42")
	if !ref.Scoped() {
		t.Errorf("an IMAP session produced a reference with no folder: %+v", ref)
	}
	// The datastore is unreachable here, so the folder cannot be read back. The
	// inbox is the documented fallback: a message is there unless something
	// moved it, and refusing would make the ledger's older entries unusable.
	if ref.Folder != string(mailbox.FolderInbox) {
		t.Errorf("ref.Folder = %q, want %q when the stored folder cannot be read",
			ref.Folder, mailbox.FolderInbox)
	}
	if ref.ID != "42" {
		t.Errorf("ref.ID = %q, want the uid unchanged", ref.ID)
	}
}

// And the adapter follows the transport, so a mutation cannot be translated by
// the wrong table.
func TestSessionMailboxPicksTheTransportsAdapter(t *testing.T) {
	h := newTestHandler(t)

	gmailSession := &mailSession{Transport: provider.TransportGmailAPI, h: h}
	if _, ok := gmailSession.Mailbox().(gmailMailbox); !ok {
		t.Errorf("a Gmail session returned %T, want the Gmail adapter", gmailSession.Mailbox())
	}

	// The two adapters disagree about what the same verb means (marking read
	// removes a label on one and sets a flag on the other), so picking the
	// wrong one does not fail, it does the opposite thing.
	imapSession := &mailSession{Transport: provider.TransportIMAP, h: h}
	if _, ok := imapSession.Mailbox().(gmailMailbox); ok {
		t.Error("an IMAP session returned the Gmail adapter; every verb would be translated by the wrong table")
	}
}

// applyMutations folds a compound act into one call on Gmail, because that
// transport can, and runs them in order otherwise, because IMAP cannot: a flag
// change and a move are different commands there.
func TestApplyMutationsFoldsOnGmailAndSequencesOtherwise(t *testing.T) {
	h := newTestHandler(t)
	rec := &recordingGmail{}

	err := h.applyMutations(context.Background(), gmailMailbox{svc: rec},
		mailbox.OnAccount("m1"),
		mailbox.Mutation{Action: mailbox.ActionLabel, LabelID: "Label_snooze"},
		mailbox.Mutation{Action: mailbox.ActionArchive})
	if err != nil {
		t.Fatalf("applyMutations: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Errorf("Gmail received %d call(s), want the pair folded into 1", len(rec.calls))
	}

	// The sequencing half, through a recorder that is not the Gmail adapter.
	seq := &recordingMailbox{}
	err = h.applyMutations(context.Background(), seq, mailbox.InFolder("INBOX", "42"),
		mailbox.Mutation{Action: mailbox.ActionMarkRead},
		mailbox.Mutation{Action: mailbox.ActionArchive})
	if err != nil {
		t.Fatalf("applyMutations: %v", err)
	}
	if len(seq.applied) != 2 {
		t.Fatalf("a non-folding transport received %d mutation(s), want 2 in order", len(seq.applied))
	}
	if seq.applied[0].Action != mailbox.ActionMarkRead || seq.applied[1].Action != mailbox.ActionArchive {
		t.Errorf("order = %v, want markRead then archive: a move invalidates the id, so it has to be last",
			seq.applied)
	}
}

// recordingMailbox stands in for any transport that is not Gmail.
type recordingMailbox struct {
	applied []mailbox.Mutation
	refs    []mailbox.Ref
	err     error
}

func (r *recordingMailbox) Apply(_ context.Context, ref mailbox.Ref, m mailbox.Mutation) error {
	r.refs = append(r.refs, ref)
	r.applied = append(r.applied, m)
	return r.err
}

// A failing mutation stops the sequence. Carrying on would leave the message in
// a state no single verb describes, and the ledger would record acts that did
// not happen.
func TestApplyMutationsStopsAtTheFirstFailure(t *testing.T) {
	h := newTestHandler(t)
	seq := &recordingMailbox{err: errors.New("server said no")}

	err := h.applyMutations(context.Background(), seq, mailbox.InFolder("INBOX", "42"),
		mailbox.Mutation{Action: mailbox.ActionMarkRead},
		mailbox.Mutation{Action: mailbox.ActionArchive})
	if err == nil {
		t.Fatal("applyMutations swallowed a transport failure")
	}
	if len(seq.applied) != 1 {
		t.Errorf("%d mutation(s) were attempted after the first failed, want the sequence stopped", len(seq.applied))
	}
}
