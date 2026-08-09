---
subsystem: data-model
description: Exhaustive map of the 16 MongoDB collections, their Go structs, bson tags, indexes and owning packages, so a query can be written without opening the code.
keywords: [mongodb, collection, bson tag, index, ensureindexes, database.go, models.go, objectid, upsert, userId, persistence, schema, mongo query, collection names, field names]
files:
  - backend/internal/models/models.go
  - backend/internal/database/database.go
  - backend/internal/account/account.go
  - backend/internal/api/account_data.go
  - mongo-init/init-db.js
  - docker-compose.yml
priority: high
related: [gmail-sync, ai-triage, rules-engine, billing-quota]
last-verified: 2026-08-09
---
# Data Model: MongoDB Collections

Database `mailsorter` on MongoDB 7. Sixteen collections, one accessor each in
`backend/internal/database/database.go`, most of them backed by a struct in
`backend/internal/models/models.go`. This document is the anti-invention
reference: every collection name, field name, bson tag and index below was read
out of the source, not inferred.

`docs/ARCHITECTURE.md` is stale for this topic. Its "Composants" section lists
only 4 collections (`emails`, `sorting_rules`, `labels`, `users`), which was true
before most of the app existed. This file supersedes it for anything about the
persistence layer. `CLAUDE.md` names the 16 collections but stops at the names;
this file is the terrain under that map.

## Overview

Every `Collection("...")` call in the backend lives in `database.go`. No other
file opens a collection by name, so this table is exhaustive.

| Collection | Accessor | Go struct | Owning package / files | Purpose |
|---|---|---|---|---|
| `users` | `db.Users()` | `models.User` | `api`: handlers.go, account.go, billing.go, ai_handlers.go, auto_sync.go, digest_scheduler.go, account_data.go | Account record, OAuth tokens, plan, settings |
| `emails` | `db.Emails()` | `models.Email` | `api`: handlers.go (sync), analysis.go, ai_handlers.go, unsubscribe.go | Local mirror of the synced inbox |
| `labels` | `db.Labels()` | `models.Label` | nobody | Declared and indexed, never read or written. See pitfalls |
| `gmail_config` | `db.GmailConfig()` | `models.GmailConfig` | `cmd/server/main.go`, `cmd/test_decrypt/main.go` | Legacy instance OAuth credentials, read-only fallback |
| `sorting_rules` | `db.SortingRules()` | `models.SortingRule` | `api`: rules.go, handlers.go, account_data.go | Deterministic AI-free triage rules |
| `protected_senders` | `db.ProtectedSenders()` | `models.ProtectedSender` | `api`: protected.go, account_data.go | VIP safety net, blocks destructive automation |
| `snoozes` | `db.Snoozes()` | `models.Snooze` | `api`: snooze.go, account_data.go | Emails parked until a wake time |
| `action_log` | `db.ActionLog()` | `models.ActionLog` | `api`: ledger.go (write), history.go, account.go, account_data.go | Append-only ledger of every Gmail mutation |
| `ai_suggestions` | `db.AISuggestions()` | `models.AISuggestion` | `api`: analysis.go (write), ai_handlers.go, account_data.go | One AI verdict per email, pending until applied |
| `sender_preferences` | `db.SenderPreferences()` | `models.SenderPreference` | `api`: ai_handlers.go, analysis.go, account_data.go | Per-sender learned defaults and auto-pilot flag |
| `smart_labels` | `db.SmartLabels()` | `models.SmartLabel` | `api`: ai_handlers.go, analysis.go, account_data.go | AI-managed label catalog mapped to Gmail label IDs |
| `analysis_jobs` | `db.AnalysisJobs()` | `models.AnalysisJob` | `api`: jobs.go, account_data.go | Async batch-analysis job state |
| `analysis_cache` | `db.AnalysisCache()` | `models.AnalysisCacheEntry` | `api`: analysis.go | Content-keyed memo of AI verdicts, shared across users |
| `usage` | `db.Usage()` | none, raw `bson.M` | `api`: account.go, account_data.go | Monthly AI quota counter |
| `unsubscribes` | `db.Unsubscribes()` | `models.Unsubscribe` | `api`: unsubscribe.go, account_data.go | Completed or assisted unsubscribes per sender |
| `waitlist` | `db.Waitlist()` | `models.WaitlistEntry` | `api`: waitlist.go | Pro waitlist signups, not user-scoped |

### Scoping rules

