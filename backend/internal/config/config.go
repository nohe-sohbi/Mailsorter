package config

import (
	"os"
)

type Config struct {
	MongoDBURI        string
	Port              string
	GmailClientID     string
	GmailClientSecret string
	GmailRedirectURL  string
	EncryptionKey     string
}

func Load() *Config {
	return &Config{
		MongoDBURI:        getEnv("MONGODB_URI", "mongodb://admin:password@localhost:27017/mailsorter?authSource=admin"),
		Port:              getEnv("PORT", "8080"),
		GmailClientID:     getEnv("GMAIL_CLIENT_ID", ""),
		GmailClientSecret: getEnv("GMAIL_CLIENT_SECRET", ""),
		GmailRedirectURL:  getEnv("GMAIL_REDIRECT_URL", "http://localhost:3000/auth/callback"),
		EncryptionKey:     getEnv("ENCRYPTION_KEY", "default-dev-key-change-in-production"),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
