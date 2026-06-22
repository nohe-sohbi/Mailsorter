# API Documentation

Base URL: `http://localhost:8080`

## Authentication

Authenticated endpoints require a **session token** in the `Authorization` header.
The token is issued by `GET /api/auth/callback` after a successful Google login;
it is an HMAC-signed, expiring value that identifies the user.

```
Authorization: Bearer <session-token>
```

The server derives the user identity from this token; clients must **not** send a
raw `X-User-Email` header (any client-supplied value is stripped server-side).
Requests without a valid token receive `401 Unauthorized`.

Public endpoints (no token needed): `/health`, `/api/auth/*`, `/api/config/*`,
and `/api/billing/webhook` (which authenticates via its Stripe signature).

## Endpoints

### Health Check

#### GET /health

Readiness probe: verifies the process can still reach MongoDB (not just that the
HTTP server is up) and reports the running build and uptime. Public (no auth).

**Response:** `200 OK` when healthy, `503 Service Unavailable` when the datastore
ping fails (so an orchestrator can pull the instance from rotation).
```json
{
  "status": "ok",
  "version": "dev",
  "uptimeSeconds": 4211,
  "checks": { "mongo": true }
}
```

### Ops Metrics

#### GET /metrics

In-process request meter. Aggregate-only (no user data), so it can be scraped
without authentication. Counters are bucketed by HTTP method and status **class**
to keep cardinality bounded.

**Response:**
```json
{
  "version": "dev",
  "metrics": {
    "uptimeSeconds": 4211,
    "totalRequests": 1820,
    "byMethod": { "GET": 1500, "POST": 320 },
    "byStatusClass": { "2xx": 1789, "4xx": 28, "5xx": 3 },
    "avgLatencyMs": 42.7,
    "maxLatencyMs": 1503.2
  }
}
```

---

## Auth Endpoints

### Get Authorization URL

#### GET /api/auth/url

Get the Google OAuth authorization URL. The URL embeds a signed, expiring
`state` parameter used for CSRF protection on the callback.

**Response:**
```json
{
  "authUrl": "https://accounts.google.com/o/oauth2/v2/auth?...&state=..."
}
```

### Handle OAuth Callback

#### GET /api/auth/callback

Validate the OAuth `state`, exchange the authorization code, and return a signed
**session token** (used as `Authorization: Bearer …` on subsequent requests).

**Query Parameters:**
- `code` (required): Authorization code from Google
- `state` (required): The signed state value returned by Google; rejected with
  `400` if missing, forged, or expired

**Response:**
```json
{
  "accessToken": "<session-token>",
  "userEmail": "user@gmail.com"
}
```

> `accessToken` is Mailsorter's own session token — **not** the Gmail access
> token, which never leaves the server.

**Error Responses:**
- `400 Bad Request`: Missing code parameter
- `500 Internal Server Error`: Failed to exchange code or get user profile

---

## Email Endpoints

### Get Emails

#### GET /api/emails

Get a list of emails.

**Headers:**
- `Authorization: Bearer <session-token>` (required)

**Query Parameters:**
- `q` (optional): Gmail search query (default: "in:inbox")

**Response:**
```json
[
  {
    "id": "",
    "messageId": "18c8c1f2a3b4d5e6",
    "userId": "user@gmail.com",
    "threadId": "18c8c1f2a3b4d5e6",
    "from": "sender@example.com",
    "to": ["user@gmail.com"],
    "subject": "Test Email",
    "snippet": "This is a test email...",
    "labelIds": ["INBOX", "UNREAD"],
    "receivedDate": "2024-01-01T12:00:00Z",
    "isRead": false,
    "createdAt": "2024-01-01T12:00:00Z"
  }
]
```

**Error Responses:**
- `401 Unauthorized`: Missing user email
- `404 Not Found`: User not found
- `500 Internal Server Error`: Failed to fetch emails

### Sync Emails

#### POST /api/emails/sync

Synchronize emails from Gmail to database.

**Headers:**
- `Authorization: Bearer <session-token>` (required)

**Response:**
```json
{
  "synced": 42,
  "total": 50
}
```

**Error Responses:**
- `401 Unauthorized`: Missing user email
- `404 Not Found`: User not found
- `500 Internal Server Error`: Failed to sync emails

---

## Snooze Endpoints ("Reporter")

Pull a message out of the inbox until a chosen time, then have it return on its
own (marked unread). Wake time is resolved from a friendly preset server-side.

### Snooze an email

#### POST /api/emails/snooze

