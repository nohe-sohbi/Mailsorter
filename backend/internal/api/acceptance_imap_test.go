//go:build acceptance

package api

// The IMAP side of the harness: people whose mailbox Mailsorter reaches over
// IMAP, each with a real IMAP server of their own, and the database as
// production runs it.
//
// A person here is what the hosted edition calls a user: an account signed in
// with its own session, and a mailbox row in mail_accounts pointing at a server.
// The server is the go-imap in-memory one the imap package is tested against,
// served over IMPLICIT TLS like every real provider in the catalog, so the
// request goes through the production path untouched: openSession, openMailbox,
// openSecret, imap.Connect and its certificate check. Nothing in production
// code was relaxed to make that possible (see internal/imap/imap.go, newClient,
// on why that seam must not exist): the test process trusts the test
// certificate the way any Go program trusts a certificate, through
// SSL_CERT_FILE.

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"mime"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	imapv2 "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/nohe-sohbi/mailsorter/backend/internal/database"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// imapTestPassword is every person's app password. It reaches the server only
// after being sealed into mail_accounts and opened again by openMailbox.
const imapTestPassword = "mot-de-passe-d-application"

// ---------------------------------------------------------------------------
// The database as production has it
// ---------------------------------------------------------------------------

// seedProductionVolume gives a fresh database the indexes the production
// volume already holds on the emails collection, BEFORE the app's own
// EnsureIndexes runs, which is the order production boots in: the volume
// predates the code.
//
// Two sources put them there, and both are copied here as a fixed picture
// rather than read at runtime:
//   - mongo-init/init-db.js, which MongoDB ran once when the volume was created
//     and never again. Its unique index on messageId ALONE is the one that
//     matters: it outlives any change to EnsureIndexes, which never removes an
//     index.
//   - EnsureIndexes as it stood when this picture was taken, which has run at
//     every boot since. Seeding its indexes too means a change that redefines
//     one under the same name fails here the way it would fail on that volume.
func seedProductionVolume(ctx context.Context, db *database.Database) error {
	emails := db.Emails()
	_, err := emails.Indexes().CreateMany(ctx, []mongo.IndexModel{
		// init-db.js
		{Keys: bson.D{{Key: "messageId", Value: 1}}, Options: options.Index().SetUnique(true).SetName("messageId_1")},
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "receivedDate", Value: -1}}},
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "labelIds", Value: 1}}},
		// EnsureIndexes, as of commit 9a1adee
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "messageId", Value: 1}}},
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "from", Value: 1}}},
	})
	return err
}

// refuseToStore makes the database refuse any mail with this subject, and only
// that one: a write error on one message while the others go through, which is
// what a real MongoDB does on a document it cannot take (a duplicate key, a
// document over 16 MB). It is a validator on the test database, set from here,
// so production code carries no switch for it.
func (a *testApp) refuseToStore(subject string) {
	a.t.Helper()
	a.setEmailsValidator(bson.M{"subject": bson.M{"$ne": subject}})
}

// storeEverythingAgain lifts refuseToStore.
func (a *testApp) storeEverythingAgain() {
	a.t.Helper()
	a.setEmailsValidator(bson.M{})
}

func (a *testApp) setEmailsValidator(validator bson.M) {
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.h.db.DB.RunCommand(ctx, bson.D{
		{Key: "collMod", Value: a.h.db.Emails().Name()},
		{Key: "validator", Value: validator},
		{Key: "validationLevel", Value: "strict"},
		{Key: "validationAction", Value: "error"},
	}).Err(); err != nil {
		a.fatalf("réglage du refus d'enregistrement sur la base de test : %v", err)
	}
}

// ---------------------------------------------------------------------------
// TLS: a certificate the test process trusts, and production code does not know
// ---------------------------------------------------------------------------

var (
	// imapServerTLS is what every fake IMAP server presents, and every fake
	// sending server of acceptance_smtp_test.go: both live on 127.0.0.1.
	imapServerTLS *tls.Config
	// imapTLSErr is why imapServerTLS is missing, reported by the first
	// scenario that needs a server rather than by every scenario.
	imapTLSErr error
)

// trustIMAPTestCertificate generates a throwaway CA and a certificate for
// 127.0.0.1 signed by it, and makes this process trust the CA through
// SSL_CERT_FILE. It must run before anything in the process verifies a
// certificate: on Linux Go reads the system roots once, on first use. TestMain
// calls it first for that reason. The returned function removes the files.
//
// The file holds this CA only. Nothing in a scenario may reach a real TLS
// service, so not trusting the usual ones in this process costs nothing.
func trustIMAPTestCertificate() (cleanup func()) {
	dir, err := os.MkdirTemp("", "mailsorter-scenarios-ca-")
	if err != nil {
		imapTLSErr = fmt.Errorf("dossier temporaire pour le certificat de test : %w", err)
		return func() {}
	}
	cleanup = func() { _ = os.RemoveAll(dir) }

	now := time.Now()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		imapTLSErr = fmt.Errorf("clé de l'autorité de test : %w", err)
		return cleanup
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Mailsorter scenarios CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		imapTLSErr = fmt.Errorf("certificat de l'autorité de test : %w", err)
		return cleanup
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		imapTLSErr = fmt.Errorf("lecture du certificat de l'autorité de test : %w", err)
		return cleanup
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		imapTLSErr = fmt.Errorf("clé du serveur IMAP de test : %w", err)
		return cleanup
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		DNSNames:     []string{"localhost"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		imapTLSErr = fmt.Errorf("certificat du serveur IMAP de test : %w", err)
		return cleanup
	}

	caFile := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600); err != nil {
		imapTLSErr = fmt.Errorf("écriture du certificat de l'autorité de test : %w", err)
		return cleanup
	}
	if err := os.Setenv("SSL_CERT_FILE", caFile); err != nil {
		imapTLSErr = fmt.Errorf("SSL_CERT_FILE : %w", err)
		return cleanup
	}

	imapServerTLS = &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER}, PrivateKey: leafKey}},
		MinVersion:   tls.VersionTLS12,
	}
	return cleanup
}

// ---------------------------------------------------------------------------
// People and their mailboxes
// ---------------------------------------------------------------------------

// imapPerson is one Mailsorter user whose mailbox is reached over IMAP.
type imapPerson struct {
	app     *testApp
	email   string
	token   string
	mailbox *imapmemserver.User
	port    int
	// sending is the sending server next to the IMAP one, nil until
	// withSendingServer (acceptance_smtp_test.go) gives the person one.
	sending *fakeSMTP
}

