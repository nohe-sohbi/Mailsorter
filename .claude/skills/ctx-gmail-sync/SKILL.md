---
name: ctx-gmail-sync
description: "The Gmail v1 integration layer: OAuth scopes and token refresh, message listing and fetching, header and MIME body parsing, the retry/backoff wrapper, label and mutation operations, the inbox upsert into MongoDB, and List-Unsubscribe handling. Use when working on: gmail, gmail api, oauth scopes, token refresh, sync inbox, messages list, messages get, messages modify, retry backoff, 429, list-unsubscribe, one-click unsubscribe, mime body, base64url, mailbox stats."
paths:
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
metadata:
  generated-by: context-skills-gen/v1
---

# Gmail Sync (context skill)

The Gmail v1 integration layer: OAuth scopes and token refresh, message listing and fetching, header and MIME body parsing, the retry/backoff wrapper, label and mutation operations, the inbox upsert into MongoDB, and List-Unsubscribe handling. Use when working on: gmail, gmail api, oauth scopes, token refresh, sync inbox, messages list, messages get, messages modify, retry backoff, 429, list-unsubscribe, one-click unsubscribe, mime body, base64url, mailbox stats.

**Key files:**
- `backend/internal/gmail/gmail.go`
- `backend/internal/gmail/retry.go`
- `backend/internal/gmail/body_test.go`
- `backend/internal/gmail/headers_test.go`
- `backend/internal/gmail/retry_test.go`
- `backend/internal/gmail/unsubscribe_test.go`
- `backend/internal/api/handlers.go`
- `backend/internal/api/unsubscribe.go`
- `backend/internal/api/auto_sync.go`
- `backend/internal/models/models.go`

**Full spec:** Read `.claude/context/gmail-sync.md` for the complete specification. Do not rely on this summary for implementation details.

Related subsystems: `auth-session-security`, `data-model`, `background-workers`, `rules-engine`.
