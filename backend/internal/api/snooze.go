package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/nohe-sohbi/mailsorter/backend/internal/gmail"
	"github.com/nohe-sohbi/mailsorter/backend/internal/imap"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/protect"
	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
	"github.com/nohe-sohbi/mailsorter/backend/internal/snooze"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	gmailapi "google.golang.org/api/gmail/v1"
)

// snoozeLabelName is the Gmail label applied to snoozed mail so it stays
// findable in Gmail itself while it is out of the inbox.
const snoozeLabelName = "Mailsorter/Reporté"

// snoozeFolderName is, on a mailbox reached over IMAP, the folder a reported
// mail waits in. IMAP has no labels: a mail is in one folder, so reporting it
// moves it there, as filing does (made, and subscribed, the first time, under
// the user's prefix), and its hour moves it back. The person sees it on their
// phone by this name, and may take a mail out of it before its hour.
const snoozeFolderName = "Plus tard"

// snoozeSweepInterval is how often the background loop checks for emails whose
// snooze has elapsed and brings them back.
const snoozeSweepInterval = time.Minute

// maxSnoozeWakeAttempts caps how many times a due snooze is retried before it is
// parked as "failed". Without it, a wake that can never succeed (e.g. the message
// was hard deleted) would be re-attempted on every sweep indefinitely.
const maxSnoozeWakeAttempts = 5

// snoozeAccountBound is how long the sweep gives one mailbox to bring its due
// reports back, through its one session, the opening of that session
// included: an IMAP session is bound to the context it was opened under
// (imap.Connect), so past it the connection is dropped. A server that stops
// answering, at LOGIN or later, would otherwise hold the sweep, and every
// other account's reports, with it. What it did not reach waits for the next pass, untouched and not
// counted. A variable, so a test can shorten it.
var snoozeAccountBound = 30 * time.Second

