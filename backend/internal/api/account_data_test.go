package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/account"
	"go.mongodb.org/mongo-driver/bson"
)

// The receiver is not in the pattern: pin it to `d` and an accessor on another
// receiver stops matching, which reads as a smaller datastore rather than a hole.
var collectionAccessor = regexp.MustCompile(`\.DB\.Collection\("([^"]+)"\)`)

// notUserScoped are the collections the catalog must NOT name, each for a stated
// reason. Erasure in DeleteAccount is a DeleteMany on userId, so a collection
// keyed any other way can only be handled by hand, and pretending otherwise would
// report a deletion count of zero as a success. Expected to stay at four: a wrong
// entry is the one failure no test can catch.
var notUserScoped = map[string]string{
	"users":          "keyed by email, erased separately as the account record itself",
	"gmail_config":   "instance-wide legacy fallback, one document for every user",
	"analysis_cache": "shared across all users on purpose, keyed by sender and subject",
	"waitlist":       "logged-out visitors, the email address is the identity",
}

// The inverse of TestEveryDatasetHasABackingCollection, which walks Datasets() and
// so cannot see a collection nobody declared. That hole was ai_settings.
func TestCatalogCoversEveryUserScopedCollection(t *testing.T) {
	h := newTestHandler(t)

	src, err := os.ReadFile(filepath.Join("..", "database", "database.go"))
	if err != nil {
		t.Fatalf("read ../database/database.go: %v", err)
	}
	matches := collectionAccessor.FindAllStringSubmatch(string(src), -1)
	declared := make(map[string]bool, len(matches))
	for _, m := range matches {
		declared[m[1]] = true
	}

	// Partial staleness must fail too, not only a pattern that stopped matching.
	if calls := strings.Count(string(src), ".Collection("); len(matches) != calls {
		t.Fatalf("matched %d of the %d collection accessors in database.go: the pattern is stale, so a collection can be declared where this test cannot see it",
			len(matches), calls)
	}

	covered := map[string]bool{}
	for _, ds := range account.Datasets() {
		coll := h.datasetCollection(ds)
		if coll == nil {
			t.Errorf("dataset %q maps to no collection: export and erasure both skip it", ds)
			continue
		}
		name := coll.Name()
		if reason, exempt := notUserScoped[name]; exempt {
			t.Errorf("dataset %q resolves to %q, which is not user-scoped (%s): erasure would delete nothing and still count it as a success", ds, name, reason)
		}
		// A name no accessor defines is a typo both export and erasure query forever.
		if !declared[name] {
			t.Errorf("datasetCollection maps dataset %q to collection %q, which database.go has no accessor for", ds, name)
		}
		covered[name] = true
	}

	for name := range declared {
		if _, exempt := notUserScoped[name]; exempt {
			continue
		}
		if !covered[name] {
			t.Errorf("collection %q is scoped by userId but absent from account.Datasets(): it is neither exported nor erased, and account deletion reports success", name)
		}
	}
}

func TestAISettingsAreErasableButTheirKeyIsNotExportable(t *testing.T) {
	h := newTestHandler(t)

	coll := h.datasetCollection(account.DatasetAISettings)
	if coll == nil {
		t.Fatal("datasetCollection has no entry for aiSettings; export and erasure would silently skip it")
	}
	if got, want := coll.Name(), h.db.AISettings().Name(); got != want {
		t.Errorf("datasetCollection(aiSettings) = %q, want %q", got, want)
	}

	if got := account.SecretFields(account.DatasetAISettings); len(got) != 1 || got[0] != "apiKey" {
		t.Errorf("SecretFields(aiSettings) = %v, want [apiKey]", got)
	}
}

func TestRedactRowsStripsTheNamedFields(t *testing.T) {
	rows := []bson.M{
		{"userId": "me@example.com", "provider": "openai", "apiKey": "enc:v1:cipher", "model": "gpt-4o-mini"},
		{"userId": "me@example.com", "provider": "orange", "secret": "enc:v1:cipher", "username": "me@orange.fr"},
	}

	got := redactRows(rows, "apiKey", "secret")

	if _, leaked := got[0]["apiKey"]; leaked {
		t.Error("redactRows left apiKey on the row: the export would ship the sealed BYOK key")
	}
	if _, leaked := got[1]["secret"]; leaked {
		t.Error("redactRows left secret on the row: the export would ship the sealed app password")
	}

	// Redaction is not erasure: the rest of the row must survive.
	for _, tc := range []struct {
		row   bson.M
		field string
		want  string
	}{
		{got[0], "provider", "openai"},
		{got[0], "model", "gpt-4o-mini"},
		{got[1], "username", "me@orange.fr"},
		{got[0], "userId", "me@example.com"},
	} {
		if v, _ := tc.row[tc.field].(string); v != tc.want {
			t.Errorf("redactRows dropped or altered %q: got %q, want %q", tc.field, v, tc.want)
		}
	}
}

func TestRedactRowsToleratesMissingFields(t *testing.T) {
	rows := []bson.M{{"userId": "me@example.com"}, {}}

	got := redactRows(rows, "apiKey", "secret")

	if len(got) != 2 {
		t.Fatalf("redactRows changed the row count: got %d, want 2", len(got))
	}
	if len(got[0]) != 1 || got[0]["userId"] != "me@example.com" {
		t.Errorf("redactRows altered a row that held no secret: %+v", got[0])
	}
}

// The whole point of account.Datasets is that one list drives both the RGPD
// export and the account deletion. That only holds if every entry actually
// resolves to a collection here: a dataset with no mapping is skipped by both,
// silently, which is exactly the failure mode the catalog exists to prevent.
func TestEveryDatasetHasABackingCollection(t *testing.T) {
	h := newTestHandler(t)

	for _, ds := range account.Datasets() {
		if h.datasetCollection(ds) == nil {
			t.Errorf("dataset %q maps to no collection: export and erasure both skip it", ds)
		}
	}
}

// The mailbox mirror holds the decoded body of every synced message. It was
// absent from the catalog, so "supprimer definitivement mon compte" removed the
// profile and the settings and left the full text of the user's mail in the
// database. Pin it: the catalog must carry the collections that hold mail.
func TestCatalogCoversTheMailboxMirror(t *testing.T) {
	h := newTestHandler(t)

	wantSame := map[account.Dataset]string{
		account.DatasetEmails: h.db.Emails().Name(),
		account.DatasetLabels: h.db.Labels().Name(),
	}

	present := map[account.Dataset]bool{}
	for _, ds := range account.Datasets() {
		present[ds] = true
	}

	for ds, collName := range wantSame {
		if !present[ds] {
			t.Errorf("dataset %q missing from account.Datasets(): export and erasure skip it", ds)
			continue
		}
		if got := h.datasetCollection(ds); got == nil || got.Name() != collName {
			t.Errorf("datasetCollection(%q) does not resolve to the %q collection", ds, collName)
		}
	}
}
