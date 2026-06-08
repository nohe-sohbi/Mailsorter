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
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}
	if h.billing.Client == nil || h.billing.PriceID == "" {
		http.Error(w, "Billing not configured", http.StatusServiceUnavailable)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	// Already Pro — nothing to buy.
	if h.getPlan(ctx, userEmail) == PlanPro {
		http.Error(w, "Vous êtes déjà abonné à Pro.", http.StatusConflict)
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
		http.Error(w, "Impossible de démarrer le paiement.", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"url": url})
}

// StripeWebhook receives Stripe events, verifies their signature, and keeps the
// user's plan in sync with their subscription lifecycle. It must read the raw
// body before any parsing so the HMAC check is performed on the exact payload.
func (h *Handler) StripeWebhook(w http.ResponseWriter, r *http.Request) {
	if h.billing.WebhookSecret == "" {
		http.Error(w, "Billing not configured", http.StatusServiceUnavailable)
		return
	}

	payload, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MiB cap
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}

	event, err := billing.ConstructEvent(payload, r.Header.Get("Stripe-Signature"), h.billing.WebhookSecret)
	if err != nil {
		log.Printf("stripe webhook rejected: %v", err)
		http.Error(w, "Invalid signature", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
