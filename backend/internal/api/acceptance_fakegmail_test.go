//go:build acceptance

package api

// A fake Gmail, served by an httptest server, for the acceptance scenarios.
//
// It is the Gmail counterpart of the imapmemserver the imap package is tested
// against: a real HTTP server speaking the slice of the Gmail v1 REST API that
// Mailsorter calls, holding an in-memory mailbox. The point is to observe the
// outcome a user would see in Gmail (is this message still in the inbox, is it
// in the trash, which labels does it carry) instead of trusting what a handler
// says it did.
//
// It answers with the gmail/v1 types themselves, so the JSON it produces is the
// JSON the real client parses. Anything it does not simulate is answered 400
// AND recorded: the harness fails the scenario, naming the request, rather than
// letting an unsimulated call pass for a successful one.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	gmailapi "google.golang.org/api/gmail/v1"
)

// fakeAccessToken is the bearer token the fake expects on every call. The user
// the harness seeds holds it, sealed, so a request only reaches the fake after
// going through the real token path (getUserToken, openToken).
const fakeAccessToken = "fake-gmail-access-token"

// Gmail's system labels the app relies on.
const (
	labelInbox   = "INBOX"
	labelTrash   = "TRASH"
	labelUnread  = "UNREAD"
	labelStarred = "STARRED"
)

type fakeMessage struct {
	id                  string
	threadID            string
	from                string
	to                  string
	subject             string
	body                string
	date                time.Time
	listUnsubscribe     string
	listUnsubscribePost string
	labels              map[string]bool
}

type fakeGmail struct {
	mu        sync.Mutex
	server    *httptest.Server
	messages  map[string]*fakeMessage
	order     []string // insertion order, used to list newest first
	labels    map[string]string
	nextID    int
	nextLabel int
	// unsupported collects every request the fake could not simulate. The
	// harness turns a non-empty list into a scenario failure.
	unsupported []string
}

func newFakeGmail() *fakeGmail {
	f := &fakeGmail{
		messages: map[string]*fakeMessage{},
		labels:   map[string]string{},
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

func (f *fakeGmail) close() { f.server.Close() }

// add stores a message in the mailbox and returns its id. Ids are m1, m2, ...
// so a failure message points at something a reader can follow.
func (f *fakeGmail) add(m *fakeMessage) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	m.id = fmt.Sprintf("m%d", f.nextID)
	if m.threadID == "" {
		m.threadID = "t" + m.id
	}
	if m.labels == nil {
		m.labels = map[string]bool{}
	}
	f.messages[m.id] = m
	f.order = append(f.order, m.id)
	return m.id
}

// labelsOf returns a message's labels, sorted, or nil if it does not exist.
func (f *fakeGmail) labelsOf(id string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.messages[id]
	if !ok {
		return nil
	}
	return sortedKeys(m.labels)
}

func (f *fakeGmail) has(id, label string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.messages[id]
	return ok && m.labels[label]
}

// inInbox is what Gmail shows in the inbox: carrying INBOX and not trashed. A
// trashed message is excluded from every listing that does not ask for the
// trash, whatever its other labels say.
func (f *fakeGmail) inInbox(id string) bool {
	return f.has(id, labelInbox) && !f.has(id, labelTrash)
}

func (f *fakeGmail) labelID(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, n := range f.labels {
		if n == name {
			return id
		}
	}
	return ""
}

func (f *fakeGmail) unsupportedCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.unsupported...)
}

func (f *fakeGmail) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+fakeAccessToken {
		writeGmailError(w, http.StatusUnauthorized, "missing or wrong access token")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/gmail/v1/users/me")
	parts := strings.Split(strings.Trim(path, "/"), "/")

	switch {
	case r.Method == http.MethodGet && path == "/messages":
		f.listMessages(w, r)
	case r.Method == http.MethodGet && len(parts) == 2 && parts[0] == "messages":
		f.getMessage(w, r, parts[1])
	case r.Method == http.MethodPost && len(parts) == 3 && parts[0] == "messages" && parts[2] == "modify":
		f.modifyMessage(w, r, parts[1])
	case r.Method == http.MethodGet && path == "/labels":
		f.listLabels(w)
	case r.Method == http.MethodPost && path == "/labels":
		f.createLabel(w, r)
	case r.Method == http.MethodGet && path == "/profile":
		f.profile(w)
	default:
		f.refuse(w, r.Method+" "+r.URL.Path)
	}
}

