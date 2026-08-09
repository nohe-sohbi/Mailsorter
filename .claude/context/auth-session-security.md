---
subsystem: auth-session-security
description: Google OAuth login, stateless HMAC session tokens and OAuth state, the X-User-Email strip-and-reinject middleware, and what AES-256-GCM actually protects at rest.
keywords: [auth, oauth, session token, hmac, bearer token, x-user-email, middleware, encryption key, aes-gcm, csrf state, login, refresh token, rate limiter, publicprefixes, accesstoken]
files:
  - backend/internal/auth/auth.go
  - backend/internal/auth/auth_test.go
  - backend/internal/crypto/crypto.go
  - backend/internal/crypto/crypto_test.go
  - backend/internal/config/config.go
  - backend/internal/config/config_test.go
  - backend/internal/api/middleware.go
  - backend/internal/api/middleware_test.go
  - backend/internal/api/handlers.go
  - backend/internal/api/respond.go
  - backend/internal/api/routes.go
  - backend/internal/api/routes_integration_test.go
  - backend/cmd/server/main.go
  - backend/cmd/test_decrypt/main.go
  - frontend/src/services/api.js
  - frontend/src/pages/Login.js
  - frontend/src/pages/AuthCallback.js
priority: high
related: [gmail-sync, data-model, frontend-inbox]
last-verified: 2026-08-09
---
# Auth, Session and Secrets

Mailsorter has no session store, no account entity and no JWT library. Identity is a
single HMAC-SHA256 string the server signs and re-verifies on every request, the OAuth
handshake is protected by a second HMAC string with a different derived key, and both
keys plus the AES key come from one env var: `ENCRYPTION_KEY`. The user's Gmail OAuth
token never leaves the server.

## Overview

| Concern | Mechanism | Where |
|---|---|---|
| Who is calling | HMAC-signed session token, verified per request | `internal/auth/auth.go`, `internal/api/middleware.go` |
| Login CSRF | HMAC-signed, expiring OAuth `state`, no server storage | `auth.IssueState` / `auth.VerifyState` |
| Identity plumbing | `X-User-Email` deleted from the inbound request, then re-set by the middleware | `middleware.go:61`, `middleware.go:86` |
| Secrets at rest | AES-256-GCM, key = `sha256(ENCRYPTION_KEY)` | `internal/crypto/crypto.go` |
| Boot safety | `config.Validate()` refuses two known placeholder keys and anything under 32 chars | `internal/config/config.go:73` |

Three independent keys are derived from the same `ENCRYPTION_KEY` string
(`cmd/server/main.go:54` and `:120`):

```
crypto AES key   = sha256(ENCRYPTION_KEY)
session HMAC key = sha256("mailsorter-session-v1|"     + ENCRYPTION_KEY)
state HMAC key   = sha256("mailsorter-oauth-state-v1|" + ENCRYPTION_KEY)
```

The distinct labels are the reason a session token cannot be replayed as an OAuth state
and vice versa (`auth.go:43-57`, asserted by `TestStateAndSessionUseDistinctKeys`).

## Key Files

