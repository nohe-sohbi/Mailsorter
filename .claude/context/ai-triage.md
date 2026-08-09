---
subsystem: ai-triage
description: Mistral-backed email triage: synchronous and async analysis, the job queue, the shared analysis cache, the suggestion lifecycle, sender auto-pilot, smart labels and the monthly quota that bounds the billed API.
keywords: [mistral, ai triage, analyze emails, ai suggestion, analysis cache, analysis job, sender preference, auto-pilot, smart labels, quota, apply batch, apply bulk, analyze-async, cache key, ai quota]
files:
  - backend/internal/ai/mistral.go
  - backend/internal/ai/mistral_test.go
  - backend/internal/api/analysis.go
  - backend/internal/api/analysis_test.go
  - backend/internal/api/ai_handlers.go
  - backend/internal/api/jobs.go
  - backend/internal/api/account.go
  - backend/internal/models/models.go
  - backend/internal/api/routes.go
priority: high
related: [rules-engine, billing-quota, data-model, frontend-inbox]
last-verified: 2026-08-09
---

# AI Triage (Mistral)

The AI layer turns a list of Gmail message IDs into `pending` suggestions the user
validates. It is the only place in Mailsorter that spends money per request, so
almost everything in this subsystem exists to *avoid* calling the model: sender
auto-pilot short-circuits first, a globally shared analysis cache second, and only
the remainder is batched 8 at a time into a Mistral chat completion.

Read `CLAUDE.md` for the package map and the pure-core rule. This doc is the terrain
underneath it: where the billed calls actually happen, what bounds them, and where
the bounds leak.

## Every call to Mistral, exhaustively

Three call sites reach `internal/ai` from `internal/api`. There are no others
(`grep -rn "AnalyzeBatch\|AnalyzeEmail(\|AnalyzeSender(" backend/ --include=*.go`).

| Call site | AI method | Cost shape | Quota checked | Cached |
|---|---|---|---|---|
| `analysis.go:120` (pass 2, per chunk) | `AnalyzeBatch(chunk, labels)` | 1 request per 8 emails | yes, before the run | yes |
| `analysis.go:132` (per email, batch fallback) | `AnalyzeEmail(email, labels)` | 1 request per email | yes, before the run | yes |
| `ai_handlers.go:155` (`AnalyzeSender` handler) | `AnalyzeSender(sender, emails, labels)` | 1 request per HTTP call | **no** | **no** |

`h.aiClient` is `nil` when `MISTRAL_API_KEY` is empty (`cmd/server/main.go:97-104`).
`AnalyzeEmails`, `AnalyzeSender` and `EnqueueAnalyze` answer 503 in that case;
`runAnalysis` degrades silently (it counts the email as processed and produces no
suggestion).

## Mistral client (`internal/ai/mistral.go`)

Hand-rolled HTTP against `https://api.mistral.ai/v1/chat/completions`. No SDK.

| Knob | Value | Set in |
|---|---|---|
| HTTP client timeout | 30 s | `NewMistralClient` |
| `temperature` | 0.3 | `chatTokens` |
| `max_tokens`, single-email prompts | 500 | `chat` |
| `max_tokens`, batch prompt | `120*n + 200`, capped at 4000 | `AnalyzeBatch` |
| `maxRetries` (extra attempts after the first) | 2, overridable by `MISTRAL_MAX_RETRIES`, clamped at >= 0 | `NewMistralClient`, `SetMaxRetries` |
| `baseDelay` / `maxDelay` | 500 ms / 8 s | `NewMistralClient` |
| Snippet truncation | 200 chars single, 160 chars batch | `AnalyzeEmail`, `AnalyzeBatch` |

Retry policy (`chatTokens` + `doChat` + `backoff`):

- Retryable: HTTP 429, any 5xx, transport errors (timeout, reset).
- Fail fast: every other 4xx, and any JSON decode failure on a 200.
- Wait = `baseDelay << attempt`, then full jitter (random in `[d/2, d]`), floored at
  the server's `Retry-After`, capped at `maxDelay`.
- `parseRetryAfter` only understands delta-seconds. Non-numeric or `<= 0` yields 0,
  so an HTTP-date `Retry-After` is ignored rather than mis-parsed.