func (f *fakeGmail) refuse(w http.ResponseWriter, what string) {
	f.mu.Lock()
	f.unsupported = append(f.unsupported, what)
	f.mu.Unlock()
	// 400 rather than 501: the client retries 5xx with a backoff, and a
	// scenario has nothing to gain from waiting before it fails.
	writeGmailError(w, http.StatusBadRequest, "fake gmail does not simulate "+what)
}

func (f *fakeGmail) listMessages(w http.ResponseWriter, r *http.Request) {
	match, err := parseFakeQuery(r.URL.Query().Get("q"))
	if err != nil {
		f.refuse(w, "query "+strconv.Quote(r.URL.Query().Get("q"))+": "+err.Error())
		return
	}
	limit := 100
	if v, err := strconv.Atoi(r.URL.Query().Get("maxResults")); err == nil && v > 0 {
		limit = v
	}

	f.mu.Lock()
	refs := make([]*gmailapi.Message, 0)
	for i := len(f.order) - 1; i >= 0 && len(refs) < limit; i-- {
		m := f.messages[f.order[i]]
		if match(m) {
			refs = append(refs, &gmailapi.Message{Id: m.id, ThreadId: m.threadID})
		}
	}
	f.mu.Unlock()

	writeGmailJSON(w, &gmailapi.ListMessagesResponse{
		Messages:           refs,
		ResultSizeEstimate: int64(len(refs)),
	})
}

// parseFakeQuery understands the search terms the app sends today. Any other
// term is refused, so a handler that starts relying on a new one fails loudly
// here instead of being answered with a listing that ignored it.
func parseFakeQuery(q string) (func(*fakeMessage) bool, error) {
	var checks []func(*fakeMessage) bool
	for _, term := range strings.Fields(q) {
		lower := strings.ToLower(term)
		switch {
		case lower == "in:inbox":
			checks = append(checks, func(m *fakeMessage) bool { return m.labels[labelInbox] && !m.labels[labelTrash] })
		case lower == "in:trash":
			checks = append(checks, func(m *fakeMessage) bool { return m.labels[labelTrash] })
		case lower == "is:unread":
			checks = append(checks, func(m *fakeMessage) bool { return m.labels[labelUnread] })
		case strings.HasPrefix(lower, "from:"):
			needle := strings.TrimPrefix(lower, "from:")
			checks = append(checks, func(m *fakeMessage) bool { return strings.Contains(strings.ToLower(m.from), needle) })
		default:
			return nil, fmt.Errorf("unsupported search term %q", term)
		}
	}
	return func(m *fakeMessage) bool {
		// Like Gmail, a listing that does not ask for the trash never returns it.
		if m.labels[labelTrash] && !strings.Contains(strings.ToLower(q), "in:trash") {
			return false
		}
		for _, c := range checks {
			if !c(m) {
				return false
			}
		}
		return true
	}, nil
}

func (f *fakeGmail) getMessage(w http.ResponseWriter, r *http.Request, id string) {
	f.mu.Lock()
	m, ok := f.messages[id]
	var msg *gmailapi.Message
	if ok {
		msg = m.resource(r.URL.Query().Get("format"))
	}
	f.mu.Unlock()
	if !ok {
		writeGmailError(w, http.StatusNotFound, "Requested entity was not found.")
		return
	}
	writeGmailJSON(w, msg)
}

