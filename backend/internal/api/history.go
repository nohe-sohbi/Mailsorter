package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/activity"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	gmailapi "google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
)

// actionLogView is one row of the user-facing action history. It enriches the
// raw ledger entry with whether the action can still be reversed (it has a clean
// inverse and has not already been undone), so the UI can decide where to show
// an "Annuler" button without re-deriving the rule.
//
// MailboxGone says the entry is of a mailbox no longer connected (see
// generation.go): final, its message number names, now, a mail of the one
// connected since, or of none.
type actionLogView struct {
	models.ActionLog
	Undoable    bool `json:"undoable"`
	MailboxGone bool `json:"mailboxGone,omitempty"`
}

// GetActionLog returns the caller's most recent ledger entries (newest first),
// optionally filtered by source. This turns the append-only audit trail (every
// archive, trash, rule firing, snooze, unsubscribe) into a transparent history
// the user can actually see and act on. Each entry is flagged with whether it is
// still reversible.
func (h *Handler) GetActionLog(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	// Bounded page size: default 50, capped at 200 so a crafted query can't ask
	// the server to stream the entire ledger.
	limit := int64(50)
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := parseInt64(v); err == nil && n > 0 {
			limit = n
			if limit > 200 {
				limit = 200
			}
		}
	}

	filter := bson.M{"userId": userEmail}
	if source := r.URL.Query().Get("source"); source != "" {
		filter["source"] = source
	}
	// Cursor pagination: "everything older than the last row I have" is a stable
	// next page even while new actions land at the top. Without it the history
	// stopped dead at the most recent 100 entries.
	//
	// The cursor is (createdAt, _id), not createdAt alone. BSON stores dates to
	// the millisecond and a bulk action writes its entries back to back with no
	// I/O between them, so a page boundary landing inside such a burst would drop
	// every sibling sharing that timestamp: silently, which is the worst way to
	// lose an audit trail. The _id tiebreaker also makes the sort total, so two
	// requests cannot order equal timestamps differently.
	if before := r.URL.Query().Get("before"); before != "" {
		if ts, oid, ok := parseLogCursor(before); ok {
			filter["$and"] = []bson.M{{
				"$or": []bson.M{
					{"createdAt": bson.M{"$lt": ts}},
					{"createdAt": ts, "_id": bson.M{"$lt": oid}},
				},
			}}
		}
	}
	// Free-text search over the message identity. Anchored to the caller's own
	// entries by the userId filter above, and the needle is quoted so a stray
	// regex metacharacter can't turn into a scan the user did not ask for.
	//
	// This matches the identity stored ON the entry. Every writer now records it,
	// so anything logged from here on is searchable; entries predating that still
	// render (resolveLogIdentities fills them in below) but cannot be matched,
	// because their subject only exists after the query has run.
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		needle := bson.M{"$regex": regexp.QuoteMeta(q), "$options": "i"}
		filter["$or"] = []bson.M{{"subject": needle}, {"from": needle}}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// bson.D, not bson.M: a compound sort needs a defined key order, and a map
	// has none.
	cursor, err := h.db.ActionLog().Find(ctx, filter,
		options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}}).SetLimit(limit))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load history")
		return
	}
	defer cursor.Close(ctx)

	var logs []models.ActionLog
	if err := cursor.All(ctx, &logs); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to decode history")
		return
	}

	// The mailbox connected now: "Annuler" only acts on its entries.
	transport, account, tErr := h.transportFor(ctx, userEmail)
	if errors.Is(tErr, errMailboxChanged) {
		writeAuthError(w, tErr)
		return
	}
	known := tErr == nil || errors.Is(tErr, errNoMailboxConnected)
	if tErr != nil && known {
		transport, account = "", models.MailAccount{}
	}
	if !known {
		log.Printf("history: which mailbox %s is on: %v", userEmail, tErr)
	}
	gen := generationOf(transport, account)

	h.resolveLogIdentities(ctx, userEmail, gen, logs)

	imapState := h.imapUndoState(ctx, userEmail, transport, gen, known, logs)
	views := make([]actionLogView, 0, len(logs))
	for _, l := range logs {
		inverse, reversible := activity.Inverse(l.Action)
		if imapState != nil {
			inverse, reversible = activity.InverseOnFolders(l.Action)
		}
		if imapState != nil && reversible {
			switch inverse {
			case "unarchive", "untrash", "unlabel":
				reversible = imapState.canBringBack[l.MessageID]
			default:
				// A flag change can only be reversed on a mail still in the
				// inbox: elsewhere its number names nothing.
				reversible = imapState.inInbox[l.MessageID]
			}
		}
		// An entry of another mailbox than the one connected now names a
		// message by a number the one connected now gives to another.
		current := ofCurrentGeneration(l, gen)
		views = append(views, actionLogView{ActionLog: l, Undoable: known && reversible && !l.Undone && current, MailboxGone: known && !current})
	}

	// A full page means there is probably more; hand back the cursor to ask for
	// it rather than making the client guess.
	var nextBefore string
	if int64(len(logs)) == limit && len(logs) > 0 {
		nextBefore = formatLogCursor(logs[len(logs)-1])
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"entries": views, "nextBefore": nextBefore})
}

