# Mailsorter

AI-assisted email triage: the model reads, understands and sorts an inbox, and every
action stays a proposal the user validates (reversible on the Gmail API). Go REST
API + React SPA + MongoDB, deployed with Docker Compose on a self-hosted Dokploy.
The public instance, [mailsorter.sohbi.dev](https://mailsorter.sohbi.dev), runs the
hosted edition (`EDITION=hosted`); anyone can run the self-hosted one.

Solo-developer project. Production is live. The 13 roadmap phases (0 to 12) are
delivered (`docs/ROADMAP.md`, now a history); what came after them (IMAP, the two
editions, multi-provider AI, password accounts) is described in this file and in
the README. The last audit closed every item it opened (`BACKLOG.md`).

## Tech Stack

| Layer | Choice | Version pinned in |
|---|---|---|
| Backend | Go, `net/http` + `gorilla/mux` router | `backend/go.mod` (`go 1.26`), `backend/Dockerfile` (`golang:1.26-alpine`) |
| Module path | `github.com/nohe-sohbi/mailsorter/backend` | `backend/go.mod` |
| HTTP deps | `gorilla/mux` 1.8.1, `rs/cors` 1.10.1 | `backend/go.mod` |
| Data | MongoDB 7, official `mongo-driver` 1.13.1 | `docker-compose.yml`, `backend/go.mod` |
| Google | `golang.org/x/oauth2` 0.38.0, `google.golang.org/api` 0.154.0 (Gmail v1) | `backend/go.mod` |
| IMAP | `github.com/emersion/go-imap/v2` v2.0.0-beta.8, pinned, plus its companion `go-message` v0.18.2 (already in the graph, promoted to direct for MIME body parsing) | `backend/go.mod` |
| SMTP | the standard library's `net/smtp`, no dependency: the recap of an IMAP mailbox leaves through its sending server | `backend/internal/smtpsend` |
| Passwords | `golang.org/x/crypto` v0.58.0, for bcrypt on account passwords | `backend/go.mod` |
| AI | Mistral, OpenAI-compatible, Anthropic and Ollama (local), hand-rolled HTTP clients behind a fallback registry | `backend/internal/ai/registry.go` |
| Billing | Stripe REST, hand-rolled client (no SDK) | `backend/internal/billing/stripe.go` |
| Frontend | React 18.2, react-router-dom 6.21, axios 1.6, dompurify 3.0 | `frontend/package.json` |
| Build (front) | Create React App (`react-scripts` 5.0.1) + Tailwind 3.4 | `frontend/package.json` |
| Serving | nginx alpine, SPA + `/api` reverse proxy | `frontend/nginx.conf` |
| CI | GitHub Actions, Go (version read from `backend/go.mod`) + Node 20 | `.github/workflows/ci.yml` |
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

## Changing Behavior: the acceptance scenarios

Behavior is validated, not just code: a change starts from short
French sentences the maintainer agreed to, each proven by an acceptance scenario
(`backend/internal/api/scenarios_*_test.go`, running on the harness in
`acceptance_*_test.go`), and `make scenarios` is the proof. The scenarios are
the definition of done: never edit one to make a change pass. If a scenario
looks wrong, say so; never work around it.

Rules for the sentences in this project:

- If the change moves mail out of the inbox, one sentence covers a protected
  sender.

## Code Quality Standards

1. **Keep the decision logic pure.** Anything that decides (does this rule match,
   is this snooze due, may this sender be archived, what does the digest say) goes in
   an I/O-free package under `backend/internal/` and is tested exhaustively. Only the
   `api` package talks to Mongo, the mailboxes (Gmail, IMAP), the AI providers or
   Stripe. Never put a `bson.M` or an `http.Request` in a pure package.
2. **Fail closed on security, fail fast on config.** `config.Validate()` refuses to boot
   on a missing, short (under 32 characters) or known example `ENCRYPTION_KEY` (the
   one an earlier README showed only warns: refusing it would strand instances
   already running on it), and on an unknown `EDITION` or `UI_THEME`. The auth middleware strips a client-supplied
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
   Both take a `mailbox.Mailbox` and a `mailbox.Ref`, not a client and a bare id. On a
   ported path, open a session and let it build both: `session.Mailbox()` and
   `session.RefFor(ctx, id)` know the transport, which is the thing a handler holding a
   string from the client does not. On a Gmail-only path, `h.mailboxOf(client)` and
   `mailbox.OnAccount(id)`.
5. **Cheap paths before expensive ones.** Deterministic rules cost nothing and run
   without the model (at sync time with `autoApplyRules`, or on `POST /api/rules/apply`;
   the analysis does not consult them); sender auto-pilot and the shared analysis cache
   run before any model call; cache hits and auto-pilot do not burn quota. New AI work
   must justify why it cannot be a rule or a cache hit.
6. **No em dashes, en dashes, ellipsis characters or curly quotes in anything you write**
   (code, comments, docs, commit messages, UI copy). Plain ASCII punctuation only. The
   repository carries none outside docs/superpowers and .claude:
   `git grep -nP '[\x{2014}\x{2013}\x{2026}\x{201C}\x{201D}\x{2018}\x{2019}]'` proves it.

## Project Structure

```
backend/
  cmd/server/            main: config -> db -> indexes -> services -> router -> serve
  internal/api/          the ONLY I/O layer: handlers, routes, middleware, schedulers
  internal/{account,activity,attachment,digest,egress,mailbox,mailer,metrics,
            mirror,protect,provider,rules,schedule,search,snooze,unsubscribe}/
                         pure logic, no I/O, heavily unit-tested
  internal/{ai,billing,gmail,imap,smtpsend}/  outbound clients (AI providers, Stripe,
                         Gmail, IMAP, and SMTP for the recap of an IMAP mailbox)
  internal/{auth,crypto,config,database,models}/  cross-cutting primitives
  scripts/scenarios.sh   runs the acceptance scenarios against a throwaway MongoDB
frontend/
  src/pages/             one file per route (13 routes)
  src/components/        shared non-route components (Header, EmailReader,
                         PublicFooter and LegalLayout for the logged-out surface,
                         MailboxBriefing for the connect screen, which asks for an
                         address and a mailbox credential)
  src/contexts/          EmailContext: the shared inbox cache.
                         InstanceContext: what this deployment is (edition, billing, configured)
  src/styles/hotel.css   the Grand Hotel theme (UI_THEME=hotel): tokens under
                         .theme-hotel, day and night, ht- primitives, motion
  src/styles/hotel-app.css the theme's dashboard half: the classic tokens
                         re-pointed at the hotel palette, the shape language,
                         and the hd- classes. Loaded on demand, never by classic
  src/ui/hotel/          the theme's drawings and bricks: HotelFacade, Door, Vault,
                         KeyTag, ElevatorPanel, Plaque, Emblem, useHotelFonts,
                         and HotelTheme.js (the switch: HotelThemeProvider, useHotel)
  src/components/landing/ the Grand Hotel landing's sections (hl- classes) and
                         features.js, what the landing may promise (GMAIL_ONLY)
  src/components/hotel/  the Grand Hotel dashboard (hd- classes): the lift-panel
                         header, floors.js, PageHead, the inbox bits, the lift doors
  src/components/inbox/  the AI's triage plan on /inbox: TriagePlan.js (the panel
                         and useTriagePlan, which applies, ignores, undoes and
                         settles) and planGroups.js (pure: one group per sender
                         and verdict, the "sure" threshold, the held order)
  src/services/api.js    every HTTP call in the app, grouped by service object
  src/ui/                design-system primitives (icons, Toast, Spinner, Modal, SnoozeMenu, cn, streak, avatar)
  src/lib/analytics.js   Umami tracker injection + track()
  src/lib/waitlist.js    whether THIS browser joined the Pro waitlist (landing + pricing share it)
  nginx.conf             SPA fallback, immutable /static, no-cache index.html, /api proxy
docs/                    ARCHITECTURE.md, API.md, ROADMAP.md, USER-STORIES.md,
                         design/moodboard.html, assets/, superpowers/ (design plans)
mongo-init/init-db.js    creates nothing on purpose: EnsureIndexes owns every index
```

## Build, Run, Test

All commands verified against this working copy.

| Goal | Command | Notes |
|---|---|---|
| Full stack, containers | `make up` (then `make logs`, `make down`) | app on :3000, API on :8080. Uses `docker-compose.yml` PLUS `compose.local.yml`, which publishes the host ports the deployment file omits. A bare `docker compose up` binds nothing |
| Rebuild images | `make build` | `docker compose build` |
| Nuke containers + volumes + node_modules + binaries | `make clean` | destructive |
| Backend tests | `make test` (= `cd backend && go test ./...`) | all green (the acceptance scenarios are separate, see below) |
| Backend tests as CI runs them | `cd backend && go test -race ./...` | what `.github/workflows/ci.yml` runs |
| Acceptance scenarios | `make scenarios` | runs `backend/scripts/scenarios.sh`: the scenarios against a throwaway MongoDB (Docker, or a downloaded `mongod` when there is no Docker), then prints the French report. CI runs it in the `scenarios` job and publishes the report as the job summary |
| Backend vet + build | `cd backend && go vet ./... && go build ./...` | both clean |
| Backend alone | `make backend` (build + run on :8080, exports `.env` first) or `cd backend && go run cmd/server/main.go` with `ENCRYPTION_KEY` exported | needs a reachable Mongo (`make up` publishes it on 127.0.0.1:27017) |
| Frontend dev server | `make frontend` or `cd frontend && npm install && npm start` | :3000, proxies nothing, hits :8080 directly |
| Frontend production build | `cd frontend && CI=false npm run build` | verified: "Compiled successfully" |
| Health | `curl -w "%{http_code}" http://localhost:8080/health` | 200 healthy, 503 when Mongo is unreachable |

Notes that will bite you:

- `go` may not be on the default PATH. On the maintainer's machine the toolchain is in
  `$HOME/.local/go-sdk/bin` (Go 1.26), so `export PATH="$HOME/.local/go-sdk/bin:$PATH"`.
  `go.mod`, the Dockerfile and CI target 1.26; with the default `GOTOOLCHAIN=auto` an
  older local Go downloads 1.26 on first use.
- **The Dockerfile's Go tag must follow `go.mod`.** The official golang images set
  `GOTOOLCHAIN=local`, so an image older than the `go` line refuses to build while CI,
  which downloads the newer toolchain on its own, stays green. That is how #155 broke
  the deploy with every check passing. The `docker` CI job builds both images to catch it.
- **There are zero frontend tests.** `npm test` exists (`react-scripts test`) but no
  `*.test.js` file does. CI does not run it. Frontend verification is a real build plus
  browser navigation, never a unit test claim.
- CI (`.github/workflows/ci.yml`) runs, on every push to `main` and every PR: backend
  `go mod download`, a `gofmt -l .` gate (any listed file fails the job), `go vet ./...`,
  `go build ./...`, `go test -v -race ./...`; the acceptance scenarios
  (`go vet -tags acceptance ./internal/api/`, then `scripts/scenarios.sh` against a
  MongoDB service, report published as the job summary); frontend `npm install` +
  `CI=false npm run build`; and `docker build` of both images, as Dokploy builds them.
  Nothing else: no linter beyond `go vet`, nothing on the frontend code itself.

## Backend Architecture

Read `docs/ARCHITECTURE.md` for the overview, the sign-in, sync and analysis flows,
security and the background loops, in short. Read `docs/API.md` for every endpoint
payload. This section is the map, not a copy of them. When they disagree, the code
wins.

### The pure core rule

`internal/api` is the only package allowed to touch Mongo, the mailboxes, the AI
providers or Stripe. Everything below is pure by construction, and each one says so
in its package doc:

| Package | Owns | Key entry points |
|---|---|---|
| `rules` | Deterministic AI-free triage engine, plus the portable form of a ruleset | `Matches`, `Validate`, `Preview`, `Reorder`, `BuildExport`, `ValidateImport`, field/operator/action constants |
| `protect` | VIP safety net: which senders can never be auto-archived, trashed or deleted, nor, on IMAP where a label is a folder, auto-filed | `Allowed`, `AllowedOnFolders`, `ActionArchive/Trash/Delete/Label` |
| `snooze` | Preset ("ce soir", "demain", "weekend") to a concrete wake time, and the guard on a hand-picked one; what an attempt at bringing a report back leaves on it (done, cancelled when the mail is gone from where it waited or the user undid the report, retried and counted, given up at the limit, or left uncounted), and the due reports grouped by account, one session each | `Resolve`, `ValidateWake`, `MaxHorizon`, `Preset*` constants, `Settle`, `Outcome` (`Woken`, `Gone`, `Failed`, `Untried`, `Undone`), `ByAccount`, `Status*` |
| `search` | Gmail's query language as this app uses it: what makes a saved search valid, its identity, its default name, and what a query string actually CONSTRAINS | `Normalize`, `Key`, `SuggestName`, `MaxPerUser`, `Parse`, `Criteria` |
| `schedule` | "Is this periodic work due?" | `Due(last, now, interval)` |
| `mirror` | What the local copy of an IMAP inbox should hold, given every UID the inbox holds now: what goes, what Mailsorter keeps hidden for "Annuler", what is back, and what a sync must leave alone (a move in flight, a row flagged after the listing started) | `Judge`, `Row`, `Rules`, `Verdict` |
| `activity` | Action ledger rows to a 7-day series plus breakdowns | `Row`, `DayCount` aggregation |
| `digest` | The 7-day recap rendered into subject + text + HTML, the HTML in the instance's look (classic or Grand Hotel, email-safe) | `Digest`, `Render`, `RenderStyle`, `Style*` |
| `mailer` | The recap as an RFC 5322 multipart message (bodies in quoted-printable, so no line a sending server may refuse; a `Message-ID` on the domain of the sending address, its unique part drawn by the caller), its base64url encoding for Gmail's send API, and daily-due arithmetic | `Message`, `Build`, `MessageID`, `ForGmail`, `DueAt` |
| `account` | The single catalog of user-owned data driving BOTH export and erasure, and which of its fields are secrets | `Dataset*`, `SecretFields`, `RedactUser` |
| `metrics` | In-process bounded request meter (method x status class, latency) | `Registry` |
| `provider` | The mailbox catalog: which provider is reachable by which transport, with which credential, in which EDITION, and what can block it. Routes are ordered best first; `PickIMAP` is the route an address and a password can connect to (the first IMAP route of the edition that names its server), which is what connecting a mailbox uses. `SendingServer` is where a connected IMAP mailbox sends from: the server recorded on its row, or that route's in the catalog for a row from before | `All`, `ForEdition`, `Lookup`, `Detect`, `PickIMAP`, `SendingServer`, `Edition*`, `Transport*`, `Auth*`, `Cap*`, `Blocker*` |
| `mailbox` | The provider-neutral verb vocabulary, how a message is NAMED on each transport, and the translation per transport. The ONLY place that knows Gmail's system label ids, and IMAP's flags and special folders | `Action*`, `Parse`, `Destructive`, `Mutation`, `Ref`, `OnAccount`, `InFolder`, `Mailbox`, `GmailLabels`, `GmailLabelsFor`, `GmailIsRead`, `GmailAfter`, `IMAPOpFor`, `IMAPOpsFor`, `IMAPIsRead`, `IMAPIsStarred`, `Folder*`, `Flag*`, and `MatchLabel` / `LabelKey`: a label the model proposed matched with one the user has (case, accents and a plural aside, never a part of a name) |
| `egress` | Where the server may send a request of its own. Guards the one-click unsubscribe, the only outbound URL a stranger chooses | `Parse`, `Allowed`, `AllowedIP`, `ErrNotHTTPS`, `ErrNoHost`, `ErrPrivateAddress` |
| `unsubscribe` | The `List-Unsubscribe` (RFC 2369) and `List-Unsubscribe-Post` (RFC 8058) headers. Shared by both transports: the headers belong to the message, not to how it was fetched | `Parse`, `SplitAngleList`, `Links` |
| `attachment` | Which parts of a mail the reader lists as its files, and under which name, on both transports: a named part, an unnamed one declared an attachment (`piece-jointe`) or a forwarded mail (`message.eml`), except an image the mail's HTML shows through `cid:`. And the cleaning of a file name (no directory, control or bidirectional control character), which the list and the download's `Content-Disposition` share. `InlineImage` is a picture an `<img src="cid:...">` of the HTML draws (raster types only, never SVG), which the reader returns as `inlineImages` and the download serves so the screen can draw it, the `MaxDrawn` (30) first in the HTML (`Shown.FirstDrawn`): no other is served. `ListedWithoutHTML` is the rule read off a `BODYSTRUCTURE`, for the filter "Pièces jointes" (`imap/state.go`): an inline image with a `Content-ID` is taken for a picture the HTML draws; `WithoutHTMLVersion` is stored with each count, and a count made with another version is made again by the sync | `Listed`, `InlineImage`, `ListedWithoutHTML`, `WithoutHTMLVersion`, `Part`, `ShownBy`, `Shown`, `SafeName`, `DispositionType`, `Unnamed`, `UnnamedMail` |

The outbound clients and primitives:

| Package | Responsibility |
|---|---|
| `ai` | One `Analyzer` interface over Mistral, OpenAI (and any OpenAI-compatible server), Anthropic and Ollama, and the `Registry` that tries them in ascending priority and moves to the next one on a transient error (429, 5xx, timeout, unreachable). The Mistral, OpenAI and Anthropic clients retry 429 and 5xx with exponential backoff and jitter and fail fast on other 4xx; only Mistral also honours `Retry-After`. Ollama does not retry; it is asked `think: false` and names a slow model `ollama timeout`. Batches of 8 are cut by the caller (`analysis.go`). Default models: `mistral-small-latest`, `gpt-6-luna`, `claude-haiku-5-5`, `qwen3.5:9b` |
| `gmail` | Gmail v1 wrapper. `retry.go` wraps every call with the same backoff policy |
| `imap` | The IMAP transport: every mailbox of the hosted edition, and the IMAP mailboxes of a self-hosted one. Connect (TLS or STARTTLS, never cleartext, certificate verified against the system roots), resolve the server's special folders, list a page, fetch one message, read one of its files, apply a mutation, and `Abort` a connection whose caller's time is up (go-imap waits for an answer with no time limit). A label is a folder there: `Destination` finds the user's folder of that name or makes it (subscribed), and refuses a special one (`ErrNotOwnFolder`); `OwnFolders` lists the user's folders, special ones left out by special-use attribute or, when the server marks none, by name (`notOwn`). Folder names go through the namespace read at connect (`namespace.go`, NAMESPACE when announced): the user reads them without the personal prefix and with `/` between levels ("INBOX.Projets.Maison" on Courier reads "Projets/Maison"), a folder Mailsorter makes goes under the prefix with the server's delimiter, the special ones are known by name under the prefix too, and a folder under another user's or a shared prefix is never the user's. A folder is made only under a name `cleanName` and `newFolderName` accept (no control character, `*` or `%`, no empty, `.` or `..` level, 100 characters a level and 200 in all, the server's delimiter inside a level turned into `-`, a first level that is the user's), and at most `maxNewFolders` (10) per connection (`ErrTooManyNewFolders`). `FilingName` is what the analysis stores on a label proposal: the user's folder named alike (`mailbox.MatchLabel`), or the cleaned new name; the model is told no folder. `list.go` mirrors the Gmail listing's payload discipline; `state.go` is what a sync reads besides it (the flags and the size of the whole folder, the attachments of a batch, `hasAttachment` on a BODYSTRUCTURE), `halves.go` reads a batch so that one unreadable message fails only itself (`ReadInHalves`). `parts.go` is how both the reader (`fetch.go`) and the download (`attachments.go`) read a message without its files: `BODYSTRUCTURE` first, then in one command each part's MIME header, the text and the HTML (bounded) and each part's decoded size (`BINARY.SIZE` where the server has BINARY or IMAP4rev2, an estimate otherwise); which parts are files is `attachment.Listed` on those headers, under their IMAP part number (the `attachmentId`), names decoded from RFC 2047 or RFC 2231 in any charset. The download then fetches its one part, as it arrives, through the cap it is given. Both close the connection when their context ends (`within`): the two methods of the client a context can stop |
| `smtpsend` | Hands one message to the sending server of an IMAP mailbox: the daily recap on that transport. Implicit TLS (465) or STARTTLS (587), never cleartext (a STARTTLS route whose server does not offer it is refused before the password is sent), certificate verified against the system roots, AUTH PLAIN, 30 s bound. `ErrAuth` (credentials refused: 535, or 534 5.7.9), `ErrBlocked` (sign-in blocked for where it came from, the password being right: 534 5.7.14) and `ErrUnreachable` are typed; any other 534 is none of them |
| `billing` | Stripe Checkout Session creation and webhook signature verification, with a 5 min replay window |
| `auth` | HMAC-SHA256 session tokens and OAuth `state`, keyed by distinct labels off the master secret so one cannot be replayed as the other |
| `crypto` | AES-256-GCM at rest, key SHA-256-derived from `ENCRYPTION_KEY`. Its production callers are `api/tokens.go` (Google tokens and IMAP passwords), `api/ai_settings.go` (BYOK provider keys) and the legacy `gmail_config` read at boot |
| `config` | Env loading + `Validate()` fail-fast |
| `database` | Mongo client, one accessor per collection, `EnsureIndexes` |
| `models` | Every BSON/JSON struct. One file, `models.go` |

### The api package, file by file

| File | Covers |
|---|---|
| `routes.go` | The single route table (79 registrations) and the middleware chain. Source of truth for the API surface |
| `middleware.go` | `authMiddleware`, `securityHeadersMiddleware`, `recoverMiddleware`, `requestIDMiddleware`, `loggingMiddleware`, token-bucket rate limiter, `clientIP`, `publicPrefixes` |
| `respond.go` | `writeJSON`, `writeError`, `decodeJSON`, `writeAuthError` (no mailbox -> 428, dead grant -> 401, wrong transport -> 501), `errReauthRequired`, 1 MiB body cap |
| `handlers.go` | `Handler` struct + constructor (which starts the background loops), `gmailClientFor`, health, metrics, Google auth URL and callback, emails listing, stats, sync, direct action, labels, config status |
| `auth_account.go` | `POST /api/auth/register` and `/login`: the account by address and password (bcrypt, 8 characters to 72 bytes), behind the sign-in limiter |
| `message.go` | `GET /api/emails/{id}`: one message with its body and its attachment list, on both transports. `writeMailboxFailure`: what a read of the mailbox that failed answers, on both transports and for the download too: a French sentence, 404 when the mail is no longer there (`mailGone`), 502 otherwise, the error itself only in the log |
| `batch.go` | `POST /api/emails/batch-action` (Gmail only) and `/batch-undo` (Gmail and IMAP), plus the identity lookups the ledger uses |
| `analysis.go` | `runAnalysis`: the shared engine behind sync and async AI analysis. Cache, batching, auto-pilot, quota |
| `ai_handlers.go` | Eight of the `/api/ai/*` endpoints (analyze, analyze-sender, apply, apply-batch, apply-bulk, suggestions, reject, reject-batch), `GET /api/senders`, the sender preferences and `/api/smart-labels`. `analyze-async` and `jobs/{id}` are in `jobs.go` |
| `ai_settings.go` | The BYOK routes (`/api/ai/providers`, `/api/ai/settings`, `/api/ai/settings/test`) and `resolveAnalyzer`, which puts the account's own provider first |
| `tokens.go` | Stored credentials: `sealToken` / `openToken` for Google's (with the legacy-plaintext migration), `sealSecret` / `openSecret` for IMAP passwords, all AES-256-GCM behind the `enc:v1:` prefix, and `getUserToken` |
| `jobs.go` | Async analysis job queue and worker pool |
| `rules.go` | Rules CRUD, `apply`, `preview` (dry run reuses the apply path) |
| `snooze.go` | Snooze CRUD (preset or explicit wake time), the batch snooze, "Réactiver", plus the 1 min wake sweeper, on both transports. Over IMAP (`snoozeIMAP`) a reported mail is moved into the folder "Plus tard" (`snoozeFolderName`, made like a filing folder, by `Destination`) through `mirroredIMAP`, like an archive, and the snooze records where it landed (`folder`, `folderUid`, `headerMessageId`); its pending AI proposal is set moot. The sweep goes account by account (`snooze.ByAccount`), one session each, an IMAP one bounded by `snoozeAccountBound` (30 s, then `Abort`); the sweep, "Réactiver" (`wakeIMAP`, `wakeGmail`) and "Annuler" claim the snooze first (`claimReport`, `wakingAt`), on both transports, so two of them move the mail once and write its return once, and `settleWake` writes the outcome (`snooze.Settle`) and the ledger entry. "Annuler" that brings back a mail with a scheduled snooze (`UndoAction`, `EmailAction`) ends it (`endReportOnReturn`: `cancelled`, out of the scheduled list at once; `423` while the sweep or a "Réactiver" holds it) |
| `attachment.go` | Attachment download, on both transports: the one route that streams bytes rather than JSON. Over IMAP the id is a part number, served only when the reader's list of that message has a file under it, or an image of `inlineImages` |
| `searches.go` | Saved searches CRUD and their use counter |
| `rules_portable.go` | Ruleset-wide operations: reorder, duplicate, export, import |
| `protected.go` | VIP list CRUD and `protectedValues` lookup |
| `unsubscribe.go` | `List-Unsubscribe` (RFC 2369) and one-click POST (RFC 8058) |
| `history.go` | Action log view and undo |
| `ledger.go` | `logAction` plus the `Source*` vocabulary. Read this before adding a mutating action |
| `account.go` | Settings, usage, profile, activity and digest preview, `FreeMonthlyLimit = 200` and `analysisCapFor` (only free accounts of the hosted edition are capped) |
| `account_data.go` | `datasetCollection`: the one bridge from `account.Dataset` to a Mongo collection. Export and delete both walk it |
| `billing.go` | Checkout, portal, Stripe webhook |
| `waitlist.go` | Public Pro waitlist capture |
| `providers.go` | `GET /api/providers`: the `internal/provider` catalog for the running `Edition`, shaped for the connect screen. The SPA hardcodes no provider |
| `mail_accounts.go` | The mailbox a user connected over IMAP: connect (proved before it is stored, route from `provider.PickIMAP`, whose sending server is stored with it as `smtpHost` / `smtpPort` / `smtpTls`, or cleared when the route has none: `mailboxRow` writes the row), read, disconnect (which also drops the Google tokens). `connectAndStore` is the shared half, used by the authenticated connect AND by the public sign-in, so the sealing and the upsert cannot drift. Plus `openMailbox`, the IMAP counterpart of `getUserToken`, and `sendThroughBox`, which hands the recap to the mailbox's sending server: the only two readers of the sealed app password |
| `auth_mailbox.go` | `POST /api/auth/mailbox`: signing in with a mailbox, which creates the account on first success. An API alternative the SPA does not call. The sign-in limiter's constants and the guard against taking over a Google account |
| `mirror.go` | Serving a listing and its counters from the STORED mailbox rather than from the provider. The IMAP half of `GetEmails` and `GetMailboxStats`, plus the query-to-lookup translation |
| `session.go` | WHICH transport a user is on, and a session open on it. `transportFor`, `openSession`, `mailSession.Mailbox()` / `.RefFor()`, the IMAP sync, and `errWrongTransport`. On IMAP, `mirroredIMAP` keeps the stored inbox right: a message Mailsorter moves out is flagged `leftInbox` with where it went (`movedTo`) and stays stored, hidden from every inbox read (`notLeftInbox`), for `undoWindow` (30 days), which is how long "Annuler" can bring it back. A move claims the row first (`leftInbox` set only if unset), so of two requests moving one message at once only one moves it; `errAlreadyOut` is the other one, and a message another mail client moved first. `dropStaleInbox` judges every stored row against the inbox's full UID list at each sync (`mirror.Judge`); `storeStarredOutsideList`, `refreshInboxState` and `completeInboxStructure` keep the read state, the star, the size and the attachments of every stored row right, old favorites included, and a flag changed from Mailsorter goes into the row at once (`mirrorFlags`). `bringBack` is a reported mail's return: moved back from where the snooze recorded it (`MoveRecorded`), marked unread, and the stored row takes its new number as after "Annuler"; gone from there, `errNotWhereLeft` and nothing moves |
| `mailbox.go` | `gmailMailbox`, the Gmail adapter for `mailbox.Mailbox`, plus `applyVerb` and `applyMutations`. Every mutating handler goes through these two |
| `digest_scheduler.go` | 15 min ticker sending the daily digest from the user's own mailbox (`deliverDigest`: the Gmail API, or the IMAP mailbox's sending server through `sendThroughBox`; `digestMessage` gives it its `Message-ID`), paced by `dailyDigestPace` (4 accounts at once, 30 s each, the stamp under its own 10 s), `digestHourOf` (a stored hour, 0 = midnight, or `DIGEST_HOUR_UTC` when none was picked; the settings screen and the scheduler both read it), `POST /api/account/digest/test` (one per account per minute, `digestTestLimiter`, a 429 in words beyond; `writeDigestSendError` says why a test did not leave, never with the Gmail sentence on IMAP, never with a 401), and `digestStyle()`, the UI theme as a digest style |
| `auto_sync.go` | 30 min per-user background inbox sync |

