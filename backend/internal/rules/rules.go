// Package rules implements Mailsorter's deterministic, AI-free triage engine.
//
// A SortingRule pairs a set of conditions with an action. When the conditions
// match an email, the action is applied directly — without ever calling the
// language model. This gives users instant, predictable, free handling of the
// obvious cases (a noisy sender, a recurring subject), and lets the AI focus on
// the genuinely ambiguous mail. The matcher here is pure (no I/O) so it is
// cheap to test exhaustively.
package rules

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
)

// Supported condition fields.
const (
	FieldFrom    = "from"
	FieldSubject = "subject"
	FieldSnippet = "snippet"
	FieldTo      = "to"
	FieldBody    = "body"
)

// Supported operators.
const (
	OpContains    = "contains"
	OpEquals      = "equals"
	OpStartsWith  = "startsWith"
	OpEndsWith    = "endsWith"
	OpRegex       = "regex"
	OpNotContains = "notContains" // text operators, negated
	OpNotEquals   = "notEquals"
	OpOlderThan   = "olderThan" // value = age in days; matches mail received before now-N days
	OpNewerThan   = "newerThan" // value = age in days; matches mail received within the last N days
)

// textOperators compare a string field; temporalOperators compare ReceivedDate
// against a relative age in days. The two families are validated differently
// (a temporal value must be a positive integer) so they are tracked separately.
var temporalOperators = map[string]bool{
	OpOlderThan: true, OpNewerThan: true,
}

// Supported actions.
const (
	ActionArchive  = "archive"
	ActionTrash    = "trash"
	ActionLabel    = "label"
	ActionMarkRead = "markRead"
	ActionStar     = "star"
)

var validFields = map[string]bool{
	FieldFrom: true, FieldSubject: true, FieldSnippet: true, FieldTo: true, FieldBody: true,
}

var validOperators = map[string]bool{
	OpContains: true, OpEquals: true, OpStartsWith: true, OpEndsWith: true, OpRegex: true,
	OpNotContains: true, OpNotEquals: true, OpOlderThan: true, OpNewerThan: true,
}

var validActions = map[string]bool{
	ActionArchive: true, ActionTrash: true, ActionLabel: true, ActionMarkRead: true, ActionStar: true,
}

// fieldValue extracts the comparable string for a condition field from an email.
func fieldValue(email models.Email, field string) string {
	switch strings.ToLower(field) {
	case FieldFrom:
		return email.From
	case FieldSubject:
		return email.Subject
	case FieldSnippet:
		return email.Snippet
	case FieldBody:
		return email.Body
	case FieldTo:
		return strings.Join(email.To, " ")
	default:
		return ""
	}
}

// matchCondition reports whether a single condition holds for the email,
// evaluating temporal operators against the current time.
func matchCondition(email models.Email, c models.RuleCondition) bool {
	return matchConditionAt(email, c, time.Now())
}

// matchConditionAt reports whether a single condition holds for the email at the
// given reference time. All text operators are case-insensitive except regex,
// which honors the pattern as written. An empty field or value never matches.
// Temporal operators (olderThan / newerThan) compare the email's received date
// against `now` minus the configured number of days.
func matchConditionAt(email models.Email, c models.RuleCondition, now time.Time) bool {
	if strings.TrimSpace(c.Value) == "" || c.Field == "" {
		return false
	}
	if temporalOperators[c.Operator] {
		return matchTemporal(email, c, now)
	}
	actual := fieldValue(email, c.Field)
	switch c.Operator {
	case OpContains:
		return strings.Contains(strings.ToLower(actual), strings.ToLower(c.Value))
	case OpEquals:
		return strings.EqualFold(strings.TrimSpace(actual), strings.TrimSpace(c.Value))
	case OpStartsWith:
		return strings.HasPrefix(strings.ToLower(actual), strings.ToLower(c.Value))
	case OpEndsWith:
		return strings.HasSuffix(strings.ToLower(actual), strings.ToLower(c.Value))
	case OpRegex:
		re, err := regexp.Compile(c.Value)
		if err != nil {
			return false
		}
		return re.MatchString(actual)
	case OpNotContains:
		return !strings.Contains(strings.ToLower(actual), strings.ToLower(c.Value))
	case OpNotEquals:
		return !strings.EqualFold(strings.TrimSpace(actual), strings.TrimSpace(c.Value))
	default:
		return false
	}
}

// matchTemporal evaluates an age-based condition. The condition value is a
// number of days; a malformed value never matches. An email with no received
// date (zero time) never matches a temporal rule, so age conditions can't
// silently act on undated mail.
func matchTemporal(email models.Email, c models.RuleCondition, now time.Time) bool {
	days, err := strconv.Atoi(strings.TrimSpace(c.Value))
	if err != nil || days < 0 {
		return false
	}
	if email.ReceivedDate.IsZero() {
		return false
	}
	cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
	switch c.Operator {
	case OpOlderThan:
		return email.ReceivedDate.Before(cutoff)
	case OpNewerThan:
		return !email.ReceivedDate.Before(cutoff)
	default:
		return false
	}
}

// Matches reports whether the rule applies to the email, resolving temporal
// conditions against the current time.
func Matches(email models.Email, rule models.SortingRule) bool {
	return MatchesAt(email, rule, time.Now())
}