`sleep` and `baseURL` are unexported fields so `mistral_test.go` can drive the retry
matrix against an `httptest` server with zero real waiting.

### The three prompts

All prompts are French, ask for JSON only, and are built inline in `mistral.go`.
Existing smart-label names are injected as context so the model reuses them.

| Method | Shape asked for | Extraction fallback | Normalization |
|---|---|---|---|
| `AnalyzeEmail` | one JSON object | substring between first `{` and last `}` | action lowercased, unknown to `keep`, confidence clamped to `[0,1]` |
| `AnalyzeBatch` | JSON array, same order as input | substring between first `[` and last `]` | same as above, per element |
| `AnalyzeSender` | one JSON object | substring between first `{` and last `}` | **none** |

`AnalyzeBatch` errors out (so the caller can fall back per email) when there is no
array in the response, when the array does not parse, or when it returns fewer
analyses than emails. A longer array is truncated with `results[:len(emails)]`.

`EmailAnalysis.Action` vocabulary: `archive`, `delete`, `label`, `keep`.
`SenderAnalysis` adds `sender_type`: `commercial`, `personal`, `work`, `newsletter`,
`transactional`.

## Key files

| File | Responsibility |
|---|---|
| `backend/internal/ai/mistral.go` | The Mistral client: prompts, retry/backoff, response normalization |
| `backend/internal/ai/mistral_test.go` | Retry matrix (429 then 200, 500 exhausted, 400 not retried), `parseRetryAfter`, backoff cap |
| `backend/internal/api/analysis.go` | `runAnalysis`, the shared engine: auto-pilot, cache, batching, protection, quota charge |
| `backend/internal/api/analysis_test.go` | Cache-key determinism and normalization, `localMatchLabel` |
| `backend/internal/api/ai_handlers.go` | The nine `/api/ai/*` handlers, `/api/senders/*`, `autoApplySender`, `ensureLabel`, `getUserToken` |
| `backend/internal/api/jobs.go` | Async job queue, 3 workers, `EnqueueAnalyze`, `GetJob` |
| `backend/internal/api/account.go` | `FreeMonthlyLimit`, `getUsage`, `incrUsage`, `quotaExceeded`, `GET /api/usage` |
| `backend/internal/api/routes.go` | Route table (52 registrations); the AI block is lines 71-89 |
| `backend/internal/models/models.go` | `AISuggestion`, `SenderPreference`, `SmartLabel`, `AnalysisJob`, `AnalysisCacheEntry` and the AI request bodies |
| `frontend/src/services/api.js` | `aiService` and `senderService`: the client mirror of every route below |
| `frontend/src/pages/Inbox.js` | Sync/async choice, job polling, apply and reject UX |

## Routes

| Method | Route | Handler | Notes |
|---|---|---|---|
| POST | `/api/ai/analyze` | `AnalyzeEmails` | Synchronous, 90 s context |
| POST | `/api/ai/analyze-async` | `EnqueueAnalyze` | 202 with `{jobId, status}` |
| GET | `/api/ai/jobs/{id}` | `GetJob` | Scoped by `userId`, 404 otherwise |
| POST | `/api/ai/analyze-sender` | `AnalyzeSender` | Billed, unmetered, uncached |
| POST | `/api/ai/apply` | `ApplySuggestion` | One suggestion |
| POST | `/api/ai/apply-batch` | `ApplyBatch` | N suggestions, one token refresh |
| POST | `/api/ai/apply-bulk` | `ApplyBulk` | All stored emails from one sender |
| GET | `/api/ai/suggestions` | `GetSuggestions` | `?status=` default `pending`, sort `createdAt` desc, limit 100 |
| POST | `/api/ai/suggestions/{id}/reject` | `RejectSuggestion` | 204 No Content |
| GET | `/api/senders` | `GetSenders` | Aggregation over `emails`, top 50 by count |
| POST | `/api/senders/rule` | `CreateSenderRule` | Lives in `rules.go`, promotes a sender to a deterministic rule |
| PUT | `/api/senders/{id}/preferences` | `UpdateSenderPreference` | Keyed by preference `_id` |
| GET/POST | `/api/smart-labels` | `GetSmartLabels`, `CreateSmartLabel` | No frontend caller (`grep -rn "smart-labels" frontend/src` is empty) |

