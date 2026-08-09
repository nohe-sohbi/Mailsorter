---
subsystem: gmail-sync
description: The Gmail v1 integration layer: OAuth scopes and token refresh, message listing and fetching, header and MIME body parsing, the retry/backoff wrapper, label and mutation operations, the inbox upsert into MongoDB, and List-Unsubscribe handling.
keywords: [gmail, gmail api, oauth scopes, token refresh, sync inbox, messages list, messages get, messages modify, retry backoff, 429, list-unsubscribe, one-click unsubscribe, mime body, base64url, mailbox stats]
files:
  - backend/internal/gmail/gmail.go
  - backend/internal/gmail/retry.go
  - backend/internal/gmail/body_test.go
  - backend/internal/gmail/headers_test.go
  - backend/internal/gmail/retry_test.go
  - backend/internal/gmail/unsubscribe_test.go
  - backend/internal/api/handlers.go
  - backend/internal/api/unsubscribe.go
  - backend/internal/api/auto_sync.go
  - backend/internal/models/models.go
priority: high
related: [auth-session-security, data-model, background-workers, rules-engine]
last-verified: 2026-08-09
---
# Gmail Sync

`internal/gmail` is the only package that speaks to the Gmail v1 API. It exposes a
thin, retry-wrapped `Service` plus three pure parsers (`ParseEmailHeaders`,
`GetEmailBody`, `ParseUnsubscribe`) that turn a `*gmail.Message` into the fields
`models.Email` stores. `internal/api` owns every call site, every MongoDB write and
every ledger entry: the `gmail` package never touches Mongo.

## Overview

| Aspect | Reality |
|---|---|
| Client library | `google.golang.org/api/gmail/v1` (pinned 0.154.0), `golang.org/x/oauth2` 0.15.0 |
| Auth model | One instance-wide OAuth Web-application client, per-user refresh tokens stored on the `users` document |
| API endpoints actually used | 8, listed below. No History API, no watch/push, no threads, no drafts, no attachments |
| Resilience | Every Gmail call goes through `withRetry` / `retryErr` (`retry.go`) |
| Sync model | Full poll of `in:inbox` on demand, upserted into Mongo. No incremental `historyId` cursor exists |
| State of truth | Gmail is authoritative for labels; Mongo holds the last synced projection plus the derived unsubscribe fields |

### Every Gmail endpoint this app calls

Grep-verified: these are the only `Users.*` calls in the repository.

| Endpoint | Wrapper | Used by |
|---|---|---|
| `users.messages.list` | `ListMessagesWithPagination` | `GetEmails`, `syncInbox`, rules preview/apply |
| `users.messages.get` (format `full`) | same, plus `GetMessage` | list hydration, `Unsubscribe`, `Snooze` |
| `users.messages.modify` | `ModifyMessage` | every mutation (archive, trash, read, star, label, snooze, undo) |
| `users.messages.send` | `SendMessage` | daily digest only (`digest_scheduler.go`) |
| `users.labels.list` | `ListLabels` | `GetLabels`, `CreateLabel` dedupe, `GetMailboxStats` |
| `users.labels.create` | `CreateLabel` | `ensureLabel` (smart labels, rule labels, snooze label) |
| `users.labels.get` | inline in `GetMailboxStats` | per-label counters |
| `users.getProfile` | `GetUserProfile`, inline in `GetMailboxStats` | OAuth callback identity, mailbox totals |

## OAuth Scopes

Requested in exactly two places, `NewService` and `UpdateConfig` in `gmail.go`, with
an identical list. Both must be edited together or a hot config reload silently
changes the consent request.

| Constant | URL | What it unlocks here |
|---|---|---|
| `gmail.GmailReadonlyScope` | `https://www.googleapis.com/auth/gmail.readonly` | `messages.list`, `messages.get`, `labels.list`, `labels.get`, `getProfile` |
| `gmail.GmailModifyScope` | `https://www.googleapis.com/auth/gmail.modify` | `messages.modify`: archive, trash, read/unread, star, label add/remove |
| `gmail.GmailLabelsScope` | `https://www.googleapis.com/auth/gmail.labels` | `labels.create` for smart labels, rule labels and `Mailsorter/Reporté` |
| `gmail.GmailSendScope` | `https://www.googleapis.com/auth/gmail.send` | `messages.send` for the daily digest, sent from the user's own account |

