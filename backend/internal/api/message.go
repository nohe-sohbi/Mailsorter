package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/nohe-sohbi/mailsorter/backend/internal/gmail"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	gmailapi "google.golang.org/api/gmail/v1"
)

// messageView is one fully loaded message. It deliberately carries both body
// representations rather than a single pre-rendered blob: the client renders the
// HTML when there is one and falls back to the plain text otherwise, which is
// the difference between a readable email and a wall of collapsed markup.
type messageView struct {
	models.Email
	// Body (embedded) carries the plain-text representation; BodyHTML the rich
	// one when the sender provided it.
	BodyHTML    string           `json:"bodyHtml,omitempty"`
	Attachments []attachmentView `json:"attachments,omitempty"`
}

// attachmentView describes a file carried by the message. AttachmentID is the
// handle Gmail hands out for the bytes themselves (they never travel with the
// message payload), so it is what the download route needs; a part with no id
// carries its data inline and is not downloadable through that route.
type attachmentView struct {
	Filename     string `json:"filename"`
	MimeType     string `json:"mimeType"`
	Size         int64  `json:"size"`
	AttachmentID string `json:"attachmentId,omitempty"`
}

// GetEmail returns a single message with its decoded body.
//
// The list endpoint deliberately omits bodies: it already fetches 100 messages
// per page, and shipping 100 HTML payloads to render one would be wasteful. So
// the body is fetched on demand, when the reader actually opens a message,
// which, before this route existed, it never could, leaving the reader stuck on
// "Contenu complet indisponible" for every email.
//
// With ?markRead=1 the message is also marked read in Gmail, mirroring what
// opening an email means everywhere else. That is user intent rather than
// automation, so it is not written to the action ledger: the history exists to
// show what Mailsorter did on the user's behalf, and would drown in "Lu"
// entries otherwise.
func (h *Handler) GetEmail(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}
	messageID := mux.Vars(r)["id"]
	if messageID == "" {
		writeError(w, http.StatusBadRequest, "Message ID required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	gmailClient, err := h.gmailClientFor(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	msg, err := h.gmailService.GetMessage(gmailClient, messageID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Impossible de charger cet email : "+err.Error())
		return
	}

	from, subject, to, date := gmail.ParseEmailHeaders(msg)
	unsubURL, unsubMailto, oneClick := gmail.ParseUnsubscribe(msg)
	plain, html := gmail.GetEmailBodies(msg)

	markRead := r.URL.Query().Get("markRead") == "1"
	labelIDs := msg.LabelIds
	if markRead && !mailbox.GmailIsRead(labelIDs) {
		if err := h.applyVerb(ctx, gmailClient, mailbox.OnAccount(messageID), "read", ""); err == nil {
			labelIDs = mailbox.GmailAfter(labelIDs, mailbox.Mutation{Action: mailbox.ActionMarkRead})
			// Keep the local cache honest so the next list render does not show
			// the message as unread again.
			h.db.Emails().UpdateOne(ctx,
				bson.M{"userId": userEmail, "messageId": messageID},
				bson.M{"$set": bson.M{"isRead": true}},
			)
		}
	}

	view := messageView{
		Email: models.Email{
			MessageID:     msg.Id,
			UserID:        userEmail,
			ThreadID:      msg.ThreadId,
			From:          from,
			To:            to,
			Subject:       subject,
			Snippet:       msg.Snippet,
			Body:          plain,
			LabelIDs:      labelIDs,
			ReceivedDate:  date,
			IsRead:        mailbox.GmailIsRead(labelIDs),
			UnsubURL:      unsubURL,
			UnsubMailto:   unsubMailto,
			UnsubOneClick: oneClick,
		},
		BodyHTML:    html,
		Attachments: listAttachments(msg),
	}

	writeJSON(w, http.StatusOK, view)
}

// isInlinePart reports whether a named MIME part is embedded in the message body
// rather than attached to it.
func isInlinePart(part *gmailapi.MessagePart) bool {
	for _, h := range part.Headers {
		switch strings.ToLower(h.Name) {
		case "content-id", "x-attachment-id":
			if strings.TrimSpace(h.Value) != "" {
				return true
			}
		case "content-disposition":
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(h.Value)), "inline") {
				return true
			}
		}
	}
	return false
}

// listAttachments walks the MIME tree for parts that carry a filename.
func listAttachments(msg *gmailapi.Message) []attachmentView {
	if msg == nil || msg.Payload == nil {
		return nil
	}
	var out []attachmentView
	var walk func(part *gmailapi.MessagePart, depth int)
	walk = func(part *gmailapi.MessagePart, depth int) {
		if part == nil || depth > 12 {
			return
		}
		// A filename alone does not make an attachment: Gmail names the inline
		// images a newsletter references with cid: too (image001.png, logo.gif),
		// so listing every named part turned a normal marketing email into a row
		// of meaningless chips. An inline part announces itself with a
		// Content-ID, or with Content-Disposition: inline.
		if part.Filename != "" && !isInlinePart(part) {
			var size int64
			var attachmentID string
			if part.Body != nil {
				size = part.Body.Size
				attachmentID = part.Body.AttachmentId
			}
			out = append(out, attachmentView{
				Filename:     part.Filename,
				MimeType:     part.MimeType,
				Size:         size,
				AttachmentID: attachmentID,
			})
		}
		for _, child := range part.Parts {
			walk(child, depth+1)
		}
	}
	walk(msg.Payload, 0)
	return out
}
