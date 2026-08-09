---
subsystem: background-workers
description: Everything Mailsorter runs without an HTTP request: server bootstrap, the async AI worker pool, the snooze wake sweep, the auto-sync sweep, the daily digest scheduler and shutdown.
keywords: [background worker, goroutine, ticker, scheduler, digest, auto-sync, snooze sweep, job queue, worker pool, graceful shutdown, bootstrap, main.go, cron, idempotency]
files:
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
priority: medium
related: [gmail-sync, ai-triage, data-model]
last-verified: 2026-08-09
---
# Background Workers

Five long-lived goroutines run inside the single backend process: one worker pool
draining the AI analysis queue and four tickers (snooze wake, daily digest, auto-sync,
rate-limiter bucket eviction). There is no cron, no external scheduler and no queue
broker. All of them are started during process bootstrap, none of them is stopped at
shutdown, and every one of them is torn down abruptly when the container is replaced.

## Overview

| Aspect | Reality |
|---|---|
| Mechanism | `go func()` + `time.NewTicker`, plus a buffered `chan string` for the job pool |
| Where started | Four in `api.NewHandler` (`handlers.go:85-91`), the fifth in `newRateLimiter` called from `SetupRoutes` (`routes.go:103`) |
| Started before listening | Yes. `NewHandler` runs at `main.go:129`, `srv.ListenAndServe` at `main.go:149` |
| Coordination | None. No leader election, no distributed lease, no `sync.WaitGroup` |
| Per-user gating | Pure functions: `schedule.Due` (auto-sync) and `mailer.DueAt` (digest) |
| Stop signal | None reaches the loops. `srv.Shutdown` drains HTTP connections only |
| Error policy | Best effort. Every per-user or per-message failure is logged and skipped so one bad account never stalls a sweep |

The interval constants are deliberately split in two families: a **sweep interval**
(how often the goroutine wakes) and a **work interval** (how stale a given user's last
run has to be before work actually happens). Reading only one of the two gives a wrong
cadence.

## Every background loop

| Loop | File | Cadence (constant) | What it does | What it writes |
|---|---|---|---|---|
| Analysis worker pool (3 goroutines) | `jobs.go:19-29` | Not timed: `for jobID := range h.jobQueue`, `jobQueue` is `make(chan string, 256)` (`handlers.go:80`), pool size is the literal `3` at `handlers.go:85` | Pops a job id, loads the `analysis_jobs` doc, runs `h.runAnalysis` with a progress callback | `analysis_jobs` (status queued/running/done/error, counters), plus everything `runAnalysis` writes: `ai_suggestions`, `analysis_cache`, `usage`, `action_log` |
| Snooze wake sweep | `snooze.go:213-282` | `snoozeSweepInterval = time.Minute` | Finds `snoozes` with `status:"scheduled"` and `wakeAt <= now` (limit 200), restores each to INBOX+UNREAD, strips the snooze label | `snoozes` (status done/failed, attempts), `action_log` (`unarchive` / `SourceSnooze`), Gmail labels |
| Daily digest | `digest_scheduler.go:32-116` | Ticker `digestSweepInterval = 15 * time.Minute`; per-user gate `mailer.DueAt(u.DigestLastSentAt, now, hour)` = at most one send per user per UTC day | Loads the 7-day ledger recap, renders it, sends it through the user's own Gmail | `users.digestLastSentAt`, and an email in the user's own mailbox |
| Auto-sync sweep | `auto_sync.go:25-83` | Ticker `autoSyncSweepInterval = 5 * time.Minute`; per-user gate `schedule.Due(u.LastAutoSyncAt, now, autoSyncInterval)` with `autoSyncInterval = 30 * time.Minute` | Runs `h.syncInbox` for each opted-in user, which also fires their deterministic rules when autopilot is on | `users.lastAutoSyncAt`, `emails`, `sorting_rules.appliedCount`, `action_log` (`SourceRule`), Gmail labels |
| Rate-limiter bucket eviction | `middleware.go:215-228` | Bare literal `5 * time.Minute` (no named constant); drops buckets idle for more than `10 * time.Minute` | Prunes the in-memory token-bucket map so it cannot grow without bound | Nothing persistent, in-process map only |

