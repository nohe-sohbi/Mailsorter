package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/digest"
	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
	"github.com/nohe-sohbi/mailsorter/backend/internal/smtpsend"
)

// The digest recap and the test send are per-account data: both must be gated,
// and both must exist as routes (a 404 here would mean the Settings buttons
// point at nothing).
func TestDigestRoutesExistAndAreGated(t *testing.T) {
	srv := newRoutedTestServer(t)

	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/stats/digest"},
		{http.MethodPost, "/api/account/digest/test"},
	}
	for _, tc := range cases {
		req, err := http.NewRequest(tc.method, srv.URL+tc.path, nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401", tc.method, tc.path, res.StatusCode)
		}
	}
}

// A test send that cannot reach the datastore must report a failure, never a
// cheerful "sent": the whole point of the button is to tell the user the truth
// about whether delivery works.
func TestSendTestDigestReportsFailure(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/account/digest/test", strings.NewReader(""))
	req.Header.Set("X-User-Email", "user@example.com")
	req = req.WithContext(cancelledContext())
	rec := httptest.NewRecorder()

	h.SendTestDigest(rec, req)

	if rec.Code == http.StatusOK {
		t.Errorf("SendTestDigest with no reachable datastore = 200: %s", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

// What "Envoyer un test" says when the recap did not leave. On IMAP every
// answer is about the mailbox's sending server: the Gmail sentence there sent
// people to a "Compte Gmail" card their screen does not have. And none of
// them is a 401, on which the SPA signs the person out before they read it.
func TestWriteDigestSendErrorAnswersInTheMailboxsWords(t *testing.T) {
	throughBox := func(err error) error { return fmt.Errorf("%w: %w", errSendThroughBox, err) }
	const gmail = "Envoi impossible. Reconnectez Gmail depuis la carte « Compte Gmail » pour réautoriser l'envoi."

	cases := []struct {
		name   string
		err    error
		status int
		want   string
	}{
		{"IMAP, no known sending server", throughBox(provider.ErrNoSendingServer), http.StatusUnprocessableEntity,
			"Mailsorter ne connaît pas le serveur d'envoi de cette boîte : le récap ne peut pas en partir."},
		{"IMAP, app password refused", throughBox(fmt.Errorf("%w: 535", smtpsend.ErrAuth)), http.StatusBadGateway,
			"Le serveur d'envoi de votre boîte a refusé le mot de passe d'application."},
		{"IMAP, sign-in blocked by the provider", throughBox(fmt.Errorf("%w: 534 5.7.14", smtpsend.ErrBlocked)), http.StatusBadGateway,
			"Votre fournisseur a bloqué la connexion venant du serveur de Mailsorter. Autorisez-la depuis la sécurité de votre compte, puis réessayez."},
		{"IMAP, server unreachable", throughBox(fmt.Errorf("%w: dial", smtpsend.ErrUnreachable)), http.StatusBadGateway,
			"Le serveur d'envoi de votre boîte ne répond pas. Réessayez dans quelques minutes."},
		{"IMAP, anything else", throughBox(errors.New("smtpsend: 554 rejected")), http.StatusBadGateway,
			"Le récap n'a pas pu partir du serveur d'envoi de votre boîte. Réessayez dans quelques minutes."},
		{"Gmail API, send refused", errors.New("envoi du digest impossible: 403"), http.StatusBadGateway, gmail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeDigestSendError(rec, tc.err)

			var body struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("writeDigestSendError(%v) body is not JSON: %v: %s", tc.err, err, rec.Body.String())
			}
			if rec.Code != tc.status || body.Error != tc.want {
				t.Errorf("writeDigestSendError(%v) = %d %q, want %d %q", tc.err, rec.Code, body.Error, tc.status, tc.want)
			}
		})
	}
}

// One test send per account per minute: each one is an authenticated session
// with the person's sending server, and a burst of refused passwords is what
// gets the instance's address throttled. The second click inside the minute
// is answered in words, without trying; another account is not held by it.
func TestSendTestDigestWaitsAMinuteBetweenTwoTests(t *testing.T) {
	h := newTestHandler(t)
	send := func(userEmail string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/account/digest/test", nil)
		req.Header.Set("X-User-Email", userEmail)
		req = req.WithContext(cancelledContext())
		rec := httptest.NewRecorder()
		h.SendTestDigest(rec, req)
		return rec
	}

	if first := send("moi@exemple.fr"); first.Code == http.StatusTooManyRequests {
		t.Fatalf("first SendTestDigest = 429, want it to try: %s", first.Body.String())
	}

	second := send("moi@exemple.fr")
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &body); err != nil {
		t.Fatalf("second SendTestDigest body is not JSON: %v: %s", err, second.Body.String())
	}
	if second.Code != http.StatusTooManyRequests || body.Error != digestTestTooSoon {
		t.Errorf("second SendTestDigest within the minute = %d %q, want 429 %q", second.Code, body.Error, digestTestTooSoon)
	}
	if got := second.Header().Get("Retry-After"); got != "60" {
		t.Errorf("Retry-After = %q, want %q", got, "60")
	}

	if other := send("autre@exemple.fr"); other.Code == http.StatusTooManyRequests {
		t.Errorf("SendTestDigest for another account = 429, want it to try: %s", other.Body.String())
	}
}

