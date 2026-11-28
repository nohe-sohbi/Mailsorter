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
	"go.mongodb.org/mongo-driver/bson"
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
// exportable by default. That default is wrong for the datasets that hold a
// credential, and this is the assertion that keeps it wrong on purpose.
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
		if ds == account.DatasetMailAccounts || ds == account.DatasetAISettings {
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

func TestConnectMailboxRefusesAnOversizedPassword(t *testing.T) {
	srv := newRoutedTestServer(t)
	token := newTestAuth(t).IssueSession("someone@example.com")

	longPassword := strings.Repeat("a", 1025)
	body, err := json.Marshal(models.ConnectMailboxRequest{Address: "someone@orange.fr", Password: longPassword})
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
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("connect with password length 1025 = %d, want 400", resp.StatusCode)
	}
}

// A provider the running edition does not reach over IMAP has to be refused
// before any connection is attempted, and with a status that says "this cannot
// work here" rather than "your password is wrong". Outlook.com only routes
// through Microsoft Graph, so there is no IMAP endpoint to pick. (Gmail is not
// such a provider: its catalog entry offers IMAP in both editions, see
// provider.PickIMAP.)
func TestConnectMailboxRefusesAProviderTheEditionReachesAnotherWay(t *testing.T) {
	previous := Edition
	Edition = provider.EditionSelfHosted
	t.Cleanup(func() { Edition = previous })

	srv := newRoutedTestServer(t)
	token := newTestAuth(t).IssueSession("someone@example.com")

	body, err := json.Marshal(models.ConnectMailboxRequest{Address: "someone@outlook.com", Password: "app-password"})
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
		t.Errorf("connecting Outlook.com over IMAP = %d, want 422", resp.StatusCode)
	}
}

// applyUpsert is what MongoDB makes of update over prev (nil: no row yet), for
// the operators mailboxRow writes.
func applyUpsert(t *testing.T, prev bson.M, update bson.M) bson.M {
	t.Helper()
	row := bson.M{}
	for k, v := range prev {
		row[k] = v
	}
	for op, fields := range update {
		m, ok := fields.(bson.M)
		if !ok {
			t.Fatalf("update %s is a %T, want a bson.M", op, fields)
		}
		switch op {
		case "$set":
			for k, v := range m {
				row[k] = v
			}
		case "$setOnInsert":
			if prev == nil {
				for k, v := range m {
					row[k] = v
				}
			}
		case "$unset":
			for k := range m {
				delete(row, k)
			}
		default:
			t.Fatalf("update has the operator %s, which applyUpsert does not know", op)
		}
	}
	return row
}

// readRow decodes a stored row through the model, the way sendThroughBox
// reads it back: a field name the update and the model spell differently
// comes back empty.
func readRow(t *testing.T, row bson.M) models.MailAccount {
	t.Helper()
	raw, err := bson.Marshal(row)
	if err != nil {
		t.Fatalf("bson.Marshal(%v): %v", row, err)
	}
	var account models.MailAccount
	if err := bson.Unmarshal(raw, &account); err != nil {
		t.Fatalf("bson.Unmarshal into MailAccount: %v", err)
	}
	return account
}

// Connecting a mailbox writes the sending server of the route its IMAP server
// was proved on, and a route without one clears the server a previous
// connection left: the recap must never leave from another provider's server
// with this mailbox's password.
func TestMailboxRowWritesTheSendingServerOfTheRoute(t *testing.T) {
	now := time.Date(2026, 11, 24, 7, 0, 0, 0, time.UTC)
	imapServer := &provider.Endpoint{Host: "imap.exemple.fr", Port: 993, TLS: provider.TLSImplicit}
	withSMTP := provider.Route{Transport: provider.TransportIMAP, IMAP: imapServer,
		SMTP: &provider.Endpoint{Host: "smtp.exemple.fr", Port: 587, TLS: provider.TLSSTARTTLS}}
	withoutSMTP := provider.Route{Transport: provider.TransportIMAP, IMAP: imapServer}
	previous := bson.M{"userId": "moi@exemple.fr", "username": "ancien@ailleurs.fr", "secret": "old-sealed",
		"smtpHost": "smtp.ailleurs.fr", "smtpPort": 465, "smtpTls": "implicit", "createdAt": now.Add(-time.Hour)}

	cases := []struct {
		name  string
		prev  bson.M
		route provider.Route
		want  provider.Endpoint
	}{
		{"a first connection, on a route with a sending server", nil, withSMTP,
			provider.Endpoint{Host: "smtp.exemple.fr", Port: 587, TLS: provider.TLSSTARTTLS}},
		{"over a previous connection, on a route with a sending server", previous, withSMTP,
			provider.Endpoint{Host: "smtp.exemple.fr", Port: 587, TLS: provider.TLSSTARTTLS}},
		{"over a previous connection, on a route without one", previous, withoutSMTP, provider.Endpoint{}},
		{"a first connection, on a route without one", nil, withoutSMTP, provider.Endpoint{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			account, update := mailboxRow("moi@exemple.fr", "exemple", "moi@exemple.fr", tc.route, "sealed", now)

			answered := provider.Endpoint{Host: account.SMTPHost, Port: account.SMTPPort, TLS: provider.TLSMode(account.SMTPTLS)}
			if answered != tc.want {
				t.Errorf("mailboxRow answers the sending server %+v, want %+v", answered, tc.want)
			}

			stored := readRow(t, applyUpsert(t, tc.prev, update))
			got := provider.Endpoint{Host: stored.SMTPHost, Port: stored.SMTPPort, TLS: provider.TLSMode(stored.SMTPTLS)}
			if got != tc.want {
				t.Errorf("the stored row has the sending server %+v, want %+v", got, tc.want)
			}
			if stored.Username != "moi@exemple.fr" || stored.Secret != "sealed" || stored.UserID != "moi@exemple.fr" {
				t.Errorf("the stored row is %q / %q / user %q, want the new address, its sealed password and the account", stored.Username, stored.Secret, stored.UserID)
			}
			if stored.Host != imapServer.Host || stored.Port != imapServer.Port || stored.TLS != string(imapServer.TLS) {
				t.Errorf("the stored row has the IMAP server %s:%d %s, want %+v", stored.Host, stored.Port, stored.TLS, *imapServer)
			}
			if account.Secret != "" {
				t.Errorf("mailboxRow answers an account carrying the secret %q, want none", account.Secret)
			}
		})
	}
}