| Scope | Collections | Key |
|---|---|---|
| Per user, filtered by `userId` | `emails`, `labels`, `sorting_rules`, `protected_senders`, `snoozes`, `action_log`, `ai_suggestions`, `sender_preferences`, `smart_labels`, `analysis_jobs`, `usage`, `unsubscribes` | `userId` is the user's email address, taken from the `X-User-Email` header the auth middleware sets |
| Per user, filtered by `email` | `users` | The account record uses `email`, NOT `userId`. See pitfalls |
| Global, content-keyed | `analysis_cache` | `sha256(lower(trim(from)) + "|" + lower(trim(subject)))`, deliberately shared across all users |
| Global, email-keyed | `waitlist` | `email`, unique. Logged-out visitors sign up |
| Instance-wide singleton | `gmail_config` | Read with an empty filter `bson.M{}`, at most one document |

### `_id` handling

Every struct declares `ID string` with `bson:"_id,omitempty"`. Mongo stores an
`ObjectID`; the driver decodes it into the Go `string` as its hex form because
`mongo-driver` 1.13.1 defaults `StringCodec.DecodeObjectIDAsHex` to true
(`bson/bsonoptions/string_codec_options.go`). Two consequences:

- Writing a struct with an empty `ID` omits `_id`, so Mongo generates one.
- Querying by id always goes through `primitive.ObjectIDFromHex(...)` first.
  `bson.M{"_id": "65f0..."}` as a string matches nothing.

## Collection field tables

### `users` (`models.User`)

| Go field | bson tag | Go type | Meaning |
|---|---|---|---|
| ID | `_id,omitempty` | string | ObjectID hex |
| Email | `email` | string | The identity. Same value other collections store as `userId` |
| AccessToken | `accessToken` | string | Google OAuth access token, `json:"-"` |
| RefreshToken | `refreshToken` | string | Google OAuth refresh token, `json:"-"` |
| TokenExpiry | `tokenExpiry` | time.Time | Access token expiry |
| Plan | `plan,omitempty` | string | `"pro"` or absent. `PlanFree = "free"` is only a Go-side default |
| StripeCustomerID | `stripeCustomerId,omitempty` | string | Set by the Stripe webhook |
| StripeSubscriptionID | `stripeSubscriptionId,omitempty` | string | Webhook lookup key for subscription events |
| PlanUpdatedAt | `planUpdatedAt,omitempty` | time.Time | Last plan change |
| AutoApplyRules | `autoApplyRules,omitempty` | bool | Run deterministic rules on every sync |
| AutoSyncEnabled | `autoSyncEnabled,omitempty` | bool | Background inbox sync opt-in |
| LastAutoSyncAt | `lastAutoSyncAt,omitempty` | time.Time | Throttle stamp for the 30 min minimum interval |
| DigestEnabled | `digestEnabled,omitempty` | bool | Daily digest opt-in |
| DigestHourUTC | `digestHourUTC,omitempty` | int | 0 to 23. A stored 0 is treated as unset |
| DigestLastSentAt | `digestLastSentAt,omitempty` | time.Time | At most one digest per user per day |
| CreatedAt | `createdAt` | time.Time | `$setOnInsert` at first OAuth callback |
| UpdatedAt | `updatedAt` | time.Time | Touched on every token refresh and settings write |

Written only through `bson.M` `$set` documents, never by marshalling the struct.
The creating write is the OAuth callback in `handlers.go`:
`filter {email}`, `$set {accessToken, refreshToken, tokenExpiry, updatedAt}`,
`$setOnInsert {email, createdAt}`, upsert true. A brand-new user document
therefore has six fields and nothing else.

### `emails` (`models.Email`)

| Go field | bson tag | Go type | Meaning |
|---|---|---|---|
| ID | `_id,omitempty` | string | ObjectID hex |
| MessageID | `messageId` | string | Gmail message id |
| UserID | `userId` | string | Owner email |
| ThreadID | `threadId` | string | Gmail thread id |
| From | `from` | string | Raw `From` header, usually `Name <addr>` |
| To | `to` | []string | Parsed recipients |
| Subject | `subject` | string | Subject header |
| Body | `body` | string | Decoded body text |
| Snippet | `snippet` | string | Gmail snippet |
| LabelIDs | `labelIds` | []string | Gmail label ids on the message |
| ReceivedDate | `receivedDate` | time.Time | Parsed `Date` header |
| IsRead | `isRead` | bool | Derived: `UNREAD` absent from labelIds |
| UnsubURL | `unsubUrl,omitempty` | string | RFC 2369 http unsubscribe |
| UnsubMailto | `unsubMailto,omitempty` | string | RFC 2369 mailto unsubscribe |
| UnsubOneClick | `unsubOneClick,omitempty` | bool | RFC 8058 one-click supported |
| CreatedAt | `createdAt` | time.Time | Overwritten on every sync, see pitfalls |

