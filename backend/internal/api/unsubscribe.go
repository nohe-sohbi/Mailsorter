package api

import (
	"context"
	"crypto/rand"
	"errors"
	"log"
	"net/http"
	"regexp"
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

// Unsubscribe performs a one-click (or assisted) unsubscribe for the sender of a
// given message, over whichever transport the mailbox uses. When the sender
// advertises RFC 8058 one-click support the POST is fired server-side and the
// user never leaves the app; otherwise the https link or mailto: address is
// returned for the client to open. Over IMAP a sender that offers only an
// address is written to from here, through the mailbox's own sending server,
// so there is nothing left for the client to open. The action is recorded
// idempotently per sender and can optionally archive the backlog.
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

	session, err := h.openSession(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	defer session.Close()

	links, from, err := h.unsubscribeLinksOf(ctx, session, userEmail, req.MessageID)
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

	method := "browser"
	status := "opened"
	done := false

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
		if session.Transport == provider.TransportIMAP {
			if err := h.sendUnsubscribeMail(ctx, session.account, mailto); err == nil {
				// Sent: the client has nothing left to open, and opening the
				// person's mail program on it would write the request twice.
				status, done, mailto = "done", true, ""
			} else {
				// Not fatal either: the address goes back to the client, which
				// opens the person's own mail program on it.
				log.Printf("unsubscribe: the mail to %s for %s did not leave through the mailbox's sending server, handed to the screen: %v", senderAddr, userEmail, err)
			}
		}
	}

	if _, err := h.db.Unsubscribes().UpdateOne(ctx,
		bson.M{"userId": userEmail, "senderEmail": senderAddr},
		bson.M{
			"$set": bson.M{
				"method":     method,
				"status":     status,
				"senderName": senderName,
				"updatedAt":  time.Now(),
			},
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

	archived := 0
	if req.AlsoArchive {
		archived = h.archiveBySender(ctx, session, userEmail, senderAddr)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"done":     done,
		"method":   method,
		"url":      httpURL,
		"mailto":   mailto,
		"archived": archived,
		"sender":   senderAddr,
	})
}

// errMessageUnknown is a message the mailbox does not hold: on the Gmail API,
// Gmail does not return it; over IMAP, the stored inbox does not hold it.
var errMessageUnknown = errors.New("api: no such message")

// unsubscribeLinksOf reads the way out a message offers, and its sender.
//
// Over the Gmail API the headers are read from Gmail. Over IMAP they are read
// from the stored copy of the inbox, which the sync wrote them into
// (imap.Client.List reads both headers): the screen only shows what it holds,
// and reading them again costs a round trip for the same answer.
func (h *Handler) unsubscribeLinksOf(ctx context.Context, session *mailSession, userEmail, messageID string) (unsubscribe.Links, string, error) {
	if session.Transport == provider.TransportIMAP {
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

// sendUnsubscribeMail writes the mail a list's mailto: asks for (to its
// address, with its subject and body), from the mailbox's own address, the
// one subscribed, through the mailbox's sending server: the recap's way out.
// A mailto: this server will not write to (more than one address, an address
// that is not plain) is an error, and the client opens it instead.
func (h *Handler) sendUnsubscribeMail(ctx context.Context, account models.MailAccount, mailto string) error {
	request, err := unsubscribe.ParseMailto(mailto)
	if err != nil {
		return err
	}
	from := account.Username
	message := mailer.Build(mailer.Message{
		From:    from,
		To:      request.To,
		Subject: request.Subject,
		Text:    request.Body,
		Date:    time.Now(),
		ID:      mailer.MessageID(rand.Text(), from),
	})
	return h.sendThroughBox(ctx, account, request.To, message)
}

// archiveBySender archives every stored email sent from senderAddr, whatever
// display name each one carries, and returns how many were archived.
//
// The datastore query is only a narrowing: a case-insensitive substring of the
// address, which every mail of that sender contains. protect.SameAddress is what
// decides, because the same substring is also inside longer addresses (a prefix
// on the local part, a longer domain), other senders this sweep used to archive
// too.
//
// A protected sender is still unsubscribed (the caller has recorded it before
// this runs) but its mail stays in the inbox, as with every other sweep.
//
// Best-effort past the listing: the unsubscribe itself has already happened, so
// a mail that fails to move is logged and skipped rather than failing the call.
func (h *Handler) archiveBySender(ctx context.Context, session *mailSession, userEmail, senderAddr string) int {
	cursor, err := h.db.Emails().Find(ctx, bson.M{
		"userId":    userEmail,
		"leftInbox": notLeftInbox,
		"from":      bson.M{"$regex": regexp.QuoteMeta(senderAddr), "$options": "i"},
	})
	if err != nil {
		log.Printf("unsubscribe: listing the mail of %s for %s failed, nothing archived: %v", senderAddr, userEmail, err)
		return 0
	}
	defer cursor.Close(ctx)

	var emails []models.Email
	if err := cursor.All(ctx, &emails); err != nil {
		log.Printf("unsubscribe: reading the mail of %s for %s failed, nothing archived: %v", senderAddr, userEmail, err)
		return 0
	}

	protectedList := h.protectedValues(ctx, userEmail)
	mb := session.Mailbox()
	n := 0
	for _, e := range emails {
		if !protect.SameAddress(e.From, senderAddr) {
			continue
		}
		if !allows(protect.ActionArchive, e.From, protectedList) {
			continue
		}
		err := h.applyVerb(ctx, mb, session.RefFor(ctx, e.MessageID), "archive", "")
		if errors.Is(err, errAlreadyOut) {
			// Already out of the inbox: nothing moved, nothing to count.
			continue
		}
		if err != nil {
			log.Printf("unsubscribe: archiving %s for %s failed: %v", e.MessageID, userEmail, err)
			continue
		}
		n++
		h.logActionMeta(ctx, userEmail, e.MessageID, "archive", SourceUnsubscribe, e.Subject, e.From)
	}
	return n
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
