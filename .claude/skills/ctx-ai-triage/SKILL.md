---
name: ctx-ai-triage
description: "Mistral-backed email triage: synchronous and async analysis, the job queue, the shared analysis cache, the suggestion lifecycle, sender auto-pilot, smart labels and the monthly quota that bounds the billed API. Use when working on: mistral, ai triage, analyze emails, ai suggestion, analysis cache, analysis job, sender preference, auto-pilot, smart labels, quota, apply batch, apply bulk, analyze-async, cache key, ai quota."
paths:
  - backend/internal/ai/mistral.go
  - backend/internal/ai/mistral_test.go
  - backend/internal/api/analysis.go
  - backend/internal/api/analysis_test.go
  - backend/internal/api/ai_handlers.go
  - backend/internal/api/jobs.go
  - backend/internal/api/account.go
  - backend/internal/models/models.go
  - backend/internal/api/routes.go
metadata:
  generated-by: context-skills-gen/v1
---

# AI Triage (Mistral) (context skill)

Mistral-backed email triage: synchronous and async analysis, the job queue, the shared analysis cache, the suggestion lifecycle, sender auto-pilot, smart labels and the monthly quota that bounds the billed API. Use when working on: mistral, ai triage, analyze emails, ai suggestion, analysis cache, analysis job, sender preference, auto-pilot, smart labels, quota, apply batch, apply bulk, analyze-async, cache key, ai quota.

**Key files:**
- `backend/internal/ai/mistral.go`
- `backend/internal/ai/mistral_test.go`
- `backend/internal/api/analysis.go`
- `backend/internal/api/analysis_test.go`
- `backend/internal/api/ai_handlers.go`
- `backend/internal/api/jobs.go`
- `backend/internal/api/account.go`
- `backend/internal/models/models.go`
- `backend/internal/api/routes.go`

**Full spec:** Read `.claude/context/ai-triage.md` for the complete specification. Do not rely on this summary for implementation details.

Related subsystems: `rules-engine`, `billing-quota`, `data-model`, `frontend-inbox`.
