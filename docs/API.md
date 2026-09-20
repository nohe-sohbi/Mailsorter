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

Public endpoints (no token needed): `/health`, `/metrics`, `/api/auth/*`,
`/api/config/status`, `/api/waitlist`, and `/api/billing/webhook` (which
authenticates via its Stripe signature).

## Errors

Every failure, from any layer (middleware, routing, handler), is a JSON envelope:

```json
{ "error": "Choisissez une échéance valide", "status": 400 }
```

`Content-Type` is always `application/json`, including on `401`, `429` and `500`.
Clients can therefore read one shape everywhere; the SPA does exactly that, in
`apiError` (`frontend/src/services/api.js`).

Note that only `/api/config/status` is public, never the whole `/api/config/`
prefix. The Gmail credentials are a single instance-wide OAuth app: they are
read from the environment at boot (`GMAIL_CLIENT_ID`, `GMAIL_CLIENT_SECRET`,
`GMAIL_REDIRECT_URL`) and have no HTTP surface, so they can be neither read nor
written over the API.

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

> `accessToken` is Mailsorter's own session token, **not** the Gmail access
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
- `maxResults` (optional): page size, default 50, capped at 500
- `pageToken` (optional): opaque, echoed back from a previous answer

**Response:**
```json
{
  "emails": [
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
  ],
  "nextPageToken": "",
  "resultSizeEstimate": 120
}
```

Bodies are deliberately omitted: the page carries up to 500 messages and the
reader fetches the one it opens (`GET /api/emails/{id}`).

On a mailbox reached over IMAP the answer is served from the stored mailbox and
`labelIds` is empty, because a message there is in one folder rather than in a
set of labels; `folder` carries that folder instead. See "Transports and what
works on each".

**Error Responses:**
- `401 Unauthorized`: Missing user email
- `404 Not Found`: User not found
- `500 Internal Server Error`: Failed to fetch emails
- `501 Not Implemented`: a query term a stored mailbox cannot answer (IMAP only)

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

### Direct action on a message

#### POST /api/emails/action

Apply a single, direct action to one message via Gmail: no AI, no rule. Used by
the inbox for one-off triage.

**Headers:**
- `Authorization: Bearer <session-token>` (required)

**Request body:**
```json
{ "messageId": "18c...", "action": "archive" }
```
`action` is one of `archive`, `delete` (alias `trash`), `unarchive`, `untrash`,
`read`, `unread`, `star`, `unstar`. Forward triage actions
(`archive`/`delete`/`read`/`star`) are recorded in the action ledger; the inverse
actions are not. The vocabulary matches `/api/emails/batch-action`, so the reader
and the shortcuts can express the same things one message at a time.

**Response:** `200 OK`
```json
{ "status": "ok", "action": "archive" }
```

**Error Responses:**
- `400 Bad Request`: Missing `messageId` or unsupported `action`
- `401 Unauthorized`: Missing user email
- `500 Internal Server Error`: Gmail call failed

---

### Get one message (with its body)

#### GET /api/emails/{id}

Return a single message, decoded. `GET /api/emails` deliberately omits bodies
(it ships up to 100 messages per page), so the reader fetches the one it is
about to display.

**Query parameters:**
- `markRead` (optional): `1` also marks the message read in the mailbox (the
  `UNREAD` label goes on Gmail, the `\Seen` flag is set over IMAP) and updates
  the local cache. Opening an email is user intent rather than automation, so
  this is **not** written to the action ledger.

**Response:** `200 OK`
```json
{
  "messageId": "18c...",
  "from": "Acme <news@acme.com>",
  "subject": "Votre facture",
  "snippet": "Bonjour…",
  "body": "version texte",
  "bodyHtml": "<p>version html</p>",
  "attachments": [
    { "filename": "facture.pdf", "mimeType": "application/pdf", "size": 20481, "attachmentId": "ANGjdJ..." }
  ],
  "labelIds": ["INBOX"],
  "isRead": true
}
```
`body` and `bodyHtml` are both returned when the sender provided both: the reader
renders the HTML, the deterministic rule engine matches on the text. Either may
be absent.

`attachmentId` is the handle the bytes live behind (see the download route
below). A part without one carries its data inline in the message payload and is
not downloadable; inline images a newsletter references are filtered out
entirely.

On a mailbox reached over IMAP the fetch PEEKS: it downloads the body without
setting `\Seen`, so opening a message stays distinct from reading it and only
`markRead=1` changes the flag. `attachments` and `labelIds` come back empty
there: the download route has not been ported, and a chip that answers 501 when
clicked is worse than no chip.

**Error Responses:**
- `400 Bad Request`: Missing id
- `401 Unauthorized`: Missing or expired session
- `502 Bad Gateway`: the mailbox could not return the message

---

### Download an attachment

#### GET /api/emails/{id}/attachments/{attachmentId}

