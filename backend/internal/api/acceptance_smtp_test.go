//go:build acceptance

package api

// The sending server of an IMAP mailbox: a fake SMTP server, running next to
// the person's IMAP server of acceptance_imap_test.go.
//
// The daily recap is a message Mailsorter writes on its own, and over IMAP it
// leaves through the mailbox's own sending server, with the address and the
// app password the person connected with. So does the unsubscribe mail to a
// list that offers only an address (scenarios_3_imap_senders_test.go). A
// sentence about either is about what reached that server, so the harness
// runs one and keeps what it took, the way the fake Gmail keeps the state of
// a mailbox, rather than trusting what a handler says it sent.
//
// It speaks the part of SMTP a mail program uses to hand over a message (RFC
// 5321, with AUTH PLAIN from RFC 4954), over IMPLICIT TLS on 127.0.0.1, and
// presents the IMAP servers' test certificate, which the process trusts
// through SSL_CERT_FILE (trustIMAPTestCertificate): Mailsorter reaches it
// through the production path, certificate check included, with nothing
// relaxed for the test. Like every provider of the catalog, it takes no
// message before a login with the person's address and app password. A
// command it does not simulate is answered 502 AND recorded, and reading what
// it received fails the scenario, naming the command, as the fake Gmail does
// with a request.
//
// Where Mailsorter finds it: on the person's mail_accounts row, in smtpHost,
// smtpPort and smtpTls, the same shape as host, port and tls for the IMAP
// server. Those are the names connecting a mailbox is to record the sending
// server under. The harness writes them itself because the connect route only
// accepts the catalog's hosts, and this server is on 127.0.0.1.

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/mail"
	"net/textproto"
	"strings"
	"sync"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
	"go.mongodb.org/mongo-driver/bson"
)

// The fields of a mail_accounts row that say where the mailbox sends from.
const (
	rowSMTPHost = "smtpHost"
	rowSMTPPort = "smtpPort"
	rowSMTPTLS  = "smtpTls"
)

// ---------------------------------------------------------------------------
// The server
// ---------------------------------------------------------------------------

// fakeSMTP is one person's sending server.
type fakeSMTP struct {
	owner string // whose server it is, for failure messages
	port  int
	ln    net.Listener
	wg    sync.WaitGroup

	mu sync.Mutex
	// password is the app password it accepts for owner.
	password    string
	closed      bool
	conns       map[net.Conn]struct{}
	connections int
	tlsFailures []string
	logins      []smtpLogin
	received    []smtpMessage
	// unsupported collects every command the server could not simulate.
	// Reading what it received turns a non-empty list into a failure.
	unsupported []string
}

// smtpLogin is one AUTH the server was offered.
type smtpLogin struct {
	user     string
	accepted bool
}

// smtpMessage is one message the server took: who handed it over, the
// envelope, and the message itself.
type smtpMessage struct {
	login    string
	mailFrom string
	rcptTo   []string
	data     []byte
}

// startFakeSMTP runs a sending server for owner that accepts password, over
// implicit TLS like Orange's or Fastmail's (465), and stops it when the
// scenario ends.
func startFakeSMTP(a *testApp, owner, password string) *fakeSMTP {
	a.t.Helper()
	if imapTLSErr != nil || imapServerTLS == nil {
		a.fatalf("pas de serveur d'envoi de test possible : %v", imapTLSErr)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", imapServerTLS)
	if err != nil {
		a.fatalf("démarrage du serveur d'envoi de %s : %v", owner, err)
	}
	s := &fakeSMTP{
		owner:    owner,
		port:     ln.Addr().(*net.TCPAddr).Port,
		ln:       ln,
		password: password,
		conns:    map[net.Conn]struct{}{},
	}
	s.wg.Add(1)
	go s.accept()
	a.t.Cleanup(s.close)
	return s
}

func (s *fakeSMTP) accept() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = c.Close()
			return
		}
		s.conns[c] = struct{}{}
		s.connections++
		s.mu.Unlock()
		s.wg.Add(1)
		go s.serve(c)
	}
}

