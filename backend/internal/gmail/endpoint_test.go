package gmail

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/oauth2"
	gmailapi "google.golang.org/api/gmail/v1"
)

// A client built by GetClient talks to Google unless WithEndpoint said
// otherwise. Both halves matter: the acceptance scenarios depend on the
// override reaching every call, and a deployment depends on nothing else ever
// setting it.
func TestGetClientEndpoint(t *testing.T) {
	token := &oauth2.Token{AccessToken: "access", Expiry: time.Now().Add(time.Hour)}

	t.Run("defaults to Google", func(t *testing.T) {
		svc := NewService("id", "secret", "http://localhost/cb")
		if got, want := svc.GetClient(token).BasePath, "https://gmail.googleapis.com/"; got != want {
			t.Errorf("GetClient().BasePath = %q, want %q", got, want)
		}
	})

	t.Run("WithEndpoint reroutes every call", func(t *testing.T) {
		var gotAuth, gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
			json.NewEncoder(w).Encode(&gmailapi.Profile{EmailAddress: "me@example.com"})
		}))
		defer srv.Close()

		svc := NewService("id", "secret", "http://localhost/cb").WithEndpoint(srv.URL + "/")
		addr, err := svc.GetUserProfile(svc.GetClient(token))
		if err != nil {
			t.Fatalf("GetUserProfile() error = %v", err)
		}
		if addr != "me@example.com" {
			t.Errorf("GetUserProfile() = %q, want %q", addr, "me@example.com")
		}
		if gotPath != "/gmail/v1/users/me/profile" {
			t.Errorf("request path = %q, want %q", gotPath, "/gmail/v1/users/me/profile")
		}
		if gotAuth != "Bearer access" {
			t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer access")
		}
	})
}