Stream one attachment of one message. Gmail never ships attachment data with the
message, so this resolves the message first (to learn the part's real filename
and MIME type, and to prove the message actually carries that id) and then
fetches the bytes.

Only a part of the named message may be served: an attachment id the message
does not carry is a 404, never a passthrough to Gmail.

**Response:** `200 OK`, the raw file, with:
- `Content-Type`: the part's MIME type when it parses, `application/octet-stream` otherwise
- `Content-Disposition`: `attachment`, with the filename sanitized (no directory
  components, no control characters) and published in both the quoted and the
  RFC 5987 `filename*` form so accents survive
- `X-Content-Type-Options: nosniff`

The route is session-authenticated, so a plain `<a href>` cannot fetch it: the
SPA downloads through the API client and hands the browser an object URL.

**Error Responses:**
- `400 Bad Request`: Missing message or attachment id
- `401 Unauthorized`: Missing or expired session
- `404 Not Found`: This message carries no such attachment
- `413 Payload Too Large`: Over 30 MiB
- `502 Bad Gateway`: Gmail could not return the message or the bytes

---

### Act on a whole selection

#### POST /api/emails/batch-action

Apply one triage action to a list of messages in a single request. Protected
senders are shielded from destructive actions exactly as in the rules and
bulk-by-sender paths; a message that fails is counted rather than aborting the
batch.

**Request body:**
```json
{ "messageIds": ["18c...", "18d..."], "action": "archive", "labelName": "" }
```
`action` is one of `archive`, `delete`, `read`, `unread`, `star`, `unstar`,
`label`. `labelName` is required for `label` and creates the label if it does not
exist. At most 200 ids per request.

**Response:** `200 OK`
```json
{
  "applied": ["18c..."],
  "failed": 0,
  "protectedSkipped": 1,
  "total": 2,
  "reversible": true,
  "inverse": "unarchive"
}
```
`reversible` tells the client whether to offer an "Annuler" affordance;
`applied` carries the exact ids to hand back to `/batch-undo`.

**Error Responses:**
- `400 Bad Request`: Empty selection, unsupported action, missing label name, or over 200 ids
- `401 Unauthorized`: Missing or expired session
- `502 Bad Gateway`: The label could not be created

---

#### POST /api/emails/batch-undo

Reverse a batch the client just applied. Separate from the per-entry history undo
because the client already holds the exact ids, which is what makes undoing a
100-email action one request instead of a hundred.

**Request body:**
```json
{ "messageIds": ["18c..."], "action": "label", "labelName": "Factures" }
```
`archive`, `delete` and `label` are reversible. `labelName` is required when
undoing a `label` and is the name the client just applied: the server resolves it
to a Gmail label id, which the client does not have. Marking as read stays
one-way on purpose.

The matching ledger entries are marked undone so the history does not offer a
second "Annuler" on the same work. Note that the per-entry history undo
(`POST /api/activity/undo`) still cannot reverse a labelling: the action log
stores no label name, so it would have nothing to remove.

**Response:** `200 OK`
```json
{ "restored": 1, "total": 1 }
```

**Error Responses:**
- `400 Bad Request`: Non-reversible action, empty list, missing label name, or over 200 ids
- `401 Unauthorized`: Missing or expired session

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
`nextWeek`. Alternatively pass an explicit `wakeAt` (RFC 3339): it wins over the
preset and is validated on its own terms, since a preset cannot produce a bad
time but a date picker can. It must be in the future and within one year
(`snooze.MaxHorizon`), so a mistyped year cannot hide an email for a decade.

**Response:**
```json
{ "status": "snoozed", "wakeAt": "2026-06-22T08:00:00Z" }
```

**Error Responses:**
- `400 Bad Request`: Unknown preset, or a wake time in the past or beyond one year
- `401 Unauthorized`: Missing or expired session
- `502 Bad Gateway`: Gmail refused the label or the move

### Snooze a whole selection

#### POST /api/emails/batch-snooze

The same choice of preset or explicit `wakeAt`, applied to a selection so one
wake time covers every message in it.

**Body:**
```json
{ "messageIds": ["18c...", "18d..."], "preset": "weekend" }
```
At most 200 ids per request. Protected senders are shielded exactly as in
`/batch-action`: a bulk snooze takes mail out of the inbox, which is what the VIP
list exists to veto, and a sender that could not be established is treated as
possibly protected rather than assumed harmless.

**Response:** `200 OK`
```json
{
  "snoozed": ["18c..."],
  "failed": 0,
  "protectedSkipped": 1,
  "total": 2,
  "wakeAt": "2026-06-22T08:00:00Z"
}
```

**Error Responses:**
- `400 Bad Request`: Empty selection, over 200 ids, or an invalid deadline
- `401 Unauthorized`: Missing or expired session
- `502 Bad Gateway`: The snooze label could not be prepared

