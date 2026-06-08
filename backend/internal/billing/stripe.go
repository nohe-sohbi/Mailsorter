// Package billing is a minimal, dependency-free Stripe client covering the two
// flows Mailsorter needs: creating Checkout Sessions and verifying webhook
// signatures. It talks to the Stripe REST API directly so the project keeps its
// lean dependency footprint.
package billing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const stripeAPIBase = "https://api.stripe.com"

// webhookTolerance bounds how stale a webhook timestamp may be (replay defense).
const webhookTolerance = 5 * time.Minute

// Client is an authenticated Stripe REST client.
type Client struct {
	secretKey string
	http      *http.Client
}

// New returns a Stripe client bound to the given secret key.
func New(secretKey string) *Client {
	return &Client{
		secretKey: secretKey,
		http:      &http.Client{Timeout: 20 * time.Second},
	}
}

// CheckoutParams describes a subscription Checkout Session to create.
type CheckoutParams struct {
	PriceID           string
	CustomerEmail     string
	ClientReferenceID string
	SuccessURL        string
	CancelURL         string
}

// CreateCheckoutSession creates a subscription-mode Checkout Session and returns
// the hosted checkout URL the client should redirect to.
func (c *Client) CreateCheckoutSession(p CheckoutParams) (string, error) {
	form := url.Values{}
	form.Set("mode", "subscription")
	form.Set("line_items[0][price]", p.PriceID)
	form.Set("line_items[0][quantity]", "1")
	form.Set("success_url", p.SuccessURL)
	form.Set("cancel_url", p.CancelURL)
	form.Set("client_reference_id", p.ClientReferenceID)
	form.Set("allow_promotion_codes", "true")
	if p.CustomerEmail != "" {
		form.Set("customer_email", p.CustomerEmail)
	}

	req, err := http.NewRequest(http.MethodPost, stripeAPIBase+"/v1/checkout/sessions", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(c.secretKey, "")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("stripe checkout failed (%d): %s", resp.StatusCode, string(body))
	}

	var out struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	if out.URL == "" {
		return "", fmt.Errorf("stripe returned an empty checkout url")
	}
	return out.URL, nil
}

// Event is a minimally-decoded Stripe webhook event.
type Event struct {
	Type   string
	Object json.RawMessage
}

// CheckoutSession is the subset of a checkout.session object we act on.
type CheckoutSession struct {
	ClientReferenceID string `json:"client_reference_id"`
	Customer          string `json:"customer"`
	Subscription      string `json:"subscription"`
	CustomerEmail     string `json:"customer_email"`
	CustomerDetails   struct {
		Email string `json:"email"`
	} `json:"customer_details"`
}

// Subscription is the subset of a subscription object we act on.
type Subscription struct {
	ID       string `json:"id"`
	Customer string `json:"customer"`
	Status   string `json:"status"`
}

// ConstructEvent verifies the Stripe-Signature header against the raw payload
// and the webhook signing secret, then returns the decoded event. Verification
// follows Stripe's scheme: signed_payload = "{timestamp}.{payload}", compared
// against the v1 HMAC-SHA256 signature in constant time, within the tolerance.
func ConstructEvent(payload []byte, sigHeader, secret string) (Event, error) {
	var evt Event
	if secret == "" {
		return evt, fmt.Errorf("webhook secret not configured")
	}

	var timestamp string
	signatures := make([]string, 0, 2)
	for _, part := range strings.Split(sigHeader, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "t":
			timestamp = kv[1]
		case "v1":
			signatures = append(signatures, kv[1])
		}
	}
	if timestamp == "" || len(signatures) == 0 {
		return evt, fmt.Errorf("malformed Stripe-Signature header")
	}

	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return evt, fmt.Errorf("invalid signature timestamp")
	}
	if time.Since(time.Unix(ts, 0)) > webhookTolerance {
		return evt, fmt.Errorf("webhook timestamp outside tolerance")
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(payload)
	expected := hex.EncodeToString(mac.Sum(nil))

	matched := false
	for _, sig := range signatures {
		if hmac.Equal([]byte(sig), []byte(expected)) {
			matched = true
			break
		}
	}
	if !matched {
		return evt, fmt.Errorf("signature verification failed")
	}

	var parsed struct {
		Type string `json:"type"`
		Data struct {
			Object json.RawMessage `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return evt, fmt.Errorf("invalid event payload: %w", err)
	}

	return Event{Type: parsed.Type, Object: parsed.Data.Object}, nil
}