// MatchesAt reports whether the rule applies to the email at reference time
// `now`. A disabled rule, or a rule with no conditions, never matches (so an
// empty rule can't silently act on everything). With MatchAll the conditions
// are AND-ed; otherwise OR-ed. Injecting `now` keeps temporal matching pure and
// deterministic to test.
func MatchesAt(email models.Email, rule models.SortingRule, now time.Time) bool {
	if !rule.Enabled || len(rule.Conditions) == 0 {
		return false
	}
	if rule.MatchAll {
		for _, c := range rule.Conditions {
			if !matchConditionAt(email, c, now) {
				return false
			}
		}
		return true
	}
	for _, c := range rule.Conditions {
		if matchConditionAt(email, c, now) {
			return true
		}
	}
	return false
}

// FirstMatch returns the first rule in slice order that matches the email, or
// nil, resolving temporal conditions against the current time.
func FirstMatch(email models.Email, ruleset []models.SortingRule) *models.SortingRule {
	return FirstMatchAt(email, ruleset, time.Now())
}

// FirstMatchAt returns the first rule in slice order that matches the email at
// reference time `now`, or nil. Callers pass rules pre-sorted by priority (lower
// number = higher priority) so the winning action is deterministic. Returning a
// pointer into the slice lets the caller cheaply update per-rule stats.
func FirstMatchAt(email models.Email, ruleset []models.SortingRule, now time.Time) *models.SortingRule {
	for i := range ruleset {
		if MatchesAt(email, ruleset[i], now) {
			return &ruleset[i]
		}
	}
	return nil
}

// PreviewItem is the projected outcome for a single email under a ruleset: the
// rule that would win and the action it would take. No side effect is implied.
type PreviewItem struct {
	MessageID string `json:"messageId"`
	From      string `json:"from"`
	Subject   string `json:"subject"`
	RuleName  string `json:"ruleName"`
	Action    string `json:"action"`
	LabelName string `json:"labelName,omitempty"`
}

// RuleHits aggregates how many emails a single rule would act on.
type RuleHits struct {
	RuleName  string `json:"ruleName"`
	Action    string `json:"action"`
	LabelName string `json:"labelName,omitempty"`
	Matched   int    `json:"matched"`
}

// Preview runs the ruleset over emails WITHOUT any side effect and reports what
// would happen: one PreviewItem per matched email plus a per-rule tally. It
// mirrors ApplyRules exactly — each email is attributed to its FirstMatch in
// priority order — so the dry-run is a faithful forecast of a real apply.
// Callers pass rules pre-sorted by priority (disabled rules are skipped by the
// matcher). The hits slice preserves the order in which rules first match.
func Preview(emails []models.Email, ruleset []models.SortingRule) ([]PreviewItem, []RuleHits) {
	return PreviewAt(emails, ruleset, time.Now())
}

// PreviewAt is Preview evaluated at reference time `now`, so temporal rules can
// be forecast deterministically.
func PreviewAt(emails []models.Email, ruleset []models.SortingRule, now time.Time) ([]PreviewItem, []RuleHits) {
	items := make([]PreviewItem, 0)
	hits := make([]RuleHits, 0)
	idx := map[string]int{} // rule name -> position in hits

	for _, email := range emails {
		match := FirstMatchAt(email, ruleset, now)
		if match == nil {
			continue
		}
		items = append(items, PreviewItem{
			MessageID: email.MessageID,
			From:      email.From,
			Subject:   email.Subject,
			RuleName:  match.Name,
			Action:    match.Action,
			LabelName: match.LabelName,
		})
		if i, ok := idx[match.Name]; ok {
			hits[i].Matched++
			continue
		}
		idx[match.Name] = len(hits)
		hits = append(hits, RuleHits{
			RuleName:  match.Name,
			Action:    match.Action,
			LabelName: match.LabelName,
			Matched:   1,
		})
	}
	return items, hits
}

// Validate checks a rule is well-formed before persistence, returning a
// human-readable (French) error when it is not.
func Validate(rule models.SortingRule) error {
	if strings.TrimSpace(rule.Name) == "" {
		return fmt.Errorf("le nom de la règle est requis")
	}
	if !validActions[rule.Action] {
		return fmt.Errorf("action invalide : %q", rule.Action)
	}
	if rule.Action == ActionLabel && strings.TrimSpace(rule.LabelName) == "" {
		return fmt.Errorf("un libellé est requis pour l'action \"label\"")
	}
	if len(rule.Conditions) == 0 {
		return fmt.Errorf("au moins une condition est requise")
	}
	for i, c := range rule.Conditions {
		if !validFields[strings.ToLower(c.Field)] {
			return fmt.Errorf("condition %d : champ invalide %q", i+1, c.Field)
		}
		if !validOperators[c.Operator] {
			return fmt.Errorf("condition %d : opérateur invalide %q", i+1, c.Operator)
		}
		if strings.TrimSpace(c.Value) == "" {
			return fmt.Errorf("condition %d : la valeur est requise", i+1)
		}
		if c.Operator == OpRegex {
			if _, err := regexp.Compile(c.Value); err != nil {
				return fmt.Errorf("condition %d : expression régulière invalide : %v", i+1, err)
			}
		}
		if temporalOperators[c.Operator] {
			if n, err := strconv.Atoi(strings.TrimSpace(c.Value)); err != nil || n < 0 {
				return fmt.Errorf("condition %d : un nombre de jours (≥ 0) est requis pour cet opérateur", i+1)
			}
		}
	}
	return nil
}
