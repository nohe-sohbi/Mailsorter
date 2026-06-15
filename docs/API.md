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

Check if the API is running.

**Response:**
```json
{
  "status": "ok"
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

## Sorting Rules Endpoints

### Get Sorting Rules

#### GET /api/rules

Get all sorting rules for a user.

**Headers:**
- `Authorization: Bearer <session-token>` (required)

**Response:**
```json
[
  {
    "id": "507f1f77bcf86cd799439011",
    "userId": "user@gmail.com",
    "name": "Work Emails",
    "description": "Sort work-related emails",
    "conditions": [
      {
        "field": "from",
        "operator": "contains",
        "value": "@company.com"
      }
    ],
    "actions": [
      {
        "type": "addLabel",
        "value": "Work"
      }
    ],
    "priority": 1,
    "enabled": true,
    "createdAt": "2024-01-01T12:00:00Z",
    "updatedAt": "2024-01-01T12:00:00Z"
  }
]
```

### Create Sorting Rule

#### POST /api/rules

Create a new sorting rule.

**Headers:**
- `Authorization: Bearer <session-token>` (required)

**Request Body:**
```json
{
  "name": "Work Emails",
  "description": "Sort work-related emails",
  "conditions": [
    {
      "field": "from",
      "operator": "contains",
      "value": "@company.com"
    }
  ],
  "actions": [
    {
      "type": "addLabel",
      "value": "Work"
    }
  ],
  "priority": 1,
  "enabled": true
}
```

**Condition Fields:**
- `field`: `from`, `to`, `subject`, `body`
- `operator`: `contains`, `equals`, `startsWith`, `endsWith`
- `value`: String to match

**Action Types:**
- `addLabel`: Add a label (requires `value`)
- `removeLabel`: Remove a label (requires `value`)
- `markAsRead`: Mark as read (no `value` needed)
- `archive`: Archive the email (no `value` needed)

**Response:**
```json
{
  "id": "507f1f77bcf86cd799439011",
  "userId": "user@gmail.com",
  "name": "Work Emails",
  ...
}
```

**Status:** `201 Created`

### Update Sorting Rule

#### PUT /api/rules/:id

Update an existing sorting rule.

**Headers:**
- `Authorization: Bearer <session-token>` (required)

**URL Parameters:**
- `id`: Rule ID

**Request Body:** Same as Create Sorting Rule

**Response:**
```json
{
  "id": "507f1f77bcf86cd799439011",
  "userId": "user@gmail.com",
  ...
}
```

**Error Responses:**
- `400 Bad Request`: Invalid rule ID
- `404 Not Found`: Rule not found
- `500 Internal Server Error`: Failed to update rule

### Delete Sorting Rule

#### DELETE /api/rules/:id

Delete a sorting rule.

**Headers:**
- `Authorization: Bearer <session-token>` (required)

**URL Parameters:**
- `id`: Rule ID

**Response:** `204 No Content`

**Error Responses:**
- `400 Bad Request`: Invalid rule ID
- `404 Not Found`: Rule not found
- `500 Internal Server Error`: Failed to delete rule

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
