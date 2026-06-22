// Package account holds the pure, I/O-free pieces of Mailsorter's GDPR / RGPD
// surface: the canonical catalog of a user's data and the redaction that keeps
// secrets out of an export.
//
// A single source of truth (Datasets) drives BOTH the data export and the
// account deletion, so the two can never drift: we never hand back data we can't
// delete, nor silently delete data we never disclosed. Keeping it pure means the
// invariant is cheap to test.
package account

import (
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
)

// Dataset is a logical category of user-owned data. Each maps (in the API layer)
// to exactly one MongoDB collection scoped by `userId`.
type Dataset string

const (
	DatasetRules            Dataset = "rules"
	DatasetProtectedSenders Dataset = "protectedSenders"
	DatasetSnoozes          Dataset = "snoozes"
	DatasetSuggestions      Dataset = "suggestions"
	DatasetSenderPrefs      Dataset = "senderPreferences"
	DatasetSmartLabels      Dataset = "smartLabels"
	DatasetUnsubscribes     Dataset = "unsubscribes"
	DatasetUsage            Dataset = "usage"
	DatasetActionLog        Dataset = "actionLog"
	DatasetJobs             Dataset = "analysisJobs"
)

// Datasets returns the canonical, stable list of user-owned data categories. The
// order is the export's presentation order. Adding a new per-user collection?
// Add it here once and both export and deletion pick it up.
func Datasets() []Dataset {
	return []Dataset{
		DatasetRules,
		DatasetProtectedSenders,
		DatasetSnoozes,
		DatasetSuggestions,
		DatasetSenderPrefs,
		DatasetSmartLabels,
		DatasetUnsubscribes,
		DatasetUsage,
		DatasetActionLog,
		DatasetJobs,
	}
}

// Profile is the redacted view of a user's account record, safe to include in an
// export. It deliberately omits OAuth tokens and Stripe identifiers — secrets a
// user's own data export must never leak, even to the user.
type Profile struct {
	Email          string    `json:"email"`
	Plan           string    `json:"plan"`
	AutoApplyRules bool      `json:"autoApplyRules"`
	DigestEnabled  bool      `json:"digestEnabled"`
	DigestHourUTC  int       `json:"digestHourUTC"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// RedactUser projects a stored User onto the safe Profile, dropping the OAuth
// access/refresh tokens and Stripe customer/subscription IDs.
func RedactUser(u models.User) Profile {
	plan := u.Plan
	if plan == "" {
		plan = "free"
	}
	return Profile{
		Email:          u.Email,
		Plan:           plan,
		AutoApplyRules: u.AutoApplyRules,
		DigestEnabled:  u.DigestEnabled,
		DigestHourUTC:  u.DigestHourUTC,
		CreatedAt:      u.CreatedAt,
		UpdatedAt:      u.UpdatedAt,
	}
}
