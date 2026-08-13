package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/rules"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

// ReorderRules sets the running order of the whole ruleset in one request.
//
// Order is the semantics of the engine, not a display preference: FirstMatch
// stops at the first rule that matches, so which rule wins IS the priority. The
// editor only ever exposed it as a raw number field, which meant expressing
// "this one before that one" required doing the arithmetic by hand and getting
// ties wrong. The client now sends the list in the order it wants.
func (h *Handler) ReorderRules(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	var req struct {
		IDs []string `json:"ids"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "Aucun ordre fourni")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	ruleset, err := h.loadRules(ctx, userEmail)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load rules")
		return
	}

	// Pure: ids the caller does not own are dropped, and rules it did not
	// mention keep their relative order behind the ones it did.
	priorities := rules.Reorder(ruleset, req.IDs)
	if len(priorities) == 0 {
		writeError(w, http.StatusBadRequest, "Aucune règle correspondante")
		return
	}

	writes := make([]mongo.WriteModel, 0, len(priorities))
	now := time.Now()
	for id, priority := range priorities {
		oid, oErr := primitive.ObjectIDFromHex(id)
		if oErr != nil {
			continue
		}
		writes = append(writes, mongo.NewUpdateOneModel().
			SetFilter(bson.M{"_id": oid, "userId": userEmail}).
			SetUpdate(bson.M{"$set": bson.M{"priority": priority, "updatedAt": now}}))
	}
	if len(writes) == 0 {
		writeError(w, http.StatusBadRequest, "Aucune règle correspondante")
		return
	}

	// One round trip for the whole ruleset: reordering is a single user gesture
	// and must not be able to leave half the priorities rewritten.
	if _, err := h.db.SortingRules().BulkWrite(ctx, writes); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to reorder rules")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":    "reordered",
		"reordered": len(writes),
	})
}

// DuplicateRule copies a rule the caller owns.
//
// The fastest way to a second rule is almost always an existing one: the same
// conditions with one value changed. Retyping four conditions to change a word
// was the actual cost of a ruleset that grows.
func (h *Handler) DuplicateRule(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	oid, err := primitive.ObjectIDFromHex(mux.Vars(r)["id"])
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid rule ID")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	ruleset, err := h.loadRules(ctx, userEmail)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load rules")
		return
	}

	var original *models.SortingRule
	names := make([]string, 0, len(ruleset))
	for i := range ruleset {
		names = append(names, ruleset[i].Name)
		if ruleset[i].ID == oid.Hex() {
			original = &ruleset[i]
		}
	}
	if original == nil {
		writeError(w, http.StatusNotFound, "Rule not found")
		return
	}

	now := time.Now()
	// Through the portable form so the copy is built from the rule's INTENT,
	// never from its account state: no id, no applied counter, no shared history.
	copyOf := rules.FromPortable(userEmail, rules.ToPortable([]models.SortingRule{*original})[0], now)
	copyOf.Name = rules.DuplicateName(original.Name, names)
	// A copy starts disabled. It is one edit away from being what the user
	// actually wants, and until then it would be a second rule silently acting
	// on the same mail as the one it was cloned from.
	copyOf.Enabled = false

	if err := rules.Validate(copyOf); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	res, err := h.db.SortingRules().InsertOne(ctx, copyOf)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to duplicate rule")
		return
	}
	if id, ok := res.InsertedID.(primitive.ObjectID); ok {
		copyOf.ID = id.Hex()
	}

	writeJSON(w, http.StatusCreated, copyOf)
}

// ExportRules hands the caller their whole ruleset as a portable document.
//
// A ruleset is the part of Mailsorter a user actually authors, and it lived in
// exactly one place with no way to back it up or move it. The file carries
// intent only (no ids, no owner, no counters), so importing it into another
// account is a supported operation rather than an accident waiting to happen.
func (h *Handler) ExportRules(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	ruleset, err := h.loadRules(ctx, userEmail)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load rules")
		return
	}

	now := time.Now()
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q", "mailsorter-regles-"+now.UTC().Format("2006-01-02")+".json"))
	writeJSON(w, http.StatusOK, rules.BuildExport(ruleset, now))
}

// ImportRules creates rules from a previously exported document.
//
// It appends rather than replaces: an import that silently wiped the existing
// ruleset would be an irreversible action behind a file picker. Duplicates are
// the user's to remove, and they can see them.
func (h *Handler) ImportRules(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	var doc rules.Export
	if !decodeJSON(w, r, &doc) {
		return
	}

	now := time.Now()
	// All-or-nothing, and validated before anything is written: a half-applied
	// import leaves a ruleset that is neither the old one nor the file's.
	imported, err := rules.ValidateImport(doc, userEmail, now)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	existing, err := h.loadRules(ctx, userEmail)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load rules")
		return
	}
	if len(existing)+len(imported) > rules.MaxImportRules {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("Ce fichier porterait votre ruleset à %d règles, la limite est de %d", len(existing)+len(imported), rules.MaxImportRules))
		return
	}

	// Imported rules land after the ones already in place, so an import can
	// never quietly outrank the rules the user built by hand.
	offset := len(existing)
	docs := make([]interface{}, 0, len(imported))
	for i := range imported {
		imported[i].Priority = offset + i
		docs = append(docs, imported[i])
	}

	if _, err := h.db.SortingRules().InsertMany(ctx, docs); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to import rules")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"status":   "imported",
		"imported": len(docs),
	})
}