Upsert key: `bson.M{"messageId": msg.Id, "userId": userEmail}`, update
`bson.M{"$set": email}` where `email` is the whole marshalled struct.

Two aggregations read this collection and nothing else does anything clever:

| Endpoint | File | Pipeline |
|---|---|---|
| `GET /api/senders` | `backend/internal/api/ai_handlers.go` | `$match {userId}` then `$group _id=$from, emailCount, lastEmail=$max receivedDate`, sort by count, limit 50 |
| `GET /api/subscriptions` | `backend/internal/api/unsubscribe.go` | `$match {userId, $or unsubUrl/unsubMailto not in [null,""]}`, sort receivedDate desc, `$group _id=$from, emailCount, lastReceived, sampleMsgId=$first messageId, oneClick=$max unsubOneClick`, sort by count, limit 100 |

Both group on `$from`, the raw header, so the group key is `Name <addr>` and not
a bare address.

### `labels` (`models.Label`)

| Go field | bson tag | Go type | Meaning |
|---|---|---|---|
| ID | `_id,omitempty` | string | ObjectID hex |
| UserID | `userId` | string | Owner email |
| GmailID | `gmailId` | string | Gmail label id |
| Name | `name` | string | Label name |
| Color | `color` | string | Label color |
| CreatedAt | `createdAt` | time.Time | Creation |

Dead storage. `grep -rn "\.Labels()"` outside `database.go` returns nothing.
`GET /api/labels` calls `gmailService.ListLabels` live and never touches Mongo.
The struct and accessor exist, the collection stays empty.

### `gmail_config` (`models.GmailConfig`)

| Go field | bson tag | Go type | Meaning |
|---|---|---|---|
| ID | `_id,omitempty` | string | ObjectID hex |
| ClientID | `clientId` | string | OAuth client id |
| ClientSecretEncrypted | `clientSecretEncrypted` | string | AES-256-GCM ciphertext, `json:"-"` |
| RedirectURL | `redirectUrl` | string | OAuth redirect |
| IsConfigured | `isConfigured` | bool | Gate on the boot-time fallback |
| CreatedAt | `createdAt` | time.Time | Creation |
| UpdatedAt | `updatedAt` | time.Time | Last update |

Read once at boot in `cmd/server/main.go`, only when `GMAIL_CLIENT_ID` and
`GMAIL_CLIENT_SECRET` are both empty. Nothing writes it. Decryption uses
`ENCRYPTION_KEY`, so a rotated key makes the fallback unusable.

### `sorting_rules` (`models.SortingRule`)

| Go field | bson tag | Go type | Meaning |
|---|---|---|---|
| ID | `_id,omitempty` | string | ObjectID hex |
| UserID | `userId` | string | Owner email |
| Name | `name` | string | Display name, also the key used to increment `appliedCount` |
| Enabled | `enabled` | bool | Off rules are skipped by `enabledRules` |
| MatchAll | `matchAll` | bool | true = AND all conditions, false = OR any |
| Conditions | `conditions` | []RuleCondition | Embedded array, see below |
| Action | `action` | string | Primary action, mirrors `Actions[0]` |
| LabelName | `labelName,omitempty` | string | Required when the action is `label` |
| Actions | `actions,omitempty` | []RuleAction | Full ordered action list |
| Priority | `priority` | int | Lower runs first |
| AppliedCount | `appliedCount` | int | `$inc` counter, best-effort |
| CreatedAt | `createdAt` | time.Time | Creation |
| UpdatedAt | `updatedAt` | time.Time | Last edit |

Embedded `RuleCondition`: `field` / `operator` / `value`, all string. Fields are
`from`, `subject`, `snippet`, `to`, `body`. Operators are `contains`, `equals`,
`startsWith`, `endsWith`, `regex`, `notContains`, `notEquals`, `olderThan`,
`newerThan` (the last two compare an age in days against `receivedDate`).

Embedded `RuleAction`: `type` (`archive`, `trash`, `label`, `markRead`, `star`)
and `labelName,omitempty`.

Load order is `SetSort(bson.D{{priority, 1}, {createdAt, 1}})` so the first match
in slice order is the highest priority one.

### `protected_senders` (`models.ProtectedSender`)

| Go field | bson tag | Go type | Meaning |
|---|---|---|---|
| ID | `_id,omitempty` | string | ObjectID hex |
| UserID | `userId` | string | Owner email |
| Value | `value` | string | Normalized address or domain, from `protect.NormalizeEntry` |
| Kind | `kind` | string | `"address"` or `"domain"` |
| Note | `note,omitempty` | string | Free-text reason |
| CreatedAt | `createdAt` | time.Time | Creation |