// imapUndo is what the history needs to know, on an IMAP mailbox, to offer
// "Annuler" only where it can work.
type imapUndo struct {
	// canBringBack: messages Mailsorter moved out whose stored copy says
	// where they went (a number or a Message-ID to find them by), within
	// undoWindow. Past it the sync forgets the move, and a move made before
	// Mailsorter recorded destinations was never known.
	canBringBack map[string]bool
	// inInbox: messages still in the inbox, the only ones whose flags can be
	// changed back.
	inInbox map[string]bool
}

// imapUndoState answers for these entries, from the copy of the mailbox
// connected now (gen); nil off IMAP, where every reversible action stays
// reversible. When the store cannot answer (known false: which mailbox is
// connected could not be read), nothing is offered: offering "Annuler" on
// what cannot be undone is a promise broken on click, hiding it for one page
// load is not.
func (h *Handler) imapUndoState(ctx context.Context, userEmail string, transport provider.Transport, gen string, known bool, logs []models.ActionLog) *imapUndo {
	none := &imapUndo{canBringBack: map[string]bool{}, inInbox: map[string]bool{}}
	if !known {
		return none
	}
	if transport != provider.TransportIMAP {
		return nil
	}
	ids := make([]string, 0, len(logs))
	for _, l := range logs {
		if l.MessageID != "" {
			ids = append(ids, l.MessageID)
		}
	}
	if len(ids) == 0 {
		return none
	}
	filter := inboxOf(userEmail, gen)
	filter["messageId"] = bson.M{"$in": ids}
	cur, err := h.db.Emails().Find(ctx, filter,
		options.Find().SetProjection(bson.M{"messageId": 1, "headerMessageId": 1, "leftInbox": 1, "leftAt": 1, "movedTo": 1}))
	if err != nil {
		log.Printf("history: which actions of %s can be undone: %v", userEmail, err)
		return none
	}
	var rows []movedRow
	if err := cur.All(ctx, &rows); err != nil {
		log.Printf("history: which actions of %s can be undone: %v", userEmail, err)
		return none
	}
	since := time.Now().Add(-undoWindow)
	for _, r := range rows {
		switch {
		case !r.LeftInbox:
			none.inInbox[r.MessageID] = true
		case r.MovedTo.Folder != "" && (r.MovedTo.UID != "" || r.HeaderMessageID != "") && !r.LeftAt.Before(since):
			none.canBringBack[r.MessageID] = true
		}
	}
	return none
}

// formatLogCursor encodes the (createdAt, _id) pair the next page starts after.
// Opaque to the client, which only ever echoes it back.
func formatLogCursor(entry models.ActionLog) string {
	return entry.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + entry.ID
}

// parseLogCursor decodes what formatLogCursor produced. A cursor missing its id
// half is still accepted so links minted by the previous, timestamp-only scheme
// keep working; it simply falls back to the far end of that millisecond.
func parseLogCursor(raw string) (time.Time, primitive.ObjectID, bool) {
	stamp, id, _ := strings.Cut(raw, "|")
	ts, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return time.Time{}, primitive.NilObjectID, false
	}
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		// No usable id: treat every entry in that millisecond as already seen,
		// which is exactly what the old cursor did.
		return ts, primitive.NilObjectID, true
	}
	return ts, oid, true
}

