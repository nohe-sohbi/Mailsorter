package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
	"github.com/nohe-sohbi/mailsorter/backend/internal/snooze"
	"go.mongodb.org/mongo-driver/bson"
)

// The generation of a mailbox.
//
// An IMAP message number only means something in one mailbox: the next
// mailbox connected gives the same numbers to its own mails. So what
// Mailsorter keeps of an IMAP mailbox (the copy of its inbox, the AI's
// proposals, the history) carries the mailbox it was read from or done on:
// its generation, the _id of the mail_accounts row the work went through.
//
//   - A different mailbox connected gets a row of its own, so a generation
//     of its own (connectAndStore). The same mailbox connected again by the
//     same route keeps its row, its generation and what was kept of it; once
//     its row is deleted (disconnected), it comes back as a new generation.
//   - Every read of the copy, of the proposals and of what "Annuler" acts on
//     asks for the generation connected now; every write carries the one the
//     operation opened (mailSession.generation). An operation still running
//     on a mailbox since replaced (a sync, a run of the rules, "Annuler")
//     writes rows nobody reads: it cannot reach the next mailbox's.
//   - What is not of the generation connected now is erased
//     (clearMailboxCopy): at a connection, at a disconnection, and at the
//     start of each sync, which erases what such an operation wrote late.
//
// The screen holds message numbers too: it sends the generation it loaded
// (X-Mailbox-Generation, from GET /api/mailbox), and a request from a screen
// loaded on another mailbox is refused (errMailboxChanged) before it names a
// mail by a number of the old one.
//
// The Gmail API names a message on the account, whatever was connected
// before: its rows carry no generation ("").

// generationOf is the generation a mailbox reached by this transport and
// stored as this row is: the row's _id over IMAP, "" over the Gmail API.
func generationOf(transport provider.Transport, account models.MailAccount) string {
	if transport == provider.TransportIMAP {
		return account.ID
	}
	return ""
}

// generation is the generation of the mailbox this session reaches.
func (s *mailSession) generation() string {
	return generationOf(s.Transport, s.account)
}

// inGeneration is what a filter matches a generation with: the generation
// itself, or, for "" (the Gmail API), the rows that carry none.
func inGeneration(gen string) interface{} {
	if gen == "" {
		return nil
	}
	return gen
}

// notGeneration is what a filter matches every other generation with: over
// the Gmail API ("") the rows that carry one, otherwise the rows that carry
// another or none.
func notGeneration(gen string) bson.M {
	if gen == "" {
		return bson.M{"$exists": true}
	}
	return bson.M{"$ne": gen}
}

// currentGeneration is the generation connected now, for a read that opens no
// session: "" over the Gmail API and with no mailbox connected, where no row
// of an IMAP mailbox is read.
func (h *Handler) currentGeneration(ctx context.Context, userEmail string) (string, error) {
	transport, account, err := h.transportFor(ctx, userEmail)
	if errors.Is(err, errNoMailboxConnected) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return generationOf(transport, account), nil
}

// mailboxGenerationHeader carries the mailbox the screen loaded, as GET
// /api/mailbox named it (mailboxStamp).
const mailboxGenerationHeader = "X-Mailbox-Generation"

// mailboxStamp is how the screen names the mailbox it loaded: its generation
// over IMAP, "gmail" over the Gmail API, whose ids name a message on the
// account. Empty with no mailbox.
func mailboxStamp(transport provider.Transport, account models.MailAccount) string {
	if transport == provider.TransportIMAP {
		return account.ID
	}
	return "gmail"
}

// errMailboxChanged is a request from a screen loaded on another mailbox than
// the one connected now: its message numbers are the other one's. Nothing is
// done; the user reads mailboxChangedMessage (409).
var errMailboxChanged = errors.New("api: the screen was loaded on another mailbox than the one connected")

// mailboxChangedMessage is what the user reads for errMailboxChanged.
const mailboxChangedMessage = "Votre boîte a changé depuis l'ouverture de cette page : rechargez-la."

type screenMailboxKey struct{}

// screenMailboxMiddleware puts the mailbox the screen loaded, when it says
// one, in the request's context, where transportFor checks it.
func screenMailboxMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if stamp := r.Header.Get(mailboxGenerationHeader); stamp != "" {
			r = r.WithContext(context.WithValue(r.Context(), screenMailboxKey{}, stamp))
		}
		next.ServeHTTP(w, r)
	})
}

// checkScreenMailbox answers errMailboxChanged when the request comes from a
// screen loaded on another mailbox than this one. A request that names none
// (an older screen, a background job) is not checked: the generation of what
// it reads and writes still keeps each mailbox's apart.
func checkScreenMailbox(ctx context.Context, transport provider.Transport, account models.MailAccount) error {
	stamp, ok := ctx.Value(screenMailboxKey{}).(string)
	if !ok || stamp == mailboxStamp(transport, account) {
		return nil
	}
	return errMailboxChanged
}

// writeMailboxChanged answers a request refused by errMailboxChanged: the
// usual envelope, and mailboxChanged, by which the screen tells this 409 from
// the others and loads the mailbox again (api.js).
func writeMailboxChanged(w http.ResponseWriter) {
	writeJSON(w, http.StatusConflict, map[string]interface{}{
		"error": mailboxChangedMessage, "status": http.StatusConflict, "mailboxChanged": true,
	})
}

