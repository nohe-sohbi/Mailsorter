package api

import (
	"context"
	"fmt"

	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
	gmailapi "google.golang.org/api/gmail/v1"
)

// gmailMailbox is the Gmail implementation of mailbox.Mailbox. It lives here
// rather than in internal/mailbox because it holds a live API client: the
// vocabulary and its translation are pure, performing the call is not.
//
// It is deliberately thin. Everything that decides what a verb means was moved
// into mailbox.GmailLabels, so the next adapter (IMAP, then Graph) is the same
// four lines against a different translation function rather than a second copy
// of the switch that used to live in forty places.
type gmailMailbox struct {
	svc    gmailService
	client *gmailapi.Service
}

// gmailService is the subset of internal/gmail this adapter needs. Naming it
// keeps the adapter honest about its dependency, and lets a test substitute a
// recorder without a live client.
type gmailService interface {
	ModifyMessage(gmailService *gmailapi.Service, messageID string, addLabels, removeLabels []string) error
}

func (m gmailMailbox) Apply(ctx context.Context, ref mailbox.Ref, mut mailbox.Mutation) error {
	return m.ApplyAll(ctx, ref, mut)
}

// ApplyAll performs several mutations in ONE modify call. A compound act (snooze
// parks a message by labelling AND archiving it) must not be split into separate
// requests: it would multiply the traffic on the sweeper that runs every minute,
// and a failure between the two would leave the message visibly half-moved.
func (m gmailMailbox) ApplyAll(ctx context.Context, ref mailbox.Ref, muts ...mailbox.Mutation) error {
	// A folder-scoped reference here means the caller is holding an IMAP UID and
	// about to send it to Gmail as a message id. Gmail would answer 404 on a
	// good day and act on an unrelated message on a bad one, so it is refused
	// rather than ignored: the Gmail API has no folders, and a ref that carries
	// one did not come from this transport.
	if ref.Scoped() {
		return fmt.Errorf("%w: gmail ids are account-wide, got %s", mailbox.ErrWrongRefKind, ref)
	}
	add, remove, err := mailbox.GmailLabelsFor(muts)
	if err != nil {
		return err
	}
	// The Gmail client does not take a context: its calls are wrapped by the
	// retry policy in internal/gmail and bounded by the server's write timeout.
	// The parameter stays on the interface because the next transport (IMAP,
	// which holds a connection) genuinely needs one, and adding it later would
	// mean touching every call site a second time.
	_ = ctx
	return m.svc.ModifyMessage(m.client, ref.ID, add, remove)
}

// mailboxOf wraps an authenticated Gmail client as a Mailbox. Call sites hold a
// client already (they need it for reads and label creation too), so this is a
// wrap rather than a lookup.
func (h *Handler) mailboxOf(client *gmailapi.Service) mailbox.Mailbox {
	return gmailMailbox{svc: h.gmailService, client: client}
}

// applyVerb is the one bridge from a wire verb to a mutation, used by every
// handler that acts on a message. Resolving the verb here means an unsupported
// action is refused once, in a typed way, instead of falling through a switch
// default in each of the places that used to have one.
// It takes a Mailbox rather than a Gmail client so the same bridge serves both
// transports: a Gmail-only path passes h.mailboxOf(client), and a path that has
// been ported passes session.Mailbox(). One function, so a verb cannot mean one
// thing on one transport and something else on the other.
func (h *Handler) applyVerb(ctx context.Context, mb mailbox.Mailbox, ref mailbox.Ref, verb, labelID string) error {
	action, err := mailbox.Parse(verb)
	if err != nil {
		return err
	}
	return mb.Apply(ctx, ref, mailbox.Mutation{Action: action, LabelID: labelID})
}

// applyMutations performs a compound act in one call. Used where the intent is
// several verbs at once rather than a verb the API accepted.
// Compound acts are folded into one call where the transport allows it (the
// Gmail API does), and run in order where it does not (IMAP, where a flag
// change and a move are different commands).
func (h *Handler) applyMutations(ctx context.Context, mb mailbox.Mailbox, ref mailbox.Ref, muts ...mailbox.Mutation) error {
	if gm, ok := mb.(gmailMailbox); ok {
		return gm.ApplyAll(ctx, ref, muts...)
	}
	for _, m := range muts {
		if err := mb.Apply(ctx, ref, m); err != nil {
			return err
		}
	}
	return nil
}