There is no `gmail.compose`, no `gmail.settings.*`, no `https://mail.google.com/`
full scope. The auth URL is built with `oauth2.AccessTypeOffline` only: no
`ApprovalForce`, so a user who already granted the first three scopes before the
digest feature shipped keeps a token without `gmail.send` until they reconnect.
`sendOneDigest` logs that failure rather than surfacing it.

### Token lifecycle

`getUserToken` (`ai_handlers.go:770`) is the single entry point; `gmailClientFor`
(`handlers.go:29`) wraps it and is what handlers should call.

```
handler -> gmailClientFor -> getUserToken
  users.findOne(email) -> build oauth2.Token{access, refresh, expiry}
  expiry in the past?
     no refreshToken            -> errReauthRequired
     RefreshToken() fails       -> errReauthRequired
     ok                         -> persist accessToken + tokenExpiry, continue
  -> gmailService.GetClient(token) -> *gmail.Service
```

`errReauthRequired` maps to HTTP 401 through `writeAuthError` (`respond.go`), which
is what makes the SPA restart OAuth instead of looping on 500s. Any handler that
obtains a client must route its error through `writeAuthError`, never `http.Error`.

## Sync Flow, Step by Step

`syncInbox` (`handlers.go:354`) is shared by the manual `POST /api/emails/sync`
handler and by the background auto-sync sweeper, so both behave identically.

| Step | What happens | Source |
|---|---|---|
| 1 | Resolve an authenticated client (refresh + persist if needed) | `gmailClientFor` |
| 2 | `ListMessages(client, "in:inbox", 100)`: one `messages.list`, then one `messages.get` per id | `gmail.go:113` |
| 3 | If `autoApplyRulesEnabled`, load the user's enabled rules and their protected-sender list | `handlers.go:369` |
| 4 | Per message: `ParseEmailHeaders`, `GetEmailBody`, `ParseUnsubscribe` | `handlers.go:377` |
| 5 | Build `models.Email`, upsert on `{messageId, userId}`, count `synced` on a nil error | `handlers.go:399` |
| 6 | Rule autopilot: `rules.FirstMatch` then `applyRuleToMessage` (protect-guarded), `logAction(..., SourceRule)` per applied action | `handlers.go:406` |
| 7 | `$inc appliedCount` per rule name on `sorting_rules`, best-effort | `handlers.go:421` |
| 8 | Return `(synced, len(messages), rulesApplied)` | `handlers.go:428` |

Callers and their budgets:

| Caller | Query | Cap | Context timeout |
|---|---|---|---|
| `POST /api/emails/sync` | `in:inbox` | 100 | 30s |
| Auto-sync sweeper | `in:inbox` | 100 | 5 min for the whole batch of users |
| `GET /api/emails` | `?q=` or `in:inbox` | 50 default, 500 hard cap | 10s |
| `ApplyRules` | `in:inbox` | 200 | 90s |
| `PreviewRules` (dry run) | `in:inbox` | 200 | 60s |
| `GET /api/stats` | n/a (labels walk) | n/a | 15s |

### Read path: two shapes, one parser

| | `GET /api/emails` | `syncInbox` |
|---|---|---|
| Persists to Mongo | no | yes, upsert |
| Sets `Body` | **no**, the field is left empty | yes, via `GetEmailBody` |
| Pagination | yes, `pageToken` in, `nextPageToken` + `resultSizeEstimate` out | no, first page only |
| Applies rules | no | yes when autopilot is on |

Consequence: AI analysis reads `models.Email` back out of Mongo
(`analysis.go:47`), so an email that was only ever fetched through `GET /api/emails`
has no body for the model to reason about. Sync is the write path, always.

## Header Parsing

`ParseEmailHeaders(message) (from, subject string, to []string, date time.Time)`.