Upsert key `{userId, value}`, backed by a unique index, which is what makes a
duplicate add a no-op. `value` is stored normalized, so a query with the raw
user input will often miss.

### `snoozes` (`models.Snooze`)

| Go field | bson tag | Go type | Meaning |
|---|---|---|---|
| ID | `_id,omitempty` | string | ObjectID hex |
| UserID | `userId` | string | Owner email |
| MessageID | `messageId` | string | Gmail message id |
| ThreadID | `threadId,omitempty` | string | Gmail thread id, best-effort |
| From | `from` | string | Sender, denormalized for the list view |
| Subject | `subject` | string | Subject, denormalized for the list view |
| WakeAt | `wakeAt` | time.Time | When the sweeper brings it back |
| Status | `status` | string | `scheduled`, `done`, `cancelled`, `failed` |
| Attempts | `attempts,omitempty` | int | Wake retries, capped at `maxSnoozeWakeAttempts = 5` |
| CreatedAt | `createdAt` | time.Time | Creation |
| UpdatedAt | `updatedAt` | time.Time | Last transition |

Upsert key includes the status: `{userId, messageId, status: "scheduled"}`.
The sweeper query is `{status: "scheduled", wakeAt: {$lte: now}}` with a 200 row
limit, run every minute. `cancelled` is declared in the struct comment; the
handlers only ever write `scheduled`, `done` and `failed`.

### `action_log` (`models.ActionLog`)

| Go field | bson tag | Go type | Meaning |
|---|---|---|---|
| ID | `_id,omitempty` | string | ObjectID hex |
| UserID | `userId` | string | Owner email |
| MessageID | `messageId` | string | Gmail message id acted on |
| Action | `action` | string | The verb applied to Gmail |
| Source | `source` | string | Where it came from, see the vocabulary below |
| Undone | `undone,omitempty` | bool | Set when the user reverses the entry |
| UndoneAt | `undoneAt,omitempty` | time.Time | When it was reversed |
| CreatedAt | `createdAt` | time.Time | Append time |

`Source` vocabulary, from the constants in `backend/internal/api/ledger.go`:
`direct`, `rule`, `ai`, `ai-auto`, `bulk`, `snooze`, `unsubscribe`, `undo`.
Never write a bare string here; add to that constant block.

`logAction` returns early and writes nothing when the action is empty or
`"keep"`, so the ledger only ever holds real Gmail mutations. The activity recap
reads `{userId, createdAt: {$gte: today-6d}}` with a 20000 row cap.

### `ai_suggestions` (`models.AISuggestion`)

| Go field | bson tag | Go type | Meaning |
|---|---|---|---|
| ID | `_id,omitempty` | string | ObjectID hex |
| UserID | `userId` | string | Owner email |
| EmailID | `emailId` | string | Gmail message id. NOT the `emails._id` |
| Action | `action` | string | `archive`, `delete`, `label`, `keep` |
| LabelName | `labelName` | string | Suggested label when the action is `label` |
| LabelID | `labelId` | string | Gmail label id resolved from `smart_labels` |
| Confidence | `confidence` | float64 | 0.0 to 1.0 |
| Reasoning | `reasoning` | string | Model explanation |
| Status | `status` | string | `pending`, `applied`, `rejected` |
| CreatedAt | `createdAt` | time.Time | Creation |
| AppliedAt | `appliedAt,omitempty` | time.Time | Set when applied |

Inserted as a marshalled struct by `persistSuggestion` in `analysis.go`. Updated
with `bson.M` `$set` on `{status, appliedAt, labelId}` from `ai_handlers.go`.
The list endpoint reads `{userId, status}` defaulting to `pending`, sorted by
`createdAt` descending, limit 100.

### `sender_preferences` (`models.SenderPreference`)

| Go field | bson tag | Go type | Meaning |
|---|---|---|---|
| ID | `_id,omitempty` | string | ObjectID hex |
| UserID | `userId` | string | Owner email |
| SenderEmail | `senderEmail` | string | Raw `From` header value, see pitfalls |
| SenderDomain | `senderDomain` | string | Extracted domain |
| SenderName | `senderName` | string | Display name |
| AutoApply | `autoApply` | bool | Auto-pilot: apply `defaultAction` without asking |
| DefaultAction | `defaultAction` | string | Action applied by the auto-pilot |
| DefaultLabel | `defaultLabel` | string | Label used when the action is `label` |
| EmailCount | `emailCount` | int | Sample size at analysis time |
| CreatedAt | `createdAt` | time.Time | Creation |
| UpdatedAt | `updatedAt` | time.Time | Last edit |