### List snoozes

#### GET /api/snoozes?status=scheduled

Returns `{ "snoozes": [ … ] }`, soonest wake first.

### Wake a snooze now

#### POST /api/snoozes/{id}/wake

Brings the email back to the inbox immediately, marked unread.

---

## Protected Senders Endpoints (VIP)

A per-user safety net: while a sender (full address or whole domain, subdomains
included) is protected, no automated pass (AI suggestion, deterministic rule,
sender auto-pilot or bulk action) may archive, trash or delete their mail.
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
`List-Unsubscribe-Post` (RFC 8058) headers, and unsubscribes the user, either
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

The server-side POST is the one request in the API whose address is chosen by a
stranger: it comes from the `List-Unsubscribe` header of a received email. It is
therefore restricted by `internal/egress`, which requires `https` and refuses any
address that is not publicly routable (loopback, RFC 1918, link-local including
the cloud metadata endpoint, carrier-grade NAT), on the resolved address and on
every redirect hop. A refused endpoint is not an error for the caller: the
response falls back to `done: false` with the `url` for the client to open, which
is where a request driven by a stranger belongs.

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

## Transports and what works on each

A user's mailbox is reached either through the Gmail API (the Google OAuth path,
and the only one before `mail_accounts` existed) or over IMAP (a mailbox
connected through the endpoints below). The transport is resolved per request
from the stored connection, never guessed: a datastore failure fails the request
rather than resolving to Gmail.

Most of the API was written when every user was a Gmail user and has not been
ported. Those endpoints answer **`501 Not Implemented`** for a caller whose
mailbox is on IMAP, rather than acting on the wrong message. Ported so far:

| Works on IMAP | Answers 501 on IMAP |
|---|---|
| `POST /api/emails/sync` (no rules applied) | Rules, AI suggestions, snooze, unsubscribe, attachments, labels |
| `POST /api/emails/action` (archive, trash, read, unread, star, unstar) | `label` / `unlabel`, which plain IMAP cannot express at all |
| `GET /api/emails` (served from the stored mailbox, see below) | the `has:`, `larger:`, `smaller:`, `label:`, `filename:`, `is:starred` and non-inbox `in:` query terms |
| `GET /api/emails/{id}` (body included, no attachment list) | `GET /api/emails/{id}/attachments/{attachmentId}` |
| `GET /api/stats` (inbox total and unread, counted locally) | the sent, draft, spam, trash and per-label counters |

### The listing is served from the stored mailbox on IMAP

Over the Gmail API a listing is a call to Google: the query goes out as written.
Over IMAP there is nobody to hand it to. `IMAP SEARCH` is a round trip on a
stateful socket with a folder selected, and a listing runs on every page load,
every filter chip and every keystroke, so `GET /api/emails` answers from the
mirror that `POST /api/emails/sync` writes.

Two consequences worth knowing:

- A message that arrived since the last sync is not in the answer. The SPA syncs
  before it lists, and a background loop syncs every 30 min, so the window is
  the one the inbox already had.
- `resultSizeEstimate` counts what is mirrored, not what the mailbox holds.

A query term the mirror cannot answer is **refused with 501, naming the term**,
rather than dropped. Dropping `has:attachment` does not produce an error, it
produces a full inbox under a button labelled "Pieces jointes", and nothing on
screen would say the filter did nothing.

## Mailbox Endpoints

How a user connects a mailbox over IMAP, which is the hosted edition's way in:
a shared OAuth client can never reach the Gmail API at scale (see the
100-authorization cap in `internal/provider`), so the hosted service asks for an
address and an app password instead.

The stored app password is sealed with AES-256-GCM, exactly like a Google OAuth
token, and it is never returned by any endpoint. It is also stripped from the
RGPD export: `account.SecretFields` names it, and the export redacts it on the
way out, because an export leaves the server and ends up in places the
encryption key's threat model never covered.

### Read the connected mailbox

#### GET /api/mailbox

**Headers:**
- `Authorization: Bearer <session-token>` (required)

**Response:** `200 OK`. `mailbox` is `null` when nothing is connected, which is
a normal state rather than an error.
```json
{
  "mailbox": {
    "provider": "orange",
    "transport": "imap",
    "username": "someone@orange.fr",
    "host": "imap.orange.fr",
    "port": 993,
    "tls": "implicit",
    "createdAt": "2026-09-17T19:00:00Z",
    "updatedAt": "2026-09-17T19:00:00Z"
  }
}
```

### Connect a mailbox

#### POST /api/mailbox/connect

Proves the connection works, then stores it. The connection is tested BEFORE
anything is written: a password accepted on faith becomes a mailbox that fails
on every later sync, with the failure surfacing far from the screen where it
could be fixed.