### Request pipeline

Declared in `routes.go`, outermost first:

```
CORS -> security headers -> recover -> request-id -> metrics -> logging -> rate-limit (20 r/s, burst 40) -> auth -> handler
```

The rate limit is keyed per account when the bearer is valid, per visitor address
otherwise. An unknown path or a wrong method never reaches this chain: the router's
own handlers answer it, in the same JSON envelope (404 "No such route", 405).

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
best-effort at boot by `EnsureIndexes`, the single source of truth (the unique
`users.email` index included). `mongo-init/init-db.js` creates nothing: it only ever
ran on a fresh Compose volume, never on CI, the scenarios or an existing database.

`users`, `emails`, `labels`, `gmail_config` (legacy, read-only fallback),
`ai_suggestions`, `sender_preferences`, `smart_labels`, `analysis_jobs`,
`analysis_cache`, `usage`, `unsubscribes`, `sorting_rules`, `protected_senders`,
`snoozes`, `action_log`, `saved_searches`, `waitlist`, `mail_accounts`,
`ai_settings`.

Everything is scoped by `userId` (which is the user's email address) except `users`
(keyed by `email`), `gmail_config` (one legacy instance-wide document),
`analysis_cache`, keyed by `sha256(version + "|" + lower(from) + "|" + lower(subject))` and shared
across all users on purpose, and `waitlist`, keyed by email, whose row carries the
account's `userId` only when the signup was made while signed in.

### Background loops

All four are started by `NewHandler` in `handlers.go`. They are goroutines with tickers,
not cron; a redeploy restarts them.

| Loop | Cadence | File |
|---|---|---|
| Analysis worker pool (3 workers) | drains `jobQueue` (cap 256) | `jobs.go` |
| Snooze wake sweep | 1 min, account by account: one session each, an IMAP one bounded to 30 s | `snooze.go` |
| Daily digest | 15 min tick, at most one send per user per day, through the Gmail API or the IMAP mailbox's sending server (SMTP); 4 accounts at once, each with its own 30 s, so a sending server that does not answer holds only its own | `digest_scheduler.go` |
| Auto-sync | 5 min tick, but min 30 min between two syncs of the same user | `auto_sync.go` |

## Frontend Architecture

### Routes

Declared in `src/App.js`. The app boots by calling `GET /api/config/status` once,
through `InstanceProvider`; if the instance has NO WAY IN AT ALL
(`isUsable` = `isConfigured || mailboxSignIn`) every guarded route redirects to
`/setup`. Not on `isConfigured` alone: an instance reachable only over IMAP is a
working instance, and gating on Google sent every hosted visitor to a screen
telling them to create a Google Cloud project that edition can never use.

| Route | Page | Purpose |
|---|---|---|
| `/` | `pages/HotelLanding.js` or `pages/Login.js` | Chosen at runtime by `UI_THEME` (`lib/uiTheme.js`; `?ui=hotel\|classic` previews it for one tab): the Grand Hotel landing or the classic one. Both share the auth logic (`lib/useAuthForm.js`) and the doors below. Marketing landing + its doors: e-mail and password sign-up and sign-in, plus Google when `isConfigured`, and the Pro waitlist when billing is off (in the Tarifs section of the hotel landing, in the "Pas encore prêt ?" card of the classic one). Its claims follow too, because an IMAP mailbox does not get every feature (`GMAIL_ONLY`). Also the public trust surface: what leaves for the model, the FAQ, and the providers read from `GET /api/providers` |
| `/inbox` | `pages/Inbox.js` | The cockpit: triage, the AI's triage plan (`components/inbox/`), bulk apply, keyboard shortcuts (2288 lines, the heaviest file) |
| `/rules` | `pages/Rules.js` | Deterministic rule editor + dry-run preview |
| `/snoozed` | `pages/Snoozed.js` | Scheduled returns |
| `/history` | `pages/History.js` | Action ledger + undo |
| `/pricing` | `pages/Pricing.js` | Plans, weekly recap, Stripe checkout or waitlist. Redirects away in the `self-hosted` edition, which bills nobody |
| `/settings` | `pages/Settings.js` | Auto-apply, auto-sync, digest hour, and the way in to `/connect` |
| `/connect` | `pages/Connect.js` | Connect a mailbox over IMAP. Detects the provider from the address against the catalog the SERVER returned, and shows that route's blockers BEFORE the attempt, because each of them otherwise surfaces as "credentials refused" |
| `/account` | `pages/Account.js` | Profile, usage, GDPR export and delete |
| `/setup` | `pages/Setup.js` | Read-only briefing on the env vars. Not a form: the OAuth app is instance config. Edition-aware: the own-project guide (6 steps, including "Publier l'application") self-hosted, the operator one (5 steps) otherwise, plus the reachable providers read from `GET /api/providers` |
| `/auth/callback` | `pages/AuthCallback.js` | Exchanges the OAuth code for a session token. The ONLY callback: `Login` used to handle `?code=` too, in a path `GMAIL_REDIRECT_URL` could never reach |
| `/confidentialite` | `pages/Privacy.js` | Privacy policy. Public and unconditional, including before the instance is configured: Google's OAuth verification expects it reachable from the home page |
| `/conditions` | `pages/Terms.js` | Terms of use |

`/emails` and `/triage` redirect to `/inbox`.

An instance with NO WAY IN no longer sends everyone to `/setup`. That screen asks the
reader to fill in environment variables and restart the service, which is the right
screen for the owner of a self-hosted instance and useless to a visitor on a hosted
one. So `self-hosted` still redirects there, `hosted` renders `Unavailable` in
`App.js` instead, and `/setup` stays reachable by its own address for the operator.

"No way in" is `!isUsable`, not `!isConfigured`: missing Google credentials is not
the same as being unreachable, since a mailbox sign-in is a door of its own. A
hosted instance with an IMAP route in its catalog renders the landing page and its
sign-in form, and is never called unavailable.

Which makes `Unavailable` a safety net rather than a screen anyone reaches today:
both editions carry IMAP routes (14 self-hosted, 13 hosted), so `mailboxSignIn` is
true whenever the server answers at all, and mailbox sign-in needs no instance
config, no OAuth app and no env var. Keep the screen: it is the right answer for an
edition that ever ships without an IMAP route. Do not reach for it to explain a
blank page, and do not wire new behaviour behind it. An instance whose server does
not answer gets the boot-error screen instead, which is a different branch.

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
  `selfHosted`, `uiTheme`). Never probe the instance from a component: App and Pricing each
  called it separately and could disagree about the same instance for a few
  hundred milliseconds. Everything else is local `useState`.
  `useHotel()` (`ui/hotel/HotelTheme.js`) is not a third store: it is a boolean
  derived once from `uiTheme` and the `?ui=` preview, true only once the hotel
  stylesheet has loaded. Branch on it, never on `uiTheme` directly.
