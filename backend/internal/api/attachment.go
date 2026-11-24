package api

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/nohe-sohbi/mailsorter/backend/internal/attachment"
	"github.com/nohe-sohbi/mailsorter/backend/internal/gmail"
	"github.com/nohe-sohbi/mailsorter/backend/internal/imap"
	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
	gmailapi "google.golang.org/api/gmail/v1"
)

// maxAttachmentBytes bounds what the download route will buffer and stream.
// Gmail itself refuses attachments above 25 MiB, so anything larger cannot be a
// real message part; the cap exists so a crafted (or corrupt) size can't make
// the server hold an arbitrary allocation.
const maxAttachmentBytes = 30 << 20 // 30 MiB

// attachmentNotInMailIMAP is the answer to a download of a file the mail on
// an IMAP mailbox does not have.
const attachmentNotInMailIMAP = "Cette pièce jointe est introuvable dans ce mail."

// DownloadAttachment streams one attachment of one message back to the caller.
//
// The reader could already name what was attached but not open it, which is the
// half of the feature people actually need: an invoice you can see the name of
// and not read is still an email you have to go open in Gmail. Gmail keeps the
// bytes behind a separate call keyed by an attachment id, so this resolves the
// message first (to learn the part's real filename and MIME type, and to prove
// the caller's message actually carries that id) and then fetches the data.
// Over IMAP the bytes are inside the message: see downloadAttachmentIMAP.
func (h *Handler) DownloadAttachment(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	vars := mux.Vars(r)
	messageID, attachmentID := vars["id"], vars["attachmentId"]
	if messageID == "" || attachmentID == "" {
		writeError(w, http.StatusBadRequest, "Message et pièce jointe requis")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	session, err := h.openSession(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	defer session.Close()

	if session.Transport == provider.TransportIMAP {
		h.downloadAttachmentIMAP(ctx, w, session, messageID, attachmentID)
		return
	}
	h.downloadAttachmentGmail(w, session.gmail, userEmail, messageID, attachmentID)
}

// downloadAttachmentGmail is the Gmail half of DownloadAttachment.
//
// The id is the part number the reader listed. The message is read again,
// fresh, and the bytes are asked for under the attachment id Gmail gives that
// part in this copy: an id from an earlier copy is not trusted to still name
// them. An id that is no part number, sent by a screen opened before the
// reader handed out part numbers, is looked for among the attachment ids of
// the fresh copy, and served only if it is still one of them.
func (h *Handler) downloadAttachmentGmail(w http.ResponseWriter, gmailClient *gmailapi.Service, userEmail, messageID, attachmentID string) {
	failed := fmt.Sprintf("attachment: %s of message %s for %s", attachmentID, messageID, userEmail)
	msg, err := h.gmailService.GetMessage(gmailClient, messageID)
	if err != nil {
		writeMailboxFailure(w, err, failed, attachmentGoneMessage, attachmentFailedMessage)
		return
	}

	// Only a part of THIS message may be served. Without the lookup the route
	// would forward any attachment id the caller invented straight to Gmail,
	// turning it into a proxy for reading parts of messages the request never
	// named. The list is the reader's, HTML included, so a part the reader
	// does not offer is not served either.
	_, html := gmail.GetEmailBodies(msg)
	att, ok := findAttachment(listAttachments(msg, html), attachmentID)
	if !ok {
		writeError(w, http.StatusNotFound, "Pièce jointe introuvable")
		return
	}
	if att.Size > maxAttachmentBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "Pièce jointe trop volumineuse")
		return
	}

	data, err := h.gmailService.GetAttachment(gmailClient, messageID, att.gmailID)
	if err != nil {
		writeMailboxFailure(w, err, failed, attachmentGoneMessage, attachmentFailedMessage)
		return
	}
	if len(data) > maxAttachmentBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "Pièce jointe trop volumineuse")
		return
	}
	writeAttachment(w, att.Filename, att.MimeType, data)
}