// Snooze pulls a message out of the inbox until a chosen wake time. It resolves
// the wake time from a friendly preset (or an explicit timestamp) and takes the
// message out of the inbox: over the Gmail API it archives it (removing INBOX)
// and tags it with the snooze label so it is easy to find; over IMAP it moves
// it into the folder "Plus tard" (snoozeIMAP). A background loop returns it to
// the inbox, marked unread, when due.
func (h *Handler) Snooze(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	var req models.SnoozeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.MessageID == "" {
		writeError(w, http.StatusBadRequest, "Message ID required")
		return
	}

	now := time.Now()
	wakeAt, wErr := resolveWakeAt(req.Preset, req.WakeAt, now)
	if wErr != nil {
		writeError(w, http.StatusBadRequest, wErr.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	session, err := h.openSession(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	defer session.Close()

	if session.Transport == provider.TransportIMAP {
		err := h.snoozeIMAP(ctx, session, userEmail, req.MessageID, wakeAt, now)
		switch {
		case errors.Is(err, errAlreadyOut):
			// It had already left the inbox (reported a moment ago, or moved
			// by another mail client): nothing happened, so nothing is
			// recorded, and the screen is told so.
			writeJSON(w, http.StatusOK, map[string]interface{}{"status": "already", "wakeAt": wakeAt})
		case errors.Is(err, errLeftInbox):
			writeError(w, http.StatusNotFound, leftInboxMessage)
		case errors.Is(err, errCannotFindAgain):
			writeError(w, http.StatusUnprocessableEntity, cannotFindAgainMessage)
		case err != nil:
			if why, ok := filingRefusal(err); ok {
				writeError(w, http.StatusBadGateway, "Report impossible : "+why)
				return
			}
			writeError(w, http.StatusBadGateway, "Report impossible : "+err.Error())
		default:
			writeJSON(w, http.StatusOK, map[string]interface{}{"status": "snoozed", "wakeAt": wakeAt})
		}
		return
	}
	gmailClient := session.gmail

	// Enrich the record with sender/subject for the snoozed list (best-effort).
	var identity models.Email
	if msg, mErr := h.gmailService.GetMessage(gmailClient, req.MessageID); mErr == nil {
		from, subject, _, _ := gmail.ParseEmailHeaders(msg)
		identity = models.Email{MessageID: req.MessageID, From: from, Subject: subject, ThreadID: msg.ThreadId}
	}

	labelID, err := h.ensureLabel(ctx, gmailClient, userEmail, snoozeLabelName)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Impossible de préparer le report")
		return
	}

	if err := h.snoozeMessage(ctx, gmailClient, userEmail, labelID, identity, wakeAt, now); err != nil {
		writeError(w, http.StatusBadGateway, "Report impossible : "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "snoozed",
		"wakeAt": wakeAt,
	})
}

// resolveWakeAt turns a request's preset-or-timestamp pair into one concrete
// wake time. An explicit timestamp wins and is validated on its own terms (in
// the future, inside the supported horizon) rather than merely being "not in the
// past": presets cannot produce a bad time, a date picker can. Shared by the
// single and batch snooze routes so both accept exactly the same input.
func resolveWakeAt(preset string, explicit, now time.Time) (time.Time, error) {
	if explicit.IsZero() {
		resolved, err := snooze.Resolve(preset, now)
		if err != nil {
			return time.Time{}, errInvalidSnoozeDeadline
		}
		return resolved, nil
	}
	// Surfaced verbatim, like rules.Validate: the pure package owns the wording
	// so the same sentence reaches the user from every caller.
	if err := snooze.ValidateWake(explicit, now); err != nil {
		return time.Time{}, err
	}
	return explicit, nil
}

// errInvalidSnoozeDeadline is what an unknown preset reports. The preset names
// are ours, not the user's, so the message names the fix rather than the value.
var errInvalidSnoozeDeadline = errors.New("choisissez une échéance valide")

// snoozeMessage performs one snooze: out of the inbox, tagged with the snooze
// label, recorded as scheduled and journaled. The caller resolves the wake time
// and the label id once (they are identical across a batch) and passes whatever
// identity it already holds, so the batch route costs one label lookup rather
// than one per message.
func (h *Handler) snoozeMessage(ctx context.Context, gmailClient *gmailapi.Service, userEmail, labelID string, identity models.Email, wakeAt, now time.Time) error {
	messageID := identity.MessageID
	// One call, two verbs: tag it and take it out of the inbox. See
	// mailbox.GmailLabelsFor for why this must not become two requests.
	if err := h.applyMutations(ctx, h.mailboxOf(gmailClient), mailbox.OnAccount(messageID),
		mailbox.Mutation{Action: mailbox.ActionLabel, LabelID: labelID},
		mailbox.Mutation{Action: mailbox.ActionArchive},
	); err != nil {
		return err
	}
	// The message is out of the inbox: its report and its history line are
	// written under their own time, so a request ending right now cannot
	// leave it archived with nothing to bring it back.
	ctx, cancel := settleCtx(ctx)
	defer cancel()

	if _, err := h.db.Snoozes().UpdateOne(ctx,
		bson.M{"userId": userEmail, "messageId": messageID, "status": snooze.StatusScheduled},
		bson.M{
			"$set": bson.M{
				"from": identity.From, "subject": identity.Subject, "threadId": identity.ThreadID,
				"wakeAt": wakeAt, "status": snooze.StatusScheduled, "updatedAt": now,
			},
			"$setOnInsert": bson.M{
				"userId": userEmail, "messageId": messageID, "createdAt": now,
			},
		},
		options.Update().SetUpsert(true),
	); err != nil {
		// The message is already out of the inbox at this point. Without a
		// scheduled row nothing would ever bring it back, so this is a real
		// failure and not something to swallow.
		return fmt.Errorf("le report n'a pas pu être enregistré: %w", err)
	}

	h.logActionMeta(ctx, userEmail, messageID, "archive", SourceSnooze, identity.Subject, identity.From)
	return nil
}

// errCannotFindAgain is reporting, over IMAP, a mail that could not be found
// again at its hour: it has no Message-ID header, and the server does not say
// where a move lands (imap.Client.SaysWhereMovesLand). Nothing moved.
var errCannotFindAgain = errors.New("api: the mail could not be found again once reported")

// cannotFindAgainMessage is what the user reads for errCannotFindAgain.
const cannotFindAgainMessage = "Ce mail ne peut pas être reporté sur cette boîte : Mailsorter ne pourrait pas le retrouver à l'heure prévue."

// snoozedRow is a stored inbox message, as reporting it over IMAP reads it.
// Spelled out rather than embedding movedRow: the bson codec skips an
// embedded struct of an unexported type, and the Message-ID would read empty.
type snoozedRow struct {
	HeaderMessageID string `bson:"headerMessageId"`
	From            string `bson:"from"`
	Subject         string `bson:"subject"`
}

// snoozeIMAP performs one snooze on a mailbox reached over IMAP: the mail is
// moved into the folder "Plus tard" like an archive, through the stored copy
// of the inbox (mirroredIMAP: the row is claimed, hidden from every list at
// once, and records where the mail landed), the report is recorded with that
// place, which is where its hour fetches it from, and it is journaled as the
// Gmail one is. The folder is made the first time, as filing makes one
// (imap.Client.Destination): there is one way to make a folder.
//
// Only a mail the stored copy holds: where it lands is recorded there, and a
// mail moved without that record could not be found again at its hour.
// errLeftInbox otherwise, and errAlreadyOut for a mail already out of the
// inbox (reported a moment ago, or moved by another mail client first).
func (h *Handler) snoozeIMAP(ctx context.Context, session *mailSession, userEmail, messageID string, wakeAt, now time.Time) error {
	inbox := string(mailbox.FolderInbox)
	var before snoozedRow
	if err := h.db.Emails().FindOne(ctx, bson.M{"userId": userEmail, "folder": inbox, "messageId": messageID}).Decode(&before); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return errLeftInbox
		}
		return fmt.Errorf("read the stored copy of message %s: %w", messageID, err)
	}
	// Its hour finds it in "Plus tard" by the number the server gives it
	// there, or else by its Message-ID header. A server that does not say
	// where a move lands, with a mail that has no Message-ID, would leave it
	// there for good: refused before anything moves or is made.
	if before.HeaderMessageID == "" && !session.imapc.SaysWhereMovesLand() {
		return errCannotFindAgain
	}

	into := mailbox.Mutation{Action: mailbox.ActionLabel, LabelID: snoozeFolderName}
	if err := h.applyMutations(ctx, session.Mailbox(), session.RefFor(ctx, messageID), into); err != nil {
		return err
	}
	// The mail is in "Plus tard": its report and its history line are written
	// under their own time, so a request ending right now cannot leave it
	// there with nothing to bring it back.
	ctx, cancel := settleCtx(ctx)
	defer cancel()

	// Where it landed, as the move recorded it. A record that did not reach
	// the store (logged by the move) leaves the folder, known by its name on
	// the server, and the Message-ID to find the mail in it at its hour.
	var after movedRow
	if err := h.db.Emails().FindOne(ctx, bson.M{"userId": userEmail, "folder": inbox, "messageId": messageID}).Decode(&after); err != nil {
		log.Printf("snooze: message %s of %s moved, where it landed not read back: %v", messageID, userEmail, err)
	}
	folder, uid := after.MovedTo.Folder, after.MovedTo.UID
	if folder == "" {
		op, err := mailbox.IMAPOpFor(into)
		if err == nil {
			folder, err = session.imapc.Destination(op)
		}
		if err != nil {
			log.Printf("snooze: message %s of %s moved, the folder it went to not known: %v", messageID, userEmail, err)
		}
		uid = ""
	}

	// Out of the inbox, like a mail tidied away: a pending AI proposal about
	// it would otherwise outlive it, and come back beside a second one once
	// the mail is back under its new number.
	h.markProposalsMoot(ctx, userEmail, []string{messageID})

	if _, err := h.db.Snoozes().UpdateOne(ctx,
		bson.M{"userId": userEmail, "messageId": messageID, "status": snooze.StatusScheduled},
		bson.M{
			"$set": bson.M{
				"from": before.From, "subject": before.Subject,
				"wakeAt": wakeAt, "status": snooze.StatusScheduled, "updatedAt": now,
				"folder": folder, "folderUid": uid, "headerMessageId": before.HeaderMessageID,
			},
			"$setOnInsert": bson.M{
				"userId": userEmail, "messageId": messageID, "createdAt": now,
			},
		},
		options.Update().SetUpsert(true),
	); err != nil {
		// As over the Gmail API: the mail is out of the inbox, and without a
		// scheduled row nothing would bring it back.
		return fmt.Errorf("le report n'a pas pu être enregistré: %w", err)
	}

	h.logActionMeta(ctx, userEmail, messageID, "archive", SourceSnooze, before.Subject, before.From)
	return nil
}

