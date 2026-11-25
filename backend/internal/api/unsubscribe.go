package api

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/gmail"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailer"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/protect"
	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
	"github.com/nohe-sohbi/mailsorter/backend/internal/unsubscribe"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// unsubscribeSendBudget is how long the unsubscribe mail has to leave through
// the mailbox's sending server, on a clock of its own, started once the
// archive is done: a sending server that does not answer must not leave "et
// archiver ses mails" with no time. A var so that a test need not wait for it.
var unsubscribeSendBudget = 20 * time.Second

// unsubscribeResendWindow is how long, after Mailsorter wrote to an
// unsubscribe address for a person, it refuses to write to it again for
// them: a list takes days to process a request, and a second click must not
// become a second mail, or a hundred.
const unsubscribeResendWindow = 24 * time.Hour

// Unsubscribe performs a one-click (or assisted) unsubscribe for the sender of a
// given message, over whichever transport the mailbox uses. When the sender
// advertises RFC 8058 one-click support the POST is fired server-side and the
// user never leaves the app; otherwise the https link or mailto: address is
// returned for the client to open. Over IMAP, a sender that offers only an
// address on its own domain is written to from here, through the mailbox's own
// sending server, so there is nothing left for the client to open
// (sendUnsubscribeMail says what is written and when). The action is recorded
// idempotently per sender and can optionally archive the backlog, which is
// done first, on the request's time: the sending has a budget of its own.
// The answer says what was not done (message).
func (h *Handler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	var req models.UnsubscribeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.MessageID == "" || len(req.MessageID) > 256 {
		writeError(w, http.StatusBadRequest, "Message ID required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	transport, account, err := h.transportFor(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	// A session holds a connection: over IMAP it is opened only to archive,
	// the headers being in the stored inbox. Over the Gmail API it reads the
	// message.
	var session *mailSession
	if transport != provider.TransportIMAP || req.AlsoArchive {
		session, err = h.openSession(ctx, userEmail)
		if err != nil {
			writeAuthError(w, err)
			return
		}
		defer session.Close()
	}

	links, from, err := h.unsubscribeLinksOf(ctx, transport, session, userEmail, req.MessageID)
	if errors.Is(err, errMessageUnknown) {
		writeError(w, http.StatusNotFound, "Email introuvable")
		return
	}
	if err != nil {
		log.Printf("unsubscribe: read message %s of %s: %v", req.MessageID, userEmail, err)
		writeError(w, http.StatusInternalServerError, "Lecture de l'email impossible, réessayez.")
		return
	}
	httpURL, mailto := links.URL, links.Mailto
	senderAddr := extractSenderAddress(from)
	senderName := extractSenderName(from)

	if httpURL == "" && mailto == "" {
		writeError(w, http.StatusUnprocessableEntity, "Cet expéditeur ne propose pas de lien de désabonnement.")
		return
	}

	// What was not done, in the words the screen shows.
	var notDone []string

	archived, notArchived := 0, 0
	switch {
	case req.AlsoArchive && senderAddr == "":
		// A From header that does not read as one address names nobody:
		// nothing is swept in its name.
		notDone = append(notDone, unreadableSenderMessage)
	case req.AlsoArchive:
		// A protected list that cannot be read sweeps nothing, and the
		// unsubscribe waits with it: one retry does both.
		shield, err := h.protectedValues(ctx, userEmail)
		if err != nil {
			writeProtectedUnreadable(w, userEmail, err)
			return
		}
		archived, notArchived = h.archiveBySender(ctx, session, userEmail, senderAddr, shield)
		if notArchived > 0 {
			notDone = append(notDone, notArchivedMessage(notArchived))
		}
	}

	method := "browser"
	status := "opened"
	done := false
	sentTo := ""
	var sentAt time.Time
	alreadySent := false

	switch {
	case links.OneClick:
		if err := h.gmailService.OneClickUnsubscribe(httpURL); err == nil {
			method, status, done = "one-click", "done", true
		} else {
			// Not fatal: we fall through and hand the link to the client, who
			// finishes in their own browser. But it is logged, because the URL
			// came out of an email header and a refusal here is either a broken
			// sender or a List-Unsubscribe aimed at this server's own network.
			// Silently retrying by hand would hide the second one entirely.
			log.Printf("unsubscribe: one-click refused for %s: %v", senderAddr, err)
		}
	case httpURL == "" && mailto != "":
		method = "mailto"
		if transport != provider.TransportIMAP {
			break
		}
		sendCtx, cancelSend := context.WithTimeout(r.Context(), unsubscribeSendBudget)
		to, at, err := h.sendUnsubscribeMail(sendCtx, account, userEmail, from, mailto)
		cancelSend()
		var already *alreadySentError
		switch {
		case err == nil:
			// Sent: the client has nothing left to open, and opening the
			// person's mail program on it would write the request twice.
			status, done, mailto, sentTo, sentAt = "done", true, "", to, at
		case errors.As(err, &already):
			// Written already, a moment ago: nothing to open either.
			status, mailto, sentTo, alreadySent = "done", "", already.to, true
			notDone = append(notDone, fmt.Sprintf(
				"Une demande de désabonnement est déjà partie vers %s il y a moins de 24 h : la liste peut mettre quelques jours à la traiter.", already.to))
		case errors.Is(err, errNotWrittenForYou):
			// Not for Mailsorter to write without showing it: the client
			// opens it in the person's own mail program, as on Gmail.
		default:
			log.Printf("unsubscribe: the mail to the list of %s for %s did not leave through the mailbox's sending server, handed to the screen: %v", senderAddr, userEmail, err)
			notDone = append(notDone, "Le mail de désabonnement n'a pas pu partir de votre boîte : il s'ouvre dans votre messagerie pour que vous l'envoyiez.")
		}
	}

	// On its own clock: the archive may have used the request's.
	recordCtx, cancelRecord := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancelRecord()
	record := bson.M{
		"method":     method,
		"status":     status,
		"senderName": senderName,
		"updatedAt":  time.Now(),
	}
	if !sentAt.IsZero() {
		// Who was written to in the person's name, and when.
		record["sentTo"], record["sentAt"] = sentTo, sentAt
	}
	if senderAddr == "" {
		// Nobody to record as unsubscribed: the sender cannot be read.
		log.Printf("unsubscribe: the sender of message %s of %s cannot be read (%q), not recorded", req.MessageID, userEmail, from)
	} else if _, err := h.db.Unsubscribes().UpdateOne(recordCtx,
		bson.M{"userId": userEmail, "senderEmail": senderAddr},
		bson.M{
			"$set": record,
			"$setOnInsert": bson.M{
				"userId":      userEmail,
				"senderEmail": senderAddr,
				"createdAt":   time.Now(),
			},
		},
		options.Update().SetUpsert(true),
	); err != nil {
		log.Printf("unsubscribe: record the unsubscribe of %s from %s: %v", userEmail, senderAddr, err)
	}

	answer := map[string]interface{}{
		"done":     done,
		"method":   method,
		"url":      httpURL,
		"mailto":   mailto,
		"archived": archived,
		"sender":   senderAddr,
	}
	if sentTo != "" {
		answer["sentTo"] = sentTo
	}
	if alreadySent {
		answer["alreadySent"] = true
	}
	if notArchived > 0 {
		answer["notArchived"] = notArchived
	}
	if len(notDone) > 0 {
		answer["message"] = strings.Join(notDone, " ")
	}
	writeJSON(w, http.StatusOK, answer)
}

// unreadableSenderMessage is why "et archiver ses mails" archived nothing for
// a mail whose From header does not read as one address.
const unreadableSenderMessage = "L'adresse de l'expéditeur de ce mail ne se lit pas : ses mails ne sont pas archivés."

// notArchivedMessage says how many of a sender's mails the archive left.
func notArchivedMessage(n int) string {
	if n == 1 {
		return "1 email de cet expéditeur n'a pas pu être archivé : réessayez."
	}
	return fmt.Sprintf("%d emails de cet expéditeur n'ont pas pu être archivés : réessayez.", n)
}

// errMessageUnknown is a message the mailbox does not hold: on the Gmail API,
// Gmail does not return it; over IMAP, the stored inbox does not hold it.
var errMessageUnknown = errors.New("api: no such message")

// unsubscribeLinksOf reads the way out a message offers, and its sender.
//
// Over the Gmail API the headers are read from Gmail, through the session.
// Over IMAP they are read from the stored copy of the inbox, which the sync
// wrote them into (imap.Client.List reads both headers): the screen only
// shows what it holds, and reading them again costs a connection for the
// same answer.
func (h *Handler) unsubscribeLinksOf(ctx context.Context, transport provider.Transport, session *mailSession, userEmail, messageID string) (unsubscribe.Links, string, error) {
	if transport == provider.TransportIMAP {
		var stored models.Email
		err := h.db.Emails().FindOne(ctx, bson.M{
			"userId": userEmail, "folder": string(mailbox.FolderInbox), "messageId": messageID,
		}).Decode(&stored)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return unsubscribe.Links{}, "", errMessageUnknown
		}
		if err != nil {
			return unsubscribe.Links{}, "", err
		}
		return unsubscribe.Links{URL: stored.UnsubURL, Mailto: stored.UnsubMailto, OneClick: stored.UnsubOneClick}, stored.From, nil
	}
	msg, err := h.gmailService.GetMessage(session.gmail, messageID)
	if err != nil {
		log.Printf("unsubscribe: message %s of %s not read from Gmail: %v", messageID, userEmail, err)
		return unsubscribe.Links{}, "", errMessageUnknown
	}
	httpURL, mailto, oneClick := gmail.ParseUnsubscribe(msg)
	from, _, _, _ := gmail.ParseEmailHeaders(msg)
	return unsubscribe.Links{URL: httpURL, Mailto: mailto, OneClick: oneClick}, from, nil
}

// errNotWrittenForYou is a mailto: Mailsorter does not write on the person's
// behalf (unsubscribe.ForSender says why): the client opens it instead.
var errNotWrittenForYou = errors.New("api: this unsubscribe mail is not written for the person")

// alreadySentError is an unsubscribe mail Mailsorter wrote to the same
// address for the person less than unsubscribeResendWindow ago.
type alreadySentError struct{ to string }

func (e *alreadySentError) Error() string {
	return "api: an unsubscribe mail already left for " + e.to
}

// sendUnsubscribeMail writes the mail a list's mailto: asks for, from the
// mailbox's own address (the one subscribed), through the mailbox's sending
// server: the recap's way out. from is the From header of the mail that
// carries the mailto:. It returns the address it wrote to, and when.
//
// Nobody sees this mail before it leaves, from the person's real address,
// and the header that asks for it was written by whoever sent the mail. So it
// is written only to an address on the registrable domain of the sender's
// real address (never one in its display name), with a subject that is an
// unsubscribe command and a body that is always the same (unsubscribe.ForSender;
// errNotWrittenForYou otherwise), and only once per address in
// unsubscribeResendWindow (claimUnsubscribeMail; alreadySentError otherwise).
func (h *Handler) sendUnsubscribeMail(ctx context.Context, account models.MailAccount, userEmail, from, mailto string) (string, time.Time, error) {
	request, err := unsubscribe.ForSender(mailto, from)
	if err != nil {
		log.Printf("unsubscribe: the mailto of %q is not written for %s, handed to the screen: %v", from, userEmail, err)
		return "", time.Time{}, fmt.Errorf("%w: %w", errNotWrittenForYou, err)
	}
	// Written to as the list wrote it; counted in the window in the form
	// senders are compared in (protect.NormalizeAddress: the local part in
	// lowercase, the domain as IDNA reads it), since stop@café.fr and
	// STOP@xn--caf-dma.fr are one address.
	to := request.To
	key := protect.NormalizeAddress(to)
	if key == "" {
		return "", time.Time{}, fmt.Errorf("%w: %q does not read as one address", errNotWrittenForYou, to)
	}
	now := time.Now().Truncate(time.Millisecond)
	claimed, err := h.claimUnsubscribeMail(ctx, userEmail, key, now)
	if err != nil {
		return "", time.Time{}, err
	}
	if !claimed {
		return "", time.Time{}, &alreadySentError{to: to}
	}
	sender := account.Username
	message := mailer.Build(mailer.Message{
		From:    sender,
		To:      to,
		Subject: request.Subject,
		Text:    unsubscribe.Body,
		Date:    now,
		ID:      mailer.MessageID(rand.Text(), sender),
	})
	if err := h.sendThroughBox(ctx, account, to, message); err != nil {
		// Not sent: the window must not refuse the next try. On a clock of
		// its own, the send's may be spent.
		releaseCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, rErr := h.db.UnsubscribeMails().DeleteOne(releaseCtx,
			bson.M{"userId": userEmail, "address": key, "sentAt": now},
		); rErr != nil {
			log.Printf("unsubscribe: the mail to %s for %s did not leave, and the next try stays refused for 24 h: %v", to, userEmail, rErr)
		}
		return "", time.Time{}, err
	}
	return to, now, nil
}

// claimUnsubscribeMail reserves, at now, the unsubscribe mail to the address
// `to` for this person: one document per address in unsubscribe_mails,
// unique on (userId, address), whose sentAt is set only when it is older than
// unsubscribeResendWindow. false when a mail to that address left for them in
// that time, whatever the sender: two lists sharing one unsubscribe address,
// or two clicks at once, write once. A document from before the window is
// taken over; when there is none, the upsert creates it, and the unique index
// lets only one of two concurrent upserts do so.
func (h *Handler) claimUnsubscribeMail(ctx context.Context, userEmail, to string, now time.Time) (bool, error) {
	since := now.Add(-unsubscribeResendWindow)
	_, err := h.db.UnsubscribeMails().UpdateOne(ctx,
		bson.M{"userId": userEmail, "address": to, "sentAt": bson.M{"$not": bson.M{"$gte": since}}},
		bson.M{
			"$set":         bson.M{"sentAt": now},
			"$setOnInsert": bson.M{"createdAt": now},
		},
		options.Update().SetUpsert(true),
	)
	if mongo.IsDuplicateKeyError(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reserve the unsubscribe mail to %s: %w", to, err)
	}
	return true, nil
}

// archiveBySender archives every stored email sent from senderAddr, whatever
// display name each one carries, and returns how many were archived, and how
// many of his were not (a failure, or no time left).
//
// The datastore query is only a narrowing: the address's domain, which every
// mail of that sender holds as written (its local part may be quoted in the
// header and not in the address: "<x@y>"@evil.io), in each form it may be
// written in (protect.DomainForms). A mail of his that writes the domain yet
// another way (full-width letters, a capital ẞ) is not found and stays in the
// inbox: the narrowing may miss one of his, never reach someone else's. protect.SameAddress is what
// decides, because the same domain is also inside other senders' addresses (a
// prefix on the local part, a longer domain), whom this sweep used to archive
// too. A senderAddr with no local part or no domain reaches nothing.
//
// A protected sender is still unsubscribed but its mail stays in the inbox, as
// with every other sweep, and is not counted either way.
//
// Best-effort past the listing: the unsubscribe itself is not tied to it, so a
// mail that fails to move is logged and counted rather than failing the call.
func (h *Handler) archiveBySender(ctx context.Context, session *mailSession, userEmail, senderAddr string, protectedList protect.Shield) (archived, notArchived int) {
	at := strings.LastIndex(senderAddr, "@")
	if at <= 0 || at == len(senderAddr)-1 {
		return 0, 0
	}
	// Every form its domain may be written in: café.fr and xn--caf-dma.fr.
	forms := protect.DomainForms(senderAddr[at+1:])
	quoted := make([]string, 0, len(forms))
	for _, f := range forms {
		quoted = append(quoted, regexp.QuoteMeta(f))
	}
	cursor, err := h.db.Emails().Find(ctx, bson.M{
		"userId":    userEmail,
		"leftInbox": notLeftInbox,
		"from":      bson.M{"$regex": strings.Join(quoted, "|"), "$options": "i"},
	})
	if err != nil {
		log.Printf("unsubscribe: listing the mail of %s for %s failed, nothing archived: %v", senderAddr, userEmail, err)
		return 0, 0
	}
	defer cursor.Close(ctx)

	var emails []models.Email
	if err := cursor.All(ctx, &emails); err != nil {
		log.Printf("unsubscribe: reading the mail of %s for %s failed, nothing archived: %v", senderAddr, userEmail, err)
		return 0, 0
	}

	mb := session.Mailbox()
	for _, e := range emails {
		if !protect.SameAddress(e.From, senderAddr) {
			continue
		}
		if !allows(protect.ActionArchive, e.From, protectedList) {
			continue
		}
		// Past the deadline every move would fail, and its ledger write too:
		// what is left is counted as not archived, not tried.
		if ctx.Err() != nil {
			notArchived++
			continue
		}
		err := h.applyVerb(ctx, mb, session.RefFor(ctx, e.MessageID), "archive", "")
		if errors.Is(err, errAlreadyOut) {
			// Already out of the inbox: nothing moved, nothing to count.
			continue
		}
		if err != nil {
			log.Printf("unsubscribe: archiving %s for %s failed: %v", e.MessageID, userEmail, err)
			notArchived++
			continue
		}
		archived++
		h.logActionMeta(ctx, userEmail, e.MessageID, "archive", SourceUnsubscribe, e.Subject, e.From)
	}
	return archived, notArchived
}

// GetSubscriptions aggregates the mailing-list senders in the user's stored
// mailbox that advertise an unsubscribe link, ranked by volume, and flags those
// already unsubscribed. Powers the in-app subscriptions cleanup view.
func (h *Handler) GetSubscriptions(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	pipeline := []bson.M{
		{"$match": bson.M{
			"userId":    userEmail,
			"leftInbox": notLeftInbox,
			"$or": []bson.M{
				{"unsubUrl": bson.M{"$nin": []interface{}{nil, ""}}},
				{"unsubMailto": bson.M{"$nin": []interface{}{nil, ""}}},
			},
		}},
		{"$sort": bson.M{"receivedDate": -1}},
		{"$group": bson.M{
			"_id":          "$from",
			"emailCount":   bson.M{"$sum": 1},
			"lastReceived": bson.M{"$max": "$receivedDate"},
			"sampleMsgId":  bson.M{"$first": "$messageId"},
			"oneClick":     bson.M{"$max": "$unsubOneClick"},
		}},
		{"$sort": bson.M{"emailCount": -1}},
		{"$limit": 100},
	}

	cursor, err := h.db.Emails().Aggregate(ctx, pipeline)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to aggregate subscriptions")
		return
	}
	defer cursor.Close(ctx)

	var results []struct {
		ID           string    `bson:"_id"`
		EmailCount   int       `bson:"emailCount"`
		LastReceived time.Time `bson:"lastReceived"`
		SampleMsgID  string    `bson:"sampleMsgId"`
		OneClick     bool      `bson:"oneClick"`
	}
	if err := cursor.All(ctx, &results); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to decode subscriptions")
		return
	}

	// Build the set of already-unsubscribed senders for this user.
	done := map[string]bool{}
	if uc, err := h.db.Unsubscribes().Find(ctx, bson.M{"userId": userEmail}); err == nil {
		var records []models.Unsubscribe
		if uc.All(ctx, &records) == nil {
			for _, rec := range records {
				done[rec.SenderEmail] = true
			}
		}
	}

	subscriptions := make([]models.Subscription, 0, len(results))
	for _, res := range results {
		addr := extractSenderAddress(res.ID)
		subscriptions = append(subscriptions, models.Subscription{
			SenderEmail:     addr,
			SenderName:      extractSenderName(res.ID),
			EmailCount:      res.EmailCount,
			LastReceived:    res.LastReceived,
			SampleMessageID: res.SampleMsgID,
			OneClick:        res.OneClick,
			Unsubscribed:    done[addr],
		})
	}

	writeJSON(w, http.StatusOK, subscriptions)
}