// downloadAttachmentIMAP is the IMAP half of DownloadAttachment.
//
// The id is the part number the reader listed (getEmailIMAP), and the client
// serves it only when the walk of this same message lists a file under it. A
// part number is easy to guess ("1" is the text of almost every mail), so
// without that check the route would hand out the text of a mail, or any part
// of it, as a file.
//
// The cap is the client's to hold, because over IMAP the bytes are read to be
// measured: it asks the server for the file's size first, and never keeps
// more than the cap of it.
func (h *Handler) downloadAttachmentIMAP(ctx context.Context, w http.ResponseWriter, session *mailSession, messageID, attachmentID string) {
	ref := session.RefFor(ctx, messageID)
	att, data, err := session.imapc.Attachment(ctx, ref.Folder, ref.ID, attachmentID, maxAttachmentBytes)
	switch {
	case errors.Is(err, imap.ErrNoSuchAttachment):
		writeError(w, http.StatusNotFound, attachmentNotInMailIMAP)
		return
	case errors.Is(err, imap.ErrAttachmentTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "Pièce jointe trop volumineuse")
		return
	case err != nil:
		failed := fmt.Sprintf("attachment: %s of message %s in %q for %s", attachmentID, ref.ID, ref.Folder, session.userEmail)
		writeMailboxFailure(w, err, failed, attachmentGoneMessage, attachmentFailedMessage)
		return
	}
	writeAttachment(w, att.Filename, att.MimeType, data)
}

// writeAttachment sends a file as a download, on either transport.
func writeAttachment(w http.ResponseWriter, filename, mimeType string, data []byte) {
	w.Header().Set("Content-Type", attachmentContentType(mimeType))
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Content-Disposition", contentDisposition(filename))
	// The bytes come from a third party, so the browser must never be allowed to
	// sniff them into something executable in our origin.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// findAttachment returns the listed file the screen named id: by the id
// the reader hands out (its part number), else by the Gmail attachment id a
// screen opened before part numbers were handed out may still send, when the
// copy the list was read from still gives a file that id.
func findAttachment(list []attachmentView, id string) (attachmentView, bool) {
	if id == "" {
		return attachmentView{}, false
	}
	for _, a := range list {
		if a.AttachmentID == id {
			return a, true
		}
	}
	for _, a := range list {
		if a.AttachmentID != "" && a.gmailID == id {
			return a, true
		}
	}
	return attachmentView{}, false
}

// attachmentContentType keeps a sender-declared MIME type only when it parses.
// A malformed (or absent) type falls back to the generic binary type, which is
// what makes the browser save the file instead of trying to render it.
func attachmentContentType(mimeType string) string {
	mimeType = strings.TrimSpace(mimeType)
	if mimeType == "" {
		return "application/octet-stream"
	}
	if _, _, err := mime.ParseMediaType(mimeType); err != nil {
		return "application/octet-stream"
	}
	return mimeType
}

// contentDisposition builds an attachment disposition that survives a non-ASCII
// filename. The quoted form is ASCII-only by spec, so an accented name is also
// published in the RFC 5987 `filename*` form, which every current browser
// prefers; the plain parameter stays as the fallback. The name is cleaned the
// way the reader's list cleans it (attachment.SafeName), so the two agree.
func contentDisposition(filename string) string {
	name := attachment.SafeName(filename, attachment.Unnamed)
	ascii := asciiFallbackName(name)
	return fmt.Sprintf("attachment; filename=%q; filename*=UTF-8''%s", ascii, url.PathEscape(name))
}

// asciiFallbackName replaces every non-ASCII rune with '_' so the legacy
// filename parameter stays inside the quoted-string grammar.
func asciiFallbackName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r > 0x7f {
			b.WriteRune('_')
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	if strings.TrimSpace(strings.Trim(out, "_")) == "" {
		return attachment.Unnamed
	}
	return out
}
