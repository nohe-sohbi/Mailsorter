//go:build acceptance

package api

// The acceptance harness: what a scenario runs against.
//
// A scenario is one sentence Nohé can read and answer ("yes, that is what I
// want" or "no, that is a bug"), proven by the real app: the real router and
// middleware, a real MongoDB (a fresh database per scenario, dropped after),
// and the fake Gmail in acceptance_fakegmail_test.go. The existing api tests
// point Mongo at a dead address on purpose, which is right for the degraded
// paths and means none of them can say WHICH messages a handler moved. These
// scenarios exist to say exactly that.
//
// They do not run with a plain `go test ./...`. They need the build tag and a
// MongoDB: `make scenarios` provides both (see backend/scripts/scenarios.sh).
// Without MAILSORTER_TEST_MONGO_URI they FAIL rather than skip, because a
// scenario that silently skips reads as green.
//
// Two files are this harness and every scenarios_*_test.go file uses it. All of
// them are the definition of done: whoever writes the code must not rewrite
// them to make a change pass.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/ai"
	"github.com/nohe-sohbi/mailsorter/backend/internal/auth"
	"github.com/nohe-sohbi/mailsorter/backend/internal/crypto"
	"github.com/nohe-sohbi/mailsorter/backend/internal/database"
	"github.com/nohe-sohbi/mailsorter/backend/internal/gmail"
	"github.com/nohe-sohbi/mailsorter/backend/internal/metrics"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// scenarioUser is the account every scenario runs as.
const scenarioUser = "moi@exemple.fr"

// ---------------------------------------------------------------------------
// Scenarios and the report
// ---------------------------------------------------------------------------

// status says what Nohé answered about a sentence: nothing yet, yes or no.
type status int

const (
	// observed: what the app does today, proven by the test, but nobody has
	// said it is what they want. Characterization, not specification.
	observed status = iota
	// validated: Nohé confirmed this is the intended behavior.
	validated
	// rejected: Nohé answered no. The test still pins what the app does
	// today, so the report keeps showing it until the change that fixes it
	// replaces this scenario with the sentence he wants.
	rejected
)

func (s status) label() string {
	switch s {
	case validated:
		return "validé"
	case rejected:
		return "refusé"
	default:
		return "constaté"
	}
}

// scenario is one sentence and the test that proves it.
type scenario struct {
	// phrase is what Nohé reads: French, concrete, no jargon, one behavior.
	phrase string
	status status
	run    func(t *testing.T, app *testApp)
}

type scenarioResult struct {
	theme    string
	phrase   string
	status   status
	passed   bool
	failures []string
}

var (
	resultsMu sync.Mutex
	results   []*scenarioResult
)

// playScenarios runs each scenario as its own subtest, on its own fresh app,
// and records the outcome for the report.
func playScenarios(t *testing.T, theme string, list []scenario) {
	t.Helper()
	for _, sc := range list {
		sc := sc
		t.Run(sc.phrase, func(t *testing.T) {
			res := &scenarioResult{theme: theme, phrase: sc.phrase, status: sc.status}
			defer func() {
				// Deferred so a Fatal (runtime.Goexit) is recorded too.
				res.passed = !t.Failed()
				resultsMu.Lock()
				results = append(results, res)
				resultsMu.Unlock()
			}()
			app := newTestApp(t, res)
			sc.run(t, app)
			app.checkFakeGmail()
		})
	}
}