The auto-pilot lookup in `analysis.go` is
`{userId, senderEmail: email.From, autoApply: true}`, so it matches on the raw
`From` header, exactly the value the senders aggregation groups on.

### `smart_labels` (`models.SmartLabel`)

| Go field | bson tag | Go type | Meaning |
|---|---|---|---|
| ID | `_id,omitempty` | string | ObjectID hex |
| UserID | `userId` | string | Owner email |
| Name | `name` | string | Label name, e.g. `E-commerce` |
| GmailLabelID | `gmailLabelId` | string | Matching Gmail label id |
| Description | `description` | string | What the label represents |
| Keywords | `keywords` | []string | Associated keywords for consistency |
| EmailCount | `emailCount` | int | Emails carrying the label |
| CreatedAt | `createdAt` | time.Time | Creation |
| UpdatedAt | `updatedAt` | time.Time | Last edit |

Inserted as a marshalled struct after the Gmail label is created. Read by
`{userId}` for the catalog and by `{userId, name}` to resolve `labelId` on a
suggestion. `GET` and `POST /api/smart-labels` have no UI, per `CLAUDE.md`.

### `analysis_jobs` (`models.AnalysisJob`)

| Go field | bson tag | Go type | Meaning |
|---|---|---|---|
| ID | `_id,omitempty` | string | ObjectID hex, returned to the client as `jobId` |
| UserID | `userId` | string | Owner email |
| Status | `status` | string | `queued`, `running`, `done`, `error` |
| Total | `total` | int | Emails in the batch |
| Processed | `processed` | int | Emails handled so far |
| AutoApplied | `autoApplied` | int | Resolved by sender auto-pilot |
| SuggestionsCreated | `suggestionsCreated` | int | Rows written to `ai_suggestions` |
| CachedHits | `cachedHits` | int | Resolved from `analysis_cache` |
| Error | `error,omitempty` | string | Failure message when status is `error` |
| EmailIDs | `emailIds` | []string | Input Gmail ids. `json:"-"`, never sent to the client |
| CreatedAt | `createdAt` | time.Time | Enqueue time |
| UpdatedAt | `updatedAt` | time.Time | Last progress write |

Inserted as a marshalled struct, then updated with `bson.M` `$set` documents
whose keys are the bson tags above. Batch size is capped at
`analysisJobCap = 500`.

### `analysis_cache` (`models.AnalysisCacheEntry`)

| Go field | bson tag | Go type | Meaning |
|---|---|---|---|
| ID | `_id,omitempty` | string | ObjectID hex |
| Key | `key` | string | `sha256(lower(trim(from)) + "|" + lower(trim(subject)))`, hex |
| Action | `action` | string | Cached verdict |
| LabelName | `labelName` | string | Cached label |
| Confidence | `confidence` | float64 | Cached confidence |
| Reasoning | `reasoning` | string | Cached explanation |
| CreatedAt | `createdAt` | time.Time | Refreshed on every store |

This is the only struct in `models.go` with no `json` tags at all: it never
crosses the HTTP boundary. It is also the only user-agnostic per-content
collection, deliberately shared across all users. Never put anything
user-specific in it.

### `usage` (no Go struct)

There is no struct in `models.go` for this collection. The shape is defined by
the two operations in `backend/internal/api/account.go` and by an anonymous
decode struct.

| bson field | Type | Meaning |
|---|---|---|
| `_id` | ObjectID | Generated by the upsert |
| `userId` | string | Owner email |
| `period` | string | `time.Now().UTC().Format("2006-01")`, e.g. `2026-08` |
| `analyzed` | int | `$inc` counter of AI-analyzed emails this month |
| `updatedAt` | time.Time | `$set` on every increment |

Read: `FindOne({userId, period})` decoding only `analyzed`. Write:
`UpdateOne({userId, period}, {$inc: {analyzed: n}, $set: {updatedAt: now}}, upsert)`.
The free monthly limit is `FreeMonthlyLimit = 200`; `pro` is unlimited. Cache
hits and sender auto-pilot do not increment it.

### `unsubscribes` (`models.Unsubscribe`)

| Go field | bson tag | Go type | Meaning |
|---|---|---|---|
| ID | `_id,omitempty` | string | ObjectID hex |
| UserID | `userId` | string | Owner email |
| SenderEmail | `senderEmail` | string | Bare address from `extractSenderAddress`, see pitfalls |
| SenderName | `senderName` | string | Display name |
| Method | `method` | string | `one-click`, `browser`, `mailto` |
| Status | `status` | string | `done`, `opened` |
| CreatedAt | `createdAt` | time.Time | Creation |
| UpdatedAt | `updatedAt` | time.Time | Last attempt |

