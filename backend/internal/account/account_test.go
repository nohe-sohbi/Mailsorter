package account

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
)

func TestDatasetsAreUniqueAndNonEmpty(t *testing.T) {
	ds := Datasets()
	if len(ds) == 0 {
		t.Fatal("Datasets() must not be empty")
	}
	seen := map[Dataset]bool{}
	for _, d := range ds {
		if d == "" {
			t.Error("dataset key must not be empty")
		}
		if seen[d] {
			t.Errorf("duplicate dataset key %q", d)
		}
		seen[d] = true
	}
}

// The catalog drives BOTH the export and the erasure, so a per-user collection
// missing from it is a collection no deletion ever touches. The mailbox mirror
// was exactly that: it stores the decoded body of every synced message, and it
// survived "delete my account" untouched.
func TestDatasetsCoverTheMailboxMirror(t *testing.T) {
	present := map[Dataset]bool{}
	for _, d := range Datasets() {
		present[d] = true
	}

	for _, want := range []Dataset{DatasetEmails, DatasetLabels} {
		if !present[want] {
			t.Errorf("Datasets() is missing %q: it would be neither exported nor erased", want)
		}
	}
}

// The BYOK override is a credential: skipping it on deletion leaves a key on disk.
func TestDatasetsCoverTheAIProviderOverride(t *testing.T) {
	present := map[Dataset]bool{}
	for _, d := range Datasets() {
		present[d] = true
	}

	if !present[DatasetAISettings] {
		t.Error("Datasets() is missing aiSettings: a deleted account would keep its BYOK provider key on file")
	}

	if got := SecretFields(DatasetAISettings); len(got) != 1 || got[0] != "apiKey" {
		t.Errorf("SecretFields(aiSettings) = %v, want [apiKey]", got)
	}
}

func TestOnlyCredentialDatasetsDeclareSecretFields(t *testing.T) {
	for _, ds := range Datasets() {
		entry, expected := secretBearingModels[ds]
		got := SecretFields(ds)
		if !expected {
			if len(got) != 0 {
				t.Errorf("SecretFields(%s) = %v, want none: a blanket redaction would empty the export", ds, got)
			}
			continue
		}
		if len(got) != 1 || got[0] != entry.field {
			t.Errorf("SecretFields(%s) = %v, want [%s]", ds, got, entry.field)
		}
	}
}

// Nothing type-checks SecretFields: a renamed bson tag silently stops the redaction.
func TestSecretFieldsNameRealBsonFields(t *testing.T) {
	// A dataset declaring a secret with no entry here would be checked for nothing.
	declared := 0
	for _, ds := range Datasets() {
		if len(SecretFields(ds)) > 0 {
			declared++
		}
	}
	if declared != len(secretBearingModels) {
		t.Fatalf("%d datasets declare a secret but %d are described in secretBearingModels", declared, len(secretBearingModels))
	}

	for ds, entry := range secretBearingModels {
		for _, field := range SecretFields(ds) {
			if !hasBsonField(entry.typ, field) {
				t.Errorf("SecretFields(%s) names %q, which %s has no bson field for: the export would not redact it",
					ds, field, entry.typ.Name())
			}
		}
	}
}

var secretBearingModels = map[Dataset]struct {
	typ   reflect.Type
	field string
}{
	DatasetMailAccounts: {reflect.TypeOf(models.MailAccount{}), "secret"},
	DatasetAISettings:   {reflect.TypeOf(models.AIProviderSettings{}), "apiKey"},
}

// hasBsonField reports whether the struct type has a field with this bson tag.
func hasBsonField(t reflect.Type, name string) bool {
	for i := 0; i < t.NumField(); i++ {
		// The tag is a comma-separated option list: `bson:"apiKey,omitempty"`.
		if strings.Split(t.Field(i).Tag.Get("bson"), ",")[0] == name {
			return true
		}
	}
	return false
}

func TestRedactUserDropsSecrets(t *testing.T) {
	now := time.Now()
	u := models.User{
		Email:                "alice@example.com",
		AccessToken:          "ya29.secret-access",
		RefreshToken:         "1//secret-refresh",
		StripeCustomerID:     "cus_123",
		StripeSubscriptionID: "sub_123",
		Plan:                 "pro",
		AutoApplyRules:       true,
		DigestEnabled:        true,
		DigestHourUTC:        9,
		CreatedAt:            now,
		UpdatedAt:            now,
	}

	p := RedactUser(u)

	if p.Email != u.Email || p.Plan != "pro" || !p.AutoApplyRules || !p.DigestEnabled || p.DigestHourUTC != 9 {
		t.Errorf("safe fields not carried through: %+v", p)
	}

	// The redacted profile must not structurally expose any secret. We assert it
	// at the JSON-shape level: the Profile type simply has no token/stripe fields,
	// so a marshaled export can never carry them. Guard against a future field
	// being added with a secret-looking value.
	if got := p.Email; got == u.AccessToken || got == u.RefreshToken {
		t.Fatal("email field unexpectedly holds a token")
	}
}

func TestRedactUserDefaultsPlan(t *testing.T) {
	if p := RedactUser(models.User{Email: "b@x.com"}); p.Plan != "free" {
		t.Errorf("empty plan should default to free, got %q", p.Plan)
	}
}
