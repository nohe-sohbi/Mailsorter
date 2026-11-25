package api

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"google.golang.org/api/googleapi"
)

// Gmail refuses a message id as naming no message when it does not know it
// (404) and when it is not an id at all (400: the number of an IMAP mailbox
// disconnected before history entries carried their generation). Anything
// else is not that, and is not answered as if it were.
func TestGmailNamesNoMail(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"not found", &googleapi.Error{Code: http.StatusNotFound}, true},
		{"an IMAP number", fmt.Errorf("modify: %w", &googleapi.Error{Code: http.StatusBadRequest, Message: "Invalid id value"}), true},
		{"Gmail down", &googleapi.Error{Code: http.StatusInternalServerError}, false},
		{"another error", errors.New("aucun libellé à retirer"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := gmailNamesNoMail(tc.err); got != tc.want {
				t.Errorf("gmailNamesNoMail(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