// The recap leaves with a Message-ID on the domain of the mailbox it leaves
// from, a new one each time.
func TestDigestMessageCarriesAMessageIDOnTheMailboxsDomain(t *testing.T) {
	d := digest.Digest{Subject: "Votre récap", Text: "texte", HTML: "<p>html</p>"}
	now := time.Date(2026, 11, 24, 7, 0, 0, 0, time.UTC)
	shape := regexp.MustCompile(`^<[A-Z2-7]{26}@orange\.fr>$`)

	ids := map[string]bool{}
	for range 2 {
		msg, err := mail.ReadMessage(bytes.NewReader(digestMessage(d, "moi@orange.fr", now)))
		if err != nil {
			t.Fatalf("digestMessage is not a message a mail program reads: %v", err)
		}
		id := msg.Header.Get("Message-ID")
		if !shape.MatchString(id) {
			t.Errorf("digestMessage(moi@orange.fr) Message-ID = %q, want %s", id, shape)
		}
		ids[id] = true
	}
	if len(ids) != 2 {
		t.Errorf("two recaps carry the Message-IDs %v, want two different ones", ids)
	}
}

// lockedLog captures what the package logs while a test runs.
type lockedLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lockedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func captureLog(t *testing.T) *lockedLog {
	t.Helper()
	l := &lockedLog{}
	prev := log.Writer()
	log.SetOutput(l)
	t.Cleanup(func() { log.SetOutput(prev) })
	return l
}

// A sending server that does not answer holds its own account, not the ones
// behind it: while it is held, every other account of the pass gets its recap
// and its stamp.
func TestDigestPassKeepsASlowServerFromHoldingTheOthers(t *testing.T) {
	const slow = "lent@exemple.fr"
	others := []string{"a@exemple.fr", "b@exemple.fr", "c@exemple.fr", "d@exemple.fr", "e@exemple.fr"}
	pace := digestPace{senders: 2, send: time.Minute, stamp: time.Second}

	release := make(chan struct{})
	othersDone := make(chan struct{})
	var mu sync.Mutex
	stamped := map[string]int{}
	send := func(ctx context.Context, email string) {
		if email == slow {
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
	}
	stamp := func(_ context.Context, email string) error {
		mu.Lock()
		defer mu.Unlock()
		stamped[email]++
		if email != slow {
			done := 0
			for _, o := range others {
				done += stamped[o]
			}
			if done == len(others) {
				close(othersDone)
			}
		}
		return nil
	}

	finished := make(chan struct{})
	go func() {
		pace.run(append([]string{slow}, others...), send, stamp)
		close(finished)
	}()

	select {
	case <-othersDone:
	case <-time.After(5 * time.Second):
		mu.Lock()
		got := fmt.Sprint(stamped)
		mu.Unlock()
		close(release)
		t.Fatalf("after 5 s held on %s, the other accounts were stamped %s, want all of %v", slow, got, others)
	}
	mu.Lock()
	if stamped[slow] != 0 {
		t.Errorf("%s was stamped while its send was still held", slow)
	}
	mu.Unlock()

	close(release)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the pass did not return once the slow send was released")
	}
	for _, email := range append([]string{slow}, others...) {
		if stamped[email] != 1 {
			t.Errorf("%s was stamped %d times, want once", email, stamped[email])
		}
	}
}

