package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/nohe-sohbi/mailsorter/backend/internal/api"
	"github.com/nohe-sohbi/mailsorter/backend/internal/config"
	"github.com/nohe-sohbi/mailsorter/backend/internal/database"
	"github.com/nohe-sohbi/mailsorter/backend/internal/gmail"
)

func main() {
	// Load configuration
	cfg := config.Load()

	// Initialize database
	db, err := database.NewDatabase(cfg.MongoDBURI)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	log.Println("Connected to MongoDB successfully")

	// Initialize Gmail service
	gmailService := gmail.NewService(
		cfg.GmailClientID,
		cfg.GmailClientSecret,
		cfg.GmailRedirectURL,
	)

	// Initialize API handler
	handler := api.NewHandler(db, gmailService)

	// Setup routes
	router := handler.SetupRoutes()

	// Start server
	addr := fmt.Sprintf(":%s", cfg.Port)
	log.Printf("Starting server on %s", addr)
	
	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