**Request Body:** an address and a password, and nothing else. The provider,
host, port and TLS mode are resolved from the address against
`internal/provider`. A body that could name its own host would let any caller
make the server open an authenticated connection wherever they liked.
```json
{
  "address": "someone@orange.fr",
  "password": "app-password"
}
```

**Response:** `200 OK`, the stored mailbox, in the shape `GET /api/mailbox`
returns under `mailbox`.

**Error Responses:**
- `400 Bad Request`: missing address or password
- `401 Unauthorized`: no session, or the provider rejected the credentials
- `422 Unprocessable Entity`: this provider is not reachable over IMAP in the
  running edition (Gmail in `self-hosted`, where the API route is used instead)
- `502 Bad Gateway`: the mail server could not be reached

### Disconnect

#### DELETE /api/mailbox

Forgets the connection, app password included. This is also how a user revokes
Mailsorter's access without going through their provider. The mailbox itself is
never touched.

**Response:** `200 OK`, `{ "disconnected": true }`

---

## Stats Endpoints

The recap is computed from the append-only action ledger (`action_log`), so it
counts every Gmail mutation (direct actions, rules, bulk sweeps, snoozes,
unsubscribes) over the trailing 7 days, not just applied AI suggestions.

### Get mailbox stats

#### GET /api/stats

Live counts pulled from Gmail for the current mailbox.

On a mailbox reached over IMAP the counters are read from the stored mailbox
instead: `totalMessages`, `inboxCount` and `unreadCount` are the mirror's, and
the sent, draft, spam, trash and `labelStats` counters stay at zero rather than
being invented. A counter that cannot be read is an error, never a zero: an
empty inbox is a state the app celebrates, so it must not be what a failed
count looks like.

