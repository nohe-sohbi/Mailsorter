package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/nohe-sohbi/mailsorter/backend/internal/attachment"
	"github.com/nohe-sohbi/mailsorter/backend/internal/gmail"
	"github.com/nohe-sohbi/mailsorter/backend/internal/imap"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/protect"
	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
	gmailapi "google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
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
	// InlineImages maps the cid of each picture the HTML draws to the id the
	// download serves it under, on both transports: the screen downloads
	// them and puts them in place of the cid: URLs, which no browser
	// resolves.
	InlineImages map[string]string `json:"inlineImages,omitempty"`
}

// attachmentView describes a file carried by the message. AttachmentID is what
// the screen sends back to download it: the file's part number in the
// message, on both transports (see readGmailParts for Gmail's).
type attachmentView struct {
	Filename     string `json:"filename"`
	MimeType     string `json:"mimeType"`
	Size         int64  `json:"size"`
	AttachmentID string `json:"attachmentId,omitempty"`
	// gmailID is the handle Gmail gave the bytes in the copy of the message
	// this view was read from, and inlineData the bytes themselves when
	// Gmail sends them with the message instead. Never sent: they are only
	// good for that copy.
	gmailID    string
	inlineData string
}

// gmailPayloadPart names the file a single-part Gmail message is, which
// Gmail gives no part number ("" is the payload's).
const gmailPayloadPart = "payload"

// What the reader and the download answer when the mailbox could not hand a
// message over: sentences for the screen, which shows what the server says.
// The error itself, in English and in the protocol's words ("imap: fetch
// message 12 in \"INBOX\": ...", "googleapi: Error 404"), goes to the log.
const (
	messageGoneMessage      = "Ce mail n'est plus dans votre boîte : il a été déplacé ou supprimé entre-temps."
	messageFailedMessage    = "Ce mail n'a pas pu être chargé depuis votre boîte. Réessayez dans un instant."
	attachmentGoneMessage   = "Ce mail n'est plus dans votre boîte : la pièce jointe ne peut plus être téléchargée."
	attachmentFailedMessage = "Cette pièce jointe n'a pas pu être téléchargée depuis votre boîte. Réessayez dans un instant."
)

// mailGone says whether err means the message is no longer in the mailbox:
// moved or deleted from another mail client (the person's phone) since the
// list showed it. Over IMAP its UID no longer names anything in its folder;
// over the Gmail API its id answers 404.
func mailGone(err error) bool {
	if errors.Is(err, imap.ErrMessageGone) {
		return true
	}
	var apiErr *googleapi.Error
	return errors.As(err, &apiErr) && apiErr.Code == http.StatusNotFound
}

// writeMailboxFailure answers a read of the mailbox that failed: 404 with
// gone when the message is no longer there, 502 with failed otherwise, on
// either transport. What went wrong is logged after what, never sent.
func writeMailboxFailure(w http.ResponseWriter, err error, what, gone, failed string) {
	log.Printf("%s: %v", what, err)
	if mailGone(err) {
		writeError(w, http.StatusNotFound, gone)
		return
	}
	writeError(w, http.StatusBadGateway, failed)
}

