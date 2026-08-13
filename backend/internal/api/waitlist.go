package api

import (
	"context"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// waitlistPlan is the only plan people can currently queue for. Kept explicit
// so the stored rows stay meaningful if a second tier ever opens.
const waitlistPlan = PlanPro

// normalizeWaitlistEmail validates an address and returns it in the form we
// store: trimmed and lowercased, so "Nohe@Example.com " and "nohe@example.com"
// are one signup rather than two. Returns "" when the address is not usable.
func normalizeWaitlistEmail(raw string) string {
	addr, err := mail.ParseAddress(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	// ParseAddress accepts `Name <a@b.c>`; keep only the address itself.
	at := strings.LastIndex(addr.Address, "@")
	if at <= 0 || at == len(addr.Address)-1 {
		return ""
	}
	return strings.ToLower(addr.Address)
}

// JoinWaitlist records someone who wants to hear when Pro opens.
//
// Public on purpose: a pre-launch waitlist exists to measure intent from cold
// traffic, so it cannot require a session. When the caller does have one, the
// middleware-vouched X-User-Email is attached, which lets us tell an existing
// user from a first-time visitor.
//
// Signing up twice is not an error: the unique index on `email` makes this an
// idempotent upsert, so the count stays honest and the UI can stay simple.
func (h *Handler) JoinWaitlist(w http.ResponseWriter, r *http.Request) {
	var in models.WaitlistInput
	if !decodeJSON(w, r, &in) {
		return
	}

	email := normalizeWaitlistEmail(in.Email)
	if email == "" {
		writeError(w, http.StatusBadRequest, "Adresse email invalide")
		return
	}

	source := strings.TrimSpace(in.Source)
	if source == "" {
		source = "pricing"
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	set := bson.M{"source": source, "plan": waitlistPlan}
	// X-User-Email is stripped from client input and re-set by authMiddleware
	// only after verifying the bearer token, so it is trustworthy when present.
	if userEmail := r.Header.Get("X-User-Email"); userEmail != "" {
		set["userId"] = userEmail
	}

	_, err := h.db.Waitlist().UpdateOne(ctx,
		bson.M{"email": email},
		bson.M{
			"$set":         set,
			"$setOnInsert": bson.M{"email": email, "createdAt": time.Now()},
		},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to join waitlist")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"joined": true, "email": email})
}
