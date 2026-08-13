package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/search"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// GetSavedSearches lists the caller's saved searches, most used first.
//
// The inbox has always spoken Gmail's query language, which is what makes it
// powerful and what made it single-use: nobody retypes
// "in:inbox from:linkedin.com older_than:7d" every morning. The six built-in
// quick filters covered the generic cases; these are the user's own.
func (h *Handler) GetSavedSearches(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	cursor, err := h.db.SavedSearches().Find(ctx,
		bson.M{"userId": userEmail},
		// Most used first, then most recent: the bar orders itself by how much
		// each shortcut has earned its place.
		options.Find().
			SetSort(bson.D{{Key: "usedCount", Value: -1}, {Key: "createdAt", Value: -1}}).
			SetLimit(search.MaxPerUser))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load saved searches")
		return
	}
	defer cursor.Close(ctx)

	rows := make([]models.SavedSearch, 0)
	if err := cursor.All(ctx, &rows); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to decode saved searches")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"searches": rows})
}

// CreateSavedSearch saves a query, or renames the chip that already runs it.
func (h *Handler) CreateSavedSearch(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	var in models.SavedSearchInput
	if !decodeJSON(w, r, &in) {
		return
	}

	name, query, err := search.Normalize(in.Name, in.Query)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	key := search.Key(query)
	now := time.Now()

	// The count is checked against the searches the user already has, but only
	// when this one would be a NEW chip: renaming an existing shortcut must keep
	// working on a full list.
	existing, err := h.db.SavedSearches().CountDocuments(ctx, bson.M{"userId": userEmail})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load saved searches")
		return
	}
	if existing >= search.MaxPerUser {
		var already models.SavedSearch
		if fErr := h.db.SavedSearches().FindOne(ctx, bson.M{"userId": userEmail, "key": key}).Decode(&already); fErr != nil {
			writeError(w, http.StatusBadRequest, "Vous avez atteint la limite de recherches enregistrées")
			return
		}
	}

	// Upsert on (userId, key): the same query is the same shortcut, whatever it
	// is called, so saving it twice renames it rather than duplicating the chip.
	var saved models.SavedSearch
	err = h.db.SavedSearches().FindOneAndUpdate(ctx,
		bson.M{"userId": userEmail, "key": key},
		bson.M{
			"$set": bson.M{"name": name, "query": query, "updatedAt": now},
			"$setOnInsert": bson.M{
				"userId": userEmail, "key": key, "createdAt": now,
			},
		},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	).Decode(&saved)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save the search")
		return
	}

	writeJSON(w, http.StatusCreated, saved)
}

// DeleteSavedSearch removes a saved search the caller owns.
func (h *Handler) DeleteSavedSearch(w http.ResponseWriter, r *http.Request) {
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

	res, err := h.db.SavedSearches().DeleteOne(ctx, bson.M{"_id": oid, "userId": userEmail})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to delete the search")
		return
	}
	if res.DeletedCount == 0 {
		writeError(w, http.StatusNotFound, "Recherche introuvable")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// UseSavedSearch records that a shortcut was clicked, which is what orders the
// bar. Best-effort and fire-and-forget from the client's point of view: a
// counter that failed to increment must never cost the user their search.
func (h *Handler) UseSavedSearch(w http.ResponseWriter, r *http.Request) {
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

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	res := h.db.SavedSearches().FindOneAndUpdate(ctx,
		bson.M{"_id": oid, "userId": userEmail},
		bson.M{"$inc": bson.M{"usedCount": 1}, "$set": bson.M{"updatedAt": time.Now()}},
	)
	if err := res.Err(); err != nil {
		if err == mongo.ErrNoDocuments {
			writeError(w, http.StatusNotFound, "Recherche introuvable")
			return
		}
		writeError(w, http.StatusInternalServerError, "Failed to record the search")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