// GetEmail returns a single message with its decoded body.
//
// The list endpoint deliberately omits bodies: it already fetches 100 messages
// per page, and shipping 100 HTML payloads to render one would be wasteful. So
// the body is fetched on demand, when the reader actually opens a message,
// which, before this route existed, it never could, leaving the reader stuck on
// "Contenu complet indisponible" for every email.
//
// With ?markRead=1 the message is also marked read in the mailbox, mirroring
// what opening an email means everywhere else. That is user intent rather than
// automation, so it is not written to the action ledger: the history exists to
// show what Mailsorter did on the user's behalf, and would drown in "Lu"
// entries otherwise.
//
// Both transports are served here, and they diverge after the fetch rather than
// before it: see getEmailIMAP for what a message does not carry over IMAP.
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

	markRead := r.URL.Query().Get("markRead") == "1"

	session, err := h.openSession(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	defer session.Close()

	if session.Transport == provider.TransportIMAP {
		h.getEmailIMAP(ctx, w, session, userEmail, messageID, markRead)
		return
	}
	gmailClient := session.gmail

	msg, err := h.gmailService.GetMessage(gmailClient, messageID)
	if err != nil {
		writeMailboxFailure(w, err, fmt.Sprintf("message: %s for %s", messageID, userEmail), messageGoneMessage, messageFailedMessage)
		return
	}

	from, subject, to, date := gmail.ParseEmailHeaders(msg)
	unsubURL, unsubMailto, oneClick := gmail.ParseUnsubscribe(msg)
	plain, html := gmail.GetEmailBodies(msg)

	labelIDs := msg.LabelIds
	if markRead && !mailbox.GmailIsRead(labelIDs) {
		if err := h.applyVerb(ctx, session.Mailbox(), session.RefFor(ctx, messageID), "read", ""); err == nil {
			labelIDs = mailbox.GmailAfter(labelIDs, mailbox.Mutation{Action: mailbox.ActionMarkRead})
			// Keep the local cache honest so the next list render does not show
			// the message as unread again.
			h.db.Emails().UpdateOne(ctx,
				bson.M{"userId": userEmail, "messageId": messageID},
				bson.M{"$set": bson.M{"isRead": true}},
			)
		}
	}

	parts := readGmailParts(msg, html)
	view := messageView{
		Email: models.Email{
			MessageID:     msg.Id,
			UserID:        userEmail,
			ThreadID:      msg.ThreadId,
			From:          from,
			To:            to,
			Subject:       subject,
			Snippet:       gmail.Snippet(msg),
			Body:          plain,
			LabelIDs:      labelIDs,
			ReceivedDate:  date,
			IsRead:        mailbox.GmailIsRead(labelIDs),
			UnsubURL:      unsubURL,
			UnsubMailto:   unsubMailto,
			UnsubOneClick: oneClick,
		},
		BodyHTML:     html,
		Attachments:  parts.files,
		InlineImages: parts.inlineImages(),
	}

	opts := options.Update().SetUpsert(true)
	h.db.Emails().UpdateOne(ctx,
		bson.M{"userId": userEmail, "messageId": messageID},
		bson.M{"$set": view.Email},
		opts,
	)

	view.SenderAddress = protect.Address(view.From)
	view.SenderLabel = protect.Label(view.From)
	writeJSON(w, http.StatusOK, view)
}

// gmailParts is what the reader shows of the parts of a Gmail message: the
// files it lists, and the pictures its HTML draws through cid:, by cid, the
// attachment.MaxDrawn first ones.
type gmailParts struct {
	files  []attachmentView
	images map[string]attachmentView
}

// inlineImages is the cid of each picture the HTML draws, to its id.
func (g gmailParts) inlineImages() map[string]string {
	if len(g.images) == 0 {
		return nil
	}
	out := make(map[string]string, len(g.images))
	for cid, img := range g.images {
		out[cid] = img.AttachmentID
	}
	return out
}

// find is the part the screen named id that the download serves: a listed
// file or a picture the HTML draws, by the id the reader hands out, else a
// listed file by the Gmail attachment id a screen opened before part
// numbers were handed out may still send, when this copy still gives it.
func (g gmailParts) find(id string) (attachmentView, bool) {
	if a, ok := findAttachment(g.files, id); ok {
		return a, true
	}
	for _, img := range g.images {
		if id != "" && img.AttachmentID == id {
			return img, true
		}
	}
	return attachmentView{}, false
}

// readGmailParts reads the parts of a Gmail message by the rule both
// transports share (internal/attachment), html being the HTML the reader
// renders for it: the files attachment.Listed lists, and the pictures
// attachment.InlineImage says the HTML draws.
//
// A part is named by its part number (partId: "1", "0.2"), not by the
// attachment id Gmail hands out for its bytes. Nothing in Gmail's
// documentation says that id stays the same from one messages.get to the
// next, so the screen gets the number, which does, and the download asks
// Gmail for a fresh id under it (downloadAttachmentGmail). A part whose bytes
// Gmail sends with the message rather than behind an id is named too: the
// download serves them from the message.
//
// The rule changed for Gmail when IMAP mailboxes got their attachments. A
// part with a Content-ID, an X-Attachment-Id or an inline disposition used
// to be hidden whatever it was: that hid every file attached from Gmail's
// own web interface, which gives each one a Content-ID and an
// X-Attachment-Id, and the PDF Apple Mail sends inline. Now only an unnamed
// image that html shows through cid: stays out of the list, as on IMAP, and
// the screen draws it. A part without a name is listed when it is declared
// an attachment ("piece-jointe") or is a forwarded mail ("message.eml"), and
// every name is cleaned (attachment.SafeName) before the screen gets it.
func readGmailParts(msg *gmailapi.Message, html string) gmailParts {
	out := gmailParts{images: map[string]attachmentView{}}
	if msg == nil || msg.Payload == nil {
		return out
	}
	shown := attachment.ShownBy(html)
	var walk func(part *gmailapi.MessagePart, depth int)
	walk = func(part *gmailapi.MessagePart, depth int) {
		if part == nil || depth > 12 {
			return
		}
		described := gmailPart(part)
		if name, ok := attachment.Listed(described, shown); ok {
			view := gmailView(msg, part)
			view.Filename = name
			out.files = append(out.files, view)
		}
		if cid, ok := attachment.InlineImage(described, shown); ok {
			if view := gmailView(msg, part); view.AttachmentID != "" {
				view.Filename = attachment.SafeName(part.Filename, attachment.Unnamed)
				out.images[cid] = view
			}
		}
		for _, child := range part.Parts {
			walk(child, depth+1)
		}
	}
	walk(msg.Payload, 0)
	// The pictures the screen draws, bounded: the download serves no other.
	cids := make([]string, 0, len(out.images))
	for cid := range out.images {
		cids = append(cids, cid)
	}
	kept := make(map[string]attachmentView, len(out.images))
	for _, cid := range shown.FirstDrawn(cids) {
		kept[cid] = out.images[cid]
	}
	out.images = kept
	return out
}