// BatchSnooze reports a whole selection to the same wake time in one request.
//
// Snoozing was a one-message-at-a-time affordance reachable only from the
// reader, which made it useless against the case it is best at: the twenty
// newsletters you want out of the way until Saturday. Everything else about it
// is unchanged, protected senders included: a bulk snooze takes mail out of the
// inbox, which is exactly what the VIP list is there to veto. On both
// transports, through one session.
func (h *Handler) BatchSnooze(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	var req models.BatchSnoozeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.MessageIDs) == 0 {
		writeError(w, http.StatusBadRequest, "Aucun email sélectionné")
		return
	}
	if len(req.MessageIDs) > maxBatchActionSize {
		writeError(w, http.StatusBadRequest, "Trop d'emails sélectionnés en une fois")
		return
	}

	now := time.Now()
	wakeAt, wErr := resolveWakeAt(req.Preset, req.WakeAt, now)
	if wErr != nil {
		writeError(w, http.StatusBadRequest, wErr.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	session, err := h.openSession(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	defer session.Close()

	// One lookup for the whole batch, exactly as BatchAction does: the shield
	// needs each sender, and the snoozed list needs the subject to be readable.
	identities := h.emailIdentities(ctx, userEmail, req.MessageIDs)
	protectedList := h.protectedValues(ctx, userEmail)

	var snoozeOne func(meta models.Email) error
	if session.Transport == provider.TransportIMAP {
		// The stored copy is the only identity there is: the listing stored
		// it, and there is no metadata call to make (see identityIn).
		snoozeOne = func(meta models.Email) error {
			return h.snoozeIMAP(ctx, session, userEmail, meta.MessageID, wakeAt, now)
		}
	} else {
		labelID, err := h.ensureLabel(ctx, session.gmail, userEmail, snoozeLabelName)
		if err != nil {
			writeError(w, http.StatusBadGateway, "Impossible de préparer le report")
			return
		}
		if len(protectedList) > 0 || len(identities) < len(req.MessageIDs) {
			h.fillMissingIdentities(ctx, session.gmail, req.MessageIDs, identities)
		}
		snoozeOne = func(meta models.Email) error {
			return h.snoozeMessage(ctx, session.gmail, userEmail, labelID, meta, wakeAt, now)
		}
	}

	res := batchSnoozeResult{Total: len(req.MessageIDs), WakeAt: wakeAt}
	res.Snoozed = make([]string, 0, len(req.MessageIDs))

	for _, id := range req.MessageIDs {
		if id == "" || ctx.Err() != nil {
			res.Failed++
			continue
		}
		meta := identities[id]
		meta.MessageID = id
		// Fail safe, like the batch actions: a sender we could not establish is
		// treated as possibly protected rather than assumed harmless.
		if len(protectedList) > 0 && (meta.From == "" || !allows(protect.ActionArchive, meta.From, protectedList)) {
			res.Protected++
			continue
		}
		err := snoozeOne(meta)
		if errors.Is(err, errAlreadyOut) {
			// Already out of the inbox: neither reported nor failed.
			continue
		}
		if err != nil {
			log.Printf("snooze: batch failed for %s: %v", id, err)
			res.Failed++
			continue
		}
		res.Snoozed = append(res.Snoozed, id)
	}

	writeJSON(w, http.StatusOK, res)
}

// batchSnoozeResult mirrors batchActionResult: the client has to be able to tell
// "done" from "shielded by your VIP list" from "failed".
type batchSnoozeResult struct {
	Snoozed   []string  `json:"snoozed"`
	Failed    int       `json:"failed"`
	Protected int       `json:"protectedSkipped"`
	Total     int       `json:"total"`
	WakeAt    time.Time `json:"wakeAt"`
}

// GetSnoozes lists the caller's snoozes (scheduled by default), soonest first.
func (h *Handler) GetSnoozes(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	status := r.URL.Query().Get("status")
	if status == "" {
		status = snooze.StatusScheduled
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	cursor, err := h.db.Snoozes().Find(ctx,
		bson.M{"userId": userEmail, "status": status},
		options.Find().SetSort(bson.M{"wakeAt": 1}).SetLimit(200))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load snoozes")
		return
	}
	defer cursor.Close(ctx)

	rows := make([]models.Snooze, 0)
	if err := cursor.All(ctx, &rows); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to decode snoozes")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"snoozes": rows})
}

// WakeSnooze brings a snoozed email back to the inbox immediately (the user
// changed their mind), marking it unread so it is not missed.
func (h *Handler) WakeSnooze(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	oid, err := primitive.ObjectIDFromHex(mux.Vars(r)["id"])
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid id")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	var s models.Snooze
	if err := h.db.Snoozes().FindOne(ctx, bson.M{"_id": oid, "userId": userEmail}).Decode(&s); err != nil {
		writeError(w, http.StatusNotFound, "Snooze introuvable")
		return
	}

	session, err := h.openSession(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	defer session.Close()

	var wakeErr error
	if session.Transport == provider.TransportIMAP {
		wakeErr = h.wakeIMAP(ctx, session, s, true)
	} else {
		wakeErr = h.wakeGmail(ctx, session, s)
	}
	switch {
	case wakeErr == nil, errors.Is(wakeErr, errAlreadyBack):
		// Back already (the sweep, a moment ago): nothing more moved or written.
		writeJSON(w, http.StatusOK, map[string]string{"status": "woken"})
	case errors.Is(wakeErr, errNotWhereLeft):
		// Cancelled: over IMAP taken out of "Plus tard" by another mail
		// program, or brought back by "Annuler". Nothing to bring back.
		writeError(w, http.StatusNotFound, notWhereLeftMessage)
	case errors.Is(wakeErr, errWakeBusy):
		writeError(w, http.StatusLocked, wakeBusyMessage)
	case errors.Is(wakeErr, imap.ErrAmbiguous):
		// Several copies of it in "Plus tard": which one to bring back
		// cannot be told. Nothing moved; the report stays scheduled.
		writeSeveralCopiesAs(w, severalCopiesWakeMessage)
	default:
		writeError(w, http.StatusBadGateway, "Réactivation impossible : "+wakeErr.Error())
	}
}

// wakeGmail is "Réactiver" over the Gmail API: back to the inbox, unread, the
// snooze label stripped (restoreSnoozed). Claimed like every return
// (claimReport), so a press at the very moment the sweep wakes the same mail
// neither moves it twice nor writes its return twice in the history. A report
// given up (failed) may be retried this way, and a failure is the user's to
// retry: not counted.
func (h *Handler) wakeGmail(ctx context.Context, session *mailSession, s models.Snooze) error {
	claim, err := h.claimReport(ctx, s, []string{snooze.StatusScheduled, snooze.StatusFailed})
	if err != nil {
		return err
	}
	restoreErr := h.restoreSnoozed(ctx, session.gmail, s.UserID, s.MessageID)
	outcome := snooze.Woken
	if restoreErr != nil {
		outcome = snooze.Untried
	}
	sctx, cancel := settleCtx(ctx)
	defer cancel()
	h.settleWake(sctx, s, claim, outcome)
	return restoreErr
}

// restoreSnoozed returns a message to the inbox, marks it unread and strips the
// snooze label.
func (h *Handler) restoreSnoozed(ctx context.Context, gmailClient *gmailapi.Service, userEmail, messageID string) error {
	muts := []mailbox.Mutation{
		{Action: mailbox.ActionUnarchive},
		{Action: mailbox.ActionMarkUnread},
	}
	// Stripping the snooze label is best effort: if the label cannot be
	// resolved the message still has to come back, which is the part the user
	// is waiting for.
	if labelID, err := h.ensureLabel(ctx, gmailClient, userEmail, snoozeLabelName); err == nil {
		muts = append(muts, mailbox.Mutation{Action: mailbox.ActionUnlabel, LabelID: labelID})
	}
	return h.applyMutations(ctx, h.mailboxOf(gmailClient), mailbox.OnAccount(messageID), muts...)
}

// errWakeBusy is a report someone else is bringing back at that very moment:
// "Réactiver" or "Annuler" while the sweep holds it, or the other way round.
// Nothing more happened; the user may try again.
var errWakeBusy = errors.New("api: this report is being brought back already")

// wakeBusyMessage is what the user reads for errWakeBusy.
const wakeBusyMessage = "Ce mail est déjà en train de revenir dans votre boîte, réessayez dans un instant."

// claimReport claims report s, in one of statuses, before its mail is moved
// back (wakingAt), and returns the filter that writes on it while the claim
// holds. The sweep, "Réactiver" and "Annuler" all claim first, so that of two
// of them reaching the same report at once only one moves the mail: over
// IMAP the other would find it gone from "Plus tard" and cancel a report that
// did come back, and either would write a second return. A claim left by a
// crash expires after moveInFlight.
//
// When the claim does not land: errAlreadyBack for a report already back
// (done), errNotWhereLeft for one cancelled, errWakeBusy while another holds
// it.
func (h *Handler) claimReport(ctx context.Context, s models.Snooze, statuses []string) (bson.M, error) {
	oid, err := primitive.ObjectIDFromHex(s.ID)
	if err != nil {
		return nil, fmt.Errorf("snooze id %q: %w", s.ID, err)
	}
	claimedAt := time.Now()
	res, err := h.db.Snoozes().UpdateOne(ctx, bson.M{
		"_id": oid, "userId": s.UserID, "status": bson.M{"$in": statuses},
		"wakingAt": bson.M{"$not": bson.M{"$gte": claimedAt.Add(-moveInFlight)}},
	}, bson.M{"$set": bson.M{"wakingAt": claimedAt}})
	if err != nil {
		return nil, fmt.Errorf("claim report %s for its return: %w", s.ID, err)
	}
	if res.MatchedCount == 0 {
		var now models.Snooze
		if err := h.db.Snoozes().FindOne(ctx, bson.M{"_id": oid}).Decode(&now); err != nil {
			return nil, fmt.Errorf("read report %s again: %w", s.ID, err)
		}
		switch now.Status {
		case snooze.StatusDone:
			return nil, errAlreadyBack
		case snooze.StatusCancelled:
			return nil, errNotWhereLeft
		}
		return nil, errWakeBusy
	}
	return bson.M{"_id": oid, "wakingAt": claimedAt}, nil
}

// settleCtx is the time a claimed report is settled under: its own, so a
// move that happened is written even when the caller ran out of time just
// after.
func settleCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
}

// wakeIMAP brings one reported mail of an IMAP mailbox back to the inbox,
// unread (mirroredIMAP.bringBack), and writes what came of it on the report.
// byHand is "Réactiver": a report given up (failed) may be retried that way,
// and a failure there is the user's to retry, so it is not counted.
//
// The report is claimed first (claimReport). Whatever the outcome, the claim
// is released; nothing is counted twice: one ledger entry for a return, one
// attempt for a failure.
//
// It answers nil when the mail is back, errNotWhereLeft when it was no longer
// in "Plus tard" (the report is cancelled), errAlreadyBack when another wake
// brought it back, errWakeBusy while another holds it, or the failure.
func (h *Handler) wakeIMAP(ctx context.Context, session *mailSession, s models.Snooze, byHand bool) error {
	statuses := []string{snooze.StatusScheduled}
	if byHand {
		statuses = append(statuses, snooze.StatusFailed)
	}
	claim, err := h.claimReport(ctx, s, statuses)
	if err != nil {
		return err
	}

	mb := mirroredIMAP{client: session.imapc, h: h, userEmail: s.UserID}
	wakeErr := mb.bringBack(ctx, s.MessageID, s.Folder, s.FolderUID, s.HeaderMessageID)
	outcome := snooze.Woken
	switch {
	case wakeErr == nil:
	case errors.Is(wakeErr, errNotWhereLeft):
		outcome = snooze.Gone
		log.Printf("snooze: report %s of %s cancelled: message %s is no longer in %q, another mail program took it out", s.ID, s.UserID, s.MessageID, s.Folder)
	case errors.Is(wakeErr, imap.ErrAmbiguous):
		// Several copies of it where it waited: which one to bring back
		// cannot be told, now or at the next pass. Not counted, so the
		// report stays scheduled, on the Reporté screen, where "Réactiver"
		// says why, rather than being given up or cancelled out of sight
		// while the mail waits in "Plus tard".
		outcome = snooze.Untried
	case byHand || ctx.Err() != nil || session.imapc.Closed():
		// Cut short by the time bound or by a connection that went away
		// (nothing to say about the mail itself), or the user's to try
		// again.
		outcome = snooze.Untried
	default:
		outcome = snooze.Failed
	}
	switch {
	case outcome == snooze.Failed:
		log.Printf("snooze: failed to restore %s for %s (attempt %d): %v", s.MessageID, s.UserID, s.Attempts+1, wakeErr)
	case wakeErr != nil && outcome == snooze.Untried:
		log.Printf("snooze: did not restore %s for %s (not counted, tried again later): %v", s.MessageID, s.UserID, wakeErr)
	}
	sctx, cancel := settleCtx(ctx)
	defer cancel()
	h.settleWake(sctx, s, claim, outcome)
	return wakeErr
}

// endReportOnReturn runs back, an "Annuler" that brings messageID back to the
// inbox, and ends the report of that mail when it has one scheduled: the user
// undid it, so it leaves the scheduled list at once (cancelled) instead of
// waiting there for an hour that would move nothing, or, over the Gmail API,
// mark unread a mail already back. The report is claimed before the move
// (claimReport): while the sweep holds it, nothing moves and errWakeBusy says
// so; a move that fails leaves it scheduled. No return is journaled for the
// report: the undo writes its own line.
func (h *Handler) endReportOnReturn(ctx context.Context, userEmail, messageID string, back func() error) error {
	var s models.Snooze
	err := h.db.Snoozes().FindOne(ctx, bson.M{"userId": userEmail, "messageId": messageID, "status": snooze.StatusScheduled}).Decode(&s)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return back()
	}
	if err != nil {
		return fmt.Errorf("look for the report of message %s: %w", messageID, err)
	}
	claim, err := h.claimReport(ctx, s, []string{snooze.StatusScheduled})
	if errors.Is(err, errAlreadyBack) || errors.Is(err, errNotWhereLeft) {
		// Ended a moment ago: nothing left to end, the undo goes on alone.
		return back()
	}
	if err != nil {
		return err
	}
	backErr := back()
	outcome := snooze.Undone
	if backErr != nil {
		outcome = snooze.Untried
	}
	sctx, cancel := settleCtx(ctx)
	defer cancel()
	h.settleWake(sctx, s, claim, outcome)
	if backErr == nil {
		log.Printf("snooze: report %s of %s cancelled: \"Annuler\" brought message %s back", s.ID, userEmail, messageID)
	}
	return backErr
}

// returnsToInbox says whether verb, on this transport, brings a mail back to
// the inbox: what "Annuler" sends after a move out. Over the Gmail API a label
// taken off leaves the mail where it is; over IMAP it brings it back from the
// folder that stood for the label.
func returnsToInbox(verb string, transport provider.Transport) bool {
	action, err := mailbox.Parse(verb)
	if err != nil {
		return false
	}
	switch action {
	case mailbox.ActionUnarchive, mailbox.ActionUntrash:
		return true
	case mailbox.ActionUnlabel:
		return transport == provider.TransportIMAP
	}
	return false
}

// settleWake writes on report s what an attempt at bringing it back came to
// (snooze.Settle), over filter, and journals a return. The claim
// (claimReport) is released whatever happened.
func (h *Handler) settleWake(ctx context.Context, s models.Snooze, filter bson.M, outcome snooze.Outcome) {
	status, counted := snooze.Settle(outcome, s.Attempts, maxSnoozeWakeAttempts)
	update := bson.M{"$unset": bson.M{"wakingAt": ""}}
	if status != "" {
		update["$set"] = bson.M{"status": status, "updatedAt": time.Now()}
	}
	if counted {
		update["$inc"] = bson.M{"attempts": 1}
	}
	if _, err := h.db.Snoozes().UpdateOne(ctx, filter, update); err != nil {
		log.Printf("snooze: report %s of %s not updated (%v): %v", s.ID, s.UserID, outcome, err)
	}
	if outcome == snooze.Woken {
		h.logActionMeta(ctx, s.UserID, s.MessageID, "unarchive", SourceSnooze, s.Subject, s.From)
	}
}

// startSnoozeLoop launches the background sweeper that resurfaces due snoozes.
func (h *Handler) startSnoozeLoop() {
	go func() {
		ticker := time.NewTicker(snoozeSweepInterval)
		defer ticker.Stop()
		for range ticker.C {
			h.wakeDueSnoozes()
		}
	}()
}

// wakeDueSnoozes brings back every snooze whose wake time has passed. It is
// best-effort and resilient: a per-message failure is logged and skipped so
// one bad message never stalls the rest. The due reports are worked through
// account by account (snooze.ByAccount), each account through one session:
// one Gmail client, or one IMAP connection for all its due mails rather than
// one per mail.
func (h *Handler) wakeDueSnoozes() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cursor, err := h.db.Snoozes().Find(ctx,
		bson.M{"status": snooze.StatusScheduled, "wakeAt": bson.M{"$lte": time.Now()}},
		options.Find().SetLimit(200))
	if err != nil {
		return
	}
	defer cursor.Close(ctx)

	var due []models.Snooze
	if err := cursor.All(ctx, &due); err != nil {
		return
	}

	accounts := make([]string, len(due))
	for i, s := range due {
		accounts[i] = s.UserID
	}
	for _, group := range snooze.ByAccount(accounts) {
		if ctx.Err() != nil {
			// Out of time: the rest waits for the next pass, untouched.
			return
		}
		batch := make([]models.Snooze, 0, len(group))
		for _, i := range group {
			batch = append(batch, due[i])
		}
		h.wakeAccount(ctx, batch)
	}
}