// imapPerson signs a person up the way POST /api/auth/mailbox leaves them: a
// users row, a mail_accounts row with the app password sealed by the handler's
// own sealSecret, and a session. The row is written here rather than through
// the connect route because that route only accepts the hosts of the provider
// catalog, and this person's provider is a server on 127.0.0.1.
func (a *testApp) imapPerson(email string) *imapPerson {
	a.t.Helper()
	if imapTLSErr != nil || imapServerTLS == nil {
		a.fatalf("pas de serveur IMAP de test possible : %v", imapTLSErr)
	}
	p := &imapPerson{app: a, email: email}
	p.startServer()

	sealed, err := a.h.sealSecret(imapTestPassword)
	if err != nil {
		a.fatalf("chiffrement du mot de passe d'application de %s : %v", email, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	now := time.Now()
	if _, err := a.h.db.MailAccounts().InsertOne(ctx, models.MailAccount{
		UserID:    email,
		Provider:  provider.Detect(email).Key,
		Transport: string(provider.TransportIMAP),
		Username:  email,
		Secret:    sealed,
		Host:      "127.0.0.1",
		Port:      p.port,
		TLS:       string(provider.TLSImplicit),
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		a.fatalf("enregistrement de la boîte IMAP de %s : %v", email, err)
	}
	if _, err := a.h.db.Users().InsertOne(ctx, bson.M{
		"email": email, "createdAt": now, "updatedAt": now,
	}); err != nil {
		a.fatalf("création du compte de %s : %v", email, err)
	}
	p.token = a.h.auth.IssueSession(email)
	return p
}

// The folders every person's server has, by the names a mail program shows.
// The in-memory server drops the special use startServer asks for (go-imap
// v2.0.0-beta.8 ignores CreateOptions.SpecialUse), so, as on a server without
// the SPECIAL-USE extension, Mailsorter knows the last two by these names.
const (
	serverInbox   = "INBOX"
	serverArchive = "Archive"
	serverTrash   = "Trash"
)

// The other special folders a real mailbox comes with, by the names a mail
// program shows. Only usualSpecialFolders makes them, and the server says
// which is which by these names alone, as it does for the archive and the
// trash.
const (
	serverSent   = "Sent"
	serverDrafts = "Drafts"
	serverJunk   = "Junk"
)

// usualSpecialFolders gives this person's server the special folders a real
// mailbox has besides the inbox, the archive and the trash: sent mail, drafts
// and junk, empty. Made on the server itself, as the provider makes them, not
// by a mail program. For the sentences about which folders are the person's
// own: a server with only an inbox, an archive and a trash cannot tell a list
// that leaves out every special folder from one that leaves out those three.
func (p *imapPerson) usualSpecialFolders() {
	a := p.app
	a.t.Helper()
	for _, f := range []struct {
		name string
		use  imapv2.MailboxAttr
	}{
		{serverSent, imapv2.MailboxAttrSent},
		{serverDrafts, imapv2.MailboxAttrDrafts},
		{serverJunk, imapv2.MailboxAttrJunk},
	} {
		if err := p.mailbox.Create(f.name, &imapv2.CreateOptions{SpecialUse: []imapv2.MailboxAttr{f.use}}); err != nil {
			a.fatalf("création du dossier %q sur le serveur IMAP de %s : %v", f.name, p.email, err)
		}
	}
}

// startServer runs this person's own IMAP server, over implicit TLS. Without
// InsecureAuth, so the server itself refuses a login that did not come over TLS.
func (p *imapPerson) startServer() {
	a := p.app
	a.t.Helper()
	p.mailbox = imapmemserver.NewUser(p.email, imapTestPassword)
	for name, use := range map[string][]imapv2.MailboxAttr{
		serverInbox:   nil,
		serverArchive: {imapv2.MailboxAttrArchive},
		serverTrash:   {imapv2.MailboxAttrTrash},
	} {
		var opts *imapv2.CreateOptions
		if len(use) > 0 {
			opts = &imapv2.CreateOptions{SpecialUse: use}
		}
		if err := p.mailbox.Create(name, opts); err != nil {
			a.fatalf("création du dossier %q sur le serveur IMAP de %s : %v", name, p.email, err)
		}
	}
	mem := imapmemserver.New()
	mem.AddUser(p.mailbox)

	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		Caps: imapv2.CapSet{imapv2.CapIMAP4rev1: {}, imapv2.CapIMAP4rev2: {}},
	})
	ln, err := tls.Listen("tcp", "127.0.0.1:0", imapServerTLS)
	if err != nil {
		a.fatalf("démarrage du serveur IMAP de %s : %v", p.email, err)
	}
	go func() { _ = srv.Serve(ln) }()
	a.t.Cleanup(func() { _ = srv.Close() })
	p.port = ln.Addr().(*net.TCPAddr).Port
}

// receive delivers mails to this person's IMAP inbox, unread, oldest first,
// and returns the UIDs their server gave them: the numbers Mailsorter knows
// them by. It does not sync.
func (p *imapPerson) receive(mails ...incoming) []string {
	a := p.app
	a.t.Helper()
	uids := make([]string, 0, len(mails))
	base := time.Now().Add(-time.Duration(len(mails)+1) * time.Minute)
	for i, m := range mails {
		subject := m.Subject
		if subject == "" {
			subject = fmt.Sprintf("Message %d", i+1)
		}
		body := m.Body
		if body == "" {
			body = "Bonjour, ceci est le message " + subject + "."
		}
		date := base.Add(time.Duration(i) * time.Minute)

		from := m.From
		if addr, err := mail.ParseAddress(m.From); err == nil {
			from = addr.String() // RFC 2047 for an accented name
		}
		var raw strings.Builder
		fmt.Fprintf(&raw, "From: %s\r\n", from)
		fmt.Fprintf(&raw, "To: %s\r\n", p.email)
		fmt.Fprintf(&raw, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
		fmt.Fprintf(&raw, "Date: %s\r\n", date.Format(time.RFC1123Z))
		fmt.Fprintf(&raw, "Message-ID: <%d.%d@scenarios.test>\r\n", date.UnixNano(), i)
		if m.Unsubscribe != "" {
			fmt.Fprintf(&raw, "List-Unsubscribe: %s\r\n", m.Unsubscribe)
		}
		if m.UnsubscribePost != "" {
			fmt.Fprintf(&raw, "List-Unsubscribe-Post: %s\r\n", m.UnsubscribePost)
		}
		raw.WriteString("MIME-Version: 1.0\r\n")
		writeMailContent(&raw, m, body)

		data, err := p.mailbox.Append("INBOX", bytes.NewReader([]byte(raw.String())), &imapv2.AppendOptions{Time: date})
		if err != nil {
			a.fatalf("dépôt du mail %q dans la boîte IMAP de %s : %v", subject, p.email, err)
		}
		uids = append(uids, fmt.Sprint(uint32(data.UID)))
	}
	return uids
}

// The boundaries of the multipart mails receive writes. "=_" cannot occur in
// base64, and the texts of a scenario never hold it.
const (
	mixedBoundary       = "=_mailsorter_mixed"
	alternativeBoundary = "=_mailsorter_alternative"
)

// writeMailContent writes the mail's Content-Type and everything after it:
// its text alone (text/plain), the way receive always wrote it, or, with an
// HTML version, both versions (multipart/alternative), and with an
// attachment, those first and then the file (multipart/mixed, the file with
// Content-Disposition: attachment), which is how a mail program sends one.
func writeMailContent(raw *strings.Builder, m incoming, body string) {
	if m.Attachment == nil {
		writeMailText(raw, m, body)
		return
	}
	f := m.Attachment
	fmt.Fprintf(raw, "Content-Type: multipart/mixed; boundary=\"%s\"\r\n\r\n", mixedBoundary)
	fmt.Fprintf(raw, "--%s\r\n", mixedBoundary)
	writeMailText(raw, m, body)
	fmt.Fprintf(raw, "--%s\r\n", mixedBoundary)
	fmt.Fprintf(raw, "Content-Type: %s; name=\"%s\"\r\n", f.Type, f.Name)
	fmt.Fprintf(raw, "Content-Disposition: attachment; filename=\"%s\"\r\n", f.Name)
	raw.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	// Lines of 76 characters, as RFC 2045 asks of base64.
	encoded := base64.StdEncoding.EncodeToString(fileContent(*f))
	for len(encoded) > 76 {
		raw.WriteString(encoded[:76] + "\r\n")
		encoded = encoded[76:]
	}
	raw.WriteString(encoded + "\r\n")
	fmt.Fprintf(raw, "--%s--\r\n", mixedBoundary)
}

// writeMailText writes the text of the mail, headers of its part included:
// text/plain, or multipart/alternative when it has an HTML version.
func writeMailText(raw *strings.Builder, m incoming, body string) {
	if m.HTML == "" {
		raw.WriteString("Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
		raw.WriteString(body + "\r\n")
		return
	}
	fmt.Fprintf(raw, "Content-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n", alternativeBoundary)
	fmt.Fprintf(raw, "--%s\r\n", alternativeBoundary)
	raw.WriteString("Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
	raw.WriteString(body + "\r\n")
	fmt.Fprintf(raw, "--%s\r\n", alternativeBoundary)
	raw.WriteString("Content-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
	raw.WriteString(m.HTML + "\r\n")
	fmt.Fprintf(raw, "--%s--\r\n", alternativeBoundary)
}

// fileContent is the filler an attached file holds: Size bytes of text, a
// sentence naming the file over and over.
func fileContent(f attachedFile) []byte {
	if f.Size <= 0 {
		return nil
	}
	pattern := []byte("Contenu du fichier " + f.Name + ". ")
	return bytes.Repeat(pattern, f.Size/len(pattern)+1)[:f.Size]
}

// expectNumbered checks what the scenario takes for granted about the
// server's numbering. A scenario about two mails numbered 1 proves nothing if
// the server numbered them otherwise.
func (p *imapPerson) expectNumbered(uid, want string) {
	p.app.t.Helper()
	if uid != want {
		p.app.fatalf("le serveur IMAP de %s a numéroté le mail %s, pas %s : le scénario ne prouve rien (à corriger dans acceptance_imap_test.go)", p.email, uid, want)
	}
}

// syncAnswer presses "Synchroniser" as this person and returns the answer,
// whatever it is.
func (p *imapPerson) syncAnswer() (int, map[string]interface{}) {
	p.app.t.Helper()
	return p.app.callAs(p.token, http.MethodPost, "/api/emails/sync", nil)
}

// sync presses "Synchroniser" and expects it to succeed.
func (p *imapPerson) sync() {
	p.app.t.Helper()
	if code, out := p.syncAnswer(); code != http.StatusOK {
		p.app.fatalf("\"Synchroniser\" a répondu %d pour %s (attendu 200) : %v", code, p.email, out)
	}
}

// shownMail is one row of the inbox screen. IsRead is what the row shows as
// read or "non lu" (FLAG_PATCH in Inbox.js patches the same field). IsStarred
// and LabelIDs are the two fields the row reads its star from (see starred).
type shownMail struct {
	MessageID string   `json:"messageId"`
	From      string   `json:"from"`
	Subject   string   `json:"subject"`
	IsRead    bool     `json:"isRead"`
	IsStarred *bool    `json:"isStarred"`
	LabelIDs  []string `json:"labelIds"`
}

// starred is whether the row shows the star "Favori", read the way both
// themes read it (the row in Inbox.js, HotelRow in
// components/hotel/InboxBits.js): isStarred, or the STARRED label among
// labelIds.
func (m shownMail) starred() bool {
	if m.IsStarred != nil && *m.IsStarred {
		return true
	}
	for _, l := range m.LabelIDs {
		if l == labelStarred {
			return true
		}
	}
	return false
}

// inbox is what this person's inbox screen shows: the listing it loads, with
// the query and page size the SPA sends (DEFAULT_QUERY in EmailContext.js).
func (p *imapPerson) inbox() []shownMail {
	a := p.app
	a.t.Helper()
	code, out := a.callAs(p.token, http.MethodGet, "/api/emails?q=in%3Ainbox&maxResults=100", nil)
	if code != http.StatusOK {
		a.fatalf("la boîte de réception de %s ne s'affiche pas : GET /api/emails a répondu %d : %v", p.email, code, out)
	}
	raw, err := json.Marshal(out["emails"])
	if err != nil {
		a.fatalf("réponse de GET /api/emails illisible pour %s : %v", p.email, err)
	}
	var mails []shownMail
	if err := json.Unmarshal(raw, &mails); err != nil {
		a.fatalf("réponse de GET /api/emails illisible pour %s : %v", p.email, err)
	}
	return mails
}

// wholeInbox is everything this person's inbox screen lists once scrolled to
// the end: the first page inbox loads, then "Charger plus" (loadMoreEmails in
// EmailContext.js, 100 at a time) for as long as the listing hands back a next
// page. A sentence about a mailbox of more than 100 mails is about every page,
// and the first one alone would miss its oldest mails.
func (p *imapPerson) wholeInbox() []shownMail {
	a := p.app
	a.t.Helper()
	var all []shownMail
	token := ""
	for page := 1; ; page++ {
		// A bound, so a listing that hands back the same token forever stops
		// the scenario instead of hanging it.
		if page > 50 {
			a.fatalf("la boîte de réception de %s propose encore « Charger plus » après 50 pages : la liste ne finit pas", p.email)
		}
		path := "/api/emails?q=in%3Ainbox&maxResults=100"
		if token != "" {
			path += "&pageToken=" + url.QueryEscape(token)
		}
		code, out := a.callAs(p.token, http.MethodGet, path, nil)
		if code != http.StatusOK {
			a.fatalf("la page %d de la boîte de réception de %s ne s'affiche pas : GET /api/emails a répondu %d : %v", page, p.email, code, out)
		}
		raw, err := json.Marshal(out["emails"])
		if err != nil {
			a.fatalf("réponse de GET /api/emails illisible pour %s : %v", p.email, err)
		}
		var mails []shownMail
		if err := json.Unmarshal(raw, &mails); err != nil {
			a.fatalf("réponse de GET /api/emails illisible pour %s : %v", p.email, err)
		}
		all = append(all, mails...)
		token, _ = out["nextPageToken"].(string)
		if token == "" {
			return all
		}
	}
}

// quickFilter is one of the filters above the inbox: its label, and the query
// the inbox loads when it is clicked (QUICK_FILTERS in Inbox.js).
type quickFilter struct {
	label string
	query string
}

var (
	favoritesFilter   = quickFilter{label: "Favoris", query: "in:inbox is:starred"}
	attachmentsFilter = quickFilter{label: "Pièces jointes", query: "in:inbox has:attachment"}
	largeMailFilter   = quickFilter{label: "Volumineux", query: "in:inbox larger:5M"}
)

// underFilter is everything this person's inbox shows once the filter is
// clicked and the list scrolled to the end: the first page the filter loads,
// then "Charger plus" for as long as the listing hands back a next page, with
// the page size the SPA asks for (fetchData and loadMoreEmails in
// EmailContext.js, 100 at a time). A filter that does not display stops the
// scenario: there is then nothing on the screen to look at.
func (p *imapPerson) underFilter(f quickFilter) []shownMail {
	a := p.app
	a.t.Helper()
	var all []shownMail
	token := ""
	for page := 1; ; page++ {
		// A bound, so a listing that hands back the same token forever stops
		// the scenario instead of hanging it.
		if page > 50 {
			a.fatalf("le filtre « %s » de %s propose encore « Charger plus » après 50 pages : la liste ne finit pas", f.label, p.email)
		}
		path := "/api/emails?q=" + url.QueryEscape(f.query) + "&maxResults=100"
		if token != "" {
			path += "&pageToken=" + url.QueryEscape(token)
		}
		code, out := a.callAs(p.token, http.MethodGet, path, nil)
		if code != http.StatusOK {
			a.fatalf("le filtre « %s » de %s ne s'affiche pas (page %d) : GET /api/emails avec q=%q a répondu %d : %v", f.label, p.email, page, f.query, code, out)
		}
		raw, err := json.Marshal(out["emails"])
		if err != nil {
			a.fatalf("réponse du filtre « %s » illisible pour %s : %v", f.label, p.email, err)
		}
		var mails []shownMail
		if err := json.Unmarshal(raw, &mails); err != nil {
			a.fatalf("réponse du filtre « %s » illisible pour %s : %v", f.label, p.email, err)
		}
		all = append(all, mails...)
		token, _ = out["nextPageToken"].(string)
		if token == "" {
			return all
		}
	}
}

// expectFilterShows checks what the filter shows, every page: each mail of
// want, by its subject, once, and no other. left gives, for a mail the filter
// must leave out, why it must, for the failure message. when says at which
// moment the filter is read.
func (p *imapPerson) expectFilterShows(f quickFilter, when string, want []string, left map[string]string) {
	a := p.app
	a.t.Helper()
	mails := p.underFilter(f)
	count := map[string]int{}
	for _, m := range mails {
		count[m.Subject]++
	}
	wanted := map[string]bool{}
	for _, subject := range want {
		wanted[subject] = true
		switch n := count[subject]; {
		case n == 0:
			a.failf("%s, le filtre « %s » de %s devrait montrer le mail %q, il ne le montre pas (il montre : %s)", when, f.label, p.email, subject, describeInbox(mails))
		case n > 1:
			a.failf("%s, le filtre « %s » de %s devrait montrer le mail %q une fois, il le montre %d fois", when, f.label, p.email, subject, n)
		}
	}
	for _, m := range mails {
		if wanted[m.Subject] {
			continue
		}
		if why, ok := left[m.Subject]; ok {
			a.failf("%s, le filtre « %s » de %s ne devrait pas montrer le mail n°%s %q (%s), il le montre", when, f.label, p.email, m.MessageID, m.Subject, why)
			continue
		}
		a.failf("%s, le filtre « %s » de %s ne devrait montrer que %s, il montre aussi le mail n°%s %q", when, f.label, p.email, quotedList(want), m.MessageID, m.Subject)
	}
}

// quotedList is subjects in a failure message: each quoted, comma separated.
func quotedList(subjects []string) string {
	if len(subjects) == 0 {
		return "aucun mail"
	}
	parts := make([]string, 0, len(subjects))
	for _, s := range subjects {
		parts = append(parts, fmt.Sprintf("%q", s))
	}
	return strings.Join(parts, ", ")
}

// openMail is opening a mail in the reader, as this person: the reader loads
// it with markRead (getEmail in services/api.js), which is what makes a mail
// read in Mailsorter. It expects the mail to open.
func (p *imapPerson) openMail(uid string) {
	a := p.app
	a.t.Helper()
	code, out := a.callAs(p.token, http.MethodGet, "/api/emails/"+url.PathEscape(uid)+"?markRead=1", nil)
	if code != http.StatusOK {
		a.fatalf("le mail n°%s de %s ne s'ouvre pas dans la liseuse : GET /api/emails/%s a répondu %d : %v", uid, p.email, uid, code, out)
	}
}

// counters is what the counters above this person's inbox show: "Boîte de
// réception" and "Non lus", the two STAT_CARDS of Inbox.js an IMAP mailbox
// gets (the other two are Gmail only), as GET /api/stats hands them over.
func (p *imapPerson) counters() (inbox, unread int) {
	a := p.app
	a.t.Helper()
	code, out := a.callAs(p.token, http.MethodGet, "/api/stats", nil)
	if code != http.StatusOK {
		a.fatalf("les compteurs de la boîte de %s ne s'affichent pas : GET /api/stats a répondu %d : %v", p.email, code, out)
	}
	return a.number(out, "inboxCount"), a.number(out, "unreadCount")
}

// shownRow returns the row of the inbox screen for the mail with this number,
// and whether the screen shows one.
func (p *imapPerson) shownRow(uid string) (shownMail, bool) {
	p.app.t.Helper()
	for _, m := range p.inbox() {
		if m.MessageID == uid {
			return m, true
		}
	}
	return shownMail{}, false
}

func describeInbox(mails []shownMail) string {
	if len(mails) == 0 {
		return "aucun mail"
	}
	parts := make([]string, 0, len(mails))
	for _, m := range mails {
		parts = append(parts, fmt.Sprintf("n°%s %q", m.MessageID, m.Subject))
	}
	return strings.Join(parts, ", ")
}

// shows reports whether the inbox screen lists a mail with this subject.
func (p *imapPerson) shows(subject string) bool {
	p.app.t.Helper()
	for _, m := range p.inbox() {
		if m.Subject == subject {
			return true
		}
	}
	return false
}

// expectShows checks that the inbox screen lists the mail with this number,
// under this subject.
func (p *imapPerson) expectShows(uid, subject string) {
	a := p.app
	a.t.Helper()
	mails := p.inbox()
	for _, m := range mails {
		if m.MessageID != uid {
			continue
		}
		if m.Subject != subject {
			a.failf("dans la boîte de réception de %s, le mail n°%s devrait avoir pour sujet %q, il a %q", p.email, uid, subject, m.Subject)
		}
		return
	}
	a.failf("la boîte de réception de %s devrait montrer son mail n°%s %q, elle ne le montre pas (elle montre : %s)", p.email, uid, subject, describeInbox(mails))
}

// expectDoesNotShow checks that the inbox screen lists no mail with this
// subject.
func (p *imapPerson) expectDoesNotShow(subject, why string) {
	a := p.app
	a.t.Helper()
	for _, m := range p.inbox() {
		if m.Subject == subject {
			a.failf("la boîte de réception de %s ne devrait pas montrer le mail %q (%s), elle le montre sous le n°%s", p.email, subject, why, m.MessageID)
		}
	}
}

// sendersTab is this person's Expéditeurs tab: one card per sender, as
// GET /api/senders lists them, each with the count of mails it shows.
func (p *imapPerson) sendersTab() []senderCard {
	a := p.app
	a.t.Helper()
	code, out := a.callAs(p.token, http.MethodGet, "/api/senders", nil)
	if code != http.StatusOK {
		a.fatalf("l'onglet Expéditeurs de %s ne s'affiche pas : GET /api/senders a répondu %d : %v", p.email, code, out)
	}
	raw, _ := out["_raw"].(string)
	var cards []senderCard
	if err := json.Unmarshal([]byte(raw), &cards); err != nil {
		a.fatalf("réponse de GET /api/senders illisible pour %s : %v", p.email, err)
	}
	return cards
}

// senderCardOf returns the card of the Expéditeurs tab whose sender has this
// address, and whether the tab shows one. By the address, because a card
// holds the sender as the mail wrote it, name included ("Boutique"
// <info@boutique.com>), and which way the IMAP listing quotes that name is
// not what a sentence about the tab is about.
func (p *imapPerson) senderCardOf(addr string) (senderCard, bool) {
	p.app.t.Helper()
	for _, c := range p.sendersTab() {
		got := c.SenderEmail
		if parsed, err := mail.ParseAddress(got); err == nil {
			got = parsed.Address
		}
		if strings.EqualFold(got, addr) {
			return c, true
		}
	}
	return senderCard{}, false
}

// describeSenders is the Expéditeurs tab in a failure message.
func describeSenders(cards []senderCard) string {
	if len(cards) == 0 {
		return "aucun expéditeur"
	}
	parts := make([]string, 0, len(cards))
	for _, c := range cards {
		parts = append(parts, fmt.Sprintf("%s (%d mail(s))", c.SenderEmail, c.EmailCount))
	}
	return strings.Join(parts, ", ")
}

// expectSenderListed checks that the Expéditeurs tab shows this sender.
func (p *imapPerson) expectSenderListed(addr string) {
	a := p.app
	a.t.Helper()
	if _, ok := p.senderCardOf(addr); !ok {
		a.failf("l'onglet Expéditeurs de %s devrait montrer %s, il ne le montre pas (il montre : %s)", p.email, addr, describeSenders(p.sendersTab()))
	}
}

// expectSenderNotListed checks that the Expéditeurs tab no longer shows this
// sender. why says why it should have left.
func (p *imapPerson) expectSenderNotListed(addr, why string) {
	a := p.app
	a.t.Helper()
	if c, ok := p.senderCardOf(addr); ok {
		a.failf("l'onglet Expéditeurs de %s ne devrait plus montrer %s (%s), il le montre encore, avec %d mail(s)", p.email, addr, why, c.EmailCount)
	}
}

// ---------------------------------------------------------------------------
// A selection, a whole sender, and the auto-pilot
//
// The same screen actions as the helpers of acceptance_harness_test.go, as
// this person: app.batchAction, app.archiveAllFrom and the others act as the
// Gmail user of the scenario, whose session this person does not hold.
// ---------------------------------------------------------------------------

// onSelection is a button of the action bar on a selection of mails, as this
// person (runBulk in Inbox.js): one POST /api/emails/batch-action with the
// numbers the selected rows hold and the verb of the button ("archive",
// "delete", "read"). It returns the answer, whatever it is.
func (p *imapPerson) onSelection(action string, uids ...string) answer {
	p.app.t.Helper()
	code, out := p.app.callAs(p.token, http.MethodPost, "/api/emails/batch-action",
		map[string]interface{}{"messageIds": uids, "action": action})
	return answer{code: code, body: out}
}

// actOnSelection is onSelection, expecting the action to go through: a
// sentence about what a selection action leaves behind proves nothing if the
// action was refused. It returns the body of the answer.
func (p *imapPerson) actOnSelection(action string, uids ...string) map[string]interface{} {
	a := p.app
	a.t.Helper()
	got := p.onSelection(action, uids...)
	if got.code != http.StatusOK {
		a.fatalf("l'action %q sur la sélection des mails n°%s de %s a répondu %s (attendu 200)", action, strings.Join(uids, ", n°"), p.email, got)
	}
	return got.body
}

// fileSelectionInto is "Classer" on a selection, as this person, once a
// folder is picked (setLabelPickerOpen then runBulk in Inbox.js):
// POST /api/emails/batch-action with the "label" verb and the folder's name.
// It returns the answer, whatever it is.
func (p *imapPerson) fileSelectionInto(folder string, uids ...string) answer {
	p.app.t.Helper()
	code, out := p.app.callAs(p.token, http.MethodPost, "/api/emails/batch-action",
		map[string]interface{}{"messageIds": uids, "action": "label", "labelName": folder})
	return answer{code: code, body: out}
}

// undoFilingInto is "Annuler" in the message that follows "Classer" on a
// selection: POST /api/emails/batch-undo with the numbers the action gave as
// done, the "label" verb and the folder's name, which runBulk sends back too
// (batchUndo in services/api.js). It returns the answer, whatever it is.
func (p *imapPerson) undoFilingInto(folder string, uids ...string) answer {
	p.app.t.Helper()
	code, out := p.app.callAs(p.token, http.MethodPost, "/api/emails/batch-undo",
		map[string]interface{}{"messageIds": uids, "action": "label", "labelName": folder})
	return answer{code: code, body: out}
}

// doneInSelection is the numbers an answer of the action bar gives as done
// ("applied"): the rows the screen counts as treated and takes off the list,
// and the numbers its "Annuler" sends back.
func doneInSelection(body map[string]interface{}) []string {
	list, _ := body["applied"].([]interface{})
	out := make([]string, 0, len(list))
	for _, v := range list {
		if id, ok := v.(string); ok {
			out = append(out, id)
		}
	}
	return out
}

// expectDoneInSelection checks that the answer of the action bar gives each
// of these mails as done: a mail it leaves out is one the screen announces
// as not treated, and one "Annuler" never sends back. what names the action.
func (p *imapPerson) expectDoneInSelection(body map[string]interface{}, what string, uids ...string) {
	a := p.app
	a.t.Helper()
	done := map[string]bool{}
	for _, id := range doneInSelection(body) {
		done[id] = true
	}
	for _, uid := range uids {
		if !done[uid] {
			a.failf("%s devrait compter le mail n°%s de %s parmi les mails traités, la réponse ne le compte pas (traités : n°%s)", what, uid, p.email, strings.Join(doneInSelection(body), ", n°"))
		}
	}
}

// undoSelection is "Annuler" in the message that follows an action on a
// selection (runBulk in Inbox.js): POST /api/emails/batch-undo with the
// numbers the action gave as done and the verb of the action. It returns the
// answer, whatever it is.
func (p *imapPerson) undoSelection(action string, uids ...string) answer {
	p.app.t.Helper()
	code, out := p.app.callAs(p.token, http.MethodPost, "/api/emails/batch-undo",
		map[string]interface{}{"messageIds": uids, "action": action})
	return answer{code: code, body: out}
}

// requireSenderCard returns the card of this person's Expéditeurs tab whose
// sender has this address, and stops the scenario when the tab shows none:
// there is then no button to press on it.
func (p *imapPerson) requireSenderCard(addr string) senderCard {
	a := p.app
	a.t.Helper()
	card, ok := p.senderCardOf(addr)
	if !ok {
		a.fatalf("l'onglet Expéditeurs de %s devrait montrer %s, il ne le montre pas (il montre : %s) : le scénario ne prouve rien", p.email, addr, describeSenders(p.sendersTab()))
	}
	return card
}

// archiveAllFrom is "Tout archiver" on the card of the Expéditeurs tab whose
// sender has this address, as this person (handleApplyBulk in Inbox.js): the
// sender as the card holds it, and the archive action. It expects the action
// to go through and returns the body of the answer.
func (p *imapPerson) archiveAllFrom(addr string) map[string]interface{} {
	a := p.app
	a.t.Helper()
	card := p.requireSenderCard(addr)
	code, out := a.callAs(p.token, http.MethodPost, "/api/ai/apply-bulk",
		map[string]string{"senderEmail": card.SenderEmail, "action": "archive"})
	if code != http.StatusOK {
		a.fatalf("« Tout archiver » pour %s de %s a répondu %d (attendu 200) : %v", addr, p.email, code, out)
	}
	return out
}

// unsubscribeFrom is "Se désabonner" on this mail, as this person
// (handleUnsubscribe in Inbox.js), with "et archiver ses mails" when
// alsoArchive. It expects the unsubscribe to go through and returns the body
// of the answer.
func (p *imapPerson) unsubscribeFrom(uid string, alsoArchive bool) map[string]interface{} {
	a := p.app
	a.t.Helper()
	code, out := a.callAs(p.token, http.MethodPost, "/api/unsubscribe",
		map[string]interface{}{"messageId": uid, "alsoArchive": alsoArchive})
	if code != http.StatusOK {
		a.fatalf("« Se désabonner » sur le mail n°%s de %s a répondu %d (attendu 200) : %v", uid, p.email, code, out)
	}
	return out
}

// autopilot stores what the Expéditeurs tab saves when this person turns on
// "Auto-pilote" for the sender of the card with this address: the sender as
// the card holds it, the action, autoApply on. Written to the database, as
// app.autopilot does for the Gmail user: the toggle only updates a preference
// the analysis of a sender created, and that analysis is not what these
// sentences are about. The card is read from the tab, so the mails must be
// synced first: the IMAP listing writes the sender in its own way (with the
// name in quotes), and a preference keyed otherwise would match no mail.
func (p *imapPerson) autopilot(addr, action string) {
	p.app.t.Helper()
	p.storeAutopilot(addr, action, "")
}

// autopilotFiling is autopilot for a sender whose saved action is to file
// its mails in folder: what "Analyser" on the card saves when the model
// proposes to classify them ("label", with the name), once "Auto-pilote" is
// turned on.
func (p *imapPerson) autopilotFiling(addr, folder string) {
	p.app.t.Helper()
	p.storeAutopilot(addr, "label", folder)
}

// storeAutopilot writes the preference autopilot and autopilotFiling
// describe: label is the name of the folder of a "label" action, empty for
// any other.
func (p *imapPerson) storeAutopilot(addr, action, label string) {
	a := p.app
	a.t.Helper()
	card := p.requireSenderCard(addr)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	now := time.Now()
	if _, err := a.h.db.SenderPreferences().InsertOne(ctx, models.SenderPreference{
		UserID:        p.email,
		SenderEmail:   card.SenderEmail,
		SenderDomain:  extractDomain(card.SenderEmail),
		SenderName:    extractSenderName(card.SenderEmail),
		AutoApply:     true,
		DefaultAction: action,
		DefaultLabel:  label,
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		a.fatalf("activation du pilote automatique de %s pour %s : %v", p.email, addr, err)
	}
}

// act is a button of an inbox row, as this person: the single-message action
// (POST /api/emails/action), with the verb the row sends ("archive" for
// "Archiver", "delete" for "Supprimer"). It expects the action to go through:
// a sentence about what the screen shows after an archive proves nothing if
// nothing was archived.
func (p *imapPerson) act(uid, action string) {
	a := p.app
	a.t.Helper()
	code, out := a.callAs(p.token, http.MethodPost, "/api/emails/action", map[string]string{"messageId": uid, "action": action})
	if code != http.StatusOK {
		a.fatalf("l'action %q sur le mail n°%s de %s a répondu %d (attendu 200) : %v", action, uid, p.email, code, out)
	}
}

// actAnswer is act, returning the answer whatever it is: for the sentences
// about what that answer is.
func (p *imapPerson) actAnswer(uid, action string) answer {
	p.app.t.Helper()
	code, out := p.app.callAs(p.token, http.MethodPost, "/api/emails/action", map[string]string{"messageId": uid, "action": action})
	return answer{code: code, body: out}
}

// protect is "Protéger" in the reader, as this person (handleProtect in
// Inbox.js): POST /api/protected with the sender as the mail shows it, name
// included, which the server reduces to the address. testApp.protect does the
// same for the person on Gmail, who is not this one. It stops the scenario
// when the server does not take it: a sentence about a protected sender proves
// nothing about a sender that is not.
func (p *imapPerson) protect(from string) {
	a := p.app
	a.t.Helper()
	code, out := a.callAs(p.token, http.MethodPost, "/api/protected", map[string]string{"value": from})
	if code != http.StatusCreated && code != http.StatusOK {
		a.fatalf("« Protéger » l'expéditeur %q de %s a répondu %d (attendu 201) : %v : le scénario ne prouve rien", from, p.email, code, out)
	}
}

// actTwiceAtOnce is a row's button clicked twice before the first answer
// comes back (a double click, or the same mail in two tabs): two requests of
// the single-message action, released at the same instant. It returns both
// answers, whatever they are.
func (p *imapPerson) actTwiceAtOnce(uid, action string) [2]answer {
	p.app.t.Helper()
	return p.twiceAtOnce("/api/emails/action", map[string]string{"messageId": uid, "action": action},
		fmt.Sprintf("de l'action %q sur le mail n°%s", action, uid))
}

// undoFromHistoryTwiceAtOnce is "Annuler" on a line of the Historique screen
// confirmed twice before the first answer comes back (a double click, or the
// history open in two tabs): two POST /api/activity/undo on the same line,
// released at the same instant. It returns both answers, whatever they are.
func (p *imapPerson) undoFromHistoryTwiceAtOnce(line historyLine) [2]answer {
	p.app.t.Helper()
	return p.twiceAtOnce("/api/activity/undo", map[string]string{"id": line.ID},
		fmt.Sprintf("de « Annuler » sur la ligne %q du mail n°%s", line.Action, line.MessageID))
}

// twiceAtOnce sends the same POST twice as this person, both requests
// released at the same instant, and returns both answers, whatever they are.
// what names the request in a failure.
//
// The two requests run on goroutines of their own, so they cannot go through
// callAs, which stops the scenario on a network error: only the test's own
// goroutine may do that. A network error is collected and reported from here,
// once both have come back.
func (p *imapPerson) twiceAtOnce(path string, body interface{}, what string) [2]answer {
	a := p.app
	a.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		a.fatalf("encodage %s : %v", what, err)
	}
	var (
		got  [2]answer
		errs [2]error
		wg   sync.WaitGroup
	)
	start := make(chan struct{})
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req, err := http.NewRequest(http.MethodPost, a.server.URL+path, bytes.NewReader(raw))
			if err != nil {
				errs[i] = err
				return
			}
			req.Header.Set("Authorization", "Bearer "+p.token)
			req.Header.Set("Content-Type", "application/json")
			<-start
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				errs[i] = err
				return
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			var out map[string]interface{}
			_ = json.Unmarshal(body, &out)
			if out == nil && len(body) > 0 {
				out = map[string]interface{}{"_raw": string(body)}
			}
			got[i] = answer{code: resp.StatusCode, body: out}
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			a.fatalf("la requête n°%d %s de %s n'a pas abouti : %v", i+1, what, p.email, err)
		}
	}
	return got
}

// ---------------------------------------------------------------------------
// The same mailbox, from another mail program
// ---------------------------------------------------------------------------

// otherProgram opens this person's mailbox the way another mail program does
// (the one on their phone, a webmail): its own IMAP connection, over TLS, with
// the same app password, and nothing of Mailsorter in between. Whatever it
// changes, Mailsorter can only learn from the server, which is what the
// sentences about a mailbox used elsewhere are about. The caller closes it.
func (p *imapPerson) otherProgram() *imapclient.Client {
	a := p.app
	a.t.Helper()
	c, err := imapclient.DialTLS(net.JoinHostPort("127.0.0.1", fmt.Sprint(p.port)), &imapclient.Options{
		TLSConfig: &tls.Config{ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12},
	})
	if err != nil {
		a.fatalf("un autre logiciel de messagerie n'arrive pas à joindre le serveur IMAP de %s : %v", p.email, err)
	}
	if err := c.Login(p.email, imapTestPassword).Wait(); err != nil {
		_ = c.Close()
		a.fatalf("un autre logiciel de messagerie n'arrive pas à ouvrir la boîte IMAP de %s : %v", p.email, err)
	}
	return c
}

// serverUID reads back a number receive returned.
func (p *imapPerson) serverUID(uid string) imapv2.UID {
	p.app.t.Helper()
	n, err := strconv.ParseUint(uid, 10, 32)
	if err != nil || n == 0 {
		p.app.fatalf("%q n'est pas un numéro de mail IMAP (à corriger dans le scénario)", uid)
	}
	return imapv2.UID(n)
}

// openInboxElsewhere is another mail program with this person's inbox open,
// ready to change it.
func (p *imapPerson) openInboxElsewhere() *imapclient.Client {
	a := p.app
	a.t.Helper()
	c := p.otherProgram()
	if _, err := c.Select(serverInbox, nil).Wait(); err != nil {
		_ = c.Close()
		a.fatalf("un autre logiciel de messagerie n'arrive pas à ouvrir la boîte de réception IMAP de %s : %v", p.email, err)
	}
	return c
}

// expectGoneElsewhere checks, on the connection that just acted, that the
// server's inbox no longer holds this mail. A MOVE or an EXPUNGE of a number
// the folder does not hold answers OK and does nothing, so without this a
// scenario built on a wrong number would prove nothing and say nothing.
func (p *imapPerson) expectGoneElsewhere(c *imapclient.Client, uid, what string) {
	a := p.app
	a.t.Helper()
	found, err := c.UIDSearch(&imapv2.SearchCriteria{UID: []imapv2.UIDSet{imapv2.UIDSetNum(p.serverUID(uid))}}, nil).Wait()
	if err != nil {
		a.fatalf("relecture de la boîte de réception IMAP de %s, une fois qu'%s : %v", p.email, what, err)
	}
	if len(found.AllUIDs()) > 0 {
		a.fatalf("le mail n°%s est encore dans la boîte de réception du serveur IMAP de %s alors qu'%s : le scénario ne prouve rien (à corriger dans acceptance_imap_test.go)", uid, p.email, what)
	}
}

// movedElsewhere is another mail program moving this mail out of the inbox
// into folder: the person archived it on their phone. It does not sync.
func (p *imapPerson) movedElsewhere(uid, folder string) {
	a := p.app
	a.t.Helper()
	c := p.openInboxElsewhere()
	defer c.Close()
	if _, err := c.Move(imapv2.UIDSetNum(p.serverUID(uid)), folder).Wait(); err != nil {
		a.fatalf("un autre logiciel de messagerie n'a pas pu déplacer le mail n°%s de %s vers %s : %v", uid, p.email, folder, err)
	}
	p.expectGoneElsewhere(c, uid, "un autre logiciel l'a déplacé vers "+folder)
}

// deletedElsewhere is another mail program deleting this mail for good:
// marked deleted and expunged at once, as a program set to empty the trash as
// it deletes does. It does not sync.
func (p *imapPerson) deletedElsewhere(uid string) {
	a := p.app
	a.t.Helper()
	c := p.openInboxElsewhere()
	defer c.Close()
	deleted := &imapv2.StoreFlags{Op: imapv2.StoreFlagsAdd, Silent: true, Flags: []imapv2.Flag{imapv2.FlagDeleted}}
	if err := c.Store(imapv2.UIDSetNum(p.serverUID(uid)), deleted, nil).Close(); err != nil {
		a.fatalf("un autre logiciel de messagerie n'a pas pu marquer supprimé le mail n°%s de %s : %v", uid, p.email, err)
	}
	if err := c.Expunge().Close(); err != nil {
		a.fatalf("un autre logiciel de messagerie n'a pas pu effacer le mail n°%s de %s : %v", uid, p.email, err)
	}
	p.expectGoneElsewhere(c, uid, "un autre logiciel l'a supprimé définitivement")
}

// flaggedElsewhere is another mail program putting this mail of the inbox in
// its favorites: the person starred it on their phone, which sets \Flagged on
// the server. It does not sync.
func (p *imapPerson) flaggedElsewhere(uid string) {
	p.app.t.Helper()
	p.flagElsewhere(uid, imapv2.FlagFlagged, "mettre en favori", "en favori")
}

// readElsewhere is another mail program marking this mail of the inbox read:
// the person read it on their phone, which sets \Seen on the server. It does
// not sync.
func (p *imapPerson) readElsewhere(uid string) {
	p.app.t.Helper()
	p.flagElsewhere(uid, imapv2.FlagSeen, "marquer lu", "lu")
}

// flagElsewhere sets this flag on this mail of the inbox from another mail
// program, then reads it back on the same connection: a STORE on a number the
// folder does not hold answers OK and does nothing, so without the check a
// scenario built on a wrong number would prove nothing and say nothing. change
// and state name the change and the state it leaves in a failure.
func (p *imapPerson) flagElsewhere(uid string, flag imapv2.Flag, change, state string) {
	a := p.app
	a.t.Helper()
	c := p.openInboxElsewhere()
	defer c.Close()
	set := p.serverUID(uid)
	add := &imapv2.StoreFlags{Op: imapv2.StoreFlagsAdd, Silent: true, Flags: []imapv2.Flag{flag}}
	if err := c.Store(imapv2.UIDSetNum(set), add, nil).Close(); err != nil {
		a.fatalf("un autre logiciel de messagerie n'a pas pu %s le mail n°%s de %s : %v", change, uid, p.email, err)
	}
	msgs, err := c.Fetch(imapv2.UIDSetNum(set), &imapv2.FetchOptions{UID: true, Flags: true}).Collect()
	if err != nil {
		a.fatalf("relecture des drapeaux du mail n°%s de %s, qu'un autre logiciel vient de %s : %v", uid, p.email, change, err)
	}
	for _, m := range msgs {
		if m.UID != set {
			continue
		}
		for _, f := range m.Flags {
			if f == flag {
				return
			}
		}
	}
	a.fatalf("le mail n°%s de la boîte de réception du serveur IMAP de %s n'est pas %s alors qu'un autre logiciel vient de le %s : le scénario ne prouve rien (à corriger dans acceptance_imap_test.go)", uid, p.email, state, change)
}

// openFolderElsewhere is another mail program with this folder of the
// person's mailbox open, ready to change it.
func (p *imapPerson) openFolderElsewhere(folder string) *imapclient.Client {
	a := p.app
	a.t.Helper()
	c := p.otherProgram()
	if _, err := c.Select(folder, nil).Wait(); err != nil {
		_ = c.Close()
		a.fatalf("un autre logiciel de messagerie n'arrive pas à ouvrir le dossier %s du serveur IMAP de %s : %v", folder, p.email, err)
	}
	return c
}

// numberElsewhere is the number of the mail with this subject in the folder
// open on c. By its subject, because a mail Mailsorter moved has a number in
// its new folder that nobody told the scenario. A folder without that mail
// stops the scenario: another program acting on a mail that is not there
// proves nothing.
func (p *imapPerson) numberElsewhere(c *imapclient.Client, folder, subject string) imapv2.UID {
	a := p.app
	a.t.Helper()
	for _, m := range p.mailsIn(c, folder) {
		if m.subject == subject {
			return m.uid
		}
	}
	a.fatalf("le dossier %s du serveur IMAP de %s ne contient pas le mail %q : le scénario ne prouve rien (à corriger dans le scénario)", folder, p.email, subject)
	return 0
}

// expectLeftElsewhere checks, on the connection that just acted, that the
// folder no longer holds this mail, for the reason expectGoneElsewhere gives.
func (p *imapPerson) expectLeftElsewhere(c *imapclient.Client, folder, subject, what string) {
	a := p.app
	a.t.Helper()
	for _, m := range p.mailsIn(c, folder) {
		if m.subject == subject {
			a.fatalf("le mail %q est encore dans le dossier %s du serveur IMAP de %s alors qu'%s : le scénario ne prouve rien (à corriger dans acceptance_imap_test.go)", subject, folder, p.email, what)
		}
	}
}

// newFolderElsewhere is another mail program creating a folder of the
// person's own, the way they make one on their phone to file mail in.
func (p *imapPerson) newFolderElsewhere(name string) {
	a := p.app
	a.t.Helper()
	c := p.otherProgram()
	defer c.Close()
	if err := c.Create(name, nil).Wait(); err != nil {
		a.fatalf("un autre logiciel de messagerie n'a pas pu créer le dossier %s sur le serveur IMAP de %s : %v", name, p.email, err)
	}
}

// folders is every folder of this person's server, by its full name
// ("Projets/Maison" for a folder inside another), sorted, as another mail
// program lists them (LIST "" "*").
func (p *imapPerson) folders() []string {
	a := p.app
	a.t.Helper()
	c := p.otherProgram()
	defer c.Close()
	listed, err := c.List("", "*", nil).Collect()
	if err != nil {
		a.fatalf("lecture des dossiers du serveur IMAP de %s : %v", p.email, err)
	}
	names := make([]string, 0, len(listed))
	for _, l := range listed {
		names = append(names, l.Mailbox)
	}
	sort.Strings(names)
	return names
}

// hasFolder reports whether this person's server has a folder by this full
// name, as another mail program lists it.
func (p *imapPerson) hasFolder(name string) bool {
	p.app.t.Helper()
	for _, f := range p.folders() {
		if f == name {
			return true
		}
	}
	return false
}

// movedFromElsewhere is another mail program moving the mail with this
// subject out of folder into to: the person filed it elsewhere on their
// phone. It does not sync.
func (p *imapPerson) movedFromElsewhere(folder, subject, to string) {
	a := p.app
	a.t.Helper()
	c := p.openFolderElsewhere(folder)
	defer c.Close()
	uid := p.numberElsewhere(c, folder, subject)
	if _, err := c.Move(imapv2.UIDSetNum(uid), to).Wait(); err != nil {
		a.fatalf("un autre logiciel de messagerie n'a pas pu déplacer le mail %q de %s du dossier %s vers %s : %v", subject, p.email, folder, to, err)
	}
	p.expectLeftElsewhere(c, folder, subject, "un autre logiciel l'a déplacé vers "+to)
}

// erasedFromElsewhere is another mail program deleting the mail with this
// subject from folder for good, as deletedElsewhere does in the inbox. It
// does not sync.
func (p *imapPerson) erasedFromElsewhere(folder, subject string) {
	a := p.app
	a.t.Helper()
	c := p.openFolderElsewhere(folder)
	defer c.Close()
	uid := p.numberElsewhere(c, folder, subject)
	deleted := &imapv2.StoreFlags{Op: imapv2.StoreFlagsAdd, Silent: true, Flags: []imapv2.Flag{imapv2.FlagDeleted}}
	if err := c.Store(imapv2.UIDSetNum(uid), deleted, nil).Close(); err != nil {
		a.fatalf("un autre logiciel de messagerie n'a pas pu marquer supprimé le mail %q du dossier %s de %s : %v", subject, folder, p.email, err)
	}
	if err := c.Expunge().Close(); err != nil {
		a.fatalf("un autre logiciel de messagerie n'a pas pu effacer le mail %q du dossier %s de %s : %v", subject, folder, p.email, err)
	}
	p.expectLeftElsewhere(c, folder, subject, "un autre logiciel l'a supprimé définitivement")
}

// serverMail is one mail of a folder, as another mail program lists it.
type serverMail struct {
	uid     imapv2.UID
	subject string
}

// mailsIn lists the folder open on c, read from the envelopes, so listing
// marks nothing read.
func (p *imapPerson) mailsIn(c *imapclient.Client, folder string) []serverMail {
	a := p.app
	a.t.Helper()
	found, err := c.UIDSearch(&imapv2.SearchCriteria{}, nil).Wait()
	if err != nil {
		a.fatalf("lecture du dossier %s du serveur IMAP de %s : %v", folder, p.email, err)
	}
	uids := found.AllUIDs()
	if len(uids) == 0 {
		return nil
	}
	msgs, err := c.Fetch(imapv2.UIDSetNum(uids...), &imapv2.FetchOptions{UID: true, Envelope: true}).Collect()
	if err != nil {
		a.fatalf("lecture du dossier %s du serveur IMAP de %s : %v", folder, p.email, err)
	}
	mails := make([]serverMail, 0, len(msgs))
	for _, m := range msgs {
		if m.Envelope != nil {
			mails = append(mails, serverMail{uid: m.UID, subject: m.Envelope.Subject})
		}
	}
	return mails
}

// onServer returns the subjects of the mails in this folder of the person's
// server, as another mail program lists them. Opened read-only and read from
// the envelopes, so looking marks nothing read.
func (p *imapPerson) onServer(folder string) []string {
	a := p.app
	a.t.Helper()
	c := p.otherProgram()
	defer c.Close()
	if _, err := c.Select(folder, &imapv2.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		a.fatalf("le dossier %s du serveur IMAP de %s ne s'ouvre pas : %v", folder, p.email, err)
	}
	mails := p.mailsIn(c, folder)
	if len(mails) == 0 {
		return nil
	}
	subjects := make([]string, 0, len(mails))
	for _, m := range mails {
		subjects = append(subjects, m.subject)
	}
	return subjects
}

// seenOnServer says whether the mail with this subject in this folder of the
// person's server is read there (the \Seen flag), as another mail program
// shows it.
func (p *imapPerson) seenOnServer(folder, subject string) bool {
	p.app.t.Helper()
	return p.hasFlagOnServer(folder, subject, imapv2.FlagSeen)
}

// flaggedOnServer says whether the mail with this subject in this folder of
// the person's server is a favorite there (the \Flagged flag, which mail
// programs show as a star or a flag), as another mail program shows it.
func (p *imapPerson) flaggedOnServer(folder, subject string) bool {
	p.app.t.Helper()
	return p.hasFlagOnServer(folder, subject, imapv2.FlagFlagged)
}

// hasFlagOnServer says whether the mail with this subject in this folder of
// the person's server carries this flag, as another mail program shows it.
// Opened read-only and read from the flags, so looking marks nothing read. A
// folder without that mail stops the scenario: a flag read on nothing proves
// nothing.
func (p *imapPerson) hasFlagOnServer(folder, subject string, flag imapv2.Flag) bool {
	a := p.app
	a.t.Helper()
	c := p.otherProgram()
	defer c.Close()
	if _, err := c.Select(folder, &imapv2.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		a.fatalf("le dossier %s du serveur IMAP de %s ne s'ouvre pas : %v", folder, p.email, err)
	}
	uid := p.numberElsewhere(c, folder, subject)
	msgs, err := c.Fetch(imapv2.UIDSetNum(uid), &imapv2.FetchOptions{UID: true, Flags: true}).Collect()
	if err != nil {
		a.fatalf("lecture des drapeaux du mail %q dans le dossier %s du serveur IMAP de %s : %v", subject, folder, p.email, err)
	}
	for _, m := range msgs {
		if m.UID != uid {
			continue
		}
		for _, f := range m.Flags {
			if f == flag {
				return true
			}
		}
		return false
	}
	a.fatalf("le serveur IMAP de %s ne donne pas les drapeaux du mail %q du dossier %s : le scénario ne prouve rien (à corriger dans acceptance_imap_test.go)", p.email, subject, folder)
	return false
}

// sizeOnServer is the size in bytes of the mail with this subject in this
// folder of the person's server (RFC822.SIZE), as another mail program reads
// it. Opened read-only. A folder without that mail stops the scenario.
func (p *imapPerson) sizeOnServer(folder, subject string) int64 {
	a := p.app
	a.t.Helper()
	c := p.otherProgram()
	defer c.Close()
	if _, err := c.Select(folder, &imapv2.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		a.fatalf("le dossier %s du serveur IMAP de %s ne s'ouvre pas : %v", folder, p.email, err)
	}
	uid := p.numberElsewhere(c, folder, subject)
	msgs, err := c.Fetch(imapv2.UIDSetNum(uid), &imapv2.FetchOptions{UID: true, RFC822Size: true}).Collect()
	if err != nil {
		a.fatalf("lecture de la taille du mail %q dans le dossier %s du serveur IMAP de %s : %v", subject, folder, p.email, err)
	}
	for _, m := range msgs {
		if m.UID == uid {
			return m.RFC822Size
		}
	}
	a.fatalf("le serveur IMAP de %s ne donne pas la taille du mail %q du dossier %s : le scénario ne prouve rien (à corriger dans acceptance_imap_test.go)", p.email, subject, folder)
	return 0
}

// expectOnServer checks that this folder of the person's server holds a mail
// with this subject: where the mail went, seen from outside Mailsorter.
func (p *imapPerson) expectOnServer(folder, subject string) {
	a := p.app
	a.t.Helper()
	subjects := p.onServer(folder)
	for _, s := range subjects {
		if s == subject {
			return
		}
	}
	held := "aucun mail"
	if len(subjects) > 0 {
		held = fmt.Sprintf("%q", subjects)
	}
	a.failf("le mail %q devrait être dans le dossier %s du serveur IMAP de %s, il n'y est pas (le dossier contient : %s)", subject, folder, p.email, held)
}

// expectNotOnServer checks that this folder of the person's server no longer
// holds a mail with this subject. why says why it should have left.
func (p *imapPerson) expectNotOnServer(folder, subject, why string) {
	a := p.app
	a.t.Helper()
	for _, s := range p.onServer(folder) {
		if s == subject {
			a.failf("le mail %q ne devrait plus être dans le dossier %s du serveur IMAP de %s (%s), il y est encore", subject, folder, p.email, why)
			return
		}
	}
}

// requireOnServer is expectOnServer for what a scenario starts from: when the
// folder does not hold the mail, the scenario stops there, since undoing an
// action that moved nothing proves nothing. what names that action.
func (p *imapPerson) requireOnServer(folder, subject, what string) {
	a := p.app
	a.t.Helper()
	for _, s := range p.onServer(folder) {
		if s == subject {
			return
		}
	}
	a.fatalf("le mail %q devrait être dans le dossier %s du serveur IMAP de %s, puisqu'%s : il n'y est pas, le scénario ne prouve rien", subject, folder, p.email, what)
}

// expectBackInInbox checks that the inbox screen lists the mail with this
// subject again, and once. By its subject rather than its number: a mail that
// comes back to an IMAP inbox gets a new number there from the server. when
// says at which moment the screen is read.
func (p *imapPerson) expectBackInInbox(subject, when string) {
	a := p.app
	a.t.Helper()
	mails := p.inbox()
	var numbers []string
	for _, m := range mails {
		if m.Subject == subject {
			numbers = append(numbers, m.MessageID)
		}
	}
	switch {
	case len(numbers) == 0:
		a.failf("%s, la boîte de réception de %s devrait montrer de nouveau le mail %q, elle ne le montre pas (elle montre : %s)", when, p.email, subject, describeInbox(mails))
	case len(numbers) > 1:
		a.failf("%s, la boîte de réception de %s devrait montrer le mail %q une fois, elle le montre %d fois (n°%s)", when, p.email, subject, len(numbers), strings.Join(numbers, ", n°"))
	}
}

// ---------------------------------------------------------------------------
// The AI's triage plan, and the history, as this person sees them
// ---------------------------------------------------------------------------

// analyze is "Trier avec l'IA" on these mails, as this person, and expects the
// analysis to go through: a scenario about what happens to a proposal proves
// nothing if the AI made none.
func (p *imapPerson) analyze(uids ...string) {
	a := p.app
	a.t.Helper()
	code, out := a.callAs(p.token, http.MethodPost, "/api/ai/analyze", map[string]interface{}{"emailIds": uids})
	if code != http.StatusOK {
		a.fatalf("l'analyse par l'IA des mails n°%s de %s a répondu %d (attendu 200) : %v", strings.Join(uids, ", n°"), p.email, code, out)
	}
}

// analyzeAnswer is "Analyser" on these mails, as this person, returning the
// answer whatever it is: for a mail the analysis may rightly turn down.
func (p *imapPerson) analyzeAnswer(uids ...string) answer {
	p.app.t.Helper()
	code, out := p.app.callAs(p.token, http.MethodPost, "/api/ai/analyze", map[string]interface{}{"emailIds": uids})
	return answer{code: code, body: out}
}

// plan is the AI's triage plan as this person's inbox loads it
// (GET /api/ai/suggestions): the proposals still waiting for an answer. It is
// what the plan shows again after a refresh, so a proposal missing from it is
// one the person can no longer apply or ignore.
func (p *imapPerson) plan() []models.AISuggestion {
	a := p.app
	a.t.Helper()
	code, out := a.callAs(p.token, http.MethodGet, "/api/ai/suggestions", nil)
	if code != http.StatusOK {
		a.fatalf("le plan de l'IA de %s ne s'affiche pas : GET /api/ai/suggestions a répondu %d : %v", p.email, code, out)
	}
	raw, _ := out["_raw"].(string)
	var plan []models.AISuggestion
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		a.fatalf("réponse de GET /api/ai/suggestions illisible pour %s : %v", p.email, err)
	}
	return plan
}

// inPlan reports whether the plan still shows this proposal.
func (p *imapPerson) inPlan(id string) bool {
	p.app.t.Helper()
	for _, s := range p.plan() {
		if s.ID == id {
			return true
		}
	}
	return false
}

// proposalFor returns the proposal the plan shows for this mail, and fails
// the scenario when it shows none.
func (p *imapPerson) proposalFor(uid string) models.AISuggestion {
	a := p.app
	a.t.Helper()
	for _, s := range p.plan() {
		if s.EmailID == uid {
			return s
		}
	}
	a.fatalf("le plan de l'IA de %s ne propose rien pour le mail n°%s : le scénario ne prouve rien", p.email, uid)
	return models.AISuggestion{}
}

// proposalStatus is what the database holds for a proposal ("pending",
// "applied", "rejected"). Only for a failure message: it says why the plan no
// longer shows a proposal.
func (p *imapPerson) proposalStatus(id string) string {
	a := p.app
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return fmt.Sprintf("identifiant %q illisible", id)
	}
	var s models.AISuggestion
	if err := a.h.db.AISuggestions().FindOne(ctx, bson.M{"userId": p.email, "_id": oid}).Decode(&s); err != nil {
		return fmt.Sprintf("introuvable (%v)", err)
	}
	return s.Status
}

// applyProposal is "Appliquer" on one proposal of the plan, as this person,
// and returns the answer, whatever it is.
func (p *imapPerson) applyProposal(id string) answer {
	p.app.t.Helper()
	code, out := p.app.callAs(p.token, http.MethodPost, "/api/ai/apply", map[string]string{"suggestionId": id})
	return answer{code: code, body: out}
}

// tidyAll is "Tout ranger" on these proposals of the plan, as this person, and
// returns the answer, whatever it is.
func (p *imapPerson) tidyAll(ids ...string) answer {
	p.app.t.Helper()
	code, out := p.app.callAs(p.token, http.MethodPost, "/api/ai/apply-batch", map[string]interface{}{"suggestionIds": ids})
	return answer{code: code, body: out}
}

// listedFolder is one entry of the list of folders, or labels, Mailsorter
// offers to file a mail into (GET /api/labels): the shape of a Gmail label.
// Every screen that offers one (LabelPicker in Inbox.js, the rules editor)
// shows the name of the entries whose type is "user", and only those.
type listedFolder struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// offeredByScreens reports whether the screens offer this entry to file into.
func (f listedFolder) offeredByScreens() bool {
	return f.Type == "user" && f.Name != ""
}

// foldersToFileInto is the list of folders Mailsorter offers this person to
// file a mail into, every entry as GET /api/labels hands it over. It stops the
// scenario when the list does not load: there is then nothing to look at.
func (p *imapPerson) foldersToFileInto() []listedFolder {
	a := p.app
	a.t.Helper()
	code, out := a.callAs(p.token, http.MethodGet, "/api/labels", nil)
	if code != http.StatusOK {
		a.fatalf("la liste des dossiers où classer de %s ne s'affiche pas : GET /api/labels a répondu %d : %v", p.email, code, out)
	}
	raw, _ := out["_raw"].(string)
	var list []listedFolder
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		a.fatalf("réponse de GET /api/labels illisible pour %s : %v (%v)", p.email, err, out)
	}
	return list
}

