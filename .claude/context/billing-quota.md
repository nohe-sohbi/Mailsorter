---
subsystem: billing-quota
description: Stripe checkout, portal and webhook, the free/pro plan split, the monthly AI usage counter that gates analysis, account profile and settings, the RGPD export and erasure, and the Pro waitlist.
keywords: [stripe, billing, checkout, webhook, subscription, plan, pro, free, quota, usage, monetisation, rgpd, gdpr, export, account deletion, waitlist]
files:
  - backend/internal/billing/stripe.go
  - backend/internal/api/billing.go
  - backend/internal/api/account.go
  - backend/internal/api/account_data.go
  - backend/internal/api/waitlist.go
  - backend/internal/api/waitlist_test.go
  - backend/internal/account/account.go
  - backend/internal/account/account_test.go
  - frontend/src/pages/Pricing.js
  - frontend/src/pages/Account.js
  - .env.example
priority: medium
related: [ai-triage, data-model, auth-session-security]
last-verified: 2026-08-09
---
# Billing, Quota and Account Data

Monetisation is one boolean stretched across the app: a `plan` string on the user
record, set to `"pro"` by a Stripe webhook and read by exactly one gate, the monthly
AI analysis quota. Everything else in this subsystem (profile, settings, RGPD export,
erasure, Pro waitlist) hangs off the same user record and the same
`X-User-Email` identity.

## Overview

| Aspect | Reality |
|---|---|
| Plans in code | Two: `PlanFree = "free"`, `PlanPro = "pro"` (`api/billing.go:16-17`) |
| Source of truth for the plan | `users.plan` in Mongo. Empty or anything not `"pro"` reads as free (`getPlan`) |
| What the plan actually changes | One thing: the AI monthly quota. Nothing else is plan-gated |
| Stripe integration | Hand-rolled REST client, no SDK (`internal/billing/stripe.go`, 223 lines) |
| Stripe surface used | Checkout Sessions, Billing Portal Sessions, webhook signature verification |
| Metering unit | Emails that actually hit the Mistral API. Cache hits and sender auto-pilot are free |
| Metering period | Calendar month in UTC, `time.Now().UTC().Format("2006-01")` |
| Billing can be entirely off | Yes. Empty `STRIPE_SECRET_KEY` leaves `billing.Client == nil` and the UI falls back to the waitlist |

## Plans and Limits

| Plan | Stored value | Monthly AI limit | `GET /api/usage` `limit` | Price shown in UI | Where the limit is defined |
|---|---|---|---|---|---|
| Free | `""` or anything not `"pro"` | 200 analyzed emails | `200` | `0 EUR / mois` | `FreeMonthlyLimit = 200`, `api/account.go:19` |
| Pro | `"pro"` | unlimited | `-1` | `7 EUR / mois` | `quotaExceeded` returns false immediately for Pro |

The `7 EUR` figure is a hardcoded string in `frontend/src/pages/Pricing.js:28`. The
backend never reads the amount of the Stripe Price: `STRIPE_PRICE_ID` is passed through
opaquely. The displayed price and the charged price are two unlinked facts.

The Pro feature list in `Pricing.js:33-39` advertises "Tri automatique à chaque synchro",
"Digest quotidien par email" and "Plusieurs comptes Gmail". None of those is gated on the
plan in the backend: auto-apply rules, auto-sync and the daily digest are free settings
for everyone, and multi-account Gmail does not exist at all (there is no account entity,
`userId` is the email address). Treat that list as marketing copy, not as a capability
matrix.

## Routes

Registered in `backend/internal/api/routes.go` (billing block lines 51-54, account block
lines 42-49, waitlist line 93).

