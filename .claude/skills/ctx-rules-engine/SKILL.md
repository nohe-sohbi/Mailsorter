---
name: ctx-rules-engine
description: "Deterministic AI-free triage: sorting rules (conditions, operators, multi-action, priority, temporal), the protected-senders VIP veto on destructive actions, snooze scheduling and wake sweep, and the periodic-work schedule helper. Use when working on: sorting rules, rule engine, deterministic triage, condition operators, rule priority, dry run preview, protected senders, vip guardrail, snooze, wake time, snooze preset, autopilot, olderthan, newerthan, multi-action rule."
paths:
  - backend/internal/rules/rules.go
  - backend/internal/protect/protect.go
  - backend/internal/snooze/snooze.go
  - backend/internal/schedule/schedule.go
  - backend/internal/api/rules.go
  - backend/internal/api/snooze.go
  - backend/internal/api/protected.go
  - frontend/src/pages/Rules.js
  - frontend/src/pages/Snoozed.js
metadata:
  generated-by: context-skills-gen/v1
---

# Rules Engine (deterministic, AI-free triage) (context skill)

Deterministic AI-free triage: sorting rules (conditions, operators, multi-action, priority, temporal), the protected-senders VIP veto on destructive actions, snooze scheduling and wake sweep, and the periodic-work schedule helper. Use when working on: sorting rules, rule engine, deterministic triage, condition operators, rule priority, dry run preview, protected senders, vip guardrail, snooze, wake time, snooze preset, autopilot, olderthan, newerthan, multi-action rule.

**Key files:**
- `backend/internal/rules/rules.go`
- `backend/internal/protect/protect.go`
- `backend/internal/snooze/snooze.go`
- `backend/internal/schedule/schedule.go`
- `backend/internal/api/rules.go`
- `backend/internal/api/snooze.go`
- `backend/internal/api/protected.go`
- `frontend/src/pages/Rules.js`
- `frontend/src/pages/Snoozed.js`

**Full spec:** Read `.claude/context/rules-engine.md` for the complete specification. Do not rely on this summary for implementation details.

Related subsystems: `ai-triage`, `gmail-sync`, `data-model`, `background-workers`.