| File | Responsibility |
|---|---|
| `backend/internal/auth/auth.go` | The whole credential primitive: `Manager`, `IssueSession`, `VerifySession`, `IssueState`, `VerifyState`, `sign`, `verify`. Pure, no I/O, 157 lines |
| `backend/internal/auth/auth_test.go` | 8 tests: round trip, tampered payload, wrong secret, expiry, malformed input, key separation |
| `backend/internal/crypto/crypto.go` | `Encryptor` with `Encrypt` / `Decrypt`, AES-256-GCM, nonce prefixed to the ciphertext, base64 std encoding |
| `backend/internal/crypto/crypto_test.go` | 4 tests: round trip, non-determinism (fresh nonce), wrong key, garbage input |
| `backend/internal/config/config.go` | `Load()` reads the env, `Validate()` gates boot on `ENCRYPTION_KEY` only |
| `backend/internal/config/config_test.go` | Table test of every rejected and accepted key shape, plus `getEnvList` parsing |
| `backend/internal/api/middleware.go` | `publicPrefixes`, `isPublicPath`, `authMiddleware`, `bearerToken`, `clientKey`, the token-bucket rate limiter |
| `backend/internal/api/middleware_test.go` | Public-vs-protected path table, bearer parsing, rate limiter, and the spoofed-header case |
| `backend/internal/api/handlers.go` | `GetAuthURL` (`:145`), `HandleAuthCallback` (`:155`), and the `Handler` struct that holds `encryptor` and `auth` |
| `backend/internal/api/respond.go` | `errReauthRequired` and `writeAuthError`: the 401 / 404 / 500 mapping for a dead Gmail grant |
| `backend/internal/api/routes.go` | Middleware order and the CORS allow-list |
| `backend/internal/api/routes_integration_test.go` | `TestProtectedRouteRejectsMissingSession`, `TestGmailCredentialsHaveNoHTTPSurface`, waitlist reachability |
| `backend/cmd/server/main.go` | Wires `Validate` -> `NewEncryptor` -> Gmail credentials -> `auth.NewManager` |
| `backend/cmd/test_decrypt/main.go` | One-off ops tool that decrypts the legacy `gmail_config` document |
| `frontend/src/services/api.js` | Axios request interceptor attaching the Bearer token, response interceptor clearing it on 401 |
| `frontend/src/pages/Login.js` | Starts the flow (`getAuthUrl` then `window.location.href`), and also handles a callback landing on `/` |
| `frontend/src/pages/AuthCallback.js` | Reads `code` and `state` from the URL, exchanges them, writes `localStorage` |

## Token Formats

Both credentials share `sign` / `verify` (`auth.go:129-157`). The MAC is computed over
the **already base64url-encoded** payload, not the raw bytes.

```
token = base64url_raw(payload) "." base64url_raw(HMAC_SHA256(key, base64url_raw(payload)))
```

| Credential | Payload | TTL constant | Value |
|---|---|---|---|
| Session token | `email + "|" + unixExpiry` | `auth.DefaultSessionTTL` | 30 * 24h (30 days) |
| OAuth state | `hex(16 random bytes) + "|" + unixExpiry` | `auth.DefaultStateTTL` | 10 minutes |

Verification details that matter:

- `verify` compares with `hmac.Equal` on the base64 strings, so the comparison is
  constant time.
- `VerifySession` splits the payload on the **last** `|`, because an email address
  cannot contain one (`auth.go:74-75`).
- Errors are sentinels: `ErrMalformedToken`, `ErrBadSignature`, `ErrExpired`. Callers in
  `api` never distinguish them; they all become a 401.
- `IssueState` falls back to a time-seeded nonce if `crypto/rand.Read` fails
  (`auth.go:98-101`). The signature still guarantees integrity, the nonce is only there
  for uniqueness.

## Flow 1: Login

Every step below is a line in the codebase, in order.

1. SPA calls `authService.getAuthUrl()` -> `GET /api/auth/url` (public prefix `/api/auth/`).
2. `GetAuthURL` calls `h.auth.IssueState()` then `h.gmailService.GetAuthURL(state)`
   (`handlers.go:148-149`), which builds the Google URL with
   `config.AuthCodeURL(state, oauth2.AccessTypeOffline)` (`gmail/gmail.go:76-80`).
   `AccessTypeOffline` is the only option passed: there is no `prompt=consent`.
3. Requested scopes, spelled out twice (`gmail/gmail.go:35-38` in `NewService` and
   `:60-63` in `UpdateConfig`): `gmail.readonly`, `gmail.modify`, `gmail.labels`,
   `gmail.send` (the last one exists only to send the daily digest as the user). Change
   one list and you must change the other.
4. `Login.js` does `window.location.href = response.data.authUrl` after `track('login_start')`.
5. Google redirects the **browser** to `GMAIL_REDIRECT_URL`, which points at the SPA
   (`/auth/callback`), not at the API. So the callback is a JSON endpoint the SPA calls,
   never a server-side 302.
