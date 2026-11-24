# API Documentation

Base URL: `http://localhost:8080`

## Authentication

Authenticated endpoints require a **session token** in the `Authorization` header.
The token is issued by `POST /api/auth/register` or `POST /api/auth/login`, by
`GET /api/auth/callback` after a Google consent, or by `POST /api/auth/mailbox`.
It is an HMAC-signed value that identifies the user and expires after 30 days.

```
Authorization: Bearer <session-token>
```

The way into the app is an account: the SPA creates one with
`POST /api/auth/register` (or through Google when the instance has Google
credentials), signs in with `POST /api/auth/login`, then connects a mailbox
(see "Mailbox Endpoints"). `POST /api/auth/mailbox` is an alternative the API
keeps and the SPA does not call.

The server derives the user identity from this token; clients must **not** send a
raw `X-User-Email` header (any client-supplied value is stripped server-side).
Requests without a valid token receive `401 Unauthorized`.

Public endpoints (no token needed): `/health`, `/metrics`, `/api/auth/*`,
`/api/config/status`, `/api/providers`, `/api/waitlist`, and
`/api/billing/webhook` (which authenticates via its Stripe signature). On a
public route a valid token still identifies the caller; an invalid one is not
an error.

## Errors

Every failure, from any layer (middleware, routing, handler), is a JSON envelope.
That includes the routing layer itself: an unknown path answers
`404 { "error": "No such route" }` and a known path with the wrong method
`405 { "error": "Method not allowed on this route" }`.

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

### Register Account

#### POST /api/auth/register

Create a new independent Mailsorter account with email and password.

**Request Body:**
```json
{
  "email": "vous@exemple.com",
  "password": "motdepasse123"
}
```

**Response:** `201 Created`
```json
{
  "accessToken": "<session-token>",
  "userEmail": "vous@exemple.com"
}
```

The address is trimmed and lowercased; it is the account's identity
(`userId`) everywhere else. The password is stored as a bcrypt hash. No mailbox
is connected yet: until one is, mailbox routes answer `428`.

**Throttling.** Register, login and `POST /api/auth/mailbox` share one limiter,
far below the global one: roughly **one attempt every 3 seconds, burst 5**,
keyed on the visitor's address and on the email address (each route counts
the email under its own key). Over it: `429` with `Retry-After: 3`.

**Error Responses:**
- `400 Bad Request`: Invalid email format, or a password shorter than 8
  characters or longer than 72 bytes (bcrypt's limit; an accented letter
  counts twice)
- `409 Conflict`: An account with this email already exists
- `429 Too Many Requests`: throttled, see above
- `500 Internal Server Error`: the datastore could not be read or written

---

### Login Account

#### POST /api/auth/login

Log in to an existing Mailsorter account with email and password.

**Request Body:**
```json
{
  "email": "vous@exemple.com",
  "password": "motdepasse123"
}
```

**Response:** `200 OK`
```json
{
  "accessToken": "<session-token>",
  "userEmail": "vous@exemple.com"
}
```

**Error Responses:**
- `400 Bad Request`: Missing credentials, a password longer than 72 bytes, or
  an account that has no password (created through Google or through
  `POST /api/auth/mailbox`; the message points to the Google button in both
  cases)
- `401 Unauthorized`: Unknown address or wrong password (same answer for both)
- `429 Too Many Requests`: throttled, shared with register (see above)
- `500 Internal Server Error`: the datastore could not be read

---

### Get Authorization URL

#### GET /api/auth/url

Get the Google OAuth authorization URL. The URL embeds a signed `state`
parameter, valid 10 minutes, used for CSRF protection on the callback. When the
caller already has a session, the state also carries their address, so the
callback attaches the Google mailbox to that account rather than to the
Google address.

**Query Parameters:**
- `reconnect` (optional): `1` forces Google's consent screen, which is what
  hands back a refresh token. The settings' "Reconnecter Gmail" uses it to
  repair a dead grant; a first login does not need it.

**Response:**
```json
{
  "authUrl": "https://accounts.google.com/o/oauth2/v2/auth?...&state=..."
}
```

### Handle OAuth Callback

#### GET /api/auth/callback

Validate the OAuth `state`, exchange the authorization code, and return a signed
**session token** (used as `Authorization: Bearer ...` on subsequent requests).

The Google tokens are sealed (AES-256-GCM) and stored on the account: the one
named in the state when the caller was signed in, otherwise the account of
the Google address, created if needed. A refresh token is never overwritten by
the empty one Google returns on later consents. The account's mailbox
connection is set to the Gmail API (`transport: "gmail"`, see
`GET /api/mailbox`).

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
- `400 Bad Request`: Missing code parameter, or a missing, forged or expired state
- `500 Internal Server Error`: Failed to exchange code, get the Google profile,
  seal the tokens or save the account

---

### Sign in with a mailbox

#### POST /api/auth/mailbox

Sign in with a mailbox: if the provider accepts the address and its
credential, the caller is who they say they are. The first successful request
creates the account (the address is its identity), every later one signs into
it, and each one stores the credential it was given. An alternative way in kept
in the API: the SPA uses register and login instead (see "Authentication").

Public. The body carries an address and a password and **nothing else**: the
provider, host, port and TLS mode are resolved from `internal/provider`, so a
caller cannot name the host the server connects to. The password is whatever
the provider's IMAP route asks for: an app password for most, the mailbox
password itself for the routes the catalog marks `"auth": "password"`.

**Request Body:**
```json
{ "address": "vous@orange.fr", "password": "<mot de passe d'application>" }
```

**Response:** `200 OK`
```json
{ "accessToken": "<session-token>", "userEmail": "vous@orange.fr" }
```

On success the mailbox is stored (same row `POST /api/mailbox/connect` writes,
app password sealed with AES-256-GCM) and the user document is upserted. A
sign-in with a rotated app password stores the new one, which is why losing an
app password is a trip to the provider rather than a support ticket.

**Throttling.** This is the only public route that makes the server try a
password against somebody else's mail provider. It uses the sign-in limiter it
shares with register and login: roughly **one attempt every 3 seconds, burst
5**, keyed on the visitor's address **and** on the email address, so neither
rotating addresses from one machine nor hammering one address from many gets a
free pass. Over it: `429` with `Retry-After: 3`.

**Error Responses:**
- `400 Bad Request`: address or password missing, or a password over 1024 bytes
- `401 Unauthorized`: the provider refused the credentials. An unknown address
  and a wrong password answer identically: the provider is what refused, and it
  does not say which. Distinguishing them would make this route an address
  oracle
- `409 Conflict`: this address already holds Google credentials. A stored
  mailbox row takes precedence over a Google token (see "Transports and what
  works on each"), so accepting here would move the account to IMAP for whoever
  holds the app password. Switching is still possible from inside the app, with
  `POST /api/mailbox/connect`, where control of the account was already proved.
  The check fails closed: a datastore error is a `500`, never "no Google"
- `422 Unprocessable Entity`: this address has no IMAP route with a known
  server in this edition (see `POST /api/mailbox/connect`)
- `429 Too Many Requests`: throttled, see above
- `502 Bad Gateway`: the provider could not be reached

---

## Email Endpoints

### Get Emails

#### GET /api/emails

Get a page of the mailbox, through the caller's transport.

**Headers:**
- `Authorization: Bearer <session-token>` (required)

**Query Parameters:**
- `q` (optional): Gmail search query (default: "in:inbox"). Over IMAP, only
  part of the syntax is answered, see below
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
set of labels; `folder` carries that folder instead, and `messageId` is the
message's UID in it. Three fields come with it there: `isStarred` (`true` when
the server holds `\Flagged`, absent otherwise), `sizeEstimate` (the whole
message in bytes, `RFC822.SIZE`) and `hasAttachments` (`true` when it carries a
file, absent otherwise), the last two read by the sync. The Gmail API's
answer carries none of them: the star is the `STARRED` label there. See
"Transports and what works on each".

**Error Responses:**
- `401 Unauthorized`: Missing or expired session, or a dead Google grant
- `428 Precondition Required`: no mailbox is connected to this account yet
- `500 Internal Server Error`: Failed to fetch emails
- `501 Not Implemented`: a query term a stored mailbox cannot answer (IMAP only)

### Sync Emails

#### POST /api/emails/sync

Mirror the 100 most recent messages of the inbox into the database, through
the caller's transport. Over IMAP it also drops from the mirror the messages
that have left the inbox since the last sync, however old: it asks the server
for the UID of every message in the inbox (one `UID SEARCH`), not only the
ones it listed. The background sync (`autoSyncEnabled`) runs the same code.
Over IMAP, `500` when the mirror could not be brought in line with the inbox
(the listing itself is stored).

Over the Gmail API, when `autoApplyRules` is on, the deterministic rules run on
each synced message (protected senders shielded, each action journaled with
source `rule`), and `rulesApplied` counts the messages a rule acted on. Over
IMAP rules do not run and `rulesApplied` is `0`; the listing reads the messages
with PEEK, so syncing never marks them read.

**Headers:**
- `Authorization: Bearer <session-token>` (required)

**Response:**
```json
{
  "synced": 42,
  "total": 50,
  "rulesApplied": 3
}
```

**Error Responses:**
- `401 Unauthorized`: Missing or expired session, or a dead Google grant
- `500 Internal Server Error`: Failed to sync emails. This includes an account
  with no mailbox connected, which this route does not map to `428`

