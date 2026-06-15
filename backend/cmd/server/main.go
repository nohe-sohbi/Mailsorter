package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/ai"
	"github.com/nohe-sohbi/mailsorter/backend/internal/api"
	"github.com/nohe-sohbi/mailsorter/backend/internal/auth"
	"github.com/nohe-sohbi/mailsorter/backend/internal/billing"
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

	// Ensure hot-path indexes exist (best-effort).
	idxCtx, idxCancel := context.WithTimeout(context.Background(), 15*time.Second)
	if err := db.EnsureIndexes(idxCtx); err != nil {
		log.Printf("Warning: failed to ensure some indexes: %v", err)
	} else {
		log.Println("Database indexes ensured")
	}
	idxCancel()

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

	// Initialize Mistral AI client
	var aiClient *ai.MistralClient
	if cfg.MistralAPIKey != "" {
		aiClient = ai.NewMistralClient(cfg.MistralAPIKey, cfg.MistralModel)
		log.Println("Mistral AI client initialized")
	} else {
		log.Println("Warning: MISTRAL_API_KEY not set - AI features disabled")
	}

	// Initialize Stripe billing (optional)
	billingCfg := api.BillingConfig{
		PriceID:       cfg.StripePriceID,
		WebhookSecret: cfg.StripeWebhookSecret,
		AppBaseURL:    cfg.AppBaseURL,
	}
	if cfg.StripeSecretKey != "" {
		billingCfg.Client = billing.New(cfg.StripeSecretKey)
		log.Println("Stripe billing initialized")
	} else {
		log.Println("Warning: STRIPE_SECRET_KEY not set - billing disabled")
	}

	// Session/CSRF token manager, keyed off the server secret.
	authManager := auth.NewManager(cfg.EncryptionKey)

	// Initialize API handler
	handler := api.NewHandler(db, gmailService, encryptor, aiClient, billingCfg, authManager)

	// Setup routes
	router := handler.SetupRoutes()

	// Hardened HTTP server: bounded timeouts protect against slow-client and
	// resource-exhaustion attacks that an unconfigured server is vulnerable to.
	addr := fmt.Sprintf(":%s", cfg.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      150 * time.Second, // long enough for synchronous AI analysis
		IdleTimeout:       120 * time.Second,
	}

	// Run the server in the background so we can listen for shutdown signals.
	go func() {
		log.Printf("Starting server on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// Graceful shutdown: stop accepting new connections and let in-flight
	// requests finish before exiting (e.g. on container redeploy).
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Println("Shutdown signal received, draining connections…")

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Graceful shutdown failed: %v", err)
	}
	log.Println("Server stopped")
}