// describeFolders is that list in a failure message.
func describeFolders(list []listedFolder) string {
	if len(list) == 0 {
		return "aucun dossier"
	}
	parts := make([]string, 0, len(list))
	for _, f := range list {
		parts = append(parts, fmt.Sprintf("%q (type %q)", f.Name, f.Type))
	}
	return strings.Join(parts, ", ")
}

// history returns this person's action log for one mail, oldest first: what
// their Historique screen lists for it. Scoped to this person, since another
// person's mail may carry the same number.
func (p *imapPerson) history(uid string) []models.ActionLog {
	a := p.app
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cur, err := a.h.db.ActionLog().Find(ctx, bson.M{"userId": p.email, "messageId": uid},
		options.Find().SetSort(bson.D{{Key: "createdAt", Value: 1}, {Key: "_id", Value: 1}}))
	if err != nil {
		a.fatalf("lecture de l'historique de %s : %v", p.email, err)
	}
	var rows []models.ActionLog
	if err := cur.All(ctx, &rows); err != nil {
		a.fatalf("lecture de l'historique de %s : %v", p.email, err)
	}
	return rows
}

// ---------------------------------------------------------------------------
// "Annuler", from the three places that offer it
// ---------------------------------------------------------------------------

// undoRightAfter is "Annuler" in the message that follows a row's "Archiver"
// or "Supprimer" (undoToast in Inbox.js), as this person, on the number the
// mail had in the inbox. It returns the answer, whatever it is.
func (p *imapPerson) undoRightAfter(uid, action string) answer {
	p.app.t.Helper()
	return p.app.undoRightAfterAs(p.token, uid, action)
}

