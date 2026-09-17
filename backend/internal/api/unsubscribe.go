package api

import (
	"context"
	"log"
	"net/http"
	"regexp"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/gmail"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
	gmailapi "google.golang.org/api/gmail/v1"
)

// Unsubscribe performs a one-click (or assisted) unsubscribe for the sender of a
// given message. When the sender advertises RFC 8058 one-click support the POST
// is fired server-side and the user never leaves the app; otherwise the https
// link or mailto: address is returned for the client to open. The action is
// recorded idempotently per sender and can optionally archive the backlog.
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
	if req.MessageID == "" {
		writeError(w, http.StatusBadRequest, "Message ID required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	token, err := h.getUserToken(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	gmailClient := h.gmailService.GetClient(token)

	msg, err := h.gmailService.GetMessage(gmailClient, req.MessageID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Email introuvable")
		return
	}

	httpURL, mailto, oneClick := gmail.ParseUnsubscribe(msg)
	from, _, _, _ := gmail.ParseEmailHeaders(msg)
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
	case oneClick:
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
	}

	h.db.Unsubscribes().UpdateOne(ctx,
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
	)

	archived := 0
	if req.AlsoArchive {
		archived = h.archiveBySender(ctx, gmailClient, userEmail, senderAddr)
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

// archiveBySender removes INBOX from every stored email of a sender and returns
// how many were archived. Best-effort: individual failures are skipped.
func (h *Handler) archiveBySender(ctx context.Context, gmailClient *gmailapi.Service, userEmail, senderAddr string) int {
	cursor, err := h.db.Emails().Find(ctx, bson.M{
		"userId": userEmail,
		"from":   bson.M{"$regex": regexp.QuoteMeta(senderAddr), "$options": "i"},
	})
	if err != nil {
		return 0
	}
	defer cursor.Close(ctx)

	var emails []models.Email
	if err := cursor.All(ctx, &emails); err != nil {
		return 0
	}

	n := 0
	for _, e := range emails {
		if err := h.applyVerb(ctx, gmailClient, e.MessageID, "archive", ""); err == nil {
			n++
			h.logActionMeta(ctx, userEmail, e.MessageID, "archive", SourceUnsubscribe, e.Subject, e.From)
		}
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
			"userId": userEmail,
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