Upsert key `{userId, senderEmail}`, backed by a unique index, which is what makes
the operation idempotent.

### `waitlist` (`models.WaitlistEntry`)

| Go field | bson tag | Go type | Meaning |
|---|---|---|---|
| ID | `_id,omitempty` | string | ObjectID hex |
| Email | `email` | string | Lowercased, trimmed, unique |
| UserID | `userId,omitempty` | string | Present only when the signup came from a session |
| Source | `source` | string | Which surface produced the signup, defaults to `pricing` |
| Plan | `plan` | string | Always `pro` today (`waitlistPlan = PlanPro`) |
| CreatedAt | `createdAt` | time.Time | First signup, `$setOnInsert` |

The only collection not scoped by `userId` on purpose: a pre-launch waitlist has
to accept logged-out visitors. `userId` presence is the flag that separates a
cold visitor from an existing user.

## Indexes actually created

### At boot, by `EnsureIndexes` in `database.go`

Best-effort: a failure on one index does not block the others, and `main.go`
only logs a warning. This is the real source of truth for indexes.

| Collection | Keys | Options |
|---|---|---|
| `emails` | `{userId: 1, messageId: 1}` | none |
| `emails` | `{userId: 1, from: 1}` | none |
| `ai_suggestions` | `{userId: 1, status: 1}` | none |
| `sender_preferences` | `{userId: 1, senderEmail: 1}` | none |
| `analysis_cache` | `{key: 1}` | unique |
| `analysis_jobs` | `{userId: 1, createdAt: -1}` | none |
| `usage` | `{userId: 1, period: 1}` | unique |
| `unsubscribes` | `{userId: 1, senderEmail: 1}` | unique |
| `users` | `{stripeSubscriptionId: 1}` | sparse |
| `sorting_rules` | `{userId: 1, priority: 1}` | none |
| `protected_senders` | `{userId: 1, value: 1}` | unique |
| `snoozes` | `{status: 1, wakeAt: 1}` | none |
| `snoozes` | `{userId: 1, status: 1}` | none |
| `action_log` | `{userId: 1, createdAt: -1}` | none |
| `waitlist` | `{email: 1}` | unique |

Collections with no index from `EnsureIndexes`: `labels`, `gmail_config`,
`smart_labels`, and `users` except for the sparse Stripe one.

### On a fresh volume only, by `mongo-init/init-db.js`

Mongo runs `/docker-entrypoint-initdb.d` scripts only when the data directory is
empty. The script creates 4 collections and 7 indexes that `EnsureIndexes` does
not:

| Collection | Keys | Options |
|---|---|---|
| `emails` | `{messageId: 1}` | unique, GLOBAL not per user |
| `emails` | `{userId: 1, receivedDate: -1}` | none |
| `emails` | `{userId: 1, labelIds: 1}` | none |
| `sorting_rules` | `{userId: 1, priority: 1}` | none, duplicate of the boot one |
| `sorting_rules` | `{userId: 1, enabled: 1}` | none |
| `labels` | `{userId: 1, name: 1}` | unique |
| `users` | `{email: 1}` | unique |

### Mongo service

`docker-compose.yml`: image `mongo:7.0`, named volume `mongodb_data:/data/db`,
`./mongo-init` mounted into `/docker-entrypoint-initdb.d`, healthcheck
`db.adminCommand('ping')`. No host port is published: the database is only
reachable from the compose network. Credentials come from `MONGO_ROOT_USERNAME`
and `MONGO_ROOT_PASSWORD` and are composed into
`MONGODB_URI=mongodb://user:pass@mongodb:27017/mailsorter?authSource=admin` for
the backend service. `dev-start.sh` runs a throwaway `mailsorter-mongodb-dev`
container that DOES publish 27017 with hardcoded `admin` / `password`.

## GDPR catalog

`account.Datasets()` in `backend/internal/account/account.go` is the canonical
list of user-owned data; `datasetCollection` in
`backend/internal/api/account_data.go` is the only bridge from that list to a
Mongo collection. Export and erasure both walk it, so a collection missing from
either one silently escapes both.

| `account.Dataset` constant | Value (export JSON key) | Collection |
|---|---|---|
| DatasetRules | `rules` | `sorting_rules` |
| DatasetProtectedSenders | `protectedSenders` | `protected_senders` |
| DatasetSnoozes | `snoozes` | `snoozes` |
| DatasetSuggestions | `suggestions` | `ai_suggestions` |
| DatasetSenderPrefs | `senderPreferences` | `sender_preferences` |
| DatasetSmartLabels | `smartLabels` | `smart_labels` |
| DatasetUnsubscribes | `unsubscribes` | `unsubscribes` |
| DatasetUsage | `usage` | `usage` |
| DatasetActionLog | `actionLog` | `action_log` |
| DatasetJobs | `analysisJobs` | `analysis_jobs` |

