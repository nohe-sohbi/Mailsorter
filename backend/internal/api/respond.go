package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/nohe-sohbi/mailsorter/backend/internal/imap"
	"go.mongodb.org/mongo-driver/mongo"
)

// errReauthRequired signals that the user's Google authorization can no longer
// mint a valid access token (no refresh token on file, or the refresh was
// rejected, typically a revoked grant). Handlers map it to 401 so the SPA
// clears the session and restarts OAuth instead of looping on opaque 500s.
var errReauthRequired = errors.New("gmail authorization expired; re-authentication required")

// mailboxRejectedMessage is what the user reads when their mail provider
// refuses the address and the app password Mailsorter holds: on the connect
// screen as everywhere else.
const mailboxRejectedMessage = "Identifiants refusés par le fournisseur. Vérifiez le mot de passe d'application."

// mailboxNoAnswerMessage is what the user reads when their mailbox could not
// be opened for another reason: out of reach, too slow, or unavailable.
const mailboxNoAnswerMessage = "Votre boîte n'a pas répondu, réessayez dans un instant."

// noMailboxMessage is what the user reads when no mailbox is connected.
const noMailboxMessage = "Aucune boîte mail n'est connectée à ce compte. Rendez-vous sur /connect pour en brancher une."

// severalCopiesMessage is what the user reads when "Annuler" would bring back
// a mail whose Message-ID several messages carry where Mailsorter put it
// (imap.ErrAmbiguous): which one to put back cannot be told.
const severalCopiesMessage = "Plusieurs copies de ce mail sont dans votre boîte : Mailsorter ne sait pas laquelle remettre. Faites-le depuis votre messagerie."

// writeSeveralCopies answers an "Annuler" refused for severalCopiesMessage:
// 409, and severalCopies so that a screen can tell it from the 409 of an
// action already undone, which it treats as such.
func writeSeveralCopies(w http.ResponseWriter) {
	writeJSON(w, http.StatusConflict, map[string]interface{}{
		"error": severalCopiesMessage, "status": http.StatusConflict, "severalCopies": true,
	})
}

// writeAuthError maps a getUserToken / gmailClientFor / openSession failure to
// the right status: 401 when the Google grant is dead (SPA re-runs OAuth), 428
// when no mailbox is connected or when the provider refuses the IMAP password
// on file (to be connected again on /connect: a 401 would sign the person out
// of Mailsorter, whose session is fine), 502 when the mailbox did not answer,
// 404 when the account row is gone, 501 when the path has not been ported to
// the caller's transport, 500 for anything else. In French, as the screens
// show them.
//
// The 501 branch is the visible half of the guard in gmailClientFor. Every
// handler already routes its credential failures through here, so adding the
// case in one place is what turns "a confusing 500" into "this does not work on
// an IMAP mailbox yet" across the whole unported surface at once.
func writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNoMailboxConnected):
		writeError(w, http.StatusPreconditionRequired, noMailboxMessage)
	case errors.Is(err, errReauthRequired):
		writeError(w, http.StatusUnauthorized, "L'autorisation Google a expiré. Reconnectez votre compte Google.")
	case errors.Is(err, imap.ErrAuth):
		writeError(w, http.StatusPreconditionRequired, mailboxRejectedMessage)
	case errors.Is(err, imap.ErrUnreachable):
		writeError(w, http.StatusBadGateway, mailboxNoAnswerMessage)
	case errors.Is(err, errWrongTransport):
		writeTransportError(w)
	case errors.Is(err, mongo.ErrNoDocuments):
		writeError(w, http.StatusNotFound, "User not found")
	default:
		writeError(w, http.StatusInternalServerError, "Impossible d'ouvrir votre boîte pour le moment, réessayez dans un instant.")
	}
}

// maxRequestBody caps the size of a JSON request body Mailsorter will read. None
// of our payloads (an action, a rule, a settings toggle) come close to 1 MiB, so
// anything larger is either a mistake or an attempt to exhaust server memory.
const maxRequestBody = 1 << 20 // 1 MiB

// writeJSON writes v as a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a consistent JSON error envelope ({"error":...,"status":n})
// instead of the bare text http.Error produces, so clients can parse failures
// uniformly.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]interface{}{"error": msg, "status": status})
}

// decodeJSON reads a bounded JSON body into dst. It wraps the body in a
// MaxBytesReader so an oversized payload is rejected with 413 rather than read
// into memory, and returns a clean 400 on malformed JSON. It returns true only
// when dst was populated successfully; on failure it has already written the
// response and the caller should just return.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst interface{}) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "Request body too large")
			return false
		}
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return false
	}
	return true
}
