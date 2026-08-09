---
name: ctx-background-workers
description: "Everything Mailsorter runs without an HTTP request: server bootstrap, the async AI worker pool, the snooze wake sweep, the auto-sync sweep, the daily digest scheduler and shutdown. Use when working on: background worker, goroutine, ticker, scheduler, digest, auto-sync, snooze sweep, job queue, worker pool, graceful shutdown, bootstrap, main.go, cron, idempotency."
paths:
  - backend/cmd/server/main.go
  - backend/internal/api/auto_sync.go
  - backend/internal/api/digest_scheduler.go
  - backend/internal/api/jobs.go
  - backend/internal/api/snooze.go
  - backend/internal/api/handlers.go
  - backend/internal/api/ledger.go
  - backend/internal/digest/digest.go
  - backend/internal/mailer/mailer.go
  - backend/internal/schedule/schedule.go
  - backend/internal/activity/activity.go
metadata:
  generated-by: context-skills-gen/v1
---

# Background Workers (context skill)

Everything Mailsorter runs without an HTTP request: server bootstrap, the async AI worker pool, the snooze wake sweep, the auto-sync sweep, the daily digest scheduler and shutdown. Use when working on: background worker, goroutine, ticker, scheduler, digest, auto-sync, snooze sweep, job queue, worker pool, graceful shutdown, bootstrap, main.go, cron, idempotency.

**Key files:**
- `backend/cmd/server/main.go`
- `backend/internal/api/auto_sync.go`
- `backend/internal/api/digest_scheduler.go`
- `backend/internal/api/jobs.go`
- `backend/internal/api/snooze.go`
- `backend/internal/api/handlers.go`
- `backend/internal/api/ledger.go`
- `backend/internal/digest/digest.go`
- `backend/internal/mailer/mailer.go`
- `backend/internal/schedule/schedule.go`
- `backend/internal/activity/activity.go`

**Full spec:** Read `.claude/context/background-workers.md` for the complete specification. Do not rely on this summary for implementation details.

Related subsystems: `gmail-sync`, `ai-triage`, `data-model`.