| Header | Destination | Behavior |
|---|---|---|
| `From` | `from` | exact-case match, last occurrence wins |
| `Subject` | `subject` | exact-case match, last occurrence wins |
| `To` | `to` | appended, one slice entry per `To` header (a single header with several addresses stays one string) |
| `Date` | `date` | parsed by `parseDateHeader`, see below |

Anything else is ignored: no `Cc`, no `Reply-To`, no `Message-ID`. Note the
asymmetry with `ParseUnsubscribe`, which lowercases header names before matching
while `ParseEmailHeaders` compares them case-sensitively.

### Date layouts

`dateHeaderLayouts` (`gmail.go:327`), tried in order, on the trimmed value:

```
time.RFC1123Z                     Mon, 02 Jan 2006 15:04:05 -0700
time.RFC1123                      Mon, 02 Jan 2006 15:04:05 MST
"Mon, 2 Jan 2006 15:04:05 -0700"  single-digit day
"Mon, 2 Jan 2006 15:04:05 MST"    single-digit day, named zone
"2 Jan 2006 15:04:05 -0700"       no weekday
"2 Jan 2006 15:04:05 MST"         no weekday, named zone
```

Fallback chain: no layout matches -> `time.UnixMilli(message.InternalDate).UTC()`
when `InternalDate > 0` -> zero `time.Time`. This exists because `time.RFC1123Z`
alone rejects the single-digit-day and named-zone forms and used to silently zero
the date (see `BACKLOG.md` on the swallowed `time.Parse` error).

## MIME Body Extraction

`GetEmailBody(message) string`, three steps, first hit wins:

1. `message.Payload.Body.Data` non-empty: decode and return.
2. Iterate `message.Payload.Parts` and return the first part whose `MimeType` is
   `text/plain` **or** `text/html` and whose `Body.Data` is non-empty.
3. Return `""`.

The walk is one level deep: it does not recurse into nested parts.

`decodeBodyData` (`gmail.go:390`) tries `base64.URLEncoding` (padded), then
`base64.RawURLEncoding` (Gmail frequently omits padding), then returns the raw
string unchanged rather than dropping it. Returning the raw base64 would be worse
than useless downstream: the deterministic rules engine compares the body as
plaintext, so an undecoded body would only match by accident.

## List-Unsubscribe Handling

`ParseUnsubscribe(message) (httpURL, mailto string, oneClick bool)` reads two
headers, matched case-insensitively:

| Header | RFC | Use |
|---|---|---|
| `List-Unsubscribe` | 2369 | comma-separated `<uri>` list, split by `splitAngleList` |
| `List-Unsubscribe-Post` | 8058 | `oneClick` is true only if it contains `one-click` (case-insensitive) **and** an http(s) URL was found |

First `https://` or `http://` token wins for `httpURL`, first `mailto:` token wins
for `mailto`. A nil message or nil payload returns zero values without panicking.

The three results land on `models.Email` as `unsubUrl`, `unsubMailto`,
`unsubOneClick` (all `omitempty` in both BSON and JSON), written by both
`GetEmails` and `syncInbox`.

### The unsubscribe action

`POST /api/unsubscribe` (`unsubscribe.go:22`), 30s context:

```
GetMessage(messageID) -> ParseUnsubscribe + ParseEmailHeaders
  no httpURL and no mailto -> 422
  oneClick  -> OneClickUnsubscribe(httpURL); on success method="one-click" status="done" done=true
               on failure fall through and hand the link back to the client
  http only -> method="browser" status="opened"
  mailto only -> method="mailto" status="opened"
upsert unsubscribes on {userId, senderEmail}   (idempotent per sender)
req.AlsoArchive -> archiveBySender: for every stored email of that sender,
                   ModifyMessage(remove INBOX) + logAction(archive, SourceUnsubscribe)
```

`OneClickUnsubscribe` (`gmail.go:447`) is a plain `net/http` POST, **not** a Gmail
call and therefore **not** wrapped by the retry policy:

