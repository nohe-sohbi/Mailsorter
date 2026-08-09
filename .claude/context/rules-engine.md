---
subsystem: rules-engine
description: Deterministic AI-free triage: sorting rules (conditions, operators, multi-action, priority, temporal), the protected-senders VIP veto on destructive actions, snooze scheduling and wake sweep, and the periodic-work schedule helper.
keywords: [sorting rules, rule engine, deterministic triage, condition operators, rule priority, dry run preview, protected senders, vip guardrail, snooze, wake time, snooze preset, autopilot, olderthan, newerthan, multi-action rule]
files:
  - backend/internal/rules/rules.go
  - backend/internal/protect/protect.go
  - backend/internal/snooze/snooze.go
  - backend/internal/schedule/schedule.go
  - backend/internal/api/rules.go
  - backend/internal/api/snooze.go
  - backend/internal/api/protected.go
  - frontend/src/pages/Rules.js
  - frontend/src/pages/Snoozed.js
priority: high
related: [ai-triage, gmail-sync, data-model, background-workers]
last-verified: 2026-08-09
---
# Rules Engine (deterministic, AI-free triage)

The cheap half of Mailsorter. A sorting rule pairs conditions with actions and is
applied straight to Gmail with no model call and no quota burn. Layered on top,
the protected-senders list vetoes any *automated* destructive action, and snooze
moves a message out of the inbox with a scheduled return. Every decision here is
computed by a pure package under `backend/internal/`; `backend/internal/api` is
the only layer that touches Mongo or Gmail.

## Package map

| Package | Purity | Owns | Entry points |
|---|---|---|---|
| `internal/rules` | pure | matching, validation, dry-run projection | `Matches`, `MatchesAt`, `FirstMatch`, `FirstMatchAt`, `Preview`, `PreviewAt`, `Validate`, `EffectiveActions` |
| `internal/protect` | pure | VIP veto on destructive actions | `Allowed`, `Match`, `IsDestructive`, `NormalizeEntry`, `NormalizeAddress`, `Domain` |
| `internal/snooze` | pure | preset to absolute wake time | `Resolve` |
| `internal/schedule` | pure | "is this periodic work due" | `Due(last, now, interval)` |
| `internal/api/rules.go` | I/O | rule CRUD, apply, preview, per-message action fan-out | `GetRules`, `CreateRule`, `UpdateRule`, `DeleteRule`, `ApplyRules`, `PreviewRules`, `CreateSenderRule`, `enabledRules`, `applyRuleToMessage`, `applyOneAction` |
| `internal/api/protected.go` | I/O | VIP list CRUD, list loader | `GetProtected`, `CreateProtected`, `DeleteProtected`, `protectedValues`, `allows` |
| `internal/api/snooze.go` | I/O | snooze write, list, manual wake, 1 min sweeper | `Snooze`, `GetSnoozes`, `WakeSnooze`, `restoreSnoozed`, `startSnoozeLoop`, `wakeDueSnoozes` |

Every clock-dependent function has an `...At(..., now)` twin so temporal logic is
deterministic under test. `Matches`, `FirstMatch` and `Preview` are thin wrappers
that pass `time.Now()`.

## Rule anatomy

`models.SortingRule` (`backend/internal/models/models.go:155`), collection
`sorting_rules`.

| Field | JSON / BSON | Type | Meaning |
|---|---|---|---|
| ID | `id` / `_id` | string | Mongo ObjectID hex |
| UserID | `userId` | string | owner, always the caller's email |
| Name | `name` | string | required, also the aggregation key for hit counts |
| Enabled | `enabled` | bool | a disabled rule never matches |
| MatchAll | `matchAll` | bool | `true` = AND all conditions, `false` = OR any |
| Conditions | `conditions` | `[]RuleCondition` | at least one required |
| Action | `action` | string | legacy primary action, mirrors `Actions[0].Type` |
| LabelName | `labelName` | string | legacy primary label |
| Actions | `actions` | `[]RuleAction` | full ordered action list (multi-action) |
| Priority | `priority` | int | lower runs first |
| AppliedCount | `appliedCount` | int | incremented `$inc` after each apply pass |
| CreatedAt / UpdatedAt | `createdAt` / `updatedAt` | time | tiebreaker for sort order |

