package api

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/billing"
	"go.mongodb.org/mongo-driver/bson"
)

// PlanPro marks an unlimited subscription; the absence of a plan means free.
const PlanPro = "pro"
const PlanFree = "free"

// getPlan returns the user's billing plan, defaulting to free.
func (h *Handler) getPlan(ctx context.Context, userEmail string) string {
	var u struct {
		Plan string `bson:"plan"`
	}
	if err := h.db.Users().FindOne(ctx, bson.M{"email": userEmail}).Decode(&u); err != nil {
		return PlanFree
	}
	if u.Plan == PlanPro {
		return PlanPro
	}
	return PlanFree
}

// CreateCheckout starts a Stripe Checkout Session for the Pro subscription and
// returns the hosted checkout URL for the client to redirect to.
func (h *Handler) CreateCheckout(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}
	if h.billing.Client == nil || h.billing.PriceID == "" {
		writeError(w, http.StatusServiceUnavailable, "Billing not configured")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()

	// Already Pro, nothing to buy.
	if h.getPlan(ctx, userEmail) == PlanPro {
		writeError(w, http.StatusConflict, "Vous êtes déjà abonné à Pro.")
		return
	}

	url, err := h.billing.Client.CreateCheckoutSession(billing.CheckoutParams{
		PriceID:           h.billing.PriceID,
		CustomerEmail:     userEmail,
		ClientReferenceID: userEmail,
		SuccessURL:        h.billing.AppBaseURL + "/pricing?checkout=success",
		CancelURL:         h.billing.AppBaseURL + "/pricing?checkout=cancel",
	})
	if err != nil {
		log.Printf("stripe checkout error: %v", err)
		writeError(w, http.StatusBadGateway, "Impossible de démarrer le paiement.")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

// CreatePortal starts a Stripe Billing Portal session so a subscribed user can
// manage or cancel their plan without leaving the product, then returns the
// hosted URL for the client to redirect to.
func (h *Handler) CreatePortal(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}
	if h.billing.Client == nil {
		writeError(w, http.StatusServiceUnavailable, "Billing not configured")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()

	var u struct {
		StripeCustomerID string `bson:"stripeCustomerId"`
	}
	if err := h.db.Users().FindOne(ctx, bson.M{"email": userEmail}).Decode(&u); err != nil || u.StripeCustomerID == "" {
		writeError(w, http.StatusNotFound, "Aucun abonnement à gérer.")
		return
	}

	url, err := h.billing.Client.CreatePortalSession(u.StripeCustomerID, h.billing.AppBaseURL+"/pricing")
	if err != nil {
		log.Printf("stripe portal error: %v", err)
		writeError(w, http.StatusBadGateway, "Impossible d'ouvrir le portail de facturation.")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

// StripeWebhook receives Stripe events, verifies their signature, and keeps the
// user's plan in sync with their subscription lifecycle. It must read the raw
// body before any parsing so the HMAC check is performed on the exact payload.
func (h *Handler) StripeWebhook(w http.ResponseWriter, r *http.Request) {
	if h.billing.WebhookSecret == "" {
		writeError(w, http.StatusServiceUnavailable, "Billing not configured")
		return
	}

	payload, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MiB cap
	if err != nil {
		writeError(w, http.StatusBadRequest, "Failed to read body")
		return
	}

	event, err := billing.ConstructEvent(payload, r.Header.Get("Stripe-Signature"), h.billing.WebhookSecret)
	if err != nil {
		log.Printf("stripe webhook rejected: %v", err)
		writeError(w, http.StatusBadRequest, "Invalid signature")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	switch event.Type {
	case "checkout.session.completed":
		var sess billing.CheckoutSession
		if json.Unmarshal(event.Object, &sess) == nil {
			email := sess.ClientReferenceID
			if email == "" {
				email = sess.CustomerEmail
			}
			if email == "" {
				email = sess.CustomerDetails.Email
			}
			if email != "" {
				h.setPlan(ctx, bson.M{"email": email}, PlanPro, bson.M{
					"stripeCustomerId":     sess.Customer,
					"stripeSubscriptionId": sess.Subscription,
				})
			}
		}

	case "customer.subscription.updated":
		var sub billing.Subscription
		if json.Unmarshal(event.Object, &sub) == nil {
			plan := PlanPro
			if sub.Status != "active" && sub.Status != "trialing" {
				plan = PlanFree
			}
			h.setPlan(ctx, bson.M{"stripeSubscriptionId": sub.ID}, plan, nil)
		}

	case "customer.subscription.deleted":
		var sub billing.Subscription
		if json.Unmarshal(event.Object, &sub) == nil {
			h.setPlan(ctx, bson.M{"stripeSubscriptionId": sub.ID}, PlanFree, nil)
		}
	}

	w.WriteHeader(http.StatusOK)
}

// setPlan updates a user's plan (and optional extra fields) by the given filter.
func (h *Handler) setPlan(ctx context.Context, filter bson.M, plan string, extra bson.M) {
	set := bson.M{"plan": plan, "planUpdatedAt": time.Now(), "updatedAt": time.Now()}
	for k, v := range extra {
		set[k] = v
	}
	if _, err := h.db.Users().UpdateOne(ctx, filter, bson.M{"$set": set}); err != nil {
		log.Printf("failed to set plan %s for %v: %v", plan, filter, err)
	}
}