```
Timeout:      12s
Method:       POST
Body:         List-Unsubscribe=One-Click
Content-Type: application/x-www-form-urlencoded
User-Agent:   Mailsorter/1.0 (+unsubscribe)
Accepted:     any status < 400; >= 400 returns an error
Schemes:      https and http both accepted
```

`GET /api/subscriptions` never touches Gmail: it aggregates the `emails`
collection on `unsubUrl` / `unsubMailto`, groups by `from`, sorts by count and caps
at 100, then flags senders already present in `unsubscribes`.

## Mutations

Every mutation is a `users.messages.modify` through `ModifyMessage(client, id,
addLabels, removeLabels)`. There is no call to `messages.trash` or
`messages.untrash`: trashing is expressed as adding the `TRASH` label.

### Direct actions, `POST /api/emails/action`

| `action` | Add | Remove | Ledger entry |
|---|---|---|---|
| `archive` | | `INBOX` | `archive` / `SourceDirect` |
| `delete`, `trash` | `TRASH` | | `delete` / `SourceDirect` |
| `unarchive` | `INBOX` | | none (reversal) |
| `untrash` | `INBOX` | `TRASH` | none (reversal) |
| `read` | | `UNREAD` | `read` / `SourceDirect` |
| `unread` | `UNREAD` | | none (reversal) |
| anything else | | | 400 Unsupported action |

### The other mutation paths

| Path | Add / Remove | Source constant | File |
|---|---|---|---|
| Rule action `archive` | remove `INBOX` | `SourceRule` | `rules.go:441` |
| Rule action `trash` | add `TRASH` | `SourceRule` | `rules.go:443` |
| Rule action `mark_read` | remove `UNREAD` | `SourceRule` | `rules.go:445` |
| Rule action `star` | add `STARRED` | `SourceRule` | `rules.go:447` |
| Rule action `label` | add resolved label id | `SourceRule` | `rules.go:449` |
| AI suggestion apply / apply-batch / apply-bulk | archive, trash or label | `SourceAI`, `SourceBulk` | `ai_handlers.go` |
| Sender auto-pilot | archive, trash or label | `SourceAIAuto` | `ai_handlers.go:67` |
| Snooze | add `Mailsorter/Reporté` label (`snoozeLabelName`), remove `INBOX` | `SourceSnooze` | `snooze.go:91` |
| Snooze wake | add `INBOX` + `UNREAD`, remove snooze label | `SourceSnooze` | `snooze.go:205` |
| Unsubscribe sweep | remove `INBOX` | `SourceUnsubscribe` | `unsubscribe.go:131` |
| Undo | add `INBOX`, or `INBOX` + remove `TRASH`, or add `UNREAD` | `SourceUndo` | `history.go:153` |

## Labels

`ListLabels` returns the raw `[]*gmail.Label` and `GET /api/labels` serializes it
untouched (that endpoint has no UI, see CLAUDE.md).

`CreateLabel(service, name)` is create-or-get, not create:

1. `ListLabels`, return the existing id if a label already carries that exact name.
2. Otherwise create with `LabelListVisibility: "labelShow"`,
   `MessageListVisibility: "show"` and return the new id.

Note the signature takes `interface{}` and type-asserts to `*gmail.Service`,
returning `invalid gmail service` on a mismatch. It is the one wrapper that is not
statically typed.

`ensureLabel` (`ai_handlers.go:808`) is the layer above it: it looks the name up in
the `smart_labels` collection first, calls `CreateLabel` on a miss, and records the
resulting `gmailLabelId`. Rule application additionally memoizes ids in a
per-sync `labelCache map[string]string` so a rule that labels 100 messages resolves
the label once.

`models.Label` and `database.Labels()` (the `labels` collection) exist but have
**zero call sites** anywhere in the backend. The live label mapping lives in
`smart_labels`.

## Mailbox Stats

`GET /api/stats` -> `GetMailboxStats` (`gmail.go:259`), 15s context:

```
getProfile           -> TotalMessages, TotalThreads
labels.list          -> the label set
labels.get per label -> LabelStat{id, name, messagesTotal, messagesUnread, threadsTotal, type}
switch on label id   -> INBOX / UNREAD / SENT / DRAFT / SPAM / TRASH counters
```

