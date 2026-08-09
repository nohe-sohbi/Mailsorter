---
name: ctx-billing-quota
description: "Stripe checkout, portal and webhook, the free/pro plan split, the monthly AI usage counter that gates analysis, account profile and settings, the RGPD export and erasure, and the Pro waitlist. Use when working on: stripe, billing, checkout, webhook, subscription, plan, pro, free, quota, usage, monetisation, rgpd, gdpr, export, account deletion, waitlist."
paths:
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
metadata:
  generated-by: context-skills-gen/v1
---

# Billing, Quota and Account Data (context skill)

Stripe checkout, portal and webhook, the free/pro plan split, the monthly AI usage counter that gates analysis, account profile and settings, the RGPD export and erasure, and the Pro waitlist. Use when working on: stripe, billing, checkout, webhook, subscription, plan, pro, free, quota, usage, monetisation, rgpd, gdpr, export, account deletion, waitlist.

**Key files:**
- `backend/internal/billing/stripe.go`
- `backend/internal/api/billing.go`
- `backend/internal/api/account.go`
- `backend/internal/api/account_data.go`
- `backend/internal/api/waitlist.go`
- `backend/internal/api/waitlist_test.go`
- `backend/internal/account/account.go`
- `backend/internal/account/account_test.go`
- `frontend/src/pages/Pricing.js`
- `frontend/src/pages/Account.js`
- `.env.example`

**Full spec:** Read `.claude/context/billing-quota.md` for the complete specification. Do not rely on this summary for implementation details.

Related subsystems: `ai-triage`, `data-model`, `auth-session-security`.
