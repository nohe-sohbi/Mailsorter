package gmail

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	gmailapi "google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

// fakeGmail stands in for the Gmail v1 REST API. The generated client is only
// reachable over HTTP, so the listing path (which is about HOW calls are issued,
// not about parsing) can only be tested against a real server.
type fakeGmail struct {
	ids []string

	// get is called for each message fetch and returns the message to serve, or
	// an HTTP status to fail with.
	get func(id string, r *http.Request) (*gmailapi.Message, int)
}

func (f *fakeGmail) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	const base = "/gmail/v1/users/me/messages"
	w.Header().Set("Content-Type", "application/json")

	if r.URL.Path == base {
		list := &gmailapi.ListMessagesResponse{ResultSizeEstimate: int64(len(f.ids))}
		for _, id := range f.ids {
			list.Messages = append(list.Messages, &gmailapi.Message{Id: id})
		}
		json.NewEncoder(w).Encode(list)
		return
	}

	id := strings.TrimPrefix(r.URL.Path, base+"/")
	msg, status := f.get(id, r)
	if status != http.StatusOK {
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"error":{"code":%d,"message":"fake failure"}}`, status)
		return
	}
	json.NewEncoder(w).Encode(msg)
}

// newFakeService returns a Service wired to the fake API, plus the client the
// api package would hand it.
func newFakeService(t *testing.T, fake *fakeGmail) (*Service, *gmailapi.Service) {
	t.Helper()

	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)

	client, err := gmailapi.NewService(context.Background(),
		option.WithHTTPClient(srv.Client()),
		option.WithEndpoint(srv.URL+"/"))
	if err != nil {
		t.Fatalf("build fake gmail client: %v", err)
	}

	s := NewService("id", "secret", "https://example.test/callback")
	// No waiting in tests: the backoff is covered by retry_test.go.
	s.retry.sleep = func(time.Duration) {}
	return s, client
}

func ids(msgs []*gmailapi.Message) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.Id)
	}
	return out
}

// Two invariants of the concurrent fetch, pinned together because they are in
// tension: the fetches must actually overlap, and the results must still come
// back in the mailbox's own order.
//
// The barrier is what makes the concurrency assertion deterministic rather than
// a race against the clock: each handler waits until enough peers have arrived.
// A sequential implementation can never satisfy it and every request times out.
func TestListMessagesFetchesConcurrentlyAndKeepsOrder(t *testing.T) {
	const total = 12
	const barrierSize = 4

	want := make([]string, 0, total)
	for i := 0; i < total; i++ {
		want = append(want, fmt.Sprintf("m%02d", i))
	}

	var (
		mu       sync.Mutex
		arrived  int
		peak     int
		inFlight int
		timedOut int
	)
	release := make(chan struct{})

	fake := &fakeGmail{ids: want}
	fake.get = func(id string, _ *http.Request) (*gmailapi.Message, int) {
		mu.Lock()
		inFlight++
		if inFlight > peak {
			peak = inFlight
		}
		arrived++
		if arrived == barrierSize {
			close(release)
		}
		mu.Unlock()

		select {
		case <-release:
		case <-time.After(2 * time.Second):
			mu.Lock()
			timedOut++
			mu.Unlock()
		}

		mu.Lock()
		inFlight--
		mu.Unlock()
		return &gmailapi.Message{Id: id}, http.StatusOK
	}

	s, client := newFakeService(t, fake)
	resp, err := s.ListMessagesWithPagination(client, "in:inbox", total, "", FieldsFull)
	if err != nil {
		t.Fatalf("ListMessagesWithPagination: %v", err)
	}

	mu.Lock()
	gotPeak, gotTimeouts := peak, timedOut
	mu.Unlock()

	if gotTimeouts > 0 {
		t.Errorf("%d fetch(es) waited alone for the barrier: the fetches are running one at a time", gotTimeouts)
	}
	if gotPeak > listFetchConcurrency {
		t.Errorf("peak concurrent fetches = %d, want at most %d: the bound is what keeps Gmail from answering 429", gotPeak, listFetchConcurrency)
	}

	got := ids(resp.Messages)
	if len(got) != total {
		t.Fatalf("got %d messages, want %d", len(got), total)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("message order = %v, want %v (the listing order is the order the user reads)", got, want)
			break
		}
	}
}

// One message the API refuses must not take the whole mailbox down with it, and
// must not shift the others either.
func TestListMessagesSkipsAnUnreadableMessage(t *testing.T) {
	all := []string{"m0", "m1", "m2", "m3"}
	const broken = "m2"

	fake := &fakeGmail{ids: all}
	fake.get = func(id string, _ *http.Request) (*gmailapi.Message, int) {
		if id == broken {
			// 404 is permanent: shouldRetry declines it, so this fails fast.
			return nil, http.StatusNotFound
		}
		return &gmailapi.Message{Id: id}, http.StatusOK
	}

	s, client := newFakeService(t, fake)
	resp, err := s.ListMessagesWithPagination(client, "in:inbox", int64(len(all)), "", FieldsFull)
	if err != nil {
		t.Fatalf("one unreadable message should not fail the listing, got: %v", err)
	}

	want := []string{"m0", "m1", "m3"}
	got := ids(resp.Messages)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("messages = %v, want %v", got, want)
	}
}

// The listing used to ask for full payloads everywhere, downloading every MIME
// part of every message on screen for a view that shows a sender, a subject and
// a snippet. FieldsMetadata fixes that, but only if the headers the parsers read
// are named explicitly: Gmail returns none otherwise.
func TestListMessagesRequestsTheRightPayload(t *testing.T) {
	cases := map[string]struct {
		fields      MessageFields
		wantFormat  string
		wantHeaders bool
	}{
		"metadata listing": {FieldsMetadata, "metadata", true},
		"full listing":     {FieldsFull, "full", false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var (
				mu      sync.Mutex
				formats []string
				headers []string
			)

			fake := &fakeGmail{ids: []string{"m0", "m1"}}
			fake.get = func(id string, r *http.Request) (*gmailapi.Message, int) {
				mu.Lock()
				formats = append(formats, r.URL.Query().Get("format"))
				headers = r.URL.Query()["metadataHeaders"]
				mu.Unlock()
				return &gmailapi.Message{Id: id}, http.StatusOK
			}

			s, client := newFakeService(t, fake)
			if _, err := s.ListMessagesWithPagination(client, "in:inbox", 2, "", tc.fields); err != nil {
				t.Fatalf("ListMessagesWithPagination: %v", err)
			}

			mu.Lock()
			defer mu.Unlock()

			for _, got := range formats {
				if got != tc.wantFormat {
					t.Errorf("fetch format = %q, want %q", got, tc.wantFormat)
				}
			}
			if !tc.wantHeaders {
				if len(headers) != 0 {
					t.Errorf("full listing asked for metadataHeaders %v, want none", headers)
				}
				return
			}

			asked := map[string]bool{}
			for _, h := range headers {
				asked[h] = true
			}
			for _, need := range metadataHeaders {
				if !asked[need] {
					t.Errorf("metadata listing did not ask for the %q header, so it would come back empty", need)
				}
			}
		})
	}
}