Read carefully: **`autoSyncSweepInterval` (5 min) is the tick, `autoSyncInterval`
(30 min) is the minimum spacing between two syncs of the same user.** A tighter tick
only shortens the lag, it never causes an extra sync, because `schedule.Due` is the
real gate.

## Bootstrap sequence (main.go, in order)

| # | Line | Step | Failure mode |
|---|---|---|---|
| 1 | 27 | `config.Load()` | none |
| 2 | 31 | `cfg.Validate()` | `log.Fatalf`, refuses to boot on a placeholder `ENCRYPTION_KEY` |
| 3 | 36 | `database.NewDatabase(cfg.MongoDBURI)`, `defer db.Close()` | `log.Fatalf` |
| 4 | 45-51 | `db.EnsureIndexes` under a 15s context | best effort, logs a warning and continues |
| 5 | 54 | `crypto.NewEncryptor(cfg.EncryptionKey)` | none |
| 6 | 57-94 | Gmail service: env credentials win; otherwise a 5s read of the legacy `gmail_config` document, decrypted | logs and continues unconfigured |
| 7 | 97-104 | Mistral client when `MISTRAL_API_KEY` is set, plus `SetMaxRetries` | logs "AI features disabled" |
| 8 | 107-117 | `BillingConfig`, Stripe client when `STRIPE_SECRET_KEY` is set | logs "billing disabled" |
| 9 | 120 | `auth.NewManager(cfg.EncryptionKey)` | none |
| 10 | 124-126 | Package vars `api.Version`, `api.DefaultDigestHourUTC`, `api.AllowedOrigins` | must precede steps 11 and 12 |
| 11 | 129 | `api.NewHandler(...)` | **starts the four background loops here**, before any listener exists |
| 12 | 132 | `handler.SetupRoutes()` | builds the rate limiter, which starts the fifth goroutine |
| 13 | 137-144 | `http.Server` with explicit timeouts | none |
| 14 | 147-152 | `go srv.ListenAndServe()` | `log.Fatalf` on a non-`ErrServerClosed` error |
| 15 | 156-158 | `signal.Notify(stop, os.Interrupt, syscall.SIGTERM)` then block on `<-stop` | none |
| 16 | 161-166 | `srv.Shutdown` under a 20s context, then `db.Close()` via the deferred call | logs a failed drain and exits anyway |

Consequence of step 11: the loops can hit Mongo and Gmail before the process is
serving traffic, and they keep running even if `ListenAndServe` fails on a bound port
(until `log.Fatalf` kills the process).

## HTTP server timeouts

```
ReadHeaderTimeout:  10s  - slow-header defence
ReadTimeout:        30s  - whole request read
WriteTimeout:      150s  - long enough for synchronous AI analysis
IdleTimeout:       120s  - keep-alive reuse window
Shutdown grace:     20s  - main.go:161, HTTP connections only
```

## The analysis worker pool

Enqueue path, `EnqueueAnalyze` (`jobs.go:84-141`):

```
POST /api/ai/analyze-async
  -> aiClient nil? 503
  -> quotaExceeded(user)? 402
  -> truncate EmailIDs to analysisJobCap (500)
  -> InsertOne into analysis_jobs (status "queued")
  -> select { case h.jobQueue <- jobID: default: go h.processAnalysisJob(jobID) }
  -> 202 {jobId, status:"queued"}
```

Worker path, `processAnalysisJob` (`jobs.go:31-76`): parse the hex ObjectID, open a
**10 minute** context, load the job, set `status:"running"`, run
`h.runAnalysis(ctx, job.UserID, job.EmailIDs, onProgress)`, then write the terminal
document with `status:"done"` or `status:"error"` plus the error string.

