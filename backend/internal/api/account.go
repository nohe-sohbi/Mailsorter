package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/activity"
	"github.com/nohe-sohbi/mailsorter/backend/internal/digest"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// FreeMonthlyLimit is how many AI-analyzed emails a free account gets per month.
// Cache hits and sender auto-pilot do NOT count against it.
const FreeMonthlyLimit = 200

func currentPeriod() string { return time.Now().UTC().Format("2006-01") }

func (h *Handler) getUsage(ctx context.Context, userEmail string) int {
	var doc struct {
		Analyzed int `bson:"analyzed"`
	}
	if err := h.db.Usage().FindOne(ctx, bson.M{"userId": userEmail, "period": currentPeriod()}).Decode(&doc); err != nil {
		return 0
	}
	return doc.Analyzed
}

func (h *Handler) incrUsage(ctx context.Context, userEmail string, n int) {
	if n <= 0 {
		return
	}
	h.db.Usage().UpdateOne(ctx,
		bson.M{"userId": userEmail, "period": currentPeriod()},
		bson.M{"$inc": bson.M{"analyzed": n}, "$set": bson.M{"updatedAt": time.Now()}},
		options.Update().SetUpsert(true),
	)
}

// quotaExceeded is true only for free users who have spent their monthly budget.
// Pro is unlimited.
func (h *Handler) quotaExceeded(ctx context.Context, userEmail string) bool {
	if h.getPlan(ctx, userEmail) == PlanPro {
		return false
	}
	return h.getUsage(ctx, userEmail) >= FreeMonthlyLimit
}

// GetUsage reports this month's AI usage against the plan's limit.
func (h *Handler) GetUsage(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	used := h.getUsage(ctx, userEmail)
	plan := h.getPlan(ctx, userEmail)
	limit := FreeMonthlyLimit
	if plan == PlanPro {
		limit = -1 // unlimited
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"used":      used,
		"limit":     limit,
		"period":    currentPeriod(),
		"plan":      plan,
		"billingOn": h.billing.Client != nil && h.billing.PriceID != "",
	})
}

// autoApplyRulesEnabled reports whether the user opted into running their
// deterministic rules automatically on every sync.
func (h *Handler) autoApplyRulesEnabled(ctx context.Context, userEmail string) bool {
	return h.userSettings(ctx, userEmail).AutoApplyRules
}

// userSettings loads the caller's tunable settings, applying the server default
// digest hour when the user has not picked one (a stored 0 is treated as
// "unset" rather than midnight, which is rarely what a user means).
func (h *Handler) userSettings(ctx context.Context, userEmail string) models.UserSettings {
	var doc struct {
		AutoApplyRules bool `bson:"autoApplyRules"`
		DigestEnabled  bool `bson:"digestEnabled"`
		DigestHourUTC  int  `bson:"digestHourUTC"`
	}
	if err := h.db.Users().FindOne(ctx, bson.M{"email": userEmail}).Decode(&doc); err != nil {
		return models.UserSettings{DigestHourUTC: defaultDigestHour()}
	}
	hour := doc.DigestHourUTC
	if hour <= 0 || hour > 23 {
		hour = defaultDigestHour()
	}
	return models.UserSettings{
		AutoApplyRules: doc.AutoApplyRules,
		DigestEnabled:  doc.DigestEnabled,
		DigestHourUTC:  hour,
	}
}

// GetSettings returns the caller's tunable account settings.
func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(h.userSettings(ctx, userEmail))
}

// UpdateSettings persists the caller's tunable account settings.
func (h *Handler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	var in models.UserSettings
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// A digest hour must be a valid hour of day; fall back to the server default.
	hour := in.DigestHourUTC
	if hour < 0 || hour > 23 {
		hour = defaultDigestHour()
	}
	in.DigestHourUTC = hour

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := h.db.Users().UpdateOne(ctx,
		bson.M{"email": userEmail},
		bson.M{"$set": bson.M{
			"autoApplyRules": in.AutoApplyRules,
			"digestEnabled":  in.DigestEnabled,
			"digestHourUTC":  in.DigestHourUTC,
			"updatedAt":      time.Now(),
		}},
	)
	if err != nil {
		http.Error(w, "Failed to update settings", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(in)
}

// GetActivity returns triage activity for the last 7 days from the action
// ledger: a per-day series plus breakdowns by action and by source. Reading the
// ledger (rather than only applied AI suggestions) means the recap now counts
// every mutation — direct actions, rules, bulk sweeps, snoozes, unsubscribes —
// not just the ones the AI suggested.
func (h *Handler) GetActivity(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	summary, err := h.activitySummary(ctx, userEmail)
	if err != nil {
		http.Error(w, "Failed to load activity", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(summary)
}

// activitySummary loads the trailing-7-day action ledger for a user and folds
// it into the recap. Shared by GetActivity and GetDigest so both render from
// the exact same numbers.
func (h *Handler) activitySummary(ctx context.Context, userEmail string) (activity.Summary, error) {
	since := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -6)

	cursor, err := h.db.ActionLog().Find(ctx, bson.M{
		"userId":    userEmail,
		"createdAt": bson.M{"$gte": since},
	}, options.Find().SetLimit(20000))
	if err != nil {
		return activity.Summary{}, err
	}
	defer cursor.Close(ctx)

	var logs []models.ActionLog
	if err := cursor.All(ctx, &logs); err != nil {
		return activity.Summary{}, err
	}

	rows := make([]activity.Row, 0, len(logs))
	for _, l := range logs {
		rows = append(rows, activity.Row{At: l.CreatedAt, Action: l.Action, Source: l.Source})
	}
	return activity.Summarize(rows, time.Now()), nil
}

// GetDigest renders the same 7-day recap as GetActivity into a ready-to-send
// email digest (subject + plain-text body + HTML body). This is the "Digest
// quotidien par email" content: previewable now, and the payload a scheduler
// would hand to a gmail.send (or SMTP) sender once that scope is wired.
func (h *Handler) GetDigest(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	summary, err := h.activitySummary(ctx, userEmail)
	if err != nil {
		http.Error(w, "Failed to load activity", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(digest.Render(summary, time.Now()))
}
