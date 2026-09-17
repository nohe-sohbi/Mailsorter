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
	DatasetSavedSearches    Dataset = "savedSearches"
	// DatasetEmails is the local mirror of the synced mailbox, and it holds the
	// DECODED body of every message, which makes it the most sensitive thing
	// Mailsorter stores. It was missing from this catalog, so deleting an account
	// wiped the profile and settings while leaving the full text of the user's
	// mail in the database indefinitely, under a screen promising erasure.
	DatasetEmails Dataset = "emails"
	// DatasetLabels is the per-user label cache. Nothing writes it today, so it
	// exports as an empty list, but the collection and its unique index exist in
	// every deployment: listing it here means the day something does start
	// writing it, export and erasure already cover it instead of silently
	// drifting apart.
	DatasetLabels Dataset = "labels"
	// DatasetMailAccounts is the mailbox connection: which provider, over which
	// transport, under which username. It is listed here because erasure must
	// reach it (an account deleted while its app password stays on file is the
	// exact bug the last audit found in this catalog), and its one secret field
	// is redacted on the way out by SecretFields.
	DatasetMailAccounts Dataset = "mailAccounts"
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
		DatasetSavedSearches,
		DatasetMailAccounts,
		// Last, because they are the bulkiest: the settings a user recognizes
		// should come first in an export they open themselves.
		DatasetLabels,
		DatasetEmails,
	}
}

// SecretFields names the BSON fields of a dataset that must never leave the
// server, not even in the owner's own export.
//
// Export and erasure share one catalog on purpose, which means adding a
// collection makes its rows exportable by default. That default is right for
// every dataset here but one: a mailbox connection carries a sealed app
// password. Sealed is not the same as safe to hand out, because a downloaded
// export outlives the encryption key's threat model: it ends up in a mail
// attachment, a backup, a support ticket.
//
// It lives beside the catalog rather than in the API layer so that adding a
// dataset and declaring its secrets are the same act, in the same file.
func SecretFields(ds Dataset) []string {
	if ds == DatasetMailAccounts {
		return []string{"secret"}
	}
	return nil
}

// Profile is the redacted view of a user's account record, safe to include in an
// export. It deliberately omits OAuth tokens and Stripe identifiers: secrets a
// user's own data export must never leak, even to the user.
type Profile struct {
	Email           string    `json:"email"`
	Plan            string    `json:"plan"`
	AutoApplyRules  bool      `json:"autoApplyRules"`
	AutoSyncEnabled bool      `json:"autoSyncEnabled"`
	DigestEnabled   bool      `json:"digestEnabled"`
	DigestHourUTC   int       `json:"digestHourUTC"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// RedactUser projects a stored User onto the safe Profile, dropping the OAuth
// access/refresh tokens and Stripe customer/subscription IDs.
func RedactUser(u models.User) Profile {
	plan := u.Plan
	if plan == "" {
		plan = "free"
	}
	return Profile{
		Email:           u.Email,
		Plan:            plan,
		AutoApplyRules:  u.AutoApplyRules,
		AutoSyncEnabled: u.AutoSyncEnabled,
		DigestEnabled:   u.DigestEnabled,
		DigestHourUTC:   u.DigestHourUTC,
		CreatedAt:       u.CreatedAt,
		UpdatedAt:       u.UpdatedAt,
	}
}