| Method | Route | Handler | Auth | Notes |
|---|---|---|---|---|
| GET | `/api/usage` | `GetUsage` | session | `{used, limit, period, plan, billingOn}` |
| GET | `/api/account/profile` | `GetProfile` | session | Redacted profile, same projection as the export |
| GET | `/api/account/settings` | `GetSettings` | session | `models.UserSettings` |
| PUT | `/api/account/settings` | `UpdateSettings` | session | Pointer-field merge, returns the merged state |
| GET | `/api/account/export` | `ExportAccount` | session | RGPD portability, one JSON document |
| DELETE | `/api/account` | `DeleteAccount` | session | RGPD erasure, irreversible |
| POST | `/api/billing/checkout` | `CreateCheckout` | session | `{url}` to redirect to |
| POST | `/api/billing/portal` | `CreatePortal` | session | `{url}` to redirect to |
| POST | `/api/billing/webhook` | `StripeWebhook` | HMAC signature | Public prefix, rate-limit exempt |
| POST | `/api/waitlist` | `JoinWaitlist` | none (optional session) | Public prefix, idempotent upsert |

`/api/billing/webhook` and `/api/waitlist` are in `publicPrefixes`
(`api/middleware.go:29-36`). On a public route `authMiddleware` still sets
`X-User-Email` when a valid bearer token is present, which is how the waitlist tells an
authenticated signup from cold traffic. The webhook is additionally exempt from the
token-bucket rate limiter (`api/middleware.go:235`), on the grounds that Stripe controls
its own delivery rate.

## Quota Mechanics

### The usage document

There is no `Usage` struct in `models.go`. The collection is written with ad-hoc bson
from `api/account.go:33-42`.

| Field | Type | Meaning |
|---|---|---|
| `userId` | string | The user's email address |
| `period` | string | `YYYY-MM` in UTC |
| `analyzed` | int | Emails charged this period, `$inc`-ed |
| `updatedAt` | time | Last increment |

Unique index on `(userId, period)`, created by `EnsureIndexes`
(`internal/database/database.go:128`). `incrUsage` is an upsert, so the first charge of a
new month creates the row.

### What counts and what does not

Counted from `analysisProgress.Analyzed`, incremented only in the batch pass of
`runAnalysis` (`api/analysis.go:145`), then charged once at the end
(`api/analysis.go:160`).

| Path | Counts against quota | Why |
|---|---|---|
| Email sent to the Mistral batch call | yes | `p.Analyzed++` right before caching the verdict |
| Single-email fallback after a misaligned batch | yes | Same counter, same branch |
| `analysis_cache` hit | no | Pass 1 increments `CachedHits` and `continue`s |
| Sender auto-pilot applied a saved preference | no | Pass 1 increments `AutoApplied` and `continue`s |
| Deterministic rules (`/api/rules/apply`, at-sync autopilot) | no | Never touches `runAnalysis` |
| AI call that failed and produced no analysis | no | `p.Processed++` only |

### Where the gate is checked, and what happens on exceed

| Call site | File:line | Behaviour on exceed |
|---|---|---|
| `POST /api/ai/analyze` | `api/ai_handlers.go:45` | `402 Payment Required`, body `Quota mensuel atteint. Passez à Pro pour continuer.` |
| `POST /api/ai/analyze-async` | `api/jobs.go:111` | Same 402, before the job document is created |

`quotaExceeded` (`api/account.go:46-51`) is `plan != pro && getUsage() >= 200`. Two Mongo
reads per call, no caching.

Critical: the gate is a pre-flight check on the request, not a per-email budget. A free
user at 199/200 who submits a batch of 500 ids passes the gate once and is then charged
for everything the batch analyzed. The async cap (`analysisJobCap = 500`,
`api/jobs.go:17`) bounds the overshoot on the async path; the synchronous path has no
cap beyond the 90 s handler timeout. Overshoot is by design cheap to reason about, but do
not describe 200 as a hard ceiling.

### Reset semantics

There is no reset job, no cron, no TTL index. The period key changes on the first UTC
day of the month and `incrUsage` upserts a fresh document with `analyzed: 0 + n`. Old
period documents are kept forever and are only ever removed by account deletion
(`usage` is in the RGPD dataset catalog).