Both operations filter on `bson.M{"userId": userEmail}`. `users` is handled
separately: exported through `account.RedactUser` (which strips the OAuth tokens
and the Stripe ids) and deleted with `DeleteMany({email: userEmail})`.

## Canonical query shapes

Copy these rather than inventing filters.

| Intent | Filter |
|---|---|
| The caller's account | `bson.M{"email": userEmail}` on `users` |
| Anything else owned by the caller | `bson.M{"userId": userEmail}` |
| One document by id | `oid, err := primitive.ObjectIDFromHex(s); bson.M{"_id": oid, "userId": userEmail}` |
| A stored email | `bson.M{"userId": userEmail, "messageId": gmailID}` |
| Pending suggestions | `bson.M{"userId": userEmail, "status": "pending"}` |
| Due snoozes | `bson.M{"status": "scheduled", "wakeAt": bson.M{"$lte": time.Now()}}` |
| This month's quota row | `bson.M{"userId": userEmail, "period": time.Now().UTC().Format("2006-01")}` |
| Cache probe | `bson.M{"key": analysisCacheKey(from, subject)}` |
| Opted-in users for a background loop | `bson.M{"autoSyncEnabled": true}` or `bson.M{"digestEnabled": true}` |

## Known pitfalls

Each of these will make a plausible-looking query silently return nothing, or a
write silently do nothing.

**`users` is keyed by `email`, everything else by `userId`.** Both hold the same
value, the user's email address. `db.Users().Find({userId: ...})` matches zero
documents. There is no account entity and no numeric id anywhere.

**`plan` is absent for free users.** `Plan` carries `omitempty` and only the
Stripe webhook ever writes it. A free account has no `plan` field at all, so
`{plan: "free"}` matches nobody. `getPlan` in `billing.go` maps the empty value
to `PlanFree` in Go, not in Mongo. Query `{plan: {$ne: "pro"}}` instead.

**Boolean settings are absent until first written.** `autoApplyRules`,
`autoSyncEnabled` and `digestEnabled` all carry `omitempty`, and a fresh user
document created by the OAuth callback has none of them. `{digestEnabled: true}`
is correct and is what the scheduler uses; `{digestEnabled: false}` misses every
user who never touched the setting.

**`sender_preferences.senderEmail` holds a raw `From` header, `unsubscribes.senderEmail` holds a bare address.** The senders aggregation groups on `$from`,
so a preference is stored under `Shop <news@shop.com>`. The unsubscribe flow runs
`extractSenderAddress` first, so it stores `news@shop.com`. The same column name
in two collections means two different things. Joining them on value will not
work.

**`protected_senders.value` is normalized.** It goes through
`protect.NormalizeEntry`, which also decides `kind`. Query with the normalized
form, not with what a user typed.

**`ai_suggestions.emailId` is a Gmail message id, not a reference into `emails`.**
Same for `action_log.messageId` and `snoozes.messageId`. Nothing in this schema
uses a Mongo `_id` as a foreign key.

**Writes are `bson.M` documents, so a renamed Go field will not break a build.**
Handlers set fields by literal bson key (`"digestHourUTC"`, `"appliedCount"`,
`"cachedHits"`). Renaming a bson tag in `models.go` leaves those writes pointing
at the old key and the compiler stays silent. Grep the string, not the field.

**`AnalyzeSender` never persists its preference.** `ai_handlers.go` builds a
`models.SenderPreference` whose `CreatedAt` is the zero time and passes it as
`$set` while also passing `$setOnInsert: {createdAt: time.Now()}`. `CreatedAt`
has no `omitempty`, so `createdAt` appears in both operators, which MongoDB
rejects as a conflicting path. The `UpdateOne` return value is discarded, so the
failure is invisible. Deduced from reading `ai_handlers.go` lines 164 to 185 plus
the struct tags, not reproduced against a live database. Consequence: do not
expect a row in `sender_preferences` after `POST /api/ai/analyze-sender`. The
rows that do exist come from `PUT /api/senders/{id}/preferences`, which uses a
plain `$set`.

**`emails.createdAt` means "last synced", not "first seen".** The sync upserts
`$set: email` with the whole struct, and `CreatedAt` has no `omitempty`, so every
sync rewrites it to `time.Now()`. Use `receivedDate` for anything chronological.