// close stops the server and every session still open, and waits for them:
// a session left running would write into a scenario that has ended.
func (s *fakeSMTP) close() {
	_ = s.ln.Close()
	s.mu.Lock()
	s.closed = true
	for c := range s.conns {
		_ = c.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *fakeSMTP) record(command string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unsupported = append(s.unsupported, command)
}

// login checks one AUTH, and remembers it: a sentence about a refused password
// must be able to tell a refusal from a server nobody tried.
func (s *fakeSMTP) login(user, password string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	ok := strings.EqualFold(user, s.owner) && password == s.password
	s.logins = append(s.logins, smtpLogin{user: user, accepted: ok})
	return ok
}

// serve is one session, from the greeting to QUIT.
func (s *fakeSMTP) serve(c net.Conn) {
	defer s.wg.Done()
	defer func() {
		_ = c.Close()
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
	}()
	// A bound, so a client that stops talking cannot hold the scenario's
	// cleanup forever.
	_ = c.SetDeadline(time.Now().Add(time.Minute))
	if tc, ok := c.(*tls.Conn); ok {
		if err := tc.Handshake(); err != nil {
			s.mu.Lock()
			s.tlsFailures = append(s.tlsFailures, err.Error())
			s.mu.Unlock()
			return
		}
	}

	tp := textproto.NewConn(c)
	// A failed write ends the session at the next read.
	say := func(line string) { _ = tp.PrintfLine("%s", line) }
	say("220 127.0.0.1 ESMTP serveur d'envoi de test")

	var (
		loggedIn string
		inMail   bool
		from     string
		rcpt     []string
	)
	reset := func() { inMail, from, rcpt = false, "", nil }
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		verb, arg, _ := strings.Cut(line, " ")
		switch strings.ToUpper(verb) {
		case "EHLO":
			reset()
			say("250-127.0.0.1")
			say("250-8BITMIME")
			say("250 AUTH PLAIN")
		case "HELO":
			reset()
			say("250 127.0.0.1")
		case "AUTH":
			mech, initial, _ := strings.Cut(arg, " ")
			if loggedIn != "" {
				say("503 5.5.1 Already authenticated")
				continue
			}
			if !strings.EqualFold(mech, "PLAIN") {
				s.record("AUTH " + mech)
				say("504 5.5.4 Unrecognized authentication type")
				continue
			}
			if initial == "" {
				say("334 ")
				if initial, err = tp.ReadLine(); err != nil {
					return
				}
			}
			if initial == "*" {
				say("501 5.7.0 Authentication cancelled")
				continue
			}
			user, password, ok := decodeAuthPlain(initial)
			if !ok {
				s.record("AUTH PLAIN avec une réponse illisible")
				say("501 5.5.2 Cannot decode response")
				continue
			}
			if !s.login(user, password) {
				say("535 5.7.8 Authentication credentials invalid")
				continue
			}
			loggedIn = user
			say("235 2.7.0 Authentication successful")
		case "MAIL":
			if loggedIn == "" {
				say("530 5.7.0 Authentication required")
				continue
			}
			addr, ok := s.path(line, arg, "FROM:")
			if !ok {
				say("501 5.5.4 Syntax error in MAIL")
				continue
			}
			reset()
			inMail, from = true, addr
			say("250 2.1.0 OK")
		case "RCPT":
			if !inMail {
				say("503 5.5.1 Need MAIL command")
				continue
			}
			addr, ok := s.path(line, arg, "TO:")
			if !ok {
				say("501 5.5.4 Syntax error in RCPT")
				continue
			}
			rcpt = append(rcpt, addr)
			say("250 2.1.5 OK")
		case "DATA":
			if !inMail || len(rcpt) == 0 {
				say("503 5.5.1 Need RCPT command")
				continue
			}
			say("354 End data with <CR><LF>.<CR><LF>")
			data, err := tp.ReadDotBytes()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.received = append(s.received, smtpMessage{login: loggedIn, mailFrom: from, rcptTo: rcpt, data: data})
			s.mu.Unlock()
			reset()
			say("250 2.0.0 OK queued")
		case "RSET":
			reset()
			say("250 2.0.0 OK")
		case "NOOP":
			say("250 2.0.0 OK")
		case "QUIT":
			say("221 2.0.0 Bye")
			return
		case "*":
			// What net/smtp sends after a refused AUTH, to cancel an
			// exchange the server has already ended. Not a command to
			// simulate: the answer a real server gives.
			say("501 5.7.0 Authentication cancelled")
		default:
			s.record(line)
			say("502 5.5.2 Command not recognized")
		}
	}
}

