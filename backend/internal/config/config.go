package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
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
	// Edition decides which providers this instance may offer. It is not a
	// packaging label: the self-hosted edition can reach the Gmail API through
	// the operator's own Cloud project and Proton through a local Bridge, and
	// the hosted one can do neither. See internal/provider.
	Edition provider.Edition
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
		MistralModel:        getEnv("MISTRAL_MODEL", "mistral-large-2411"),
		MistralMaxRetries:   getEnvInt("MISTRAL_MAX_RETRIES", 2),
		StripeSecretKey:     getEnv("STRIPE_SECRET_KEY", ""),
		StripePriceID:       getEnv("STRIPE_PRICE_ID", ""),
		StripeWebhookSecret: getEnv("STRIPE_WEBHOOK_SECRET", ""),
		AppBaseURL:          getEnv("APP_BASE_URL", "http://localhost:3000"),
		BuildVersion:        getEnv("BUILD_VERSION", "dev"),
		DigestHourUTC:       getEnvInt("DIGEST_HOUR_UTC", 7),
		AllowedOrigins:      getEnvList("ALLOWED_ORIGINS", defaultAllowedOrigins),
		// Self-hosted is the default because it is the safe one to get wrong:
		// an instance that wrongly believes it is self-hosted offers routes its
		// operator can simply not configure, while one that wrongly believes it
		// is hosted would hide them.
		Edition: provider.Edition(strings.ToLower(strings.TrimSpace(
			getEnv("EDITION", string(provider.EditionSelfHosted))))),
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
		return errors.New("ENCRYPTION_KEY is set to a known insecure default. Generate a random one (e.g. `openssl rand -base64 32`)")
	}
	if len(key) < minEncryptionKeyLen {
		return fmt.Errorf("ENCRYPTION_KEY is too short (%d chars); use at least %d", len(key), minEncryptionKeyLen)
	}
	// An unrecognised edition must not fall back to a default: it decides which
	// mailbox routes exist, so booting on a typo would silently offer the wrong
	// set of providers and only show up as a connection that cannot be made.
	switch c.Edition {
	case provider.EditionSelfHosted, provider.EditionHosted:
	default:
		return fmt.Errorf("EDITION is %q; use %q or %q",
			c.Edition, provider.EditionSelfHosted, provider.EditionHosted)
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