Consequence: the reset boundary is UTC midnight on the 1st, not the user's local month,
and not the subscription anniversary. A user upgrading mid-month does not get their
counter zeroed; they simply stop being gated.

## Stripe

### Environment contract

From `.env.example:40-48` and `internal/config/config.go:41-64`.

| Var | Default | Effect when empty |
|---|---|---|
| `STRIPE_SECRET_KEY` | `""` | `billing.Client` stays nil: checkout and portal answer `503`, `billingOn` is false, the UI shows the waitlist |
| `STRIPE_PRICE_ID` | `""` | Checkout answers `503`; `billingEnabled()` is false even if the secret key is set |
| `STRIPE_WEBHOOK_SECRET` | `""` | Webhook answers `503` and no plan ever changes |
| `APP_BASE_URL` | `http://localhost:3000` | Used verbatim to build the success, cancel and portal return URLs |

`billingEnabled()` (`api/account.go:105-107`) is `Client != nil && PriceID != ""`. It is
the single answer behind both `GET /api/usage` (`billingOn`) and the public
`GET /api/config/status` (`models.InstanceStatus.BillingOn`), so a logged-out visitor and
a logged-in user can never disagree about whether Pro is buyable.

Note the asymmetry: `CreatePortal` requires only `Client != nil`, not a price. A
deployment with a secret key but no price ID can open the portal while checkout is
refused.

### Checkout flow

`POST /api/billing/checkout` -> reject if no `X-User-Email` (401) -> reject if
`Client == nil || PriceID == ""` (503) -> `getPlan` says pro already (409) ->
`CreateCheckoutSession` -> Stripe hosted URL -> `{"url": ...}` -> SPA does
`window.location.href = data.url`.

Session parameters sent (`billing/stripe.go:52-62`):

| Form field | Value |
|---|---|
| `mode` | `subscription` |
| `line_items[0][price]` | `STRIPE_PRICE_ID` |
| `line_items[0][quantity]` | `1` |
| `success_url` | `{APP_BASE_URL}/pricing?checkout=success` |
| `cancel_url` | `{APP_BASE_URL}/pricing?checkout=cancel` |
| `client_reference_id` | the user's email, the only reliable link back to the account |
| `allow_promotion_codes` | `true` |
| `customer_email` | the user's email, when non-empty |

HTTP client timeout is 20 s (`billing/stripe.go:36`); the handler context is 25 s.
A Stripe status >= 400 or an empty `url` becomes a `502` for the client.

### Portal flow

`POST /api/billing/portal` -> 401 without identity -> 503 if `Client == nil` -> read
`users.stripeCustomerId` -> `404` with `Aucun abonnement à gérer.` when the field is
missing or the user is not found -> `CreatePortalSession(customerID, APP_BASE_URL + "/pricing")`
-> `{"url": ...}`. The customer ID only exists after a completed checkout, so a user
whose `checkout.session.completed` was lost gets a 404 here forever.

### Webhook signature verification

`billing.ConstructEvent(payload, sigHeader, secret)` (`billing/stripe.go:163-223`),
Stripe's documented scheme, implemented by hand.

| Step | Detail |
|---|---|
| Precondition | Empty secret is a hard error, and the handler also 503s before calling |
| Raw body | `io.ReadAll(io.LimitReader(r.Body, 1<<20))`, 1 MiB cap, read before any parsing |
| Header parse | `Stripe-Signature` split on `,`, then `k=v`. Collects `t` and every `v1` |
| Malformed | Missing `t` or zero `v1` values is an error |
| Replay window | `webhookTolerance = 5 * time.Minute`. `time.Since(t) > tolerance` rejects |
| Signed payload | `HMAC-SHA256(secret, "{t}." + rawBody)`, hex encoded |
| Comparison | `hmac.Equal` against each `v1` candidate, constant time |
| Decode | Only `type` and `data.object` are unmarshalled; `data.object` stays `json.RawMessage` |

