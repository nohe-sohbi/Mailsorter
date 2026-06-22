package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/account"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// datasetCollection maps a logical user-data category to its backing collection.
// It is the one place that bridges the pure account.Dataset catalog to Mongo, so
// export and deletion share an identical, exhaustive view of a user's data.
func (h *Handler) datasetCollection(ds account.Dataset) *mongo.Collection {
	switch ds {
	case account.DatasetRules:
		return h.db.SortingRules()
	case account.DatasetProtectedSenders:
		return h.db.ProtectedSenders()
	case account.DatasetSnoozes:
		return h.db.Snoozes()
	case account.DatasetSuggestions:
		return h.db.AISuggestions()
	case account.DatasetSenderPrefs:
		return h.db.SenderPreferences()
	case account.DatasetSmartLabels:
		return h.db.SmartLabels()
	case account.DatasetUnsubscribes:
		return h.db.Unsubscribes()
	case account.DatasetUsage:
		return h.db.Usage()
	case account.DatasetActionLog:
		return h.db.ActionLog()
	case account.DatasetJobs:
		return h.db.AnalysisJobs()
	}
	return nil
}

// ExportAccount returns a single JSON document with everything Mailsorter stores
// about the caller: a redacted account profile plus every user-owned dataset.
// It is the data-portability half of the RGPD promise ("vos emails ne quittent
// jamais votre contrôle") — emails themselves live in Gmail, but every artifact
// Mailsorter derived is handed back in the open. OAuth tokens and Stripe IDs are
// stripped via account.RedactUser.
func (h *Handler) ExportAccount(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	export := map[string]interface{}{
		"exportedAt": time.Now().UTC(),
		"account":    account.RedactUser(h.loadUser(ctx, userEmail)),
		"settings":   h.userSettings(ctx, userEmail),
	}

	for _, ds := range account.Datasets() {
		coll := h.datasetCollection(ds)
		if coll == nil {
			continue
		}
		rows := h.dumpUserRows(ctx, coll, userEmail)
		export[string(ds)] = rows
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"mailsorter-export-%s.json\"", time.Now().UTC().Format("2006-01-02")))
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(export)
}

// dumpUserRows reads every document a user owns in a collection as raw bson, so
// the export is faithful regardless of the struct shape. Secrets only live on
// the user record (handled separately via RedactUser), never on these per-user
// collections. Returns an empty slice (not nil) so the JSON shows [] not null.
func (h *Handler) dumpUserRows(ctx context.Context, coll *mongo.Collection, userEmail string) []bson.M {
	rows := make([]bson.M, 0)
	cursor, err := coll.Find(ctx, bson.M{"userId": userEmail})
	if err != nil {
		return rows
	}
	defer cursor.Close(ctx)
	_ = cursor.All(ctx, &rows)
	return rows
}

// loadUser fetches the caller's account record, or a minimal record carrying
// just the email when none is stored yet.
func (h *Handler) loadUser(ctx context.Context, userEmail string) models.User {
	var u models.User
	if err := h.db.Users().FindOne(ctx, bson.M{"email": userEmail}).Decode(&u); err != nil {
		return models.User{Email: userEmail}
	}
	return u
}

// DeleteAccount permanently erases everything Mailsorter stores about the caller:
// every user-owned dataset plus the account record itself. This is the
// right-to-erasure half of RGPD. It is intentionally irreversible; the frontend
// gates it behind an explicit typed confirmation. Gmail is never touched — the
// user's mailbox is theirs — and revoking Mailsorter's access is done by the
// user from their Google account. Returns per-dataset deletion counts so the
// action is auditable.
func (h *Handler) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	deleted := map[string]int64{}
	for _, ds := range account.Datasets() {
		coll := h.datasetCollection(ds)
		if coll == nil {
			continue
		}
		res, err := coll.DeleteMany(ctx, bson.M{"userId": userEmail})
		if err == nil && res != nil {
			deleted[string(ds)] = res.DeletedCount
		}
	}

	// Finally remove the account record itself (keyed by email, not userId).
	if res, err := h.db.Users().DeleteMany(ctx, bson.M{"email": userEmail}); err == nil && res != nil {
		deleted["account"] = res.DeletedCount
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "deleted",
		"deleted": deleted,
	})
}
