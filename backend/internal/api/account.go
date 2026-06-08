package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

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

// GetActivity returns triage activity for the last 7 days: a per-day series and
// a breakdown by action. Powers the in-app weekly recap.
func (h *Handler) GetActivity(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	since := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -6)

	cursor, err := h.db.AISuggestions().Find(ctx, bson.M{
		"userId":    userEmail,
		"status":    "applied",
		"appliedAt": bson.M{"$gte": since},
	}, options.Find().SetLimit(5000))
	if err != nil {
		http.Error(w, "Failed to load activity", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var rows []struct {
		AppliedAt time.Time `bson:"appliedAt"`
		Action    string    `bson:"action"`
	}
	if err := cursor.All(ctx, &rows); err != nil {
		http.Error(w, "Failed to decode activity", http.StatusInternalServerError)
		return
	}

	dayCounts := map[string]int{}
	byAction := map[string]int{"archive": 0, "delete": 0, "label": 0, "keep": 0}
	total := 0
	for _, row := range rows {
		dayCounts[row.AppliedAt.UTC().Format("2006-01-02")]++
		byAction[row.Action]++
		total++
	}

	days := make([]map[string]interface{}, 0, 7)
	for i := 6; i >= 0; i-- {
		key := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -i).Format("2006-01-02")
		days = append(days, map[string]interface{}{"date": key, "count": dayCounts[key]})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"total":    total,
		"days":     days,
		"byAction": byAction,
	})
}
