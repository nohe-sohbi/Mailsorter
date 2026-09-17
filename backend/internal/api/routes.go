package api

import (
	"github.com/gorilla/mux"
	"github.com/rs/cors"
	"net/http"
)

func (h *Handler) SetupRoutes() http.Handler {
	r := mux.NewRouter()

	// Health check + ops metrics
	r.HandleFunc("/health", h.HealthCheck).Methods("GET")
	r.HandleFunc("/metrics", h.Metrics).Methods("GET")

	// Auth routes
	r.HandleFunc("/api/auth/url", h.GetAuthURL).Methods("GET")
	r.HandleFunc("/api/auth/callback", h.HandleAuthCallback).Methods("GET")

	// Email routes
	r.HandleFunc("/api/emails", h.GetEmails).Methods("GET")
	r.HandleFunc("/api/emails/sync", h.SyncEmails).Methods("POST")
	r.HandleFunc("/api/emails/action", h.EmailAction).Methods("POST")
	// Selection-scoped triage: one action over N messages, and its reversal.
	r.HandleFunc("/api/emails/batch-action", h.BatchAction).Methods("POST")
	r.HandleFunc("/api/emails/batch-undo", h.BatchUndo).Methods("POST")
	// Snooze: one message, or a whole selection to the same wake time.
	r.HandleFunc("/api/emails/snooze", h.Snooze).Methods("POST")
	r.HandleFunc("/api/emails/batch-snooze", h.BatchSnooze).Methods("POST")
	// Single message with its decoded body (the list omits bodies on purpose)
	// and its attachments. Registered last so every fixed /api/emails/* path
	// above always wins over the {id} pattern.
	r.HandleFunc("/api/emails/{id}/attachments/{attachmentId}", h.DownloadAttachment).Methods("GET")
	r.HandleFunc("/api/emails/{id}", h.GetEmail).Methods("GET")
	r.HandleFunc("/api/stats", h.GetMailboxStats).Methods("GET")
	r.HandleFunc("/api/stats/activity", h.GetActivity).Methods("GET")
	r.HandleFunc("/api/stats/digest", h.GetDigest).Methods("GET")

	// Action history (audit trail) + one-click undo of automated actions.
	r.HandleFunc("/api/activity/log", h.GetActionLog).Methods("GET")
	r.HandleFunc("/api/activity/undo", h.UndoAction).Methods("POST")

	// Snooze ("Reporter"): return-to-inbox scheduling
	r.HandleFunc("/api/snoozes", h.GetSnoozes).Methods("GET")
	r.HandleFunc("/api/snoozes/{id}/wake", h.WakeSnooze).Methods("POST")

	// Protected senders (VIP): never auto-archived/trashed/deleted
	r.HandleFunc("/api/protected", h.GetProtected).Methods("GET")
	r.HandleFunc("/api/protected", h.CreateProtected).Methods("POST")
	r.HandleFunc("/api/protected/{id}", h.DeleteProtected).Methods("DELETE")

	// Account / usage / settings
	r.HandleFunc("/api/usage", h.GetUsage).Methods("GET")
	r.HandleFunc("/api/account/profile", h.GetProfile).Methods("GET")
	r.HandleFunc("/api/account/settings", h.GetSettings).Methods("GET")
	r.HandleFunc("/api/account/settings", h.UpdateSettings).Methods("PUT")
	// Send the daily recap right now, so the digest can be verified without
	// waiting a day to find out the Gmail grant lost its send scope.
	r.HandleFunc("/api/account/digest/test", h.SendTestDigest).Methods("POST")
	// RGPD: data portability (export) and right to erasure (delete).
	// The mailbox connected over IMAP, which is how the hosted edition reaches
	// mail at all. The body carries an address and a password and nothing else:
	// the provider, host, port and TLS mode are resolved from
	// internal/provider, so a caller cannot name the host the server connects to.
	r.HandleFunc("/api/mailbox", h.GetMailbox).Methods("GET")
	r.HandleFunc("/api/mailbox/connect", h.ConnectMailbox).Methods("POST")
	r.HandleFunc("/api/mailbox", h.DisconnectMailbox).Methods("DELETE")

	r.HandleFunc("/api/account/export", h.ExportAccount).Methods("GET")
	r.HandleFunc("/api/account", h.DeleteAccount).Methods("DELETE")

	// Billing (Stripe)
	r.HandleFunc("/api/billing/checkout", h.CreateCheckout).Methods("POST")
	r.HandleFunc("/api/billing/portal", h.CreatePortal).Methods("POST")
	r.HandleFunc("/api/billing/webhook", h.StripeWebhook).Methods("POST")

	// Deterministic sorting rules (AI-free triage)
	r.HandleFunc("/api/rules", h.GetRules).Methods("GET")
	r.HandleFunc("/api/rules", h.CreateRule).Methods("POST")
	r.HandleFunc("/api/rules/apply", h.ApplyRules).Methods("POST")
	r.HandleFunc("/api/rules/preview", h.PreviewRules).Methods("POST")
	// Order IS the engine's semantics (first match wins), and a ruleset is worth
	// backing up: both are edited as a whole, hence their own routes. Registered
	// before /api/rules/{id} so the fixed paths win.
	r.HandleFunc("/api/rules/reorder", h.ReorderRules).Methods("PUT")
	r.HandleFunc("/api/rules/export", h.ExportRules).Methods("GET")
	r.HandleFunc("/api/rules/import", h.ImportRules).Methods("POST")
	r.HandleFunc("/api/rules/{id}/duplicate", h.DuplicateRule).Methods("POST")
	r.HandleFunc("/api/rules/{id}", h.UpdateRule).Methods("PUT")
	r.HandleFunc("/api/rules/{id}", h.DeleteRule).Methods("DELETE")

	// Unsubscribe / subscriptions cleanup
	r.HandleFunc("/api/subscriptions", h.GetSubscriptions).Methods("GET")
	r.HandleFunc("/api/unsubscribe", h.Unsubscribe).Methods("POST")

	// Saved searches: the user's own Gmail queries, kept as one-click filters.
	r.HandleFunc("/api/searches", h.GetSavedSearches).Methods("GET")
	r.HandleFunc("/api/searches", h.CreateSavedSearch).Methods("POST")
	r.HandleFunc("/api/searches/{id}/use", h.UseSavedSearch).Methods("POST")
	r.HandleFunc("/api/searches/{id}", h.DeleteSavedSearch).Methods("DELETE")

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

	// Pro waitlist. Public: it exists to measure buying intent from visitors who
	// have no account yet, so requiring a session would defeat the purpose.
	r.HandleFunc("/api/waitlist", h.JoinWaitlist).Methods("POST")

	// Boot probe: tells the frontend whether the instance has Gmail credentials.
	// Public by design (the SPA calls it before any login) and deliberately the
	// only /api/config/ route: the credentials themselves are set through
	// environment variables, never over HTTP.
	r.HandleFunc("/api/config/status", h.GetConfigStatus).Methods("GET")

	// The mailbox catalog for the running edition. Public because the connect
	// screen is shown before any login, and it carries no secret: provider
	// names, hosts, ports and help text.
	r.HandleFunc("/api/providers", h.GetProviders).Methods("GET")

	// Middleware chain (applied to every matched route, innermost last):
	// recover → request-id → metrics → logging → rate-limit → auth → handler.
	rl := newRateLimiter(20, 40) // ~20 req/s sustained, burst 40, per client
	r.Use(recoverMiddleware)
	r.Use(requestIDMiddleware)
	r.Use(h.metricsMiddleware)
	r.Use(loggingMiddleware)
	r.Use(rl.middleware)
	r.Use(h.authMiddleware)

	// Setup CORS (allow-list configurable via ALLOWED_ORIGINS at startup)
	c := cors.New(cors.Options{
		AllowedOrigins:   AllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type", "Authorization", "X-User-Email"},
		AllowCredentials: true,
	})

	return c.Handler(r)
}
