package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ctxKey is a private type for request-scoped context values.
type ctxKey string

const requestIDKey ctxKey = "requestID"

// publicPrefixes are the routes reachable without a session token: health,
// the OAuth handshake, the boot-time configuration probe, the Pro waitlist
// (whose whole point is to capture cold traffic that has no account yet), and
// the Stripe webhook (which authenticates itself via its HMAC signature).
//
// Only /api/config/status is public, never the whole /api/config/ prefix: the
// Gmail credentials are a single instance-wide config, so an unauthenticated
// write there would hijack the OAuth flow for every user. They now live in
// environment variables and have no HTTP surface at all.
var publicPrefixes = []string{
	"/health",
	"/metrics",
	"/api/auth/",
	"/api/config/status",
	"/api/providers",
	"/api/waitlist",
	"/api/billing/webhook",
}

func isPublicPath(path string) bool {
	for _, p := range publicPrefixes {
		if path == p || strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// authMiddleware enforces a valid session token on every non-public route.
//
// Crucially, it ALWAYS strips any client-supplied X-User-Email header first and
// only re-sets it after verifying the bearer token. Downstream handlers keep
// reading X-User-Email, but its value is now server-vouched rather than blindly
// trusted from the request, closing the impersonation hole.
//
// On public routes it still identifies the caller when it can: a valid token
// gets X-User-Email set, an absent or bad one is simply not an error. That lets
// a public handler like the waitlist tell an existing user from an anonymous
// visitor without ever requiring a session.
func (h *Handler) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Never let a caller smuggle in an identity.
		r.Header.Del("X-User-Email")

		public := isPublicPath(r.URL.Path)

		token := bearerToken(r)
		if token == "" {
			if public {
				next.ServeHTTP(w, r)
				return
			}
			writeError(w, http.StatusUnauthorized, "Authentication required")
			return
		}

		email, err := h.auth.VerifySession(token)
		if err != nil {
			if public {
				// An expired token must not lock someone out of a public route.
				next.ServeHTTP(w, r)
				return
			}
			writeError(w, http.StatusUnauthorized, "Invalid or expired session")
			return
		}

		r.Header.Set("X-User-Email", email)
		next.ServeHTTP(w, r)
	})
}

// bearerToken extracts a token from the Authorization header, tolerating both
// "Bearer <t>" and a bare token for convenience.
func bearerToken(r *http.Request) string {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if h == "" {
		return ""
	}
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return h
}

// recoverMiddleware turns a panic in any handler into a 500 instead of crashing
// the whole server process, logging the offending request for diagnosis.
func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[panic] %s %s: %v", r.Method, r.URL.Path, rec)
				writeError(w, http.StatusInternalServerError, "Internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// requestIDMiddleware attaches a short request id to the context and response,
// so a log line can be correlated with a specific client request.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func newRequestID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "req-" + time.Now().Format("150405.000000")
	}
	return hex.EncodeToString(b)
}

// statusRecorder captures the response status code for logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// loggingMiddleware emits one structured line per request once it completes.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		id, _ := r.Context().Value(requestIDKey).(string)
		log.Printf("[req %s] %s %s -> %d (%s)", id, r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond))
	})
}

// --- Rate limiting -------------------------------------------------------

// rateLimiter is a small in-memory token-bucket limiter keyed per client. It
// caps abuse (and runaway clients) without any external dependency. Buckets are
// lazily created and periodically swept so memory stays bounded.
type rateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	rate     float64 // tokens added per second
	capacity float64 // max burst
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(ratePerSec, burst float64) *rateLimiter {
	rl := &rateLimiter{
		buckets:  make(map[string]*bucket),
		rate:     ratePerSec,
		capacity: burst,
	}
	go rl.sweep()
	return rl
}

// allow reports whether a request from key may proceed, consuming one token.
func (rl *rateLimiter) allow(key string) bool {
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()

	b, ok := rl.buckets[key]
	if !ok {
		rl.buckets[key] = &bucket{tokens: rl.capacity - 1, last: now}
		return true
	}

	// Refill based on elapsed time, capped at capacity.
	b.tokens += now.Sub(b.last).Seconds() * rl.rate
	if b.tokens > rl.capacity {
		b.tokens = rl.capacity
	}
	b.last = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweep drops idle buckets so the map does not grow without bound.
func (rl *rateLimiter) sweep() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		rl.mu.Lock()
		for k, b := range rl.buckets {
			if now.Sub(b.last) > 10*time.Minute {
				delete(rl.buckets, k)
			}
		}
		rl.mu.Unlock()
	}
}

// rateLimitMiddleware throttles requests per client (session token if present,
// otherwise remote IP). The webhook is exempt: Stripe controls its own rate.
func (rl *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/billing/webhook") || r.URL.Path == "/health" || r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}
		key := clientKey(r)
		if !rl.allow(key) {
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusTooManyRequests, "Too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientKey identifies the caller for rate-limiting: prefer the bearer token,
// fall back to the network address.
func clientKey(r *http.Request) string {
	if t := bearerToken(r); t != "" {
		return "tok:" + t
	}
	if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return "ip:" + ip
	}
	return "ip:" + r.RemoteAddr
}
