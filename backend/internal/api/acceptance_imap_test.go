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
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"mime"
	"net"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	// imapServerTLS is what every fake IMAP server presents.
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
// Mailsorter finds the last two by their special use, not by these names.
const (
	serverInbox   = "INBOX"
	serverArchive = "Archive"
	serverTrash   = "Trash"
)

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
		raw.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
		raw.WriteString(body + "\r\n")

		data, err := p.mailbox.Append("INBOX", bytes.NewReader([]byte(raw.String())), &imapv2.AppendOptions{Time: date})
		if err != nil {
			a.fatalf("dépôt du mail %q dans la boîte IMAP de %s : %v", subject, p.email, err)
		}
		uids = append(uids, fmt.Sprint(uint32(data.UID)))
	}
	return uids
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

// shownMail is one row of the inbox screen.
type shownMail struct {
	MessageID string `json:"messageId"`
	From      string `json:"from"`
	Subject   string `json:"subject"`
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