- **The AI's suggestions are a plan, one row per sender and verdict, not one per
  email.** `components/inbox/planGroups.js` groups them and is pure;
  `useTriagePlan` in `TriagePlan.js` does the I/O, and the Inbox holds it because
  the `a` shortcut needs it. Three rules come with it. A `keep` verdict is not a
  task: it has its own block and applying it moves nothing. The bulk "Ranger"
  takes only groups whose every verdict reaches `SURE_CONFIDENCE` (0.8, also what
  flags the reader's card "À vérifier"), and "Annuler" is offered only when
  apply-batch answers `reversible`. And any action that takes mail out of the
  inbox by hand must call `plan.settle(ids)`, or the plan goes on proposing to
  archive a message that is already gone.
- **The edition is a frontend concern too.** A `self-hosted` instance bills nobody,
  so the header drops its pricing entry and `/pricing` redirects instead of
  rendering a page with no offer. The SPA still hardcodes no provider: the connect
  surfaces render whatever `GET /api/providers` returns.
- **A `Route.Note` in `internal/provider/catalog.go` is never shown to the user.**
  `GET /api/providers` sends it as `note`, but it is written for whoever maintains
  the catalog (compliance, bandwidth, a pitch, some of it unaccented), and the SPA
  does not render it. What a user must know before creating the password is said
  again, in French, in `PROVIDER_TIPS` in `components/MailboxBriefing.js`, keyed by
  the provider's key and rendered on `/connect` under the detected provider. When a
  note says something the user has to act on (Orange's "clé d'accès", iCloud
  showing the password only once), add or update its tip there, or the screen stays
  silent about it. A tip only adds a sentence to a provider the server returned; it
  never makes one appear. The same goes for a new `Blocker`: add its French
  sentence to `BLOCKER_COPY` in the same file or the screen silently drops it,
  which is worse than showing nothing, because the blocker is the reason the
  connection is about to fail. `/connect` is the one screen that renders them (the
  landing's sign-in asks for an account password, not a mailbox credential): keep
  both tables in this one file, since two copies would drift.
- Session identity lives in `localStorage` (`accessToken`, `userEmail`). Gamification
  state lives in `localStorage` too (`ui/streak.js`, key `mailsorter_gamify`), and so
  does whether the triage plan is folded (`mailsorter_plan_collapsed`), a per-browser
  convenience that is allowed to reset.

### Design system

- Tailwind only, configured in `frontend/tailwind.config.js`: one accent (`brand`, deep
  ink blue), cool neutrals (`ink`), a three-step shadow scale, six named animations.
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
- **The Grand Hotel theme is a second, switchable design system** (`UI_THEME`, see
  Configuration). Its tokens live in `src/styles/hotel.css` under `.theme-hotel`
  (night under `.dark .theme-hotel`), never on `:root`, so `classic` leaves no trace.
  It is plain CSS on purpose, the one exception to "Tailwind only": a theme that can
  be switched off cannot live in `index.css`. Classes are `ht-` (primitives),
  `hl-` (landing) and `hf-` (illustration text inside the SVG drawings), never a
  generic name, because a CSS chunk stays loaded after navigation and would
  restyle the dashboard. Illustrations are hand-drawn SVG
  components in `src/ui/hotel/` with fixed colours. Bodoni Moda is pinned at
  `font-variation-settings: 'opsz' 28` at 28px and above, or its hairlines
  swallow a 4 and a hyphen; smaller Bodoni text uses `font-optical-sizing: auto`,
  which keeps the hairlines thick enough at that size.
  The reference rendering is `docs/design/moodboard.html`, which loads the real
  `hotel.css`: open it before touching the theme.
- **The dashboard wears the hotel too, in two layers, and classic stays
  byte-for-byte what it was.** Layer one is CSS only (`styles/hotel-app.css`,
  everything under `.ht-app`, set on `<body>` off the landing): it re-points the
  classic tokens (`brand`, `ink`, `surface`, the status ramps) at the hotel
  palette, day and night, and gives `.btn*`, `.card`, `.input`, `.chip` and
  `.skeleton` the hotel's shape (square, 1.5px ink, solid offset shadow, no blur,
  no gradient). That alone restyles every screen. Layer two is what the hotel
  says rather than wears: JSX behind `useHotel()` (the lift-panel header, floor
  plaques via `components/hotel/PageHead`, the counters board, luggage-tag
  filters, proposals on the rows, the history as a register, drawings on empty
  states via `EmptyState`'s `scene` / `hotelTitle` / `hotelDescription`). The
  triage plan is the same `TriagePlan` in both themes, wearing the skin and the
  hotel avatars: it stays the one place to decide by sender, and a row's
  proposal goes through the same handlers (single apply, `plan.ignoreEmail`),
  never `keep`, which the plan no longer treats as a task. A hotel
  variant is always a branch that leaves the classic markup untouched: the
  classic theme was checked pixel-identical (68 screenshots, day and night,
  1440 and 390) after this work, keep it that way. Copy in the hotel branches
  follows the moodboard's tone (the action's own verb, Ignorer, Annuler, as the
  triage plan says them; the humour in titles and empty states only), ASCII
  punctuation.
- **`hotel-app.css` loads after Tailwind, so it beats the utilities.** An `hd-`
  class that sets `display` overrides `hidden` / `lg:flex` on the same element,
  which is how the header once showed its phone-only floor indicator on a
  desktop. Set an `hd-` element's display in `hotel-app.css`, with its own media
  query, never with a Tailwind display utility.
- **Bodoni figures run at a low optical size in the dashboard.** `opsz 28` keeps a
  4 and a hyphen readable for words set large (`.hd-title`), not for figures on a
  1x screen: "47" drew as "/17". `.font-display`, the counters and every brass
  button number are pinned at `opsz` 9 to 12.

## Key Conventions

### Language

| Surface | Language |
|---|---|
| Go and JS code, identifiers, comments | English |
| `docs/API.md`, `.env.example`, `CLAUDE.md` | English |
| `README.md`, `docs/ARCHITECTURE.md`, `docs/ROADMAP.md`, `BACKLOG.md` | French |
| `docs/USER-STORIES.md` | French, written without accents |
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
- Acceptance scenarios (build tag `acceptance`) are the one place the `api` package
  runs against a REAL MongoDB and a fake Gmail (`acceptance_fakegmail_test.go`), or a
  real in-memory IMAP server over TLS trusted through `SSL_CERT_FILE`, with the
  database prepared as the production volume is (`acceptance_imap_test.go`), plus
  a fake SMTP sending server next to it, over the same TLS (`acceptance_smtp_test.go`), and
  the only tests that say which messages a handler actually moved. One scenario is
  one French sentence (`phrase`) Nohé can answer yes or no to: `observed` until he
  answers, `validated` on a yes, `rejected` on a no. A rejected scenario still pins
  what the app does today, and the report lists it under "À corriger" until the
  change that fixes it rewrites it in place. They FAIL without `MAILSORTER_TEST_MONGO_URI`
  rather than skip. `go test ./...` without the tag does not see them.
- A test that needs the same harness but proves wiring rather than a sentence
  (what a sync does with an answer a real server gives, what a handler reads in a
  real database) is a `TestAcceptance_*` under the same tag, in a file of its own
  (`wiring_imap_test.go`, `wiring_filing_test.go` for filing into folders over
  IMAP: what the model is told, the shield at analysis, "unlabel" right after
  "Appliquer", the direct "label" without a folder, and `wiring_snooze_test.go`
  for the report's wake over IMAP: one connection per mailbox, a mailbox out of
  reach, a failed return tried again, and on both transports "Réactiver" while
  the sweep holds a report and "Annuler" ending one): not in the report, run by
  `scripts/scenarios.sh -run '^TestAcceptance'` and by its own CI step.

### React

- Function components, hooks, no class components. Default export per page, named
  exports for hooks and helpers.
- One file per route under `pages/`. Cross-route UI goes to `components/` or `ui/`.
- Files: PascalCase for components (`EmailReader.js`, `Toast.js`), camelCase for plain
  modules (`api.js`, `cn.js`, `analytics.js`, `streak.js`, `icons.js`).
- `track(event, props)` from `lib/analytics.js` for product events. Properties must carry
  no personal data: no email address, no sender, no subject. Counts and enum labels only.
  Never add a manual pageview: the tracker hooks the History API already.
- Untrusted HTML (email bodies) goes through dompurify before rendering. The
  reader then puts object URLs in place of the `src="cid:..."` the server names
  in `inlineImages` (pictures downloaded through the authenticated route, at
  most 3 at a time from the top of the mail: over IMAP each one is a
  connection to the mailbox), and when it leaves the message drops the queue,
  aborts what is under way and revokes the URLs: after the sanitizing, so
  nothing the sender wrote picks an image's URL. The queue is tied to the
  message loaded (`full.messageId`), never to the one asked for, which differs
  while the next one loads. A `cid:` image the server does not serve is taken
  out of the text and counted in a line under it.

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
- Exception, for a behavior change run through the acceptance scenarios: the
  commit that locks the scenarios and the commit that makes them pass are made
  locally on the feature branch without asking, because the first one is what
  the check diffs against. Pushing and merging still wait for Nohé.

## Configuration

`.env.example` is the documented contract; read it before touching any env plumbing.
`docker-compose.yml` (the deployment) maps the host vars onto container vars:
`MONGO_ROOT_*` into a composed `MONGODB_URI`, `PORT` fixed to `8080` (the
healthcheck and the frontend proxy expect it), and of the AI variables
`MISTRAL_*` only. `compose.local.yml`, stacked on it by `make up`, publishes the
host ports and forwards `OLLAMA_*`, `OPENAI_*`, `ANTHROPIC_*` and
`AI_FALLBACK_PROVIDER`. `make backend` exports `.env` before starting the binary,
which reads none of it by itself.

| Var | Required | Notes |
|---|---|---|
| `ENCRYPTION_KEY` | yes | No default. Boot **refuses** it missing, under 32 characters, or set to a known example (the `.env.example` placeholder, the README's placeholder, and the old built-in default; `docker-compose.yml` passes it through with no fallback). The example an earlier README showed (`une-chaine-aleatoire-...`) is accepted with a startup warning: boot took it before, and refusing it would stop an instance whose only way out loses every stored secret. It seals every stored credential and signs the sessions; changing it after the fact logs everyone out and makes every stored secret unreadable |
| `GMAIL_CLIENT_ID` / `GMAIL_CLIENT_SECRET` / `GMAIL_REDIRECT_URL` | optional | Needed for the Gmail API route and the Google sign-in; without them a Gmail address still connects over IMAP. One OAuth app for the whole instance. Env is the source of truth; the `gmail_config` document is a read-only legacy fallback. Must be a **Web application** client, not Desktop |
| `MISTRAL_API_KEY`, `MISTRAL_MODEL`, `MISTRAL_MAX_RETRIES` | one AI provider is needed for the AI features | defaults `mistral-small-latest`, 2 extra retries. Mistral is first in the fallback order |
| `OPENAI_API_KEY`, `OPENAI_MODEL`, `OPENAI_BASE_URL` | optional | default model `gpt-6-luna`. `OPENAI_BASE_URL` is the full chat completions URL, for any OpenAI-compatible server (llama.cpp, LM Studio, vLLM); the key must not be empty even when that server ignores it. Second in the order |
| `ANTHROPIC_API_KEY`, `ANTHROPIC_MODEL` | optional | default model `claude-haiku-5-5`. Third in the order |
| `OLLAMA_MODEL`, `OLLAMA_BASE_URL` | optional | Ollama joins the registry only when `OLLAMA_MODEL` is set (it is sent `think: false`). Base URL default `http://localhost:11434`, and `http://host.docker.internal:11434` under `make up`. Last in the order |
| `AI_FALLBACK_PROVIDER` | optional | names a provider (`openai`, `anthropic`, `ollama`) to move right after Mistral in the order |
| `MONGODB_URI` / `MONGO_ROOT_USERNAME` / `MONGO_ROOT_PASSWORD` / `PORT` | see `.env.example` | direct `go run` reads `MONGODB_URI` and `PORT` (default 8080); in the containers `PORT` is always 8080 |
| `BACKEND_PORT` / `FRONTEND_PORT` / `MONGO_PORT` | optional | host side only, under `make up` (defaults 8080, 3000, 27017, MongoDB bound to 127.0.0.1) |
| `STRIPE_SECRET_KEY`, `STRIPE_PRICE_ID`, `STRIPE_WEBHOOK_SECRET`, `APP_BASE_URL` | optional | empty key keeps the waitlist CTA instead of checkout |
| `ALLOWED_ORIGINS` | optional | comma separated. Empty falls back to localhost:3000, localhost, mailsorter.sohbi.dev. No rebuild needed |
| `EDITION` | optional (defaults to `self-hosted`) | `self-hosted` or `hosted`. Boot **refuses** anything else. Decides which providers `internal/provider` offers (the Gmail API and Proton exist only in `self-hosted`) and whether free accounts are capped (200 analyses a month, `hosted` only) |
| `UI_THEME` | optional (defaults to `hotel`) | `hotel` or `classic`: the look of the landing, of every dashboard screen and of the daily digest email, read at runtime from `GET /api/config/status`, so switching is a backend restart, not a rebuild. Boot **refuses** anything else. `?ui=hotel\|classic` previews the other one for one tab (the digest, sent by the server, follows `UI_THEME` only) |
| `BUILD_VERSION`, `DIGEST_HOUR_UTC` | optional | reported by `/health` and `/metrics`; digest hour for users who never picked one, default 7 (07:00 UTC). A stored hour wins, 0 meaning midnight |
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
| `README.md` | Product pitch, editions and providers, what works over IMAP, the engineering decisions worth reading, quickstart | Current |
| `docs/API.md` | Every endpoint (all 79 registrations), payload, error, auth rule, and what each one does over IMAP | Current, English |
| `docs/ARCHITECTURE.md` | Overview, sign-in, sync and analysis flows, security, background loops, observability | Current, short; the detail is here |
| `docs/ROADMAP.md` | The 13 delivered phases (0 to 12), as a history, and the deploy checklist | History; the checklist is current |
| `BACKLOG.md` | Last audit: every finding, all closed | Historical record, useful for "why is it like this" |
| `docs/USER-STORIES.md` | Audit of the public front's user stories (September 2026) and what was fixed | Historical record |

## Known Gotchas

- **The public domain is hardcoded in three files, on purpose.** `public/index.html`
  (canonical, `og:url`, `og:image`), `public/sitemap.xml` and `public/robots.txt` carry
  `https://mailsorter.sohbi.dev` in full. A social share needs an ABSOLUTE image URL, and
  `index.html` is not traversed by the bundler at runtime, so there is no env var to read
  here: an instance on another domain edits those three files or drops them. The share
  image itself is generated from `docs/assets/og-image.source.html` with headless Chromium
  at 1200x630.
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
  One credential does not follow this yet: the BYOK provider key. `ai_settings.go`
  encrypts it with the same encryptor directly, without the `enc:v1:` prefix, and
  `resolveAnalyzer` uses the stored value as is when it does not decrypt. A new
  credential goes through `sealSecret` / `openSecret`.
- **Adding a per-user collection means two catalogs, not one.** `account.Datasets()`
  makes erasure reach it (an account deleted while its app password stays on file is
  the exact bug the last audit found), and `account.SecretFields` says which of its
  columns must be stripped from the export. Export and erasure share the catalog, so
  a new collection is exportable BY DEFAULT: if it holds a credential, say so there.
  `ai_settings` shipped the BYOK feature and missed both, so a deleted account kept
  its provider key on disk. `TestCatalogCoversEveryUserScopedCollection` closes that:
  it reads the accessors in `internal/database/database.go` and fails on any
  `userId`-scoped collection the catalog does not reach, with an exemption list for
  the three that are not user-scoped (`users`, `gmail_config`, `analysis_cache`;
  `waitlist` left it once a signed-in signup started carrying the `userId`). A bson
  tag rename disables redaction silently, so
  `TestSecretFieldsNameRealBsonFields` checks each declared name against the struct.
  Run both after adding a collection.
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
- **On IMAP the listing is served from the mirror, not from the mailbox.** Over the
  Gmail API `GET /api/emails` hands the query to Google. Over IMAP there is nobody
  to hand it to: `IMAP SEARCH` is a round trip on a stateful socket with a folder
  selected, and a listing runs on every page load, every chip and every keystroke.
  So `mirror.go` answers from what `syncInboxIMAP` wrote, which means a message that
  arrived since the last sync is not in the answer, and `resultSizeEstimate` counts
  what is mirrored. Two rules that come with it: a query term the mirror cannot
  answer is REFUSED (501, naming the term) rather than dropped, because a dropped
  `label:Factures` renders as a full inbox under a saved search that says otherwise;
  and a datastore that cannot be read is an ERROR, never an empty list, because an
  empty list renders as "Inbox Zero atteint" and gives the user nothing to retry.
  Every quick filter of the inbox is answered (`is:unread`, `newer_than:`,
  `is:starred`, `has:attachment`, `larger:5M`), because the copy keeps what they
  ask: the star, the size and whether a mail carries a file. Each sync keeps them
  right beyond the 100 messages it lists: `imap.Client.States` reads the flags and
  `RFC822.SIZE` of the whole inbox in one FETCH; `storeStarredOutsideList` stores
  the starred messages the copy does not hold yet (a favorite of years ago, 500 a
  sync); `refreshInboxState` carries read, star and size into every stored row (a
  row Mailsorter changed after the flags were read, `flagsAt`, keeps its flags;
  the upsert of a listed row writes them only on insert, `inboxUpsert`); and
  `completeInboxStructure` reads `BODYSTRUCTURE` for up to 500 rows without
  `hasAttachments`, newest first. A row without it matches no attachment filter.
- **A FETCH of many messages is read by halves (`imap.ReadInHalves`).** go-imap
  reads an answer whole: one message whose description it cannot parse fails the
  command AND closes the connection. Read as one batch, that message would fail
  its neighbours at every sync and empty the filter for good. So the sync's
  batch reads (starred envelopes, structures) go through `ReadInHalves`: on an
  answer that could not be parsed it reopens the connection (`sessionReopener`,
  at most `phaseReopens` times per phase, each phase its own budget, and
  `openForPhase` reopens one a phase before left closed) and reads each half,
  down to the message alone. A message unreadable alone is logged, and its
  structure is stored as `hasAttachments: false, structureUnreadable: true`,
  never asked again. ONLY a parse failure is a verdict on a message
  (`imap.IsUnreadableAnswer`: go-imap does not type it, the "imapwire:" of its
  decoder marks it, and a test against the real client pins that): a refusal of
  the server (`*imapv2.Error`, NO [UNAVAILABLE]), a time limit or a lost
  connection stops the reading with no verdict (`ErrReadStopped`, the sync
  answers `errMailboxUnavailable`), and a reopen that fails is
  `errMailboxLost`. Out of reopens, the rest waits for the next sync.
- **Free text from a query goes into a `$regex`, so it goes through `regexp.QuoteMeta`.**
  `containsInsensitive` in `mirror.go` is the only place a user-typed string becomes a
  pattern. Unescaped, a "(" is a syntax error that fails the whole listing and a "."
  silently matches any character, so a search for `a.com` would return `axcom`.
- **An IMAP message has no labels in the mirror, and its star is `isStarred`.**
  `models.Email.LabelIDs` is empty on that transport on purpose (a message is in one
  folder, which `Folder` carries). `\Flagged` is `IsStarred`, and its JSON tag must
  stay `omitempty`: the reader does `email.isStarred ?? full?.isStarred ??
  labelIds.includes('STARRED')`, and `??` only falls through on a missing value, so
  a `false` sent with every Gmail message would hide its star. Its BSON tag is NOT
  `omitempty`, so a star taken off clears the stored one. `SizeEstimate` and
  `HasAttachments` are `omitempty` both ways: written back from a listing or the
  reader, which do not read them, a message keeps what the sync stored.
- **`analysis_cache` is shared across all users.** It is keyed on sender plus subject only, so
  never cache anything user-specific through it. That is also why the model is told
  none of the user's labels or folders (the `ai.Analyzer` methods take none, and
  `internal/ai/prompts.go` asks for no list):
  its verdict would carry one person's folder name into another's plan, and the
  public pages say it sees the sender, the subject and 200 characters. What it
  proposes is matched with the user's labels afterwards, per account
  (`mailbox.MatchLabel`; over IMAP `imap.Client.FilingName`, against the folders
  only, at the analysis and again when the filing is applied), and the cache keeps
  the model's own verdict. Every entry carries `database.AnalysisCacheVersion`, which
  also starts its key: change it whenever what a verdict is made from changes. The
  collection never expires, so `EnsureIndexes` deletes at each boot, in one request,
  every entry of another version (`dropStaleAnalysisCache`, a failure only logged).
- **`docker-compose.yml` publishes no ports.** Dokploy routes through its own proxy, so
  the deployment file binds nothing to the host. Local runs need `compose.local.yml` on
  top, which is what `make up` does. It is deliberately NOT named
  `docker-compose.override.yml`, since Compose would merge that into the deployment too.
- **No handler knows Gmail's label vocabulary.** As Gmail label ids, `"INBOX"`,
  `"TRASH"`, `"UNREAD"` and `"STARRED"` appear in exactly two files:
  `internal/mailbox/mailbox.go` (the translation) and `internal/gmail/gmail.go` (the
  client). They used to appear 51 times across the tree. `"INBOX"` also appears in the
  IMAP code (`internal/imap`, `internal/mailbox/imap.go`), where it is the protocol's
  own name for the inbox, and in `internal/search`, as the one scope the mirror answers.
  Two traps the table encodes: untrashing must BOTH restore `INBOX` and drop `TRASH`, and
  read state is the ABSENCE of `UNREAD`, so marking read removes rather than adds.
- **`mailbox.Parse` accepts every spelling already in circulation.** `delete` and `trash`
  are one act, `read` and `markRead` another, and both spellings sit in `action_log` rows
  written over the life of the app. Dropping an alias would silently stop the undo history
  resolving for every entry written the other way.
- **MailSorter account registration is decoupled from mailbox connection.** Users
  register and sign in to their MailSorter account (`POST /api/auth/register`,
  `POST /api/auth/login`) with email and password (bcrypt-hashed), or via Google SSO.
  That is the way in the SPA uses. Once authenticated, they define the mailbox to
  sort on `/connect` either via Google OAuth or via IMAP (`POST /api/mailbox/connect`,
  route from `provider.PickIMAP`). Register, login and `POST /api/auth/mailbox` share
  one sign-in limiter far below the global one (~1 attempt / 3 s, burst 5), keyed on
  the visitor's address AND on the email address; do not remove it. On
  `POST /api/auth/mailbox`, an API alternative the SPA does not call and the one
  public route where a stranger makes the server try a password against a real mail
  provider, a second guard must stay too: a 409 when the address already has Google
  credentials, because a stored `mail_accounts` row wins over a Google token in
  `transportFor`, so accepting would hand the account to whoever holds the app
  password. That check fails CLOSED: a datastore error is a 500, never "no Google".
  `POST /api/mailbox/connect` has neither guard: its caller is signed in, and it is
  how an account deliberately switches from Google to IMAP.
- **The boot gate is `isConfigured || mailboxSignIn`, never `isConfigured` alone.**
  `GET /api/config/status` reports both doors, and `mailboxSignIn` is computed
  from `provider.ForEdition(Edition)` rather than from the edition name, so the
  catalog stays the single source. Gating on Google alone made the hosted edition
  literally impossible to enter: every route, `/` included, redirected to a setup
  screen instructing the visitor to create a Google Cloud project that edition can
  never use.
- **The edition is not packaging, it is a capability gate.** `EDITION=hosted` can never
  reach the Gmail API: a shared OAuth client is capped by Google at 100 authorizations for
  the lifetime of the Cloud project, non-resettable. Hosted reaches Gmail over IMAP with an
  app password instead. `internal/provider` holds that rule as data and a test enforces it,
  so do not special-case a provider in a handler: add or fix its route in the catalog.
  What the catalog gates is the connect screen and `PickIMAP`. The Google OAuth routes
  (`/api/auth/url`, `/api/auth/callback`) do not read the edition: an instance with
  `GMAIL_*` set offers Google sign-in, and with it a Gmail API transport, whatever its
  `EDITION`.
- **Any outbound request whose URL is not a constant goes through `internal/egress`.**
  The fully guarded one is the RFC 8058 one-click unsubscribe, whose address
  comes from the `List-Unsubscribe` header of a received email, so a stranger picks
  it. Unguarded, that is a server-side request forgery: the server POSTs to its own
  loopback API, to the datastore on the Docker bridge, or to 169.254.169.254 for the
  host's credentials. The scheme check must be https-only, the address check must
  run from the dialer's `Control` hook (not before the lookup, or DNS rebinding
  walks past it), and every redirect hop must be re-judged. The other user-chosen
  URL is a BYOK provider's base URL (`/api/ai/settings`): it only goes through
  `egress.Parse` when saved or tested (https, no `localhost` / `.local` name, no
  private IP literal), and the AI clients dial it without the `Control` hook, so a
  name that resolves to a private address is not caught. The instance's own base
  URLs (`OPENAI_BASE_URL`, `OLLAMA_BASE_URL`) are operator configuration, and the
  default Mistral, OpenAI, Anthropic and Stripe URLs are constants.
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
- **A user's transport is resolved per request, and NEVER guessed.** `transportFor`
  reads `mail_accounts`: a row means IMAP, `mongo.ErrNoDocuments` means the Google
  OAuth path, and any OTHER error fails the request. That third branch is the whole
  point. Falling back to Gmail on a datastore hiccup would silently turn an IMAP user
  into a Gmail user and send their UIDs to Gmail as message ids. A test pins it by
  pointing Mongo at a dead address and asserting an error rather than a transport.