**Body:**
```json
{ "messageId": "msg-id", "preset": "tomorrow" }
```
`preset` is one of `laterToday`, `thisEvening`, `tomorrow`, `weekend`,
`nextWeek`. Alternatively pass an explicit `wakeAt` (RFC 3339, must be future).

**Response:**
```json
{ "status": "snoozed", "wakeAt": "2026-06-22T08:00:00Z" }
```

### List snoozes

#### GET /api/snoozes?status=scheduled

Returns `{ "snoozes": [ … ] }`, soonest wake first.

### Wake a snooze now

#### POST /api/snoozes/{id}/wake

Brings the email back to the inbox immediately, marked unread.

---

## Protected Senders Endpoints (VIP)

A per-user safety net: while a sender (full address or whole domain, subdomains
included) is protected, no automated pass — AI suggestion, deterministic rule,
sender auto-pilot or bulk action — may archive, trash or delete their mail.
Non-destructive actions (label, star, mark read) are unaffected.

### List protected senders

#### GET /api/protected

Returns `{ "protected": [ { "id", "value", "kind", "note", "createdAt" } ] }`.

### Add a protected sender

#### POST /api/protected

**Body:**
```json
{ "value": "boss@corp.com", "note": "" }
```
The value is normalized and classified server-side (`kind`: `address` or
`domain`). A raw `Name <addr>` header is accepted; the bare address is stored.

### Remove a protected sender

#### DELETE /api/protected/{id}

---

## Unsubscribe Endpoints

Detects mailing-list senders via the `List-Unsubscribe` (RFC 2369) and
`List-Unsubscribe-Post` (RFC 8058) headers, and unsubscribes the user — either
silently server-side (one-click) or by handing back the link to open.

### Get Subscriptions

#### GET /api/subscriptions

Aggregates the senders in the user's mailbox that advertise an unsubscribe link,
ranked by volume.

**Headers:**
- `Authorization: Bearer <session-token>` (required)

**Response:**
```json
[
  {
    "senderEmail": "news@medium.com",
    "senderName": "Medium Daily Digest",
    "emailCount": 37,
    "lastReceived": "2026-06-07T08:12:00Z",
    "sampleMessageId": "18c8c1f2a3b4d5e6",
    "oneClick": true,
    "unsubscribed": false
  }
]
```

### Unsubscribe

#### POST /api/unsubscribe

Unsubscribes from the sender of a given message. When the sender supports RFC
8058 one-click, the POST is performed server-side (`done: true`); otherwise the
`url` / `mailto` is returned for the client to open. Optionally archives the
sender's backlog in the same call.

**Headers:**
- `Authorization: Bearer <session-token>` (required)

**Request Body:**
```json
{
  "messageId": "18c8c1f2a3b4d5e6",
  "alsoArchive": true
}
```

**Response:**
```json
{
  "done": true,
  "method": "one-click",
  "url": "https://medium.com/unsub?token=abc",
  "mailto": "",
  "archived": 37,
  "sender": "news@medium.com"
}
```

**Error Responses:**
- `401 Unauthorized`: Missing user email
- `404 Not Found`: Email not found
- `422 Unprocessable Entity`: Sender exposes no unsubscribe link

---

## Stats Endpoints

The recap is computed from the append-only action ledger (`action_log`), so it
counts every Gmail mutation — direct actions, rules, bulk sweeps, snoozes,
unsubscribes — over the trailing 7 days, not just applied AI suggestions.

### Get Activity Recap

#### GET /api/stats/activity

Returns the 7-day series plus breakdowns by action and by source:

```json
{
  "total": 42,
  "days": [{ "date": "2026-06-15", "count": 3 }, "…7 entries, oldest first…"],
  "byAction": { "archive": 18, "delete": 9, "label": 12, "keep": 3 },
  "bySource": { "direct": 20, "rule": 12, "ai": 10 }
}
```

### Get Daily Digest

#### GET /api/stats/digest

Renders the same 7-day recap into a ready-to-send email digest (subject +
plain-text body + HTML body). This is the content payload also used by the daily
digest scheduler (see Account Settings). Delivery uses the `gmail.send` scope; a
background loop emails opted-in users once a day at their chosen UTC hour.

```json
{
  "subject": "Mailsorter — 3 emails triés aujourd'hui",
  "text": "Votre récap Mailsorter — 21/06/2026\n\nAujourd'hui : 3 emails triés.\n…",
  "html": "<div style=\"…\"><h2>3 emails triés aujourd'hui</h2>…</div>"
}
```

---

## Account Settings

### Get Settings

#### GET /api/account/settings