**Response:** `200 OK` (a `MailboxStats`)
```json
{
  "totalMessages": 10432, "totalThreads": 8210, "unreadCount": 57,
  "inboxCount": 120, "sentCount": 900, "draftCount": 3,
  "spamCount": 12, "trashCount": 40, "labelStats": []
}
```

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
  "subject": "Mailsorter : 3 emails triés aujourd'hui",
  "text": "Votre récap Mailsorter du 21/06/2026\n\nAujourd'hui : 3 emails triés.\n…",
  "html": "<div style=\"…\"><h2>3 emails triés aujourd'hui</h2>…</div>"
}
```

---

## Action History Endpoints

The same append-only ledger that powers the recap is exposed as a transparent,
actionable history. Forward triage actions and their reversals (recorded with
source `undo`) both appear.

### Get action history

#### GET /api/activity/log?source=&limit=&before=&q=

Returns the caller's most recent ledger entries, newest first. Each entry is
flagged `undoable` (it has a clean inverse and has not been undone yet).

**Query parameters:**
- `source` (optional): filter on (`direct`, `rule`, `ai`, `ai-auto`, `bulk`,
  `snooze`, `unsubscribe`, `undo`)
- `limit`: defaults to `50`, capped at `200`
- `before`: RFC 3339 cursor, return entries strictly older than this. Pass the
  previous page's `nextBefore`. Cursoring on `createdAt` keeps paging stable
  while new actions land at the top.
- `q`: free-text search over the acted-on message's subject and sender

Entries carry `subject`/`from` so the history can say *which* email it is talking
about. They are captured when the action is logged, and resolved from the stored
mailbox at read time for entries written before the ledger carried an identity.
Note that `q` matches only what is **stored on the entry**: those older entries
still render, but cannot be searched.

`nextBefore` is non-empty when the page was full, i.e. there is probably more.

```json
{
  "entries": [
    {
      "id": "665…",
      "messageId": "18f…",
      "action": "archive",
      "source": "rule",
      "subject": "Votre relevé de compte",
      "from": "Banque <no-reply@banque.fr>",
      "undone": false,
      "createdAt": "2026-06-22T09:14:00Z",
      "undoable": true
    }
  ],
  "nextBefore": "2026-06-22T09:14:00Z"
}
```

### Undo an action

#### POST /api/activity/undo

Reverses one recorded action by replaying its inverse Gmail mutation
(`archive`→`unarchive`, `delete`/`trash`→`untrash`, `read`→`unread`). The entry
is marked `undone` and the reversal is itself appended to the ledger (source
`undo`). Only stateful triage actions are reversible; others return `400`.

**Request Body:** `{ "id": "665…" }`

**Responses:** `200 { "status": "undone", "action": "unarchive" }` ·
`400` not reversible · `404` not found · `409` already undone.

---

## Account Settings

### Get Settings

#### GET /api/account/settings

Returns the caller's tunable settings.

```json
{
  "autoApplyRules": false,
  "autoSyncEnabled": false,
  "digestEnabled": true,
  "digestHourUTC": 7
}
```

### Update Settings

#### PUT /api/account/settings

Persists the settings. **Partial merge:** every field is optional and only the
fields present in the body are updated, so one screen can toggle its setting
without clobbering the others. `digestHourUTC` is clamped to `0-23` (out-of-range
falls back to the server default `DIGEST_HOUR_UTC`). When `digestEnabled` is
true, a background scheduler emails the 7-day recap once a day at `digestHourUTC`
(UTC). When `autoSyncEnabled` is true, a background scheduler periodically syncs
the inbox (and applies rules when `autoApplyRules` is on) with no manual click.
Returns the full, merged settings.

> Accounts connected before the digest feature must **reconnect Gmail** to grant
> the `gmail.send` scope before delivery can succeed.

**Request Body (all optional):** `{ "autoApplyRules": bool, "autoSyncEnabled": bool, "digestEnabled": bool, "digestHourUTC": int }`

### Export account data (RGPD / data portability)

#### GET /api/account/export

Returns a single JSON document with everything Mailsorter stores about the
caller: a **redacted** account profile (never the OAuth tokens or Stripe IDs)
plus every user-owned dataset (rules, protected senders, snoozes, suggestions,
sender preferences, smart labels, unsubscribes, usage, action log, analysis
jobs, saved searches). Served as a downloadable attachment. The user's Gmail mailbox is not
included: those emails live in Gmail and never leave the user's control.

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

### Send today's digest now

#### POST /api/account/digest/test

Email the caller their 7-day recap immediately, through their own Gmail, using
the same rendering and the same delivery path as the scheduler.

The digest was the one feature nobody could verify: you turned it on and waited a
day to learn whether your Gmail grant still carried the send scope. This closes
that loop. It deliberately does **not** stamp `digestLastSentAt`, so a test never
consumes the day's real digest, and it sends even when the week is empty (unlike
the scheduler, which has no reason to mail "0 emails triés" unprompted).

**Response:** `200 OK`
```json
{ "status": "sent", "subject": "Mailsorter : 34 emails triés cette semaine", "total": 34 }
```

**Error Responses:**
- `401 Unauthorized`: Missing session, or a dead Google grant (the SPA offers
  "Reconnecter Gmail")
- `502 Bad Gateway`: Gmail refused the send, typically a missing `gmail.send`
  scope on an older authorization

The matching preview, without sending anything, is `GET /api/stats/digest`.


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
update payment details, switch plans, or cancel, entirely self-service. Returns
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

## AI Sorting Endpoints

The AI triage surface. Every email is classified into an action
(`archive` / `delete` / `label` / `keep`) with a confidence score and a short
reasoning, persisted as a pending suggestion the user reviews and applies.

> All endpoints in this section require the AI to be configured (`MISTRAL_API_KEY`)
> and answer `503 Service Unavailable` otherwise. On the free plan they answer
> `402 Payment Required` once the monthly quota is exhausted (see `GET /api/usage`).

### Analyze (synchronous)

#### POST /api/ai/analyze

Analyze a set of Gmail message IDs now. Resolves sender auto-pilot and the content
cache first, batches the rest to Mistral, and stores one pending suggestion per
email. Auto-applied senders are applied inline.

**Request body:**
```json
{ "emailIds": ["18c...", "18d..."] }
```

**Response:** `200 OK`
```json
{
  "suggestions": [
    {
      "id": "662...", "emailId": "18c...", "action": "archive",
      "labelName": "", "labelId": "", "confidence": 0.92,
      "reasoning": "Promotional newsletter", "status": "pending"
    }
  ],
  "autoApplied": 3,
  "cachedHits": 5
}
```

### Analyze (asynchronous job)

#### POST /api/ai/analyze-async

Enqueue the same analysis as a background job, preferred for large batches. The
email-ID list is capped server-side.

**Request body:** `{ "emailIds": ["18c...", "18d..."] }`

**Response:** `202 Accepted`
```json
{ "jobId": "662...", "status": "queued" }
```

### Job status

#### GET /api/ai/jobs/{id}

Poll an analysis job.

**Response:** `200 OK` (an `AnalysisJob`)
```json
{
  "id": "662...", "status": "running", "total": 120, "processed": 40,
  "autoApplied": 6, "suggestionsCreated": 30, "cachedHits": 4
}
```
`status` is one of `queued`, `running`, `done`, `error` (with an `error` field).

### Learn a sender

#### POST /api/ai/analyze-sender

Analyze a sender's recent emails and persist a `SenderPreference` (default action,
optional auto-apply).

**Request body:** `{ "senderEmail": "news@acme.com" }`

**Response:** `200 OK`
```json
{ "analysis": { "...": "AI verdict" }, "emailCount": 12, "preference": { "...": "SenderPreference" } }
```

### Apply one suggestion

#### POST /api/ai/apply

Apply a single pending suggestion to Gmail, mark it `applied`, and log it. A
protected (VIP) sender is never auto-archived/trashed.

**Request body:** `{ "suggestionId": "662..." }`

**Response:** `200 OK` → `{ "status": "applied" }`

### Apply many suggestions

#### POST /api/ai/apply-batch

Apply a list of suggestions in one request (token refreshed once, looped
server-side).

**Request body:** `{ "suggestionIds": ["a", "b", "c"] }`

**Response:** `200 OK`
```json
{ "applied": 8, "failed": 1, "total": 10, "appliedIds": ["a", "b"], "protectedSkipped": 1 }
```

### Apply in bulk for a sender

#### POST /api/ai/apply-bulk

Apply one action to every stored email from a sender ("learn once, clean up
everything").

**Request body:**
```json
{ "senderEmail": "news@acme.com", "action": "archive", "labelName": "" }
```

**Response:** `200 OK`
```json
{ "applied": 34, "total": 40, "protectedSkipped": 0 }
```

### List suggestions

#### GET /api/ai/suggestions?status=pending

List the caller's suggestions, filtered by `status` (default `pending`).

**Response:** `200 OK`, an array of `AISuggestion` enriched with the identity of
the email each one is about: `subject`, `from`, `snippet`, resolved in one
lookup. Without them the client had to find the message among those currently on
screen, so any suggestion for an email outside the loaded page asked the user to
approve an action on "Sans sujet · Expéditeur inconnu". The three fields are
omitted when the message is no longer in the stored mailbox.

### Reject a suggestion

#### POST /api/ai/suggestions/{id}/reject

Reject a pending suggestion. Marks it `rejected`; nothing changes in Gmail.

**Response:** `204 No Content`

---

## Senders Endpoints

Learn-once triage keyed by sender.

### List senders

#### GET /api/senders

Inbox senders aggregated with their email counts and any learned preference.

**Response:** `200 OK`, an array of `SenderStats`:
```json
[
  {
    "senderEmail": "news@acme.com", "senderDomain": "acme.com",
    "senderName": "Acme", "emailCount": 12,
    "preference": { "autoApply": true, "defaultAction": "archive", "defaultLabel": "" }
  }
]
```
`preference` is omitted when the sender has none.

### Create a rule from a sender

#### POST /api/senders/rule

Turn a sender into a permanent deterministic rule: every future email whose
`From` contains the sender gets the action.

**Request body:**
```json
{ "senderEmail": "news@acme.com", "action": "archive", "labelName": "" }
```
`action` is one of `archive`, `trash`, `label`, `markRead`, `star`; `labelName`
is required when `action` is `label`.

**Response:** `201 Created`, the created `SortingRule` (see the Sorting Rules
section for its shape).

### Update a sender preference

#### PUT /api/senders/{id}/preferences

**Request body:**
```json
{ "autoApply": true, "defaultAction": "archive", "defaultLabel": "" }
```

**Response:** `200 OK` → `{ "status": "updated" }`

---

## Sorting Rules Endpoints

Deterministic, **AI-free** triage. A rule pairs conditions with an action; when
the conditions match an email, the action is applied directly via Gmail: no
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

- **`matchAll`** : `true` ANDs every condition, `false` ORs them.
- **Condition `field`** : `from`, `subject`, `snippet`, `to`, `body`.
- **Condition `operator`** : text: `contains`, `notContains`, `equals`,
  `notEquals`, `startsWith`, `endsWith`, `regex` (all case-insensitive except
  `regex`); temporal: `olderThan` / `newerThan`, whose `value` is a **number of
  days** compared against the email's received date (an undated email never
  matches a temporal condition).
- **`actions`** : an **ordered list** of actions applied in sequence (e.g.
  *label* then *archive*). Each is `{ "type": ..., "labelName": ... }` where
  `type` is `archive`, `trash`, `label` (requires `labelName`), `markRead` or
  `star`. A protected (VIP) sender has destructive actions (archive/trash)
  skipped while non-destructive actions in the same rule still run.
- **`action` / `labelName`** : legacy single-action fields, kept for backward
  compatibility. A client may send either shape; the server normalizes them and
  mirrors the primary (first) action onto these fields. Rules created before
  multi-action, and one-click sender rules, use only these.
- **`priority`** : lower runs first; the first matching rule wins per email.

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
  "byRule": { "Archiver les newsletters Acme": 12, "Promos": 6 },
  "protectedSkipped": 0
}
```