// ofCurrentGeneration says whether a history entry is of the mailbox
// connected now (gen, "" over the Gmail API or with none): the only ones
// "Annuler" acts on. The others are final.
func ofCurrentGeneration(entry models.ActionLog, gen string) bool {
	return entry.Generation == gen
}

// clearMailboxCopy erases what Mailsorter kept of the IMAP mailboxes that are
// not connected now: the copy of their inbox (moved-out rows kept for
// "Annuler" included) and the AI's pending proposals about their mails; and
// it ends their reports still open (cancelled), whose mails stay where they
// wait. The history stays, final on them (ofCurrentGeneration). The rows of a
// Gmail mailbox, which carry no generation, are not touched.
//
// The generations to erase are read before the one connected now: one that is
// not connected at that moment never is again (a mailbox connected again
// after a disconnection is a new generation), so a mailbox connected between
// the two reads, whose rows were not in the first, loses nothing.
func (h *Handler) clearMailboxCopy(ctx context.Context, userEmail string) error {
	copyGens, err := h.db.Emails().Distinct(ctx, "generation", bson.M{"userId": userEmail, "folder": bson.M{"$exists": true}})
	if err != nil {
		return err
	}
	proposalGens, err := h.db.AISuggestions().Distinct(ctx, "generation", bson.M{"userId": userEmail, "status": "pending"})
	if err != nil {
		return err
	}
	current, err := h.currentGeneration(withoutScreen(ctx), userEmail)
	if err != nil {
		return err
	}
	// A row of the copy without a generation is of a mailbox connected
	// before rows carried one, and not connected at boot (the database's
	// stampGenerations gave the connected ones theirs).
	if _, err := h.db.Emails().DeleteMany(ctx, bson.M{
		"userId": userEmail, "folder": bson.M{"$exists": true},
		"$or": []bson.M{
			{"generation": bson.M{"$in": otherGenerations(copyGens, current)}},
			{"generation": bson.M{"$exists": false}},
		},
	}); err != nil {
		return err
	}
	// A proposal without a generation is of the Gmail API: of the mailbox
	// connected now when that is a Gmail one (or none: it is kept, as its
	// listing is), of another when an IMAP mailbox is.
	stale := []bson.M{{"generation": bson.M{"$in": otherGenerations(proposalGens, current)}}}
	if current != "" {
		stale = append(stale, bson.M{"generation": bson.M{"$exists": false}})
	}
	if _, err := h.db.AISuggestions().DeleteMany(ctx, bson.M{"userId": userEmail, "status": "pending", "$or": stale}); err != nil {
		return err
	}
	// A report of another mailbox ends (cancelled): its hour would bring
	// back, in the mailbox connected now, the mail of that number. Its mail
	// stays where it waits, in that other mailbox. Read with the others:
	// a report without a generation is of the Gmail API, as a proposal is.
	reportGens, err := h.db.Snoozes().Distinct(ctx, "generation", bson.M{"userId": userEmail, "status": bson.M{"$in": openReports}})
	if err != nil {
		return err
	}
	staleReports := []bson.M{{"generation": bson.M{"$in": otherGenerations(reportGens, current)}}}
	if current != "" {
		staleReports = append(staleReports, bson.M{"generation": bson.M{"$exists": false}})
	}
	if _, err := h.db.Snoozes().UpdateMany(ctx,
		bson.M{"userId": userEmail, "status": bson.M{"$in": openReports}, "$or": staleReports},
		bson.M{"$set": bson.M{"status": snooze.StatusCancelled, "updatedAt": time.Now()}},
	); err != nil {
		return err
	}
	return nil
}

// openReports are the statuses of a report whose mail still waits:
// scheduled, and given up (failed), which "Réactiver" may still bring back.
var openReports = []string{snooze.StatusScheduled, snooze.StatusFailed}

// otherGenerations is, of the generations Distinct read, the ones that are
// not current.
func otherGenerations(gens []interface{}, current string) []string {
	out := make([]string, 0, len(gens))
	for _, g := range gens {
		if s, ok := g.(string); ok && s != "" && s != current {
			out = append(out, s)
		}
	}
	return out
}

// withoutScreen is ctx without the mailbox the screen loaded: what the server
// does for itself (erasing what is not connected now) is not refused for a
// screen loaded on another mailbox.
func withoutScreen(ctx context.Context) context.Context {
	if _, ok := ctx.Value(screenMailboxKey{}).(string); !ok {
		return ctx
	}
	return context.WithValue(ctx, screenMailboxKey{}, nil)
}

// clearOtherGenerations is clearMailboxCopy at the start of a sync: a failure
// is logged, and the next sync erases them (they are not read meanwhile).
func (h *Handler) clearOtherGenerations(ctx context.Context, userEmail string) {
	if err := h.clearMailboxCopy(ctx, userEmail); err != nil {
		log.Printf("imap sync: erase what was kept of the mailboxes of %s not connected now, done at the next sync: %v", userEmail, err)
	}
}

// inboxOf is the filter of the copy of the inbox of one generation.
func inboxOf(userEmail, gen string) bson.M {
	return bson.M{"userId": userEmail, "generation": inGeneration(gen), "folder": string(mailbox.FolderInbox)}
}