// path reads the address of "MAIL FROM:<a> ..." or "RCPT TO:<a> ...". Of the
// parameters, only BODY (8BITMIME, which the server announces) and SIZE are
// taken: anything else is a command this server does not simulate.
func (s *fakeSMTP) path(line, arg, prefix string) (string, bool) {
	if len(arg) < len(prefix) || !strings.EqualFold(arg[:len(prefix)], prefix) {
		s.record(line)
		return "", false
	}
	rest := strings.TrimSpace(arg[len(prefix):])
	end := strings.Index(rest, ">")
	if !strings.HasPrefix(rest, "<") || end < 0 {
		s.record(line)
		return "", false
	}
	for _, param := range strings.Fields(rest[end+1:]) {
		key, _, _ := strings.Cut(param, "=")
		if k := strings.ToUpper(key); k != "BODY" && k != "SIZE" {
			s.record(line)
			return "", false
		}
	}
	return rest[1:end], true
}

// decodeAuthPlain reads the response of AUTH PLAIN: base64 of
// "authzid NUL user NUL password", the authzid empty or the user itself.
func decodeAuthPlain(response string) (user, password string, ok bool) {
	raw, err := base64.StdEncoding.DecodeString(response)
	if err != nil {
		return "", "", false
	}
	parts := strings.Split(string(raw), "\x00")
	if len(parts) != 3 || (parts[0] != "" && parts[0] != parts[1]) {
		return "", "", false
	}
	return parts[1], parts[2], true
}

// seen says what the server went through, for a failure message about a
// recap that did not arrive, or arrived wrong.
func (s *fakeSMTP) seen() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.connections == 0 {
		return "personne ne s'y est connecté"
	}
	parts := []string{fmt.Sprintf("%d connexion(s)", s.connections)}
	for _, e := range s.tlsFailures {
		parts = append(parts, "une connexion sans TLS valable ("+e+")")
	}
	for _, l := range s.logins {
		if l.accepted {
			parts = append(parts, "connexion acceptée pour "+l.user)
		} else {
			parts = append(parts, "mot de passe refusé pour "+l.user)
		}
	}
	parts = append(parts, fmt.Sprintf("%d message(s) reçu(s)", len(s.received)))
	return strings.Join(parts, ", ")
}

// ---------------------------------------------------------------------------
// A person's sending server
// ---------------------------------------------------------------------------

// withSendingServer gives this person the sending server their provider runs
// next to the IMAP one, accepting their address and app password, and records
// it on their mail_accounts row the way connecting a mailbox is to record it.
func (p *imapPerson) withSendingServer() {
	a := p.app
	a.t.Helper()
	p.sending = startFakeSMTP(a, p.email, imapTestPassword)
	p.updateRow(bson.M{"$set": bson.M{
		rowSMTPHost: "127.0.0.1",
		rowSMTPPort: p.sending.port,
		rowSMTPTLS:  string(provider.TLSImplicit),
	}})
}

// sendingServerRefusesPassword is the provider no longer taking, for sending,
// the app password Mailsorter holds (revoked, or replaced on that side). The
// IMAP server still takes it.
func (p *imapPerson) sendingServerRefusesPassword() {
	s := p.sendingServer()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.password = "un-autre-mot-de-passe-d-application"
}

// noSendingServerOnFile leaves this person's row as every row written before
// Mailsorter recorded a sending server: no smtpHost, smtpPort or smtpTls at
// all. And it checks that the catalog has none to fall back on for their
// provider either: otherwise a sentence about a mailbox whose sending server
// Mailsorter does not know proves nothing.
func (p *imapPerson) noSendingServerOnFile() {
	a := p.app
	a.t.Helper()
	p.updateRow(bson.M{"$unset": bson.M{rowSMTPHost: "", rowSMTPPort: "", rowSMTPTLS: ""}})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var row struct {
		Provider string `bson:"provider"`
	}
	if err := a.h.db.MailAccounts().FindOne(ctx, bson.M{"userId": p.email}).Decode(&row); err != nil {
		a.fatalf("lecture de la boîte IMAP de %s : %v", p.email, err)
	}
	known, err := provider.Lookup(row.Provider)
	if err != nil {
		a.fatalf("le fournisseur %q de la boîte de %s n'est pas au catalogue : le scénario ne prouve rien (à corriger dans acceptance_smtp_test.go)", row.Provider, p.email)
	}
	for _, r := range known.Routes {
		if r.Transport == provider.TransportIMAP && r.SMTP != nil {
			a.fatalf("le catalogue connaît un serveur d'envoi pour le fournisseur %q de %s (%s:%d) : le scénario ne prouve rien (à corriger dans le scénario, avec une adresse d'un fournisseur sans serveur d'envoi au catalogue)", row.Provider, p.email, r.SMTP.Host, r.SMTP.Port)
		}
	}
}