Two authoring shapes exist and `rules.EffectiveActions` collapses them into one
list: if `Actions` is non-empty it wins; otherwise a non-blank `Action` yields a
one-element list; otherwise `nil` (which `Validate` rejects). On write,
`ruleFromInput` (`backend/internal/api/rules.go:468`) backfills `Action` and
`LabelName` from `Actions[0]` so old readers and the per-rule stats keep working.

## Conditions: fields

Constants in `backend/internal/rules/rules.go:22`. `fieldValue` lower-cases the
field name before the switch, so `From` and `from` are the same field. Any other
field name yields `""` and never matches, and `Validate` rejects it.

| Field constant | Value | Source on `models.Email` | Note |
|---|---|---|---|
| `FieldFrom` | `from` | `Email.From` | the raw From header, display name included |
| `FieldSubject` | `subject` | `Email.Subject` | |
| `FieldSnippet` | `snippet` | `Email.Snippet` | Gmail's own snippet |
| `FieldTo` | `to` | `strings.Join(Email.To, " ")` | recipients joined with a single space |
| `FieldBody` | `body` | `Email.Body` | first text part, decoded from base64url |

## Conditions: operators (exhaustive)

Constants at `backend/internal/rules/rules.go:31`, evaluated in
`matchConditionAt` (`:130`). A condition whose `Value` trims to empty, or whose
`Field` is `""`, never matches: a blank rule cannot silently act on the whole
inbox.

| Operator constant | Value | Family | Semantics | Case |
|---|---|---|---|---|
| `OpContains` | `contains` | text | `strings.Contains` | insensitive |
| `OpEquals` | `equals` | text | `strings.EqualFold` on both sides trimmed | insensitive |
| `OpStartsWith` | `startsWith` | text | `strings.HasPrefix` | insensitive |
| `OpEndsWith` | `endsWith` | text | `strings.HasSuffix` | insensitive |
| `OpRegex` | `regex` | text | `regexp.Compile` then `MatchString` on the raw field | as written |
| `OpNotContains` | `notContains` | text | negation of `contains` | insensitive |
| `OpNotEquals` | `notEquals` | text | negation of `equals` | insensitive |
| `OpOlderThan` | `olderThan` | temporal | `ReceivedDate` strictly before `now - N days` | n/a |
| `OpNewerThan` | `newerThan` | temporal | `ReceivedDate` at or after `now - N days` | n/a |

Temporal specifics (`matchTemporal`, `backend/internal/rules/rules.go:166`):

- `Value` is a day count parsed with `strconv.Atoi` after trimming. A malformed
  value or a negative one never matches.
- `cutoff = now.Add(-days * 24h)`. `olderThan` is `ReceivedDate.Before(cutoff)`,
  `newerThan` is `!ReceivedDate.Before(cutoff)` (inclusive at the boundary).
- An email with a zero `ReceivedDate` never matches either temporal operator, so
  undated mail is never swept by an age rule.
- The temporal branch runs before `fieldValue`, so the condition's `Field` is
  ignored at match time even though `Validate` still requires it to be legal.

An unparsable regex returns `false` at match time (`matchConditionAt`) and is
rejected up front by `Validate`.

## Actions (exhaustive)

Constants at `backend/internal/rules/rules.go:51`. `applyOneAction`
(`backend/internal/api/rules.go:438`) is the only place a rule action reaches Gmail.

| Action constant | Value | Gmail mutation (`ModifyMessage(add, remove)`) | Destructive |
|---|---|---|---|
| `ActionArchive` | `archive` | remove `INBOX` | yes |
| `ActionTrash` | `trash` | add `TRASH` | yes |
| `ActionLabel` | `label` | add the resolved label ID, `labelName` required | no |
| `ActionMarkRead` | `markRead` | remove `UNREAD` | no |
| `ActionStar` | `star` | add `STARRED` | no |

Multi-action rules run their actions in list order (`applyRuleToMessage`,
`backend/internal/api/rules.go:423`). The canonical case the single-action shape
could not express is "label Newsletters, then archive". Label IDs are resolved
once per apply pass through a `labelCache map[labelName]labelID` and created on
demand by `h.ensureLabel`. A failing action is skipped; the remaining actions of
the same rule still run.

## Validation

`rules.Validate` (`backend/internal/rules/rules.go:309`) runs on both create and
update, before persistence, and returns a French human-readable error that the
handler passes through as the 400 body.