The six counters are read from each system label's `MessagesTotal`, including
`UnreadCount`, which is the total of the `UNREAD` label rather than a
`MessagesUnread` sum. A `labels.get` failure skips that label and continues.

## Retry Policy

Defined in `retry.go`, installed by `NewService` as `defaultRetryConfig()`, applied
to every Gmail call through `withRetry[T]` (value-returning) or `retryErr`
(error-only). `withRetry` is a free function because Go methods cannot be generic.

```
maxRetries: 3           extra attempts after the first, so 4 total
baseDelay:  400ms       seeds the exponential backoff
maxDelay:   8s          ceiling on any single wait
sleep:      time.Sleep  injectable, tests pass a no-op
```

### Classification, `shouldRetry(err)`

| Error | Retryable | Retry-After honored |
|---|---|---|
| `nil` | no | |
| `googleapi.Error` 429 | yes | yes |
| `googleapi.Error` >= 500 | yes | yes |
| `googleapi.Error` 400, 401, 403, 404 (any other 4xx) | no | |
| wrapped `googleapi.Error` (`fmt.Errorf("%w")`) | unwrapped via `errors.As`, then as above | |
| transport error (timeout, reset, DNS) | yes | |
| `context.Canceled`, `context.DeadlineExceeded` | no | |

`parseRetryAfterHeader` reads only the delta-seconds form. A non-numeric value (an
HTTP-date) or a non-positive one yields 0.

### Backoff

`backoff(attempt, retryAfter)`:

1. `d = baseDelay << attempt` (400ms, 800ms, 1.6s, ...).
2. Full jitter: `d = d/2 + rand[0, d/2]`, so the wait lands in `[d/2, d]`.
3. `retryAfter` acts as a floor when it is larger.
4. `maxDelay` clamps the result last, so even a hostile `Retry-After` cannot stall
   a request past the handler's context budget.

Worst case with the defaults: 3 waits, each at most 8s, so at most 24s of sleeping
on top of the calls themselves. That is why `SyncEmails` runs on a 30s context and
`GetEmails` on 10s: a fully unlucky retry storm inside a 10s handler will be cut
short by the context, and `context.DeadlineExceeded` is deliberately not retried.

## Persistence

`syncInbox` upsert, `handlers.go:399`:

```go
filter := bson.M{"messageId": msg.Id, "userId": userEmail}
update := bson.M{"$set": email}   // the whole models.Email struct
opts := options.Update().SetUpsert(true)
```

| Field | Written by | Note |
|---|---|---|
| `messageId`, `userId`, `threadId` | sync | the composite identity |
| `from`, `to`, `subject`, `body`, `snippet` | parsers | `body` only on the sync path |
| `labelIds` | `msg.LabelIds` | raw Gmail label ids, system and user mixed |
| `receivedDate` | `parseDateHeader` | |
| `isRead` | `!contains(msg.LabelIds, "UNREAD")` | derived, not a Gmail field |
| `unsubUrl`, `unsubMailto`, `unsubOneClick` | `ParseUnsubscribe` | `omitempty` |
| `createdAt` | `time.Now()` | inside `$set`, so it is rewritten on every sync |

Indexes (`database.go:122`): `{userId, messageId}` and `{userId, from}`, both
non-unique.

## Auto-Sync

`auto_sync.go`, started by `NewHandler`. Goroutine with a ticker, not cron: a
redeploy restarts it.

```
autoSyncSweepInterval = 5 * time.Minute    how often the loop wakes
autoSyncInterval      = 30 * time.Minute   minimum gap between two syncs of one user
```

Per sweep: 5 min context, `users.find({autoSyncEnabled: true})`, then for each user
`schedule.Due(u.LastAutoSyncAt, now, autoSyncInterval)`. A due user is **stamped
before the attempt** (`lastAutoSyncAt = now`) so a transient failure costs one cycle
instead of retrying every sweep all day. A per-user failure is logged and skipped.

## Quota Considerations