6. `AuthCallback.js` reads `code` and `state` from the query string and calls
   `authService.handleCallback(code, state)` -> `GET /api/auth/callback?code=...&state=...`.
7. `HandleAuthCallback` rejects an empty `code` (400), then `h.auth.VerifyState(state)`;
   a bad or expired state is a 400 `Invalid OAuth state` (`handlers.go:164-168`).
8. `gmailService.ExchangeCode(code)` -> `GetClient(token)` -> `GetUserProfile(client)`
   yields the user's own email address, which **is** the `userId` everywhere else.
9. Upsert into `users` on `{email}`: `accessToken`, `refreshToken`, `tokenExpiry`,
   `updatedAt`, plus `email` / `createdAt` on insert (`handlers.go:187-202`).
10. `sessionToken := h.auth.IssueSession(userEmail)`; the response is
    `models.TokenResponse{accessToken, userEmail}` where `accessToken` is **our** session
    token, never the Google one (`handlers.go:208-216`).
11. `AuthCallback.js` writes `localStorage.userEmail` and `localStorage.accessToken`,
    fires `track('login_done')`, navigates to `/inbox`.

## Flow 2: Authenticated request

Chain declared in `routes.go:103-109`, outermost first:

```
CORS -> recover -> request-id -> metrics -> logging -> rate-limit -> auth -> handler
```

```
axios interceptor sets Authorization: Bearer <localStorage.accessToken>
  -> authMiddleware: r.Header.Del("X-User-Email")        <- unconditional, always first
  -> isPublicPath(r.URL.Path)?
  -> bearerToken(r)                                       <- "Bearer x", "bearer x" or bare "x"
  -> h.auth.VerifySession(token)
  -> r.Header.Set("X-User-Email", email)                  <- server-vouched value
  -> handler reads r.Header.Get("X-User-Email")           <- 46 non-test call sites in the api package
```

Outcome matrix (`middleware.go:63-87`, proven by `TestAuthMiddlewareIdentifiesOnPublicRoutesWithoutRequiringIt`):

| Route | No token | Valid token | Bad or expired token |
|---|---|---|---|
| Public | pass, anonymous | pass, `X-User-Email` set | pass, anonymous |
| Protected | 401 `Authentication required` | pass, `X-User-Email` set | 401 `Invalid or expired session` |
| Any | a client-supplied `X-User-Email` is dropped in all cases | | |

Public prefixes (`middleware.go:29-36`): `/health`, `/metrics`, `/api/auth/`,
`/api/config/status`, `/api/waitlist`, `/api/billing/webhook`. Matching is exact-or-prefix
via `strings.HasPrefix`. Only `/api/config/status` is listed, never `/api/config/`:
`TestGmailCredentialsHaveNoHTTPSurface` asserts both verbs on `/api/config/gmail` return
404, since instance-wide OAuth credentials would be a login hijack for every user.

## Flow 3: Gmail token refresh

The session token proves who you are; it says nothing about whether Google still trusts
us. That second check lives in `getUserToken` (`ai_handlers.go:770-806`).

```
handler -> gmailClientFor(ctx, userEmail) -> getUserToken
  -> users.FindOne({email})                     -> mongo.ErrNoDocuments => 404
  -> token.Expiry.Before(now)?
       refreshToken == ""                        => errReauthRequired
       gmailService.RefreshToken(refresh) fails   => errReauthRequired
       success -> users.UpdateOne accessToken + tokenExpiry
  -> gmailService.GetClient(token)
```

`writeAuthError` (`respond.go:20-29`) maps the result: `errReauthRequired` -> 401,
`mongo.ErrNoDocuments` -> 404, anything else -> 500. The 401 is what makes the axios
response interceptor clear `localStorage` and bounce to `/`, which restarts OAuth.
Returning the dead token instead would produce an unescapable stream of 500s, which is
why every handler that touches Gmail must use `writeAuthError` and not `http.Error`.

## Encryption at Rest: what is actually encrypted