Any verification failure is logged and answered `400 Invalid signature`. The tolerance
check is one-sided: a timestamp in the future yields a negative `time.Since` and passes.
That is not exploitable without the signing secret, but it is not symmetric either.

### Events handled (exhaustive)

`api/billing.go:133-167`. Three cases, nothing else. Every other event type falls through
the switch and is answered `200 OK`.

| Event type | Object decoded | Match filter | Effect |
|---|---|---|---|
| `checkout.session.completed` | `billing.CheckoutSession` | `{"email": <resolved>}` | Set `plan: "pro"`, plus `stripeCustomerId` and `stripeSubscriptionId` |
| `customer.subscription.updated` | `billing.Subscription` | `{"stripeSubscriptionId": sub.ID}` | `plan: "pro"` if status is `active` or `trialing`, otherwise `plan: "free"` |
| `customer.subscription.deleted` | `billing.Subscription` | `{"stripeSubscriptionId": sub.ID}` | `plan: "free"` |

Email resolution on `checkout.session.completed`, in order:
`client_reference_id` -> `customer_email` -> `customer_details.email`. The first is the
one we set ourselves at checkout, so it is the only one that is guaranteed to match a
stored account exactly.

Not handled, deliberately or otherwise: `invoice.payment_failed`,
`invoice.payment_succeeded`, `customer.subscription.trial_will_end`,
`checkout.session.expired`, `customer.deleted`. A failed renewal only downgrades the user
if Stripe also emits `customer.subscription.updated` with a non-active status.

`setPlan` (`api/billing.go:173-181`) is a fire-and-forget `UpdateOne` that logs its error
and returns nothing. It is **not** an upsert.

### User record fields owned by billing

`models.User` (`models/models.go:13-17`).

| Field | BSON | JSON | Written by |
|---|---|---|---|
| `Plan` | `plan` | `plan` | `setPlan` (all three webhook events) |
| `StripeCustomerID` | `stripeCustomerId` | `-` (never serialized) | `checkout.session.completed` only |
| `StripeSubscriptionID` | `stripeSubscriptionId` | `-` | `checkout.session.completed` only |
| `PlanUpdatedAt` | `planUpdatedAt` | `-` | `setPlan`, every call |

A sparse index on `users.stripeSubscriptionId` (`database.go:130`) makes the two
subscription-event lookups cheap.

## Account Profile and Settings

### Redaction

`account.RedactUser` (`internal/account/account.go:68-83`) is the only projection of a
`models.User` that leaves the server. It is pure and I/O-free, and it is shared by
`GET /api/account/profile` and the RGPD export, so there is one definition of "safe to
show about an account".

| In `Profile` | Deliberately absent |
|---|---|
| `email`, `plan`, `autoApplyRules`, `autoSyncEnabled`, `digestEnabled`, `digestHourUTC`, `createdAt`, `updatedAt` | `accessToken`, `refreshToken`, `tokenExpiry`, `stripeCustomerId`, `stripeSubscriptionId`, `planUpdatedAt`, `lastAutoSyncAt`, `digestLastSentAt` |

An empty `plan` is normalised to `"free"` in the projection, which is what
`TestRedactUserDefaultsPlan` pins.

### Settings merge

`models.SettingsUpdate` uses `*bool` / `*int` for every field so the server can tell
"set this to false" from "field not sent" (`models/models.go:53-58`). `UpdateSettings`
builds a `$set` from only the non-nil fields, which is what stops the Rules screen and
the digest card from clobbering each other. It then re-reads and returns the merged
`UserSettings`.

`digestHourUTC` has two different clamps, and they disagree:

| Location | Rule |
|---|---|
| `UpdateSettings` write path (`account.go:184`) | `hour < 0 \|\| hour > 23` falls back to `defaultDigestHour()` |
| `userSettings` read path (`account.go:129`) | `hour <= 0 \|\| hour > 23` falls back to `defaultDigestHour()` |

So hour `0` (midnight UTC) can be stored but never read back: the read path treats a
stored `0` as "unset". This is intentional and commented, but it means the API is not
round-trip faithful for that one value.

