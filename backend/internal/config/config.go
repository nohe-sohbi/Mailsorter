package config

import (
	"os"
	"strconv"
)

type Config struct {
	MongoDBURI          string
	Port                string
	GmailClientID       string
	GmailClientSecret   string
	GmailRedirectURL    string
	EncryptionKey       string
	MistralAPIKey       string
	MistralModel        string
	MistralMaxRetries   int
	StripeSecretKey     string
	StripePriceID       string
	StripeWebhookSecret string
	AppBaseURL          string
	BuildVersion        string
	DigestHourUTC       int
}

func Load() *Config {
	return &Config{
		MongoDBURI:          getEnv("MONGODB_URI", "mongodb://admin:password@localhost:27017/mailsorter?authSource=admin"),
		Port:                getEnv("PORT", "8080"),
		GmailClientID:       getEnv("GMAIL_CLIENT_ID", ""),
		GmailClientSecret:   getEnv("GMAIL_CLIENT_SECRET", ""),
		GmailRedirectURL:    getEnv("GMAIL_REDIRECT_URL", "http://localhost:3000/auth/callback"),
		EncryptionKey:       getEnv("ENCRYPTION_KEY", "default-dev-key-change-in-production"),
		MistralAPIKey:       getEnv("MISTRAL_API_KEY", ""),
		MistralModel:        getEnv("MISTRAL_MODEL", "mistral-small-latest"),
		MistralMaxRetries:   getEnvInt("MISTRAL_MAX_RETRIES", 2),
		StripeSecretKey:     getEnv("STRIPE_SECRET_KEY", ""),
		StripePriceID:       getEnv("STRIPE_PRICE_ID", ""),
		StripeWebhookSecret: getEnv("STRIPE_WEBHOOK_SECRET", ""),
		AppBaseURL:          getEnv("APP_BASE_URL", "http://localhost:3000"),
		BuildVersion:        getEnv("BUILD_VERSION", "dev"),
		DigestHourUTC:       getEnvInt("DIGEST_HOUR_UTC", 7),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvInt reads an integer env var, falling back to defaultValue when unset or
// unparseable.
func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if n, err := strconv.Atoi(value); err == nil {
			return n
		}
	}
	return defaultValue
}