The progress callback writes `total`, `processed`, `autoApplied`, `suggestionsCreated`
and `cachedHits` on **every processed email**, which is one Mongo `UpdateOne` per
email. `GET /api/ai/jobs/{id}` (`routes.go:74`) is what the SPA polls against those
counters, and it filters on `{_id, userId}` so a job id alone does not leak.

## Digest render and send

```
sendDueDigests (15 min tick)
  -> Find users {digestEnabled:true}          (3 minute context for the whole sweep)
  -> per user: hour = u.DigestHourUTC, fall back to defaultDigestHour() when <= 0 or > 23
  -> mailer.DueAt(u.DigestLastSentAt, now, hour) == false -> skip
  -> sendOneDigest: activitySummary -> Total == 0 -> return without sending
                    gmailClientFor -> digest.Render -> mailer.BuildRaw -> gmailService.SendMessage
  -> stampDigestSent unconditionally (users.digestLastSentAt = now)
```

`activitySummary` (`account.go:233-255`) reads `action_log` for the trailing 7 UTC days
with `SetLimit(20000)` and folds it through `activity.Summarize`. The same summary
powers `GET /api/stats/activity` and `GET /api/stats/digest`, so the preview endpoint
and the real email always show identical numbers.

`mailer.BuildRaw` produces a base64url multipart/alternative message with a **fixed**
boundary (`mailsorter-alt-boundary`) so the output is byte-for-byte deterministic. The
subject is MIME B-encoded so the French accents survive the header. The send needs
`gmail.GmailSendScope`, requested at `gmail.go:38` and `gmail.go:63`: a user who
connected before that scope existed fails here and only sees a log line.

## Concurrency and idempotency guarantees

| Question | Answer | Where |
|---|---|---|
| Can two workers grab the same job? | No. A buffered channel hands each value to exactly one receiver, and every `EnqueueAnalyze` inserts a fresh ObjectID, so a job id is never re-queued | `jobs.go:26`, `jobs.go:132` |
| Is there a DB-level claim on a job? | No. `status` goes `queued -> running` with a plain `$set`, no compare-and-set | `jobs.go:45` |
| Can a sweep overlap itself? | No. Each loop is a single goroutine, and Go's `Ticker` drops ticks instead of queueing them, so a slow sweep only delays the next one | `snooze.go:214`, `digest_scheduler.go:33`, `auto_sync.go:26` |
| What stops a digest going out twice? | `mailer.DueAt` returns false unless the current UTC hour has reached the target **and** `last` is before the start of today UTC, plus the `digestLastSentAt` stamp written after each attempt | `mailer.go:66-77`, `digest_scheduler.go:74` |
| Is that check atomic? | No. It is read-then-write. Two processes could both pass `DueAt` before either stamps | `digest_scheduler.go:70-74` |
| What stops an auto-sync retry storm? | The stamp is written **before** the attempt, so a failure costs one 30 minute cycle rather than every 5 minute tick | `auto_sync.go:63` |
| What stops a snooze wake retry storm? | `maxSnoozeWakeAttempts = 5`, then the row is parked as `status:"failed"` and drops out of the scheduled query | `snooze.go:31`, `snooze.go:268-273` |
| Is a snooze wake idempotent in Gmail? | Yes for the mutation (adding INBOX/UNREAD twice is a no-op), no for the ledger: a repeat produces a second `action_log` row | `snooze.go:203-209`, `snooze.go:280` |
| Does a background loop respect VIP protection? | Yes, transitively. Auto-sync goes through `syncInbox`, which loads `h.protectedValues` and passes it into `applyRuleToMessage` | `handlers.go:369-371` |
| Does a background loop burn AI quota? | Auto-sync does not: rules are deterministic and AI-free. The worker pool does: `runAnalysis` ends with `h.incrUsage(ctx, userEmail, p.Analyzed)` | `handlers.go:365-366`, `analysis.go:159` |

Per-sweep context budgets, all created with `context.WithTimeout(context.Background(), ...)`
so they are independent of any request:

