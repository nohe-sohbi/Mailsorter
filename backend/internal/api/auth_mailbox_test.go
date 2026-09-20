package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// signInRequest carries an already-cancelled context, so the datastore call
// behind the handler fails instantly instead of waiting out Mongo's server
// selection timeout. Every assertion here is about what happens BEFORE the
// datastore, which is where this route's guards live.
func signInRequest(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/auth/mailbox", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "203.0.113.7:54321"
	return r.WithContext(cancelledContext())
}

// The route exists so the hosted edition has a first door: before it, the only
// way to obtain a session was Google's OAuth callback, which that edition
// cannot reach at all.
func TestSignInWithMailboxIsReachableWithoutASession(t *testing.T) {
	srv := newRoutedTestServer(t)

	resp, err := http.Post(srv.URL+"/api/auth/mailbox", "application/json",
		bytes.NewBufferString(`{"address":"","password":""}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatal("the sign-in route demanded a session; nobody signing in has one yet")
	}
	// It answers the request on its merits instead: an empty body is a bad one.
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("empty credentials = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestSignInWithMailboxRefusesAnIncompleteBody(t *testing.T) {
	cases := map[string]string{
		"nothing":      `{}`,
		"no password":  `{"address":"someone@orange.fr"}`,
		"no address":   `{"password":"abcd efgh ijkl mnop"}`,
		"blank spaces": `{"address":"   ","password":"x"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			h := newTestHandler(t)
			rec := httptest.NewRecorder()
			h.SignInWithMailbox(rec, signInRequest(body))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
			}
		})
	}
}

// The throttle that makes this route safe to leave public.
//
// It is the one place an unauthenticated caller makes the server try a password
// against somebody else's mail provider. At the global 20 r/s that is a
// credential-stuffing engine running from our IP, and the provider would start
// refusing us for every user.
func TestSignInWithMailboxThrottlesFarBelowTheGlobalLimit(t *testing.T) {
	h := newTestHandler(t)

	var refusedAt int
	for i := 1; i <= signInBurst+3; i++ {
		rec := httptest.NewRecorder()
		h.SignInWithMailbox(rec, signInRequest(`{"address":"a@orange.fr","password":"secret"}`))
		if rec.Code == http.StatusTooManyRequests {
			refusedAt = i
			break
		}
	}
	if refusedAt == 0 {
		t.Fatalf("%d attempts in a row were all accepted; the sign-in throttle is not wired",
			signInBurst+3)
	}
	if refusedAt > signInBurst+1 {
		t.Errorf("first refusal at attempt %d, want at most %d", refusedAt, signInBurst+1)
	}
}

// And the second key. An attacker guessing one account from a thousand
// addresses gets a fresh client bucket every time, so the address needs a
// bucket of its own or the throttle only slows down the clumsy version.
func TestSignInWithMailboxThrottlesPerAddressToo(t *testing.T) {
	h := newTestHandler(t)
	const victim = `{"address":"victim@orange.fr","password":"guess"}`

	for i := 0; i < signInBurst; i++ {
		rec := httptest.NewRecorder()
		r := signInRequest(victim)
		r.RemoteAddr = "198.51.100." + string(rune('1'+i)) + ":40000"
		h.SignInWithMailbox(rec, r)
	}

	// A brand new client, never seen before, going for the same address.
	rec := httptest.NewRecorder()
	fresh := signInRequest(victim)
	fresh.RemoteAddr = "192.0.2.99:40000"
	h.SignInWithMailbox(rec, fresh)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("a new client hammering an exhausted address got %d, want %d: "+
			"the throttle is keyed only on the caller", rec.Code, http.StatusTooManyRequests)
	}
}

// The guard that stops an app password from taking over a Google account.
//
// A stored mail_accounts row wins over a Google token (see transportFor), so
// signing in here with an address that already has a Google grant would move
// that account to IMAP for whoever holds the app password. A datastore failure
// must not answer "no Google" either, or the guard is walked past by retrying
// until Mongo blinks.
func TestHasGoogleCredentialsFailsRatherThanAnswersNo(t *testing.T) {
	h := newTestHandler(t)

	linked, err := h.hasGoogleCredentials(cancelledContext(), "someone@gmail.com")
	if err == nil {
		t.Fatal("an unreachable datastore answered the Google-link question")
	}
	if linked {
		t.Error("hasGoogleCredentials returned both an error and true")
	}
}

// And the handler propagates that: it must not reach the provider at all when
// it cannot tell whether the address is already a Google account.
func TestSignInWithMailboxStopsWhenItCannotCheckTheGoogleLink(t *testing.T) {
	h := newTestHandler(t)
	rec := httptest.NewRecorder()

	h.SignInWithMailbox(rec, signInRequest(`{"address":"someone@orange.fr","password":"abcd efgh ijkl mnop"}`))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d: an unreadable datastore is not a green light",
			rec.Code, http.StatusInternalServerError)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Errorf("the answer is not the JSON envelope every error uses: %v", err)
	}
}

// One mapping, used by the authenticated connect and by the public sign-in, so
// the two cannot come to disagree about what a refused password means.
func TestWriteMailboxConnectError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"no IMAP route in this edition", errMailboxNoRoute, http.StatusUnprocessableEntity},
		{"provider refused the password", errMailboxRejected, http.StatusUnauthorized},
		{"provider unreachable", errMailboxUnreachable, http.StatusBadGateway},
		{"wrapped, as connectAndStore returns it", errors.Join(errMailboxUnreachable, errors.New("dial")), http.StatusBadGateway},
		{"anything else", errors.New("mongo said no"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeMailboxConnectError(rec, tc.err)
			if rec.Code != tc.want {
				t.Errorf("writeMailboxConnectError(%v) = %d, want %d", tc.err, rec.Code, tc.want)
			}
		})
	}
}

// A refused password and an unknown address answer the same way, because the
// provider is what refused and it does not tell us which it was. Saying
// otherwise would turn this route into an address oracle.
func TestMailboxSignInDoesNotDistinguishUnknownFromWrong(t *testing.T) {
	rejected := httptest.NewRecorder()
	writeMailboxConnectError(rejected, errMailboxRejected)

	var body map[string]interface{}
	if err := json.Unmarshal(rejected.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	msg, _ := body["error"].(string)
	for _, leak := range []string{"inconnu", "existe", "compte", "inscrit"} {
		if bytes.Contains([]byte(msg), []byte(leak)) {
			t.Errorf("the refusal message mentions %q: %q", leak, msg)
		}
	}
}
