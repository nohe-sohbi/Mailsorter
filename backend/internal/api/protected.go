package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/protect"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// protectedValues returns the caller's normalized protected entries (addresses
// and domains), consulted before any automated destructive action. Protection
// is a safety bonus layered on top of the user's own choices, not an
// access-control boundary, so on a query error we return nil and let the caller
// proceed rather than blocking legitimate triage.
func (h *Handler) protectedValues(ctx context.Context, userEmail string) []string {
	cursor, err := h.db.ProtectedSenders().Find(ctx, bson.M{"userId": userEmail})
	if err != nil {
		return nil
	}
	defer cursor.Close(ctx)

	var rows []models.ProtectedSender
	if err := cursor.All(ctx, &rows); err != nil {
		return nil
	}
	values := make([]string, 0, len(rows))
	for _, r := range rows {
		values = append(values, r.Value)
	}
	return values
}

// allows reports whether action may be applied to an email from `from` given the
// user's protected list. A small wrapper so callers read naturally.
func allows(action, from string, protectedList []string) bool {
	return protect.Allowed(action, from, protectedList)
}

// GetProtected lists the caller's protected senders.
func (h *Handler) GetProtected(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	cursor, err := h.db.ProtectedSenders().Find(ctx, bson.M{"userId": userEmail},
		options.Find().SetSort(bson.M{"createdAt": -1}))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load protected senders")
		return
	}
	defer cursor.Close(ctx)

	rows := make([]models.ProtectedSender, 0)
	if err := cursor.All(ctx, &rows); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to decode protected senders")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"protected": rows})
}

// CreateProtected adds a sender (full address or whole domain) to the protected
// list. The value is normalized and classified server-side; duplicates are a
// no-op thanks to the unique {userId, value} index.
func (h *Handler) CreateProtected(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	var in models.ProtectedSenderInput
	if !decodeJSON(w, r, &in) {
		return
	}

	value, kind := protect.NormalizeEntry(in.Value)
	if value == "" {
		writeError(w, http.StatusBadRequest, "Adresse ou domaine invalide")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	now := time.Now()
	res, err := h.db.ProtectedSenders().UpdateOne(ctx,
		bson.M{"userId": userEmail, "value": value},
		bson.M{
			"$set":         bson.M{"kind": kind, "note": in.Note},
			"$setOnInsert": bson.M{"userId": userEmail, "value": value, "createdAt": now},
		},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save protected sender")
		return
	}

	entry := models.ProtectedSender{UserID: userEmail, Value: value, Kind: kind, Note: in.Note, CreatedAt: now}
	if oid, ok := res.UpsertedID.(primitive.ObjectID); ok {
		entry.ID = oid.Hex()
	}

	writeJSON(w, http.StatusCreated, entry)
}

// DeleteProtected removes a protected sender the caller owns.
func (h *Handler) DeleteProtected(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	oid, err := primitive.ObjectIDFromHex(mux.Vars(r)["id"])
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid id")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	res, err := h.db.ProtectedSenders().DeleteOne(ctx, bson.M{"_id": oid, "userId": userEmail})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to delete")
		return
	}
	if res.DeletedCount == 0 {
		writeError(w, http.StatusNotFound, "Not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