- **Most of the app is still Gmail-only, and `gmailClientFor` is the guard that
  makes that safe.** It refuses a user whose mailbox is reached another way, so every
  unported path answers 501 ("Cette action n'est pas encore disponible sur une boîte
  branchée en IMAP.") through `writeAuthError` instead of acting on the wrong message.
  Ported so far: `syncInbox` (without rules), `EmailAction`, `GetEmails`, `GetEmail`
  and `GetMailboxStats`, which is the set that makes a connected mailbox usable at
  all, then the AI analysis (`runAnalysis`, without sender auto-pilot),
  `ApplySuggestion` and `ApplyBatch`, the undo (`UndoAction`, `BatchUndo`),
  `GetLabels`, then `DownloadAttachment`. Over IMAP a label is a folder: a `label` verdict files the mail in
  the folder of that name (`mailbox.IMAPOpFor`, made if missing), out of the inbox
  like an archive, undone like one (`activity.InverseOnFolders`, and "unlabel" on
  the single-message action right after), shielded like one for a protected
  sender at analysis and in a batch (`allowsOn`), and `GetLabels` lists the
  user's own folders. Snooze too (`Snooze`, `BatchSnooze`, `WakeSnooze` and the wake
  sweeper): over IMAP the mail waits in the folder "Plus tard" (`snooze.go`), made if
  missing, shielded like an archive for a protected sender in a selection, and
  comes back unread at its hour or on "Réactiver"; a mail another mail program took
  out of "Plus tard" is left where it is and its snooze is `cancelled`. Everything
  else (rules apply and preview, unsubscribe, smart labels, batch
  action, bulk by sender) refuses: 6 call sites of `gmailClientFor`. Three more are the Gmail branch of a
  ported handler (`GetLabels`, `UndoAction`, `BatchUndo`), and one, in
  `deliverDigest`, is the recap's Gmail API branch, which an IMAP mailbox never
  reaches: its recap leaves through the mailbox's sending server (`sendThroughBox`,
  `internal/smtpsend`).
  When porting one, open a session instead of calling `gmailClientFor`, and delete
  nothing from the guard.
  `GMAIL_ONLY` (`frontend/src/components/landing/features.js`) lists what the
  landing would otherwise promise and an IMAP mailbox cannot get: the Grand
  Hotel landing reads it to decide what it may promise, and keeps promising
  nothing it cannot deliver. So porting a promised feature to IMAP also means
  deleting its line there, as `label` was once the AI's proposal could file into
  a folder, and `snooze` once a reported mail could wait in "Plus tard". Classifying a selection by hand (`LabelPicker` in `Inbox.js`) and the
  rules' "Étiqueter" still go through the Gmail API: those screens stay closed
  on `fullFeatures`, not on that list. On IMAP the screens word a label verdict
  as a folder ("Classer dans « Factures »"): `actionMeta(action, { folders })`
  and `labelPlace` in `ui/actions.js`, with `folders` from `useMailbox().isImap`.