// gmailView describes one part of msg for the screen, its name aside. It
// gets an id when its bytes can be served: behind a Gmail attachment id, or
// sent with the message.
func gmailView(msg *gmailapi.Message, part *gmailapi.MessagePart) attachmentView {
	view := attachmentView{MimeType: part.MimeType}
	if part.Body != nil {
		view.Size = part.Body.Size
		view.gmailID = part.Body.AttachmentId
		if view.gmailID == "" {
			view.inlineData = part.Body.Data
		}
	}
	if view.gmailID == "" && view.inlineData == "" {
		return view
	}
	switch {
	case part.PartId != "":
		view.AttachmentID = part.PartId
	case part == msg.Payload:
		view.AttachmentID = gmailPayloadPart
	case view.gmailID != "":
		// A part with no number of its own: Gmail's handle is all there is
		// to name it by.
		view.AttachmentID = view.gmailID
	}
	return view
}

// gmailPart is what a part Gmail hands over says about itself.
func gmailPart(part *gmailapi.MessagePart) attachment.Part {
	p := attachment.Part{Filename: part.Filename, MediaType: part.MimeType}
	for _, h := range part.Headers {
		switch strings.ToLower(h.Name) {
		case "content-disposition":
			p.Disposition = h.Value
		case "content-id":
			p.ContentID = h.Value
		}
	}
	return p
}

// getEmailIMAP is the IMAP half of GetEmail.
//
// The two halves differ in more than their transport. Over the Gmail API the
// message carries its labels and its attachment parts, and the reader renders
// both. Over IMAP there are no labels. The files are listed by the client
// from the server's description of the message (imap.Client.Fetch), each
// under its part number, which is the id the download route takes back.
func (h *Handler) getEmailIMAP(ctx context.Context, w http.ResponseWriter, session *mailSession, userEmail, messageID string, markRead bool) {
	ref := session.RefFor(ctx, messageID)

	msg, err := session.imapc.Fetch(ctx, ref.Folder, ref.ID)
	if err != nil {
		writeMailboxFailure(w, err, fmt.Sprintf("message: %s in %q for %s", ref.ID, ref.Folder, userEmail), messageGoneMessage, messageFailedMessage)
		return
	}

	msg.UserID = userEmail
	msg.Folder = ref.Folder
	if markRead && !msg.IsRead {
		if err := h.applyVerb(ctx, session.Mailbox(), ref, "read", ""); err == nil {
			msg.IsRead = true
		}
	}

	opts := options.Update().SetUpsert(true)
	h.db.Emails().UpdateOne(ctx,
		bson.M{"userId": userEmail, "messageId": messageID},
		bson.M{"$set": msg.Email},
		opts,
	)

	view := messageView{
		Email:        msg.Email,
		BodyHTML:     msg.HTML,
		Attachments:  imapAttachmentViews(msg.Attachments),
		InlineImages: msg.InlineImages,
	}
	view.SenderAddress = protect.Address(view.From)
	view.SenderLabel = protect.Label(view.From)
	writeJSON(w, http.StatusOK, view)
}

// imapAttachmentViews describes the files of an IMAP message for the reader.
func imapAttachmentViews(list []imap.Attachment) []attachmentView {
	out := make([]attachmentView, 0, len(list))
	for _, a := range list {
		out = append(out, attachmentView{
			Filename:     a.Filename,
			MimeType:     a.MimeType,
			Size:         a.Size,
			AttachmentID: a.Part,
		})
	}
	return out
}