| Check | Failure |
|---|---|
| `Name` trims to non-empty | "le nom de la règle est requis" |
| `EffectiveActions` non-empty | "au moins une action est requise" |
| every action type in `validActions` | "action invalide" |
| `label` action has a non-blank `labelName` | label required |
| at least one condition | "au moins une condition est requise" |
| `strings.ToLower(field)` in `validFields` | "champ invalide", 1-based index |
| operator in `validOperators` | "opérateur invalide", 1-based index |
| condition value trims to non-empty | value required |
| `regex` operator compiles | invalid regular expression |
| temporal operator value parses as an int >= 0 | day count required |

Note the asymmetry: fields are matched case-insensitively, operators and action
types are matched exactly.

## Priority ordering

The full chain, from storage to winning action:

`loadRules` sorts `{priority: 1, createdAt: 1}` in Mongo -> `enabledRules` filters
out `Enabled == false` while preserving that order -> `FirstMatchAt` walks the
slice and returns the first rule whose conditions hold -> only that rule's actions
run for that email.

- Lower `priority` number wins. Equal priorities are broken by `createdAt`
  ascending (oldest first).
- Ordering is the caller's responsibility: `rules.FirstMatchAt` and
  `rules.PreviewAt` do not sort, they trust slice order. Anything that builds a
  ruleset by hand must sort it first.
- One email is attributed to exactly one rule per pass. A second matching rule
  never fires on the same message in the same pass.
- Index backing the sort: `{userId: 1, priority: 1}` on `sorting_rules`
  (`backend/internal/database/database.go:131`).

## Apply versus preview

Both handlers share `enabledRules`, the same `in:inbox` query with the same cap,
and the same first-match attribution, so the dry run is a faithful forecast of
the apply. They differ only in what happens after the match.

| | `POST /api/rules/apply` (`ApplyRules`) | `POST /api/rules/preview` (`PreviewRules`) |
|---|---|---|
| Handler | `backend/internal/api/rules.go:201` | `backend/internal/api/rules.go:292` |
| Context timeout | 90 s | 60 s |
| Gmail read | `ListMessages(client, "in:inbox", 200)` | same |
| Gmail write | yes, per action | none |
| Protected check | yes, per action, via `allows` | no, the preview does not model the veto |
| Ledger | `logAction(..., SourceRule)` per applied action | none |
| `appliedCount` | `$inc` per rule name | untouched |
| Empty ruleset | `{applied:0, scanned:0, byRule:{}}` | `{scanned:0, willApply:0, byRule:[], samples:[]}` |
| Response | `applied`, `scanned`, `byRule` (map name to count), `protectedSkipped` | `scanned`, `willApply`, `byRule` (`[]rules.RuleHits`), `samples` (`[]rules.PreviewItem`, capped at `previewSampleCap = 12`) |

`rules.Preview` returns `RuleHits` in the order rules first matched, tallied in a
`map[ruleName]int` index, and `PreviewItem` carries both the legacy
`action`/`labelName` primary pair and the full `actions` list.

## The three rule execution paths

| Path | Trigger | Code | Autopilot gate | Cap |
|---|---|---|---|---|
| Manual apply | user clicks "Appliquer maintenant" on `/rules` | `ApplyRules` | none | 200 messages |
| At-sync autopilot | any inbox sync, manual or background | `syncInbox` (`backend/internal/api/handlers.go:354`, rule block at `:406`) | `autoApplyRulesEnabled` reads the user's `autoApplyRules` setting | 100 messages |
| One-click sender rule | `POST /api/senders/rule` from the inbox | `ruleForSender` (`backend/internal/api/rules.go:357`) | n/a | n/a |

`ruleForSender` builds `{Name: "Expéditeur : <addr>", Enabled: true, MatchAll:
true, Conditions: [from contains <addr>], Priority: 0}` where `<addr>` comes from
`extractSenderAddress` (`backend/internal/api/ai_handlers.go:855`). It is pure and
its result goes through `rules.Validate` like any other rule.

Both apply paths persist per-rule counts with
`UpdateOne({userId, name}, {$inc: {appliedCount: n}})` and log one ledger row per
*action*, not per email, all tagged `SourceRule`.

## Protected senders (VIP guardrail)

The single safety net that stands between an automated pass and a VIP's mail.
Collection `protected_senders`, unique index `{userId: 1, value: 1}`
(`backend/internal/database/database.go:132`).

### Vocabulary and normalization

`protect.NormalizeEntry` classifies what the user typed and stores a canonical
lower-cased value:

| Input shape | Stored value | `Kind` |
|---|---|---|
| `Hi@Acme.com` | `hi@acme.com` | `address` |
| `Acme News <news@acme.com>` | `news@acme.com` | `address` |
| `"boss@corp.com"` | `boss@corp.com` | `address` |
| `@acme.com` | `acme.com` | `domain` |
| `acme.com` (no `@` at all) | `acme.com` | `domain` |
| `""` or `@` | `""`, rejected with 400 | `""` |

`protect.Match` compares a raw From header against the stored entries:

- The From header is reduced by `NormalizeAddress`, which strips a
  `Display Name <addr>` wrapper (including an unterminated `<`), trims quotes and
  whitespace, and lower-cases.
- An entry containing `@` must equal the address exactly.
- An entry without `@` matches the address domain exactly, or any subdomain
  (`acme.com` shields `mail.acme.com`). It deliberately does not match
  `evilacme.com`: the check is `domain == e || strings.HasSuffix(domain, "."+e)`,
  not a substring test.
- Empty entry list or unparsable From: no match, so nothing is blocked.

### What the veto blocks

`protect.IsDestructive` lower-cases and trims, then looks up a three-entry set:
`archive`, `trash`, `delete`. That covers both vocabularies in the app at once,
since rules say `trash` while the AI and direct-action paths say `delete`.

`protect.Allowed(action, from, entries)` returns `true` immediately for any
non-destructive action, and otherwise returns `!Match(from, entries)`. Labelling,
starring, marking read and "keep" are never blocked.

### Enforcement points (every call site)

`h.protectedValues(ctx, userEmail)` loads the entries; `allows(...)` in
`backend/internal/api/protected.go:42` is the one-line wrapper over
`protect.Allowed`. Complete list of call sites in the non-test code:

| Site | File:line | What it guards | Behaviour when blocked |
|---|---|---|---|
| Rule action fan-out | `backend/internal/api/rules.go:425` | every action of a matched rule, manual apply and at-sync autopilot alike | that action is skipped, `protectedSkip = true`, non-destructive actions of the same rule still run |
| Sender auto-pilot | `backend/internal/api/analysis.go:82` | `SenderPreference.DefaultAction` with `autoApply: true` | auto-apply is skipped, the email falls through to a normal (non-destructive) suggestion |
| AI verdict downgrade | `backend/internal/api/analysis.go:169` (`protectAnalysis`, called at `:93` and `:149`) | the suggestion about to be persisted | action rewritten to `keep`, label cleared, reasoning set to the protected-sender message, confidence floored at 0.9 |
| Bulk suggestion apply | `backend/internal/api/ai_handlers.go:331` (`ApplyBatch`) | each suggestion in a batch, sender resolved via `h.senderOf` | suggestion left pending, `protectedSkipped++` |
| Bulk sender action | `backend/internal/api/ai_handlers.go:440` (`ApplyBulk`) | the whole-sender sweep | email skipped, `protectedSkipped++` |

Two things are deliberately **not** guarded, because they are a single explicit
human gesture rather than an automated pass:

- `EmailAction` (`backend/internal/api/handlers.go:433`, `POST /api/emails/action`),
  the reader buttons and keyboard shortcuts.
- `ApplySuggestion` (`backend/internal/api/ai_handlers.go:196`,
  `POST /api/ai/apply`), applying one suggestion the user clicked.

The AI path relies on `protectAnalysis` having already downgraded the stored
suggestion at creation time, which only holds if the sender was protected *before*
the analysis ran.

`protectedValues` returns `nil` on a Mongo error and the caller proceeds. The
comment at `backend/internal/api/protected.go:17` states the intent explicitly:
protection is a safety bonus on the user's own choices, not an access-control
boundary, so a query failure must not block legitimate triage. It fails open.

## Snooze

`POST /api/emails/snooze` pulls a message out of the inbox until a wake time; a
1 min sweeper puts it back, marked unread.

### Presets

`snooze.Resolve(preset, now)` computes in `now`'s location and always returns a
time strictly after `now`. Anchors: `morningHour = 8`, `eveningHour = 18`.

| Constant | Wire value | Resolution | Edge case |
|---|---|---|---|
| `PresetLaterToday` | `laterToday` | `max(now + 3h, today 18:00)` | at 20:00 the evening anchor is past, so 23:00 |
| `PresetThisEvening` | `thisEvening` | today 18:00 | at or past 18:00, rolls to tomorrow 18:00 |
| `PresetTomorrow` | `tomorrow` | tomorrow 08:00 | none |
| `PresetThisWeekend` | `weekend` | coming Saturday 08:00 | on Saturday after 08:00, rolls a full week |
| `PresetNextWeek` | `nextWeek` | coming Monday 08:00 | same roll-forward rule |

