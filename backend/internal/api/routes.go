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
	r.HandleFunc("/api/stats", h.GetMailboxStats).Methods("GET")

	// Labels routes
	r.HandleFunc("/api/labels", h.GetLabels).Methods("GET")

	// AI Sorting routes
	r.HandleFunc("/api/ai/analyze", h.AnalyzeEmails).Methods("POST")
	r.HandleFunc("/api/ai/analyze-sender", h.AnalyzeSender).Methods("POST")
	r.HandleFunc("/api/ai/apply", h.ApplySuggestion).Methods("POST")
	r.HandleFunc("/api/ai/apply-bulk", h.ApplyBulk).Methods("POST")
	r.HandleFunc("/api/ai/suggestions", h.GetSuggestions).Methods("GET")
	r.HandleFunc("/api/ai/suggestions/{id}/reject", h.RejectSuggestion).Methods("POST")

	// Senders routes
	r.HandleFunc("/api/senders", h.GetSenders).Methods("GET")
	r.HandleFunc("/api/senders/{id}/preferences", h.UpdateSenderPreference).Methods("PUT")

	// Smart Labels routes
	r.HandleFunc("/api/smart-labels", h.GetSmartLabels).Methods("GET")
	r.HandleFunc("/api/smart-labels", h.CreateSmartLabel).Methods("POST")

	// Config routes (no auth required for initial setup)
	r.HandleFunc("/api/config/status", h.GetConfigStatus).Methods("GET")
	r.HandleFunc("/api/config/gmail", h.GetGmailConfig).Methods("GET")
	r.HandleFunc("/api/config/gmail", h.SaveGmailConfig).Methods("POST")

	// Setup CORS
	c := cors.New(cors.Options{
		AllowedOrigins:   []string{"http://localhost:3000", "http://localhost"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type", "Authorization", "X-User-Email"},
		AllowCredentials: true,
	})

	return c.Handler(r)
}