// tidyUndo is one request of the "Annuler" that follows "Tout ranger": the
// mails of one verdict, and what the server answered.
type tidyUndo struct {
	action string
	uids   []string
	got    answer
}

// undoTidy is "Annuler" in the message that follows "Tout ranger" (undo in
// useTriagePlan, TriagePlan.js): one POST /api/emails/batch-undo per verdict,
// with the numbers the mails had in the inbox when they were tidied, and
// nothing for a "keep", which moved nothing. applied are the proposals
// "Tout ranger" applied. It returns each answer, whatever it is.
func (p *imapPerson) undoTidy(applied []models.AISuggestion) []tidyUndo {
	p.app.t.Helper()
	// The client's spelling of a verdict (canonicalAction in ui/actions.js).
	canonical := map[string]string{"trash": "delete", "markRead": "read", "markUnread": "unread"}
	var order []string
	byVerb := map[string]*tidyUndo{}
	labels := map[string]string{}
	for _, s := range applied {
		action := s.Action
		if c, ok := canonical[action]; ok {
			action = c
		}
		if action == "keep" {
			continue
		}
		key := action
		if action == "label" {
			key = action + "|" + s.LabelName
			labels[key] = s.LabelName
		}
		if byVerb[key] == nil {
			byVerb[key] = &tidyUndo{action: action}
			order = append(order, key)
		}
		byVerb[key].uids = append(byVerb[key].uids, s.EmailID)
	}
	out := make([]tidyUndo, 0, len(order))
	for _, key := range order {
		u := byVerb[key]
		code, body := p.app.callAs(p.token, http.MethodPost, "/api/emails/batch-undo",
			map[string]interface{}{"messageIds": u.uids, "action": u.action, "labelName": labels[key]})
		u.got = answer{code: code, body: body}
		out = append(out, *u)
	}
	return out
}