An unknown preset returns an error, and the handler answers 400 so the caller can
fall back to an explicit timestamp.

### Write path

`Snooze` (`backend/internal/api/snooze.go:37`), 30 s context:

1. `messageId` required, else 400.
2. Wake time: an explicit `wakeAt` in the body wins; otherwise `snooze.Resolve(preset, now)`.
3. `wakeAt` must be strictly after now, else 400.
4. Best-effort `GetMessage` to enrich the row with `from`, `subject`, `threadId`. A failure leaves them empty and does not abort.
5. `ensureLabel` for `snoozeLabelName = "Mailsorter/Reporté"`, then `ModifyMessage(add: [labelID], remove: ["INBOX"])`.
6. Upsert on `{userId, messageId, status: "scheduled"}`, setting `wakeAt`, `status`, `updatedAt`.
7. `logAction(..., "archive", SourceSnooze)`.

### Wake path

Two entry points converge on `restoreSnoozed` (`backend/internal/api/snooze.go:203`),
which is `ModifyMessage(add: ["INBOX", "UNREAD"], remove: [snooze label])`. The
label is resolved through `ensureLabel`; if that fails, the removal list stays
empty and the message returns to the inbox still carrying the snooze label.

- Manual: `POST /api/snoozes/{id}/wake` -> find by `{_id, userId}` -> restore ->
  `status: "done"` -> `logAction(..., "unarchive", SourceSnooze)`.
- Automatic: `startSnoozeLoop` ticks every `snoozeSweepInterval = 1 min` ->
  `wakeDueSnoozes` (2 min context) queries `{status: "scheduled", wakeAt: {$lte: now}}`
  with `SetLimit(200)` **across all users** -> one Gmail client cached per user for
  the batch -> restore each -> `status: "done"` + ledger row.

Failure handling in the sweep: the error is logged with message ID, user and
attempt number, `attempts` is `$inc`-ed, and once `attempts + 1 >= maxSnoozeWakeAttempts = 5`
the row is parked as `status: "failed"` so it drops out of the scheduled query
instead of being retried every 60 s forever.

| `Snooze.Status` | Set by |
|---|---|
| `scheduled` | the write path (upsert) |
| `done` | manual wake, or a successful sweep restore |
| `failed` | the sweep after 5 failed attempts |
| `cancelled` | declared in the model comment, never written by current code |

Indexes: `{status: 1, wakeAt: 1}` for the sweep, `{userId: 1, status: 1}` for the
list (`backend/internal/database/database.go:133-134`).

## Schedule helper

`schedule.Due(last, now, interval)` (20 lines, `backend/internal/schedule/schedule.go`):
returns `true` when `last.IsZero()` (never run) or `interval <= 0`, otherwise
`!now.Before(last.Add(interval))`. The boundary is inclusive: exactly one interval
after the last run counts as due.

Its only non-test caller is `runDueAutoSyncs` (`backend/internal/api/auto_sync.go:59`),
gating per user at `autoSyncInterval = 30 min` under a
`autoSyncSweepInterval = 5 min` ticker. The snooze sweep does not use it (its
due-ness lives in the Mongo query), and the digest uses `mailer.DueAt` instead.

## HTTP surface

Registered in `backend/internal/api/routes.go`; all of these sit behind the auth
middleware, so the handler reads the caller from `X-User-Email`.

| Method | Route | Handler | routes.go line |
|---|---|---|---|
| GET | `/api/rules` | `GetRules` | 57 |
| POST | `/api/rules` | `CreateRule` | 58 |
| POST | `/api/rules/apply` | `ApplyRules` | 59 |
| POST | `/api/rules/preview` | `PreviewRules` | 60 |
| PUT | `/api/rules/{id}` | `UpdateRule` | 61 |
| DELETE | `/api/rules/{id}` | `DeleteRule` | 62 |
| POST | `/api/senders/rule` | `CreateSenderRule` | 84 |
| GET | `/api/protected` | `GetProtected` | 38 |
| POST | `/api/protected` | `CreateProtected` | 39 |
| DELETE | `/api/protected/{id}` | `DeleteProtected` | 40 |
| POST | `/api/emails/snooze` | `Snooze` | 24 |
| GET | `/api/snoozes` | `GetSnoozes` | 34 |
| POST | `/api/snoozes/{id}/wake` | `WakeSnooze` | 35 |