## RGPD Export and Erasure

### The dataset catalog

`account.Datasets()` (`internal/account/account.go:37-50`) is the single ordered list
that drives BOTH the export and the deletion. `datasetCollection`
(`api/account_data.go:19-43`) is the one bridge from that pure catalog to Mongo. Adding a
user-owned collection to one and not the other silently breaks the invariant.

| Dataset key | Collection | Scoped by | In export | Deleted |
|---|---|---|---|---|
| `rules` | `sorting_rules` | `userId` | yes | yes |
| `protectedSenders` | `protected_senders` | `userId` | yes | yes |
| `snoozes` | `snoozes` | `userId` | yes | yes |
| `suggestions` | `ai_suggestions` | `userId` | yes | yes |
| `senderPreferences` | `sender_preferences` | `userId` | yes | yes |
| `smartLabels` | `smart_labels` | `userId` | yes | yes |
| `unsubscribes` | `unsubscribes` | `userId` | yes | yes |
| `usage` | `usage` | `userId` | yes | yes |
| `actionLog` | `action_log` | `userId` | yes | yes |
| `analysisJobs` | `analysis_jobs` | `userId` | yes | yes |

`TestDatasetsAreUniqueAndNonEmpty` guards against a duplicated or empty key. Nothing
guards against a *missing* key: the catalog cannot detect a collection nobody added to it.

### What the export contains

`GET /api/account/export` returns one indented JSON document
(`api/account_data.go:51-81`):

```
exportedAt        RFC3339 UTC timestamp
account           account.RedactUser(user)      -> no tokens, no Stripe ids
settings          models.UserSettings           -> the four tunables
<dataset key>     [] of raw bson.M per dataset  -> ten keys, always an array, never null
```

Rows are dumped as raw `bson.M` rather than through the typed structs
(`dumpUserRows`), so the export stays faithful even if a document has fields the Go
struct does not declare. `_id` values therefore appear in the output.

`Content-Disposition: attachment; filename="mailsorter-export-YYYY-MM-DD.json"` is set,
but the SPA ignores it: `Account.js:41-50` reads the JSON through axios and rebuilds its
own Blob with its own filename. The header only matters if someone hits the URL directly.

### What deletion actually removes

`DELETE /api/account` (`api/account_data.go:115-147`) walks the same catalog with
`DeleteMany({userId})`, then deletes the account record itself with
`DeleteMany({email})`, and returns per-dataset counts:

```json
{"status":"deleted","deleted":{"rules":3,"usage":1,"account":1}}
```

| Removed | Left behind |
|---|---|
| The ten catalogued datasets, by `userId` | `emails` (synced message cache, scoped by `userId`, NOT in the catalog) |
| The `users` document, by `email` | `waitlist` row, keyed by `email`, not by `userId` |
| | `analysis_cache` (shared across all users on purpose, keyed on sender+subject hash) |
| | The Gmail mailbox itself, untouched by design |
| | The Google OAuth grant, which only the user can revoke from their Google account |
| | The Stripe customer and any live subscription |

The frontend gates the call behind typing `SUPPRIMER` into a confirmation input
(`Account.js:113`), then clears `accessToken` and `userEmail` from localStorage and
redirects to `/`.

## Pro Waitlist

`POST /api/waitlist`, public. `models.WaitlistInput` is `{email, source}`.

| Step | Behaviour |
|---|---|
| Normalisation | `normalizeWaitlistEmail`: `mail.ParseAddress` on the trimmed input, then lowercase the address part. Accepts `Name <a@b.c>` and keeps only `a@b.c` |
| Rejection | Anything `ParseAddress` refuses, plus an `@` at position 0 or at the end. Answered `400 Adresse email invalide` |
| Storage | Upsert on `{email}`. `$set` writes `source` and `plan`; `$setOnInsert` writes `email` and `createdAt` |
| Default source | `"pricing"` when the body omits it |
| Plan recorded | `waitlistPlan = PlanPro`, a constant so the rows stay meaningful if a second tier opens |
| Identity | `userId` is attached only when `authMiddleware` vouched an `X-User-Email` |
| Idempotence | Unique index on `waitlist.email` plus the upsert: signing up twice is one row, and answers `200 {"joined":true,"email":...}` |