The numbers below are call counts read from the code. Google's per-method quota
unit costs and the daily ceiling are not asserted here: check Google's Gmail API
usage-limits page before doing arithmetic on them.

| Operation | Gmail API calls |
|---|---|
| One `syncInbox` | 1 `messages.list` + up to 100 `messages.get` |
| One `GET /api/emails` page | 1 `messages.list` + up to `maxResults` (default 50, cap 500) `messages.get` |
| Rules preview or apply | 1 `messages.list` + up to 200 `messages.get`, plus 1 `messages.modify` per applied action |
| `GET /api/stats` | 1 `getProfile` + 1 `labels.list` + 1 `labels.get` per label |
| `ensureLabel` cache miss | 1 `labels.list` + 1 `labels.create` |
| Auto-sync, per opted-in user per 30 min | one `syncInbox` |

The listing pattern is N+1 by construction: `ListMessagesWithPagination` fetches
each id individually because `messages.list` returns ids only. There is no
`format=metadata` narrowing and no batch endpoint in use, so a 500-result page is
501 calls inside a 10s context.

Cheap paths that deliberately do **not** touch Gmail: `GET /api/subscriptions`
(Mongo aggregation), the AI analysis cache, and rule matching itself (`rules` is
pure and runs on the already-fetched `models.Email`).

## Testing

`internal/gmail` has no network test and no mock server: the four test files cover
the pure parsers and the retry math. Standard library only.

| File | Covers |
|---|---|
| `body_test.go` | padded base64url top-level body, unpadded base64url in a child part, empty payload |
| `headers_test.go` | the four accepted `Date` shapes, `InternalDate` fallback on an unparseable header, `ParseEmailHeaders` using that fallback |
| `retry_test.go` | `shouldRetry` over 13 cases (429, 429+Retry-After, 500, 503, 400, 401, 403, 404, wrapped 429, transport error, both context errors, nil), backoff bounds and the maxDelay clamp, success after transients, giving up after `maxRetries`, no retry on a permanent 403 |
| `unsubscribe_test.go` | `splitAngleList`, one-click detection, mailto-only, no headers, nil message |

The API-side handlers are exercised by `routes_integration_test.go`, which mounts
the real router with `gmail.NewService("", "", "")` (unconfigured) and Mongo pointed
at a dead address, so the degraded paths run for real.
`TestGmailCredentialsHaveNoHTTPSurface` asserts `/api/config/gmail` is 404 on both
GET and POST: the credentials must never be readable or writable over HTTP.

Do not add a Gmail HTTP mock without being asked. There is no mocking framework in
this repo by design (CLAUDE.md, Go tests).

## Known Pitfalls

- **A failed `messages.get` silently drops the message.** In
  `ListMessagesWithPagination` the per-id fetch does `continue` on error, so a
  partial page looks like a smaller inbox rather than an error. `synced` and
  `total` in the sync response are both computed after that filtering.
- **`GetClient` discards the constructor error**: `srv, _ := gmail.NewService(...)`.
  A failure there yields a nil-ish service that only blows up at the first call.
- **`GET /api/emails` does not set `Body`.** Only `syncInbox` calls `GetEmailBody`.
  Anything downstream that needs a body (AI analysis, body-matching rules) depends
  on a sync having run first.
- **`createdAt` is inside `$set`.** Every sync rewrites it to `time.Now()`, so it is
  the last-sync timestamp, not a first-seen timestamp.
- **The `{userId, messageId}` index is not unique.** A manual sync overlapping the
  auto-sync sweep can race on the same message; nothing at the database level
  prevents a duplicate row.
- **The scope list is duplicated** between `NewService` and `UpdateConfig`. Edit one
  and a hot reload requests a different consent set than a cold boot.
- **`AccessTypeOffline` without `ApprovalForce`**: users who connected before
  `gmail.send` was added keep a token missing that scope. The only symptom is a
  `digest: send failed` log line; they must reconnect Gmail.
- **`GetEmailBody` does not recurse.** A `multipart/mixed` whose text lives in a
  nested `multipart/alternative` yields an empty body, and therefore an empty
  `body` field in Mongo.