// historyScreen is this person's Historique screen: the first page it loads
// (History.js, 50 lines), newest first.
func (p *imapPerson) historyScreen() []historyLine {
	p.app.t.Helper()
	return p.app.historyScreenAs(p.token, p.email)
}

// historyLineFor returns the newest line of the Historique screen for this
// action on the mail with this number, and stops the scenario when the
// screen shows none: there is then nothing to press "Annuler" on.
func (p *imapPerson) historyLineFor(uid, action string) historyLine {
	a := p.app
	a.t.Helper()
	for _, l := range p.historyScreen() {
		if l.MessageID == uid && l.Action == action {
			return l
		}
	}
	a.fatalf("l'historique de %s ne montre pas de ligne %q pour le mail n°%s : le scénario ne prouve rien", p.email, action, uid)
	return historyLine{}
}

// historyLinesOf returns every line the Historique screen shows for the mail
// with this number, newest first. For the sentences about an action that does
// nothing: the screen must then show no line for it, so the scenario counts
// them where the person reads them, not in the database.
func (p *imapPerson) historyLinesOf(uid string) []historyLine {
	p.app.t.Helper()
	var lines []historyLine
	for _, l := range p.historyScreen() {
		if l.MessageID == uid {
			lines = append(lines, l)
		}
	}
	return lines
}