`crypto.Encryptor` is AES-256-GCM. `Encrypt` draws a fresh `gcm.NonceSize()` nonce from
`crypto/rand`, passes it as the `dst` prefix to `gcm.Seal`, and base64-std encodes the
result, so the stored value is `nonce || ciphertext || tag`. `Decrypt` splits the nonce
back off and rejects anything shorter with `ciphertext too short`.

| Value | Storage | Encrypted? | Evidence |
|---|---|---|---|
| Legacy Gmail client secret | `gmail_config.clientSecretEncrypted` | Yes, AES-256-GCM | `models.go:105`, decrypted at `cmd/server/main.go:79` |
| Live Gmail client secret | `GMAIL_CLIENT_SECRET` env var | No, it is an env var | `config.go:55`, `main.go:62-67` |
| Per-user Gmail access token | `users.accessToken` | **No, plaintext** | `handlers.go:191` writes `token.AccessToken` raw |
| Per-user Gmail refresh token | `users.refreshToken` | **No, plaintext** | `handlers.go:192` writes `token.RefreshToken` raw |
| Session token | not stored server-side at all | n/a, stateless | `auth.go` has no store |

`Encryptor.Encrypt` has **zero production callers**. A grep for `.Encrypt(` across the Go
tree returns only its own definition and `crypto_test.go`. The only production call into
the package is `Decrypt`, twice, both on the legacy document
(`cmd/server/main.go:79`, `cmd/test_decrypt/main.go:39`). The encryption path is therefore
read-only legacy support, not an active protection for user tokens.

Both `models.User.AccessToken` and `.RefreshToken` carry `json:"-"`, so they are excluded
from any JSON response even if a handler marshals a whole `User`.

## Configuration

| Var | Default (`config.go`) | Validated at boot | Notes |
|---|---|---|---|
| `ENCRYPTION_KEY` | `default-dev-key-change-in-production` | Yes, hard fail | Rejected values: the compose default above and `change-this-to-a-secure-random-string-32chars`; also rejected if empty or under 32 chars (`minEncryptionKeyLen`) |
| `GMAIL_CLIENT_ID` | `""` | No | Empty means the boot falls through to the legacy `gmail_config` document |
| `GMAIL_CLIENT_SECRET` | `""` | No | Same |
| `GMAIL_REDIRECT_URL` | `http://localhost:3000/auth/callback` | No | Must match an authorized redirect URI in the Google Cloud project exactly |
| `ALLOWED_ORIGINS` | `http://localhost:3000`, `http://localhost`, `https://mailsorter.sohbi.dev` | No | Comma separated, whitespace trimmed, empties dropped |

`docker-compose.yml:30` interpolates `${ENCRYPTION_KEY:-default-dev-key-change-in-production}`,
which is one of the rejected placeholders. Running compose without a real `.env` therefore
fails at boot instead of silently starting with a public key: that is intentional
(`config.go:19-25`).

`main.go:62` prefers the environment outright; the `gmail_config` document is only read
when both `GMAIL_CLIENT_ID` and `GMAIL_CLIENT_SECRET` are empty, and nothing writes it
anymore.

## Rate limiting

`newRateLimiter(20, 40)` in `routes.go:103`: 20 tokens per second sustained, burst 40, one
bucket per client, swept every 5 minutes for buckets idle over 10 minutes. Exempt paths:
`/api/billing/webhook` (Stripe controls its own rate), `/health`, `/metrics`.

`clientKey` (`middleware.go:251-259`) prefers `"tok:" + bearerToken(r)` and falls back to
`"ip:" + host`. This runs **before** `authMiddleware`, so the bucket key is the raw,
unverified token string. See "Known pitfalls".

## Security invariants

Break one of these and the whole model is gone.

1. **`authMiddleware` must delete `X-User-Email` before anything else, unconditionally.**
   It is `middleware.go:61`, ahead of the public-path check on purpose. nginx proxies
   client headers through untouched (`frontend/nginx.conf:28-33`), so this line is the only
   thing between an attacker and full impersonation. `TestAuthMiddlewareIdentifiesOnPublicRoutesWithoutRequiringIt`
   has a `spoofed header is dropped` case; keep it green.