- **Header name matching is inconsistent**: `ParseEmailHeaders` compares
  case-sensitively, `ParseUnsubscribe` lowercases first.
- **`OneClickUnsubscribe` POSTs to a URL taken from an incoming email header**, and
  accepts `http` as well as `https`. There is no allow-list and no private-address
  guard; the only limits are the 12s timeout and the fact that the response body is
  discarded. Treat any change here as security-relevant.
- **`OneClickUnsubscribe` is outside the retry policy** (it is not a Gmail call), so
  a single transient failure falls back to handing the link to the browser.
- **`POST /api/emails/action` applies no protect guard.** `protect.Allowed` is
  checked on the rule, AI, bulk and auto-pilot paths, not on the direct action path.
  Adding a destructive action anywhere else means adding the guard with it
  (CLAUDE.md, code quality rule 4).
- **`models.Label` and the `labels` collection are dead.** Use `smart_labels` and
  `ensureLabel`.
- **No incremental sync exists.** Every sync is a full `in:inbox` poll capped at 100.
  Anything older than the cap, or already archived, is never revisited.
- **Do not retry a 403.** It is classified permanent on purpose: an insufficient
  scope or a revoked grant will not fix itself, and hammering it burns quota.

## Key Files

| File | Description |
|---|---|
| `backend/internal/gmail/gmail.go` | Service, OAuth config, all API wrappers, the three parsers |
| `backend/internal/gmail/retry.go` | Retry config, classifier, backoff, `withRetry` / `retryErr` |
| `backend/internal/gmail/body_test.go` | MIME body decode cases |
| `backend/internal/gmail/headers_test.go` | Date layout and InternalDate fallback cases |
| `backend/internal/gmail/retry_test.go` | Classifier and backoff cases |
| `backend/internal/gmail/unsubscribe_test.go` | RFC 2369 / 8058 parsing cases |
| `backend/internal/api/handlers.go` | `gmailClientFor`, `GetEmails`, `SyncEmails`, `syncInbox`, `EmailAction`, `GetLabels`, `GetMailboxStats`, OAuth callback |
| `backend/internal/api/unsubscribe.go` | Unsubscribe action, `archiveBySender`, subscriptions aggregation |
| `backend/internal/api/auto_sync.go` | Background sweeper, the 5 min / 30 min cadence |
| `backend/internal/api/ai_handlers.go` | `getUserToken`, `ensureLabel`, AI apply paths |
| `backend/internal/api/rules.go` | `applyRuleToMessage`, `applyOneAction`, the label cache |
| `backend/internal/api/snooze.go` | Snooze label move and restore |
| `backend/internal/api/history.go` | Undo, the reverse mutations |
| `backend/internal/api/respond.go` | `errReauthRequired`, `writeAuthError` |
| `backend/internal/models/models.go` | `User`, `Email`, `Label`, `SmartLabel`, `Unsubscribe`, `Subscription`, request bodies |
| `backend/cmd/server/main.go` | Credential load order: env first, legacy `gmail_config` document as fallback |

## References

### Source Files
- `backend/internal/gmail/gmail.go` - every Gmail v1 call and the pure parsers
- `backend/internal/gmail/retry.go` - the resilience policy shared by all of them
- `backend/internal/api/handlers.go` - the sync flow and the direct action surface
- `backend/internal/api/auto_sync.go` - the hands-free sync scheduler
- `backend/internal/api/unsubscribe.go` - RFC 2369 / RFC 8058 handling
- `backend/internal/database/database.go` - the `emails` indexes
- `docs/API.md` - request and response payloads for every route named here

### Related Context Docs
- [auth-session-security.md](auth-session-security.md) - OAuth state, session tokens, the `X-User-Email` contract
- [data-model.md](data-model.md) - the `emails`, `users`, `smart_labels` and `unsubscribes` documents
- [background-workers.md](background-workers.md) - the four goroutine loops, auto-sync among them
- [rules-engine.md](rules-engine.md) - what runs on each freshly synced email under autopilot
