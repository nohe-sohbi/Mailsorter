---
name: ctx-auth-session-security
description: "Google OAuth login, stateless HMAC session tokens and OAuth state, the X-User-Email strip-and-reinject middleware, and what AES-256-GCM actually protects at rest. Use when working on: auth, oauth, session token, hmac, bearer token, x-user-email, middleware, encryption key, aes-gcm, csrf state, login, refresh token, rate limiter, publicprefixes, accesstoken."
paths:
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
metadata:
  generated-by: context-skills-gen/v1
---

# Auth, Session and Secrets (context skill)

Google OAuth login, stateless HMAC session tokens and OAuth state, the X-User-Email strip-and-reinject middleware, and what AES-256-GCM actually protects at rest. Use when working on: auth, oauth, session token, hmac, bearer token, x-user-email, middleware, encryption key, aes-gcm, csrf state, login, refresh token, rate limiter, publicprefixes, accesstoken.

**Key files:**
- `backend/internal/auth/auth.go`
- `backend/internal/auth/auth_test.go`
- `backend/internal/crypto/crypto.go`
- `backend/internal/crypto/crypto_test.go`
- `backend/internal/config/config.go`
- `backend/internal/config/config_test.go`
- `backend/internal/api/middleware.go`
- `backend/internal/api/middleware_test.go`
- `backend/internal/api/handlers.go`
- `backend/internal/api/respond.go`
- `backend/internal/api/routes.go`
- `backend/internal/api/routes_integration_test.go`
- `backend/cmd/server/main.go`
- `backend/cmd/test_decrypt/main.go`
- `frontend/src/services/api.js`
- `frontend/src/pages/Login.js`
- `frontend/src/pages/AuthCallback.js`

**Full spec:** Read `.claude/context/auth-session-security.md` for the complete specification. Do not rely on this summary for implementation details.

Related subsystems: `gmail-sync`, `data-model`, `frontend-inbox`.