Returns the caller's tunable settings.

```json
{
  "autoApplyRules": false,
  "digestEnabled": true,
  "digestHourUTC": 7
}
```

### Update Settings

#### PUT /api/account/settings

Persists the settings. `digestHourUTC` is clamped to `0–23` (out-of-range falls
back to the server default `DIGEST_HOUR_UTC`). When `digestEnabled` is true, a
background scheduler emails the 7-day recap once a day at `digestHourUTC` (UTC).

> Accounts connected before the digest feature must **reconnect Gmail** to grant
> the `gmail.send` scope before delivery can succeed.

**Request Body:** `{ "autoApplyRules": bool, "digestEnabled": bool, "digestHourUTC": int }`

### Export account data (RGPD / data portability)

#### GET /api/account/export

Returns a single JSON document with everything Mailsorter stores about the
caller: a **redacted** account profile (never the OAuth tokens or Stripe IDs)
plus every user-owned dataset (rules, protected senders, snoozes, suggestions,
sender preferences, smart labels, unsubscribes, usage, action log, analysis
jobs). Served as a downloadable attachment. The user's Gmail mailbox is not
included — those emails live in Gmail and never leave the user's control.

```json
{
  "exportedAt": "2026-06-22T10:00:00Z",
  "account": { "email": "you@example.com", "plan": "free", "autoApplyRules": false, "digestEnabled": true, "digestHourUTC": 7 },
  "settings": { "autoApplyRules": false, "digestEnabled": true, "digestHourUTC": 7 },
  "rules": [ ... ],
  "protectedSenders": [ ... ],
  "actionLog": [ ... ]
}
```

### Delete account (RGPD / right to erasure)

#### DELETE /api/account

Permanently erases the caller's account record and **all** user-owned datasets
(the same catalog the export covers). Irreversible; the UI gates it behind a
typed confirmation. Gmail is never touched. Returns per-dataset deletion counts.

```json
{ "status": "deleted", "deleted": { "rules": 4, "protectedSenders": 2, "actionLog": 137, "account": 1 } }
```

---

## Billing Endpoints (Stripe)

Pro unlocks unlimited AI analyses. These endpoints are active only when
`STRIPE_SECRET_KEY` / `STRIPE_PRICE_ID` are set; otherwise the UI falls back to a
waitlist and `/api/billing/checkout` returns `503`.

### Create Checkout Session

#### POST /api/billing/checkout

Creates a subscription Checkout Session and returns the hosted URL to redirect to.

**Headers:**
- `Authorization: Bearer <session-token>` (required)

**Response:**
```json
{ "url": "https://checkout.stripe.com/c/pay/cs_test_..." }
```

**Error Responses:**
- `401 Unauthorized`: Missing user email
- `409 Conflict`: User is already on Pro
- `502 Bad Gateway`: Stripe call failed
- `503 Service Unavailable`: Billing not configured

### Stripe Webhook

#### POST /api/billing/webhook

Receives Stripe events. The raw body is verified against the `Stripe-Signature`
header (HMAC-SHA256, 5-minute tolerance) before processing. Keeps the user's
`plan` in sync: `checkout.session.completed` → pro;
`customer.subscription.updated/deleted` → pro/free.

**Headers:**
- `Stripe-Signature` (required): Stripe webhook signature

**Response:** `200 OK` on success, `400 Bad Request` on signature failure.

> Usage/plan is reported by `GET /api/usage` → `{ used, limit, plan, billingOn }`
> where `limit: -1` means unlimited (Pro).

---

### Manage Subscription (Billing Portal)

#### POST /api/billing/portal

Creates a Stripe Billing Portal session for the current Pro user so they can
update payment details, switch plans, or cancel — entirely self-service. Returns
the hosted URL to redirect to.

**Headers:**
- `Authorization: Bearer <session-token>` (required)

**Response:**
```json
{ "url": "https://billing.stripe.com/p/session/..." }
```

**Error Responses:**
- `401 Unauthorized`: Missing user email
- `404 Not Found`: No subscription / Stripe customer to manage
- `502 Bad Gateway`: Stripe call failed
- `503 Service Unavailable`: Billing not configured

---

## Sorting Rules Endpoints

Deterministic, **AI-free** triage. A rule pairs conditions with an action; when
the conditions match an email, the action is applied directly via Gmail — no
model call, no quota consumed. Rules are the free, predictable complement to the
AI suggestions.

A rule has the shape:

