package api

import (
	"github.com/gorilla/mux"
	"github.com/rs/cors"
	"net/http"
)

func (h *Handler) SetupRoutes() http.Handler {
	r := mux.NewRouter()

	// Health check
	r.HandleFunc("/health", h.HealthCheck).Methods("GET")

	// Auth routes
	r.HandleFunc("/api/auth/url", h.GetAuthURL).Methods("GET")
	r.HandleFunc("/api/auth/callback", h.HandleAuthCallback).Methods("GET")

	// Email routes
	r.HandleFunc("/api/emails", h.GetEmails).Methods("GET")
	r.HandleFunc("/api/emails/sync", h.SyncEmails).Methods("POST")
	r.HandleFunc("/api/emails/action", h.EmailAction).Methods("POST")
	r.HandleFunc("/api/emails/snooze", h.Snooze).Methods("POST")
	r.HandleFunc("/api/stats", h.GetMailboxStats).Methods("GET")
	r.HandleFunc("/api/stats/activity", h.GetActivity).Methods("GET")

	// Snooze ("Reporter") — return-to-inbox scheduling
	r.HandleFunc("/api/snoozes", h.GetSnoozes).Methods("GET")
	r.HandleFunc("/api/snoozes/{id}/wake", h.WakeSnooze).Methods("POST")

	// Protected senders (VIP) — never auto-archived/trashed/deleted
	r.HandleFunc("/api/protected", h.GetProtected).Methods("GET")
	r.HandleFunc("/api/protected", h.CreateProtected).Methods("POST")
	r.HandleFunc("/api/protected/{id}", h.DeleteProtected).Methods("DELETE")

	// Account / usage / settings
	r.HandleFunc("/api/usage", h.GetUsage).Methods("GET")
	r.HandleFunc("/api/account/settings", h.GetSettings).Methods("GET")
	r.HandleFunc("/api/account/settings", h.UpdateSettings).Methods("PUT")

	// Billing (Stripe)
	r.HandleFunc("/api/billing/checkout", h.CreateCheckout).Methods("POST")
	r.HandleFunc("/api/billing/portal", h.CreatePortal).Methods("POST")
	r.HandleFunc("/api/billing/webhook", h.StripeWebhook).Methods("POST")

	// Deterministic sorting rules (AI-free triage)
	r.HandleFunc("/api/rules", h.GetRules).Methods("GET")
	r.HandleFunc("/api/rules", h.CreateRule).Methods("POST")
	r.HandleFunc("/api/rules/apply", h.ApplyRules).Methods("POST")
	r.HandleFunc("/api/rules/preview", h.PreviewRules).Methods("POST")
	r.HandleFunc("/api/rules/{id}", h.UpdateRule).Methods("PUT")
	r.HandleFunc("/api/rules/{id}", h.DeleteRule).Methods("DELETE")

	// Unsubscribe / subscriptions cleanup
	r.HandleFunc("/api/subscriptions", h.GetSubscriptions).Methods("GET")
	r.HandleFunc("/api/unsubscribe", h.Unsubscribe).Methods("POST")

	// Labels routes
	r.HandleFunc("/api/labels", h.GetLabels).Methods("GET")

	// AI Sorting routes
	r.HandleFunc("/api/ai/analyze", h.AnalyzeEmails).Methods("POST")
	r.HandleFunc("/api/ai/analyze-async", h.EnqueueAnalyze).Methods("POST")
	r.HandleFunc("/api/ai/jobs/{id}", h.GetJob).Methods("GET")
	r.HandleFunc("/api/ai/analyze-sender", h.AnalyzeSender).Methods("POST")
	r.HandleFunc("/api/ai/apply", h.ApplySuggestion).Methods("POST")
	r.HandleFunc("/api/ai/apply-batch", h.ApplyBatch).Methods("POST")
	r.HandleFunc("/api/ai/apply-bulk", h.ApplyBulk).Methods("POST")
	r.HandleFunc("/api/ai/suggestions", h.GetSuggestions).Methods("GET")
	r.HandleFunc("/api/ai/suggestions/{id}/reject", h.RejectSuggestion).Methods("POST")

	// Senders routes
	r.HandleFunc("/api/senders", h.GetSenders).Methods("GET")
	r.HandleFunc("/api/senders/rule", h.CreateSenderRule).Methods("POST")
	r.HandleFunc("/api/senders/{id}/preferences", h.UpdateSenderPreference).Methods("PUT")

	// Smart Labels routes
	r.HandleFunc("/api/smart-labels", h.GetSmartLabels).Methods("GET")
	r.HandleFunc("/api/smart-labels", h.CreateSmartLabel).Methods("POST")

	// Config routes (no auth required for initial setup)
	r.HandleFunc("/api/config/status", h.GetConfigStatus).Methods("GET")
	r.HandleFunc("/api/config/gmail", h.GetGmailConfig).Methods("GET")
	r.HandleFunc("/api/config/gmail", h.SaveGmailConfig).Methods("POST")

	// Middleware chain (applied to every matched route, innermost last):
	// recover → request-id → logging → rate-limit → auth → handler.
	rl := newRateLimiter(20, 40) // ~20 req/s sustained, burst 40, per client
	r.Use(recoverMiddleware)
	r.Use(requestIDMiddleware)
	r.Use(loggingMiddleware)
	r.Use(rl.middleware)
	r.Use(h.authMiddleware)

	// Setup CORS
	c := cors.New(cors.Options{
		AllowedOrigins:   []string{"http://localhost:3000", "http://localhost", "https://mailsorter.sohbi.dev"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type", "Authorization", "X-User-Email"},
		AllowCredentials: true,
	})

	return c.Handler(r)
}