## The shared engine: `runAnalysis`

One function serves both the sync handler and the async worker
(`analysis.go:34`). Signature: `(ctx, userEmail, emailIDs, onProgress) -> (progress, []AISuggestion, error)`.

Setup: load smart-label names, load the protected (VIP) list, resolve each
`emailID` to a stored `models.Email` via `{messageId, userId}`. Unresolvable IDs are
dropped without error, so `progress.Total` is the resolved count, not the requested one.
A best-effort Gmail client is built from `getUserToken`; on error it stays `nil`.

Pass 1, per email, cheapest first:

1. **Sender auto-pilot** (needs a Gmail client). Look up `sender_preferences` on
   `{userId, senderEmail: email.From, autoApply: true}`. If a `defaultAction` exists,
   `protect.Allowed` clears it, and `autoApplySender` succeeds, then `AutoApplied++`
   and the email never reaches the model.
2. **Cache lookup** on `analysisCacheKey(from, subject)`. A hit is passed through
   `protectAnalysis`, persisted as a suggestion, counted as `CachedHits++`.
3. Otherwise the email goes into `pending`.

Pass 2, cache misses only, in chunks of `analysisBatchSize = 8`:

`ctx.Err() -> break` at the top of each chunk. `AnalyzeBatch(chunk)`; on any error
each email of the chunk falls back to a single `AnalyzeEmail`. A failed single call
increments `Processed` and produces nothing. Every email actually served by the model
increments `Analyzed`, is written to the cache **raw**, then downgraded by
`protectAnalysis` before the suggestion is persisted.

Finally, `h.incrUsage(ctx, userEmail, p.Analyzed)` charges the month.

Progress counters (`analysisProgress`, mirrored into `analysis_jobs`):

| Field | Meaning |
|---|---|
| `Total` | Emails resolved from Mongo |
| `Processed` | Emails whose fate is settled (includes failures) |
| `AutoApplied` | Settled by sender auto-pilot, no model call |
| `CachedHits` | Settled from `analysis_cache`, no model call |
| `Analyzed` | Emails that hit Mistral. This is the only quota-bearing counter |
| `SuggestionsCreated` | Rows inserted into `ai_suggestions` |

## Sync path vs async path

| Aspect | `POST /api/ai/analyze` | `POST /api/ai/analyze-async` |
|---|---|---|
| Guard: no AI client | 503 | 503 |
| Guard: no `X-User-Email` | 401 | 401 |
| Guard: empty `emailIds` | 400 | 400 |
| Quota pre-check | `quotaExceeded` -> 402 | `quotaExceeded` -> 402 |
| Input cap | none | `analysisJobCap = 500`, silently truncated |
| Context budget | 90 s | 10 s to enqueue, 10 min inside the worker |
| Execution | inline, blocks the request | `analysis_jobs` row + `h.jobQueue` |
| Progress | none (`onProgress` is nil) | job document updated after **every** email |
| Response | `{suggestions, autoApplied, cachedHits}` | 202 `{jobId, status:"queued"}` |
| Client follow-up | none | poll `GET /api/ai/jobs/{id}` |

Queue mechanics: `h.jobQueue` is `make(chan string, 256)` and `startAnalysisWorkers(3)`
runs in `NewHandler` (`handlers.go:80-85`). `EnqueueAnalyze` does a non-blocking send;
if the buffer is full it launches `go h.processAnalysisJob(jobID)` directly, so
saturation costs unbounded goroutines rather than dropped jobs.

Job status vocabulary: `queued` -> `running` -> `done` | `error`. On error the message
is stored in the job document and logged as `analysis job %s failed: %v`.

Frontend split (`Inbox.js`): `ASYNC_THRESHOLD = 10`. Ten or fewer selected emails go
through the sync endpoint; more go async and are polled every 1500 ms (2500 ms after a
polling error). A 402 becomes an action toast pointing at `/pricing`.

## Analysis cache