// Each account has its own deadline: the one after a send that used up its
// budget starts with a whole one. And the stamp after such a send runs under
// its own context, so the account is still recorded as done for the day.
func TestDigestPassGivesEachAccountItsOwnDeadline(t *testing.T) {
	pace := digestPace{senders: 1, send: 50 * time.Millisecond, stamp: time.Second}

	var events []string
	send := func(ctx context.Context, email string) {
		if email == "lent@exemple.fr" {
			<-ctx.Done()
			events = append(events, "send "+email+" ended: "+fmt.Sprint(ctx.Err()))
			return
		}
		events = append(events, "send "+email+" started: "+fmt.Sprint(ctx.Err()))
	}
	stamp := func(ctx context.Context, email string) error {
		events = append(events, "stamp "+email+": "+fmt.Sprint(ctx.Err()))
		return nil
	}

	pace.run([]string{"lent@exemple.fr", "suivant@exemple.fr"}, send, stamp)

	want := []string{
		"send lent@exemple.fr ended: context deadline exceeded",
		"stamp lent@exemple.fr: <nil>",
		"send suivant@exemple.fr started: <nil>",
		"stamp suivant@exemple.fr: <nil>",
	}
	if strings.Join(events, "\n") != strings.Join(want, "\n") {
		t.Errorf("the pass went\n  %s\nwant\n  %s", strings.Join(events, "\n  "), strings.Join(want, "\n  "))
	}
}

// No more recaps leave at once than the pace allows.
func TestDigestPassStaysWithinItsSenders(t *testing.T) {
	pace := digestPace{senders: 3, send: time.Minute, stamp: time.Second}
	emails := []string{"1@x.fr", "2@x.fr", "3@x.fr", "4@x.fr", "5@x.fr", "6@x.fr", "7@x.fr", "8@x.fr"}

	release := make(chan struct{})
	var mu sync.Mutex
	inFlight, most, stamped := 0, 0, 0
	send := func(ctx context.Context, _ string) {
		mu.Lock()
		inFlight++
		most = max(most, inFlight)
		mu.Unlock()
		<-release
		mu.Lock()
		inFlight--
		mu.Unlock()
	}
	stamp := func(context.Context, string) error {
		mu.Lock()
		stamped++
		mu.Unlock()
		return nil
	}

	finished := make(chan struct{})
	go func() {
		pace.run(emails, send, stamp)
		close(finished)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := inFlight
		mu.Unlock()
		if n == pace.senders {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d recaps in flight after 5 s, want %d", n, pace.senders)
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Room for a fourth to start, if the pace let one.
	time.Sleep(50 * time.Millisecond)
	close(release)
	<-finished

	if most != pace.senders {
		t.Errorf("at most %d recaps were in flight, want %d", most, pace.senders)
	}
	if stamped != len(emails) {
		t.Errorf("%d accounts were stamped, want %d", stamped, len(emails))
	}
}

// A stamp that fails is said in the log: it means the recap leaves again on
// the next pass, which nobody would otherwise know why.
func TestDigestPassLogsAStampThatFailed(t *testing.T) {
	logged := captureLog(t)
	pace := digestPace{senders: 1, send: time.Second, stamp: time.Second}

	pace.run([]string{"moi@exemple.fr"},
		func(context.Context, string) {},
		func(context.Context, string) error { return errors.New("mongo is down") })

	if out := logged.String(); !strings.Contains(out, "moi@exemple.fr") || !strings.Contains(out, "mongo is down") {
		t.Errorf("the log says %q, want the account and the error of its stamp", out)
	}
}

// The stamp reports a write that did not happen, rather than drop it.
func TestStampDigestSentReportsItsError(t *testing.T) {
	h := newTestHandler(t)
	if err := h.stampDigestSent(cancelledContext(), "moi@exemple.fr"); err == nil {
		t.Error("stampDigestSent with no reachable datastore = nil, want an error")
	}
}