`/api/rules/apply` and `/api/rules/preview` are registered *before* `/api/rules/{id}`,
which is what keeps `apply` and `preview` from being swallowed by the id route.

`GET /api/snoozes` accepts `?status=`, defaulting to `scheduled`, sorted
`wakeAt: 1`, limit 200.

Handlers in this subsystem are pre-`respond.go` style: they call `http.Error` and
`json.NewEncoder(w).Encode(...)` directly rather than `writeError` / `writeJSON`.
Only `decodeJSON` and `writeAuthError` are used. A new handler here should follow
the constitution (`writeJSON` / `writeError`), not the surrounding code.

## Frontend surface

| Page | Service | What it drives |
|---|---|---|
| `frontend/src/pages/Rules.js` | `ruleService` (`services/api.js:127`) | rule list, editor, enable toggle, "Aperçu" dry run, "Appliquer maintenant", autopilot switch via `accountService.updateSettings({autoApplyRules})` |
| `frontend/src/pages/Snoozed.js` | `snoozeService` (`services/api.js:63`) | scheduled list, manual "Réactiver" |
| `frontend/src/components/EmailReader.js` | `emailService.snooze` (`services/api.js:60`) | the `SNOOZE_PRESETS` menu, five entries matching the Go constants |
| `frontend/src/pages/Settings.js` | `protectService` (`services/api.js:68`) | the `ProtectedSenders` card: add, list, remove |
| `frontend/src/pages/Inbox.js` | `protectService.add(email.from)` at `:364` | one-click protect from the inbox, the raw From header is sent and normalized server-side |

`Rules.js` mirrors the Go vocabulary in three constant arrays that must stay in
sync: `FIELDS` (5 entries), `OPERATORS` (9 entries) and `ACTIONS` (5 entries). Its
`effectiveActions` helper duplicates `rules.EffectiveActions` in JS, defaulting to
`archive` when a legacy rule has no action. Client-side checks (name, at least one
action, label name, non-empty condition values) are a UX shortcut; `rules.Validate`
is the authority and its French error string is surfaced through
`err.response?.data`.

The editor disables an action type already used, except `label`, which may repeat
with different names. Temporal operators switch the value input to
`type="number"` with `min=0`.

## Tuning constants (current values)

```
previewSampleCap:       12 (items)      cap on the dry-run sample list
ApplyRules scan:        200 (messages)  ListMessages "in:inbox"
ApplyRules timeout:     90 s
PreviewRules scan:      200 (messages)
PreviewRules timeout:   60 s
syncInbox scan:         100 (messages)  the autopilot path
snoozeLabelName:        "Mailsorter/Reporté"
snoozeSweepInterval:    1 min           background wake ticker
maxSnoozeWakeAttempts:  5               then status becomes "failed"
wakeDueSnoozes limit:   200 (rows)      per sweep, all users combined
wakeDueSnoozes timeout: 2 min
Snooze handler timeout: 30 s
morningHour:            8               tomorrow / weekend / nextWeek anchor
eveningHour:            18              laterToday / thisEvening anchor
laterToday offset:      3 h
autoSyncInterval:       30 min          minimum between two background syncs
autoSyncSweepInterval:  5 min           ticker that looks for due users
CRUD handler timeout:   10 s            rules and protected CRUD
```

## Testing

Standard library only, table-driven, no mocks. Run with
`cd backend && go test ./internal/rules/... ./internal/protect/... ./internal/snooze/... ./internal/schedule/...`.