### Direct action on a message

#### POST /api/emails/action

Apply a single, direct action to one message, through the caller's transport:
no AI, no rule. Used by the inbox for one-off triage. Works the same over the
Gmail API and over IMAP, where archive and delete are moves to the server's
archive and trash folders and read and star are flags.

**Headers:**
- `Authorization: Bearer <session-token>` (required)

**Request body:**
```json
{ "messageId": "18c...", "action": "archive" }
```
`action` is one of `archive`, `delete` (alias `trash`), `unarchive`, `untrash`,
`read`, `unread`, `star`, `unstar`. Forward triage actions
(`archive`/`delete`/`read`/`star`) are recorded in the action ledger, except,
over IMAP, a move of a message that already left the inbox: toward where
Mailsorter put it (a second click from a screen left open), while another
request is moving it (two key presses at once), or after another mail client
moved it. It answers `200` with `"status": "already"` and records nothing, so the
screen does not announce a move that did not happen. `read`, `unread`, `star`
and `unstar` over IMAP answer `404` "Ce mail n'est plus dans votre boîte de
réception." when the message has left the inbox (the server would accept the
change on a number that names nothing). A move elsewhere (`delete` on
a mail Mailsorter archived) moves it from where it is, and "Annuler" still
finds it. `read`, `unread`, `star` and `unstar` over IMAP also change the
stored copy, so the list and its filters (`is:unread`, `is:starred`) show them
at once. `unarchive`
and `untrash` are what "Annuler" sends right after an action, and over IMAP
`unlabel` after an AI proposal filed the mail in a folder: they mark the most
recent matching forward entry (`archive`, `delete`, `label`) `undone` and record
the reversal (source `undo`), as `POST /api/activity/undo` does. This route
carries no folder or label name, so `label` answers `400` "Cette action demande
le nom d'un dossier ou d'une étiquette, que cette route ne reçoit pas.", and so
does `unlabel` over the Gmail API.

Over IMAP, `unarchive` / `untrash` / `unlabel` take the number the message had in the inbox
(the one the client holds) and fetch it back from where Mailsorter moved it;
the list shows it again at once, under the inbox's new number. A message another
mail client moved or deleted since answers `404` "Ce mail n'est plus là où
Mailsorter l'avait rangé.". The vocabulary matches `/api/emails/batch-action`, so the reader
and the shortcuts can express the same things one message at a time.

**Response:** `200 OK`
```json
{ "status": "ok", "action": "archive" }
```

**Error Responses:**
- `400 Bad Request`: Missing `messageId`, unsupported `action`, or `label` (and over the Gmail API `unlabel`), which need a name this route does not carry
- `401 Unauthorized`: Missing or expired session, or a dead Google grant
- `428 Precondition Required`: no mailbox is connected to this account yet
- `500 Internal Server Error`: the mailbox refused the action

---

### Get one message (with its body)

#### GET /api/emails/{id}

Return a single message, decoded. `GET /api/emails` deliberately omits bodies
(a page carries up to 500 messages), so the reader fetches the one it is about
to display.

**Query parameters:**
- `markRead` (optional): `1` also marks the message read in the mailbox (the
  `UNREAD` label comes off on Gmail, the `\Seen` flag is set over IMAP) and
  updates the local cache. Opening an email is user intent rather than
  automation, so this is **not** written to the action ledger.

**Response:** `200 OK`
```json
{
  "messageId": "18c...",
  "from": "Acme <news@acme.com>",
  "subject": "Votre facture",
  "snippet": "Bonjour...",
  "body": "version texte",
  "bodyHtml": "<p>version html</p>",
  "attachments": [
    { "filename": "facture.pdf", "mimeType": "application/pdf", "size": 20481, "attachmentId": "1" }
  ],
  "inlineImages": { "logo@acme.com": "0.2" },
  "labelIds": ["INBOX"],
  "isRead": true
}
```
`body` and `bodyHtml` are both returned when the sender provided both: the reader
renders the HTML, the deterministic rule engine matches on the text. Either may
be absent.

`attachments` follows one rule on both transports (`internal/attachment`,
`Listed`): a leaf part with a file name (`filename` of `Content-Disposition`,
else `name` of `Content-Type`) is a file; so is an unnamed part declared
`Content-Disposition: attachment`, listed as `piece-jointe`, and an unnamed
forwarded mail (`message/rfc822`), listed as `message.eml`. The one exception
is an image without a name that an `<img>` of the HTML in `bodyHtml` draws
through a `cid:` URL in its `src`, whatever its disposition: the reader draws
it in the text (see `inlineImages`), and a newsletter's pictures do not become
chips named after nothing. A named one is listed too, so it can be saved. A
`cid:` anywhere else (a CSS `url()`, a `background` attribute) is not drawn
by the screen and counts for nothing: an unnamed image only a background
points at is listed as `piece-jointe` when it is declared an attachment, and
is not shown at all otherwise, as before this rule. A `Content-ID`, an
`X-Attachment-Id` or an inline disposition alone hides nothing. `filename` is cleaned before it is sent: no directory,
no control character, no quote, no bidirectional control (U+061C, U+200E,
U+200F, U+202A to U+202E, U+2066 to U+2069), the same name the download's
`Content-Disposition` carries.

Over the Gmail API this rule replaced the one it had, which hid
every part with a `Content-ID`, an `X-Attachment-Id` or an inline
disposition: the files attached from Gmail's web interface (each one carries a
`Content-ID` and an `X-Attachment-Id`), a PDF Apple Mail sends inline and an
image no HTML shows were missing from the list, and are now in it.

`inlineImages` maps the `cid:` of each picture the HTML draws to the id the
download route serves it under, on both transports (`attachment.InlineImage`):
a part of a type a browser draws without running anything (PNG, JPEG, GIF,
WebP, BMP; never SVG, a document that can carry script) whose `Content-ID` the
`src` of an `<img>` of the HTML names with a `cid:` URL. At most 30 of them
(`attachment.MaxDrawn`), the first ones in the order of the HTML: each is a
download, and over IMAP a connection to the mailbox, and a thread quoting its
signatures carries dozens of logos; the others stay as the mail wrote them.
The key is the `Content-ID` as the URL names it: %-unescaped, without its
angle brackets, in lower case. No browser resolves `cid:`, so the SPA
downloads the pictures through the route below, at most 3 at a time, puts an
object URL in place of each `src="cid:..."` after the HTML is sanitized, and
when the reader leaves the message drops the rest of the queue, aborts the
downloads under way and frees the object URLs.

`attachmentId` is what the download route below takes back: the file's part
number in the message, on both transports (`"1"`, `"0.2"` over the Gmail API,
`"2"`, `"1.3"` over IMAP). Over the Gmail API it is not Gmail's own
attachment id, which nothing promises to stay the same from one
`messages.get` to the next; a message that is a single file, which Gmail
gives no part number, has `"payload"`. A Gmail part whose data travels inline
in the message payload is downloadable too: the route serves it from the
message.

On a mailbox reached over IMAP the fetch PEEKS: it reads the message without
setting `\Seen`, so opening a message stays distinct from reading it and only
`markRead=1` changes the flag. The star comes as `isStarred`, read off the
server. `labelIds` comes back empty there. It reads the
server's description of the message first (`BODYSTRUCTURE`), then, in one
command, the MIME header of each part, the text and the HTML (each fetched up
to about 6.9 MiB as stored and kept up to 5 MiB decoded) and the decoded size
of each part (`BINARY.SIZE`, on a server with BINARY or IMAP4rev2). The bytes
of a file are never read to open a mail. The request's 20 s deadline ends the
IMAP command too (the connection is closed). `attachments` lists the parts by
the rule above, file names decoded from RFC 2047 or RFC 2231 (in any charset
the mail may use, ISO-8859-1 or windows-1252 included). `attachmentId` is the
part's IMAP number (`"2"`, `"1.3"`), `mimeType` the declared type without its
parameters, and `size` the size of the decoded file, as on Gmail, not of its
base64: the server's measure, or, on a server without BINARY (Gmail's IMAP
among them), an estimate from the stored size (a few bytes off for base64,
the stored size for quoted-printable). A file in a transfer encoding nobody
knows is left out of the list and logged; one whose base64 is broken cannot be
told without reading it, so it is listed and its download answers 502.

**Error Responses:**
- `400 Bad Request`: Missing id
- `401 Unauthorized`: Missing or expired session, or a dead Google grant
- `404 Not Found`: the message is no longer in the mailbox, moved or deleted
  from another mail client since the list showed it (an IMAP UID its folder no
  longer holds, a Gmail id that answers 404): `{"error": "Ce mail n'est plus
  dans votre boîte : il a été déplacé ou supprimé entre-temps."}`
- `428 Precondition Required`: no mailbox is connected to this account yet
- `502 Bad Gateway`: the mailbox could not return the message: `{"error": "Ce
  mail n'a pas pu être chargé depuis votre boîte. Réessayez dans un
  instant."}`. The error itself is logged, never sent

---

### Download an attachment

#### GET /api/emails/{id}/attachments/{attachmentId}

Stream one attachment of one message. Gmail never ships attachment data with the
message, so this reads the message again first (to learn the part's real
filename and MIME type, to prove the message actually carries a file under that
part number, and to get the attachment id Gmail gives it in this fresh copy)
and then fetches the bytes under that fresh id. An id that is no part number,
a Gmail attachment id sent by a screen opened before part numbers were handed
out, is served only while the fresh copy still gives a file that id.

