package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// defaultAllowedOrigins is the CORS allow-list used when ALLOWED_ORIGINS is not
// set: local dev (CRA on :3000, nginx on :80) plus the public deployment.
var defaultAllowedOrigins = []string{
	"http://localhost:3000",
	"http://localhost",
	"https://mailsorter.sohbi.dev",
}

// insecureEncryptionKeys are placeholder values shipped in the repo (compose
// fallback, .env.example). Booting with one of these would make every secret
// stored at rest trivially decryptable, so startup must refuse them.
var insecureEncryptionKeys = map[string]bool{
	"default-dev-key-change-in-production":          true,
	"change-this-to-a-secure-random-string-32chars": true,
}

// minEncryptionKeyLen is the minimum master-key length we accept. The key is
// SHA-256-derived to 32 bytes, but a short input means low entropy.
const minEncryptionKeyLen = 32

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
	AllowedOrigins      []string
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
		AllowedOrigins:      getEnvList("ALLOWED_ORIGINS", defaultAllowedOrigins),
	}
}

// Validate enforces the invariants that must hold before the server starts.
// It fails fast on insecure defaults rather than booting in a vulnerable state.
func (c *Config) Validate() error {
	key := c.EncryptionKey
	if key == "" {
		return errors.New("ENCRYPTION_KEY is required")
	}
	if insecureEncryptionKeys[key] {
		return errors.New("ENCRYPTION_KEY is set to a known insecure default — generate a random one (e.g. `openssl rand -base64 32`)")
	}
	if len(key) < minEncryptionKeyLen {
		return fmt.Errorf("ENCRYPTION_KEY is too short (%d chars); use at least %d", len(key), minEncryptionKeyLen)
	}
	return nil
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

// getEnvList reads a comma-separated env var into a slice, trimming whitespace
// and dropping empties. Falls back to defaultValue when unset or all-empty.
func getEnvList(key string, defaultValue []string) []string {
	raw := os.Getenv(key)
	if raw == "" {
		return defaultValue
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return defaultValue
	}
	return out
}