```json
{
  "id": "507f1f77bcf86cd799439011",
  "userId": "user@gmail.com",
  "name": "Archiver les newsletters Acme",
  "enabled": true,
  "matchAll": true,
  "conditions": [
    { "field": "from", "operator": "contains", "value": "acme.com" }
  ],
  "actions": [
    { "type": "label", "labelName": "Newsletters" },
    { "type": "archive" }
  ],
  "action": "label",
  "labelName": "Newsletters",
  "priority": 0,
  "appliedCount": 12,
  "createdAt": "2026-06-20T12:00:00Z",
  "updatedAt": "2026-06-20T12:00:00Z"
}
```

- **`matchAll`** — `true` ANDs every condition, `false` ORs them.
- **Condition `field`** — `from`, `subject`, `snippet`, `to`, `body`.
- **Condition `operator`** — text: `contains`, `notContains`, `equals`,
  `notEquals`, `startsWith`, `endsWith`, `regex` (all case-insensitive except
  `regex`); temporal: `olderThan` / `newerThan`, whose `value` is a **number of
  days** compared against the email's received date (an undated email never
  matches a temporal condition).
- **`actions`** — an **ordered list** of actions applied in sequence (e.g.
  *label* then *archive*). Each is `{ "type": ..., "labelName": ... }` where
  `type` is `archive`, `trash`, `label` (requires `labelName`), `markRead` or
  `star`. A protected (VIP) sender has destructive actions (archive/trash)
  skipped while non-destructive actions in the same rule still run.
- **`action` / `labelName`** — legacy single-action fields, kept for backward
  compatibility. A client may send either shape; the server normalizes them and
  mirrors the primary (first) action onto these fields. Rules created before
  multi-action, and one-click sender rules, use only these.
- **`priority`** — lower runs first; the first matching rule wins per email.

### Get Sorting Rules

#### GET /api/rules

Returns the caller's rules, ordered by priority.

**Response:** `{ "rules": [ <rule>, ... ] }`

### Create Sorting Rule

#### POST /api/rules

Validates and creates a rule. Returns the created rule. **Status:** `201 Created`.

**Request Body:** rule fields without `id`/timestamps (see shape above).

**Error Responses:**
- `400 Bad Request`: Invalid body or validation error (e.g. missing label for a
  `label` action, invalid regex, unknown field/operator)

### Update Sorting Rule

#### PUT /api/rules/:id

Validates and updates an existing rule. **Response:** `{ "status": "updated" }`.

**Error Responses:**
- `400 Bad Request`: Invalid rule ID or validation error
- `404 Not Found`: Rule not found

### Delete Sorting Rule

#### DELETE /api/rules/:id

Deletes a rule. **Response:** `{ "status": "deleted" }`.

**Error Responses:**
- `400 Bad Request`: Invalid rule ID
- `404 Not Found`: Rule not found

### Apply Sorting Rules

#### POST /api/rules/apply

Runs every **enabled** rule across the current inbox (up to 200 messages). Each
email is matched in priority order and the first match's action is applied. Never
calls the AI, never consumes quota.

**Response:**
```json
{
  "applied": 18,
  "scanned": 120,
  "byRule": { "Archiver les newsletters Acme": 12, "Promos": 6 }
}
```

---

## Labels Endpoints

### Get Labels

#### GET /api/labels

Get all Gmail labels for a user.

**Headers:**
- `Authorization: Bearer <session-token>` (required)

**Response:**
```json
[
  {
    "id": "Label_1",
    "name": "INBOX",
    "messageListVisibility": "show",
    "labelListVisibility": "labelShow",
    "type": "system"
  },
  {
    "id": "Label_2",
    "name": "Work",
    "messageListVisibility": "show",
    "labelListVisibility": "labelShow",
    "type": "user",
    "color": {
      "backgroundColor": "#434343",
      "textColor": "#ffffff"
    }
  }
]
```

**Error Responses:**
- `401 Unauthorized`: Missing user email
- `404 Not Found`: User not found
- `500 Internal Server Error`: Failed to fetch labels

---

## Error Responses

All endpoints may return the following errors:

### 400 Bad Request
```json
{
  "error": "Invalid request body"
}
```

### 401 Unauthorized
```json
{
  "error": "User email required"
}
```

### 404 Not Found
```json
{
  "error": "Resource not found"
}
```

### 500 Internal Server Error
```json
{
  "error": "Internal server error: details..."
}
```

## CORS

The API is configured to accept requests from:
- `http://localhost:3000`
- `http://localhost`

Allowed methods: GET, POST, PUT, DELETE, OPTIONS

Allowed headers: Content-Type, Authorization, X-User-Email
