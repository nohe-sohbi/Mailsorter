# Mailsorter

AI-assisted Gmail triage: the model reads, understands and sorts an inbox, but every
action stays a reversible proposal the user validates. Go REST API + React SPA +
MongoDB, self-hosted end to end on [mailsorter.sohbi.dev](https://mailsorter.sohbi.dev).

Solo-developer project. Production is live. The 12 roadmap phases are delivered
(`docs/ROADMAP.md`), and the last audit closed every item it opened (`BACKLOG.md`).

## Tech Stack

| Layer | Choice | Version pinned in |
|---|---|---|
| Backend | Go, `net/http` + `gorilla/mux` router | `backend/go.mod` (`go 1.21`) |
| Module path | `github.com/nohe-sohbi/mailsorter/backend` | `backend/go.mod` |
| HTTP deps | `gorilla/mux` 1.8.1, `rs/cors` 1.10.1 | `backend/go.mod` |
| Data | MongoDB 7, official `mongo-driver` 1.13.1 | `docker-compose.yml`, `backend/go.mod` |
| Google | `golang.org/x/oauth2` 0.15.0, `google.golang.org/api` 0.154.0 (Gmail v1) | `backend/go.mod` |
| IMAP | `github.com/emersion/go-imap/v2` v2.0.0-beta.8, pinned | `backend/go.mod` |
| AI | Mistral chat completions, hand-rolled HTTP client | `backend/internal/ai/mistral.go` |
| Billing | Stripe REST, hand-rolled client (no SDK) | `backend/internal/billing/stripe.go` |
| Frontend | React 18.2, react-router-dom 6.21, axios 1.6, dompurify 3.0 | `frontend/package.json` |
| Build (front) | Create React App (`react-scripts` 5.0.1) + Tailwind 3.4 | `frontend/package.json` |
| Serving | nginx alpine, SPA + `/api` reverse proxy | `frontend/nginx.conf` |
| CI | GitHub Actions, Go 1.21 + Node 20 | `.github/workflows/ci.yml` |
| Deploy | Dokploy (Compose app), auto-redeploy on push to `main` | `docker-compose.yml` |

There is no Stripe SDK, no icon library, no test framework beyond the standard
library, and no metrics backend. Each of those is a deliberate, hand-rolled,
dependency-free replacement. Do not add a library to replace one of them without
being asked.

`go-imap` is the one exception, and it is not a contradiction of that rule: it
replaces nothing hand-rolled, and IMAP is not in the same class as those four. It
is a stateful protocol with literals, continuation requests and nested response
grammars, where a parsing bug means the wrong message is archived rather than a
failed request. It is pinned to an exact version. It is a BETA on purpose: the
v1.2.1 tag looks stabler but has not been published since May 2022, and four
years without a fix on code that handles credentials and TLS is the larger risk.
It also ships `imapserver/imapmemserver`, which is what lets `internal/imap` be
tested against a real server rather than a mock.

## Code Quality Standards

1. **Keep the decision logic pure.** Anything that decides (does this rule match,
   is this snooze due, may this sender be archived, what does the digest say) goes in
   an I/O-free package under `backend/internal/` and is tested exhaustively. Only the
   `api` package talks to Mongo, Gmail, Mistral or Stripe. Never put a `bson.M` or an
   `http.Request` in a pure package.
2. **Fail closed on security, fail fast on config.** `config.Validate()` refuses to boot
   on a placeholder `ENCRYPTION_KEY`. The auth middleware strips a client-supplied
   `X-User-Email` before re-setting it itself. Preserve that posture in every change.
3. **Never silently drop an error.** The audit that produced `BACKLOG.md` found two
   P1 bugs that were both a swallowed error (`date, _ = time.Parse(...)`) or a wrong
   `nil` return. If you cannot handle an error, log it with context and return a typed
   one; do not return a stale value with `nil`.
4. **Every Gmail mutation is journaled and reversible.** Call `h.logAction(...)` with a
   `Source*` constant after any mutating action, and check `protect.Allowed` before any
   destructive one. A feature that mutates the inbox without a ledger entry is incomplete.
   Perform the mutation through `h.applyVerb` (one verb) or `h.applyMutations` (several in
   one call). Never call `gmailService.ModifyMessage` from a handler and never write a
   Gmail label id there: that knowledge lives in `internal/mailbox` and nowhere else.
   Both take a `mailbox.Ref`, not a bare id: `mailbox.OnAccount(id)` for a Gmail API id,
   `mailbox.InFolder(folder, uid)` for an IMAP one. Say which kind you hold; the adapter
   refuses the other rather than acting on whatever message carries that number.
5. **Cheap paths before expensive ones.** Deterministic rules run before the model; the
   shared analysis cache runs before an API call; cache hits and auto-pilot do not burn
   quota. New AI work must justify why it cannot be a rule or a cache hit.
6. **No em dashes, en dashes, ellipsis characters or curly quotes in anything you write**
   (code, comments, docs, commit messages, UI copy). Two commits already stripped them
   repo-wide (`6d44863`, `393ea5d`). Plain ASCII punctuation only.

## Project Structure

```
backend/
  cmd/server/            main: config -> db -> indexes -> services -> router -> serve
  cmd/test_decrypt/      one-off ops tool, reads the legacy gmail_config document
  internal/api/          the ONLY I/O layer: handlers, routes, middleware, schedulers
  internal/{account,activity,digest,egress,mailbox,mailer,metrics,protect,
            provider,rules,schedule,snooze,unsubscribe}/
                         pure logic, no I/O, heavily unit-tested
  internal/{ai,billing,gmail,imap}/  outbound clients (Mistral, Stripe, Gmail, IMAP)
  internal/{auth,crypto,config,database,models}/  cross-cutting primitives
frontend/
  src/pages/             one file per route (10 routes)
  src/components/        shared non-route components (Header, EmailReader)
  src/contexts/          EmailContext: the shared inbox cache.
                         InstanceContext: what this deployment is (edition, billing, configured)
  src/services/api.js    every HTTP call in the app, grouped by service object
  src/ui/                design-system primitives (icons, Toast, Spinner, Modal, SnoozeMenu, cn, streak)
  src/lib/analytics.js   Umami tracker injection + track()
  nginx.conf             SPA fallback, immutable /static, no-cache index.html, /api proxy
docs/                    ARCHITECTURE.md, API.md, ROADMAP.md, assets/
mongo-init/init-db.js    collections + indexes seeded on a fresh Mongo container
```

## Build, Run, Test

All commands verified against this working copy.

| Goal | Command | Notes |
|---|---|---|
| Full stack, containers | `make up` (then `make logs`, `make down`) | app on :3000, API on :8080. Uses `docker-compose.yml` PLUS `compose.local.yml`, which publishes the host ports the deployment file omits. A bare `docker compose up` binds nothing |
| Rebuild images | `make build` | `docker compose build` |
| Nuke containers + volumes + node_modules + binaries | `make clean` | destructive |
| Backend tests | `make test` (= `cd backend && go test ./...`) | 205 test functions, all green |
| Backend tests as CI runs them | `cd backend && go test -race ./...` | what `.github/workflows/ci.yml` runs |
| Backend vet + build | `cd backend && go vet ./... && go build ./...` | both clean |
| Backend alone | `make backend` (build + run on :8080) or `cd backend && go run cmd/server/main.go` | needs a reachable Mongo |
| Frontend dev server | `make frontend` or `cd frontend && npm install && npm start` | :3000, proxies nothing, hits :8080 directly |
| Frontend production build | `cd frontend && CI=false npm run build` | verified: "Compiled successfully" |
| Everything local without Compose | `./dev-start.sh` | starts a throwaway `mailsorter-mongodb-dev` container, backend and frontend; needs `.env` |
| Health | `curl -w "%{http_code}" http://localhost:8080/health` | 200 healthy, 503 when Mongo is unreachable |

Notes that will bite you:

- `go` is not on the default PATH in this environment. Use `export PATH="$HOME/go/bin:$PATH"`
  (local toolchain is 1.24.1; `go.mod` and CI target 1.21).
- **There are zero frontend tests.** `npm test` exists (`react-scripts test`) but no
  `*.test.js` file does. CI does not run it. Frontend verification is a real build plus
  browser navigation, never a unit test claim.
- CI (`.github/workflows/ci.yml`) runs, on every push to `main` and every PR: backend
  `go mod download`, `go vet ./...`, `go build ./...`, `go test -race ./...`; frontend
  `npm install` + `CI=false npm run build`. Nothing else. No linter, no formatter gate.

## Backend Architecture

Read `docs/ARCHITECTURE.md` for the diagrams, the auth flow and the sync flow. Read
`docs/API.md` for every endpoint payload. This section is the map, not a copy of them.

Caveat on `docs/ARCHITECTURE.md`: its "Composants" section predates most of the app
(it lists 9 endpoints and 4 collections). Its Security, Observability and Background
sections are current. When they disagree, the code wins.

### The pure core rule

`internal/api` is the only package allowed to touch Mongo, Gmail, Mistral or Stripe.
Everything below is pure by construction, and each one says so in its package doc:

| Package | Owns | Key entry points |
|---|---|---|
| `rules` | Deterministic AI-free triage engine, plus the portable form of a ruleset | `Matches`, `Validate`, `Preview`, `Reorder`, `BuildExport`, `ValidateImport`, field/operator/action constants |
| `protect` | VIP safety net: which senders can never be auto-archived, trashed or deleted | `Allowed`, `ActionArchive/Trash/Delete` |
| `snooze` | Preset ("ce soir", "demain", "weekend") to a concrete wake time, and the guard on a hand-picked one | `Resolve`, `ValidateWake`, `MaxHorizon`, `Preset*` constants |
| `search` | What makes a saved search valid, its identity, and its default name | `Normalize`, `Key`, `SuggestName`, `MaxPerUser` |
| `schedule` | "Is this periodic work due?" | `Due(last, now, interval)` |
| `activity` | Action ledger rows to a 7-day series plus breakdowns | `Row`, `DayCount` aggregation |
| `digest` | The 7-day recap rendered into subject + text + HTML | `Digest` |
| `mailer` | RFC 2822 multipart build for Gmail send, and daily-due arithmetic | `BuildRaw`, `DueAt` |
| `account` | The single catalog of user-owned data driving BOTH export and erasure, and which of its fields are secrets | `Dataset*`, `SecretFields`, `RedactUser` |
| `metrics` | In-process bounded request meter (method x status class, latency) | `Registry` |
| `provider` | The mailbox catalog: which provider is reachable by which transport, with which credential, in which EDITION, and what can block it | `All`, `ForEdition`, `Detect`, `Pick`, `Edition*`, `Transport*`, `Auth*`, `Cap*`, `Blocker*` |
| `mailbox` | The provider-neutral verb vocabulary, how a message is NAMED on each transport, and the translation per transport. The ONLY place that knows Gmail's system label ids, and IMAP's flags and special folders | `Action*`, `Parse`, `Destructive`, `Mutation`, `Ref`, `OnAccount`, `InFolder`, `Mailbox`, `GmailLabels`, `GmailLabelsFor`, `GmailIsRead`, `GmailAfter`, `IMAPOpFor`, `IMAPOpsFor`, `IMAPIsRead`, `Folder*`, `Flag*` |
| `egress` | Where the server may send a request of its own. Guards the one-click unsubscribe, the only outbound URL a stranger chooses | `Parse`, `Allowed`, `AllowedIP`, `ErrNotHTTPS`, `ErrNoHost`, `ErrPrivateAddress` |
| `unsubscribe` | The `List-Unsubscribe` (RFC 2369) and `List-Unsubscribe-Post` (RFC 8058) headers. Shared by both transports: the headers belong to the message, not to how it was fetched | `Parse`, `SplitAngleList`, `Links` |

The outbound clients and primitives:

| Package | Responsibility |
|---|---|
| `ai` | Mistral client. Batching of 8, exponential backoff with jitter honoring `Retry-After`, fast-fail on 4xx |
| `gmail` | Gmail v1 wrapper. `retry.go` wraps every call with the same backoff policy |
| `imap` | The IMAP transport, for every route the hosted edition uses. Connect (TLS or STARTTLS, never cleartext), resolve the server's special folders, list a page, apply a mutation. `list.go` mirrors the Gmail listing's payload discipline |
| `billing` | Stripe Checkout Session creation and webhook signature verification, with a 5 min replay window |
| `auth` | HMAC-SHA256 session tokens and OAuth `state`, keyed by distinct labels off the master secret so one cannot be replayed as the other |
| `crypto` | AES-256-GCM at rest, key SHA-256-derived from `ENCRYPTION_KEY`. Its production callers are `api/tokens.go` (per-user Gmail tokens) and the legacy `gmail_config` read at boot |
| `config` | Env loading + `Validate()` fail-fast |
| `database` | Mongo client, one accessor per collection, `EnsureIndexes` |
| `models` | Every BSON/JSON struct. One file, `models.go` |

### The api package, file by file

| File | Covers |
|---|---|
| `routes.go` | The single route table (70 registrations) and the middleware chain. Source of truth for the API surface |
| `middleware.go` | `authMiddleware`, `recoverMiddleware`, `requestIDMiddleware`, `loggingMiddleware`, token-bucket rate limiter, `publicPrefixes` |
| `respond.go` | `writeJSON`, `writeError`, `decodeJSON`, `writeAuthError`, `errReauthRequired`, 1 MiB body cap |
| `handlers.go` | `Handler` struct + constructor (which starts the background loops), health, metrics, auth callback, emails, sync, direct action, labels, config status |
| `analysis.go` | `runAnalysis`: the shared engine behind sync and async AI analysis. Cache, batching, auto-pilot, quota |
| `ai_handlers.go` | The nine `/api/ai/*` endpoints, plus `/api/senders/*` |
| `tokens.go` | The user's Google credentials: `sealToken` / `openToken` (AES-256-GCM at rest), the legacy-plaintext migration, and `getUserToken` |
| `jobs.go` | Async analysis job queue and worker pool |
| `rules.go` | Rules CRUD, `apply`, `preview` (dry run reuses the apply path) |
| `snooze.go` | Snooze CRUD (preset or explicit wake time), the batch snooze, plus the 1 min wake sweeper |
| `attachment.go` | Attachment download: the one route that streams bytes rather than JSON |
| `searches.go` | Saved searches CRUD and their use counter |
| `rules_portable.go` | Ruleset-wide operations: reorder, duplicate, export, import |
| `protected.go` | VIP list CRUD and `protectedValues` lookup |
| `unsubscribe.go` | `List-Unsubscribe` (RFC 2369) and one-click POST (RFC 8058) |
| `history.go` | Action log view and undo |
| `ledger.go` | `logAction` plus the `Source*` vocabulary. Read this before adding a mutating action |
| `account.go` | Settings, usage, profile, `FreeMonthlyLimit = 200` |
| `account_data.go` | `datasetCollection`: the one bridge from `account.Dataset` to a Mongo collection. Export and delete both walk it |
| `billing.go` | Checkout, portal, Stripe webhook |
| `waitlist.go` | Public Pro waitlist capture |
| `providers.go` | `GET /api/providers`: the `internal/provider` catalog for the running `Edition`, shaped for the connect screen. The SPA hardcodes no provider |
| `mail_accounts.go` | The mailbox a user connected over IMAP: connect (proved before it is stored), read, disconnect. Plus `openMailbox`, the IMAP counterpart of `getUserToken` and the only reader of the sealed app password |
| `mailbox.go` | `gmailMailbox`, the Gmail adapter for `mailbox.Mailbox`, plus `applyVerb` and `applyMutations`. Every mutating handler goes through these two |
| `digest_scheduler.go` | 15 min ticker sending the daily digest through the user's own Gmail |
| `auto_sync.go` | 30 min per-user background inbox sync |

### Request pipeline

Declared in `routes.go`, outermost first:

```
CORS -> recover -> request-id -> metrics -> logging -> rate-limit (20 r/s, burst 40) -> auth -> handler
```

`authMiddleware` deletes any inbound `X-User-Email`, verifies the `Authorization: Bearer`
session token, then sets `X-User-Email` itself. Handlers read that header and can trust it.
Public routes (`publicPrefixes` in `middleware.go`): `/health`, `/metrics`, `/api/auth/`,
`/api/config/status`, `/api/providers`, `/api/waitlist`, `/api/billing/webhook`. On a public route a valid
token still identifies the caller; a bad one is not an error.

### Adding an endpoint

1. Register it in `routes.go` under the right comment block.
2. Handler reads the caller with `r.Header.Get("X-User-Email")`. Never trust a body-supplied identity.
3. Decode with `decodeJSON(w, r, &req)` and return immediately if it is false (it already wrote the response).
4. Respond with `writeJSON` / `writeError`. Never `http.Error` for a JSON route, never a bare `json.NewEncoder(w)`.
5. Token failures go through `writeAuthError(w, err)` so a revoked Google grant yields 401 (which the SPA handles) and not a 500 loop.
6. If it mutates Gmail: check `h.protectedValues` + `protect.Allowed` first, and `h.logAction` after.
7. If it stores a new user-owned collection: add it to `account.Dataset*` AND to `datasetCollection` in `account_data.go`, or export and erasure silently drift.
8. Document it in `docs/API.md`. Mirror it in `frontend/src/services/api.js`.

### MongoDB

Database `mailsorter`. One accessor per collection in `database.go`; indexes created
best-effort at boot by `EnsureIndexes`. `mongo-init/init-db.js` seeds only the four
original collections on a fresh volume, so `EnsureIndexes` is the real source of truth.

`users`, `emails`, `labels`, `gmail_config` (legacy, read-only fallback),
`ai_suggestions`, `sender_preferences`, `smart_labels`, `analysis_jobs`,
`analysis_cache`, `usage`, `unsubscribes`, `sorting_rules`, `protected_senders`,
`snoozes`, `action_log`, `saved_searches`, `waitlist`, `mail_accounts`.

Everything is scoped by `userId` (which is the user's email address) except
`analysis_cache`, keyed by `sha256(lower(from) + "|" + lower(subject))` and shared
across all users on purpose, and `waitlist`, keyed by email.

### Background loops

All four are started by `NewHandler` in `handlers.go`. They are goroutines with tickers,
not cron; a redeploy restarts them.

| Loop | Cadence | File |
|---|---|---|
| Analysis worker pool (3 workers) | drains `jobQueue` (cap 256) | `jobs.go` |
| Snooze wake sweep | 1 min | `snooze.go` |
| Daily digest | 15 min tick, at most one send per user per day | `digest_scheduler.go` |
| Auto-sync | 5 min tick, but min 30 min between two syncs of the same user | `auto_sync.go` |

## Frontend Architecture

### Routes

Declared in `src/App.js`. The app boots by calling `GET /api/config/status` once,
through `InstanceProvider`; if the instance has no Gmail credentials every guarded
route redirects to `/setup`.

| Route | Page | Purpose |
|---|---|---|
| `/` | `pages/Login.js` | Marketing landing + Google sign-in |
| `/inbox` | `pages/Inbox.js` | The cockpit: triage, suggestions, bulk apply, keyboard shortcuts (1068 lines, the heaviest file) |
| `/rules` | `pages/Rules.js` | Deterministic rule editor + dry-run preview |
| `/snoozed` | `pages/Snoozed.js` | Scheduled returns |
| `/history` | `pages/History.js` | Action ledger + undo |
| `/pricing` | `pages/Pricing.js` | Plans, weekly recap, Stripe checkout or waitlist. Redirects away in the `self-hosted` edition, which bills nobody |
| `/settings` | `pages/Settings.js` | Auto-apply, auto-sync, digest hour |
| `/account` | `pages/Account.js` | Profile, usage, GDPR export and delete |
| `/setup` | `pages/Setup.js` | Read-only briefing on the env vars. Not a form: the OAuth app is instance config. Edition-aware: the own-project guide (6 steps, including "Publier l'application") self-hosted, the operator one (5 steps) otherwise, plus the reachable providers read from `GET /api/providers` |
| `/auth/callback` | `pages/AuthCallback.js` | Exchanges the OAuth code for a session token |

`/emails` and `/triage` redirect to `/inbox`.

### Data access and state

- **Every HTTP call lives in `src/services/api.js`**, grouped into `authService`,
  `emailService`, `labelService`, `searchService`, `snoozeService`, `protectService`,
  `aiService`, `senderService`, `accountService`, `subscriptionService`,
  `billingService`, `ruleService`, `configService`, `waitlistService`. A component
  never imports axios.
- The axios instance attaches `Authorization: Bearer <localStorage.accessToken>` on
  request, and on any 401 clears `accessToken` + `userEmail` and bounces to `/`.
- Two shared stores, and only two. `contexts/EmailContext.js` holds the inbox:
  emails, senders, subscriptions, suggestions, stats, pagination, with a 5 minute
  cache and a 5 minute sync throttle. Consume it with `useEmails()`.
  `contexts/InstanceContext.js` holds the deployment: one `GET /api/config/status`
  at boot, read by App, the header, Pricing and Setup through `useInstance()`
  (`loading`, `error`, `reload`, `isConfigured`, `billingOn`, `edition`,
  `selfHosted`). Never probe the instance from a component: App and Pricing each
  called it separately and could disagree about the same instance for a few
  hundred milliseconds. Everything else is local `useState`.
- **The edition is a frontend concern too.** A `self-hosted` instance bills nobody,
  so the header drops its pricing entry and `/pricing` redirects instead of
  rendering a page with no offer. The SPA still hardcodes no provider: the connect
  surfaces render whatever `GET /api/providers` returns.
- Session identity lives in `localStorage` (`accessToken`, `userEmail`). Gamification
  state lives in `localStorage` too (`ui/streak.js`, key `mailsorter_gamify`).

### Design system

- Tailwind only, configured in `frontend/tailwind.config.js`: one accent (`brand`, deep
  ink blue), cool neutrals (`ink`), a three-step shadow scale, five named animations.
  No second accent, no gradient, no colored glow.
- Reusable component classes are declared once in `src/index.css` under
  `@layer components`: `.btn`, `.btn-primary`, `.btn-secondary`, `.btn-ghost`,
  `.btn-danger`, `.card`, `.input`, `.chip`, `.skeleton`. Use them instead of respelling
  the utility chain.
- **Icons are hand-written SVG in `src/ui/icons.js`.** No icon package. Add a new icon there.
- `ui/Toast.js` provides `useToast()` with `.success` / `.error` / `.info` / `.action`
  (the action variant is how undo is offered). `ui/Spinner.js` and `ui/cn.js` round it out.
- `ui/SnoozeMenu.js` owns the whole "reporter" affordance (trigger, preset menu, custom
  date picker) because the reader and the inbox bulk bar both need it and the popover
  has real behaviour to get right. It calls back with `{ preset }` or `{ wakeAt }`,
  mirroring the API contract.
- **Tailwind class names must be statically present in the source.** Never build a class
  by string concatenation; the Inbox action tokens are spelled out as full literal classes
  for exactly this reason.

## Key Conventions

### Language

| Surface | Language |
|---|---|
| Go and JS code, identifiers, comments | English |
| `docs/API.md`, `.env.example`, `CLAUDE.md` | English |
| `README.md`, `docs/ARCHITECTURE.md`, `docs/ROADMAP.md`, `BACKLOG.md` | French |
| All user-facing UI strings | French |

Keep writing each surface in its existing language. Do not translate an existing file
as a side effect of another change.

### Go

- Package-level doc comments are load-bearing here: every pure package opens with a
  paragraph explaining what it owns and *why it is pure*. Write one for any new package.
- Comments explain the decision, not the mechanics. The good ones in this repo say what
  breaks without the line (see `analytics.js` "the optional chaining is load-bearing",
  `nginx.conf` on index.html caching). Match that register.
- Errors: wrap with `fmt.Errorf("...: %w", err)`, compare with `errors.Is`. `errReauthRequired`
  in `respond.go` is the model for a typed sentinel that maps to a specific status.
- Constants over string literals for any vocabulary: rule fields and operators
  (`rules`), action verbs (`rules`, `protect`), ledger sources (`ledger.go`), snooze
  presets (`snooze`), plan names (`billing.go`). Add to the constant block, never
  hardcode a new bare string.
- Timeouts everywhere: `context.WithTimeout` on Mongo calls, explicit server timeouts in
  `main.go`. Match the surrounding durations rather than inventing new ones.

### Go tests

- Standard library only. `testing` + `net/http/httptest`. No testify, no mocks framework,
  no golden-file harness.
- Table-driven with `t.Run(tc.name, ...)`, cases as an anonymous struct slice or a
  `map[string]string` for simple in/out pairs. `rules_test.go` is the reference.
- Failure messages state the call and both values: `t.Errorf("f(%q) = %q, want %q", in, got, want)`.
- Pure packages get exhaustive unit tests. The `api` package gets `httptest` tests that
  mount the *real* router (`newRoutedTestServer` in `routes_integration_test.go`) and
  point Mongo at a dead address on purpose so the degraded paths are exercised for real.
- A bug fix ships with the test that fails without it. Every B-item in `BACKLOG.md` did.

### React

- Function components, hooks, no class components. Default export per page, named
  exports for hooks and helpers.
- One file per route under `pages/`. Cross-route UI goes to `components/` or `ui/`.
- Files: PascalCase for components (`EmailReader.js`, `Toast.js`), camelCase for plain
  modules (`api.js`, `cn.js`, `analytics.js`, `streak.js`, `icons.js`).
- `track(event, props)` from `lib/analytics.js` for product events. Properties must carry
  no personal data: no email address, no sender, no subject. Counts and enum labels only.
  Never add a manual pageview: the tracker hooks the History API already.
- Untrusted HTML (email bodies) goes through dompurify before rendering.

### Naming

| Thing | Convention | Example |
|---|---|---|
| Go packages | one lowercase word, no underscores | `protect`, `snooze` |
| Go files | lowercase, `_` only for a qualifier | `ai_handlers.go`, `account_data.go` |
| Mongo collections | snake_case plural | `sorting_rules`, `action_log` |
| BSON fields | camelCase | `userId`, `wakeAt`, `messageId` |
| JSON API fields | camelCase, identical to BSON | `autoApplyRules`, `nextPageToken` |
| Routes | plural nouns, kebab in multiword | `/api/ai/analyze-async`, `/api/smart-labels` |
| React components | PascalCase | `ConfidenceRing` |
| Env vars | SCREAMING_SNAKE | `MISTRAL_MAX_RETRIES` |

### Git

- Branches are `claude/<topic>` off `main`, merged through a GitHub PR. Never commit
  straight to `main`.
- Commit subjects: `type(scope): imperative summary`, lowercase, no trailing period.
  Types in use: `feat`, `fix`, `docs`, `chore`, `test`, `style`, `security`, `hardening`,
  `ui`, `seo`, `deploy`. Scopes in use: `api`, `ai`, `gmail`, `frontend`, `nginx`,
  `snooze`, `config`, `models`, `env`, `header`, `backlog`, `roadmap`. The scope is
  optional and often omitted on a cross-cutting change.
- Show the diff and the commit message before committing. Do not push or deploy unasked.

## Configuration

`.env.example` is the documented contract; read it before touching any env plumbing.
`docker-compose.yml` maps the host vars onto container vars (notably `BACKEND_PORT` to
`PORT`, and `MONGO_ROOT_*` into a composed `MONGODB_URI`).

| Var | Required | Notes |
|---|---|---|
| `ENCRYPTION_KEY` | yes | min 32 chars. Boot **refuses** the two known placeholders. Changing it after the fact makes every stored secret unreadable |
| `GMAIL_CLIENT_ID` / `GMAIL_CLIENT_SECRET` / `GMAIL_REDIRECT_URL` | yes | One OAuth app for the whole instance. Env is the source of truth; the `gmail_config` document is a read-only legacy fallback. Must be a **Web application** client, not Desktop |
| `MISTRAL_API_KEY`, `MISTRAL_MODEL`, `MISTRAL_MAX_RETRIES` | AI features | defaults `mistral-large-2411`, 2 extra retries |
| `MONGODB_URI` / `MONGO_ROOT_USERNAME` / `MONGO_ROOT_PASSWORD` / `PORT` / `BACKEND_PORT` | see `.env.example` | direct `go run` reads `MONGODB_URI` and `PORT` |
| `STRIPE_SECRET_KEY`, `STRIPE_PRICE_ID`, `STRIPE_WEBHOOK_SECRET`, `APP_BASE_URL` | optional | empty key keeps the waitlist CTA instead of checkout |
| `ALLOWED_ORIGINS` | optional | comma separated. Empty falls back to localhost:3000, localhost, mailsorter.sohbi.dev. No rebuild needed |
| `EDITION` | yes (defaults to `self-hosted`) | `self-hosted` or `hosted`. Boot **refuses** anything else. Decides which providers `internal/provider` offers: the Gmail API and Proton exist only in `self-hosted` |
| `BUILD_VERSION`, `DIGEST_HOUR_UTC` | optional | reported by `/health` and `/metrics`; digest default 07:00 UTC |
| `REACT_APP_API_URL`, `REACT_APP_UMAMI_WEBSITE_ID` | build args | **inlined into the static bundle at image build time.** Leave `REACT_APP_API_URL` unset so it defaults to `/` and the SPA calls the API same-origin through the nginx proxy |

Secrets never go in a versioned file. No credential belongs in this document.

## Deployment

Dokploy (self-hosted, `infra.sohbi.dev`) running this repo's `docker-compose.yml` as a
Compose application. Pushing to `main` triggers a redeploy. The public domain routes to
the `frontend` service on port 80; the frontend nginx proxies `/api` to `backend:8080`,
so there is no cross-origin call in production.

Deploy checklist: `docs/ROADMAP.md`, section "Checklist de deploiement".

Before claiming a deploy works: `curl -w "%{http_code}" https://mailsorter.sohbi.dev/health`
and read the body (it reports `version` from `BUILD_VERSION`, and `checks.mongo`).

## Where Things Live

| Looking for | Path |
|---|---|
| Entry point, wiring, shutdown | `backend/cmd/server/main.go` |
| The full API surface | `backend/internal/api/routes.go` |
| Response and error helpers | `backend/internal/api/respond.go` |
| Auth, rate limit, request id | `backend/internal/api/middleware.go` |
| Every data struct | `backend/internal/models/models.go` |
| Collections and indexes | `backend/internal/database/database.go` |
| Env contract | `.env.example`, `backend/internal/config/config.go` |
| AI cost control (cache, batching, quota) | `backend/internal/api/analysis.go`, `backend/internal/api/account.go` |
| GDPR catalog | `backend/internal/account/account.go` + `backend/internal/api/account_data.go` |
| Outbound request policy (SSRF guard) | `backend/internal/egress/egress.go` |
| Every frontend HTTP call | `frontend/src/services/api.js` |
| Shared frontend state | `frontend/src/contexts/EmailContext.js` (inbox), `frontend/src/contexts/InstanceContext.js` (edition, billing) |
| Design tokens | `frontend/tailwind.config.js`, `frontend/src/index.css` |
| SPA serving and cache headers | `frontend/nginx.conf` |
| CI | `.github/workflows/ci.yml` |

## Documentation Map

Do not duplicate these into this file. Point at them.

| Doc | Holds | Trust |
|---|---|---|
| `README.md` | Product pitch, the engineering decisions worth reading, quickstart | Current |
| `docs/API.md` | Every endpoint, payload, error, auth rule | Current, English |
| `docs/ARCHITECTURE.md` | Diagrams, auth flow, sync flow, security, observability, workers | Security/observability/workers current; the "Composants" endpoint and collection lists are stale |
| `docs/ROADMAP.md` | The 12 delivered phases and the deploy checklist | Current |
| `BACKLOG.md` | Last audit: every finding, all closed | Historical record, useful for "why is it like this" |

## Known Gotchas

- **`REACT_APP_*` is frozen at image build time.** A runtime env var change does nothing to
  the SPA. This already caused one production incident (PR #15).
- **`ENCRYPTION_KEY` is not rotatable in place.** Changing it orphans every AES-GCM value at
  rest, which now includes every user's Gmail token: each one has to reconnect their account.
  It also invalidates every session token in the wild.
- **A credential is sealed by `api/tokens.go` or it is not stored.** That file owns
  the AES-256-GCM machinery for every secret Mailsorter keeps, not just Google's:
  `sealSecret` / `openSecret` are the generic pair, and the IMAP app password in
  `mail_accounts` goes through them. `openSecret` differs from `openToken` in one
  way that matters: it REFUSES an unsealed value instead of treating it as legacy
  plaintext, because no app password was ever stored in the clear, so an unprefixed
  one is corruption or tampering and handing it back would send it to a mail server.
- **Adding a per-user collection means two catalogs, not one.** `account.Datasets()`
  makes erasure reach it (an account deleted while its app password stays on file is
  the exact bug the last audit found), and `account.SecretFields` says which of its
  columns must be stripped from the export. Export and erasure share the catalog, so
  a new collection is exportable BY DEFAULT: if it holds a credential, say so there.
- **Never write a Gmail token to Mongo directly.** `api/tokens.go` owns both directions:
  `sealToken` on the way in, `openToken` on the way out. A value without the `enc:v1:`
  prefix is a legacy plaintext token, re-sealed in place the first time it is read, so the
  migration needs no backfill. A prefixed value that fails to decrypt is NOT treated as
  plaintext: it becomes `errReauthRequired` (401) so the user reconnects.
- **A message listing picks its own payload size.** `gmail.FieldsFull` downloads every MIME
  part and is only for callers that read `Email.Body` (the sync, the rule engine).
  `gmail.FieldsMetadata` is for anything that only renders sender, subject and snippet, and
  its headers must be listed in `metadataHeaders` or Gmail returns none at all. Fetches run
  8 at a time and the result keeps the listing order; that order is the mailbox order and
  the page token only makes sense against it.
- **An IMAP listing must PEEK or it marks the whole inbox read.** A plain
  `BODY[...]` fetch sets `\Seen` on every message it touches, so a listing without
  `Peek: true` empties the user's unread count just by rendering the screen, with
  nothing in the ledger to explain it. `internal/imap/list.go` sets it, and a test
  reads the flags back OFF THE SERVER after listing rather than trusting the
  struct it just built. The same fetch asks for the two unsubscribe headers by
  name rather than for `BODY[HEADER]`, which on marketing mail is routinely
  larger than the text of the message.
- **`analysis_cache` is shared across all users.** It is keyed on sender plus subject only, so
  never cache anything user-specific through it.
- **`docker-compose.yml` publishes no ports.** Dokploy routes through its own proxy, so
  the deployment file binds nothing to the host. Local runs need `compose.local.yml` on
  top, which is what `make up` does. It is deliberately NOT named
  `docker-compose.override.yml`, since Compose would merge that into the deployment too.
- **No handler knows Gmail's label vocabulary.** `"INBOX"`, `"TRASH"`, `"UNREAD"` and
  `"STARRED"` appear in exactly two files: `internal/mailbox/mailbox.go` (the translation)
  and `internal/gmail/gmail.go` (the client). They used to appear 51 times across the tree.
  Two traps the table encodes: untrashing must BOTH restore `INBOX` and drop `TRASH`, and
  read state is the ABSENCE of `UNREAD`, so marking read removes rather than adds.
- **`mailbox.Parse` accepts every spelling already in circulation.** `delete` and `trash`
  are one act, `read` and `markRead` another, and both spellings sit in `action_log` rows
  written over the life of the app. Dropping an alias would silently stop the undo history
  resolving for every entry written the other way.
- **The edition is not packaging, it is a capability gate.** `EDITION=hosted` can never
  reach the Gmail API: a shared OAuth client is capped by Google at 100 authorizations for
  the lifetime of the Cloud project, non-resettable. Hosted reaches Gmail over IMAP with an
  app password instead. `internal/provider` holds that rule as data and a test enforces it,
  so do not special-case a provider in a handler: add or fix its route in the catalog.
- **Any outbound request whose URL is not a constant goes through `internal/egress`.**
  There is exactly one today: the RFC 8058 one-click unsubscribe, whose address
  comes from the `List-Unsubscribe` header of a received email, so a stranger picks
  it. Unguarded, that is a server-side request forgery: the server POSTs to its own
  loopback API, to the datastore on the Docker bridge, or to 169.254.169.254 for the
  host's credentials. The scheme check must be https-only, the address check must
  run from the dialer's `Control` hook (not before the lookup, or DNS rebinding
  walks past it), and every redirect hop must be re-judged. Mistral and Stripe are
  safe for one reason only: their base URL is a constant.
- **An IMAP UID is NOT a message id, and `mailbox.Ref` is how the two stay apart.**
  Over the Gmail API a message id names a message on the account and never
  changes. Over IMAP a UID names a message inside one folder, so a move ends its
  validity and the same mail has a different UID in its new folder. Both are
  strings, so a bare string through a shared interface makes them look alike and
  the failure is silent: the action lands on a different message, succeeds, and
  is journaled as if it had done what was asked. Hence `Ref`, built by
  `OnAccount` or `InFolder`, and two guards that both ship with the test that
  fails without them: the Gmail adapter refuses a folder-scoped ref, and
  `imap.Client` refuses an account-wide one rather than defaulting to the inbox.
  `parseUID` refuses anything that is not entirely digits for the same reason: a
  Gmail id like `18c8c1f2a3b4d5e6` starts with digits, and a parser that stopped
  at the first letter would act on UID 18.
- **`X-User-Email` is both the identity header and the `userId`.** There is no account
  entity, which is exactly what blocks multi-account Gmail (`docs/ROADMAP.md`).
- **`GET`/`POST /api/smart-labels` have no UI.** They work and are tested; they are
  deliberately unwired. Do not delete them, do not assume they are reachable from the
  app either. (`GET /api/stats/digest` and `GET /api/labels` used to be in this list;
  both are now wired, to the digest preview in Réglages and to the rules/reader label
  pickers.)
- **Size and color modifiers in `index.css` must come AFTER the color variants.**
  Every `.btn-*` variant does `@apply btn`, which copies `.btn`'s height and padding
  into itself. At equal specificity the last rule wins, so `.btn-sm` / `.btn-icon`
  placed above the variants were silently overridden: an icon-only button kept 16px
  of padding inside a 32px box and its icon was squeezed to zero width, which is how
  the email reader's entire toolbar rendered blank.

## Post-Change Checklist

Structural change (new endpoint, new collection, new background loop, changed convention):

- [ ] `cd backend && go vet ./... && go build ./... && go test -race ./...` all green, output shown
- [ ] `cd frontend && CI=false npm run build` compiles, if the frontend was touched
- [ ] `docs/API.md` updated for any route added, removed or reshaped
- [ ] `frontend/src/services/api.js` mirrors the new route
- [ ] `account.Dataset*` and `datasetCollection` updated for any new user-owned collection
- [ ] `EnsureIndexes` updated if a new hot query appeared
- [ ] `.env.example` updated for any new env var, with the comment explaining the default
- [ ] `CLAUDE.md` updated if a package, convention, command or gotcha changed
- [ ] No em dash, en dash, ellipsis character or curly quote introduced anywhere

Skip the docs for: bug fixes inside an existing handler, copy tweaks, dependency bumps,
adding a rule or a test that uses existing patterns.

## Verification Discipline

A completion claim without a command run in the same message is not a completion claim.
Concretely, for this repo:

- Backend change: paste the `go test -race ./...` tail.
- Frontend change: paste the `npm run build` result, then verify in a browser
  (chrome-devtools MCP), not with curl. Recette means navigating the app as a user.
- Deployment or container restart: `curl -w "%{http_code}" .../health` plus a log read.
- Anything you cannot verify here, say so explicitly and name what blocks it.