// historyLineSent is the line of this person's Historique screen with this
// id, every field as GET /api/activity/log hands it over, or nil when the
// screen no longer shows it: for a sentence about what a line says beyond the
// fields historyLine reads.
func (p *imapPerson) historyLineSent(id string) map[string]interface{} {
	a := p.app
	a.t.Helper()
	code, out := a.callAs(p.token, http.MethodGet, "/api/activity/log?limit=50", nil)
	if code != http.StatusOK {
		a.fatalf("l'historique de %s ne s'affiche pas : GET /api/activity/log a répondu %d : %v", p.email, code, out)
	}
	entries, _ := out["entries"].([]interface{})
	for _, e := range entries {
		if line, ok := e.(map[string]interface{}); ok && line["id"] == id {
			return line
		}
	}
	return nil
}

// holdsString reports whether v, a value decoded from JSON, holds the string
// want at any depth, outside the keys in skip (at any depth too).
func holdsString(v interface{}, want string, skip map[string]bool) bool {
	switch t := v.(type) {
	case string:
		return t == want
	case map[string]interface{}:
		for k, inner := range t {
			if !skip[k] && holdsString(inner, want, skip) {
				return true
			}
		}
	case []interface{}:
		for _, inner := range t {
			if holdsString(inner, want, skip) {
				return true
			}
		}
	}
	return false
}