| File | Covers |
|---|---|
| `backend/internal/rules/rules_test.go` | all seven text operators plus their misses, unknown field, unknown operator, invalid regex, empty value, AND versus OR, disabled and condition-less guards, `FirstMatch` ordering, `Preview` tallies and disabled-rule exclusion, `Validate`, `EffectiveActions`, multi-action validation and preview |
| `backend/internal/rules/rules_temporal_test.go` | `notContains` / `notEquals` including the empty-value guard, `olderThan` / `newerThan` against 40-day and 2-day emails, undated email, malformed day count, temporal through `MatchesAt` and `PreviewAt`, temporal validation (accepts `0`, rejects non-numeric and negative) |
| `backend/internal/protect/protect_test.go` | `NormalizeAddress` (display name, quotes, unterminated `<`), `NormalizeEntry` classification, `Match` (exact, case, domain, subdomain, the `evilacme.com` non-match), `IsDestructive` on both vocabularies, `Allowed` in all three combinations |
| `backend/internal/snooze/snooze_test.go` | the five presets against a fixed Wednesday 10:00, `laterToday` at 20:00, `thisEvening` past 18:00, `weekend` on a Saturday morning, unknown preset |
| `backend/internal/schedule/schedule_test.go` | `Due` including the zero-`last` and non-positive-interval cases |
| `backend/internal/api/protected_test.go` | `protectAnalysis` downgrade, the `allows` wrapper |
| `backend/internal/api/helpers_test.go` | `TestRuleForSender`, `TestExtractSenderAddress` |

Manual recette for the guardrail: protect a sender in `/settings`, create a rule
that would archive them, run "Aperçu" on `/rules` (the sender still appears: the
preview does not model the veto), then "Appliquer maintenant" and check the
response `protectedSkipped` and that the message is still in the inbox.

## Known pitfalls

- **The preview does not model the protected-senders veto.** `PreviewRules` never
  calls `protectedValues`, so `willApply` counts emails that `ApplyRules` will
  then skip. `willApply` is an upper bound, not a forecast of `applied`.
- **Rule hit counts are keyed by name, not by id.** `Preview` indexes its tally on
  `match.Name` and both apply paths `$inc` with `UpdateOne({userId, name})`.
  Two rules with the same name merge in the preview and credit the wrong row in
  Mongo. Nothing enforces name uniqueness.
- **A temporal condition ignores its field.** `matchConditionAt` dispatches on the
  operator family before reading the field, so `subject olderThan 30` and
  `from olderThan 30` are the same rule. `Validate` still demands a legal field,
  which makes the UI look meaningful when it is not.
- **`FirstMatch` trusts slice order.** It does no sorting. Ordering comes from
  `loadRules`'s Mongo sort. Any code path that assembles a ruleset another way
  gets non-deterministic priority.
- **`enabledRules` swallows the load error** (`backend/internal/api/rules.go:39`
  returns `nil` on error), so a Mongo failure during apply looks exactly like
  "the user has no rules": HTTP 200 with `applied: 0`.
- **`protectedValues` fails open.** A Mongo error returns `nil`, and `Allowed`
  with an empty entry list permits everything. A protection outage silently
  degrades to no protection. That is the documented intent, not a bug, but it
  means the guardrail is not an authorization boundary.
- **A suggestion created before the sender was protected stays destructive.**
  `protectAnalysis` runs at analysis time. `ApplySuggestion` (single click) does
  not re-check, so an old pending `archive` suggestion for a now-protected sender
  will apply. `ApplyBatch` does re-check.
- **Protecting a sender does not disable a matching rule.** The veto only blocks
  `archive` / `trash` / `delete`. A rule that labels or stars a VIP keeps running,
  and a multi-action rule partially applies: the label lands, the archive does not.
- **`body` conditions see the first text part only.** `gmail.GetEmailBody` returns
  `Payload.Body.Data` if present, else the first `text/plain` **or** `text/html`
  part of `Payload.Parts`, with no recursion into nested multiparts. For most
  marketing mail that means the condition is matched against raw HTML source, so
  `body contains "unsubscribe"` can hit markup, and deeply nested parts are
  invisible to the rule.
- **Apply and preview are N+1 Gmail calls.** `ListMessages` issues one
  `messages.list` then one `messages.get` with `Format("full")` per message
  (`backend/internal/gmail/gmail.go:141`). A 200-message apply is around 201 Gmail
  round trips inside a 90 s context; a slow mailbox can time out mid-pass, leaving
  a partial apply (already-mutated messages stay mutated, the ledger stays
  consistent with them).
- **Autopilot and manual apply scan different depths.** 100 messages at sync
  versus 200 on manual apply, so a rule can look inert in autopilot and fire on
  the manual run.
- **The snooze sweep is global and unsorted.** `wakeDueSnoozes` queries all users
  with `SetLimit(200)` and no sort. With more than 200 rows due at once, which
  ones wake first is whatever Mongo returns, and the remainder waits for the next
  minute.
- **Nothing enforces one scheduled snooze per message.** The upsert filters on
  `{userId, messageId, status: "scheduled"}` but the only `snoozes` indexes are
  non-unique. Concurrent snooze calls on the same message can create duplicate
  scheduled rows.