func (f *fakeGmail) modifyMessage(w http.ResponseWriter, r *http.Request, id string) {
	var req gmailapi.ModifyMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeGmailError(w, http.StatusBadRequest, "invalid modify body")
		return
	}
	f.mu.Lock()
	m, ok := f.messages[id]
	if ok {
		for _, l := range req.AddLabelIds {
			m.labels[l] = true
		}
		for _, l := range req.RemoveLabelIds {
			delete(m.labels, l)
		}
	}
	var res *gmailapi.Message
	if ok {
		res = &gmailapi.Message{Id: m.id, ThreadId: m.threadID, LabelIds: sortedKeys(m.labels)}
	}
	f.mu.Unlock()
	if !ok {
		writeGmailError(w, http.StatusNotFound, "Requested entity was not found.")
		return
	}
	writeGmailJSON(w, res)
}

func (f *fakeGmail) listLabels(w http.ResponseWriter) {
	labels := []*gmailapi.Label{}
	for _, sys := range []string{labelInbox, labelTrash, labelUnread, labelStarred, "SPAM", "SENT", "DRAFT", "IMPORTANT"} {
		labels = append(labels, &gmailapi.Label{Id: sys, Name: sys, Type: "system"})
	}
	f.mu.Lock()
	for id, name := range f.labels {
		labels = append(labels, &gmailapi.Label{Id: id, Name: name, Type: "user"})
	}
	f.mu.Unlock()
	writeGmailJSON(w, &gmailapi.ListLabelsResponse{Labels: labels})
}

func (f *fakeGmail) createLabel(w http.ResponseWriter, r *http.Request) {
	var req gmailapi.Label
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeGmailError(w, http.StatusBadRequest, "invalid label body")
		return
	}
	f.mu.Lock()
	for _, name := range f.labels {
		if name == req.Name {
			f.mu.Unlock()
			writeGmailError(w, http.StatusConflict, "Label name exists or conflicts")
			return
		}
	}
	f.nextLabel++
	id := fmt.Sprintf("Label_%d", f.nextLabel)
	f.labels[id] = req.Name
	f.mu.Unlock()
	writeGmailJSON(w, &gmailapi.Label{Id: id, Name: req.Name, Type: "user"})
}

func (f *fakeGmail) profile(w http.ResponseWriter) {
	f.mu.Lock()
	total := len(f.messages)
	f.mu.Unlock()
	writeGmailJSON(w, &gmailapi.Profile{EmailAddress: scenarioUser, MessagesTotal: int64(total)})
}

// resource renders the message the way Gmail does for the requested format.
// Caller holds f.mu.
func (m *fakeMessage) resource(format string) *gmailapi.Message {
	headers := []*gmailapi.MessagePartHeader{
		{Name: "From", Value: m.from},
		{Name: "To", Value: m.to},
		{Name: "Subject", Value: m.subject},
		{Name: "Date", Value: m.date.Format(time.RFC1123Z)},
	}
	if m.listUnsubscribe != "" {
		headers = append(headers, &gmailapi.MessagePartHeader{Name: "List-Unsubscribe", Value: m.listUnsubscribe})
	}
	if m.listUnsubscribePost != "" {
		headers = append(headers, &gmailapi.MessagePartHeader{Name: "List-Unsubscribe-Post", Value: m.listUnsubscribePost})
	}
	msg := &gmailapi.Message{
		Id:           m.id,
		ThreadId:     m.threadID,
		LabelIds:     sortedKeys(m.labels),
		Snippet:      snippetOf(m.body),
		InternalDate: m.date.UnixMilli(),
		Payload: &gmailapi.MessagePart{
			MimeType: "text/plain",
			Headers:  headers,
		},
	}
	if format != "metadata" && format != "minimal" {
		msg.Payload.Body = &gmailapi.MessagePartBody{
			Data: base64.URLEncoding.EncodeToString([]byte(m.body)),
			Size: int64(len(m.body)),
		}
	}
	return msg
}

func snippetOf(body string) string {
	if len(body) > 120 {
		return body[:120]
	}
	return body
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func writeGmailJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeGmailError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]interface{}{"code": code, "message": message},
	})
}