Re-signing up overwrites `source` (it is in `$set`, not `$setOnInsert`), so the stored
source is the *last* surface used, not the first.

There is no read endpoint. The waitlist is queried out of band, directly in Mongo.

## Frontend Surfaces

| File | Role |
|---|---|
| `frontend/src/pages/Pricing.js` | Plan cards, usage meter, 7-day recap, checkout / portal / waitlist CTA switch |
| `frontend/src/pages/Account.js` | Identity, plan, usage meter, 7-day recap, RGPD export and delete |
| `frontend/src/services/api.js` | `billingService.checkout/portal`, `accountService.*`, `waitlistService.join` |

The Pricing CTA is a four-state machine driven by `billingOn` from the **public**
`/api/config/status` probe, never from `/api/usage`:

| `billingOn` | `isPro` | Rendered |
|---|---|---|
| `null` (not answered yet) | any | Disabled button with a spinner, so it never flips mid-read |
| `true` | `true` | "Gérer mon abonnement" -> portal |
| `true` | `false` | "Passer à Pro" -> checkout (logged out: bounce to `/`) |
| `false` | `false` | Waitlist button, or an email form for cold traffic |

An unreachable API resolves to `{billingOn: false}`, the state that cannot promise a
checkout the instance may be unable to honour.

Post-checkout redirect handling reads `?checkout=success|cancel` on `/pricing`, toasts,
refetches usage, then strips the query param. Analytics events are boolean-only
(`track('upgrade_start')`, `track('waitlist_join', {loggedIn})`, `track('data_export')`):
no address ever reaches Umami.

`localStorage.mailsorter_pro_waitlist` is a per-browser hint that this device already
signed up. The server is the record; the flag only picks which state to render.

## Tuning Constants (Current Values)

```
FreeMonthlyLimit:    200 (emails analyzed per calendar month) - api/account.go:19
analysisJobCap:      500 (emails per async job, hard truncation) - api/jobs.go:17
analysisBatchSize:   8 (emails per Mistral call) - api/analysis.go:19
webhookTolerance:    5 min (Stripe replay window) - billing/stripe.go:24
stripe http timeout: 20 s (client) - billing/stripe.go:36
checkout ctx:        25 s, portal ctx: 25 s - api/billing.go:46, :86
webhook ctx:         10 s - api/billing.go:130
webhook body cap:    1 MiB - api/billing.go:117
usage/profile ctx:   5 s - api/account.go:61, :94
export/delete ctx:   30 s - api/account_data.go:58, :122
waitlist ctx:        10 s - api/waitlist.go:62
DefaultSessionTTL:   30 days - internal/auth/auth.go:25
period format:       "2006-01" UTC - api/account.go:21
```

## Scaffolded but Not Wired Up

| Thing | State |
|---|---|
| `PlanFree` constant | Defined and used as a return value, but never written to Mongo by any path except the two downgrade webhook branches. A free user has no `plan` field at all |
| Portal without a price ID | `CreatePortal` only checks `Client != nil`, so it is reachable in a config where checkout is refused |
| `models.WaitlistEntry` | The typed struct exists, but `JoinWaitlist` writes raw `bson.M` and nothing ever decodes into it. No read endpoint |
| `labels` collection | Has a `Database.Labels()` accessor and no caller anywhere in `internal/`. Not in the RGPD catalog either |
| Pro feature list | Four of the five Pro bullets in `Pricing.js` describe capabilities that are not plan-gated (or, for multi-account Gmail, do not exist) |

## Known Pitfalls

