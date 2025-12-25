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

	// Sorting rules routes
	r.HandleFunc("/api/rules", h.GetSortingRules).Methods("GET")
	r.HandleFunc("/api/rules", h.CreateSortingRule).Methods("POST")
	r.HandleFunc("/api/rules/{id}", h.UpdateSortingRule).Methods("PUT")
	r.HandleFunc("/api/rules/{id}", h.DeleteSortingRule).Methods("DELETE")

	// Labels routes
	r.HandleFunc("/api/labels", h.GetLabels).Methods("GET")

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
