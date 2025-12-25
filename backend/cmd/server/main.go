package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/api"
	"github.com/nohe-sohbi/mailsorter/backend/internal/config"
	"github.com/nohe-sohbi/mailsorter/backend/internal/crypto"
	"github.com/nohe-sohbi/mailsorter/backend/internal/database"
	"github.com/nohe-sohbi/mailsorter/backend/internal/gmail"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"go.mongodb.org/mongo-driver/bson"
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

	// Initialize encryptor
	encryptor := crypto.NewEncryptor(cfg.EncryptionKey)

	// Initialize Gmail service (may be empty if not configured)
	gmailService := gmail.NewService("", "", "")

	// Try to load existing config from database
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	var storedConfig models.GmailConfig
	err = db.GmailConfig().FindOne(ctx, bson.M{}).Decode(&storedConfig)
	cancel()

	if err == nil && storedConfig.IsConfigured {
		// Decrypt and initialize Gmail service
		clientSecret, err := encryptor.Decrypt(storedConfig.ClientSecretEncrypted)
		if err == nil {
			gmailService.UpdateConfig(
				storedConfig.ClientID,
				clientSecret,
				storedConfig.RedirectURL,
			)
			log.Println("Gmail service initialized from stored configuration")
		} else {
			log.Printf("Warning: Failed to decrypt stored credentials: %v", err)
		}
	} else {
		// Try using environment variables as fallback
		if cfg.GmailClientID != "" && cfg.GmailClientSecret != "" {
			gmailService.UpdateConfig(
				cfg.GmailClientID,
				cfg.GmailClientSecret,
				cfg.GmailRedirectURL,
			)
			log.Println("Gmail service initialized from environment variables")
		} else {
			log.Println("Gmail credentials not configured - setup required via UI")
		}
	}

	// Initialize API handler
	handler := api.NewHandler(db, gmailService, encryptor)

	// Setup routes
	router := handler.SetupRoutes()

	// Start server
	addr := fmt.Sprintf(":%s", cfg.Port)
	log.Printf("Starting server on %s", addr)

	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
