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
	"strings"

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
	OpContains   = "contains"
	OpEquals     = "equals"
	OpStartsWith = "startsWith"
	OpEndsWith   = "endsWith"
	OpRegex      = "regex"
)

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

// matchCondition reports whether a single condition holds for the email.
// All operators are case-insensitive except regex, which honors the pattern as
// written. An empty field or value never matches.
func matchCondition(email models.Email, c models.RuleCondition) bool {
	if strings.TrimSpace(c.Value) == "" || c.Field == "" {
		return false
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
	default:
		return false
	}
}

// Matches reports whether the rule applies to the email. A disabled rule, or a
// rule with no conditions, never matches (so an empty rule can't silently act
// on everything). With MatchAll the conditions are AND-ed; otherwise OR-ed.
func Matches(email models.Email, rule models.SortingRule) bool {
	if !rule.Enabled || len(rule.Conditions) == 0 {
		return false
	}
	if rule.MatchAll {
		for _, c := range rule.Conditions {
			if !matchCondition(email, c) {
				return false
			}
		}
		return true
	}
	for _, c := range rule.Conditions {
		if matchCondition(email, c) {
			return true
		}
	}
	return false
}

// FirstMatch returns the first rule in slice order that matches the email, or
// nil. Callers pass rules pre-sorted by priority (lower number = higher
// priority) so the winning action is deterministic. Returning a pointer into
// the slice lets the caller cheaply update per-rule stats.
func FirstMatch(email models.Email, ruleset []models.SortingRule) *models.SortingRule {
	for i := range ruleset {
		if Matches(email, ruleset[i]) {
			return &ruleset[i]
		}
	}
	return nil
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
		if strings.ToLower(c.Operator) == OpRegex {
			if _, err := regexp.Compile(c.Value); err != nil {
				return fmt.Errorf("condition %d : expression régulière invalide : %v", i+1, err)
			}
		}
	}
	return nil
}