func (p *imapPerson) updateRow(update bson.M) {
	a := p.app
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := a.h.db.MailAccounts().UpdateOne(ctx, bson.M{"userId": p.email}, update)
	if err != nil {
		a.fatalf("mise à jour de la boîte IMAP de %s : %v", p.email, err)
	}
	if res.MatchedCount != 1 {
		a.fatalf("la boîte IMAP de %s est introuvable dans la base : le scénario ne prouve rien (à corriger dans acceptance_smtp_test.go)", p.email)
	}
}

func (p *imapPerson) sendingServer() *fakeSMTP {
	p.app.t.Helper()
	if p.sending == nil {
		p.app.fatalf("%s n'a pas de serveur d'envoi : appelle withSendingServer d'abord (à corriger dans le scénario)", p.email)
	}
	return p.sending
}

// sentMail is one message the sending server took, as the mail program of
// its recipient reads it, with how it was handed over.
type sentMail struct {
	// login is the address the sender logged in with; the server only takes
	// a message after a login with the person's app password.
	login        string
	envelopeFrom string
	envelopeTo   []string
	// from and to are the addresses of the From and To headers.
	from    []string
	to      []string
	subject string
}

func (m sentMail) String() string {
	return fmt.Sprintf("%q de %s à %s (enveloppe de %s à %s, connecté en %s)",
		m.subject, strings.Join(m.from, ", "), strings.Join(m.to, ", "),
		m.envelopeFrom, strings.Join(m.envelopeTo, ", "), m.login)
}

// sentThroughBox is every message this person's sending server took, oldest
// first. It fails the scenario on any command the server could not simulate.
func (p *imapPerson) sentThroughBox() []sentMail {
	a := p.app
	a.t.Helper()
	s := p.sendingServer()
	s.mu.Lock()
	unsupported := append([]string(nil), s.unsupported...)
	received := append([]smtpMessage(nil), s.received...)
	s.mu.Unlock()

	for _, cmd := range unsupported {
		a.failf("le faux serveur d'envoi de %s a reçu une commande qu'il ne sait pas simuler : %q (à ajouter dans acceptance_smtp_test.go)", p.email, cmd)
	}
	out := make([]sentMail, 0, len(received))
	for i, m := range received {
		msg, err := mail.ReadMessage(bytes.NewReader(m.data))
		if err != nil {
			a.failf("le message n°%d reçu par le serveur d'envoi de %s est illisible : %v", i+1, p.email, err)
			continue
		}
		subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
		if err != nil {
			a.failf("le sujet du message n°%d reçu par le serveur d'envoi de %s est illisible : %v", i+1, p.email, err)
		}
		out = append(out, sentMail{
			login:        m.login,
			envelopeFrom: m.mailFrom,
			envelopeTo:   m.rcptTo,
			from:         addressesIn(msg.Header, "From"),
			to:           addressesIn(msg.Header, "To"),
			subject:      subject,
		})
	}
	return out
}

// addressesIn is the bare addresses of one header, or the header as written
// when it does not parse, so a failure shows it.
func addressesIn(h mail.Header, key string) []string {
	list, err := h.AddressList(key)
	if err != nil {
		return []string{fmt.Sprintf("%q illisible", h.Get(key))}
	}
	out := make([]string, 0, len(list))
	for _, addr := range list {
		out = append(out, addr.Address)
	}
	return out
}

// onlyAddress reports whether addrs is this one address and no other.
func onlyAddress(addrs []string, want string) bool {
	return len(addrs) == 1 && strings.EqualFold(addrs[0], want)
}