```
wakeDueSnoozes:       2 minutes  (snooze.go:227)
sendDueDigests:       3 minutes  (digest_scheduler.go:48)
runDueAutoSyncs:      5 minutes  (auto_sync.go:41)
processAnalysisJob:  10 minutes  (jobs.go:37)
```

That budget covers **the whole sweep, every user in it**. When it expires mid-sweep,
the remaining Mongo calls fail, and `stampAutoSync`, `stampDigestSent`, `updateJob` and
`logAction` all discard their error, so the failure is completely silent.

## Shutdown behaviour

`main.go` traps `os.Interrupt` and `syscall.SIGTERM`, then calls `srv.Shutdown` with a
20 second budget. That is the entire shutdown story: no loop receives a cancel, no
`WaitGroup` is awaited, `jobQueue` is never closed, and the goroutines die when the
process exits.

| Killed mid-flight | Result |
|---|---|
| Analysis job | The `analysis_jobs` document stays `status:"running"` forever. Nothing requeues it at boot, so the SPA polls a job that will never finish |
| Snooze wake | Gmail may already be modified while the row is still `scheduled`. The next boot retries it: harmless in Gmail, but a duplicate `action_log` row |
| Digest | If the process dies between `SendMessage` and `stampDigestSent`, the user gets a second digest after restart |
| Auto-sync | Safe by construction: the stamp is written first, so a partial sync just waits for the next 30 minute window |

Every redeploy resets all five tickers to zero. Nothing fires at t=0: the first snooze
sweep is 1 minute after boot, the first auto-sync sweep 5 minutes, the first digest
check 15 minutes.

## Tuning constants (current values)

```
snoozeSweepInterval:      1m       (snooze.go:26)  wake-sweep tick
maxSnoozeWakeAttempts:    5        (snooze.go:31)  then status becomes "failed"
snoozeLabelName:          "Mailsorter/Reporté"     (snooze.go:22)
snooze sweep query limit: 200      (snooze.go:232) unsorted, natural order
digestSweepInterval:      15m      (digest_scheduler.go:29) due-check tick
DefaultDigestHourUTC:     7        (digest_scheduler.go:17) overridden by DIGEST_HOUR_UTC
autoSyncSweepInterval:    5m       (auto_sync.go:21) THE TICK
autoSyncInterval:         30m      (auto_sync.go:16) THE PER-USER MINIMUM
analysisJobCap:           500      (jobs.go:17)    emails per async job
analysisBatchSize:        8        (analysis.go:19) emails per Mistral call
jobQueue capacity:        256      (handlers.go:80)
analysis worker count:    3        (handlers.go:85)
syncInbox page size:      100      (handlers.go:360) "in:inbox"
activitySummary limit:    20000    (account.go:239) ledger rows over 7 days
rate limiter:             20 r/s, burst 40 (routes.go:103), bucket TTL 10m
```

## Environment variables that reach a loop

| Var | Read where | Effect on background work |
|---|---|---|
| `DIGEST_HOUR_UTC` | `main.go:125` into `api.DefaultDigestHourUTC` | Default send hour (0-23) for users who have not chosen one. Default 7 |
| `MISTRAL_API_KEY` | `main.go:98` | Empty means `h.aiClient` is nil: `EnqueueAnalyze` answers 503, so the worker pool has nothing to drain |
| `MISTRAL_MAX_RETRIES` | `main.go:100` | Extra retries inside `runAnalysis`, which directly widens a job's wall-clock time against the 10 minute worker context |
| `GMAIL_CLIENT_ID` / `GMAIL_CLIENT_SECRET` / `GMAIL_REDIRECT_URL` | `main.go:62-67` | Unconfigured means every `gmailClientFor` in a loop fails and the digest and snooze sweeps do nothing but log |
| `MONGODB_URI` | `main.go:36` | Every loop is Mongo-first: an unreachable Mongo makes each sweep a logged no-op |

There is **no** env var for any interval, the pool size or the queue capacity. Every
cadence is a compile-time constant.

## Testing

The loops themselves have no test. What is tested is the pure decision layer they call:

