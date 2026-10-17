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

func TestMain(m *testing.M) {
	code := m.Run()
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
