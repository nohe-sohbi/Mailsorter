package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/account"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
)

// The one keystroke between this struct and a leaked credential is the json
// tag on Secret. GET /api/mailbox returns the struct itself, so a tag changed
// to `json:"secret"` (or dropped, which makes it "Secret") puts the sealed app
// password in an ordinary API response. Pinning the marshalled shape catches
// that; reading the struct definition carefully does not.
func TestMailAccountNeverSerializesTheSecret(t *testing.T) {
	blob, err := json.Marshal(models.MailAccount{
		UserID:    "someone@example.com",
		Provider:  "orange",
		Transport: "imap",
		Username:  "someone@orange.fr",
		Secret:    "enc:v1:THE-SEALED-APP-PASSWORD",
		Host:      "imap.orange.fr",
		Port:      993,
		TLS:       "implicit",
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(blob)

	for _, forbidden := range []string{"THE-SEALED-APP-PASSWORD", "enc:v1:", "secret", "Secret"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("GET /api/mailbox would ship %q; the response was:\n%s", forbidden, body)
		}
	}
	// The userId is the caller's own identity header, so it is noise on the way
	// out rather than a secret, but it should not be echoed either.
	if strings.Contains(body, "someone@example.com") {
		t.Errorf("the response echoes the userId:\n%s", body)
	}
	// And the useful half must survive the redaction.
	if !strings.Contains(body, "imap.orange.fr") || !strings.Contains(body, "993") {
		t.Errorf("the response lost the connection details it is for:\n%s", body)
	}
}

// Export and erasure share one catalog, so adding a collection makes its rows
// exportable by default. That default is wrong for exactly one dataset, and
// this is the assertion that keeps it wrong on purpose.
func TestMailAccountsAreErasableButTheirSecretIsNotExportable(t *testing.T) {
	found := false
	for _, ds := range account.Datasets() {
		if ds == account.DatasetMailAccounts {
			found = true
		}
	}
	if !found {
		t.Error("mailAccounts is missing from account.Datasets(); deleting an account would leave the app password on file")
	}

	secrets := account.SecretFields(account.DatasetMailAccounts)
	if len(secrets) != 1 || secrets[0] != "secret" {
		t.Errorf("SecretFields(mailAccounts) = %v, want [secret]", secrets)
	}

	h := newTestHandler(t)
	if h.datasetCollection(account.DatasetMailAccounts) == nil {
		t.Error("datasetCollection has no entry for mailAccounts; export and erasure would silently skip it")
	}

	// Every other dataset stays fully exportable: a blanket redaction would
	// quietly empty the export the RGPD promise is about.
	for _, ds := range account.Datasets() {
		if ds == account.DatasetMailAccounts {
			continue
		}
		if fields := account.SecretFields(ds); len(fields) != 0 {
			t.Errorf("SecretFields(%s) = %v, want none", ds, fields)
		}
	}
}

// No app password was ever stored in the clear, so an unsealed value in that
// column is corruption or tampering. Treating it as a password the way tokens.go
// treats a legacy token would send it straight to a mail server.
func TestOpenSecretRefusesAnUnsealedValue(t *testing.T) {
	h := newTestHandler(t)

	if _, err := h.openSecret("hunter2"); err == nil {
		t.Error("openSecret accepted a plaintext value; want it refused")
	}
	if got, err := h.openSecret(""); err != nil || got != "" {
		t.Errorf("openSecret(\"\") = %q, %v; want an empty value and no error", got, err)
	}

	sealed, err := h.sealSecret("app-password")
	if err != nil {
		t.Fatalf("sealSecret: %v", err)
	}
	if !strings.HasPrefix(sealed, tokenCipherPrefix) {
		t.Errorf("sealSecret produced %q, want the %q prefix", sealed, tokenCipherPrefix)
	}
	if strings.Contains(sealed, "app-password") {
		t.Errorf("sealSecret left the plaintext visible: %q", sealed)
	}
	back, err := h.openSecret(sealed)
	if err != nil || back != "app-password" {
		t.Errorf("openSecret(sealSecret(x)) = %q, %v; want the original back", back, err)
	}
}

// The request body carries an address and a password and nothing else. A body
// that could name its own host would let any caller make the server open an
// authenticated connection wherever they liked, which is the hole the one-click
// unsubscribe had. Marshalling the struct is how that stays true.
func TestConnectRequestCannotNameAHost(t *testing.T) {
	blob, err := json.Marshal(models.ConnectMailboxRequest{Address: "a@b.com", Password: "p"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var fields map[string]interface{}
	if err := json.Unmarshal(blob, &fields); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(fields) != 2 {
		t.Errorf("the connect body has %d field(s): %v; want address and password only", len(fields), fields)
	}
	for _, forbidden := range []string{"host", "port", "tls", "provider", "transport"} {
		if _, ok := fields[forbidden]; ok {
			t.Errorf("the connect body accepts %q; the server must resolve that from the address", forbidden)
		}
	}
}

// The routes exist, are behind auth, and reject a body with nothing in it. The
// real router is mounted, so this also proves they were registered.
func TestMailboxRoutesAreAuthenticated(t *testing.T) {
	srv := newRoutedTestServer(t)

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/mailbox"},
		{http.MethodPost, "/api/mailbox/connect"},
		{http.MethodDelete, "/api/mailbox"},
	} {
		req, err := http.NewRequest(tc.method, srv.URL+tc.path, nil)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s without a token = %d, want 401", tc.method, tc.path, resp.StatusCode)
		}
	}
}

func TestConnectMailboxRefusesAnEmptyBody(t *testing.T) {
	srv := newRoutedTestServer(t)
	token := newTestAuth(t).IssueSession("someone@example.com")

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/mailbox/connect", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("connect with no address = %d, want 400", resp.StatusCode)
	}
}

// A provider the running edition does not reach over IMAP has to be refused
// before any connection is attempted, and with a status that says "this cannot
// work here" rather than "your password is wrong". In self-hosted, Gmail is the
// API route, so it has no IMAP endpoint to pick.
func TestConnectMailboxRefusesAProviderTheEditionReachesAnotherWay(t *testing.T) {
	previous := Edition
	Edition = provider.EditionSelfHosted
	t.Cleanup(func() { Edition = previous })

	srv := newRoutedTestServer(t)
	token := newTestAuth(t).IssueSession("someone@example.com")

	body, err := json.Marshal(models.ConnectMailboxRequest{Address: "someone@gmail.com", Password: "app-password"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/mailbox/connect", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("connecting Gmail over IMAP on a self-hosted instance = %d, want 422", resp.StatusCode)
	}
}