| Test file | Covers |
|---|---|
| `internal/schedule/schedule_test.go` | `TestDue`, `TestDueNonPositiveInterval`: the auto-sync gate, including zero-`last` and inclusive boundary |
| `internal/mailer/mailer_test.go` | `TestDueAt` (the once-per-day digest guarantee), `TestBuildRawStructureAndDecoding` |
| `internal/digest/digest_test.go` | `TestRenderSubjectLeadsWithToday`, `TestRenderEmptyFallbackSubject`, `TestRenderBodiesContainBreakdowns`, `TestSingularPluralization`, `TestBySourceOrderingIsDeterministic` |
| `internal/activity/activity_test.go` | `TestSummarizeBucketsAndWindow`, `TestSummarizeEmpty`, `TestInverse` |

No `*_test.go` in `internal/api` calls `NewHandler`, so the test suite never starts a
background goroutine. Adding a test that does would start real 1 minute and 5 minute
tickers for the lifetime of the test binary.

To exercise a loop for real without waiting: temporarily shorten the constant, rebuild,
and watch the log prefixes `autosync:`, `digest:`, `snooze:` and `analysis job`. The
observable side effects are `users.lastAutoSyncAt`, `users.digestLastSentAt`,
`snoozes.status` and `analysis_jobs.status`.

## Known pitfalls

1. **`autoSyncSweepInterval` is not `autoSyncInterval`.** 5 minutes is the tick,
   30 minutes is the per-user minimum enforced by `schedule.Due`. Quoting one as the
   other is the mistake this document exists to prevent.

2. **A digest hour of 0 (midnight UTC) is silently unreachable.** `UpdateSettings`
   accepts it (`hour < 0 || hour > 23`, so 0 passes and is persisted), but both
   `sendDueDigests` (`digest_scheduler.go:67`) and `userSettings` (`account.go:129`)
   reject it with `hour <= 0` and substitute `defaultDigestHour()`. `mailer.normalizeHour`
   would have accepted 0 perfectly well. The user picks midnight and gets 07:00.

3. **Neither sweep query is indexed.** `EnsureIndexes` (`database.go:117-139`) creates
   no index on `users.autoSyncEnabled` or `users.digestEnabled`, so both sweeps do a
   full `users` collection scan, every 5 and every 15 minutes forever. The snooze sweep
   is fine: `{status, wakeAt}` is indexed at `database.go:133`.

4. **Nothing runs at boot.** Tickers only fire after a full interval. A deployment loop
   faster than 15 minutes would mean the digest never goes out at all.

5. **The loops are not shut down.** `srv.Shutdown` drains HTTP and nothing else. A job
   interrupted mid-run stays `status:"running"` in Mongo with no recovery path.

6. **Quota is checked at enqueue, charged at completion.** `EnqueueAnalyze` calls
   `quotaExceeded` before inserting the job, but `processAnalysisJob` never re-checks,
   and `runAnalysis` calls `incrUsage(p.Analyzed)` only at the end. A 500 email job
   accepted at 199 of `FreeMonthlyLimit = 200` overshoots the limit by design.

7. **A saturated queue bypasses the pool cap.** The `default:` branch at `jobs.go:134`
   spawns `go h.processAnalysisJob(jobID)` directly. Past 256 queued jobs the
   "3 workers" bound stops being a bound and concurrency becomes unlimited.

8. **The digest stamps even when the send failed.** `sendOneDigest` returns early on a
   summary error, a missing Gmail client or a failed `SendMessage`, and
   `stampDigestSent` runs anyway (`digest_scheduler.go:73-74`). A user missing the
   `gmail.send` scope loses every digest, one per day, visible only as a log line.

9. **A background loop cannot ask the user to re-authenticate.** `gmailClientFor` can
   return `errReauthRequired` (`ai_handlers.go:789`), which a handler maps to a 401 the
   SPA acts on. In a sweep it becomes `log.Printf` and nothing else.