// resolveLogIdentities fills in the subject/sender of ledger entries that were
// written without them (either by a caller that did not hold the message, or
// before the ledger carried an identity at all). It costs a single indexed
// lookup for the whole page, and leaves entries whose email is no longer cached
// untouched so the UI can fall back to a neutral label. Only the entries of
// the mailbox connected now (gen) are resolved, from its copy: another
// mailbox's number names, there, another mail.
func (h *Handler) resolveLogIdentities(ctx context.Context, userEmail, gen string, logs []models.ActionLog) {
	missing := make([]string, 0, len(logs))
	seen := make(map[string]bool, len(logs))
	for _, l := range logs {
		if l.Subject != "" || l.From != "" || l.MessageID == "" || seen[l.MessageID] || !ofCurrentGeneration(l, gen) {
			continue
		}
		seen[l.MessageID] = true
		missing = append(missing, l.MessageID)
	}
	if len(missing) == 0 {
		return
	}

	cursor, err := h.db.Emails().Find(ctx, bson.M{
		"userId":     userEmail,
		"generation": inGeneration(gen),
		"messageId":  bson.M{"$in": missing},
	}, options.Find().SetProjection(bson.M{"messageId": 1, "subject": 1, "from": 1}))
	if err != nil {
		return // best-effort: the history still renders without identities
	}
	defer cursor.Close(ctx)

	var found []models.Email
	if err := cursor.All(ctx, &found); err != nil {
		return
	}
	byID := make(map[string]models.Email, len(found))
	for _, e := range found {
		byID[e.MessageID] = e
	}
	for i := range logs {
		if logs[i].Subject != "" || logs[i].From != "" || !ofCurrentGeneration(logs[i], gen) {
			continue
		}
		if e, ok := byID[logs[i].MessageID]; ok {
			logs[i].Subject = e.Subject
			logs[i].From = e.From
		}
	}
}

// UndoActionRequest is the request body for POST /api/activity/undo.
type UndoActionRequest struct {
	ID string `json:"id"`
}