| Property | Value |
|---|---|
| Collection | `analysis_cache` |
| Key | `hex(sha256(lower(trim(from)) + "\|" + lower(trim(subject))))` |
| Index | unique on `key` (`EnsureIndexes`) |
| Scope | **global, not per user** |
| Fields | `key`, `action`, `labelName`, `confidence`, `reasoning`, `createdAt` |
| Write | upsert on every model verdict, before VIP downgrading |
| Expiry | none. No TTL index exists |
| Read | `cacheLookup`, any decode error is treated as a miss |

Sharing semantics are deliberate: the same newsletter blast hitting a thousand inboxes
is analyzed once. The consequence is that the cached verdict was produced with the
*first* user's smart-label list in the prompt, and it is replayed verbatim for
everyone else. Never put user-specific data behind this key.

`analysis_test.go` pins the key contract: deterministic, case and whitespace
insensitive, and different subjects must not collide.

## Suggestion lifecycle

`models.AISuggestion` statuses: `pending`, `applied`, `rejected`. Collection
`ai_suggestions`, index `(userId, status)`.

| Transition | Trigger | Side effects |
|---|---|---|
| created `pending` | `persistSuggestion` from a model verdict or a cache hit | label resolved via `localMatchLabel`, `labelId` filled from `smart_labels` if known |
| created `applied` | `autoApplySender` (auto-pilot) | Gmail mutated immediately, `confidence: 1.0`, reasoning `Auto-appliqué (préférence expéditeur)`, ledger `SourceAIAuto` |
| `pending` -> `applied` | `ApplySuggestion` or `ApplyBatch` | Gmail mutated, `appliedAt` + `labelId` set, ledger `SourceAI` |
| `pending` -> `rejected` | `RejectSuggestion` | status only, no Gmail call, 204 |
| (no suggestion) | `ApplyBulk` | acts straight on `emails`, ledger `SourceBulk`, `ai_suggestions` untouched |

`localMatchLabel` (`analysis.go:218`) maps a suggested label onto an existing one with
no extra AI call: case-insensitive exact match, or containment in either direction,
else the suggestion passes through unchanged.

Action to Gmail mapping, identical in `autoApplySender`, `ApplySuggestion`,
`ApplyBatch` and `ApplyBulk`:

| Action | Gmail call |
|---|---|
| `archive` | `ModifyMessage(id, nil, ["INBOX"])` |
| `delete` | `ModifyMessage(id, ["TRASH"], nil)` (trash, never a hard delete) |
| `label` | `ensureLabel` then `ModifyMessage(id, [labelID], nil)` |
| `keep` | nothing |

### Apply paths compared

| | `/api/ai/apply` | `/api/ai/apply-batch` | `/api/ai/apply-bulk` |
|---|---|---|---|
| Input | one suggestion id | list of suggestion ids | `{senderEmail, action, labelName}` |
| Context timeout | 30 s | 120 s | 120 s |
| Token refresh | once | once, reused for the whole loop | once |
| VIP protection | **not checked** | `protectedValues` + `senderOf` per suggestion | `protect.Allowed` per email |
| Per-item failure | 500, whole request fails | counted in `failed`, loop continues | skipped silently |
| Ledger source | `SourceAI` | `SourceAI` | `SourceBulk` |
| Response | `{status:"applied"}` | `{applied, failed, total, appliedIds, protectedSkipped}` | `{applied, total, protectedSkipped}` |

## VIP protection inside the AI path

`protect.Allowed(action, from, entries)` returns true for any non-destructive action;
`archive`, `trash` and `delete` are the destructive set. Two hooks:

- `protectAnalysis` (`analysis.go:168`) downgrades a destructive verdict to
  `action: "keep"`, clears the label, rewrites the reasoning to
  `Expéditeur protégé, conservé en boîte` and floors confidence at 0.9. Applied after
  the cache write, so a protected sender never poisons the shared cache.
- Auto-pilot consults `allows(pref.DefaultAction, email.From, protectedList)` before
  firing, so a VIP falls through to a non-destructive suggestion instead.

## Sender analysis and auto-pilot

`POST /api/ai/analyze-sender` fetches up to 20 stored emails matching
`{userId, from: /QuoteMeta(sender)/i}`, sends at most 5 subjects to the model, and
upserts a `sender_preferences` row with the model's `suggested_action` and
`suggested_label`, `autoApply: false`.