2. **A handler reads identity only from `r.Header.Get("X-User-Email")`.** Never from a
   request body, a query parameter, or a JSON field. An empty value is a 401, not a
   default user.
3. **Never send a Google token to the browser.** `HandleAuthCallback` returns a session
   token in the `accessToken` field precisely so the name in the SPA stays stable while
   the value stays ours.
4. **Never add a route under `/api/config/` to `publicPrefixes`, and never re-expose the
   Gmail credentials over HTTP.** The credentials are instance-wide, so an unauthenticated
   write hijacks the login flow for every user at once.
5. **The two HMAC keys stay separate.** If you add a third signed credential, derive it
   with a new label in `deriveKey`, never reuse `sessionKey` or `stateKey`.
6. **`config.Validate()` runs before anything else in `main`.** Adding a required secret
   means adding its check there, not a `log.Printf` warning further down.
7. **Untrusted HTML must keep going through dompurify** (`components/EmailReader.js:63`).
   The session token lives in `localStorage`, so one XSS in a rendered email body is a
   30-day account takeover.
8. **Any new Gmail-touching handler routes its token error through `writeAuthError`,**
   so a revoked grant yields 401 and the SPA re-runs OAuth.

## Known pitfalls

- **`ENCRYPTION_KEY` is three secrets in one, and rotating it logs everyone out.** It is
  the AES key, the session key and the state key. Changing it invalidates every session
  token in the wild (users get a 401 and land on `/`), breaks any OAuth handshake in
  flight, and makes the legacy `clientSecretEncrypted` permanently unreadable. There is
  no rotation path in the code.
- **The OAuth `state` is not single-use, despite the doc comment saying so.**
  `VerifyState` (`auth.go:108-125`) checks the signature and the expiry, nothing else.
  No nonce is stored, so the same state replays successfully for its full 10 minutes.
  It stops forgery, not replay.
- **The rate limiter keys on an unverified bearer token.** `rl.middleware` runs before
  `authMiddleware` in the chain, and `clientKey` returns `"tok:" + token` whenever an
  `Authorization` header is present. A caller that sends a different garbage token on
  every request gets a fresh bucket every time and never hits the per-IP limit.
- **There is no logout endpoint and no revocation list.** Logout is
  `localStorage.removeItem` in three places (`api.js:28-29`, `components/Header.js:18-19`,
  `pages/Account.js:65-66`). A token that leaked stays valid until its 30-day expiry.
- **Deleting an account does not invalidate its session token.** `DeleteAccount`
  (`account_data.go:115-147`) removes the `users` row, but `VerifySession` does no
  database lookup. The token keeps passing auth; the request then fails deeper with a
  404 `User not found` from `writeAuthError`.
- **`GET /api/auth/url` on an unconfigured instance.** `gmail.Service` nil-guards
  `s.config` in `IsConfigured` only (`gmail.go:70-74`); `GetAuthURL` (`:76-80`),
  `ExchangeCode` and `GetClient` dereference it with no check, and
  `NewService("", "", "")` leaves it nil (`gmail.go:27-41`).
  What keeps this path unreached is the SPA gate: the app boots on
  `GET /api/config/status` and redirects to `/setup` when `isConfigured` is false. Do not
  remove that gate without adding the nil check.
- **The middleware's 401 is `text/plain`, not the JSON error envelope.** It uses
  `http.Error` (`middleware.go:71`, `:82`), so `err.response.data.error` is undefined for
  those two responses. The axios interceptor keys on `status === 401` only, which is why
  nobody noticed. `HandleAuthCallback` uses `http.Error` too (`handlers.go:158`, `:166`,
  `:172`, `:179`, `:204`), so `AuthCallback.js` falls back to `err.message` there.
- **CORS still allows the `X-User-Email` request header** (`routes.go:115`) even though
  the middleware deletes it on arrival. Harmless, but do not read it as evidence that a
  client may send it.