- **A webhook that fails to write still answers 200.** `setPlan` logs its Mongo error and
  returns; `StripeWebhook` then writes `http.StatusOK` unconditionally. Stripe sees a
  successful delivery and never retries, so a transient Mongo failure during
  `checkout.session.completed` loses the upgrade permanently. Recovering means replaying
  the event from the Stripe dashboard.
- **`setPlan` is not an upsert.** `UpdateOne` with `{"email": ...}` matching nothing is a
  silent no-op. If the user deleted their account between checkout and webhook delivery,
  or if Stripe hands back an address that differs in case from the stored one, the upgrade
  evaporates with only a log line.
- **The two subscription events key on `stripeSubscriptionId`, which only
  `checkout.session.completed` ever writes.** Lose or misorder that first event and every
  later `customer.subscription.updated` / `.deleted` matches zero documents. There is no
  reconciliation job and no `GET` against the Stripe API to repair it.
- **Deleting the account resets the quota.** `usage` is in the RGPD catalog, so
  `DELETE /api/account` wipes the counter, and the next OAuth login upserts a fresh user
  with no `plan` field (`handlers.go:187-202`). A free user can therefore self-serve an
  unlimited quota reset. Erasure correctness and metering integrity are in direct conflict
  here; the erasure side won.
- **Deleting the account does not cancel the Stripe subscription.** The `users` row that
  carried `stripeSubscriptionId` is gone, so the eventual `customer.subscription.deleted`
  matches nothing. The customer keeps being billed until they cancel in the portal, which
  they can no longer reach because `CreatePortal` needs the deleted `stripeCustomerId`.
- **The `emails` collection is user-owned and outside the RGPD catalog.** Every synced
  message (subject, snippet, body) is stored with a `userId` and is neither exported nor
  deleted. This is the single largest gap between what the Account page promises and what
  the code does.
- **The `waitlist` row survives deletion.** It is keyed by `email`, and `DeleteAccount`
  only deletes by `userId` per dataset. Someone who joined the waitlist then erased their
  account still has their address stored.
- **A valid session token outlives the account.** Sessions are stateless HMAC with a
  30-day TTL and no database check (`auth.VerifySession`). After `DELETE /api/account` the
  old bearer token still authenticates, and any upserting write (notably `incrUsage`) will
  happily recreate rows for a user that no longer exists.
- **The quota gate is per-request, not per-email.** See the overshoot note above. Do not
  present `200` as a hard cap in UI copy or in a test assertion.
- **`getPlan` and `quotaExceeded` hit Mongo on every AI request** with no memoisation.
  `quotaExceeded` alone is two round trips before any work starts.
- **This subsystem predates `respond.go`.** `billing.go`, `account_data.go`,
  `waitlist.go` and most of `account.go` still use `http.Error` plus a bare
  `json.NewEncoder(w).Encode(...)`, which the constitution forbids for JSON routes. Only
  `UpdateSettings` uses `decodeJSON` / `writeJSON` / `writeError`. Consequence: error
  bodies from these handlers are `text/plain`, not the `{"error": ...}` shape the SPA
  expects elsewhere, and the frontend compensates by switching on `err.response.status`
  alone. Match the surrounding style when editing, but do not assume the JSON error
  contract holds here.
- **`APP_BASE_URL` is concatenated, not joined.** A trailing slash produces
  `https://host//pricing?checkout=success`. Stripe accepts it; the SPA router mostly does
  too, which is exactly why nobody notices.

## Testing

Backend tests are standard library only. Do not run the suite while other agents are
working; the commands below are for reference.

| Scope | Command | What it covers |
|---|---|---|
| Pure RGPD catalog | `cd backend && go test ./internal/account/` | `TestDatasetsAreUniqueAndNonEmpty`, `TestRedactUserDropsSecrets`, `TestRedactUserDefaultsPlan` |
| Waitlist normalisation | `cd backend && go test -run TestNormalizeWaitlistEmail ./internal/api/` | 5 equivalent spellings collapse to one, 7 malformed inputs rejected |
| Route wiring | `cd backend && go test -run TestRoutes ./internal/api/` | `/api/usage` is protected, `/api/waitlist` accepts cold traffic and 400s a bad address, `/api/config/status` exposes exactly `isConfigured` and `billingOn` |