// The moment a mailbox was connected (createdAt) is when the rules a sync
// applies on its own start judging its mails, and the mark of its first sync
// (firstSyncedAt) is what makes the next ones judge them: connecting the same
// mailbox again (a new app password) writes over its row and keeps both;
// another mailbox gets a row of its own (connectAndStore deletes the previous
// one), so both start anew.
func TestMailboxRowKeepsTheMomentOfTheRowItWritesOver(t *testing.T) {
	now := time.Date(2026, 11, 25, 7, 0, 0, 0, time.UTC)
	before := now.Add(-48 * time.Hour)
	server := &provider.Endpoint{Host: "imap.exemple.fr", Port: 993, TLS: provider.TLSImplicit}
	route := provider.Route{Transport: provider.TransportIMAP, IMAP: server}
	synced := before.Add(time.Hour)
	prev := bson.M{"userId": "moi@exemple.fr", "username": "moi@exemple.fr", "host": "imap.exemple.fr", "port": 993, "createdAt": before, "firstSyncedAt": synced}

	for _, tc := range []struct {
		name       string
		prev       bson.M
		want       time.Time
		wantSynced time.Time // zero: the next sync is a first one
	}{
		{"the same mailbox again", prev, before, synced},
		{"a new row", nil, now, time.Time{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, update := mailboxRow("moi@exemple.fr", "exemple", "moi@exemple.fr", route, "sealed", now)
			row := readRow(t, applyUpsert(t, tc.prev, update))
			if !row.CreatedAt.Equal(tc.want) {
				t.Errorf("createdAt = %v, want %v", row.CreatedAt, tc.want)
			}
			if !row.FirstSyncedAt.Equal(tc.wantSynced) {
				t.Errorf("firstSyncedAt = %v, want %v", row.FirstSyncedAt, tc.wantSynced)
			}
		})
	}
}

func TestSameMailbox(t *testing.T) {
	server := &provider.Endpoint{Host: "imap.exemple.fr", Port: 993, TLS: provider.TLSImplicit}
	route := provider.Route{Transport: provider.TransportIMAP, IMAP: server}
	prev := models.MailAccount{Username: "Moi@Exemple.fr", Host: "imap.exemple.fr", Port: 993}
	for _, tc := range []struct {
		name    string
		prev    models.MailAccount
		address string
		route   provider.Route
		want    bool
	}{
		{"same address, same server", prev, "moi@exemple.fr", route, true},
		{"another address", prev, "autre@exemple.fr", route, false},
		{"another server", prev, "moi@exemple.fr", provider.Route{Transport: provider.TransportIMAP, IMAP: &provider.Endpoint{Host: "imap.ailleurs.fr", Port: 993}}, false},
		{"another port", prev, "moi@exemple.fr", provider.Route{Transport: provider.TransportIMAP, IMAP: &provider.Endpoint{Host: "imap.exemple.fr", Port: 143}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameMailbox(tc.prev, tc.address, tc.route); got != tc.want {
				t.Errorf("sameMailbox(%+v, %q, %+v) = %v, want %v", tc.prev, tc.address, tc.route.IMAP, got, tc.want)
			}
		})
	}
}