- **`docs/ARCHITECTURE.md:197` claims the Gmail tokens are stored encrypted, and
  `.env.example:17` says `ENCRYPTION_KEY` is "used to encrypt the Gmail OAuth tokens
  stored per user". Neither matches the code.** `handlers.go:191-192` writes them
  plaintext. Per the constitution, the code wins.
- **`cmd/test_decrypt` prints the first 5 characters of the decrypted client secret to
  stdout** (`main.go:44-47`) and never calls `cfg.Validate()`. Do not run it anywhere the
  output is captured into logs.
- **`Encrypt` produces a different ciphertext every call** (fresh GCM nonce, asserted by
  `TestEncryptIsNonDeterministic`). Never use an encrypted value as a lookup key or in a
  Mongo equality filter.

## Testing

Existing coverage, standard library only, no mocks. Do not run the suite while other
agents are working; the commands are here for reference.

| Package | File | Covers |
|---|---|---|
| `auth` | `auth_test.go` | Session round trip, tampered payload, wrong secret, expiry (`sessionTTL = -time.Minute`), malformed inputs `"" "nodot" "." "abc." ".abc"`, state round trip, state expiry, session-vs-state key separation |
| `crypto` | `crypto_test.go` | Round trip, non-determinism, wrong key, invalid base64 and too-short ciphertext |
| `config` | `config_test.go` | The 6-case key table (empty, both placeholders, too short, exactly 32, strong), `getEnvList` default and parsing |
| `api` | `middleware_test.go` | Public and protected path tables, bearer parsing including the bare-token and case-insensitive forms, burst then block, per-client bucket isolation, the 4-case auth matrix |
| `api` | `routes_integration_test.go` | Real router over `httptest`: 401 on a protected route with no token, 404 on both verbs of `/api/config/gmail`, waitlist reachable without a session |

Manual checks that the unit tests cannot cover:

1. Boot refusal: unset `ENCRYPTION_KEY` and confirm the process exits with
   `Invalid configuration: ENCRYPTION_KEY is set to a known insecure default`.
2. Impersonation: send `X-User-Email: someone@else.com` with a valid token for another
   account and confirm the response is scoped to the token holder.
3. Expiry: no test can wait 30 days, so exercise it by temporarily lowering
   `DefaultSessionTTL` locally, never by shipping a shorter constant.
4. Revoked grant: revoke the Mailsorter access in the Google account security page, then
   hit any Gmail-backed route and confirm a 401 plus a bounce to `/`, not a 500 loop.

## References

### Source Files

- `backend/internal/auth/auth.go` - the entire credential primitive, 157 lines, pure
- `backend/internal/crypto/crypto.go` - AES-256-GCM helper, `Encrypt` currently unused in production
- `backend/internal/config/config.go` - env loading and the only fail-fast validation
- `backend/internal/api/middleware.go` - auth gate, public prefixes, rate limiter
- `backend/internal/api/handlers.go` - `GetAuthURL` and `HandleAuthCallback`
- `backend/internal/api/respond.go` - `errReauthRequired`, `writeAuthError`
- `backend/internal/api/ai_handlers.go` - `getUserToken`, the Gmail token refresh path
- `backend/internal/gmail/gmail.go` - `GetAuthURL`, `ExchangeCode`, `RefreshToken`, scopes
- `backend/cmd/server/main.go` - the wiring order that makes the invariants hold
- `frontend/src/services/api.js` - the two axios interceptors
- `.env.example` - documented env contract (its `ENCRYPTION_KEY` comment overstates what is encrypted)
- `docs/API.md` - the Authentication section and every per-endpoint auth requirement

### Related Context Docs

- [gmail-sync.md](gmail-sync.md) - what happens after `getUserToken` hands back a client
- [data-model.md](data-model.md) - the `users` and `gmail_config` documents these fields live in
- [frontend-inbox.md](frontend-inbox.md) - the SPA side: `localStorage` session identity and the 401 bounce
