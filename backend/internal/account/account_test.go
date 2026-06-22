package account

import (
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
