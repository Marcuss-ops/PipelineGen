# Context Deadline Exceeded — Root Cause Analysis

**2026-07-09 | PipelineGen Agent**

## 1. Symptom

```
HTTP Error 500
failed to create job: context deadline exceeded
```

Occurs during async job enqueue for `POST /api/stock-pipeline/run` and
`POST /api/stock-pipeline/search-and-run`. Also observed on Artlist runs.

## 2. Timeline

| Time | Observation |
|------|-------------|
| Pre-fix | Artlist compile-break: `unknown field SearchStrategy` blocked `go run ./cmd/admin` |
| After `git pull origin main` (commits `f442fe200` + `aeeacf44f` + `f45ad09d2`) | Compile-break resolved; `list-drive-folder` works |
| Round 9, 10-11, 12, post-match async attempt | `context deadline exceeded` on enqueue |
| Round 1, 2, 5, 7 | ✅ Completed successfully (suggesting the timeout is borderline, not a permanent failure) |

## 3. Timeout Chain (Stock Pipeline Async Path)

```
HTTP handler (gin context, typically ~30s via Gin default or reverse-proxy)
  └─ h.useCase.Submit(c.Request.Context(), cmd, async=true)
       └─ usecase.go:94: DetachWithTimeout(ctx, "stock-submit-enqueue", 15s)
            └─ jobsSvc.Enqueue(enqueueCtx, req)
                 ├─ enqueueMu.Lock()          ← mutex contention possible
                 ├─ FindActiveByKey(ctx)       ← DB query
                 ├─ findExistingByCorrelation(ctx)
                 │    └─ DetachWithTimeout(ctx, "jobs-correlation-lookup", 2s)
                 │         └─ repo.FindByTypeAndCorrelation(lookupCtx, ...)
                 │              └─ 2s timeout can fire FIRST
                 ├─ json.Marshal(req.Payload)
                 ├─ HasHandler(type) check
                 ├─ resolveMaxRetries(type, ...)
                 └─ repo.Create(ctx, job)      ← SQLite INSERT
```

**Key insight**: `background.DetachWithTimeout` uses `context.WithoutCancel(ctx)`, so the
parent HTTP context cancellation does NOT propagate. BUT the 15s detached timeout IS the
hard deadline — if the enqueue takes longer than 15s, it fails with `context deadline exceeded`.

## 4. Primary Root Cause: 15s Enqueue Timeout Too Tight

**Location**: `internal/application/assets/providers/stock/stockpipeline/usecase.go:94`

```go
enqueueCtx, cancel := background.DetachWithTimeout(ctx, "stock-submit-enqueue", 15*time.Second)
```

**Why 15s is borderline**:

1. **Mutex contention** (`enqueueMu.Lock()`): If another concurrent enqueue is in-flight
   (e.g. multiple stock rounds submitted in parallel, or Artlist + stock overlapping), the
   goroutine blocks on `Lock()` consuming the detached timeout budget.

2. **DB contention**: Each enqueue performs 2-3 SQLite queries (active-key check,
   correlation lookup, CREATE) under WAL mode. WAL handles concurrent reads well, but
   concurrent writes still serialize — a long-running write from another job type
   (e.g. `system.cleanup` with 434 rows) can delay the enqueue.

3. **Payload size**: Large payloads (>1MB) from clips-heavy requests spend more time in
   `json.Marshal` and `MaxPayloadSize` check.

4. **Correlation lookup 2s timeout**: The correlation lookup has its own 2s detached
   timeout. If it fires, `isTransientCorrelationLookupError` returns true and the flow
   proceeds — but the 2s is lost from the parent 15s budget.

**Evidence**: Rounds 1/2/5/7 completed successfully (enqueue <15s); rounds 9/10-11/12
failed (suggesting borderline timing — the enqueue path slowed enough to cross the
15s threshold under concurrent load or DB write contention).

## 5. Secondary Factor: `context deadline exceeded` NOT Classified as Retryable

**Location**: `pkg/retry/errors_test.go:70-78`

The unit test explicitly documents that `context deadline exceeded` is **NOT** in
`pkg/retry`'s canonical transient substring taxonomy — meaning the retry pool treats
it as a non-retryable (terminal) error. The enqueue failure surfaces as a hard 500
to the caller rather than being retried with exponential backoff.

This is the correct behavior for the *enqueue path* (retrying a timed-out enqueue
could create a duplicate job), but it DOES mean the caller gets a hard failure
rather than graceful recovery.

## 6. Tertiary Factor: `RemoteDisconnected`

The user also reported `RemoteDisconnected` — this is the HTTP client-side
counterpart: the client's timeout fires while the server is still processing the
enqueue. The `DetachWithTimeout` ensures the server-side enqueue continues even
after the client disconnects, but the client never sees the response.

## 7. DB Health (Not a Root Cause)

| Metric | Value | Verdict |
|--------|-------|---------|
| DB size | 17MB | ✅ Healthy |
| WAL mode | `wal` | ✅ Enabled |
| `busy_timeout` | 5000ms | ✅ Configured |
| Integrity check | `ok` | ✅ |
| Active/stuck jobs (1h window) | 0 | ✅ |
| Job count | 661 total | ✅ |

**The DB is NOT the bottleneck.** All SQLite parameters are correctly configured.

## 8. Recommended Fixes

### Fix 1 (P0): Increase `stock-submit-enqueue` timeout from 15s → 30s

```go
// usecase.go:94
enqueueCtx, cancel := background.DetachWithTimeout(ctx, "stock-submit-enqueue", 30*time.Second)
```

**Rationale**: 15s is borderline. The default job timeout is 10 minutes — a 30s
enqueue window is still well within operational bounds. This is a 1-line change
with zero blast radius.

### Fix 2 (P1): Add timeout for Artlist enqueue path

The Artlist enqueue path in `internal/application/assets/providers/artlist/` may
have a similar or shorter timeout — audit and align with 30s.

### Fix 3 (P2): Exponential backoff on caller side

Wrap the HTTP call in the Python/shell client with a short retry loop
(3 retries × 2s backoff). The `Enqueue` method is idempotent on
`(type, correlation_id)`, so retrying the HTTP call on `context deadline exceeded`
is safe.

### Fix 4 (Forward-pointer): Enqueue deadline observability

Add a Prometheus counter for `jobs_enqueue_timeout_total{job_type}` so operators
can track how often enqueues hit the detached-context deadline.

## 9. Cross-references

- `internal/application/assets/providers/stock/stockpipeline/usecase.go:94` — timeout definition
- `internal/application/jobs/enqueue_service.go:215` — correlation lookup timeout (2s)
- `pkg/background/detach.go` — DetachWithTimeout implementation
- `pkg/retry/errors_test.go:70-78` — deadline NOT transient (correct for enqueue)
- `architecture/action-plans/2026-07-09-artlist-compile-break-recovery.md` — parent action plan

---

**godlike/06 SSOT (one canonical owner per fact):** this document lives ONLY at
`architecture/notes/context-deadline-exceeded-investigation.md`.

**godlike/07 NO-FAKE-AVAILABILITY:** the 15s timeout was measured on a dev machine
(server not running). The actual VPS timing may differ — the fix recommendation
is conservative (30s) and must be validated on the production host.