Auto-pilot only fires when the user later flips `autoApply` through
`PUT /api/senders/{id}/preferences`. It then bypasses both the model and the quota:
`AutoApplied` is counted, `Analyzed` is not.

## Smart labels

`smart_labels` holds `{userId, name, gmailLabelId, description, keywords, emailCount}`.
`getSmartLabelNames` feeds every prompt so the model reuses the user's vocabulary
instead of inventing near-duplicates. `ensureLabel` is the write path: look up by
`(userId, name)`, and on a miss create the Gmail label and insert the row. In practice
smart labels are created as a side effect of applying `label` suggestions, since the
two `/api/smart-labels` routes have no frontend caller.

## Quota and usage accounting

| Constant / rule | Value | Location |
|---|---|---|
| `FreeMonthlyLimit` | 200 AI-analyzed emails per month | `account.go:19` |
| Period key | `time.Now().UTC().Format("2006-01")` | `currentPeriod` |
| Storage | `usage` collection, unique index `(userId, period)`, `$inc` on `analyzed` | `EnsureIndexes`, `incrUsage` |
| Pro | `users.plan == "pro"` is unlimited; `GET /api/usage` reports `limit: -1` | `getPlan`, `GetUsage` |
| Charged | `progress.Analyzed` only | `runAnalysis` tail |
| Not charged | cache hits, sender auto-pilot, `keep` verdicts already cached, `AnalyzeSender` | by construction |

Enforcement points, and only these two:

| Where | Effect |
|---|---|
| `AnalyzeEmails` (before `runAnalysis`) | 402 `Quota mensuel atteint. Passez à Pro pour continuer.` |
| `EnqueueAnalyze` (before inserting the job) | same 402 |

`processAnalysisJob` does not re-check. `AnalyzeSender` never checks.

## Environment

| Var | Default | Effect |
|---|---|---|
| `MISTRAL_API_KEY` | empty | Empty leaves `h.aiClient == nil`: analyze routes answer 503 and `runAnalysis` produces no suggestions |
| `MISTRAL_MODEL` | `mistral-large-2411` | Sent as `model` on every request |
| `MISTRAL_MAX_RETRIES` | 2 | Extra attempts after the first, so 3 total by default |

Documented in `.env.example:33-38`, mapped in `docker-compose.yml:31-40`, read in
`config.go:58-60`.

## Known pitfalls

- **`AnalyzeSender` is a billed call with no quota check and no cache.** Every
  `POST /api/ai/analyze-sender` is one Mistral request regardless of the user's usage,
  and it is the only AI entry point that is neither metered nor memoized.
- **The async worker never re-checks the quota.** `quotaExceeded` runs once in
  `EnqueueAnalyze`; the job then analyzes up to 500 emails and charges usage only at
  the end of `runAnalysis`. A free user at 199/200 can spend 500 analyses in one job,
  and two jobs enqueued back to back both pass the pre-check.
- **A failed batch multiplies cost by 8.** When `AnalyzeBatch` cannot be aligned, every
  email of the chunk falls back to its own `AnalyzeEmail` call, each with its own retry
  budget. A systematic parse failure turns one request into eight.
- **`analysis_cache` is global and never expires.** Keyed on `(from, subject)` only,
  with no TTL index. A verdict computed with user A's smart labels is replayed for
  user B, label name included. Never cache anything user-specific through it.
- **Single apply skips VIP protection.** `ApplyBatch` and `ApplyBulk` consult
  `protectedValues`; `ApplySuggestion` does not. A destructive suggestion created
  before the sender was protected can still be applied one at a time.
- **Re-analyzing a sender resets its auto-pilot.** `AnalyzeSender` upserts with
  `$set: senderPref`, and `senderPref.AutoApply` is hardcoded `false`, so a second
  analysis silently disables an auto-apply the user had enabled.
- **That same upsert writes `createdAt` in both `$set` and `$setOnInsert`.**
  `SenderPreference.CreatedAt` has no `omitempty`, so the full-struct `$set` carries a
  zero `createdAt` while `$setOnInsert` sets it too, and the `UpdateOne` return values
  are discarded. Verify the row actually landed before trusting it.
- **`EmailCount` on a sender preference is a sample, not a total.** It is
  `len(emails)` after a `SetLimit(20)` on the query, so it saturates at 20.