// writeScenarioReport renders the results as the French page Nohé reads: one
// line per sentence, OK or KO, validated, rejected or only observed. The
// rejected ones are listed again on their own, because each is a fix he asked
// for and a table row is easy to read past. Nothing in it is written by hand,
// so it cannot claim a behavior the app does not have.
func writeScenarioReport(path string) error {
	resultsMu.Lock()
	defer resultsMu.Unlock()
	if len(results) == 0 {
		return nil
	}

	ok, ko := 0, 0
	byStatus := map[status]int{}
	for _, r := range results {
		if r.passed {
			ok++
		} else {
			ko++
		}
		byStatus[r.status]++
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Ce que fait Mailsorter, prouvé par l'app qui tourne\n\n")
	fmt.Fprintf(&b, "Généré le %s. %d scénarios : %d OK, %d KO. %d validés par toi, %d refusés par toi, %d seulement constatés.\n\n",
		time.Now().Format("02/01/2006 à 15:04 MST"), len(results), ok, ko,
		byStatus[validated], byStatus[rejected], byStatus[observed])
	fmt.Fprintf(&b, "- **OK** : l'app fait exactement ce que dit la phrase. **KO** : elle ne le fait pas.\n")
	fmt.Fprintf(&b, "- **validé** : tu as confirmé que c'est voulu. **refusé** : tu as dit non ; la phrase décrit ce que fait l'app aujourd'hui, à corriger. **constaté** : c'est ce que fait l'app aujourd'hui, personne ne l'a encore validé.\n")

	theme := ""
	n := 0
	for _, r := range results {
		if r.theme != theme {
			theme = r.theme
			fmt.Fprintf(&b, "\n## %s\n\n| # | Résultat | Phrase | Statut |\n|---|---|---|---|\n", theme)
		}
		n++
		mark := "OK"
		if !r.passed {
			mark = "**KO**"
		}
		phrase := strings.ReplaceAll(r.phrase, "|", "/")
		fmt.Fprintf(&b, "| %d | %s | %s | %s |\n", n, mark, phrase, r.status.label())
	}

	if byStatus[rejected] > 0 {
		fmt.Fprintf(&b, "\n## À corriger (tu as dit non)\n\n")
		for i, r := range results {
			if r.status != rejected {
				continue
			}
			// i+1 is the sentence's number in the tables above.
			fmt.Fprintf(&b, "- **%d.** %s\n", i+1, r.phrase)
		}
	}

	if ko > 0 {
		fmt.Fprintf(&b, "\n## Pourquoi c'est KO\n\n")
		n = 0
		for _, r := range results {
			n++
			if r.passed {
				continue
			}
			fmt.Fprintf(&b, "**%d. %s**\n\n", n, r.phrase)
			if len(r.failures) == 0 {
				fmt.Fprintf(&b, "- le test a échoué sans message lisible (voir la sortie de go test)\n")
			}
			for _, f := range r.failures {
				fmt.Fprintf(&b, "- %s\n", f)
			}
			b.WriteString("\n")
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// ---------------------------------------------------------------------------
// The app under test
// ---------------------------------------------------------------------------

var (
	mongoOnce   sync.Once
	mongoClient *mongo.Client
	mongoErr    error
	dbCounter   int64
)

func sharedMongo() (*mongo.Client, error) {
	mongoOnce.Do(func() {
		uri := os.Getenv("MAILSORTER_TEST_MONGO_URI")
		if uri == "" {
			mongoErr = fmt.Errorf("MAILSORTER_TEST_MONGO_URI n'est pas défini : lance les scénarios avec `make scenarios`, qui démarre une MongoDB jetable")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		mongoClient, mongoErr = mongo.Connect(ctx, options.Client().ApplyURI(uri))
		if mongoErr == nil {
			mongoErr = mongoClient.Ping(ctx, nil)
		}
		if mongoErr != nil {
			mongoErr = fmt.Errorf("MongoDB injoignable sur %s : %w", uri, mongoErr)
		}
	})
	return mongoClient, mongoErr
}

// testApp is one running Mailsorter: real router, its own database, its own
// fake Gmail, one signed-in user.
type testApp struct {
	t      *testing.T
	res    *scenarioResult
	h      *Handler
	server *httptest.Server
	gmail  *fakeGmail
	ai     *fakeAI
	token  string
}

func newTestApp(t *testing.T, res *scenarioResult) *testApp {
	t.Helper()
	app := &testApp{t: t, res: res}

	client, err := sharedMongo()
	if err != nil {
		app.fatalf("%v", err)
	}
	name := fmt.Sprintf("scn_%d_%d", time.Now().UnixNano(), atomic.AddInt64(&dbCounter, 1))
	db := &database.Database{Client: client, DB: client.Database(name)}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := db.EnsureIndexes(ctx); err != nil {
		app.fatalf("création des index MongoDB : %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = db.DB.Drop(ctx)
	})

	app.gmail = newFakeGmail()
	t.Cleanup(app.gmail.close)
	app.ai = newFakeAI()
	t.Cleanup(app.ai.close)

	gsvc := gmail.NewService("scenario-client", "scenario-secret", "http://localhost/auth/callback").
		WithEndpoint(app.gmail.server.URL + "/")

	// Built field by field like newTestHandler, and for the same reason:
	// NewHandler starts the background loops, and a scenario that needs one
	// (the snooze wake sweep) calls it explicitly, at the moment it chooses.
	app.h = &Handler{
		db:           db,
		gmailService: gsvc,
		encryptor:    crypto.NewEncryptor(testSecret),
		aiRegistry: ai.NewRegistry([]ai.ProviderConfig{{
			Name:    "openai-compatible",
			APIKey:  "fake-ai-key",
			Model:   "fake-model",
			BaseURL: app.ai.server.URL,
			Enabled: true,
		}}),
		auth:      auth.NewManager(testSecret),
		jobQueue:  make(chan string, 256),
		metrics:   metrics.New(),
		startedAt: time.Now(),
	}
	app.server = httptest.NewServer(app.h.SetupRoutes())
	t.Cleanup(app.server.Close)

	access, err := app.h.sealToken(fakeAccessToken)
	if err != nil {
		app.fatalf("chiffrement du jeton d'accès : %v", err)
	}
	refresh, err := app.h.sealToken("fake-gmail-refresh-token")
	if err != nil {
		app.fatalf("chiffrement du jeton de rafraîchissement : %v", err)
	}
	now := time.Now()
	if _, err := db.Users().InsertOne(ctx, models.User{
		Email:        scenarioUser,
		AccessToken:  access,
		RefreshToken: refresh,
		TokenExpiry:  now.Add(24 * time.Hour),
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		app.fatalf("création de l'utilisateur : %v", err)
	}
	app.token = app.h.auth.IssueSession(scenarioUser)
	return app
}

// failf records a failure in words the report can show, then fails the test.
func (a *testApp) failf(format string, args ...interface{}) {
	a.t.Helper()
	msg := fmt.Sprintf(format, args...)
	a.res.failures = append(a.res.failures, msg)
	a.t.Error(msg)
}

func (a *testApp) fatalf(format string, args ...interface{}) {
	a.t.Helper()
	msg := fmt.Sprintf(format, args...)
	a.res.failures = append(a.res.failures, msg)
	a.t.Fatal(msg)
}

func (a *testApp) checkFakeGmail() {
	a.t.Helper()
	for _, call := range a.gmail.unsupportedCalls() {
		a.failf("le faux Gmail a reçu une requête qu'il ne sait pas simuler : %s (à ajouter dans acceptance_fakegmail_test.go)", call)
	}
}

// ---------------------------------------------------------------------------
// Acting like the user
// ---------------------------------------------------------------------------

// incoming is a message as it arrives in the inbox.
type incoming struct {
	From    string // raw From header, e.g. `Boutique <info@boutique.com>`
	Subject string
	Body    string
	// Unsubscribe is the List-Unsubscribe header, e.g. "<mailto:stop@boutique.com>".
	// Use a mailto link: an https one makes the server POST to the internet.
	Unsubscribe string
}

// receive delivers mails to the Gmail inbox, unread, oldest first, and returns
// their ids. It does not sync: call sync (or receiveAndSync) for Mailsorter to
// see them.
func (a *testApp) receive(mails ...incoming) []string {
	a.t.Helper()
	ids := make([]string, 0, len(mails))
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
		ids = append(ids, a.gmail.add(&fakeMessage{
			from:            m.From,
			to:              scenarioUser,
			subject:         subject,
			body:            body,
			date:            base.Add(time.Duration(i) * time.Minute),
			listUnsubscribe: m.Unsubscribe,
			labels:          map[string]bool{labelInbox: true, labelUnread: true},
		}))
	}
	return ids
}

// sync runs the real inbox sync, the way the "Synchroniser" button does.
func (a *testApp) sync() {
	a.t.Helper()
	a.mustCall(http.MethodPost, "/api/emails/sync", nil, http.StatusOK)
}

func (a *testApp) receiveAndSync(mails ...incoming) []string {
	a.t.Helper()
	ids := a.receive(mails...)
	a.sync()
	return ids
}

// protect adds an address or a domain to the protected senders, through the API.
func (a *testApp) protect(value string) {
	a.t.Helper()
	a.mustCall(http.MethodPost, "/api/protected", map[string]string{"value": value}, http.StatusCreated, http.StatusOK)
}

// act is the single-message action the reader and the shortcuts use.
func (a *testApp) act(id, action string) {
	a.t.Helper()
	a.mustCall(http.MethodPost, "/api/emails/action", map[string]string{"messageId": id, "action": action}, http.StatusOK)
}

// call sends one authenticated request to the real router and returns the
// status and the decoded JSON body (nil when the body is not a JSON object).
func (a *testApp) call(method, path string, body interface{}) (int, map[string]interface{}) {
	a.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			a.fatalf("encodage de la requête %s %s : %v", method, path, err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, a.server.URL+path, reader)
	if err != nil {
		a.fatalf("requête %s %s : %v", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.fatalf("appel %s %s : %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]interface{}
	_ = json.Unmarshal(raw, &out)
	if out == nil && len(raw) > 0 {
		out = map[string]interface{}{"_raw": string(raw)}
	}
	return resp.StatusCode, out
}

// mustCall is call with an expected status; any other status fails the
// scenario with the response body, which usually says why.
func (a *testApp) mustCall(method, path string, body interface{}, want ...int) map[string]interface{} {
	a.t.Helper()
	code, out := a.call(method, path, body)
	for _, w := range want {
		if code == w {
			return out
		}
	}
	a.fatalf("%s %s a répondu %d (attendu %v) : %v", method, path, code, want, out)
	return nil
}

// ---------------------------------------------------------------------------
// Looking at the outcome
// ---------------------------------------------------------------------------

func (a *testApp) describe(id string) string {
	a.gmail.mu.Lock()
	defer a.gmail.mu.Unlock()
	if m, ok := a.gmail.messages[id]; ok {
		return fmt.Sprintf("%s (de %q, %q)", id, m.from, m.subject)
	}
	return id + " (inconnu du faux Gmail)"
}

func (a *testApp) expectInInbox(ids ...string) {
	a.t.Helper()
	for _, id := range ids {
		if !a.gmail.inInbox(id) {
			a.failf("le mail %s devrait être dans ta boîte de réception, il n'y est pas (libellés Gmail : %v)", a.describe(id), a.gmail.labelsOf(id))
		}
	}
}

func (a *testApp) expectNotInInbox(ids ...string) {
	a.t.Helper()
	for _, id := range ids {
		if a.gmail.inInbox(id) {
			a.failf("le mail %s ne devrait plus être dans ta boîte de réception, il y est encore (libellés Gmail : %v)", a.describe(id), a.gmail.labelsOf(id))
		}
	}
}

func (a *testApp) expectInTrash(ids ...string) {
	a.t.Helper()
	for _, id := range ids {
		if !a.gmail.has(id, labelTrash) {
			a.failf("le mail %s devrait être dans la corbeille, il n'y est pas (libellés Gmail : %v)", a.describe(id), a.gmail.labelsOf(id))
		}
	}
}

func (a *testApp) expectNotInTrash(ids ...string) {
	a.t.Helper()
	for _, id := range ids {
		if a.gmail.has(id, labelTrash) {
			a.failf("le mail %s ne devrait pas être dans la corbeille, il y est (libellés Gmail : %v)", a.describe(id), a.gmail.labelsOf(id))
		}
	}
}

func (a *testApp) expectUnread(ids ...string) {
	a.t.Helper()
	for _, id := range ids {
		if !a.gmail.has(id, labelUnread) {
			a.failf("le mail %s devrait être marqué non lu, il est marqué lu", a.describe(id))
		}
	}
}

func (a *testApp) expectLabel(id, name string) {
	a.t.Helper()
	labelID := a.gmail.labelID(name)
	if labelID == "" || !a.gmail.has(id, labelID) {
		a.failf("le mail %s devrait porter le libellé %q, il ne le porte pas (libellés Gmail : %v)", a.describe(id), name, a.gmail.labelsOf(id))
	}
}

func (a *testApp) expectNoLabel(id, name string) {
	a.t.Helper()
	labelID := a.gmail.labelID(name)
	if labelID != "" && a.gmail.has(id, labelID) {
		a.failf("le mail %s ne devrait pas porter le libellé %q, il le porte", a.describe(id), name)
	}
}

// history returns the action log for a message, oldest first: what the
// Historique screen lists.
func (a *testApp) history(id string) []models.ActionLog {
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cur, err := a.h.db.ActionLog().Find(ctx, bson.M{"userId": scenarioUser, "messageId": id},
		options.Find().SetSort(bson.D{{Key: "createdAt", Value: 1}, {Key: "_id", Value: 1}}))
	if err != nil {
		a.fatalf("lecture de l'historique : %v", err)
	}
	var rows []models.ActionLog
	if err := cur.All(ctx, &rows); err != nil {
		a.fatalf("lecture de l'historique : %v", err)
	}
	return rows
}

// expectHistory checks that the message's last history entry is this action
// from this source (the Source* constants in ledger.go).
func (a *testApp) expectHistory(id, action, source string) {
	a.t.Helper()
	rows := a.history(id)
	if len(rows) == 0 {
		a.failf("l'historique devrait montrer %q (%s) pour le mail %s, il ne montre rien", action, source, a.describe(id))
		return
	}
	last := rows[len(rows)-1]
	if last.Action != action || last.Source != source {
		a.failf("la dernière ligne d'historique du mail %s est %q (%s), attendu %q (%s)", a.describe(id), last.Action, last.Source, action, source)
	}
}

func (a *testApp) expectNoHistory(id string) {
	a.t.Helper()
	if rows := a.history(id); len(rows) > 0 {
		a.failf("l'historique ne devrait rien montrer pour le mail %s, il montre %d ligne(s), la dernière : %q (%s)", a.describe(id), len(rows), rows[len(rows)-1].Action, rows[len(rows)-1].Source)
	}
}

func (a *testApp) number(out map[string]interface{}, key string) int {
	a.t.Helper()
	v, ok := out[key].(float64)
	if !ok {
		a.failf("la réponse ne contient pas de nombre %q : %v", key, out)
		return -1
	}
	return int(v)
}

// ---------------------------------------------------------------------------
// More of what the user does, one helper per screen action
// ---------------------------------------------------------------------------

// undoLast undoes the message's most recent history entry, the way the
// "Annuler" button of the Historique screen does.
func (a *testApp) undoLast(id string) {
	a.t.Helper()
	rows := a.history(id)
	if len(rows) == 0 {
		a.fatalf("rien à annuler dans l'historique du mail %s", a.describe(id))
	}
	a.mustCall(http.MethodPost, "/api/activity/undo", map[string]string{"id": rows[len(rows)-1].ID}, http.StatusOK)
}

// batchAction is the action bar on a selection of messages.
func (a *testApp) batchAction(action string, ids ...string) map[string]interface{} {
	a.t.Helper()
	return a.mustCall(http.MethodPost, "/api/emails/batch-action",
		map[string]interface{}{"messageIds": ids, "action": action}, http.StatusOK)
}

// batchUndo is the "Annuler" offered right after a batch action.
func (a *testApp) batchUndo(action string, ids ...string) {
	a.t.Helper()
	a.mustCall(http.MethodPost, "/api/emails/batch-undo",
		map[string]interface{}{"messageIds": ids, "action": action}, http.StatusOK)
}

// snooze reports one message to tomorrow morning.
func (a *testApp) snooze(id string) {
	a.t.Helper()
	a.mustCall(http.MethodPost, "/api/emails/snooze",
		map[string]string{"messageId": id, "preset": "tomorrow"}, http.StatusOK)
}

func (a *testApp) batchSnooze(ids ...string) map[string]interface{} {
	a.t.Helper()
	return a.mustCall(http.MethodPost, "/api/emails/batch-snooze",
		map[string]interface{}{"messageIds": ids, "preset": "tomorrow"}, http.StatusOK)
}

// timeComesForSnoozes moves every scheduled wake time to the past, then runs
// the background sweep once: what the 1 minute loop does when the time comes.
func (a *testApp) timeComesForSnoozes() {
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := a.h.db.Snoozes().UpdateMany(ctx,
		bson.M{"userId": scenarioUser, "status": "scheduled"},
		bson.M{"$set": bson.M{"wakeAt": time.Now().Add(-time.Minute)}}); err != nil {
		a.fatalf("avance de l'heure des reports : %v", err)
	}
	a.h.wakeDueSnoozes()
}

// senderCard is one row of the Expediteurs screen: the sender key it holds
// and the count it shows, which is also the count the "Tout supprimer" /
// "Tout archiver" confirmation announces.
type senderCard struct {
	SenderEmail string `json:"senderEmail"`
	EmailCount  int    `json:"emailCount"`
}

// senderCardFor returns the Expediteurs row for a sender, as GET /api/senders
// lists it. It fails the scenario if the screen does not list that sender.
func (a *testApp) senderCardFor(from string) senderCard {
	a.t.Helper()
	code, out := a.call(http.MethodGet, "/api/senders", nil)
	if code != http.StatusOK {
		a.fatalf("GET /api/senders a répondu %d : %v", code, out)
	}
	raw, _ := out["_raw"].(string)
	var senders []senderCard
	if err := json.Unmarshal([]byte(raw), &senders); err != nil {
		a.fatalf("réponse de GET /api/senders illisible : %v", err)
	}
	for _, s := range senders {
		if s.SenderEmail == from {
			return s
		}
	}
	a.fatalf("l'écran Expéditeurs ne liste pas %q (il liste %v)", from, senders)
	return senderCard{}
}

// senderKey is the value the Expediteurs screen holds for a sender, and sends
// back for a sender-wide action: the sender's raw From header, as grouped by
// GET /api/senders.
func (a *testApp) senderKey(from string) string {
	a.t.Helper()
	return a.senderCardFor(from).SenderEmail
}

// announcedCount is the number of mails the Expediteurs screen shows for a
// sender, the one its "Tout supprimer" confirmation says will go to the trash.
// A sentence about "what the screen announces" needs it read from the screen's
// own endpoint rather than counted by the scenario.
func (a *testApp) announcedCount(from string) int {
	a.t.Helper()
	return a.senderCardFor(from).EmailCount
}

// deleteAllFrom is "Tout supprimer" on the Expediteurs screen.
func (a *testApp) deleteAllFrom(from string) map[string]interface{} {
	a.t.Helper()
	return a.mustCall(http.MethodPost, "/api/ai/apply-bulk",
		map[string]string{"senderEmail": a.senderKey(from), "action": "delete"}, http.StatusOK)
}

// archiveAllFrom is "Tout archiver" on the Expediteurs screen: the same call as
// "Tout supprimer", with the archive action.
func (a *testApp) archiveAllFrom(from string) map[string]interface{} {
	a.t.Helper()
	return a.mustCall(http.MethodPost, "/api/ai/apply-bulk",
		map[string]string{"senderEmail": a.senderKey(from), "action": "archive"}, http.StatusOK)
}

// expectUnsubscribed checks the "Désabonné" mark on the Abonnements tab: the
// unsubscribed flag GET /api/subscriptions returns for that bare address. The
// tab reloads from that endpoint, so it is what the user sees after a refresh.
func (a *testApp) expectUnsubscribed(addr string) {
	a.t.Helper()
	code, out := a.call(http.MethodGet, "/api/subscriptions", nil)
	if code != http.StatusOK {
		a.fatalf("GET /api/subscriptions a répondu %d : %v", code, out)
	}
	raw, _ := out["_raw"].(string)
	var subs []struct {
		SenderEmail  string `json:"senderEmail"`
		Unsubscribed bool   `json:"unsubscribed"`
	}
	if err := json.Unmarshal([]byte(raw), &subs); err != nil {
		a.fatalf("réponse de GET /api/subscriptions illisible : %v", err)
	}
	for _, s := range subs {
		if s.SenderEmail == addr {
			if !s.Unsubscribed {
				a.failf("l'onglet Abonnements devrait marquer %s \"Désabonné\", il ne le fait pas", addr)
			}
			return
		}
	}
	a.failf("l'onglet Abonnements ne liste pas %s, il ne peut donc pas le marquer \"Désabonné\" (il liste %v)", addr, subs)
}

// unsubscribeAndArchive is "Se désabonner" with "et archiver ses mails".
func (a *testApp) unsubscribeAndArchive(id string) map[string]interface{} {
	a.t.Helper()
	return a.mustCall(http.MethodPost, "/api/unsubscribe",
		map[string]interface{}{"messageId": id, "alsoArchive": true}, http.StatusOK)
}

// rule creates a sorting rule: one condition, then the actions in order.
func (a *testApp) rule(name string, priority int, field, operator, value string, actions ...models.RuleAction) {
	a.t.Helper()
	a.mustCall(http.MethodPost, "/api/rules", models.SortingRuleInput{
		Name:       name,
		Enabled:    true,
		MatchAll:   true,
		Conditions: []models.RuleCondition{{Field: field, Operator: operator, Value: value}},
		Actions:    actions,
		Priority:   priority,
	}, http.StatusCreated, http.StatusOK)
}

// applyRules is the "Appliquer mes règles" button.
func (a *testApp) applyRules() map[string]interface{} {
	a.t.Helper()
	return a.mustCall(http.MethodPost, "/api/rules/apply", nil, http.StatusOK)
}

// setAutoApplyRules flips "Appliquer mes règles automatiquement" in Réglages.
func (a *testApp) setAutoApplyRules(on bool) {
	a.t.Helper()
	a.mustCall(http.MethodPut, "/api/account/settings", map[string]bool{"autoApplyRules": on}, http.StatusOK)
}

// analyze asks the AI about these messages, the way the inbox does.
func (a *testApp) analyze(ids ...string) map[string]interface{} {
	a.t.Helper()
	return a.mustCall(http.MethodPost, "/api/ai/analyze", map[string]interface{}{"emailIds": ids}, http.StatusOK)
}

// suggestionFor returns the AI suggestion stored for a message.
func (a *testApp) suggestionFor(id string) models.AISuggestion {
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var s models.AISuggestion
	if err := a.h.db.AISuggestions().FindOne(ctx, bson.M{"userId": scenarioUser, "emailId": id}).Decode(&s); err != nil {
		a.fatalf("aucune proposition de l'IA pour le mail %s : %v", a.describe(id), err)
	}
	return s
}

// applySuggestion is "Appliquer" on one proposal of the triage plan.
func (a *testApp) applySuggestion(id string) {
	a.t.Helper()
	a.mustCall(http.MethodPost, "/api/ai/apply", map[string]string{"suggestionId": a.suggestionFor(id).ID}, http.StatusOK)
}

// applyAllSuggestions is "Tout ranger" on the triage plan.
func (a *testApp) applyAllSuggestions(ids ...string) map[string]interface{} {
	a.t.Helper()
	sids := make([]string, 0, len(ids))
	for _, id := range ids {
		sids = append(sids, a.suggestionFor(id).ID)
	}
	return a.mustCall(http.MethodPost, "/api/ai/apply-batch", map[string]interface{}{"suggestionIds": sids}, http.StatusOK)
}

// autopilot stores what the Expediteurs screen saves when the user turns on
// "Appliquer automatiquement" for a sender: its raw From header, the action,
// autoApply on.
func (a *testApp) autopilot(from, action string) {
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	now := time.Now()
	if _, err := a.h.db.SenderPreferences().InsertOne(ctx, models.SenderPreference{
		UserID:        scenarioUser,
		SenderEmail:   from,
		SenderDomain:  extractDomain(from),
		SenderName:    extractSenderName(from),
		AutoApply:     true,
		DefaultAction: action,
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		a.fatalf("activation du pilote automatique pour %q : %v", from, err)
	}
}

func TestMain(m *testing.M) {
	// First, before anything in the process verifies a certificate: Go reads
	// the system roots once, so a CA trusted later is never seen and every
	// IMAP scenario fails to reach its server.
	cleanup := trustIMAPTestCertificate()
	code := m.Run()
	cleanup()
	if path := os.Getenv("SCENARIO_REPORT"); path != "" {
		if err := writeScenarioReport(path); err != nil {
			fmt.Fprintf(os.Stderr, "scenario report: %v\n", err)
			if code == 0 {
				code = 1
			}
		}
	}
	os.Exit(code)
}
func (a *testApp) call(method, path string, body interface{}) (int, map[string]interface{}) {
	a.t.Helper()
	code, raw := a.callRaw(method, path, body)
	var out map[string]interface{}
	_ = json.Unmarshal([]byte(raw), &out)
	if out == nil && len(raw) > 0 {
		out = map[string]interface{}{"_raw": raw}
	}
	return code, out
}

// callRaw is call without the decoding: the status and the body exactly as the
// screen receives it, for a sentence about what a response must never contain.
func (a *testApp) callRaw(method, path string, body interface{}) (int, string) {
	a.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			a.fatalf("encodage de la requête %s %s : %v", method, path, err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, a.server.URL+path, reader)
	if err != nil {
		a.fatalf("requête %s %s : %v", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.fatalf("appel %s %s : %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		a.fatalf("lecture de la réponse à %s %s : %v", method, path, err)
	}
	return resp.StatusCode, string(raw)
}

// mustCall is call with an expected status; any other status fails the
// scenario with the response body, which usually says why.
func (a *testApp) mustCall(method, path string, body interface{}, want ...int) map[string]interface{} {
	a.t.Helper()
	code, out := a.call(method, path, body)
	for _, w := range want {
		if code == w {
			return out
		}
	}
	a.fatalf(""%s %s a répondu %d (attendu %v) : %v", method, path, code, want, out)
	return nil
}
func (a *testApp) callAs(token, method, path string, body interface{}) (int, map[string]interface{}) {
	a.t.Helper()
	return a.send(method, path, body, map[string]string{"Authorization": "Bearer " + token})
}
func (a *testApp) send(method, path string, body interface{}, headers map[string]string) (int, map[string]interface{}) {
	a.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			a.fatalf("encodage de la requête %s %s : %v", method, path, err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, a.server.URL+path, reader)
	if err != nil {
		a.fatalf("requête %s %s : %v", method, path, err)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.fatalf("appel %s %s : %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]interface{}
	_ = json.Unmarshal(raw, &out)
	if out == nil && len(raw) > 0 {
		out = map[string]interface{}{"_raw": string(raw)}
	}
	return resp.StatusCode, out
}
type ownAIKey struct {
	Provider string `json:"provider"`
	APIKey   string `json:"apiKey"`
	Model    string `json:"model"`
	BaseURL  string `json:"baseUrl"`
}

// testOwnAIKey is "Tester la connexion" on that card. It returns the status and
// the body exactly as received: the card shows the "error" field of a refusal
// as is, so a sentence about what that message contains reads the whole body.
func (a *testApp) testOwnAIKey(key ownAIKey) (int, string) {
	a.t.Helper()
	return a.callRaw(http.MethodPost, "/api/ai/settings/test", key)
}

// saveOwnAIKey is "Enregistrer" on that card. It returns the status and the
// decoded body, whatever they are: a sentence about a refusal checks them.
func (a *testApp) saveOwnAIKey(key ownAIKey) (int, map[string]interface{}) {
	a.t.Helper()
	return a.call(http.MethodPut, "/api/ai/settings", key)
}

// ownAIKeyShown is what the card shows after a reload: GET /api/ai/settings.
func (a *testApp) ownAIKeyShown() map[string]interface{} {
	a.t.Helper()
	return a.mustCall(http.MethodGet, "/api/ai/settings", nil, http.StatusOK)
}

// ownAIKeyPage is the same answer as ownAIKeyShown, undecoded: a sentence
// about what the card must never show reads every byte the screen receives,
// not only the fields it knows about.
func (a *testApp) ownAIKeyPage() string {
	a.t.Helper()
	code, raw := a.callRaw(http.MethodGet, "/api/ai/settings", nil)
	if code != http.StatusOK {
		a.fatalf("GET /api/ai/settings a répondu %d : %s", code, raw)
	}
	return raw
}

// ownAIKeyAlreadySaved stores a personal AI key the way "Enregistrer" leaves it
// (the key sealed, the rest as typed), straight in the datastore. A sentence
// about a key ALREADY saved must not depend on the save accepting that address
// today: the address a name leads to can change after it was saved, so what
// matters is what the app does with the key it holds.
func (a *testApp) ownAIKeyAlreadySaved(key ownAIKey) {
	a.t.Helper()
	sealed, err := a.h.encryptor.Encrypt(key.APIKey)
	if err != nil {
		a.fatalf("chiffrement de la clé IA personnelle : %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	now := time.Now()
	if _, err := a.h.db.AISettings().InsertOne(ctx, models.AIProviderSettings{
		UserID:    scenarioUser,
		Provider:  key.Provider,
		APIKey:    sealed,
		Model:     key.Model,
		BaseURL:   key.BaseURL,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		a.fatalf("enregistrement de la clé IA personnelle : %v", err)
	}
// installedAtHome runs the scenario on a Mailsorter someone installed for
// themselves: EDITION=self-hosted, what the server boots with by default.
func (a *testApp) installedAtHome() {
	a.t.Helper()
	a.runsAs(provider.EditionSelfHosted)
}

// onManagedService runs the scenario on the managed service: EDITION=hosted.
func (a *testApp) onManagedService() {
	a.t.Helper()
	a.runsAs(provider.EditionHosted)
}

// runsAs sets the edition for this scenario only. Edition is a package
// variable the server sets once at boot, so it is process-wide: without the
// restore, every scenario after this one would run in the edition this one
// chose, and pass or fail for that reason.
func (a *testApp) runsAs(e provider.Edition) {
	a.t.Helper()
	previous := Edition
	Edition = e
	a.t.Cleanup(func() { Edition = previous })
}

// alreadyAnalyzed puts this month's counter of the account signed in with
// token where n analyses by the AI leave it: the usage row incrUsage writes
// (analysis.go), keyed by the month as account.go counts it. Running n real
// analyses would take n mails with n different subjects, since a verdict
// served from the cache is not counted. Then it reads the counter back from
// the Compte screen's endpoint: a scenario about mails already analyzed this
// month proves nothing if the app does not count them.
func (a *testApp) alreadyAnalyzed(email, token string, n int) {
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := a.h.db.Usage().UpdateOne(ctx,
		bson.M{"userId": email, "period": currentPeriod()},
		bson.M{"$set": bson.M{"analyzed": n, "updatedAt": time.Now()}},
		options.Update().SetUpsert(true),
	); err != nil {
		a.fatalf("enregistrement des %d mails déjà analysés ce mois-ci par %s : %v", n, email, err)
	}
	if used := a.number(a.usageAs(token), "used"); used != n {
		a.fatalf("le compteur d'analyses du mois de %s devrait montrer %d mails déjà analysés, il en montre %d : le scénario ne prouve rien (à corriger dans acceptance_harness_test.go)", email, n, used)
	}
}

// usageAs is the month's counter of the account signed in with token, as the
// header and the Compte screen load it (GET /api/usage): "used", the
// analyses spent, and "limit", negative when there is none.
func (a *testApp) usageAs(token string) map[string]interface{} {
	a.t.Helper()
	code, out := a.callAs(token, http.MethodGet, "/api/usage", nil)
	if code != http.StatusOK {
		a.fatalf("le compteur d'analyses du mois ne s'affiche pas : GET /api/usage a répondu %d : %v", code, out)
	}
	return out
}

// workersRunQueuedAnalyses does what the pool of analysis workers does in
// production (startAnalysisWorkers, jobs.go): it runs every analysis queued
// in the background (what "Trier avec l'IA" asks for on a selection of more
// than 10 mails, ASYNC_THRESHOLD in Inbox.js), then returns. The harness builds
// the Handler without NewHandler, so no worker runs on its own; a scenario
// calls this at the moment it chooses, as timeComesForSnoozes does for the
// snooze sweep.
func (a *testApp) workersRunQueuedAnalyses() {
	a.t.Helper()
	for {
		select {
		case jobID := <-a.h.jobQueue:
			a.h.processAnalysisJob(jobID)
		default:
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Visitors: people on the landing page, before any session
// ---------------------------------------------------------------------------

// visitor is a person on the internet, at their own network address, who has
// no session yet: whoever fills in "Se connecter" or "Créer un compte" on the
// landing page. The scenario user above is already signed in; a visitor is
// everyone else.
//
// No visitor reaches the backend directly. Every /api request goes through
// the frontend's nginx (frontend/nginx.conf, location /api), so the TCP peer
// the backend sees is always that proxy, whoever is behind it. The contract
// with the proxy: it sets X-Real-IP to the visitor's address on every request
// it forwards, overwriting whatever the visitor sent, and the backend believes
// that header only from a peer on the internal network. The harness plays it
// the same way: the httptest server is reached from 127.0.0.1, which stands
// for the proxy, and the visitor's address travels in X-Real-IP. So two
// visitors of a scenario are one peer, exactly as two strangers are one peer
// to the production backend, and only that header tells them apart.
type visitor struct {
	app  *testApp
	addr string
	// bearer, when not empty, is sent as "Authorization: Bearer <bearer>":
	// a session the server issued, or any string the visitor made up.
	bearer string
}

// visitorFrom is someone connecting from addr. Use the documentation ranges
// (203.0.113.0/24, 198.51.100.0/24): they belong to nobody.
func (a *testApp) visitorFrom(addr string) visitor {
	return visitor{app: a, addr: addr}
}

// bearing is the same visitor attaching this token to their requests, as the
// app does once signed in, or as anyone can with a token of their own making.
func (v visitor) bearing(token string) visitor {
	v.bearer = token
	return v
}

// answer is what the server said to one request: the status and the JSON body.
type answer struct {
	code int
	body map[string]interface{}
}

// said is the sentence the landing page shows for a refusal: the "error"
// field, which the form displays as is.
func (r answer) said() string {
	s, _ := r.body["error"].(string)
	return s
}

func (r answer) String() string {
	if s := r.said(); s != "" {
		return fmt.Sprintf("%d %q", r.code, s)
	}
	return fmt.Sprintf("%d %v", r.code, r.body)
}

// call sends one request as this visitor, through the proxy.
func (v visitor) call(method, path string, body interface{}) answer {
	v.app.t.Helper()
	headers := map[string]string{"X-Real-IP": v.addr}
	if v.bearer != "" {
		headers["Authorization"] = "Bearer " + v.bearer
	}
	code, out := v.app.send(method, path, body, headers)
	return answer{code: code, body: out}
}

// signIn is "Se connecter" on the landing page: an address and a password.
func (v visitor) signIn(email, password string) answer {
	v.app.t.Helper()
	return v.call(http.MethodPost, "/api/auth/login", map[string]string{"email": email, "password": password})
}

// signUp is "Créer un compte" on the landing page.
func (v visitor) signUp(email, password string) answer {
	v.app.t.Helper()
	return v.call(http.MethodPost, "/api/auth/register", map[string]string{"email": email, "password": password})
}

// accountWithPassword stores a password account as POST /api/auth/register
// leaves it (the address lowercased, a bcrypt hash, the creation date), without
// going through that route: the route is throttled, and the setup of a
// scenario about the throttle must not spend the attempts the scenario counts.
// The hash is at bcrypt's minimum cost. The cost is written into the hash and
// only decides how long checking a password takes; the throttle gives one
// attempt back every three seconds, so a scenario that counts attempts must
// not spend that long hashing between two of them.
func (a *testApp) accountWithPassword(email, password string) {
	a.t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		a.fatalf("hachage du mot de passe de %s : %v", email, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	now := time.Now()
	if _, err := a.h.db.Users().InsertOne(ctx, models.User{
		Email:        strings.ToLower(email),
		PasswordHash: string(hash),
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		a.fatalf("création du compte %s : %v", email, err)
	}
}

// mustCall is call with an expected status; any other status fails the
// scenario with the response body, which usually says why.
func (a *testApp) mustCall(method, path string, body interface{}, want ...int) map[string]interface{} {
	a.t.Helper()
	code, out := a.call(method, path, body)
	for _, w := range want {
		if code == w {
			return out
		}
	}
	a.fatalf("%s %s a répondu %d (attendu %v) : %v", method, path, code, want, out)
	return nil
}

// ---------------------------------------------------------------------------
// Looking at the outcome
// ---------------------------------------------------------------------------

func (a *testApp) describe(id string) string {
	a.gmail.mu.Lock()
	defer a.gmail.mu.Unlock()
	if m, ok := a.gmail.messages[id]; ok {
		return fmt.Sprintf("%s (de %q, %q)", id, m.from, m.subject)
	}
	return id + " (inconnu du faux Gmail)"
}

func (a *testApp) expectInInbox(ids ...string) {
	a.t.Helper()
	for _, id := range ids {
		if !a.gmail.inInbox(id) {
			a.failf("le mail %s devrait être dans ta boîte de réception, il n'y est pas (libellés Gmail : %v)", a.describe(id), a.gmail.labelsOf(id))
		}
	}
}

func (a *testApp) expectNotInInbox(ids ...string) {
	a.t.Helper()
	for _, id := range ids {
		if a.gmail.inInbox(id) {
			a.failf("le mail %s ne devrait plus être dans ta boîte de réception, il y est encore (libellés Gmail : %v)", a.describe(id), a.gmail.labelsOf(id))
		}
	}
}

func (a *testApp) expectInTrash(ids ...string) {
	a.t.Helper()
	for _, id := range ids {
		if !a.gmail.has(id, labelTrash) {
			a.failf("le mail %s devrait être dans la corbeille, il n'y est pas (libellés Gmail : %v)", a.describe(id), a.gmail.labelsOf(id))
		}
	}
}

func (a *testApp) expectNotInTrash(ids ...string) {
	a.t.Helper()
	for _, id := range ids {
		if a.gmail.has(id, labelTrash) {
			a.failf("le mail %s ne devrait pas être dans la corbeille, il y est (libellés Gmail : %v)", a.describe(id), a.gmail.labelsOf(id))
		}
	}
}

func (a *testApp) expectUnread(ids ...string) {
	a.t.Helper()
	for _, id := range ids {
		if !a.gmail.has(id, labelUnread) {
			a.failf("le mail %s devrait être marqué non lu, il est marqué lu", a.describe(id))
		}
	}
}

func (a *testApp) expectLabel(id, name string) {
	a.t.Helper()
	labelID := a.gmail.labelID(name)
	if labelID == "" || !a.gmail.has(id, labelID) {
		a.failf("le mail %s devrait porter le libellé %q, il ne le porte pas (libellés Gmail : %v)", a.describe(id), name, a.gmail.labelsOf(id))
	}
}

func (a *testApp) expectNoLabel(id, name string) {
	a.t.Helper()
	labelID := a.gmail.labelID(name)
	if labelID != "" && a.gmail.has(id, labelID) {
		a.failf("le mail %s ne devrait pas porter le libellé %q, il le porte", a.describe(id), name)
	}
}

// history returns the action log for a message, oldest first: what the
// Historique screen lists.
func (a *testApp) history(id string) []models.ActionLog {
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cur, err := a.h.db.ActionLog().Find(ctx, bson.M{"userId": scenarioUser, "messageId": id},
		options.Find().SetSort(bson.D{{Key: "createdAt", Value: 1}, {Key: "_id", Value: 1}}))
	if err != nil {
		a.fatalf("lecture de l'historique : %v", err)
	}
	var rows []models.ActionLog
	if err := cur.All(ctx, &rows); err != nil {
		a.fatalf("lecture de l'historique : %v", err)
	}
	return rows
}

// expectHistory checks that the message's last history entry is this action
// from this source (the Source* constants in ledger.go).
func (a *testApp) expectHistory(id, action, source string) {
	a.t.Helper()
	rows := a.history(id)
	if len(rows) == 0 {
		a.failf("l'historique devrait montrer %q (%s) pour le mail %s, il ne montre rien", action, source, a.describe(id))
		return
	}
	last := rows[len(rows)-1]
	if last.Action != action || last.Source != source {
		a.failf("la dernière ligne d'historique du mail %s est %q (%s), attendu %q (%s)", a.describe(id), last.Action, last.Source, action, source)
	}
}

func (a *testApp) expectNoHistory(id string) {
	a.t.Helper()
	if rows := a.history(id); len(rows) > 0 {
		a.failf("l'historique ne devrait rien montrer pour le mail %s, il montre %d ligne(s), la dernière : %q (%s)", a.describe(id), len(rows), rows[len(rows)-1].Action, rows[len(rows)-1].Source)
	}
}

func (a *testApp) number(out map[string]interface{}, key string) int {
	a.t.Helper()
	v, ok := out[key].(float64)
	if !ok {
		a.failf("la réponse ne contient pas de nombre %q : %v", key, out)
		return -1
	}
	return int(v)
}

// ---------------------------------------------------------------------------
// More of what the user does, one helper per screen action
// ---------------------------------------------------------------------------

// undoLast undoes the message's most recent history entry, the way the
// "Annuler" button of the Historique screen does.
func (a *testApp) undoLast(id string) {
	a.t.Helper()
	rows := a.history(id)
	if len(rows) == 0 {
		a.fatalf("rien à annuler dans l'historique du mail %s", a.describe(id))
	}
	a.mustCall(http.MethodPost, "/api/activity/undo", map[string]string{"id": rows[len(rows)-1].ID}, http.StatusOK)
}

// batchAction is the action bar on a selection of messages.
func (a *testApp) batchAction(action string, ids ...string) map[string]interface{} {
	a.t.Helper()
	return a.mustCall(http.MethodPost, "/api/emails/batch-action",
		map[string]interface{}{"messageIds": ids, "action": action}, http.StatusOK)
}

// batchUndo is the "Annuler" offered right after a batch action.
func (a *testApp) batchUndo(action string, ids ...string) {
	a.t.Helper()
	a.mustCall(http.MethodPost, "/api/emails/batch-undo",
		map[string]interface{}{"messageIds": ids, "action": action}, http.StatusOK)
}

// snooze reports one message to tomorrow morning.
func (a *testApp) snooze(id string) {
	a.t.Helper()
	a.mustCall(http.MethodPost, "/api/emails/snooze",
		map[string]string{"messageId": id, "preset": "tomorrow"}, http.StatusOK)
}

func (a *testApp) batchSnooze(ids ...string) map[string]interface{} {
	a.t.Helper()
	return a.mustCall(http.MethodPost, "/api/emails/batch-snooze",
		map[string]interface{}{"messageIds": ids, "preset": "tomorrow"}, http.StatusOK)
}

// timeComesForSnoozes moves every scheduled wake time to the past, then runs
// the background sweep once: what the 1 minute loop does when the time comes.
func (a *testApp) timeComesForSnoozes() {
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := a.h.db.Snoozes().UpdateMany(ctx,
		bson.M{"userId": scenarioUser, "status": "scheduled"},
		bson.M{"$set": bson.M{"wakeAt": time.Now().Add(-time.Minute)}}); err != nil {
		a.fatalf("avance de l'heure des reports : %v", err)
	}
	a.h.wakeDueSnoozes()
}

// senderCard is one row of the Expediteurs screen: the sender key it holds
// and the count it shows, which is also the count the "Tout supprimer" /
// "Tout archiver" confirmation announces.
type senderCard struct {
	SenderEmail string `json:"senderEmail"`
	EmailCount  int    `json:"emailCount"`
}

// senderCardFor returns the Expediteurs row for a sender, as GET /api/senders
// lists it. It fails the scenario if the screen does not list that sender.
func (a *testApp) senderCardFor(from string) senderCard {
	a.t.Helper()
	code, out := a.call(http.MethodGet, "/api/senders", nil)
	if code != http.StatusOK {
		a.fatalf("GET /api/senders a répondu %d : %v", code, out)
	}
	raw, _ := out["_raw"].(string)
	var senders []senderCard
	if err := json.Unmarshal([]byte(raw), &senders); err != nil {
		a.fatalf("réponse de GET /api/senders illisible : %v", err)
	}
	for _, s := range senders {
		if s.SenderEmail == from {
			return s
		}
	}
	a.fatalf("l'écran Expéditeurs ne liste pas %q (il liste %v)", from, senders)
	return senderCard{}
}

// senderKey is the value the Expediteurs screen holds for a sender, and sends
// back for a sender-wide action: the sender's raw From header, as grouped by
// GET /api/senders.
func (a *testApp) senderKey(from string) string {
	a.t.Helper()
	return a.senderCardFor(from).SenderEmail
}

// announcedCount is the number of mails the Expediteurs screen shows for a
// sender, the one its "Tout supprimer" confirmation says will go to the trash.
// A sentence about "what the screen announces" needs it read from the screen's
// own endpoint rather than counted by the scenario.
func (a *testApp) announcedCount(from string) int {
	a.t.Helper()
	return a.senderCardFor(from).EmailCount
}

// deleteAllFrom is "Tout supprimer" on the Expediteurs screen.
func (a *testApp) deleteAllFrom(from string) map[string]interface{} {
	a.t.Helper()
	return a.mustCall(http.MethodPost, "/api/ai/apply-bulk",
		map[string]string{"senderEmail": a.senderKey(from), "action": "delete"}, http.StatusOK)
}

// archiveAllFrom is "Tout archiver" on the Expediteurs screen: the same call as
// "Tout supprimer", with the archive action.
func (a *testApp) archiveAllFrom(from string) map[string]interface{} {
	a.t.Helper()
	return a.mustCall(http.MethodPost, "/api/ai/apply-bulk",
		map[string]string{"senderEmail": a.senderKey(from), "action": "archive"}, http.StatusOK)
}

// expectUnsubscribed checks the "Désabonné" mark on the Abonnements tab: the
// unsubscribed flag GET /api/subscriptions returns for that bare address. The
// tab reloads from that endpoint, so it is what the user sees after a refresh.
func (a *testApp) expectUnsubscribed(addr string) {
	a.t.Helper()
	code, out := a.call(http.MethodGet, "/api/subscriptions", nil)
	if code != http.StatusOK {
		a.fatalf("GET /api/subscriptions a répondu %d : %v", code, out)
	}
	raw, _ := out["_raw"].(string)
	var subs []struct {
		SenderEmail  string `json:"senderEmail"`
		Unsubscribed bool   `json:"unsubscribed"`
	}
	if err := json.Unmarshal([]byte(raw), &subs); err != nil {
		a.fatalf("réponse de GET /api/subscriptions illisible : %v", err)
	}
	for _, s := range subs {
		if s.SenderEmail == addr {
			if !s.Unsubscribed {
				a.failf("l'onglet Abonnements devrait marquer %s \"Désabonné\", il ne le fait pas", addr)
			}
			return
		}
	}
	a.failf("l'onglet Abonnements ne liste pas %s, il ne peut donc pas le marquer \"Désabonné\" (il liste %v)", addr, subs)
}

// unsubscribeAndArchive is "Se désabonner" with "et archiver ses mails".
func (a *testApp) unsubscribeAndArchive(id string) map[string]interface{} {
	a.t.Helper()
	return a.mustCall(http.MethodPost, "/api/unsubscribe",
		map[string]interface{}{"messageId": id, "alsoArchive": true}, http.StatusOK)
}

// rule creates a sorting rule: one condition, then the actions in order.
func (a *testApp) rule(name string, priority int, field, operator, value string, actions ...models.RuleAction) {
	a.t.Helper()
	a.mustCall(http.MethodPost, "/api/rules", models.SortingRuleInput{
		Name:       name,
		Enabled:    true,
		MatchAll:   true,
		Conditions: []models.RuleCondition{{Field: field, Operator: operator, Value: value}},
		Actions:    actions,
		Priority:   priority,
	}, http.StatusCreated, http.StatusOK)
}

// applyRules is the "Appliquer mes règles" button.
func (a *testApp) applyRules() map[string]interface{} {
	a.t.Helper()
	return a.mustCall(http.MethodPost, "/api/rules/apply", nil, http.StatusOK)
}

// setAutoApplyRules flips "Appliquer mes règles automatiquement" in Réglages.
func (a *testApp) setAutoApplyRules(on bool) {
	a.t.Helper()
	a.mustCall(http.MethodPut, "/api/account/settings", map[string]bool{"autoApplyRules": on}, http.StatusOK)
}

// analyze asks the AI about these messages, the way the inbox does.
func (a *testApp) analyze(ids ...string) map[string]interface{} {
	a.t.Helper()
	return a.mustCall(http.MethodPost, "/api/ai/analyze", map[string]interface{}{"emailIds": ids}, http.StatusOK)
}

// suggestionFor returns the AI suggestion stored for a message.
func (a *testApp) suggestionFor(id string) models.AISuggestion {
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var s models.AISuggestion
	if err := a.h.db.AISuggestions().FindOne(ctx, bson.M{"userId": scenarioUser, "emailId": id}).Decode(&s); err != nil {
		a.fatalf("aucune proposition de l'IA pour le mail %s : %v", a.describe(id), err)
	}
	return s
}

// applySuggestion is "Appliquer" on one proposal of the triage plan.
func (a *testApp) applySuggestion(id string) {
	a.t.Helper()
	a.mustCall(http.MethodPost, "/api/ai/apply", map[string]string{"suggestionId": a.suggestionFor(id).ID}, http.StatusOK)
}

// applyAllSuggestions is "Tout ranger" on the triage plan.
func (a *testApp) applyAllSuggestions(ids ...string) map[string]interface{} {
	a.t.Helper()
	sids := make([]string, 0, len(ids))
	for _, id := range ids {
		sids = append(sids, a.suggestionFor(id).ID)
	}
	return a.mustCall(http.MethodPost, "/api/ai/apply-batch", map[string]interface{}{"suggestionIds": sids}, http.StatusOK)
}

// autopilot stores what the Expediteurs screen saves when the user turns on
// "Appliquer automatiquement" for a sender: its raw From header, the action,
// autoApply on.
func (a *testApp) autopilot(from, action string) {
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	now := time.Now()
	if _, err := a.h.db.SenderPreferences().InsertOne(ctx, models.SenderPreference{
		UserID:        scenarioUser,
		SenderEmail:   from,
		SenderDomain:  extractDomain(from),
		SenderName:    extractSenderName(from),
		AutoApply:     true,
		DefaultAction: action,
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		a.fatalf("activation du pilote automatique pour %q : %v", from, err)
	}
}