// describeHistory is lines of the Historique screen in a failure message:
// the action and where it came from (the Source* constants in ledger.go).
func describeHistory(lines []historyLine) string {
	if len(lines) == 0 {
		return "aucune ligne"
	}
	parts := make([]string, 0, len(lines))
	for _, l := range lines {
		parts = append(parts, fmt.Sprintf("%q (%s)", l.Action, l.Source))
	}
	return strings.Join(parts, ", ")
}

// undoFromHistory is "Annuler" on a line of the Historique screen (handleUndo
// in History.js), once its confirmation is accepted. It returns the answer,
// whatever it is.
func (p *imapPerson) undoFromHistory(line historyLine) answer {
	p.app.t.Helper()
	code, out := p.app.callAs(p.token, http.MethodPost, "/api/activity/undo", map[string]string{"id": line.ID})
	return answer{code: code, body: out}
}

// expectMarkedUndone checks, on the Historique screen as it loads again, that
// this line shows "Annulé" (undone) or not. what names the line in a failure.
func (p *imapPerson) expectMarkedUndone(line historyLine, undone bool, what string) {
	a := p.app
	a.t.Helper()
	for _, l := range p.historyScreen() {
		if l.ID != line.ID {
			continue
		}
		switch {
		case undone && !l.Undone:
			a.failf("dans l'historique de %s, la ligne %s devrait être marquée annulée, elle ne l'est pas", p.email, what)
		case !undone && l.Undone:
			a.failf("dans l'historique de %s, la ligne %s ne devrait pas être marquée annulée, elle l'est", p.email, what)
		}
		return
	}
	a.failf("l'historique de %s ne montre plus la ligne %s", p.email, what)
}

