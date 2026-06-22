package api

import (
	"encoding/json"
	"errors"
	"net/http"
)

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