// expectRefusedLogin checks that this person's sending server was offered
// their address and refused it: the refusal a sentence starts from happened.
func (p *imapPerson) expectRefusedLogin() {
	a := p.app
	a.t.Helper()
	s := p.sendingServer()
	s.mu.Lock()
	logins := append([]smtpLogin(nil), s.logins...)
	s.mu.Unlock()
	for _, l := range logins {
		if strings.EqualFold(l.user, p.email) && !l.accepted {
			return
		}
	}
	a.failf("le serveur d'envoi de %s devrait avoir refusé une connexion avec son adresse et son mot de passe d'application, il n'en a refusé aucune (%s) : la réponse ne vient pas de son refus", p.email, s.seen())
}

// ---------------------------------------------------------------------------
// The recap card of Réglages, and the daily loop
// ---------------------------------------------------------------------------

// chooseRecap is the recap card of Réglages: "Récap quotidien" on, at this
// hour, UTC (persist in Settings.js sends both together). It checks the
// settings come back as chosen: a sentence about the hour proves nothing on
// another one.
func (p *imapPerson) chooseRecap(hour int) {
	a := p.app
	a.t.Helper()
	code, out := a.callAs(p.token, http.MethodPut, "/api/account/settings",
		map[string]interface{}{"digestEnabled": true, "digestHourUTC": hour})
	if code != http.StatusOK {
		a.fatalf("l'activation du récap quotidien de %s à %02d:00 UTC a répondu %d (attendu 200) : %v", p.email, hour, code, out)
	}
	if on, _ := out["digestEnabled"].(bool); !on || a.number(out, "digestHourUTC") != hour {
		a.fatalf("les réglages de %s devraient montrer le récap quotidien activé à %02d:00 UTC, ils montrent %v : le scénario ne prouve rien", p.email, hour, out)
	}
}

// recapSubject is the subject the recap card's "Aperçu" shows
// (GET /api/stats/digest): the recap as it will be sent.
func (p *imapPerson) recapSubject() string {
	a := p.app
	a.t.Helper()
	code, out := a.callAs(p.token, http.MethodGet, "/api/stats/digest", nil)
	if code != http.StatusOK {
		a.fatalf("l'aperçu du récap de %s ne s'affiche pas : GET /api/stats/digest a répondu %d : %v", p.email, code, out)
	}
	subject, _ := out["subject"].(string)
	if subject == "" {
		a.fatalf("l'aperçu du récap de %s n'a pas de sujet : %v", p.email, out)
	}
	return subject
}

// expectSomethingToRecap checks that this week's activity, the one the recap
// sums up (GET /api/stats/activity), is not empty: the daily loop sends
// nothing for an empty week.
func (p *imapPerson) expectSomethingToRecap() {
	a := p.app
	a.t.Helper()
	code, out := a.callAs(p.token, http.MethodGet, "/api/stats/activity", nil)
	if code != http.StatusOK {
		a.fatalf("l'activité de la semaine de %s ne s'affiche pas : GET /api/stats/activity a répondu %d : %v", p.email, code, out)
	}
	if a.number(out, "total") < 1 {
		a.fatalf("le récap de %s n'a rien à résumer cette semaine : le scénario ne prouve rien", p.email)
	}
}

// sendTestRecap is "Envoyer un test" on the recap card
// (POST /api/account/digest/test). It returns the answer, whatever it is.
func (p *imapPerson) sendTestRecap() answer {
	p.app.t.Helper()
	code, out := p.app.callAs(p.token, http.MethodPost, "/api/account/digest/test", nil)
	return answer{code: code, body: out}
}

// expectTestRecapRefused checks what "Envoyer un test" shows when the recap
// cannot leave: the sentence, in the "error" field the card displays (errText
// in Settings.js), with an error status. Not 401: on a 401 the app signs the
// person out (services/api.js) before they read anything.
func (p *imapPerson) expectTestRecapRefused(got answer, sentence string) {
	a := p.app
	a.t.Helper()
	if got.code < 400 || got.code == http.StatusUnauthorized || got.said() != sentence {
		a.failf("« Envoyer un test » du récap de %s devrait répondre %q, en erreur et sans fermer sa session, le serveur a répondu %s", p.email, sentence, got)
	}
}

// recapLoopTicks is one tick of the daily recap loop (sendDueDigests,
// digest_scheduler.go): what its 15 minute ticker does. The harness builds
// the Handler without NewHandler, so the loop never runs on its own; a
// scenario runs it at the moment it chooses, as timeComesForSnoozes does the
// snooze sweep.
func (a *testApp) recapLoopTicks() {
	a.t.Helper()
	a.h.sendDueDigests()
}