10. **Every replica runs every loop.** There is no leader election and no lease. The
    current Dokploy Compose deployment is single-instance, so this is latent rather than
    live, but scaling the backend to two containers would double every digest, every
    auto-sync and every snooze wake.

11. **Sweep-wide contexts hide late failures.** All the writes a sweep performs share
    one 2, 3 or 5 minute context, and `stampAutoSync`, `stampDigestSent`, `updateJob`
    and `logAction` all discard the `UpdateOne` error. Past the deadline the sweep keeps
    iterating and writes nothing, with no log line.

12. **The snooze sweep takes 200 rows with no sort** (`snooze.go:232`). Beyond 200
    simultaneously-due snoozes the selection is natural order, so a specific message can
    be deferred across several 1 minute sweeps.

13. **`main.go:159` contains a single-character ellipsis in a log string.** That
    violates the repo-wide ASCII-punctuation rule in `CLAUDE.md`. If you touch that
    line, replace it with three periods rather than reproducing it.

## Key Files

| File | Description |
|---|---|
| `backend/cmd/server/main.go` | Bootstrap order, server timeouts, signal handling, 20s drain |
| `backend/internal/api/handlers.go` | `Handler` struct, `NewHandler` (starts four loops), `syncInbox` |
| `backend/internal/api/jobs.go` | Job queue, 3-worker pool, `EnqueueAnalyze`, `GetJob` |
| `backend/internal/api/snooze.go` | Snooze CRUD, `startSnoozeLoop`, `wakeDueSnoozes`, retry cap |
| `backend/internal/api/digest_scheduler.go` | `startDigestLoop`, `sendDueDigests`, `sendOneDigest`, stamping |
| `backend/internal/api/auto_sync.go` | `startAutoSyncLoop`, `runDueAutoSyncs`, `stampAutoSync` |
| `backend/internal/api/ledger.go` | `logAction` and the `Source*` vocabulary a loop must tag with |
| `backend/internal/api/account.go` | `activitySummary`, `userSettings`, `UpdateSettings`, `quotaExceeded` |
| `backend/internal/api/analysis.go` | `runAnalysis`: what a worker actually executes |
| `backend/internal/api/middleware.go` | The fifth goroutine: rate-limiter bucket eviction |
| `backend/internal/schedule/schedule.go` | `Due`: the pure auto-sync gate |
| `backend/internal/mailer/mailer.go` | `DueAt` (once-per-day) and `BuildRaw` (RFC 2822 multipart) |
| `backend/internal/digest/digest.go` | `Render`: subject, text and HTML bodies |
| `backend/internal/activity/activity.go` | `Summarize`: ledger rows to the 7-day recap |
| `backend/internal/database/database.go` | `EnsureIndexes`, the index list the sweeps rely on (or lack) |
| `.env.example` | `DIGEST_HOUR_UTC`, `MISTRAL_MAX_RETRIES` and the rest of the contract |

## References

### Source Files
- `backend/cmd/server/main.go` - the only place with a shutdown path
- `backend/internal/api/handlers.go:72-93` - `NewHandler`, where all four loops begin
- `backend/internal/api/jobs.go:17-80` - job cap, pool, worker body
- `backend/internal/api/snooze.go:212-282` - the wake sweep and its retry cap
- `backend/internal/api/digest_scheduler.go:15-116` - default hour, sweep, send, stamp
- `backend/internal/api/auto_sync.go:13-83` - the two intervals and the sweep
- `backend/internal/mailer/mailer.go:61-77` - `DueAt`, the idempotency guarantee
- `backend/internal/schedule/schedule.go:15-20` - `Due`, the auto-sync gate
- `backend/internal/database/database.go:116-145` - `EnsureIndexes`

### Related Context Docs
- [gmail-sync.md](gmail-sync.md) - `syncInbox`, token refresh and the Gmail retry policy the auto-sync loop drives
- [ai-triage.md](ai-triage.md) - `runAnalysis`, the cache, batching and quota the worker pool executes
- [data-model.md](data-model.md) - the `users`, `snoozes`, `analysis_jobs` and `action_log` documents these loops write
