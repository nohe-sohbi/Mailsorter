package api

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

// maxAttachmentBytes bounds what the download route will buffer and stream.
// Gmail itself refuses attachments above 25 MiB, so anything larger cannot be a
// real message part; the cap exists so a crafted (or corrupt) size can't make
// the server hold an arbitrary allocation.
const maxAttachmentBytes = 30 << 20 // 30 MiB

// fallbackAttachmentName is used when a part's filename is empty or sanitizes
// away to nothing. A download must always land with a usable name.
const fallbackAttachmentName = "piece-jointe"

// DownloadAttachment streams one attachment of one message back to the caller.
//
// The reader could already name what was attached but not open it, which is the
// half of the feature people actually need: an invoice you can see the name of
// and not read is still an email you have to go open in Gmail. Gmail keeps the
// bytes behind a separate call keyed by an attachment id, so this resolves the
// message first (to learn the part's real filename and MIME type, and to prove
// the caller's message actually carries that id) and then fetches the data.
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

	// Only a part of THIS message may be served. Without the lookup the route
	// would forward any attachment id the caller invented straight to Gmail,
	// turning it into a proxy for reading parts of messages the request never
	// named.
	att, ok := findAttachment(listAttachments(msg), attachmentID)
	if !ok {
		writeError(w, http.StatusNotFound, "Pièce jointe introuvable")
		return
	}
	if att.Size > maxAttachmentBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "Pièce jointe trop volumineuse")
		return
	}

	data, err := h.gmailService.GetAttachment(gmailClient, messageID, attachmentID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Téléchargement impossible : "+err.Error())
		return
	}
	if len(data) > maxAttachmentBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "Pièce jointe trop volumineuse")
		return
	}

	w.Header().Set("Content-Type", attachmentContentType(att.MimeType))
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Content-Disposition", contentDisposition(att.Filename))
	// The bytes come from a third party, so the browser must never be allowed to
	// sniff them into something executable in our origin.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// findAttachment returns the described part carrying attachmentID.
func findAttachment(list []attachmentView, attachmentID string) (attachmentView, bool) {
	for _, a := range list {
		if a.AttachmentID != "" && a.AttachmentID == attachmentID {
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

// safeAttachmentName reduces a sender-supplied filename to something safe to put
// in a header and on a disk: no directory components (a part named
// "../../.bashrc" must land as ".bashrc"), no control characters, no quotes or
// backslashes that would let it break out of the quoted-string form.
func safeAttachmentName(filename string) string {
	name := strings.TrimSpace(filename)
	// Both separators, because the name is authored on the sender's machine and
	// path.Base alone leaves a Windows path intact.
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(name)
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '"' {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || name == "/" {
		return fallbackAttachmentName
	}
	return name
}

// contentDisposition builds an attachment disposition that survives a non-ASCII
// filename. The quoted form is ASCII-only by spec, so an accented name is also
// published in the RFC 5987 `filename*` form, which every current browser
// prefers; the plain parameter stays as the fallback.
func contentDisposition(filename string) string {
	name := safeAttachmentName(filename)
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
		return fallbackAttachmentName
	}
	return out
}