- **`SortingRuleInput.Enabled` is a plain bool.** An omitted `enabled` in the JSON
  body decodes to `false`, so a partial `PUT /api/rules/{id}` silently disables
  the rule. The frontend always sends the full object; any other client must too.
  The same applies to `matchAll` and `priority`.
- **Two action vocabularies coexist.** Rules say `trash`; the AI, bulk and direct
  paths say `delete`. `protect.IsDestructive` covers both, but any new switch on
  an action string must handle the right one, and `applyOneAction` has no default
  case: an unknown type returns `nil` and is recorded as applied.
- **A failed `ensureLabel` on wake leaves the snooze label attached.**
  `restoreSnoozed` only appends the label ID to the removal list when the lookup
  succeeds; the message still returns to the inbox, just still tagged.

## Key Files

| File | Description |
|---|---|
| `backend/internal/rules/rules.go` | matcher, validator, preview, all field/operator/action constants |
| `backend/internal/rules/rules_test.go` | operator, ordering, preview and validation coverage |
| `backend/internal/rules/rules_temporal_test.go` | negation and temporal operator coverage |
| `backend/internal/protect/protect.go` | VIP normalization, matching and the destructive-action veto |
| `backend/internal/protect/protect_test.go` | normalization and match coverage |
| `backend/internal/snooze/snooze.go` | preset to wake time |
| `backend/internal/snooze/snooze_test.go` | preset and roll-forward coverage |
| `backend/internal/schedule/schedule.go` | `Due(last, now, interval)` |
| `backend/internal/api/rules.go` | rule CRUD, apply, preview, per-message action fan-out |
| `backend/internal/api/protected.go` | VIP CRUD, `protectedValues`, the `allows` wrapper |
| `backend/internal/api/snooze.go` | snooze write, list, manual wake, 1 min sweeper |
| `backend/internal/api/analysis.go` | `protectAnalysis` and the auto-pilot guard |
| `backend/internal/api/ai_handlers.go` | `ApplyBatch` / `ApplyBulk` guards, `extractSenderAddress` |
| `backend/internal/api/handlers.go` | `syncInbox`, the at-sync autopilot |
| `backend/internal/api/auto_sync.go` | the only `schedule.Due` caller |
| `backend/internal/api/ledger.go` | `SourceRule`, `SourceSnooze`, `logAction` |
| `backend/internal/models/models.go` | `SortingRule`, `RuleCondition`, `RuleAction`, `ProtectedSender`, `Snooze` |
| `backend/internal/database/database.go` | `sorting_rules`, `protected_senders`, `snoozes` accessors and indexes |
| `frontend/src/pages/Rules.js` | rule editor, dry run, autopilot toggle |
| `frontend/src/pages/Snoozed.js` | scheduled list and manual wake |
| `frontend/src/components/EmailReader.js` | `SNOOZE_PRESETS` menu |
| `frontend/src/pages/Settings.js` | the protected-senders card |

## References

### Source Files

- `backend/internal/rules/rules.go` - the whole deterministic matcher, 350 lines, no I/O
- `backend/internal/protect/protect.go` - 149 lines, the only place "destructive" is defined
- `backend/internal/snooze/snooze.go` - 82 lines, clock-injected preset resolution
- `backend/internal/schedule/schedule.go` - 20 lines, one function
- `backend/internal/api/rules.go` - the I/O half: Mongo, Gmail, ledger
- `backend/internal/api/protected.go` - VIP CRUD and the fail-open loader
- `backend/internal/api/snooze.go` - write path, wake path, sweeper
- `backend/internal/api/routes.go` - route registrations, lines 24, 34-35, 38-40, 57-62, 84
- `backend/internal/models/models.go` - lines 127-245 for every struct in this subsystem
- `frontend/src/services/api.js` - `ruleService`, `snoozeService`, `protectService`

### Related Context Docs

- [ai-triage.md](ai-triage.md) - the expensive half: what runs when no rule matched, and where `protectAnalysis` sits in it
- [gmail-sync.md](gmail-sync.md) - `syncInbox`, `ListMessages`, header and body parsing that feed the matcher
- [data-model.md](data-model.md) - `sorting_rules`, `protected_senders`, `snoozes` collections and indexes
- [background-workers.md](background-workers.md) - the snooze sweeper and the auto-sync loop that hosts the autopilot
