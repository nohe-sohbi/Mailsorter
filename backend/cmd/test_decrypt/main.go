package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/config"
	"github.com/nohe-sohbi/mailsorter/backend/internal/crypto"
	"github.com/nohe-sohbi/mailsorter/backend/internal/database"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"go.mongodb.org/mongo-driver/bson"
)

func main() {
	cfg := config.Load()
	
	db, err := database.NewDatabase(cfg.MongoDBURI)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	encryptor := crypto.NewEncryptor(cfg.EncryptionKey)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var storedConfig models.GmailConfig
	err = db.GmailConfig().FindOne(ctx, bson.M{}).Decode(&storedConfig)
	if err != nil {
		log.Fatal("No config found:", err)
	}

	fmt.Println("Client ID:", storedConfig.ClientID)
	fmt.Println("IsConfigured:", storedConfig.IsConfigured)
	
	decrypted, err := encryptor.Decrypt(storedConfig.ClientSecretEncrypted)
	if err != nil {
		log.Fatal("Decrypt FAILED:", err)
	}
	
	fmt.Println("Decrypt OK - Secret length:", len(decrypted))
	if len(decrypted) > 5 {
		fmt.Println("First 5 chars:", decrypted[:5])
	}
}