- **Unresolvable email IDs vanish silently.** `runAnalysis` keeps only IDs that match a
  stored email; a client asking for 50 can get `total: 12` with no error and no signal.
- **A revoked Google grant silently disables auto-pilot.** `getUserToken`'s error is
  swallowed in `runAnalysis`, `gmailClient` stays nil, pass 1 skips auto-apply
  entirely, and the model still runs and still charges quota.
- **Suggestions are never deduplicated.** `persistSuggestion` always inserts, and the
  index on `ai_suggestions` is `(userId, status)`, not unique on `emailId`. Analyzing
  the same email twice leaves two pending rows for it.
- **A timed-out sync run can lose its quota charge.** `runAnalysis` breaks the batch
  loop on `ctx.Err()`, then calls `incrUsage` with that same dead context and discards
  the write error. Emails already sent to Mistral are billed by Mistral and not counted
  locally.
- **Over-500 async requests are truncated in silence.** `EnqueueAnalyze` slices
  `req.EmailIDs` to `analysisJobCap` and returns a normal 202; the client sees a
  smaller `total` and no warning.
- **`AnalyzeSender` output is not normalized.** Unlike `AnalyzeEmail` and
  `AnalyzeBatch`, its action is not lowercased or validated and its confidence is not
  clamped. Whatever the model returns is stored as `defaultAction`, and
  `autoApplySender`'s switch returns false for anything outside the four verbs.
- **These handlers use `http.Error`, not `writeError`.** `ai_handlers.go` and `jobs.go`
  predate the convention in `CLAUDE.md`, so their error bodies are plain text, not JSON.
  Match the surrounding style when editing them, or convert the whole file at once.

## Testing

Static reading and unit tests only; the suite never contacts Mistral.

In `ai/mistral_test.go`:

| Test | Covers |
|---|---|
| `TestChatRetriesOn429ThenSucceeds` | 429, 429, 200 with 3 attempts |
| `TestChatExhaustsRetriesOn500` | 1 + 2 attempts then failure |
| `TestChatDoesNotRetryClientError` | a 400 costs exactly one attempt |
| `TestParseRetryAfter` | delta-seconds only, negatives and garbage to 0 |
| `TestBackoffHonorsRetryAfterAndCap` | `Retry-After` floor, `maxDelay` ceiling |

In `api/analysis_test.go`: cache-key determinism and normalization, `localMatchLabel`.

`newTestClient` sets `baseURL`, zeroes the delays and stubs `sleep`, which is why
those fields are unexported rather than configurable. Keep them that way.

Manual checks worth running before touching cost logic: `GET /api/usage` before and
after an analysis (only `Analyzed` should move), and a second analysis of the same
selection (expect `cachedHits` to equal the count and usage to stay flat).

## References

### Source files

- `backend/internal/ai/mistral.go` : client, prompts, retry, normalization
- `backend/internal/api/analysis.go` : `runAnalysis`, cache, protection, quota charge
- `backend/internal/api/ai_handlers.go` : `/api/ai/*` and `/api/senders/*` handlers
- `backend/internal/api/jobs.go` : queue, workers, `EnqueueAnalyze`, `GetJob`
- `backend/internal/api/account.go` : `FreeMonthlyLimit`, usage accounting
- `backend/internal/api/routes.go` : AI route registrations (lines 71-89)
- `backend/internal/models/models.go` : AI models (lines 267-384)
- `backend/internal/database/database.go` : collection accessors and `EnsureIndexes`
- `backend/internal/protect/protect.go` : `Allowed`, `IsDestructive`
- `backend/cmd/server/main.go` : client construction, `SetMaxRetries`
- `frontend/src/services/api.js` : `aiService`, `senderService`
- `frontend/src/pages/Inbox.js` : `ASYNC_THRESHOLD`, job polling, apply and reject

### Related context docs

- [rules-engine](rules-engine.md) : the deterministic, AI-free triage that runs before the model
- [billing-quota](billing-quota.md) : plans, Stripe, and what `quotaExceeded` reads
- [data-model](data-model.md) : the collections and indexes behind every table above
- [frontend-inbox](frontend-inbox.md) : how suggestions are surfaced, applied and undone