### Preview Sorting Rules (dry run)

#### POST /api/rules/preview

Reports what the rules **would** do over the current inbox without touching Gmail
This is the safe way to check a ruleset before applying it.

**Response:** `200 OK`
```json
{
  "scanned": 120,
  "willApply": 18,
  "byRule": [
    { "ruleName": "Archiver les newsletters Acme", "action": "archive", "matched": 12 }
  ],
  "samples": [
    {
      "messageId": "18c...", "from": "news@acme.com", "subject": "…",
      "ruleName": "Archiver les newsletters Acme", "action": "archive"
    }
  ]
}
```
`samples` is capped server-side. When the user has no rules, `willApply` is `0`
and the arrays are empty.

### Reorder the ruleset

#### PUT /api/rules/reorder

Set the running order of the whole ruleset in one request. Order is the engine's
semantics rather than a display preference: `FirstMatch` stops at the first rule
that matches, so which rule wins IS its priority.

**Request body:**
```json
{ "ids": ["665...a1", "665...a2", "665...a3"] }
```
The list is the desired order, first evaluated first. Ids the account does not
own are ignored, and rules the client did not mention keep their relative order
behind the ones it did, so a stale list can never drop a rule out of the ruleset
or collapse two rules onto the same priority.

**Response:** `200 OK`
```json
{ "status": "reordered", "reordered": 3 }
```