- **The visitor's address is `X-Real-IP`, and the backend believes it only from a
  private or loopback peer.** Every request arrives through nginx, so the TCP peer is
  always nginx; keying the limiters on it put every visitor in one bucket, and one
  script failing logins locked everybody out. `clientIP` (`middleware.go`) reads the
  header, `frontend/nginx.conf` resolves the visitor through the platform proxy's
  `X-Forwarded-For` (private hops only) and SETS the header, overwriting the
  visitor's. Two rules come with it: any new `location` that `proxy_pass`es to the
  backend must set `X-Real-IP` too, or the visitor's own value goes through and they
  pick their bucket; and the sign-in limiter is keyed on that address, never on a
  bearer, since a caller signing in has no session and any token it sends is made up.
- **`X-User-Email` is both the identity header and the `userId`.** An account is its
  address: there is no account id apart from it, and one mailbox per account
  (`mail_accounts` is unique on `userId`), which is exactly what blocks multi-account
  Gmail (`docs/ROADMAP.md`).
- **`GET`/`POST /api/smart-labels` have no UI.** They work (the POST's transport guard
  and input limits are tested); they are deliberately unwired. Do not delete them, do not assume they are reachable from the
  app either. (`GET /api/stats/digest` and `GET /api/labels` used to be in this list;
  both are now wired, to the digest preview in Réglages and to the rules/reader label
  pickers.)
- **Under the hotel, the digest preview in Réglages is an iframe, not `.email-body`.**
  The hotel recap is laid out with tables, as email clients need, and
  `.email-body` turns tables into scrolling blocks so a newsletter cannot widen
  the reader. Rendered there, the preview stopped being "exactly what you will
  receive". `DigestFrame` (`pages/Settings.js`) gives it its own document:
  sanitised, `sandbox="allow-same-origin"` without `allow-scripts`, so nothing in
  it runs and the parent can still read its height. Classic keeps `.email-body`.
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