**Two different index sets exist in the wild.** `mongo-init/init-db.js` only runs
on an empty data directory. A database created that way has a GLOBALLY unique
`emails.messageId`, a unique `labels.{userId,name}`, a unique `users.email` and
two extra `emails` indexes. A database created any other way (existing volume,
managed Mongo, a restored dump) has only what `EnsureIndexes` builds, meaning
`users.email` is neither unique nor indexed. Never assume uniqueness you did not
read from `EnsureIndexes`.

**`labels` is dead storage.** The accessor and the struct exist, `init-db.js`
even indexes it, and nothing reads or writes it. `GET /api/labels` goes straight
to the Gmail API. Do not build on this collection without wiring it first.

**`analysis_cache` is global.** Keyed on sender plus subject only, shared by every
user. Anything user-specific written through it leaks across accounts.

**`snoozes` deduplication is not enforced by an index.** Both snooze indexes are
non-unique; uniqueness comes from the upsert filter
`{userId, messageId, status: "scheduled"}`. A direct insert bypasses it.

**Account deletion does not touch `emails`.** `account.Datasets()` covers ten
collections; `emails` is not one of them, and neither are `labels`, `waitlist`,
`analysis_cache` or `gmail_config`. `DeleteAccount` therefore leaves the user's
synced subjects and bodies in `emails`. `waitlist` and `analysis_cache` are
excluded by design (not user-scoped); `emails` reads as an omission. Verified by
reading `account.go` and `account_data.go`, not by running the endpoint.

**`usage` has no Go struct.** Anything you write about its shape must come from
`account.go`. Do not add a `models.Usage` from memory.

## Key Files

| File | Description |
|---|---|
| `backend/internal/models/models.go` | Every BSON struct, 449 lines, one file |
| `backend/internal/database/database.go` | The 16 accessors and `EnsureIndexes` |
| `backend/internal/account/account.go` | Pure GDPR dataset catalog |
| `backend/internal/api/account_data.go` | Dataset to collection bridge, export and erasure |
| `backend/internal/api/handlers.go` | User upsert on OAuth callback, email upsert on sync |
| `backend/internal/api/analysis.go` | Cache key, cache read and write, suggestion insert |
| `backend/internal/api/ai_handlers.go` | Suggestions, sender preferences, smart labels, senders aggregation |
| `backend/internal/api/account.go` | `usage` read and increment, user settings merge |
| `backend/internal/api/ledger.go` | `logAction` and the `Source*` vocabulary |
| `backend/internal/api/rules.go` | Rules CRUD and `appliedCount` increments |
| `backend/internal/api/snooze.go` | Snooze upsert and the due sweeper query |
| `backend/internal/api/protected.go` | Protected sender upsert and lookup |
| `backend/internal/api/unsubscribe.go` | Unsubscribe upsert, subscriptions aggregation |
| `backend/internal/api/jobs.go` | Analysis job insert and progress updates |
| `backend/internal/api/waitlist.go` | Waitlist upsert and email normalization |
| `backend/internal/api/billing.go` | `setPlan` and the Stripe identifiers on `users` |
| `backend/cmd/server/main.go` | `EnsureIndexes` call, legacy `gmail_config` fallback |
| `mongo-init/init-db.js` | Fresh-volume seed, 4 collections and 7 indexes |
| `docker-compose.yml` | Mongo service, volume, credentials to `MONGODB_URI` |

## References

### Source Files

- `backend/internal/models/models.go` : all struct definitions and bson tags quoted above
- `backend/internal/database/database.go` : the only place a collection name is spelled
- `backend/internal/account/account.go` : `Dataset` constants and `RedactUser`
- `backend/internal/api/account_data.go` : `datasetCollection`, export and delete walks
- `backend/cmd/server/main.go` : boot order, index creation, credential fallback
- `mongo-init/init-db.js` : the fresh-volume index set that diverges from `EnsureIndexes`
- `docker-compose.yml` : Mongo service definition and URI composition

### Related Context Docs

- [gmail-sync.md](gmail-sync.md) : how `emails` gets filled and what the sync upsert does
- [ai-triage.md](ai-triage.md) : `ai_suggestions`, `analysis_cache`, `analysis_jobs`, `sender_preferences` in motion
- [rules-engine.md](rules-engine.md) : the `sorting_rules` shape from the decision side
- [billing-quota.md](billing-quota.md) : `users.plan`, the Stripe fields and the `usage` counter

### Superseded

- `docs/ARCHITECTURE.md`, section "Composants" : lists 4 collections and predates most of
  the schema. Stale for this topic. Its Security, Observability and Background sections
  remain current.