**Error Responses:**
- `400 Bad Request`: Empty list, or no id matching a rule the caller owns
- `401 Unauthorized`: Missing or expired session

### Duplicate a rule

#### POST /api/rules/{id}/duplicate

Copy a rule the caller owns. The copy is built from the rule's intent through the
portable form, so it carries no id, no applied counter and no shared history. It
is named `<name> (copie)` (then `(copie 2)`, since the per-rule apply counter is
keyed by name) and arrives **disabled**: until it is edited it would be a second
rule acting on the same mail as the one it was cloned from.

**Response:** `201 Created`, the new rule.

**Error Responses:**
- `400 Bad Request`: Invalid rule id
- `404 Not Found`: Rule not found

### Export the ruleset

#### GET /api/rules/export

Hand back every rule as a portable document. It carries intent only, never
account state: no ids, no owner, no applied counters. That is what makes
importing it into another account a supported operation.

**Response:** `200 OK` with a `Content-Disposition: attachment` header.
```json
{
  "version": 1,
  "exportedAt": "2026-08-13T09:00:00Z",
  "rules": [
    {
      "name": "Newsletters",
      "enabled": true,
      "matchAll": true,
      "conditions": [{ "field": "from", "operator": "contains", "value": "news@" }],
      "actions": [{ "type": "label", "labelName": "Veille" }, { "type": "archive" }],
      "priority": 0
    }
  ]
}
```

### Import a ruleset

#### POST /api/rules/import

Create rules from a previously exported document. The body is the export itself.

It **appends** rather than replaces: an import that silently wiped the existing
ruleset would be an irreversible action behind a file picker. Imported rules land
after the ones already in place, so an import never quietly outranks rules the
user built by hand.

Validation is all-or-nothing and happens before anything is written: a file with
one bad rule creates none of them, because a half-applied import leaves a ruleset
that is neither the old one nor the file's. Errors name the offending entry.

**Response:** `201 Created`
```json
{ "status": "imported", "imported": 4 }
```

**Error Responses:**
- `400 Bad Request`: Not an export, a newer format version, an empty file, an
  invalid rule (named in the message), or a ruleset that would exceed 200 rules
- `401 Unauthorized`: Missing or expired session

---

## Saved Searches Endpoints

The inbox speaks Gmail's query language, which is what makes it powerful and what
made it single-use: nobody retypes `in:inbox from:linkedin.com older_than:7d`
every morning. A saved search turns a query worked out once into a chip.

### List saved searches

#### GET /api/searches

Returns `{ "searches": [ ... ] }`, most used first then most recent, so the bar
orders itself by what has earned its place. At most 24 per account.

### Save a search

#### POST /api/searches

**Request body:**
```json
{ "name": "Recrutement", "query": "in:inbox from:linkedin.com" }
```
The name is trimmed and collapsed (60 characters max); the query is trimmed
(512 characters max) and must be a single line.

Identity is the normalized query, not the name: saving a query the account
already keeps **renames that chip** rather than growing a second, identical one.

**Response:** `201 Created`, the stored search.

**Error Responses:**
- `400 Bad Request`: Missing name or query, over the length limits, a multiline
  query, or the account is already at 24 searches
- `401 Unauthorized`: Missing or expired session

### Record a use

#### POST /api/searches/{id}/use

Increments the use counter that orders the bar. Fire-and-forget from the client's
point of view: a failed increment must never cost the user their search.

**Response:** `{ "status": "ok" }`. `404` when the search does not exist.

### Delete a saved search

#### DELETE /api/searches/{id}

**Response:** `{ "status": "deleted" }`. `404` when the search does not exist.

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

## Config Endpoints

### Instance status

#### GET /api/config/status

Public boot probe. The SPA calls it before any login to decide whether to show
the setup instructions, whether Pro can be bought yet, and which edition is
running. It is the only public route under `/api/config/`, and it returns
nothing beyond these three fields.

