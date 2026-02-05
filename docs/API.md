# API Documentation

Base URL: `http://localhost:8080`

## Authentication

Most endpoints require authentication via the `X-User-Email` header containing the user's email address.

```
X-User-Email: user@gmail.com
```

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

Get the Google OAuth authorization URL.

**Response:**
```json
{
  "authUrl": "https://accounts.google.com/o/oauth2/v2/auth?..."
}
```

### Handle OAuth Callback

#### GET /api/auth/callback

Exchange authorization code for access token.

**Query Parameters:**
- `code` (required): Authorization code from Google

**Response:**
```json
{
  "accessToken": "ya29.a0...",
  "userEmail": "user@gmail.com"
}
```

**Error Responses:**
- `400 Bad Request`: Missing code parameter
- `500 Internal Server Error`: Failed to exchange code or get user profile

---

## Email Endpoints

### Get Emails

#### GET /api/emails

Get a list of emails.

**Headers:**
- `X-User-Email` (required): User's email address

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
- `X-User-Email` (required): User's email address

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

## Sorting Rules Endpoints

### Get Sorting Rules

#### GET /api/rules

Get all sorting rules for a user.

**Headers:**
- `X-User-Email` (required): User's email address

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
- `X-User-Email` (required): User's email address

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
- `X-User-Email` (required): User's email address

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
- `X-User-Email` (required): User's email address

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
- `X-User-Email` (required): User's email address

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
