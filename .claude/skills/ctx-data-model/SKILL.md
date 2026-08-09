---
name: ctx-data-model
description: "Exhaustive map of the 16 MongoDB collections, their Go structs, bson tags, indexes and owning packages, so a query can be written without opening the code. Use when working on: mongodb, collection, bson tag, index, ensureindexes, database.go, models.go, objectid, upsert, userid, persistence, schema, mongo query, collection names, field names."
paths:
  - backend/internal/models/models.go
  - backend/internal/database/database.go
  - backend/internal/account/account.go
  - backend/internal/api/account_data.go
  - mongo-init/init-db.js
  - docker-compose.yml
metadata:
  generated-by: context-skills-gen/v1
---

# Data Model: MongoDB Collections (context skill)

Exhaustive map of the 16 MongoDB collections, their Go structs, bson tags, indexes and owning packages, so a query can be written without opening the code. Use when working on: mongodb, collection, bson tag, index, ensureindexes, database.go, models.go, objectid, upsert, userid, persistence, schema, mongo query, collection names, field names.

**Key files:**
- `backend/internal/models/models.go`
- `backend/internal/database/database.go`
- `backend/internal/account/account.go`
- `backend/internal/api/account_data.go`
- `mongo-init/init-db.js`
- `docker-compose.yml`

**Full spec:** Read `.claude/context/data-model.md` for the complete specification. Do not rely on this summary for implementation details.

Related subsystems: `gmail-sync`, `ai-triage`, `rules-engine`, `billing-quota`.