**Response:** `200 OK`
```json
{ "isConfigured": true, "billingOn": false, "edition": "self-hosted" }
```

`isConfigured` reflects the live OAuth client, whichever source its credentials
came from at boot. The credentials themselves are read from `GMAIL_CLIENT_ID`,
`GMAIL_CLIENT_SECRET` and `GMAIL_REDIRECT_URL` and have **no HTTP surface**:
they can be neither read nor written over the API, and both former
`/api/config/gmail` routes return `404`.

`edition` is `self-hosted` or `hosted`, from the `EDITION` environment variable.
It decides which mailbox providers exist (see `GET /api/providers`) and whether
there is anything to bill at all.

---

#### GET /api/providers

Public. The mailbox catalog for the running edition: which providers this
instance can reach, over which transport, with which credential, what each route
can do, and what can stop it from working for a given user.

Public because the connect screen runs before any account exists, and it carries
no secret: provider names, hostnames, ports and help text.

The SPA hardcodes no provider. It renders this payload, which is the same table
`internal/api` connects with, so a provider offered on screen but unreachable by
the backend is a state that cannot occur.

**Response:** `200 OK`
```json
{
  "edition": "self-hosted",
  "providers": [
    {
      "key": "gmail",
      "name": "Gmail",
      "domains": ["gmail.com", "googlemail.com"],
      "routes": [
        {
          "transport": "gmail-api",
          "auth": "oauth",
          "autodiscover": false,
          "capabilities": {
            "labels": true, "providerSearch": true, "stableIds": true,
            "threads": true, "send": true, "push": true
          },
          "blockers": ["own-cloud-project"],
          "note": "Pleine fidelite. L'utilisateur cree son projet Google Cloud..."
        },
        {
          "transport": "imap",
          "auth": "app-password",
          "autodiscover": false,
          "imap": { "host": "imap.gmail.com", "port": 993, "tls": "implicit" },
          "smtp": { "host": "smtp.gmail.com", "port": 587, "tls": "starttls" },
          "capabilities": {
            "labels": true, "providerSearch": true, "stableIds": true,
            "threads": true, "send": true, "push": false
          },
          "blockers": ["two-factor-required", "advanced-protection", "datacenter-ip"],
          "note": "Aucune surface de conformite..."
        }
      ]
    }
  ]
}
```

Routes are ordered best first: a revocable token before a pasted full-mailbox
credential. `autodiscover` means the endpoints are resolved from the domain when
the user connects rather than carried here, so the screen must not promise
settings it does not have.

`blockers` are why this route can fail for this particular user, and the screen
shows them BEFORE the attempt: every one of them otherwise surfaces as an
authentication error nobody can act on. Current values: `two-factor-required`,
`advanced-protection`, `admin-policy`, `admin-consent`, `paid-plan-required`,
`local-only`, `own-cloud-project`, `datacenter-ip`.

What the catalog offers depends on the edition. `hosted` never carries a
`gmail-api` route (a shared OAuth client is capped by Google at 100
authorizations for the life of the Cloud project) and never carries Proton
(Bridge binds to `127.0.0.1`). Both exist in `self-hosted`.

---

## Error Responses

Every endpoint answers failures with the same JSON envelope (see **Errors**
above): `{ "error": "...", "status": <code> }`, `Content-Type: application/json`.

| Status | Means | Typical body |
|---|---|---|
| `400` | The request is malformed or rejected on its merits | `{ "error": "Invalid request body", "status": 400 }` |
| `401` | No session, an expired one, or a dead Google grant | `{ "error": "Authentication required", "status": 401 }` |
| `402` | Free monthly AI quota exhausted | `{ "error": "Quota mensuel atteint. Passez à Pro pour continuer.", "status": 402 }` |
| `404` | No such resource for this caller | `{ "error": "Rule not found", "status": 404 }` |
| `409` | Already done (an action undone twice) | `{ "error": "Action déjà annulée", "status": 409 }` |
| `413` | Body over 1 MiB, or an attachment over 30 MiB | `{ "error": "Request body too large", "status": 413 }` |
| `429` | Rate limited (20 req/s sustained, burst 40, per client) | `{ "error": "Too many requests", "status": 429 }` |
| `500` | Mailsorter failed (usually the datastore) | `{ "error": "Failed to load rules", "status": 500 }` |
| `502` | Gmail, Mistral or Stripe failed | `{ "error": "Report impossible : ...", "status": 502 }` |
| `503` | A dependency is not configured (AI, billing) | `{ "error": "AI service not configured", "status": 503 }` |

A `401` on a route the SPA considers non-optional clears the session and returns
the user to the login screen.

## CORS

The API is configured to accept requests from:
- `http://localhost:3000`
- `http://localhost`

Allowed methods: GET, POST, PUT, DELETE, OPTIONS

Allowed headers: Content-Type, Authorization, X-User-Email