Only a part of the named message may be served: an id the message does not
carry is a 404, never a passthrough to Gmail. The parts served are the files
the reader lists and the pictures its HTML draws (`inlineImages`, the 30 first
ones), on both transports: a picture of another type, one no `<img>` of the
HTML points at, or one past the 30th, is not served unless it is a listed
file.

Over IMAP the file is inside the message. The route reads the message the
way the reader does (its description, the headers of its parts, its HTML and
the parts' decoded sizes, never a file) and serves the part only when the
reader's list has a file under `attachmentId`, or `inlineImages` a picture. A file over 30 MiB once
decoded is a 413 before a byte of it is read: on the server's measure
(`BINARY.SIZE`), or, without one, when its stored size cannot fit. Then it
fetches that part alone with `BODY.PEEK[part]` (saving a file does not mark the
mail read), reading it as it arrives rather than into memory and through the
30 MiB cap, so no more than that of it is ever held. The request's 60 s
deadline ends the IMAP command too (the connection is closed). Any other id,
the text of the mail (`"1"`), a section name (`"TEXT"`), a part past the last
one or a file of another mail, is a 404 `{"error": "Cette pièce jointe est
introuvable dans ce mail."}` and nothing else. The bytes are the file as
sent: the transfer encoding (base64, quoted-printable) is undone, the charset
of a text file is left as it is.

**Response:** `200 OK`, the raw file, with:
- `Content-Type`: the part's MIME type when it parses, `application/octet-stream` otherwise
- `Content-Disposition`: `attachment`, with the filename sanitized (no directory
  components, no control or bidirectional control characters, the name the
  reader lists) and published in both the quoted and the RFC 5987 `filename*`
  form so accents survive
- `X-Content-Type-Options: nosniff`

The route is session-authenticated, so a plain `<a href>` cannot fetch it: the
SPA downloads through the API client and hands the browser an object URL.

**Error Responses:**
- `400 Bad Request`: Missing message or attachment id
- `401 Unauthorized`: Missing or expired session
- `404 Not Found`: This message carries no such attachment ("Cette pièce
  jointe est introuvable dans ce mail.", on both transports), or the message
  is no longer in the mailbox, moved or
  deleted from another mail client since the list showed it ("Ce mail n'est
  plus dans votre boîte : la pièce jointe ne peut plus être téléchargée.", on
  both transports)
- `413 Payload Too Large`: Over 30 MiB
- `428 Precondition Required`: no mailbox is connected to this account yet
- `502 Bad Gateway`: the mailbox could not return the message or the bytes,
  or, over IMAP, the file's transfer encoding does not decode: "Cette pièce
  jointe n'a pas pu être téléchargée depuis votre boîte. Réessayez dans un
  instant.". The error itself is logged, never sent

---

### Act on a whole selection

#### POST /api/emails/batch-action

Gmail API only: `501` over IMAP.

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

Works on the Gmail API and over IMAP.

Reverse a batch the client just applied. Separate from the per-entry history undo
because the client already holds the exact ids, which is what makes undoing a
100-email action one request instead of a hundred.

**Request body:**
```json
{ "messageIds": ["18c..."], "action": "label", "labelName": "Factures" }
```
`archive`, `delete` and `label` are reversible. `labelName` is required when
undoing a `label` and is the name the client just applied: the server resolves it
to a Gmail label id, which the client does not have. Over IMAP a label is the
folder of that name, and undoing it brings the mail back to the inbox from the
folder it was filed in. Marking as read stays one-way on purpose.

The matching ledger entries are marked undone so the history does not offer a
second "Annuler" on the same work. Note that on the Gmail API the per-entry
history undo (`POST /api/activity/undo`) still cannot reverse a labelling: most
action log lines name no label, so it would have nothing to remove. Over IMAP
it can (see there).

**Response:** `200 OK`
```json
{ "restored": 1, "total": 1 }
```

Over IMAP each message is fetched back from where Mailsorter moved it, by the
number it had in the inbox. When another mail client moved some of them
meanwhile, the others still come back, and the answer says why those did not:

```json
{ "restored": 2, "total": 3, "notWhereLeft": 1, "message": "Ce mail n'est plus là où Mailsorter l'avait rangé." }
```

**Error Responses:**
- `400 Bad Request`: Non-reversible action, empty list, missing label name, or over 200 ids
- `401 Unauthorized`: Missing or expired session

---

## Snooze Endpoints ("Reporter")

Pull a message out of the inbox until a chosen time, then have it return on its
own (marked unread). Wake time is resolved from a friendly preset server-side.
The snoozed message is archived and labelled `Mailsorter/Reporté`, so snoozing
and waking are Gmail API only: `501` over IMAP. Listing snoozes reads the
datastore and works on both.

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

Returns `{ "snoozes": [ ... ] }`, soonest wake first (`status` defaults to `scheduled`).

### Wake a snooze now

#### POST /api/snoozes/{id}/wake

Brings the email back to the inbox immediately, marked unread.

---

## Protected Senders Endpoints (VIP)

A per-user safety net: while a sender (full address or whole domain, subdomains
included) is protected, no automated pass (AI suggestion, deterministic rule,
sender auto-pilot or bulk action) may archive, trash or delete their mail, nor,
over IMAP where a label is a folder, file it into one. Actions that leave the
mail in the inbox (a Gmail label, star, mark read) are unaffected.

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

Aggregates the senders in the stored mailbox that advertise an unsubscribe link,
ranked by volume (at most 100). It reads the datastore only, so it answers on
both transports.

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

Gmail API only: `501` over IMAP.

Unsubscribes from the sender of a given message. When the sender supports RFC
8058 one-click, the POST is performed server-side (`done: true`); otherwise the
`url` / `mailto` is returned for the client to open. Optionally archives the
sender's backlog in the same call: every stored mail from the same address,
whatever its display name, and only that address (a longer address that contains
it is another sender). A protected sender is still recorded as unsubscribed, but
its mail stays in the inbox and is not counted in `archived`.

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
- `401 Unauthorized`: Missing or expired session
- `404 Not Found`: Email not found
- `422 Unprocessable Entity`: Sender exposes no unsubscribe link

---

## Transports and what works on each

A user's mailbox is reached either through the Gmail API (the Google OAuth path)
or over IMAP (a mailbox connected through the endpoints below). The transport is
resolved per request, never guessed:

- a `mail_accounts` row decides: `transport: "imap"` is IMAP, `"gmail"` is the
  Gmail API;
- without a row, Google tokens on the account mean the Gmail API;
- with neither, the route answers **`428 Precondition Required`** ("no mailbox
  connected"), except `POST /api/emails/sync`, which answers `500`;
- a datastore failure fails the request rather than resolving to Gmail.

Part of the API was written when every user was a Gmail user and has not been
ported. Those endpoints answer **`501 Not Implemented`** for a caller whose
mailbox is on IMAP, rather than acting on the wrong message.

| Works on IMAP | Answers 501 on IMAP |
|---|---|
| `POST /api/emails/sync` (no rules applied) | `POST /api/rules/apply`, `POST /api/rules/preview` |
| `GET /api/emails` (served from the stored mailbox, see below) | the `label:`, `filename:`, `category:` terms, `has:` other than `attachment`, `is:` other than `read` / `unread` / `starred`, `in:` other than `inbox`, and a malformed `newer_than:` / `older_than:` / `larger:` / `smaller:` |
| `GET /api/emails/{id}` (body and attachment list included), `GET /api/emails/{id}/attachments/{attachmentId}` | |
| `POST /api/emails/action` (archive, delete, unarchive, untrash, read, unread, star, unstar), `POST /api/emails/batch-undo`, `POST /api/activity/undo` (a moved message, archived, deleted or filed in a folder, is fetched back from where Mailsorter put it, see below) | `POST /api/emails/batch-action` |
| `GET /api/stats` (inbox total and unread, counted locally) | the sent, draft, spam, trash and per-label counters stay at zero |
| `POST /api/ai/analyze`, `POST /api/ai/analyze-async` (no sender auto-pilot) | `POST /api/ai/apply-bulk` |
| `POST /api/ai/apply`, `POST /api/ai/apply-batch` (archive, delete, keep, and label, which files the mail in the folder of that name) | |
| Suggestions, senders, saved searches, protected senders, rules CRUD, history, activity and digest preview (datastore only) | `POST /api/emails/snooze`, `POST /api/emails/batch-snooze`, `POST /api/snoozes/{id}/wake` |
| `GET /api/subscriptions` (from the stored mailbox) | `POST /api/unsubscribe` |
| `POST /api/account/digest/test` and the daily digest loop (through the mailbox's sending server, see there) | `POST /api/smart-labels` |
| `GET /api/labels` (the user's own folders) | |

A rule can be created on an IMAP account, but nothing runs it there.

### The listing is served from the stored mailbox on IMAP

Over the Gmail API a listing is a call to Google: the query goes out as written.
Over IMAP there is nobody to hand it to. `IMAP SEARCH` is a round trip on a
stateful socket with a folder selected, and a listing runs on every page load,
every filter chip and every keystroke, so `GET /api/emails` answers from the
mirror that `POST /api/emails/sync` writes.

Two consequences worth knowing:

- A message that arrived since the last sync is not in the answer. The SPA syncs
  before it lists, and the background sync, for an account that turned it on,
  runs at most every 30 minutes, so the window is the one the inbox already had.
- The mirror holds what the syncs stored: the 100 most recent inbox messages at
  each sync, plus older ones earlier syncs kept. A message Mailsorter archives
  or deletes leaves the listing at once; it stays stored, hidden from every
  inbox read, for 30 days, which is how long "Annuler" can bring it back. Each
  sync drops the messages that are no longer in the inbox for another reason,
  whoever moved them and however old they are. The pending AI proposals about
  a message that left, either way, become `moot`: they leave the plan, and
  applying one from a screen loaded before does nothing (`ApplySuggestion`
  answers `{"status":"already"}`, `apply-batch` neither counts nor fails it).
- Each sync also reads the flags and the size of every message of the inbox,
  in one `FETCH` of `FLAGS` and `RFC822.SIZE` whatever its size, and carries the
  read state, the star and the size into every stored message: a mail read or
  starred on another mail program shows so after the next sync, however old it
  is, and `larger:` / `smaller:` know every stored message from the first sync.
  A read state or a star changed from Mailsorter after that read (`flagsAt`) is
  left as it is.
- It stores the starred messages of the inbox the copy does not hold yet, up
  to 500 a sync, most recent first: a favorite older than the 100 most recent
  is in `is:starred` from the first sync.
- And it reads the MIME structure (`BODYSTRUCTURE`, no message downloaded) of
  up to 500 stored inbox messages it has not read it for yet, newest first,
  which the next syncs complete. A message carries a file when one of its
  parts is a file by the rule the reader lists files with (see
  `GET /api/emails/{id}`), read without the HTML, which the structure does not
  carry: an image with a `Content-ID` that is not declared
  `Content-Disposition: attachment` is taken for a picture the HTML draws, and
  does not count, named or not: the paper clip a mail program shows
  (`attachment.ListedWithoutHTML`). Two kinds of image are decided otherwise
  than in the reader: a named image with a `Content-ID`, not declared an
  attachment (the reader lists it, to save it, whether the HTML draws it or
  not; the filter does not count it), and an unnamed image declared an
  attachment that the HTML draws (the reader draws it and lists no chip; the
  filter counts it). The count is stored with the version of that rule
  (`hasAttachmentsRule`, `attachment.WithoutHTMLVersion`): a message counted
  with an older one, or before the version was kept, is read again, once.
  Until it is read, a message does not match `has:attachment`. A message whose structure
  the server describes in a way that cannot be parsed, even alone (the batch
  it is in fails, the connection is opened again and the batch read by
  halves), is logged, kept without an attachment and marked
  `structureUnreadable`, and not asked again; the others are read. A starred
  message whose envelope cannot be parsed is logged and tried again at the
  next sync. Only an answer that cannot be parsed says something about a
  message: a server that refuses a read (`NO`, `[UNAVAILABLE]`,
  `[THROTTLED]`) or runs out of time concludes nothing, and the sync answers
  `500` "votre boîte n'a pas répondu jusqu'au bout de la synchronisation,
  réessayez dans un instant"; a connection that cannot be opened again,
  `500` "la connexion à votre boîte a été perdue pendant la synchronisation".
- `resultSizeEstimate` counts what is mirrored, not what the mailbox holds.

The filters the inbox offers are all answered: `is:unread`, `is:read`,
`is:starred`, `newer_than:`, `older_than:`, `has:attachment`, and `larger:` /
`smaller:`, whose size is a count of bytes, or of kilobytes (`K`, 1024 bytes)
or megabytes (`M`, 1024 x 1024 bytes): `larger:5M` is over 5,242,880 bytes.

A query term the mirror cannot answer is **refused with 501, naming the term**,
rather than dropped. Dropping `label:Factures` does not produce an error, it
produces a full inbox under a saved search named "Factures", and nothing on
screen would say the filter did nothing.

## Mailbox Endpoints

How a user connects a mailbox over IMAP, which is the hosted edition's way in:
its catalog carries no Gmail API route (a shared OAuth client is capped by
Google at 100 authorizations, see `internal/provider`), so the hosted service
asks for an address and an app password instead. A self-hosted instance offers
the same IMAP routes, Gmail's included, next to the Gmail API.

The stored password is sealed with AES-256-GCM, exactly like a Google OAuth
token, and it is never returned by any endpoint. It is also stripped from the
RGPD export: `account.SecretFields` names it, and the export redacts it on the
way out, because an export leaves the server and ends up in places the
encryption key's threat model never covered.

One connection per account: connecting again replaces the stored one.

### Read the connected mailbox

#### GET /api/mailbox

**Headers:**
- `Authorization: Bearer <session-token>` (required)

**Response:** `200 OK`. `mailbox` is `null` when nothing is connected, which is
a normal state rather than an error.
```json
{
  "mailbox": {
    "id": "665...",
    "provider": "orange",
    "transport": "imap",
    "username": "someone@orange.fr",
    "host": "imap.orange.fr",
    "port": 993,
    "tls": "implicit",
    "smtpHost": "smtp.orange.fr",
    "smtpPort": 465,
    "smtpTls": "implicit",
    "createdAt": "2026-09-17T19:00:00Z",
    "updatedAt": "2026-09-17T19:00:00Z"
  }
}
```

`smtpHost`, `smtpPort` and `smtpTls` are the mailbox's sending server, which
the daily recap leaves from: written at connect time from the same catalog
route as `host`, `port` and `tls` (`tls` and `smtpTls` take `implicit` or
`starttls`). A mailbox connected before they were recorded has none of them,
and the recap falls back on the catalog, by the row's `provider` and the
running edition (`provider.SendingServer`).

A mailbox reached through Google answers with `provider: "google"` and
`transport: "gmail"`. Its `host`, `port` and `tls` (`imap.gmail.com`, `993`,
`"tls"`) are fixed values written by the OAuth callback: the Gmail API does not
use them. An account that holds Google tokens without a stored row (one that
signed in before the row existed) gets the same shape, built on the fly,
without `id` or dates.

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

The provider is detected from the address's domain, and the route used is the
first one of that provider that the running edition offers, speaks IMAP and
names its server in the catalog (`provider.PickIMAP`). A Gmail address
therefore connects over IMAP in both editions, with no Google Cloud project.

What connects today: Gmail, Orange, La Poste, iCloud Mail, Fastmail and
Infomaniak in both editions, and Proton Mail through Bridge (`127.0.0.1:1143`)
in `self-hosted`, which needs the backend running on the Bridge machine rather
than in a container. Everything else answers `422`: the providers whose server
must be found by autodiscovery, which is not implemented yet (Free, SFR, Yahoo
and AOL, Zoho, OVH, and any domain the catalog does not know, Google Workspace
included), Outlook.com (Microsoft Graph only, not implemented), and Proton in
`hosted`.

Unlike `POST /api/auth/mailbox`, this route has no throttle of its own (only the
global limiter) and no Google check: the caller is already signed in, so
connecting an IMAP mailbox to an account that holds Google tokens is how that
account switches to IMAP.

**Response:** `200 OK`, the stored mailbox, in the shape `GET /api/mailbox`
returns under `mailbox`.

**Error Responses:**
- `400 Bad Request`: missing address or password, or a password over 1024 bytes
- `401 Unauthorized`: no session, or the provider rejected the credentials
- `422 Unprocessable Entity`: this address has no IMAP route with a known
  server in the running edition (see above)
- `500 Internal Server Error`: the connection worked but could not be stored
- `502 Bad Gateway`: the mail server could not be reached

### Disconnect

#### DELETE /api/mailbox

Forgets the connection: deletes the stored mailbox row (app password included)
and removes the Google tokens from the account. This is also how a user revokes
Mailsorter's access without going through their provider. The mailbox itself is
never touched, and the account remains: its mailbox routes answer `428` until a
mailbox is connected again.

**Response:** `200 OK`, `{ "disconnected": true }`, whether or not anything was
connected.

**Error Responses:**
- `500 Internal Server Error`: the stored row could not be deleted

---

## Stats Endpoints

The recap is computed from the append-only action ledger (`action_log`), so it
counts every journaled mutation (direct actions, applied suggestions, rules,
bulk sweeps, snoozes, unsubscribes) over the trailing 7 days, whatever the
transport.

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
  "days": [{ "date": "2026-06-15", "count": 3 }, "... 7 entries, oldest first ..."],
  "byAction": { "archive": 18, "delete": 9, "label": 12, "keep": 3 },
  "bySource": { "direct": 20, "rule": 12, "ai": 10 }
}
```

### Get Daily Digest

#### GET /api/stats/digest

Renders the same 7-day recap into a ready-to-send email digest (subject +
plain-text body + HTML body). This is the content payload also used by the daily
digest scheduler (see Account Settings). The preview reads the ledger only and
answers on both transports; delivery goes through the user's own Gmail API grant
(`gmail.send` scope), so only a mailbox reached through the Gmail API receives
it. A background loop emails opted-in users once a day at their chosen UTC hour.

The HTML body is drawn in the instance's look (`UI_THEME`): the plain recap
under `classic`, the Grand Hotel one under `hotel` (email-safe tables and
inline styles, web-safe font fallbacks). `subject` and `text` are the same in
both. This route returns exactly what the scheduler sends, theme included.

```json
{
  "subject": "Mailsorter : 3 emails triés aujourd'hui",
  "text": "Votre récap Mailsorter du 21/06/2026\n\nAujourd'hui : 3 emails triés.\n...",
  "html": "<div style=\"...\"><h2>3 emails triés aujourd'hui</h2>...</div>"
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
flagged `undoable` (it has a clean inverse and has not been undone yet). On
IMAP an archive, a delete or a label (a filing into a folder) is `undoable`
only while Mailsorter knows where it put the message (30 days, and never for a
move made before it recorded that), and a read change only while the message
is still in the inbox.

**Query parameters:**
- `source` (optional): filter on (`direct`, `rule`, `ai`, `ai-auto`, `bulk`,
  `snooze`, `unsubscribe`, `undo`)
- `limit`: defaults to `50`, capped at `200`
- `before`: an opaque cursor, return entries strictly older than the one it
  names. Pass the previous page's `nextBefore` back as is. It encodes the
  entry's `createdAt` and its id, so paging stays stable while new actions land
  at the top and does not skip entries written in the same millisecond. A
  cursor that does not parse is ignored (first page)
- `q`: free-text search over the acted-on message's subject and sender

An entry of an applied AI `label` proposal, and its reversal, also carry
`labelName`: the label it filed the mail under, over IMAP the folder.

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
      "id": "665...",
      "messageId": "18f...",
      "action": "archive",
      "source": "rule",
      "subject": "Votre relevé de compte",
      "from": "Banque <no-reply@banque.fr>",
      "undone": false,
      "createdAt": "2026-06-22T09:14:00Z",
      "undoable": true
    }
  ],
  "nextBefore": "2026-06-22T09:14:00.123Z|665..."
}
```

### Undo an action

#### POST /api/activity/undo

Gmail API and IMAP. On IMAP a moved message is fetched back from where
Mailsorter put it, and a read change is reversed only while the message is
still in the inbox (`404` "Ce mail n'est plus dans votre boîte de réception."
otherwise). Two "Annuler" of one move at once (here or through
`POST /api/emails/action`) bring the message back once: the second waits for
the first and answers like it, recording nothing more. A message another
mail client already moved, back to the inbox or elsewhere, answers `404` "Ce
mail n'est plus là où Mailsorter l'avait rangé.": Mailsorter did not bring
it back.

Reverses one recorded action by replaying its inverse Gmail mutation
(`archive` -> `unarchive`, `delete` / `trash` -> `untrash`, `read` -> `unread`,
and over IMAP `label` -> `unlabel`, which brings the mail back to the inbox
from the folder it was filed in). The entry is marked `undone` and the reversal
is itself appended to the ledger (source `undo`). Only these actions are
reversible; others return `400`.

**Request Body:** `{ "id": "665..." }`

**Responses:** `200 { "status": "undone", "action": "unarchive" }`; `400` not
reversible or an invalid id; `404` not found, or, on IMAP, "Ce mail n'est plus là
où Mailsorter l'avait rangé." when another mail client moved it meanwhile (the
entry stays not undone); `409` already undone, except that a request within
2 minutes of another one that undid it, with nothing done to the mail since,
answers `200` like it (two clicks, two tabs): once the mail was archived again,
a screen left open on the old line gets `409`; `500` "Annulation impossible à
vérifier, réessayez." when the actions since cannot be read; `502` the provider
refused the reversal. On IMAP a moved message stays recorded for 30 days after it left the
inbox, which is how long the history can bring it back; past that, `404` "Cette
action est trop ancienne pour être annulée ici : retrouvez ce mail dans
l'archive ou la corbeille de votre messagerie." and nothing moves.

---

## Account Endpoints

### Usage this month

#### GET /api/usage

How many emails the AI analyzed for the caller this month, against their cap.
Only model calls count: cache hits and sender auto-pilot do not.

**Response:** `200 OK`
```json
{ "used": 42, "limit": 200, "period": "2026-11", "plan": "free", "billingOn": false }
```

`limit` is `-1` for no cap: any account of a self-hosted instance
(`EDITION=self-hosted`), and a Pro account of the hosted service. Only free
accounts of the hosted service are capped, at 200 (`FreeMonthlyLimit`).
`period` is the current month in UTC, which is also when the counter resets.
`billingOn` says whether Pro can be bought on this instance (Stripe configured).

### Profile

#### GET /api/account/profile

The caller's account record, through the same redaction the RGPD export uses
(`account.RedactUser`): never the password hash, the OAuth tokens or the
Stripe identifiers.

**Response:** `200 OK`
```json
{
  "email": "you@example.com",
  "plan": "free",
  "autoApplyRules": false,
  "autoSyncEnabled": false,
  "digestEnabled": true,
  "digestHourUTC": 7,
  "createdAt": "2026-11-02T10:00:00Z",
  "updatedAt": "2026-11-20T08:00:00Z"
}
```

`plan` is `free` when none is stored. `digestHourUTC` is omitted when the user
never picked an hour. An account with no stored record answers with its
address and zero values.

### Get Settings

#### GET /api/account/settings

Returns the caller's tunable settings. `digestHourUTC` is always present here:
the hour the user picked, `0` meaning midnight, or the server default
(`DIGEST_HOUR_UTC`, 7 when unset) when they never picked one.

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
without clobbering the others. `digestHourUTC` takes `0` to `23`, `0` being
midnight; a value outside that range stores the server default
(`DIGEST_HOUR_UTC`). When `digestEnabled` is true, a background scheduler emails
the 7-day recap once a day at `digestHourUTC` (UTC), from the user's own
mailbox: through the Gmail API, or through the sending server of a mailbox
connected over IMAP (see `POST /api/account/digest/test`).
When `autoSyncEnabled` is true, a background scheduler syncs the inbox at most
every 30 minutes with no manual click, and applies rules when `autoApplyRules`
is on (Gmail API only). Returns the full, merged settings.

> On the Gmail API, accounts connected before the digest feature must
> **reconnect Gmail** to grant the `gmail.send` scope before delivery can succeed.

**Request Body (all optional):** `{ "autoApplyRules": bool, "autoSyncEnabled": bool, "digestEnabled": bool, "digestHourUTC": int }`

### Export account data (RGPD / data portability)

#### GET /api/account/export

Returns a single JSON document with everything Mailsorter stores about the
caller: a **redacted** account profile (the same one as
`GET /api/account/profile`), the current settings, plus every user-owned
dataset (rules, protected senders, snoozes, suggestions, sender preferences,
smart labels, unsubscribes, usage, action log, analysis jobs, saved searches,
the mailbox connection, the AI provider override, a Pro waitlist signup made
while signed in, labels, and `emails`: the local mirror of the synced messages,
decoded bodies included). Served as a downloadable attachment. The mailbox
itself, beyond what was synced, stays at the provider and is not included.

Two datasets hold a credential and are the exception: `mailAccounts` has its
`secret` (the sealed IMAP app password) and `aiSettings` its `apiKey` (the sealed
BYOK provider key) **removed**, both named in `account.SecretFields`. An export
leaves the server, so sealed is not the same as safe to hand out.

The catalog is exhaustive: every `userId`-scoped collection must appear in
`account.Datasets()`, and `TestCatalogCoversEveryUserScopedCollection` fails on any
that does not.

```json
{
  "exportedAt": "2026-06-22T10:00:00Z",
  "account": { "email": "you@example.com", "plan": "free", "autoApplyRules": false, "autoSyncEnabled": false, "digestEnabled": true, "digestHourUTC": 7, "createdAt": "...", "updatedAt": "..." },
  "settings": { "autoApplyRules": false, "autoSyncEnabled": false, "digestEnabled": true, "digestHourUTC": 7 },
  "rules": [ ... ],
  "protectedSenders": [ ... ],
  "actionLog": [ ... ],
  "mailAccounts": [ ... ],
  "emails": [ ... ]
}
```

Every dataset is a key, an empty list when the user has none: `rules`,
`protectedSenders`, `snoozes`, `suggestions`, `senderPreferences`,
`smartLabels`, `unsubscribes`, `usage`, `actionLog`, `analysisJobs`,
`savedSearches`, `mailAccounts`, `aiSettings`, `waitlist`, `labels`, `emails`.

### Delete account (RGPD / right to erasure)

#### DELETE /api/account

Permanently erases the caller's account record and **all** user-owned datasets
(the same catalog the export covers, credentials included: the sealed app password
and the sealed BYOK provider key are deleted, not redacted). Irreversible; the UI
gates it behind a typed confirmation. The mailbox itself is never touched.
Returns per-dataset deletion counts. The shared analysis cache is not per-user
(it is keyed on a hash of sender and subject) and is not part of this.

```json
{ "status": "deleted", "deleted": { "rules": 4, "protectedSenders": 2, "actionLog": 137, "account": 1 } }
```

### Send today's digest now

#### POST /api/account/digest/test

Email the caller their 7-day recap immediately, from their own mailbox, using
the same rendering and the same delivery path as the scheduler:

- on the Gmail API, through their Gmail;
- on IMAP, through the mailbox's sending server (SMTP): implicit TLS or
  STARTTLS, never in the clear, certificate verified, logged in with the
  mailbox's address and its stored app password (AUTH PLAIN), from that address
  to that address. The server is the one recorded with the mailbox
  (`smtpHost`, `smtpPort`, `smtpTls`, see `GET /api/mailbox`), or, for a
  mailbox connected before it was recorded, the catalog's for its provider in
  the running edition.

The digest was the one feature nobody could verify: you turned it on and waited a
day to learn whether delivery worked (whether your Gmail grant still carried the
send scope, or whether your sending server takes your app password). This closes
that loop. It deliberately does **not** stamp `digestLastSentAt`, so a test never
consumes the day's real digest, and it sends even when the week is empty (unlike
the scheduler, which has no reason to mail "0 emails triés" unprompted).

One test per account per minute, a failed one included: each opens an
authenticated session with the sending server, and a burst of refused
passwords is what gets the instance's address throttled by a provider.

**Response:** `200 OK`
```json
{ "status": "sent", "subject": "Mailsorter : 34 emails triés cette semaine", "total": 34 }
```

**Error Responses:**
- `401 Unauthorized`: Missing session, or a dead Google grant (the SPA offers
  "Reconnecter Gmail"). Never on IMAP: the SPA signs out on a 401 before the
  reason shows.
- `422 Unprocessable Entity` (IMAP): Mailsorter does not know the mailbox's
  sending server: none on its row, none in the catalog for its provider
  ("Mailsorter ne connaît pas le serveur d'envoi de cette boîte : le récap ne
  peut pas en partir.")
- `429 Too Many Requests`: a test was asked less than a minute ago for this
  account ("Vous venez de demander un test. Attendez une minute avant d'en
  envoyer un autre."), with `Retry-After: 60`. Nothing is tried.
- `500 Internal Server Error`: the ledger could not be read
- `502 Bad Gateway`: the send failed. On the Gmail API, Gmail refused it
  (typically a missing `gmail.send` scope on an older authorization), or no
  mailbox is connected ("Reconnectez Gmail"). On IMAP, the sending server
  refused the app password ("Le serveur d'envoi de votre boîte a refusé le mot
  de passe d'application.": a 535, or Gmail's 534 5.7.9), blocked the sign-in
  for where it came from, the password being right ("Votre fournisseur a
  bloqué la connexion venant du serveur de Mailsorter. Autorisez-la depuis la
  sécurité de votre compte, puis réessayez.": Gmail's 534 5.7.14), could not
  be reached, or did not take the message; each has its own sentence, none of
  them about Gmail.

The matching preview, without sending anything, is `GET /api/stats/digest`.


---

## Billing Endpoints (Stripe)

Pro lifts the monthly AI cap of the hosted service's free plan (a self-hosted
instance has none to lift). These endpoints are active only when
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
- `401 Unauthorized`: Missing or expired session
- `409 Conflict`: User is already on Pro
- `502 Bad Gateway`: Stripe call failed
- `503 Service Unavailable`: Billing not configured

### Stripe Webhook

#### POST /api/billing/webhook

Receives Stripe events. The raw body is verified against the `Stripe-Signature`
header (HMAC-SHA256, 5-minute tolerance) before processing. Keeps the user's
`plan` in sync: `checkout.session.completed` -> pro;
`customer.subscription.updated/deleted` -> pro/free.

**Headers:**
- `Stripe-Signature` (required): Stripe webhook signature

**Response:** `200 OK` on success, `400 Bad Request` on signature failure.

> Usage and plan are reported by `GET /api/usage` (see "Account Endpoints").

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
- `401 Unauthorized`: Missing or expired session
- `404 Not Found`: No subscription / Stripe customer to manage
- `502 Bad Gateway`: Stripe call failed
- `503 Service Unavailable`: Billing not configured

---

## AI Sorting Endpoints

The AI triage surface. Every email is classified into an action
(`archive` / `delete` / `label` / `keep`) with a confidence score and a short
reasoning, persisted as a pending suggestion the user reviews and applies.

> The three routes that call the model, `analyze`, `analyze-async` and
> `analyze-sender`, answer `503 Service Unavailable` when no AI provider is
> available to the caller: none configured on the instance (a Mistral, OpenAI or
> Anthropic key, or a local model, see "IA locale" in the README) and no
> personal one (see "AI Provider Settings"). On the free plan of the hosted
> service (`EDITION=hosted`), `analyze` and `analyze-async` answer
> `402 Payment Required` once the monthly quota is spent (see `GET /api/usage`);
> `analyze-sender` checks no quota and does not count toward it. A self-hosted
> instance has no monthly cap. The other routes here read or apply stored
> suggestions and need no AI.

### Analyze (synchronous)

#### POST /api/ai/analyze

Analyze a set of messages now, by the ids `GET /api/emails` returns (Gmail
message ids, or IMAP UIDs), at most 200. A message missing from the stored
mailbox is fetched through the caller's session first. Over the Gmail API,
senders on auto-pilot get their learned action applied inline (source
`ai-auto`), without the model. Then the shared cache answers what it already
knows, and the rest goes to the configured AI provider in batches of 8, falling
back to one email at a time when a batch answer does not line up. The caller's
own provider is tried first when they set one, then the instance's, in
priority order, the next one on a transient error. A destructive verdict on a
protected sender is downgraded to `keep`, and so is, over IMAP, a `label`
verdict, which files the mail out of the inbox there. The model is told
nothing of the user's labels or folders: the prompt carries the sender, the
subject and an excerpt cut at 200 characters, and the verdict goes into the
cache every account shares, as the model gave it (each entry carries a
version, which also starts its key, so a verdict cached when the prompt still
listed the user's labels is never served again, and is deleted at the next
boot). The label it proposes is matched afterwards, for this account only,
with the labels Mailsorter made over the Gmail API, or over IMAP with the
user's own folders and nothing else: the same name once case, accents and a plural
mark are set aside, never a part of a name ("Facture" is stored as the
existing "Factures", "Pro" does not take "Promotions"). Over IMAP the name
stored is the folder's real one, cleaned as `POST /api/ai/apply` would make
it, and a `label` naming the inbox, a special folder, a folder outside the
user's namespace or a name no folder is made under becomes `keep`. One pending
suggestion is stored per email, replacing a pending one for the same email;
only model calls count toward the quota.

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

**Error Responses:**
- `400 Bad Request`: no ids, or more than 200
- `402 Payment Required`: the hosted free quota is spent
- `500 Internal Server Error`: nothing could be analyzed (the message names why)
- `503 Service Unavailable`: no AI provider

### Analyze (asynchronous job)

#### POST /api/ai/analyze-async

Enqueue the same analysis as a background job, preferred for large batches,
run by a pool of 3 workers. The id list is cut to its first 500 without an
error.

**Request body:** `{ "emailIds": ["18c...", "18d..."] }`

**Response:** `202 Accepted`
```json
{ "jobId": "662...", "status": "queued" }
```

**Error Responses:** `400` no ids; `402` the hosted free quota is spent; `503`
no AI provider.

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

Analyze up to 20 stored emails whose From contains the sender, and persist a
`SenderPreference` (default action, auto-apply off). Reads the stored mailbox
only, so it answers on both transports; `404` when no stored email matches.
The model is told none of the user's labels; the label it proposes is matched
afterwards with the user's, as `POST /api/ai/analyze` does.

**Request body:** `{ "senderEmail": "news@acme.com" }`

**Response:** `200 OK`
```json
{ "analysis": { "...": "AI verdict" }, "emailCount": 12, "preference": { "...": "SenderPreference" } }
```

### Apply one suggestion

#### POST /api/ai/apply

Apply a single suggestion to the mailbox, through the caller's transport, mark
it `applied`, and log it (source `ai`). A `keep` verdict moves nothing. A
destructive verdict on a protected sender (over IMAP, a `label` too) was
already downgraded to `keep` when the suggestion was created; this route does
not check the list again (`apply-batch` does).

Over IMAP, `archive`, `delete`, `keep` and `label` apply. A label is a folder
there: a `label` verdict moves the mail out of the inbox into the folder named
like the label (`labelName`), made (and subscribed) when the mailbox does not
have it, under the personal namespace prefix when the server has one
(`INBOX.Factures` on Courier), found whatever its case when it does, and
undoable like an archive: right after, `POST /api/emails/action` with
`unlabel` brings it back. A folder is made only under a name that holds no
control character, no `*` or `%`, no empty level and no `.` or `..` level, at
most 100 characters a level and 200 in all, whose first level is neither the
inbox, nor a special folder, nor outside the user's namespace; the server's
delimiter inside a level becomes `-` (`Amazon.fr` is `Amazon-fr` on a server
whose delimiter is `.`), and that cleaned name is the folder's name, shown and
recorded.
The history line carries the folder's name (`labelName`). A label that names a
folder that is not the user's own (the inbox, the archive, the trash, sent
mail, drafts, junk) answers `422` "Mailsorter ne classe pas de mail dans ce
dossier : c'est un dossier spécial de votre boîte." and nothing moves; a name
no folder is made under answers `422` "Mailsorter ne crée pas de dossier sous
ce nom : il est trop long ou contient un caractère réservé.", and a label
proposal with no name `422` "Cette proposition ne nomme aucun dossier où
classer le mail.". The analysis turns these into `keep` beforehand, so only a
proposal it could not settle (the mailbox did not open then) reaches them.
The name is settled again here as the analysis does: the user's folder named
alike, or the cleaned name, so a proposal stored with the model's "Facture"
goes into "Factures", and the history records the folder the mail went into.

**Request body:** `{ "suggestionId": "662..." }`

**Response:** `200 OK` -> `{ "status": "applied" }`, or, over IMAP,
`{ "status": "already" }` when the mail already left the inbox (the user
tidied it by hand): nothing moves, nothing is recorded, and the proposal
becomes `moot`.

**Error Responses:** `400` invalid id; `404` no such suggestion for this caller;
`422` over IMAP, a label naming a special folder or a name no folder is made
under; `428` no mailbox connected;
`500` the mailbox refused the action.

### Apply many suggestions

#### POST /api/ai/apply-batch

Apply a list of suggestions in one request (one session, looped server-side),
at most 200. Suggestions whose sender is protected are skipped for destructive
actions and stay pending, counted in `protectedSkipped`. Over IMAP a `label`
files the mail in a folder, out of the inbox, so it is one of them there: a
protected sender's mail is not filed. One request makes at most 10 new
folders over IMAP: a filing that would make one more counts in `failed`, the
mail stays in the inbox, and the answer carries `message` "Mailsorter crée au
plus 10 nouveaux dossiers à la fois : relancez le tri pour ranger les mails
suivants.". A filing refused for its folder (special, a name no folder is made
under, no name) counts in `failed` too, with the sentence `POST /api/ai/apply`
answers in `message`; several reasons are joined, each once. Each filing
settles its folder's name as `apply` does. A suggestion whose mail already left the
inbox (tidied by hand, from a screen loaded before) is counted neither in
`applied` nor in `failed`, and becomes `moot`; nor is a suggestion already
`moot`.

**Request body:** `{ "suggestionIds": ["a", "b", "c"] }`

**Response:** `200 OK`
```json
{ "applied": 8, "failed": 1, "total": 10, "appliedIds": ["a", "b"], "protectedSkipped": 1, "reversible": true }
```

`reversible` says whether `POST /api/emails/batch-undo` can reverse what this
batch moved, so the client offers "Annuler" only when it would work. It is true
on the Gmail API and over IMAP. A
`keep` suggestion moves nothing and has nothing to undo either way.

### Apply in bulk for a sender

#### POST /api/ai/apply-bulk

Gmail API only: `501` over IMAP.

Apply one action to every stored email from a sender ("learn once, clean up
everything"). `senderEmail` is the `senderEmail` of a `GET /api/senders` row, a
stored From header, and is matched exactly: the action reaches the mails that row
counts and nothing else, never a From that merely contains it. A protected
sender's mail is skipped for `archive`, `trash` and `delete`.

**Request body:**
```json
{ "senderEmail": "Acme News <news@acme.com>", "action": "archive", "labelName": "" }
```

**Response:** `200 OK`
```json
{ "applied": 34, "total": 40, "protectedSkipped": 0 }
```

### List suggestions

#### GET /api/ai/suggestions?status=pending

List the caller's suggestions, filtered by `status` (default `pending`), the
100 most recent.

**Response:** `200 OK`, an array of `AISuggestion` enriched with the identity of
the email each one is about: `subject`, `from`, `snippet`, resolved in one
lookup. Without them the client had to find the message among those currently on
screen, so any suggestion for an email outside the loaded page asked the user to
approve an action on an email shown as "Sans sujet" from "Expéditeur inconnu".
The three fields are omitted when the message is no longer in the stored mailbox.

### Reject a suggestion

#### POST /api/ai/suggestions/{id}/reject

Reject a pending suggestion. Marks it `rejected`; nothing changes in Gmail.

**Response:** `204 No Content`

### Reject many suggestions

#### POST /api/ai/reject-batch

Reject several pending suggestions in one request: a whole sender group, or the
whole panel. One request whatever the count, which is the point: one POST per
suggestion ran past the rate limiter's burst of 40, and the rejections it
refused came back on the next refresh. Nothing changes in the mailbox.

**Request body:** `{ "suggestionIds": ["a", "b", "c"] }`

**Response:** `200 OK`
```json
{ "rejected": 3, "total": 3 }
```

Only `pending` suggestions move: an id that is already applied or rejected, or
that belongs to another user, is counted in `total` and not in `rejected`.

**Errors:** `400` for an empty list, more than 200 ids, or any id that is not a
valid ObjectID (checked before anything is written, so a bad id rejects
nothing). `500` when the datastore write fails.

---

## AI Provider Settings (bring your own key)

Each account may set its own AI provider. It is tried first, before the
instance's providers, which remain the fallback on a transient error. It does
not lift the hosted free quota.

### Known providers

#### GET /api/ai/providers

The catalog the settings screen offers. Authenticated, and carries no secret.

**Response:** `200 OK`
```json
[
  { "name": "mistral", "displayName": "Mistral AI", "requiresApiKey": true, "requiresUrl": false,
    "defaultModel": "mistral-small-latest",
    "models": ["mistral-small-latest", "mistral-medium-latest", "mistral-large-latest"] }
]
```

The list holds `mistral`, `openai` (default `gpt-6-luna`), `anthropic`
(default `claude-haiku-5-5`), `ollama` (default `qwen3.5:9b`, `requiresUrl`)
and `openai-compatible` (no default model, `requiresUrl`).

### Read the caller's setting

#### GET /api/ai/settings

**Response:** `200 OK`. Without a personal setting:
```json
{ "configured": false, "provider": "mistral,ollama", "instanceMode": true }
```
`provider` is then the instance's active providers in the order they are
tried, comma-joined, or `none`. With one:
```json
{ "configured": true, "provider": "openai", "model": "gpt-6-luna", "baseUrl": "",
  "apiKeyMasked": "sk-a...9xQ2", "instanceMode": false }
```
The key never leaves the server: `apiKeyMasked` shows its first and last four
characters, or `****` for a key of 8 characters or fewer.

### Save the caller's setting

#### PUT /api/ai/settings

**Request body:**
```json
{ "provider": "openai", "apiKey": "sk-...", "model": "gpt-6-luna", "baseUrl": "" }
```

Replaces the stored setting as a whole: a field left empty is stored empty, the
key included. The key is sealed (AES-256-GCM) before it is stored, and stripped
from the RGPD export.

`baseUrl` is checked here, on its shape only: empty, or `https`, with a host
that is neither `localhost`, a `.localhost` or `.local` name, nor an IP literal
outside the public internet. A host name is not resolved at this point. A model
on the operator's network is set in the instance's `.env`, never here.

**Response:** `200 OK` -> `{ "status": "saved" }`

**Error Responses:**
- `400 Bad Request`: unknown provider, a field over its length limit (key 1024,
  model 256, address 2048), or a refused address
- `500 Internal Server Error`: the key could not be sealed or the setting saved

### Test a setting before saving it

#### POST /api/ai/settings/test

Same body as `PUT /api/ai/settings`, with the same checks. Builds that one
provider alone, with no instance fallback, and asks it to classify a fixed test
email. Nothing is stored.

**Response:** `200 OK`
```json
{ "status": "ok", "provider": "openai", "model": "gpt-6-luna",
  "test": { "action": "keep", "label_name": "", "confidence": 0.8, "reasoning": "..." } }
```

**Error Responses:** `400` for every failure, including the provider's own: an
invalid key, a refused permission, an unreachable address, or the provider's
error message with the key redacted from it.

### Remove the caller's setting

#### DELETE /api/ai/settings

Back to the instance's providers. **Response:** `200 OK` -> `{ "status": "deleted" }`,
whether or not a setting existed.

---

## Senders Endpoints

Learn-once triage keyed by sender.

### List senders

#### GET /api/senders

Senders of the stored mailbox, the 50 with the most emails, with their counts
and any learned preference. Reads the datastore only.

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

Turn a sender into a permanent deterministic rule: every email whose `From`
contains the sender gets the action whenever the rules run (at sync time with
`autoApplyRules`, or on `POST /api/rules/apply`; Gmail API only, see "Sorting
Rules Endpoints").

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

**Response:** `200 OK` -> `{ "status": "updated" }`

---

## Sorting Rules Endpoints

Deterministic, **AI-free** triage. A rule pairs conditions with an action; when
the conditions match an email, the action is applied directly via Gmail: no
model call, no quota consumed. Rules are the free, predictable complement to the
AI suggestions, and the AI analysis does not consult them.

Rules run in two places only: at sync time when `autoApplyRules` is on, and on
`POST /api/rules/apply`. Both are Gmail API only: over IMAP the sync skips them
and `apply` / `preview` answer `501`. Creating, editing, reordering, exporting
and importing rules read and write the datastore only and answer on both
transports.

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

#### PUT /api/rules/{id}

Validates and updates an existing rule. **Response:** `{ "status": "updated" }`.

**Error Responses:**
- `400 Bad Request`: Invalid rule ID or validation error
- `404 Not Found`: Rule not found

### Delete Sorting Rule

#### DELETE /api/rules/{id}

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

Reports what the rules **would** do over the first 200 messages of the inbox,
without touching it. This is the safe way to check a ruleset before applying
it: it runs the same matching function as `POST /api/rules/apply`.

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
      "messageId": "18c...", "from": "news@acme.com", "subject": "...",
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

Get the labels a mail can be filed under: all Gmail labels, or, over IMAP, the
user's own folders.

Over IMAP a folder stands for a label, so the answer lists the folders in the
same shape, `{ "id", "name", "type": "user" }`, with the folder's full name as
both id and name (`"Projets/Maison"` for a folder inside another), sorted. The
name is the one the user reads: without the personal namespace prefix the
server announces (NAMESPACE: `INBOX.` on Courier and some Cyrus servers), with
`/` between levels whatever the server's delimiter (`INBOX.Projets.Maison`
reads `Projets/Maison`). Not listed: the inbox, the special folders (archive,
trash, sent mail, drafts, junk, and Gmail's All Mail, Starred and Important),
known by their special-use attribute or, when the server marks none for that
kind, by their usual names (under the prefix too: `INBOX.Sent`), a folder
outside the personal namespace (under the prefix of another user's or of a
shared namespace, `Other Users/`, `Public/`, even when the personal prefix is
empty), and a folder that cannot hold mail.

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
- `401 Unauthorized`: Missing or expired session, or a dead Google grant
- `428 Precondition Required`: no mailbox is connected to this account yet
- `500 Internal Server Error`: Failed to fetch labels

---

## Smart Labels Endpoints

The labels Mailsorter made in the user's Gmail (one is stored each time a
`label` verdict creates a Gmail label), with their Gmail ids. The model is told
none of them: a `label` verdict is matched with them afterwards, for this
account only and over the Gmail API only (the same name once case, accents and
a plural mark are set aside), so it reuses a stable set instead of a new name
each time. Over IMAP they play no part: only the user's folders count. No
screen of the SPA calls these two routes.

### List smart labels

#### GET /api/smart-labels

**Response:** `200 OK`, the caller's smart labels:
```json
[
  { "id": "665...", "userId": "you@example.com", "name": "Factures", "gmailLabelId": "Label_12",
    "description": "", "keywords": [], "emailCount": 0,
    "createdAt": "2026-11-02T10:00:00Z", "updatedAt": "2026-11-02T10:00:00Z" }
]
```

### Create a smart label

#### POST /api/smart-labels

Gmail API only: `501` over IMAP. Creates the label in Gmail, then stores it.

**Request body:** `{ "name": "Factures", "description": "", "keywords": [] }`

**Response:** `201 Created`, the stored smart label.

**Error Responses:**
- `400 Bad Request`: name missing or over 100 characters
- `428 Precondition Required`: no mailbox is connected to this account yet
- `500 Internal Server Error`: Gmail refused the label, or it could not be stored
- `501 Not Implemented`: the mailbox is on IMAP

---

## Pro Waitlist

#### POST /api/waitlist

Public: it records a visitor's interest in Pro before any account exists. A
caller who does have a session gets their account attached to the row, which
puts it in their RGPD export and erasure.

**Request body:** `{ "email": "you@example.com", "source": "landing" }`

The address is trimmed and lowercased (`Name <addr>` is accepted, the address
is kept). `source` names the surface the signup came from, `pricing` when
empty. Signing up twice is not an error: the row is upserted on the address.

**Response:** `200 OK` -> `{ "joined": true, "email": "you@example.com" }`

**Error Responses:**
- `400 Bad Request`: not a usable address (or over 254 characters), or a
  `source` over 64 characters
- `500 Internal Server Error`: the row could not be stored

---

## Config Endpoints

### Instance status

#### GET /api/config/status

Public boot probe. The SPA calls it before any login to decide whether the
instance has any way in at all, whether Pro can be bought yet, which edition
is running, and which landing to render. It is the only public route under
`/api/config/`, and it returns nothing beyond these five fields.

**Response:** `200 OK`
```json
{ "isConfigured": true, "mailboxSignIn": true, "billingOn": false, "edition": "self-hosted", "uiTheme": "hotel" }
```

`mailboxSignIn` says whether a mailbox can be connected over IMAP here: it is
true when the running edition's catalog carries at least one IMAP route
(autodiscovery-only routes count too). The SPA gates its whole boot on
`isConfigured || mailboxSignIn`. Gating on `isConfigured` alone sent every
visitor of an instance with no Google credentials to a setup screen telling them
to create a Google Cloud project, which the hosted edition can never use.

`isConfigured` reflects the live OAuth client, whichever source its credentials
came from at boot. The credentials themselves are read from `GMAIL_CLIENT_ID`,
`GMAIL_CLIENT_SECRET` and `GMAIL_REDIRECT_URL` and have **no HTTP surface**:
they can be neither read nor written over the API, and both former
`/api/config/gmail` routes return `404`.

`edition` is `self-hosted` or `hosted`, from the `EDITION` environment variable.
It decides which mailbox providers exist (see `GET /api/providers`), hence
`mailboxSignIn`, and whether there is anything to bill at all.

`uiTheme` is `hotel` or `classic`, from the `UI_THEME` environment variable
(default `hotel`). The SPA wears the Grand Hotel theme or the classic one, on
the landing and on every screen of the dashboard, chosen at runtime: switching
needs a backend restart, never a rebuild. The same variable decides the look of
the daily digest email (see `GET /api/stats/digest`). A
visitor can preview the other one for their tab with `?ui=hotel` or
`?ui=classic` (`?ui=default` clears it).

---

#### GET /api/providers

Public. The mailbox catalog for the running edition: which providers this
instance can reach, over which transport, with which credential, what each route
can do, and what can stop it from working for a given user.

Public because the connect screen runs before any account exists, and it carries
no secret: provider names, hostnames, ports and help text.

The SPA hardcodes no provider. It renders this payload, which is the same table
`internal/api` connects with. The payload lists every provider of the edition,
including the ones that cannot connect yet: a route with `transport: "graph"`
(Microsoft Graph is not implemented) or an IMAP route with `autodiscover: true`
(no server in the catalog) is not connectable today, and connecting such an
address answers `422`. Google Workspace's IMAP route does name a server, but
the provider has no domain to be recognized by: a Workspace address falls to
the generic entry and gets the same `422`.

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
          "note": "Pleine fidélité. L'utilisateur crée son projet Google Cloud, ..."
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
          "note": "Aucune surface de conformité: pas de client OAuth, ..."
        }
      ]
    }
  ]
}
```

Routes are ordered best first: a revocable token before a pasted full-mailbox
credential. `auth` is the credential the route asks for: `oauth`,
`app-password`, or `password`, the mailbox password itself (Proton through
Bridge, which shows its own, and the autodiscovery routes). `autodiscover` is true for an
IMAP route whose IMAP or SMTP server the catalog does not carry: its settings
would have to be found from the domain, which is not implemented yet, so the
screen must not promise settings it does not have.

`capabilities` describe what the provider offers on that route, not what
Mailsorter does with it today. Gmail over IMAP declares labels, provider
search, stable ids, threads and sending (through Gmail's IMAP extensions and
SMTP); of those, the IMAP transport uses only the sending, for the daily recap.
What actually works per transport is in "Transports and what works on each".

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
| `401` | No session, an expired one, a dead Google grant, wrong sign-in credentials, or credentials the mail provider refused | `{ "error": "Authentication required", "status": 401 }` |
| `402` | The hosted service's free monthly AI quota is spent (`analyze` and `analyze-async` only) | `{ "error": "Quota mensuel atteint. Passez à Pro pour continuer.", "status": 402 }` |
| `404` | No such resource for this caller, or no such route | `{ "error": "Rule not found", "status": 404 }` |
| `405` | The route exists, not with this method | `{ "error": "Method not allowed on this route", "status": 405 }` |
| `409` | Already done or already taken: an action undone twice, an existing account, an address already linked to Google, a user already on Pro | `{ "error": "Action déjà annulée", "status": 409 }` |
| `413` | Body over 1 MiB, or an attachment over 30 MiB | `{ "error": "Request body too large", "status": 413 }` |
| `422` | Valid request the target cannot serve: an address with no IMAP route in this edition, a sender with no unsubscribe link | `{ "error": "Cette boîte ne se connecte pas par IMAP sur cette instance.", "status": 422 }` |
| `428` | No mailbox is connected to this account yet | `{ "error": "Aucune boîte mail n'est connectée à ce compte. Rendez-vous sur /connect pour en brancher une.", "status": 428 }` |
| `429` | Rate limited: 20 req/s sustained, burst 40, per account or visitor; the sign-in limiter is far lower | `{ "error": "Too many requests", "status": 429 }` |
| `500` | Mailsorter failed (usually the datastore) | `{ "error": "Failed to load rules", "status": 500 }` |
| `501` | Not implemented for this mailbox: a Gmail-only route called on an IMAP mailbox, a label on IMAP, a query term the stored mailbox cannot answer | `{ "error": "Cette action n'est pas encore disponible sur une boîte branchée en IMAP.", "status": 501 }` |
| `502` | A service Mailsorter depends on failed: Gmail, the mail server, Stripe | `{ "error": "Report impossible : ...", "status": 502 }` |
| `503` | A dependency is not configured (AI, billing), or the datastore is unreachable (`/health`) | `{ "error": "Aucune IA n'est configurée sur cette instance.", "status": 503 }` |

A `401` on a route the SPA considers non-optional clears the session and returns
the user to the login screen.

## CORS

The allowed origins come from `ALLOWED_ORIGINS` (comma separated, read at
startup, no rebuild needed). Unset, they are:
- `http://localhost:3000`
- `http://localhost`
- `https://mailsorter.sohbi.dev`

In production the SPA calls the API on its own origin through the nginx proxy,
so CORS does not come into play.

Allowed methods: GET, POST, PUT, DELETE, OPTIONS

Allowed headers: Content-Type, Authorization, X-User-Email (a client-sent
`X-User-Email` is still stripped by the auth middleware). Credentials are
allowed.