Untested by anything: `internal/billing/stripe.go` has **no test file at all**. Signature
verification, the tolerance window and the two session builders are covered only by
production traffic. Any change there is unguarded.

Manual checks that matter:

1. Stripe CLI: `stripe listen --forward-to localhost:8080/api/billing/webhook`, then
   `stripe trigger checkout.session.completed`. Verify `users.plan` flips and that a
   tampered `Stripe-Signature` yields 400.
2. Replay an event older than 5 minutes and confirm the tolerance rejection.
3. With `STRIPE_SECRET_KEY` empty, load `/pricing` logged out and confirm the waitlist
   form renders rather than a checkout button.
4. Export, then delete, then re-authenticate: confirm the usage counter reads 0 and the
   plan reads free.

## Key Files

| File | Description |
|---|---|
| `backend/internal/billing/stripe.go` | Hand-rolled Stripe client, checkout, portal, signature verification |
| `backend/internal/api/billing.go` | Plan constants, checkout, portal, webhook switch, `setPlan` |
| `backend/internal/api/account.go` | `FreeMonthlyLimit`, usage read/increment, quota gate, profile, settings |
| `backend/internal/api/account_data.go` | Dataset to collection bridge, RGPD export, RGPD erasure |
| `backend/internal/account/account.go` | Pure dataset catalog and `RedactUser` |
| `backend/internal/account/account_test.go` | Catalog uniqueness and redaction guarantees |
| `backend/internal/api/waitlist.go` | Email normalisation and the idempotent waitlist upsert |
| `backend/internal/api/waitlist_test.go` | Normalisation table test |
| `backend/internal/api/ledger.go` | `Source*` vocabulary and `logAction`, the ledger the recap reads |
| `backend/internal/api/analysis.go` | Where `Analyzed` is counted and `incrUsage` is called |
| `backend/internal/api/jobs.go` | Async quota gate and `analysisJobCap` |
| `backend/internal/api/routes.go` | Route registrations for billing, account and waitlist |
| `backend/internal/api/middleware.go` | `publicPrefixes` and the webhook rate-limit exemption |
| `backend/internal/models/models.go` | `User` billing fields, `UserSettings`, `SettingsUpdate`, `WaitlistEntry`, `InstanceStatus` |
| `backend/internal/database/database.go` | `Usage()` / `Waitlist()` accessors and their unique indexes |
| `backend/internal/config/config.go` | Stripe env loading |
| `frontend/src/pages/Pricing.js` | Plan cards, CTA state machine, waitlist form |
| `frontend/src/pages/Account.js` | Plan and usage panel, RGPD export and delete |
| `frontend/src/services/api.js` | `billingService`, `accountService`, `waitlistService` |
| `.env.example` | Stripe and `APP_BASE_URL` contract |

## References

### Source Files
- `backend/internal/billing/stripe.go` - the only place Stripe's wire format is spoken
- `backend/internal/api/billing.go` - the only place a plan is written
- `backend/internal/api/account.go` - the only place the quota is read or charged
- `backend/internal/account/account.go` - the catalog both RGPD halves obey
- `backend/internal/api/account_data.go` - the Mongo side of that catalog
- `backend/internal/api/waitlist.go` - the pre-launch intent capture
- `docs/API.md` - endpoint payloads and error codes for the whole API

### Related Context Docs
- [ai-triage.md](ai-triage.md) - what the quota is actually metering: the Mistral batch path, the shared cache and the sender auto-pilot that both bypass it
- [data-model.md](data-model.md) - the `users`, `usage` and `waitlist` collections, their indexes, and the `userId`-is-the-email convention
- [auth-session-security.md](auth-session-security.md) - `X-User-Email` provenance, `publicPrefixes`, and why the webhook authenticates itself
