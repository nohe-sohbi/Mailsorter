package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"go.mongodb.org/mongo-driver/mongo"
)

// errReauthRequired signals that the user's Google authorization can no longer
// mint a valid access token (no refresh token on file, or the refresh was
// rejected, typically a revoked grant). Handlers map it to 401 so the SPA
// clears the session and restarts OAuth instead of looping on opaque 500s.
var errReauthRequired = errors.New("gmail authorization expired; re-authentication required")

// writeAuthError maps a getUserToken / gmailClientFor failure to the right
// status: 401 when the grant is dead (SPA re-runs OAuth), 404 when the account
// row is gone, 500 for anything else.
func writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errReauthRequired):
		writeError(w, http.StatusUnauthorized, "Gmail authorization expired. Please reconnect your account.")
	case errors.Is(err, mongo.ErrNoDocuments):
		writeError(w, http.StatusNotFound, "User not found")
	default:
		writeError(w, http.StatusInternalServerError, "Failed to get user credentials")
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