// expectNoLongerUndoable checks, on this person's Historique screen as it
// loads again, that this line shows "Annulé" and no longer offers "Annuler".
// what names the line in a failure.
func (p *imapPerson) expectNoLongerUndoable(line historyLine, what string) {
	p.app.t.Helper()
	p.app.expectNoLongerUndoableAs(p.token, p.email, line, what)
}

// actionTakenDaysAgo is days going by since the action of this history line:
// the line's date, and the date the stored copy of its mail records for when
// it left the inbox (leftAt, written by mirroredIMAP), both moved that many
// days back, which is what the database holds once that time has passed. The
// app reads no clock a scenario can turn, and a sentence about an action older
// than "Annuler" allows cannot wait thirty days.
//
// The line must exist: moving nothing back would prove nothing. The stored
// copy may not, once the app keeps none for a mail that left the inbox.
func (p *imapPerson) actionTakenDaysAgo(line historyLine, days int) {
	a := p.app
	a.t.Helper()
	oid, err := primitive.ObjectIDFromHex(line.ID)
	if err != nil {
		a.fatalf("la ligne d'historique %q n'a pas un identifiant lisible : %v", line.ID, err)
	}
	then := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := a.h.db.ActionLog().UpdateOne(ctx,
		bson.M{"_id": oid, "userId": p.email},
		bson.M{"$set": bson.M{"createdAt": then}})
	if err != nil {
		a.fatalf("recul de %d jours de la ligne d'historique %q de %s : %v", days, line.Action, p.email, err)
	}
	if res.MatchedCount != 1 {
		a.fatalf("la ligne d'historique %q du mail n°%s de %s est introuvable dans la base : le scénario ne prouve rien (à corriger dans acceptance_imap_test.go)", line.Action, line.MessageID, p.email)
	}
	if _, err := a.h.db.Emails().UpdateMany(ctx,
		bson.M{"userId": p.email, "messageId": line.MessageID, "leftInbox": true},
		bson.M{"$set": bson.M{"leftAt": then}}); err != nil {
		a.fatalf("recul de %d jours de la sortie de la boîte de réception du mail n°%s de %s : %v", days, line.MessageID, p.email, err)
	}
}