// wakeAccount brings back the due reports of one account, through one session.
func (h *Handler) wakeAccount(ctx context.Context, due []models.Snooze) {
	userEmail := due[0].UserID
	// The bound starts before the session opens: an IMAP server that greets
	// and then says nothing at LOGIN must not hold the sweep either.
	bounded, cancel := context.WithTimeout(ctx, snoozeAccountBound)
	defer cancel()
	session, err := h.openSession(bounded, userEmail)
	if err != nil {
		log.Printf("snooze: cannot open the mailbox of %s: %v", userEmail, err)
		if errors.Is(err, imap.ErrAuth) {
			// A password the server refuses stays refused until the user
			// connects the mailbox again: counted, so the reports end up
			// given up (and shown as such) rather than a refused login every
			// minute, which a provider may answer by blocking the account.
			// Anything else (the server out of reach) is tried again at the
			// next pass, uncounted.
			for _, s := range due {
				oid, _ := primitive.ObjectIDFromHex(s.ID)
				// Not one another request holds (claimReport).
				h.settleWake(ctx, s, bson.M{"_id": oid, "status": snooze.StatusScheduled,
					"wakingAt": bson.M{"$not": bson.M{"$gte": time.Now().Add(-moveInFlight)}}}, snooze.Failed)
			}
		}
		return
	}
	defer session.Close()

	if session.Transport == provider.TransportIMAP {
		// The session is bound to bounded (imap.Connect): past it, the
		// connection is dropped and the command under way fails.
		for _, s := range due {
			if bounded.Err() != nil || session.imapc.Closed() {
				// Out of time, or the connection is gone (the server
				// dropped it): the rest waits for the next pass, untouched
				// and uncounted.
				return
			}
			_ = h.wakeIMAP(bounded, session, s, false)
		}
		return
	}

	for _, s := range due {
		// Claimed (claimReport): a "Réactiver" or an "Annuler" holding it
		// moves it itself.
		claim, err := h.claimReport(ctx, s, []string{snooze.StatusScheduled})
		if err != nil {
			continue
		}
		if err := h.restoreSnoozed(ctx, session.gmail, s.UserID, s.MessageID); err != nil {
			log.Printf("snooze: failed to restore %s for %s (attempt %d): %v", s.MessageID, s.UserID, s.Attempts+1, err)
			// Cap retries: a permanently-failing wake (e.g. the message was hard
			// deleted) would otherwise be re-attempted on every 60s sweep forever.
			// After maxSnoozeWakeAttempts, park it as "failed" so it drops out of
			// the scheduled query.
			h.settleWake(ctx, s, claim, snooze.Failed)
			continue
		}
		h.settleWake(ctx, s, claim, snooze.Woken)
	}
}