// UndoAction reverses a single recorded action: it looks up the ledger entry the
// caller owns, applies the inverse Gmail action (un-archive, un-trash, mark
// unread), marks the entry undone, and records the reversal in the ledger with
// the "undo" source. Only the stateful triage actions have an inverse; anything
// else is rejected. This gives the user a real safety net over everything
// Mailsorter's automation did on their behalf.
func (h *Handler) UndoAction(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	var req UndoActionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	oid, err := primitive.ObjectIDFromHex(req.ID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid entry id")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// The mailbox connected now; refused first for a screen loaded on
	// another (errMailboxChanged). None connected goes the Gmail way, whose
	// client lookup answers for it (writeAuthError).
	transport, account, tErr := h.transportFor(ctx, userEmail)
	if tErr != nil && !errors.Is(tErr, errNoMailboxConnected) {
		writeAuthError(w, tErr)
		return
	}

	var entry models.ActionLog
	if err := h.db.ActionLog().FindOne(ctx, bson.M{"_id": oid, "userId": userEmail}).Decode(&entry); err != nil {
		writeError(w, http.StatusNotFound, "Action introuvable")
		return
	}
	if !ofCurrentGeneration(entry, generationOf(transport, account)) {
		// Of a mailbox no longer connected: its message number names, now,
		// a mail of the one connected since, which it would touch.
		writeError(w, http.StatusGone, mailboxGoneMessage)
		return
	}
	if entry.Undone {
		// Undone a moment ago by another "Annuler" (two tabs, two clicks),
		// with nothing done to the mail since: that one did what this one
		// asks, so answer like it. Otherwise it is a screen showing an old
		// line: say it was already undone.
		same, err := h.sameUndo(ctx, userEmail, entry, time.Now())
		if err != nil {
			log.Printf("history: entry %s of %s already undone, the actions since not read: %v", oid.Hex(), userEmail, err)
			writeError(w, http.StatusInternalServerError, "Annulation impossible à vérifier, réessayez.")
			return
		}
		if same {
			inverse, _ := activity.Inverse(entry.Action)
			writeJSON(w, http.StatusOK, map[string]string{"status": "undone", "action": inverse})
			return
		}
		writeError(w, http.StatusConflict, "Action déjà annulée")
		return
	}
	onIMAP := tErr == nil && transport == provider.TransportIMAP
	inverse, ok := activity.Inverse(entry.Action)
	if onIMAP {
		// Filing into a folder is undone there like an archive.
		inverse, ok = activity.InverseOnFolders(entry.Action)
	}
	if !ok {
		writeError(w, http.StatusBadRequest, "Cette action n'est pas réversible")
		return
	}

	if onIMAP {
		// On IMAP the inverse goes through the session: a moved message is
		// fetched back from where Mailsorter put it (mirroredIMAP), by the
		// number it had in the inbox, which is what the entry recorded.
		session, err := h.openSession(ctx, userEmail)
		if err != nil {
			writeAuthError(w, err)
			return
		}
		defer session.Close()
		if !ofCurrentGeneration(entry, session.generation()) {
			// Another mailbox connected since the check above.
			writeError(w, http.StatusGone, mailboxGoneMessage)
			return
		}
		err = h.applyVerb(ctx, session.Mailbox(), session.RefFor(ctx, entry.MessageID), inverse, "")
		switch {
		case errors.Is(err, errNotWhereLeft):
			writeError(w, http.StatusNotFound, notWhereLeftMessage)
			return
		case errors.Is(err, errUndoExpired):
			writeError(w, http.StatusNotFound, undoExpiredMessage)
			return
		case errors.Is(err, errLeftInbox):
			writeError(w, http.StatusNotFound, leftInboxMessage)
			return
		case errors.Is(err, errUndoBusy):
			writeError(w, http.StatusLocked, undoBusyMessage)
			return
		case errors.Is(err, errAlreadyBack):
			// Another "Annuler" brought it back a moment ago, and marks the
			// entry: nothing more to do or to record.
			writeJSON(w, http.StatusOK, map[string]string{"status": "undone", "action": inverse})
			return
		case err != nil:
			writeError(w, http.StatusBadGateway, "Annulation impossible : "+err.Error())
			return
		}
	} else {
		gmailClient, err := h.gmailClientFor(ctx, userEmail)
		if err != nil {
			writeAuthError(w, err)
			return
		}
		if err := h.applyInverseAction(gmailClient, entry.MessageID, inverse); err != nil {
			if gmailNamesNoMail(err) {
				log.Printf("history: undo entry %s of %s, Gmail knows no message %q: %v", oid.Hex(), userEmail, entry.MessageID, err)
				writeError(w, http.StatusNotFound, undoNoMailMessage)
				return
			}
			writeError(w, http.StatusBadGateway, "Annulation impossible : "+err.Error())
			return
		}
	}

	if _, err := h.db.ActionLog().UpdateOne(ctx,
		bson.M{"_id": oid},
		bson.M{"$set": bson.M{"undone": true, "undoneAt": time.Now()}},
	); err != nil {
		log.Printf("history: entry %s of %s undone but not marked: %v", oid.Hex(), userEmail, err)
	}
	// Record the reversal itself so the audit trail stays truthful.
	h.logActionFiled(ctx, userEmail, entry.Generation, entry.MessageID, inverse, SourceUndo, entry.Subject, entry.From, entry.LabelName)

	writeJSON(w, http.StatusOK, map[string]string{"status": "undone", "action": inverse})
}

// mailboxGoneMessage is what the user reads for "Annuler" on an entry of a
// mailbox no longer connected (ofCurrentGeneration).
const mailboxGoneMessage = "Cette action concerne une boîte débranchée depuis."

// undoNoMailMessage is what the user reads for "Annuler" over the Gmail API on
// an entry whose message Gmail does not know (gmailNamesNoMail): deleted for
// good since, or an entry of an IMAP mailbox disconnected before entries
// carried their generation, whose number Gmail refuses as an id.
const undoNoMailMessage = "Ce mail n'est pas dans votre boîte Gmail : cette action ne peut pas être annulée d'ici."

// gmailNamesNoMail says whether the Gmail API refused a message id as naming
// no message: not found (404), or not an id at all (400, an IMAP number).
func gmailNamesNoMail(err error) bool {
	var apiErr *googleapi.Error
	return errors.As(err, &apiErr) && (apiErr.Code == http.StatusNotFound || apiErr.Code == http.StatusBadRequest)
}

// sameUndoWindow is how long after an "Annuler" another "Annuler" of the
// same entry may be the same click seen twice (two tabs, a double click, a
// request retried) and answered like it.
const sameUndoWindow = 2 * time.Minute

// recentUndo says whether an entry was undone recently enough, at now, for
// another "Annuler" of it to be the same one.
func recentUndo(undoneAt, now time.Time) bool {
	return !undoneAt.IsZero() && !now.Before(undoneAt) && now.Sub(undoneAt) < sameUndoWindow
}

// sameUndo says whether an "Annuler" of an entry already undone is the same
// one as the undo, to be answered like it: the undo is recent (recentUndo)
// and nothing was done to the mail since. Undone, then archived again: a
// screen left open on the old line is told the action was already undone,
// never that it undid it.
func (h *Handler) sameUndo(ctx context.Context, userEmail string, entry models.ActionLog, now time.Time) (bool, error) {
	if !recentUndo(entry.UndoneAt, now) {
		return false, nil
	}
	acted, err := h.actedOnSince(ctx, userEmail, entry.Generation, entry.MessageID, entry.UndoneAt)
	return !acted, err
}

// actedOnSince says whether the history holds an action on this mail since
// that time, other than a reversal ("Annuler" records its own). Over IMAP a
// mail "Annuler" brought back has a new number, which its stored row keeps
// alongside the old one (restoredFrom): an action under either counts. Within
// the mailbox of that generation (gen): another names other mails so.
func (h *Handler) actedOnSince(ctx context.Context, userEmail, gen, messageID string, since time.Time) (bool, error) {
	ids := []string{messageID}
	cur, err := h.db.Emails().Find(ctx, bson.M{"userId": userEmail, "generation": inGeneration(gen), "restoredFrom": messageID},
		options.Find().SetProjection(bson.M{"messageId": 1}))
	if err != nil {
		return false, fmt.Errorf("look for message %s put back: %w", messageID, err)
	}
	var rows []struct {
		MessageID string `bson:"messageId"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return false, fmt.Errorf("look for message %s put back: %w", messageID, err)
	}
	for _, r := range rows {
		ids = append(ids, r.MessageID)
	}
	n, err := h.db.ActionLog().CountDocuments(ctx, bson.M{
		"userId":     userEmail,
		"generation": inGeneration(gen),
		"messageId":  bson.M{"$in": ids},
		"createdAt":  bson.M{"$gt": since},
		"source":     bson.M{"$ne": SourceUndo},
	}, options.Count().SetLimit(1))
	if err != nil {
		return false, fmt.Errorf("count the actions on message %s since its undo: %w", messageID, err)
	}
	return n > 0, nil
}

// applyInverseAction maps an inverse action name onto the corresponding Gmail
// label mutation. It mirrors the undo branches of EmailAction so a reversal from
// the history behaves exactly like a manual one.
func (h *Handler) applyInverseAction(gmailClient *gmailapi.Service, messageID, inverse string) error {
	return h.applyInverseActionWithLabel(gmailClient, messageID, inverse, "")
}

// applyInverseActionWithLabel is applyInverseAction for the one reversal that
// takes an argument: removing a label needs its Gmail id, which the caller
// resolves once per batch rather than per message. An "unlabel" with no id is a
// no-op rather than a silent full-label wipe.
func (h *Handler) applyInverseActionWithLabel(gmailClient *gmailapi.Service, messageID, inverse, labelID string) error {
	// The inverse vocabulary comes from activity.Inverse, which only ever emits
	// verbs this understands. An unlabel with nothing to remove is the one case
	// worth naming: it means the ledger entry did not record which label was
	// applied, so there is nothing to undo.
	if inverse == "unlabel" && labelID == "" {
		return fmt.Errorf("aucun libellé à retirer")
	}
	err := h.applyVerb(context.Background(), h.mailboxOf(gmailClient), mailbox.OnAccount(messageID), inverse, labelID)
	if errors.Is(err, mailbox.ErrUnknownAction) {
		return nil
	}
	return err
}
