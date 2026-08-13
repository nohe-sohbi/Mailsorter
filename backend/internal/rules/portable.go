package rules

import (
	"fmt"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
)

// A ruleset is the part of Mailsorter a user actually authors: it encodes how
// they want their mail handled, and until now it existed in exactly one place,
// with no way to back it up, move it, or hand it to someone else. Everything
// below is the portable form of that ruleset, and it is pure so the shape of an
// export (and what a safe import accepts) can be tested without a database.

// ExportVersion is the schema version stamped into every export. It exists so a
// future incompatible change can be detected at import time and refused with a
// clear message, rather than silently producing rules that mean something else.
const ExportVersion = 1

// MaxImportRules bounds one import. A ruleset is authored by hand: a file with
// thousands of entries is a mistake or an attack, not a migration.
const MaxImportRules = 200

// PortableRule is a rule stripped of everything that belongs to one account and
// one database: no id, no userId, no timestamps, no applied counter. What is
// left is the user's intent, which is the only part worth moving.
type PortableRule struct {
	Name       string                 `json:"name"`
	Enabled    bool                   `json:"enabled"`
	MatchAll   bool                   `json:"matchAll"`
	Conditions []models.RuleCondition `json:"conditions"`
	Actions    []models.RuleAction    `json:"actions"`
	Priority   int                    `json:"priority"`
}

// Export is the whole document a user downloads.
type Export struct {
	Version    int            `json:"version"`
	ExportedAt time.Time      `json:"exportedAt"`
	Rules      []PortableRule `json:"rules"`
}

// ToPortable projects stored rules onto their portable form, resolving both
// authoring shapes (multi-action list, legacy single action) through
// EffectiveActions so an export never depends on how a rule happened to be
// written.
func ToPortable(ruleset []models.SortingRule) []PortableRule {
	out := make([]PortableRule, 0, len(ruleset))
	for _, r := range ruleset {
		out = append(out, PortableRule{
			Name:       r.Name,
			Enabled:    r.Enabled,
			MatchAll:   r.MatchAll,
			Conditions: append([]models.RuleCondition(nil), r.Conditions...),
			Actions:    EffectiveActions(r),
			Priority:   r.Priority,
		})
	}
	return out
}

// BuildExport wraps a ruleset into a versioned, timestamped document.
func BuildExport(ruleset []models.SortingRule, now time.Time) Export {
	return Export{
		Version:    ExportVersion,
		ExportedAt: now.UTC(),
		Rules:      ToPortable(ruleset),
	}
}

// FromPortable turns one portable rule into a rule owned by userEmail. It
// deliberately sets no id and no applied counter: an imported rule is a NEW rule
// in this account, not a claim on someone else's history. The legacy
// Action/LabelName pair is backfilled from the primary action so every reader
// (including per-rule stats) keeps working.
func FromPortable(userEmail string, p PortableRule, now time.Time) models.SortingRule {
	rule := models.SortingRule{
		UserID:     userEmail,
		Name:       p.Name,
		Enabled:    p.Enabled,
		MatchAll:   p.MatchAll,
		Conditions: append([]models.RuleCondition(nil), p.Conditions...),
		Actions:    append([]models.RuleAction(nil), p.Actions...),
		Priority:   p.Priority,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	rule.Action, rule.LabelName = primaryAction(EffectiveActions(rule))
	return rule
}

// ValidateImport checks a whole document and returns the rules it would create,
// or the first reason it cannot.
//
// It is all-or-nothing on purpose: a half-applied import leaves a user with a
// ruleset that is neither the old one nor the one in the file, and no way to
// tell which rules made it. Every entry is named in its error so the file can be
// fixed by hand.
func ValidateImport(doc Export, userEmail string, now time.Time) ([]models.SortingRule, error) {
	if doc.Version <= 0 {
		return nil, fmt.Errorf("ce fichier n'est pas un export de règles Mailsorter")
	}
	if doc.Version > ExportVersion {
		return nil, fmt.Errorf("ce fichier vient d'une version plus récente de Mailsorter (format %d, cette instance lit le format %d)", doc.Version, ExportVersion)
	}
	if len(doc.Rules) == 0 {
		return nil, fmt.Errorf("ce fichier ne contient aucune règle")
	}
	if len(doc.Rules) > MaxImportRules {
		return nil, fmt.Errorf("ce fichier contient %d règles, la limite est de %d", len(doc.Rules), MaxImportRules)
	}

	out := make([]models.SortingRule, 0, len(doc.Rules))
	for i, p := range doc.Rules {
		rule := FromPortable(userEmail, p, now)
		if err := Validate(rule); err != nil {
			return nil, fmt.Errorf("règle %d (%s) : %v", i+1, describeRule(p), err)
		}
		out = append(out, rule)
	}
	return out, nil
}

// describeRule names a rule in an error message, falling back to a placeholder
// when the entry has no name (which is itself one of the things Validate
// rejects, so the message has to survive it).
func describeRule(p PortableRule) string {
	if p.Name == "" {
		return "sans nom"
	}
	return p.Name
}

// Reorder returns the priority every rule should carry so the ruleset runs in
// the requested order (lower first, which is what FirstMatch walks).
//
// It is total by construction: ids the account does not own are ignored, and
// rules the client did not mention keep their relative order behind the ones it
// did. A partial or stale list can therefore never drop a rule out of the
// ruleset or collapse two rules onto the same priority, which would make which
// one wins depend on the database's tie-break.
func Reorder(ruleset []models.SortingRule, orderedIDs []string) map[string]int {
	known := make(map[string]bool, len(ruleset))
	for _, r := range ruleset {
		if r.ID != "" {
			known[r.ID] = true
		}
	}

	priorities := make(map[string]int, len(ruleset))
	next := 0
	for _, id := range orderedIDs {
		if !known[id] {
			continue // not ours, or already placed
		}
		priorities[id] = next
		next++
		known[id] = false
	}
	// Whatever the caller left out keeps the order it already had.
	for _, r := range ruleset {
		if r.ID == "" || !known[r.ID] {
			continue
		}
		priorities[r.ID] = next
		next++
	}
	return priorities
}

// DuplicateName builds the name of a copy, avoiding a collision with the names
// already in use: "Factures" becomes "Factures (copie)", then "(copie 2)". Two
// rules may legally share a name in storage, but the per-rule apply counter is
// keyed by name, so a duplicate would otherwise silently share its parent's
// statistics.
func DuplicateName(original string, taken []string) string {
	used := make(map[string]bool, len(taken))
	for _, t := range taken {
		used[t] = true
	}
	candidate := original + " (copie)"
	for i := 2; used[candidate]; i++ {
		candidate = fmt.Sprintf("%s (copie %d)", original, i)
	}
	return candidate
}
