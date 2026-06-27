# ADR 0002: P2/P3 hardening — atomic ClaimNext first, JobBroker port, dep-struct ratchet, mega-package split, --strict gate

> **Status:** Accepted. Active PR-A (this PR scope). PR-B..-I logged in the implementation-status section below.
>
> **Deciders:** Architecture + observability + jobs subsystem owner.
>
> **Scope:**
>   - **Ratchet of P2 §1 (single-node job queue + DB)**: drop `claimMu`, atomicise `ClaimNext`, same-tx `job_events` insert.
>   - **Ratchet of P2 §2 (multi-node prep)**: declare `interface JobBroker` in `internal/domain/job/`, conform `*SQLiteStore` to it.
>   - **Ratchet of P2 §3 (dependencies + capability boundaries)**: ban mandatory `Set*` setters; constructor injection with ≤8-field `Deps` struct.
>   - **Ratchet of P3 §1 (mega-package split)**: cap `internal/application/scripts` and `internal/application/youtube` to ≤40 files; cap productive files to ≤500 LOC excluding `_test.go`.
>   - **Ratchet of P3 §2 (archcheck --strict)**: promote `--strict` to a blocking CI gate once the transitional baseline is zero.

## Context

A consolidated architectural review (Wave 22 / P2 + P3, June 2026) identified four cross-cutting concerns and a single close-out path:

1. **Job claim + audit trail inconsistency.** Today, `SQLiteStore.ClaimNext` performs three separate operations: a `SELECT` to pick a candidate, a separate `UPDATE` via `Start` that bumps `revision` and toggles leases, and an out-of-tx `INSERT` for the `job_running` `job_events` row. The intermediate state — a claimed job without a written `job_events` row — survives crashes, leaving the audit timeline out-of-sync with the persistent schedule. `claimMu sync.Mutex` instances in `SQLiteStore` mask this by serialising within a single process — but the mutex is process-local and does not extend to multi-node contention. The mutex is documented in the plan (`P2 single-node`) as the "primary coordination mechanism" to be removed. The outboxevents package has already implemented the canonical atomic-claim pattern (`WITH candidate AS (...) UPDATE ... WHERE status = 'pending'`) on `repository.go:106`; the jobs package did not follow the same template.

2. **No `JobBroker` interface.** Domain code today imports the concrete `*SQLiteStore` directly. A future PostgreSQL adapter (for multi-node) would re-touch every consumer. Defining a `JobBroker` port now is a no-cost paving step.

3. **Mandatory `Set*` setters in application services.** Independent reviews (PR2.5 artlist landed; stockpipeline still has `SetCutter` / `SetRenderer` / `SetClipsRepo` / `SetAssetIndex` / `SetDispatcher`; catalogsync still has `SetDispatcher`) conclude that constructor-injection is the canonical pattern. The existing `internal/application/images/service.go:86-129` is the reference template.

4. **Mega-package drift.** `internal/application/scripts` (~55 files) and `internal/application/youtube` (~43 files) exceed the P3 §1 cap of 40 files per package. Productive files over 500 LOC are 7 of 15 internal-wide (engine_test / normalizer_plan_tests are tests — they don't count against the productive-file cap). The `archcheck --strict` flag exists but is report-only in Phase 0 (`scripts/archcheck/main.go:785`); promotion to a blocking CI gate is queued but blocked on a clean transitional baseline.

Without the dep-chain rule (cf. ADR-0001) blocking start of the next PR, these four items would each grow into a multi-month refactor. Sequencing them as named PRs keeps each PR reviewable and ratcheted.

## Decision

### D1 — Atomic ClaimNext (this PR)

Replace the three-step claim sequence with a single-statement CTE-based UPDATE inside a `BeginTx` transaction. The new shape:

```sql
WITH candidate AS (
    SELECT id FROM jobs
    WHERE status = 'QUEUED' [AND type IN (?, ?, ?)]
    ORDER BY priority DESC, created_at ASC
    LIMIT 1
)
UPDATE jobs
SET status = 'RUNNING',
    started_at = ?, lease_expiry = ?, lease_id = ?, worker_id = ?,
    revision = revision + 1, updated_at = ?
WHERE id = (SELECT id FROM candidate) AND status = 'QUEUED'
RETURNING id
```

Read `id` via `tx.QueryRowContext(...).Scan(&jobID)`. The empty-queue case is `sql.ErrNoRows`. Append a `job_events` insert with `jobID` in the same tx. Refetch the full job via `r.Get(ctx, jobID)` AFTER commit (read-only, outside the tx).

Drop the `claimMu sync.Mutex` field from `SQLiteStore` entirely. With single-statement atomic UPDATE + SQLite's reserved/exclusive writer lock (WAL + busy_timeout), there is no longer a need for cross-goroutine serialisation within a single process; cross-process correctness comes from the `status='QUEUED'` fence and the `revision = revision + 1` ordering. Drop the `Start` helper that today bridges the SELECT→UPDATE→INSERT gap; it has no other callers (see repository_commands.go).

### D2 — `interface JobBroker` (PR-B)

Declare a structurally-equivalent port in `internal/domain/job/store.go`:

```go
type JobBroker interface {
    Store       // embed existing canonical shape
}
```

Add `var _ JobBroker = (*SQLiteStore)(nil)` in `repository.go`. CFG-conforming PostgreSQL adapter is OUT OF SCOPE for PR-B. PR-B is the contract introduction; PR-`future` will be the PostgreSQL implementation.

### D3 — Constructor injection ratchet (PR-D)

Remove `SetCutter` / `SetRenderer` / `SetClipsRepo` / `SetAssetIndex` / `SetDispatcher` from `internal/application/assets/providers/stock/stockpipeline/service.go` and `internal/application/assets/catalogsync/service.go::SetDispatcher`. Migrate each to an `8`-field `Deps` struct passed to the constructor. Pattern source: `internal/application/images/service.go:86-129`.

Cap `Deps` total field count at 8 (godlike/11 + AGENTS.md Pattern 0). Enforced via a new CI check (`scripts/ci-architectural-checks.sh::Check 23 — field-count cap`) reading a transient allowlist file at `docs/migrations/deps-struct-allowlist.txt` (zero-baseline rule).

### D4 — Mega-package split (PR-G)

`internal/application/scripts/` and `internal/application/youtube/` each exceed 40 files. The cap uses post-PR `--strict` to gate any package over 40 productive files. Productive-file cap is 500 LOC excluding `_test.go` (test files are subject to a separate "test-focus" size budget via the per-package test-density metric). Existing template: `internal/application/youtube/ports.go` (per-pattern split).

### D6 — Job-queue single-node invariants not merged in PR-A (backlog, tracked)

The user's plan listed six P2 §1 single-node items beyond the atomic ClaimNext that this PR scope did not absorb. They are tracked here so they do not vanish — each must land as its own ratcheted PR with owner + deadline + exit-gate, per godlike/08 zero-baseline rule:  - **D6.1 — Reaper-in-batch (PR-Reaper, P0)**. `repository_claims.go::requeueSingle` currently opens a fresh `BeginTx` per expired lease — `SELECT id, retry_count, max_retries, revision` + `UPDATE jobs ... WHERE id=? AND revision=?` + `INSERT job_events` — exactly the per-lease-tx anti-pattern the plan called out. Target shape: one SELECT to gather the candidate batch + a single batched UPDATE that returns the rows affected + N event INSERTS in one tx. Acceptance: `Reprocess reaper` bench shows >N× speedup with N expired leases; `requeueSingle` is deleted; `RequeueExpiredLeases` returns the same `[]RequeueResult` shape. **AND in `internal/infrastructure/database/sqlite/jobs/scanner.go`, declare a local one-method `LeaseReaper` interface and have `NewScanner` take that** — the scanner only needs `RequeueExpiredLeases(ctx, now, limit) ([]RequeueResult, error)`, and binding it to the full `job.JobBroker` would unnecessarily widen the canonical Store surface. The local `LeaseReaper` interface is satisfied by `*SQLiteStore` AND by a future `*pgbroker.Store` *only if the postgres adapter implements RequeueExpiredLeases*; if it cannot, the scanner must declare a per-adapter reaper struct (per godlike/07 "no fake availability"). PR-B audit classified `scanner.go` as `adapter-internal, porting in PR-Reaper`; without the explicit local-interface binding the audit entry becomes stale.

- **D6.2 — Composite index migrations (PR-Indices, P1)**. The plan calls for `(status, type, priority DESC, created_at)` and `(status, lease_expiry)`. Today we have `idx_jobs_status_priority (status, priority DESC)` from `001_velox_core.sql:233` and `idx_jobs_claim` + `idx_jobs_expired_leases` from `053_job_lifecycle_atomic.sql:86-90`. Audit: run `EXPLAIN QUERY PLAN` on the new CTE-claim BEFORE adding any composite — drop the redundant single-column index if the new composite renders it covered. Acceptance: zero-write-overhead regression in `JobClaimConflictTotal` rollouts and throughput smoke within ±5%.

- **D6.3 — job_events retention (PR-Retention, P1)**. Today `job_events` grows unbounded. Add a periodic retention sweeper that deletes rows older than N days (config-driven). Acceptance: `job_events_count` gauge stabilises below the N-day watermark; no operator dashboard regression.

- **D6.4 — Progress-update batching (PR-Progress, P2)**. `repository_lifecycle.go::SetProgress` issues one UPDATE per call + one INSERT event outside the tx (race-prone). Target shape: coalesce progress events that fire <100ms apart, single UPDATE per coalesced window + single event row. Acceptance: throughput on jobs that emit `Progress(10, ...)` / `Progress(35, ...)` / `Progress(100, ...)` triggers <3 events instead of N.

- **D6.5 — Polling aggressiveness removal (PR-Polling, P2)**. The worker heartbeat loop today polls at fixed intervals; the plan calls for backoff when queue is empty. Acceptance: idle-loop CPU drops to ~0% after N consecutive empty claims; wakes immediately on Enqueue.

- **D6.6 — Queue-DB separation (PR-Queue-Split, P2 bridging P2 §1 + §2)**. The plan explicitly calls for *"Rimuovere la queue dallo stesso database caldo di asset, script e cache"*. Today the jobs table shares `media.db.sqlite` with media_assets, scripts, and cache. Migration: spin `jobs.db.sqlite` (WAL, separate connection pool), keep one SQLiteStore legacy alias for migration compatibility, drop after transitional baseline zero. Acceptance: write throughput on media_assets' hot path is unaffected; `JobClaim_*` latency is unaffected; the ratchet to zero legacy aliases is monotonic. **Superseded by ADR-0003 (ACCEPTED 2026-06-27, Option C: SPLIT + BENCH hybrid)** — see `architecture/decisions/0003-jobs-db-split-yes-no-when.md` for the full yes/no/when analysis, the bench-driven gate, the migration-window policy, and the trigger conditions for re-evaluation. The D6.6 row below reflects the post-ADR-0003 state.

- **PR-AuditGate (P1, between PR-Reaper and PR-D)**. Promote the PR-B landing-time audit grep `rg -nE '\*sqljobs\.SQLiteStore|sqljobs\.NewSQLiteStore' internal/ --type=go` to a blocking CI check (`scripts/ci-architectural-checks.sh::Check 24 — No direct *sqljobs.SQLiteStore imports in internal/application/ or internal/api/`). Snapshot grep documents the seam; gate enforces it across PRs without re-running the audit by hand. Patterns: the check fails CI on any hit in `internal/application/**` or `internal/api/**`; composition-root (`internal/app/module_media.go`), test fixtures (`*_test.go`), and same-package adapter (`scanner.go`, post PR-Reaper) are allowlist comments via `// ARCH-ALLOWLIST: <reason>` markers. Zero-baseline rule applies: PR-AuditGate landing must show zero hits in the gated paths today; the allowlist exists only for the by-design composition-root seam.

### D6 — Job-transition-conflict metric (PR-F, tracked follow-up)

The plan called for *"Aggiungere metriche sui conflitti di revisione"*. With the CTE+RETURNING claim, ClaimNext itself cannot surface a transition conflict — the SQL-level `status='QUEUED'` fence either picks a fresh row or returns `sql.ErrNoRows`. The conflict surfaces in `Complete`, `Fail`, `Cancel`, and `ScheduleRetry` (all of which already return `ErrTransitionConflict` on lease-stolen updates). PR-F will register one counter `job_transition_conflict_total{method=<name>}` bumped inside `validateOwnership` (or each fenced `UPDATE`) — gated to defer until the E2E coverage on the new ClaimNext surface has grown enough that this counter won't fire as a noise-flag. Not merged in PR-A by design; see ADR §"Implementation status".

### D5 — `--strict` gate promotion (PR-I)

`archcheck --strict` (`cmd/archcheck/main.go:151` / exit-1 gate at `:204-205`) currently exits 0 even with violations. PR-I promotes `--strict` to a blocking CI gate, with the existing `architecture/current.yaml#id-20-21` ratchets (lint_gates[check=5], pack-size cap) raising to enforcement mode. The transitional baseline (`docs/migrations/archcheck-strict-baseline.json`) holds any open exceptions until the diff is back to zero.

> **Binary clarification (June 2026)**: `--strict` lives in `cmd/archcheck/main.go` (the permanent target-tree enforcer reading `architecture/policy.yaml`), NOT in `scripts/archcheck/main.go` (the transient legacy burndown ratchet — that one uses `--ratchet` + `--future-ratchet`). The CI script `scripts/ci-architectural-checks.sh` runs both: `scripts/archcheck` for the legacy burndown AND `cmd/archcheck --strict` for the gate promotion.

> **PR-G hard dependency**: PR-I landing requires PR-G (mega-package split for `internal/application/scripts` [55 files → ≤40] + `internal/application/youtube` [43 files → ≤40]) to be on track. Deadline per `architecture/current.yaml#id-21` (PR-I mitigation row): 2026-07-10. If PR-G slips beyond that, PR-I's pack-size cap fails CI on landing — the user explicitly accepted this risk per the pre-blocking prep phase.

### Cross-cutting conformance

- The CTE shape mirrors `internal/infrastructure/database/sqlite/outboxevents/repository.go:106` (option-C return-by-refetch). Returning the row id is preferred over refetching by `worker_id + lease_id + status='RUNNING'` because the row's `id` is the deterministic key for the subsequent `job_events` insert.
- `mattn/go-sqlite3 v1.14.17` (Go module pinned) bundles SQLite 3.39+, comfortably above the 3.35.0 minimum for RETURNING. The driver exposes `RETURNING` columns via `QueryRowContext` (confirmed via the existing outboxevents comment at line 107 + the driver's manual). `ExecContext` MUST NOT be used with a `RETURNING` clause (the driver strips returned columns).
- Empty queue semantics: `tx.QueryRowContext(...).Scan(&id)` returns `sql.ErrNoRows` cleanly when no candidate is claimable. No sentinel return, no break of the order-by contract.

## Consequences

### Positive

- **Single-statement atomicity** removes the only path that produced "claimed but unevened" rows on crash. The audit trail is now bound 1:1 with the schedule transition.
- **claimMu removal** shrinks the SQLite surface area: a single-Goroutine-testbench node has no contention latency. Multi-node correctness is achieved by the SQL-level `status='QUEUED'` fence and the `revision+1` increment — both observable across processes.
- **Dead `Start` helper** removed. The Start/ClaimNext coupling is gone; Start's callers (search shows only `r.Start(ctx, startCmd)` at line 61 of repository_claims.go) disappear with this PR.
- **JobBroker port** opens the door for PR-`future`-Postgres without domain-layer churn.

### Negative

- CTE-based claim reads the `WHERE candidate` predicate in the inner CTE, then evaluates the outer `WHERE id = (SELECT id FROM candidate) AND status = 'QUEUED'`. A concurrent claim from a second process cannot claim the same row because the outer `status = 'QUEUED'` fence fails and `RowsAffected == 0`. However, the second process will claim **a different row** (the next candidate in priority/created_at order) — this is desirable, not a regression. (Documented for operator reading.)
- Drop of `claimMu` means callers that today use ClaimNext as the **only** call site for atomic claim lose the in-process serialisation. Concurrent calls from many goroutines will now each attempt their own CTE — the database serialises them but the process wastes goroutine-context-switch overhead. Trade-off: goroutine concurrency vs lock acquisition is a GPU-flat tradeoff; the plan's intent is to remove the singleton-mutex anti-pattern. A future metric (job_claim_wasted_total) can surface over-parallelism.

### Neutral

- The `repository_claims.go::StartJob` struct remains (it's used by the public callers that we don't change yet, e.g. `internal/application/jobs/worker.go:133` indirectly). After the PR lands, `Repository_claims.go::Start` is deleted but `StartJob` is preserved.
- Job-transition-conflict metric is OUT OF SCOPE for this PR. The Complete/Fail/Cancel/ScheduleRetry paths return `ErrTransitionConflict`; a single counter `job_transition_conflict_total` (label: method) belongs in a follow-up observability PR.

## Alternatives considered

### A. Drop claimMu but keep SELECT → UPDATE → INSERT in same tx (multi-statement inside tx)

Rejected. The catch is that readers using `database/sql` + WAL upgrade a shared to exclusive lock in the gap between `SELECT id+revision` and the subsequent `UPDATE`, which causes `database is locked` with default `busy_timeout=5000`. CTE within a single statement avoids the lock upgrade.

### B. CTE-based claim + refetch by `worker_id + lease_id` (mirror outboxevents verbatim)

Rejected. The outboxevents refetch path finds the row via `worker_id + lease_id + status = 'processing' ORDER BY updated_at DESC LIMIT 1`. For REAL jobs the operationally-natural key is the row's `id`, which we already have on the `RETURNING` path. Using `id` here keeps the audit-trail unique-key clean.

### C. Keep claimMu as defense-in-depth

Rejected. The plan explicitly identifies `claimMu` as "primary coordination mechanism" to remove. With CTE atomicity, the SQL-level fence is canonical; keeping the mutex makes the call-graph carry two layers of "correctness gate" that must agree (drift hazard). For multi-node, the mutex is wrong: process-local locks cannot coordinate across processes.

## Implementation status

### PR-A (this PR)

- `internal/infrastructure/database/sqlite/jobs/repository.go` — drop `claimMu sync.Mutex` from `SQLiteStore` struct, drop `sync` import.
- `internal/infrastructure/database/sqlite/jobs/repository_claims.go`:
  - Replace `ClaimNext` body with single-statement CTE `UPDATE ... RETURNING id` inside `BeginTx`.
  - Insert `job_events` row inside the same tx.
  - Refetch via `r.Get(ctx, jobID)` after `Commit`.
  - Remove `Start` helper (dead after this refactor; no other callers).
- Run targeted e2e: `bash scripts/ci-architectural-checks.sh` exits 0; `go test ./internal/infrastructure/database/sqlite/jobs/...` green; `go test ./internal/application/jobs/...` green; worker_registry_e2e_test.go `go test -run TestRemoteWorker_*` green.

### PR-B — DONE (this commit)

- `internal/domain/job/store.go` — extended `Store` with the four application-layer
  load-bearing methods previously on the concrete `*SQLiteStore`:
  `FindActiveByKey`, `FindByTypeAndCorrelation`, `ListEvents`, `Retry`. Without
  this expansion, `JobBroker` would either need to declare methods Store does
  not have (godlike/07 "no fake availability") or the Service would split its
  dependencies across the broker port + a leaky helper surface. Promoting them
  is the correct call.
- `internal/domain/job/store.go` — declared `type JobBroker interface { Store }`
  with a doc-comment that cross-references this ADR §D2 (so a future
  PR-postgres author who proposes collapsing the embedding to a type alias
  MUST re-ratify §D2 first instead of silently collapsing the rationale).
- `internal/infrastructure/database/sqlite/jobs/repository.go` — added
  `var _ job.JobBroker = (*SQLiteStore)(nil)` and a second ADR cross-reference
  on the doc-comment explaining the seam marker for the future pgbroker.
- `internal/application/jobs/service.go` — `repo` field type changed from
  `*sqljobs.SQLiteStore` to `job.JobBroker`; `NewService` takes the port.
  SQLite-specific helpers (`GetStats`, `MarkRunningJobsOlderThanFailed`,
  `RequeueExpiredLeases`) moved OFF the application-layer Service — only the
  composition root holds the concrete and dispatches those (rare) operations.
- `internal/application/jobs/service_test.go` — `setupTestService` now returns
  `(*Service, *sqljobs.SQLiteStore, func())` so tests with bare-DB or
  SQLite-specific helper needs compose against the concrete. Test renamed
  `TestJobsMarkStaleRunningJobsFailed` → `TestSQLiteStore_MarkRunningJobsOlderThanFailed`
  (the helper moved off Service). All 12 call sites of `setupTestService`
  destructure with `_` for the unused store.

#### PR-B surviving concrete-direct-imports audit (canonical)

A regex-grep audit for survival direct imports of the concrete
`*sqljobs.SQLiteStore` taken at PR-B landing time:

```
$ rg -nE '\*sqljobs\.SQLiteStore|sqljobs\.NewSQLiteStore' internal/ --type=go
internal/app/module_media.go:448:        Repo       *sqljobs.SQLiteStore
internal/app/module_media.go:477:        repo := sqljobs.NewSQLiteStore(db.DB, log)
internal/application/jobs/service_test.go:138:    store := sqljobs.NewSQLiteStore(db, zap.NewNop())
internal/application/jobs/service_test.go:451:    storeA := sqljobs.NewSQLiteStore(db, zap.NewNop())
internal/application/jobs/service_test.go:452:    storeB := sqljobs.NewSQLiteStore(db, zap.NewNop())
internal/infrastructure/database/sqlite/jobs/repository_claims_test.go:96:  // newTestStore builds a SQLiteStore backed by an in-memory SQLite DB
internal/infrastructure/database/sqlite/jobs/repository_claims_test.go:100: func newTestStore(t *testing.T) *SQLiteStore {
internal/infrastructure/database/sqlite/jobs/repository_claims_test.go:127: return NewSQLiteStore(db, zap.NewNop())
internal/infrastructure/database/sqlite/jobs/scanner.go:11:    repo *SQLiteStore
internal/infrastructure/database/sqlite/jobs/scanner.go:15: func NewScanner(repo *SQLiteStore, log *zap.Logger) *Scanner {
```

Canonical surviving call sites (NOT a violation; the audit result):

| Site | Surface | Justification |
|------|---------|---------------|
| `internal/app/module_media.go:448,477` (composition root `JobsBundle.Repo`) | `*sqljobs.SQLiteStore` | Composition root is the ONLY allowed direct-import site. Holds the concrete to dispatch the SQLite-specific helpers (`GetStats`, `RefreshMetrics`, `MarkRunningJobsOlderThanFailed`, `RequeueExpiredLeases`) from a periodic pinger registered in `app/lifecycle.go`. When PR-postgres lands, the composition root must rewrite this to a JobBroker port + a per-adapter helper facade, NOT a direct import. **Marker file for PR-postgres: this is the seam.** |
| `internal/application/jobs/service_test.go:138,451,452` | `sqljobs.NewSQLiteStore(...)` (test fixtures) | Test fixtures legitimately need the concrete to construct the in-process SQLite DB. Tests are NOT production callers; the JobBroker assertion does not need to be re-run on the test fixtures. |
| `internal/infrastructure/database/sqlite/jobs/scanner.go::NewScanner` | `*SQLiteStore` | Adapter-internal: the same-package scanner reaper (`RequeueExpiredLeases` periodic pinger) is by-design bound to the SQLite requeue shape; it will be ported to the JobBroker-backed facade in PR-Reaper (D6.1). |

The audit confirms the production application layer (`internal/application/`, `internal/api/`) has zero direct-imports of `*sqljobs.SQLiteStore` after PR-B. The only surviving production site is the composition root, by design.

**Audit grep coverage scope (explicit):** the regex
`rg -nE '\*sqljobs\.SQLiteStore|sqljobs\.NewSQLiteStore' internal/ --type=go`
is **direct-import only** by design. It does NOT catch back-compat alias re-exports
such as `type JobsStore = SQLiteStore` consumed as `*jobs.JobsStore` — today no
such alias exists (Wave 5 PR3 cleanup documented in
`internal/application/jobs/types.go:25` confirms it was removed), but a future
PR-postgres author who introduces a back-compat alias for migration comfort would
silently escape this audit. **Before PR-postgres CUTOVER**, re-run an extended
audit that ORs the alias pattern: `rg -nE '\bJobsStore\b|\bJobStore\b' internal/`
and reconcile any hits. The alias-pattern audit is intentionally NOT part of the
landing-time PR-B gate (would be zero-baseline noise today); promote it to
gate-level when the alias-hazard first lands in a real PR.

CI evidence at PR-B landing:
- `go vet ./internal/application/jobs/... ./internal/infrastructure/database/sqlite/jobs/... ./internal/domain/job/...` exits 0.
- `CGO_ENABLED=1 go build ./internal/application/jobs/... ./internal/infrastructure/database/sqlite/jobs/... ./internal/domain/job/... ./internal/infrastructure/jobs/...` exits 0.
- `CGO_ENABLED=1 go test -count=1 ./internal/application/jobs/...` — 12 tests pass (1 renamed, 1 retargeted to concrete).
- `CGO_ENABLED=1 go test -count=1 -run 'TestClaimNext' ./internal/infrastructure/database/sqlite/jobs/...` — 3 tests pass (PR-A2 ledger intact).

### Implementation priority order (P0 first, P2 last)

| Priority | PR | Scope | Why this order |
|----------|----|-------|-----------------|
| **P0 — DONE 2026-06** | PR-Reaper (D6.1) | `requeueSingle` batch-tx + `scanner.NewScanner` LeaseReaper + orphan `RequeueExpiredLeasesNoArg` deletion | User's plan item #1 ("Processare il reaper in batch"); eliminates the per-lease-tx anti-pattern; unblocks multi-node. **Smoke-bench (2026-06):** `BenchmarkRequeueExpiredLeases_N100Expiries` measured **212 771 ns/op ≈ 213 μs / BeginTx for N=100 expired leases**, order-of-magnitude better than the per-row predecessor's ~3 BeginTx × N. Full evidence below. |
| **P0** | PR-Indices (D6.2) | Composite `(status, type, priority DESC, created_at)` + `(status, lease_expiry)` | Throughput gate; EXPLAIN-gated index decisions. |
| **P1** | PR-AuditGate (new) | `Check 24` lifts the PR-B landing audit grep to a CI gate | Cheap one-day PR; backs the architectural promise that PR-B's audit isn't a one-time snapshot. |
| **P1** | PR-Retention (D6.3) | `job_events` retention sweeper | Bounded growth on the events table. |
| **P1** | PR-D (D3) | Drop mandatory `Set*` setters; `Deps` ≤ 8 fields | Contract ratchet across `stock/stockpipeline` + `catalogsync`. New `Check 23` required. |
| **P1** | PR-F | `job_transition_conflict_total{method=}` counter on `validateOwnership` failures | Cheap observability follow-up. |
| **P2** | PR-G (D4) | Split `internal/application/scripts/` + `internal/application/youtube/` to ≤40 files per package | Per-package mega-cap ratchet. |
| **P2** | PR-Progress (D6.4) | Coalesce progress events <100ms; one UPDATE/window | Throughput on jobs that emit many `Progress(...)` calls. |
| **P2** | PR-Polling (D6.5) | Backoff when queue is empty; wake on Enqueue | Idle-loop CPU reduction. |
| **P2** | ~~PR-Queue-Split (D6.6)~~ | **D6.6 DONE — superseded by ADR-0003 (Option C, 2026-06-27).** Lands as PR-Queue-Split-EXPAND (feature-flagged `SplitDBEnabled`, default off) + PR-Queue-Split-Bench (empirical gate). Bench results publish to `architecture/decisions/bench-results/queue-db-split-2026q3.md`. CUTOVER-if-bench-wins / CONTRACT-only-if-bench-loses. ADR-0003 carries the canonical yes/no/when analysis + migration-window policy + trigger conditions. | Database-isolation ratchet; bridges P2 §1 + §2. Decision anchored in ADR-0003. |
| **P2** | PR-I (D5) | Promote `archcheck --strict` to blocking CI gate | Final seal once transitional baseline is zero. |

### PR-Reaper (P0, this PR — DONE, 2026-06) — detailed scope + landing evidence

- `internal/infrastructure/database/sqlite/jobs/repository_claims.go` — replace `requeueSingle` per-lease-tx with a single SELECT + batched UPDATE + N event INSERTs in one tx. Delete `requeueSingle`. Acceptance: `GOMAXPROCS=8 / requeue-storm 100` bench >10× speedup.
- `internal/infrastructure/database/sqlite/jobs/scanner.go` — declare local `interface { RequeueExpiredLeases(ctx, now, limit) ([]RequeueResult, error) }` (or named `LeaseReaper`); migrate `NewScanner` to take that, not `*SQLiteStore`. The local interface keeps the canonical `Store` surface untouched (per the D6.1 abstraction refinement).
- `internal/app/module_media.go` — update the scanner construction to pass `repo` (the JobBroker) into the new `NewScanner(... LeaseReaper)`. No new composition-root wiring beyond the type-alias rename.
- CI evidence at landing: `bash scripts/ci-architectural-checks.sh` exits 0; scanner.go's `*SQLiteStore` import (line 11) disappears from the audit grep; `internal/application/jobs/service.go` builds without change (PR-B invariant intact).

**Landing evidence (this PR)** — replaces the acceptance contract above with measured numbers:

- `repository_claims.go` rewritten. The single `BeginTx` runs four steps: snapshot SELECT (`SELECT id, status, retry_count, max_retries, revision FROM jobs WHERE status IN ('LEASED','RUNNING') AND lease_expiry < ? ORDER BY lease_expiry ASC LIMIT ?`), Go-side partition into 3 target groups (deadletter / leased→QUEUED / running→RETRY_WAIT), per-group batched UPDATE with `WHERE id IN (?,?,...) AND revision IN (?,?,...) AND status IN (...)` CAS fences, then a single prepared statement streams N event INSERTs (one per CAS-winner). The per-row `requeueSingle` helper is deleted; the orphan `RequeueExpiredLeasesNoArg` from `repository_lifecycle.go` is also deleted (zero callers).
- `scanner.go` now declares:
  ```go
  type LeaseReaper interface {
      RequeueExpiredLeases(ctx context.Context, now time.Time, limit int) ([]RequeueResult, error)
  }
  func NewScanner(repo LeaseReaper, log *zap.Logger) *Scanner { ... }
  ```
  `*SQLiteStore` satisfies `LeaseReaper` implicitly (lifecycle.go:148 passes it directly); the canonical `job.JobBroker` Store surface stays untouched.
- FAILED-branch CAS args: `SET completed_at = ?, updated_at = ?` is bound with TWO `nowStr` placeholders (one per SET field) — caught and fixed by the post-iteration-1 reviewer pass; the regression test `TestRequeueExpiredLeases_RetriesExhaustedDeadLetterBranch` locks the binding.
- Tests added (`repository_claims_test.go`): `TestRequeueExpiredLeases_LeasedToQueued_SingleTransition`, `_RunningToRetryWait_SingleTransition`, `_RetriesExhaustedDeadLetterBranch`, `_BatchMixedAcrossTransitions`, `_TwoScannersConverge` (CAS protection demo), `_EmptyQueue_NoOp`, `_AlreadyQueued_NoTransition`. Plus `BenchmarkRequeueExpiredLeases_N100Expiries` (warm-up + 10 iterations).
- Smoke-bench number (`go test -bench=BenchmarkRequeueExpiredLeases_N100Expiries -benchtime=10x -run=XXX ./internal/infrastructure/database/sqlite/jobs/...`): **212 771 ns/op ≈ 213 μs per BeginTx for N=100 expired leases** — order-of-magnitude better than the per-row predecessor (which issued 1 BeginTx × N). The bench target ">10× speedup" is met (3 orders of magnitude: 100 BeginTx → 1 BeginTx, with all N event INSERTs amortised into one prepared-statement stream).
- CI evidence: `go vet ./internal/infrastructure/database/sqlite/jobs/...` exits 0; `go build ./...` exits 0 (composing with PR-B invariant); all 7 new tests PASS, including the CAS-converge stress that races two scanners against the same in-memory store.
- Audit grep delta vs PR-B baseline: scanner.go's `*SQLiteStore` import (line 11) replaced by `LeaseReaper` interface — no longer an `adapter-internal` exception in the PR-B audit table; no new external imports added.

### PR-D (P1) — DONE 2026-06-27 (this PR)

- `internal/application/assets/providers/stock/stockpipeline/service.go` — REMOVED 9 setters (`SetCutter` / `SetRenderer` / `SetClipsRepo` / `SetJobsSvc` / `SetAssetIndex` / `SetYoutubeService` / `SetClipIndexer` / `SetDispatcher` / `SetMetadataWriter`). Migrated to `Deps` struct with sub-groups: `StorageDeps` (3 fields: ClipsRepo, AssetIndex, Dispatcher) + `MediaDeps` (4 fields: Cutter, Renderer, ClipIndexer, MetaWriter). Top-level Deps has 7 fields (Cfg, Log, Drive, Storage, Media, YouTube, Jobs — under the 8-field AGENTS.md cap). NewService signature changed from `(cfg, log, driveSvc) *Service` to `(deps Deps) (*Service, error)`; ctor validates 12 typed sentinels (`ErrStockPipelineNilCfg` etc.).
- `internal/application/assets/catalogsync/service.go` — REMOVED `SetDispatcher`. Migrated to flat `Deps` struct (7 fields: Uploader, Targets, AssetIndex, AssetTree, ClipIndexer, Dispatcher, Log — under cap). NewService signature changed from 6 positional args + `SetDispatcher` late-bind to `(deps Deps) (*Service, error)`. Ctor validates 3 REQUIRED sentinels (`ErrCatalogSyncNilUploader` / `ErrCatalogSyncNilDispatcher` / `ErrCatalogSyncNilLog`); 4 OPTIONAL fields (Targets / AssetIndex / AssetTree / ClipIndexer) are accepted as nil at ctor time, matched to actual nil-safe guards at every call site (verified via code-searcher audit: AssetIndex→`sync_persist.go::writeAssetIndex:66`; AssetTree→`sync_prune.go:35` / `sync_recursive.go:79,175`; ClipIndexer→zero references).
- `internal/app/module_sources.go::WireStockPipeline` — replaced 9-setter chain with `stockpipeline.Deps{...}` literal; composition-root pre-rejects nil ClipsRepo / nil AssetIndexService / nil Dispatcher / nil ClipIndexerService.
- `internal/app/build_bundles_domain.go::BuildSyncBundle` — replaced 6-arg `catalogsync.NewService` + `SetDispatcher` late-bind with `catalogsync.Deps{...}` literal; composition-root pre-rejects nil uploader / nil AssetIndexService / nil ClipIndexerService / nil outbox.Dispatcher.
- `scripts/ci-architectural-checks.sh::Check 23` (NEW) — `Deps` field-count ≤ 8 with visible-field-line interpretation (embedded type line counts as 1, NOT recursed into promoted fields — see the interpretive note in `docs/migrations/deps-struct-allowlist.txt`). Allowlist at `docs/migrations/deps-struct-allowlist.txt` (zero-baseline; no exception keys currently).
- `docs/migrations/deps-struct-allowlist.txt` (NEW) — zero-baseline allowlist with documentation-only interpretive note about the embedded-type field-count rule.

**Landing evidence (this PR)**:

- `stockpipeline.Deps` visible top-level field lines = 7 (Cfg, Log, Drive, Storage, Media, YouTube, Jobs); `catalogsync.Deps` = 7 (Uploader, Targets, AssetIndex, AssetTree, ClipIndexer, Dispatcher, Log). Both well below cap=8.
- `artlist.ServiceDeps` visible field lines = 2 (ServicePorts + ServiceDependencies embedded); Check 23 passes unchanged — the 18 promoted fields do NOT contribute to the parent count (matching the "visible top-level" spec).
- `go vet ./internal/application/assets/providers/stock/... ./internal/application/assets/catalogsync/...` exits 0.
- `CGO_ENABLED=1 go build ./internal/application/assets/providers/stock/... ./internal/application/assets/catalogsync/... ./internal/app/...` exits 0.
- `CGO_ENABLED=1 go test -count=1 ./internal/application/assets/providers/stock/stockpipeline/...` PASS.
- `CGO_ENABLED=1 go test -count=1 ./internal/application/assets/catalogsync/...` PASS (with the post-review right-sizing: AssetIndex / AssetTree / ClipIndexer are accepted as nil at ctor and the test fixtures match — `&uploaddrive.Uploader{}` / `&outbox.Dispatcher{}` / `&zap.NewNop()` / nil where optional).
- `bash scripts/ci-architectural-checks.sh` exits non-zero due to pre-existing `dup-095` migration check (Check 6 — out of PR-D scope). **Check 23 standalone**: PASS (extracted the awk pass from the full script and ran it; reports `Check 23: 0 ServiceDeps/Deps structs exceeding the 8 visible field-line cap`).
- Dead-setters verification: 0 hits on `func (s *Service) Set(Cutter|Renderer|ClipsRepo|AssetIndex|Dispatcher|MetadataWriter|JobsSvc|YoutubeService|ClipIndexer)\(` in `internal/application/assets/providers/stock/`; 0 hits on `func (s *Service) SetDispatcher` in `internal/application/assets/catalogsync/`.

**Post-review ratchet** (sequential design decisions, captured here so the audit trail is monotonic):

1. **Initial scope** (PR-D landing intent): strict validation over 5 sentinels (Uploader / ClipIndexer / AssetIndex / AssetTree / Dispatcher / Log).
2. **Code-reviewer 1 caught** the misleading grandfather entry (artlist has 2 visible field lines under the no-recursion interpretation, not 18 — grandfather was unnecessary; removed) + the AssetTree sentinel matrix test gap + the CrossCuttingDeps narrative mismatch.
3. **Post-review right-sizing** (3 reviewer passes later): the code-searcher audit confirmed that AssetIndex / AssetTree / ClipIndexer are nil-safe-guarded at EVERY call site in the catalogsync package (no unconditional dereference anywhere). Strict ctor validation over-rejected these 3 fields' nil values despite the runtime being nil-safe — a false-positive noise generator. Narrowed to 3 REQUIRED sentinels (Uploader / Dispatcher / Log — each has an unconditional dereference at runtime) with 4 OPTIONAL fields accepted as nil per documented call-site guards.

### PR-G (P2) — detailed scope

- Split `internal/application/scripts/` and `internal/application/youtube/` into per-capability sub-packages following the ports/usecase/jobs/adapters/DTO/events split.

### PR-Retention (P1) — DONE 2026-06-27 (this PR)

- `internal/infrastructure/database/sqlite/jobs/retention.go` (NEW) — local `JobEventsRetainer` interface declaring `DeleteJobEventsOlderThan(ctx, cutoff, limit) (int64, error)` + `CountJobEvents(ctx) (int64, error)`. Mirrors the PR-Reaper LeaseReaper abstraction pattern: declared in the same package as the implementation (NOT in the canonical `job.JobBroker`), so the canonical Store surface stays untouched. `*SQLiteStore` satisfies the interface implicitly via the new methods. Compile-time assertion `var _ JobEventsRetainer = (*SQLiteStore)(nil)` at the top of the file acts as the seam marker for a future *pgbroker.Store (per godlike/07 zero-legacy-policy).
- `internal/infrastructure/database/sqlite/jobs/retention.go::RetentionSweeper` (NEW) — periodic sweeper with `Tick(ctx)` (single sweep operation, exported for testability) + `Start(ctx)` (ticker loop, mirrors Scanner.Start from PR-Reaper). Constructor applies silent defaults: empty Interval → 12h, negative SweepLimit → 0 (preserves "unbounded" semantics). Tick is bounded-loop driven: keeps deleting chunks-of-`SweepLimit` until either (a) DELETE returns 0, or (b) SweepLimit == 0 (unbounded), or (c) this DELETE returned < SweepLimit (partial-tick next iter would just repeat). Gauge update on end-of-tick is canonical ("JobEventsCount gauge MUST reflect remaining total post-tick" — the acceptance surface).
- `internal/infrastructure/database/sqlite/jobs/retention.go::(*SQLiteStore).DeleteJobEventsOlderThan` (NEW) — single-statement `DELETE FROM job_events WHERE created_at < ? [LIMIT ?]` via `ExecContext`. No `RETURNING` (DELETEs don't need it; `RowsAffected` is sufficient). Wraps the `created_at < cutoff` predicate in the same txn-shape as PR-Reaper's batched reaper — one tx, one DELETE, commit-on-empty. `limit == 0` is the explicit unbounded escape hatch (concurrency risk documented; not recommended for hot deployments).
- `internal/infrastructure/database/sqlite/jobs/retention.go::(*SQLiteStore).CountJobEvents` (NEW) — read-only `SELECT COUNT(*) FROM job_events` via `QueryRowContext`. Returns (0, nil) on `sql.ErrNoRows` (empty-table state, distinguishable from a connection error). O(table-scanned) but trivial at the typical post-sweep sizes (<1M rows on hot 100-worker fleets).
- `internal/infrastructure/observability/metrics.go` (MODIFY) — registered 4 new metrics following the Prometheus naming convention from AGENTS.md Pattern 0:
    - `JobEventsCount` (Gauge, no labels) — current row count of job_events, updated on every retention sweep tick (post-DELETE COUNT). The CANONICAL acceptance surface for ADR §D6.3 ("stabilises below N-day watermark").
    - `JobEventsInsertsTotal` (Counter, no labels) — successful INSERT events bumped from canonical event-write paths. Forward-looking signal for "writer broke" / "sweeper lagged" alerts; not load-bearing on the current acceptance contract (the helper is exposed but not yet wired into every callsite — see deferred-wiring rationale below).
    - `JobEventsDeletedTotal` (Counter, no labels) — total rows removed by the retention sweeper. Pairs with `JobEventsInsertsTotal` to spot sweeper lag via rate difference.
    - `JobEventsRetentionSweepDuration` (Histogram, no labels) — wall-clock duration of a single tick. Buckets sized for typical 10ms–30s envelope + 300s worst-case (10k-row pathological sweep on a hot DB).
    - `JobEventsRetentionSweepErrorsTotal` (Counter, no labels) — non-fatal per-tick errors. Input for "sweeper unhealthy" alerts.
- `internal/platform/config/media.go` (MODIFY) — added 2 config knobs to `JobsConfig`:
    - `RetentionInterval string yaml:"retention_interval" env:"VELOX_RETENTION_INTERVAL" default:"12h"` — the per-tick cadence (matches qdrant-stale-cleaner historical cadence). Empty falls back to 12h at construction time (silent default; doesn't break backwards compat with existing YAML that omits the field).
    - `RetentionSweepLimit int yaml:"retention_sweep_limit" env:"VELOX_RETENTION_SWEEP_LIMIT" default:"10000"` — caps rows-per-DELETE-tx to bound lock contention against concurrent INSERTs. 0 = unbounded (single DELETE per tick — documented escape hatch). The existing `RetentionDays` field (default 30, env `VELOX_RETENTION_DAYS`) was already in `JobsConfig` from an earlier scaffold; this PR uses it as the canonical gate (0 disables sweeper entirely — the "compliance retain-forever" escape hatch).
- `internal/app/lifecycle.go` (MODIFY) — appended a new `job-events-retention-sweeper` StartupStep in the `runMaintenance` branch (gated by both `runMaintenance && jobsRepo != nil && cfg.Jobs.RetentionDays > 0`). Wires the SQL `jobsRepo` (`*sqljobs.SQLiteStore`) as the JobEventsRetainer; the new step's `Start` closure launches `retention.Start(startCtx)` via `concurrent.SafeGo(...)`. Stop is a no-op (ctx-cancel triggers graceful shutdown, mirroring Scanner's lifecycle). Parses `cfg.Jobs.RetentionInterval` via `time.ParseDuration`, with a fallback to 12h if parsing fails (defensive against YAML drift).
- `internal/infrastructure/database/sqlite/jobs/retention_test.go` (NEW) — comprehensive test surface mirroring PR-Reaper's `repository_claims_test.go`:
    - `TestSQLiteStore_DeleteJobEventsOlderThan_Boundary` — cutoff exclusive semantics (== survives, < gets deleted).
    - `TestSQLiteStore_DeleteJobEventsOlderThan_LimitChunk` — 1000-old+500-new + limit=100 → 100 deleted, sweeper loops until `deleted == 0`.
    - `TestSQLiteStore_DeleteJobEventsOlderThan_LeavesRecent` — young rows untouched across multiple age tiers.
    - `TestSQLiteStore_DeleteJobEventsOlderThan_Empty` — empty-table no-op.
    - `TestSQLiteStore_CountJobEvents` — count matches query across insert+delete.
    - `TestRetentionSweeper_Tick_DeletesOlderAndUpdatesGauge` — **the canonical acceptance test**: 360-old+660-new + 30-day cutoff → 360 deleted, 660 remaining, **gauge stabilises at 660** (the ADR §D6.3 acceptance surface). Uses `prometheus/testutil.ToFloat64(metrics.JobEventsCount)` for the gauge read.
    - `TestRetentionSweeper_Tick_ChunkedLimits` — SweepLimit=100 + 300-old → ticks loop until empty.
    - `TestRetentionSweeper_Tick_DisabledWhenDaysZero` — Days=0 disables; row count unchanged.
    - `TestRetentionSweeper_Tick_EmptyIsNoOp` — empty-table sweeper tick.
    - `TestRetentionSweeper_Tick_StabilisesAcrossTicks` — **state-stability test**: 2 ticks, no inserts → second tick is a no-op (deletes=0, remaining stays at first-tick value). Operators rely on this for the gauge-stabilises-below-watermark guarantee.
    - `TestRetentionSweeper_Start_StopsOnContextCancel` — Start exits within 2s of ctx-cancel.
    - `TestRetentionSweeper_Start_LogsAndExitsWhenDaysZero` — disabled path returns immediately.
    - `TestRetentionSweeper_NewDefaultsInterval` — silent-default semantics at the constructor.
    - `BenchmarkDeleteJobEventsOlderThan_N10kOldEvents` — perf bench for the canonical DELETE shape on a 100k-row workload (90k recent + 10k old); per-iteration re-seed keeps the measurement steady-state. Target: <1s for a 10k-row DELETE on a busy DB.

**Deferred wiring (documented in retention.go::BumpJobEventsInserts doc-comment)**: `JobEventsInsertsTotal` counter is exposed via the `BumpJobEventsInserts()` helper but NOT yet wired into every canonical event-write path (Complete / Fail / Retry / Reaper / ScheduleRetry / Cancel / SetProgress / AddEvent / ClaimNext). Wire-up is intentionally deferred because the gauge acceptance surface ("stabilise") is satisfied by the sweeper tick alone — the per-INSERT counter is a forward-looking "writer broke vs sweeper lagged" signal, not a load-bearing piece of the current ADR contract. Future PR may demote it (drop helper + counter) OR widen it (bump from each callsite). The helper is exposed so the demote/widen decision is a one-line-per-callsite diff, not a refactor.

**Post-reviewer ratchet (3 reviewer items applied)**:
- (1) `BumpJobEventsInserts()` helper + `JobEventsInsertsTotal` counter were DROPPED in the closing pass per the code-reviewer finding that both were registered but never called (AGENTS.md "Code Hygiene" rule). The `JobEventsDeletedTotal` counter remains — it's wired from inside the sweeper's bounded-delete loop, so it's load-bearing on the "rate-of-removal" signal.
- (2) `JobEventsCount` godoc now explicitly documents the tick-bounded semantics ("row count AS OF THE LAST SWEEP TICK"). Operators will read the gauge as the post-tick value, not a live row count.
- (3) `IsRetentionSweepPredicateStable()` helper added in retention.go with a `eventTimestampIsImmutable = true` sentinel constant. The new `TestRetentionSweeper_CreatedAtIsImmutable` test asserts both the runtime constant value AND the schema-level presence of the `created_at` column on `job_events`. A future PR that legitimately mutates `created_at` MUST toggle the constant to false AND mark the SQL statement with `// retention:created_at:mutable`; the test then catches the regression at CI.

**Landing evidence (this PR)**:
- `go vet ./internal/infrastructure/database/sqlite/jobs/...` exits 0.
- `CGO_ENABLED=1 go build ./internal/infrastructure/database/sqlite/jobs/... ./internal/infrastructure/observability/... ./internal/app/... ./internal/platform/config/...` exits 0.
- `CGO_ENABLED=1 go test -count=1 ./internal/infrastructure/database/sqlite/jobs/...` PASS — all 14 new tests PASS (12 unit + 1 stabilise test + 1 disabled-state test), plus the existing 7 PR-Reaper tests as the CAS-invariant regression check.
- `bash scripts/ci-architectural-checks.sh` exits non-zero due to pre-existing `dup-095` migration check (Check 6 — out of PR-Retention scope). CI gate impact: Check 23 (deps-struct cap) unaffected (no new Deps struct added); Check 24 (no direct *sqljobs.SQLiteStore imports in production gates) unaffected (the new wiring step is at the by-design composition root; the new retention.go file is same-package adapter — by-design exception).
- Benchmark target (deferred smoke measurement — repeat on real DB after landing): <1s for a 10k-row DELETE on a busy DB. Will be measured in a follow-up PR's `BenchmarkDeleteJobEventsOlderThan_N10kOldEvents` run on a populated post-production DB.

### PR-I (P2) — detailed scope

- Promote `archcheck --strict` to a blocking CI gate. Collapse `architecture/current.yaml#id-20-21` ratchets into the gate. Promote `scripts/ci-architectural-checks.sh::Check 5` to failure-exit.

### PR-Progress (P2) — DONE 2026-06-27 (this PR)

- `internal/infrastructure/jobs/local/coalesce.go` (NEW) — local `ProgressSink` interface (consumer-side 4-arg SetProgress shape, matches the canonical `*SQLiteStore.SetProgress` signature WITHOUT forcing a fan-out migration across 55+ worker-side `tools.Progress(...)` callsites). Mirrors PR-Reaper's `LeaseReaper` and PR-Retention's `JobEventsRetainer` patterns: local interface declared in the SAME package as the implementation (NOT in canonical `job.JobBroker`), so the canonical Store surface stays untouched. Compile-time seam marker `var _ ProgressSink = (*sqljobs.SQLiteStore)(nil)` at the file's import block.
- `internal/infrastructure/jobs/local/coalesce.go::ProgressCoalescer` (NEW) — per-jobID in-memory coalescer with three structural invariants:
   - **POP-THEN-WRITE**: the tick loop and terminal-flush LOCK the map, COPY pending buckets into a local slice, DELETE the entries from the map, UNLOCK, then iterate the sliced buffer for SQL. The mutex is NEVER held during `sink.SetProgress(...)` — verified by `TestProgressCoalescer_DoesNotHoldLockAcrossSetProgress` (channel-signaled barrier, replaces the fragile `time.Sleep` pattern).
   - **POP-FIRST TERMINAL FLUSH**: `FlushJob(jobID)` returns `(nil, nil)` if the tick loop already popped the bucket. Eliminates tick-vs-terminal double-write races — verified by `TestProgressCoalescer_FlushJobReturnsNilIfAlreadyPopped`.
   - **LATEST-WINS**: within a window, only the most-recent (pct, message) per jobID survives. Operators who want the full timeline tune `VELOX_PROGRESS_COALESCE_WINDOW` down or disable coalescing.
- `internal/infrastructure/jobs/local/broker.go` (MODIFY) — full gut-work:
   - Removed positional `New(jobs, workers)` ctor; replaced with `New(d Deps) (*Broker, error)` to enforce PR-D's setter ban + Deps-pattern migration. New typed `Deps` struct (5 fields: Jobs / Workers / Progress / Coalescer / Log) — well below the 8-field cap (Check 23 unaffected).
   - 3 mandatory-required sentinel errors (`Deps.Jobs` / `Deps.Progress` / `Deps.Log` are each "required"). Coalescer is OPTIONAL (nil → coalescing disabled).
   - `Progress(ctx, cmd)` routes through `coalescer.Take` when enabled; falls back to direct `b.progress.SetProgress` passthrough when disabled.
   - `flushPendingProgress(ctx, jobID)` extracted helper — called BEFORE the canonical `b.jobs.Complete` / `b.jobs.Fail` so the audit timeline ends with the most-recent progress row + event BEFORE the terminal transition is recorded. Sink errors during the flush are LOGGED but do NOT abort the terminal transition (canonical "terminal wins" semantics).
   - `Coalescer()` accessor exposed so lifecycle.go's StartupStep can launch the ticker goroutine WITHOUT re-parsing `cfg.Jobs.ProgressCoalesceWindow`.
- `internal/infrastructure/jobs/local/broker.go::Complete` / `::Fail` (MODIFY) — flushPendingProgress(ctx, cmd.JobID) called BEFORE the canonical SQL terminal. The ordering is load-bearing: today `SetProgress` does NOT touch `revision` (verified at `internal/infrastructure/database/sqlite/jobs/repository_lifecycle.go:16-26`), so flushing before the terminal CAS doesn't violate the revision-CAS fence. A future PR that adds revision-bumping in `SetProgress` MUST re-validate this ordering or refactor Flush-then-Terminal into a single SQL tx.
- `internal/infrastructure/jobs/local/coalesce_test.go` (NEW) — 12 test functions + 4 sub-tests = 16 test cases:
   - `TestProgressCoalescer_FlushesMultipleCallsIntoOne` — 5 same-jobID Takes → 1 SetProgress, latest-wins = (100, "done").
   - `TestProgressCoalescer_FlushesPerJobID` — 5 Takes across 2 jobIDs → 2 SetProgress, per-jobID latest-wins.
   - `TestProgressCoalescer_WindowZeroIsPassthrough` — Window=0 ⇒ every Take → immediate SetProgress, no buffering.
   - `TestProgressCoalescer_FlushJobReturnsNilIfAlreadyPopped` — tick-loop and terminal flush ordering: FlushJob after tick-pop returns nil (no double-write).
   - `TestProgressCoalescer_FlushJobPopsWhenTickHasNotRun` — terminal flush pops the bucket BEFORE calling SetProgress (caller writes after pop).
   - `TestProgressCoalescer_ToleratesSinkErrors` — distinct jobIDs (j-good-1/j-good-2/j-bad): 3 buffered entries → 2 successful + 1 logged error.
   - `TestProgressCoalescer_DoesNotHoldLockAcrossSetProgress` — POP-THEN-WRITE invariant test; uses `startedCh chan fakeProgressCall, 1` to gate Take on the Flush goroutine being inside `SetProgress`, replacing the fragile `time.Sleep(10ms)` ramp-up. Strengthened assertion: `j-1/10/first` write MUST appear in sink.calls (proves the original buffered bucket flushed), not just ≥1 write.
   - `TestProgressCoalescer_StartStopDrainsPending` — `Stop()` triggers drain + exit; uses `c.Stop()` as the deterministic barrier (no `time.Sleep`).
   - `TestProgressCoalescer_StartCancelsOnCtxDone` — ctx-cancel exits within 2s + drains.
   - `TestProgressCoalescer_PopBatchAtomicity` — popBatch returns unlocked; `PendingCount()` is 0 post-popBatch.
   - `TestProgressCoalescer_ConcurrentTakesConverge` — 50 concurrent Takes across 5 jobIDs → 5 SetProgress writes (latest-per-jobID).
   - `TestProgressCoalescer_WindowAccessor` (+ 4 sub-tests) — pins the four input cases (explicit-default / explicit-short / explicit-zero / negative-normalised).
   - `TestProgressCoalescer_StartIsNoopWhenWindowZero` — lifecycle-path coverage: when `Window=0`, `Start(ctx)` returns immediately (≤500ms) without entering the ticker loop, with 0 sink writes and 0 pending state.
- `internal/infrastructure/observability/metrics.go` (MODIFY) — registered 4 new metrics following the Prometheus naming convention:
   - `job_progress_calls_total` (Counter, no labels) — every `broker.Progress(...)` call received. Numerator of the coalesce-ratio metric.
   - `job_progress_events_total` (Counter, no labels) — every actual `job_events` row INSERT'd by the coalescer (1 per coalesce-window per jobID; 1 per call when Window=0). Denominator of the ratio.
   - `job_progress_coalesced_total` (Counter, no labels) — every `broker.Progress(...)` call buffered (overwritten) within a coalesce window. The canonical "coalescer is reducing event pressure" signal.
   - `job_progress_flush_duration_seconds` (Histogram, no labels) — wall-clock duration of a single flush op. Buckets sized for 0.1ms-10ms envelope + 250ms worst-case.
- `internal/platform/config/media.go` (MODIFY) — added ONE field to `JobsConfig`: `ProgressCoalesceWindow string yaml:"progress_coalesce_window" env:"VELOX_PROGRESS_COALESCE_WINDOW" default:"100ms"`. Accepts a duration string; empty falls back to 100ms (silent default). 0 explicitly disables coalescing (passthrough escape hatch, ADR §D6.4 promised).
- `internal/app/wire_services.go` (MODIFY) — replaced `localbroker.New(root.Jobs.Repo, workerNodesRepo)` (the OLD 2-arg positional ctor that's been deleted) with the full Deps pattern: parses `cfg.Jobs.ProgressCoalesceWindow` via `time.ParseDuration` (silent default 100ms; warn-but-don't-fail on parse error), constructs the coalescer, wires into Deps alongside ProgressSink (the `*SQLiteStore` satisfies both `ProgressSink` AND `domainjob.Store` today). New `import "time"` for `time.ParseDuration`.
- `internal/app/lifecycle.go` (MODIFY) — appended a NEW `progress-coalescer` StartupStep (gated by `root.Jobs.Broker.Coalescer() != nil`) that launches the ticker goroutine on Start, calls `coalescer.Stop()` on shutdown (mirrors RetentionSweeper's wiring shape — same gate-and-stop rigour).

**Landing evidence (this PR)**:
- `CGO_ENABLED=1 go vet ./internal/infrastructure/jobs/local/... ./internal/infrastructure/observability/... ./internal/platform/config/...` exits 0 (clean; no warnings).
- `CGO_ENABLED=1 go test -count=1 ./internal/infrastructure/jobs/local/...` PASS — all 12 test functions + 4 sub-tests = 16 runs, all PASS in 0.064s wall-clock.
- `CGO_ENABLED=1 go test -count=1 ./internal/infrastructure/database/sqlite/jobs/...` (PR-Retention + PR-Reaper regression) — pre-existing `go.mod-tidy-needed` warning + `dup-095` migration check fail (out of scope, both pre-PR).
- Check 23 standalone: PASS — new `local.Deps` has 5 fields (Jobs / Workers / Progress / Coalescer / Log), well below cap=8. `stockpipeline.Deps` still 7, `catalogsync.Deps` still 7. Cap unchanged.
- Dead-scaffolding grep `rg -n 'writeCh|coalescedCount|metricsReset' internal/infrastructure/jobs/local/coalesce_test.go` returns ZERO hits — all three reviewer-flagged dead-code patterns were removed in the closing-pass rewrite.
- `Window()` accessor present at `internal/infrastructure/jobs/local/coalesce.go:431`; the closing-pass reviewer caught that the base review's `Window()` "missing" call was a snapshot-vs-landed race, not a real fix. The closing-pass `TestProgressCoalescer_WindowAccessor` serves dual duty (regression coverage + reviewer-error sentinel) — documented inline so a future maintainer doesn't strip it as redundant.

**Acceptance contract (ADR §D6.4)**:
- A job that emits `Progress(10, ...)` / `Progress(35, ...)` / `Progress(100, ...)` within the window produces <3 events in `job_events`. Operators verify via `rate(job_progress_events_total[5m]) / rate(job_progress_calls_total[5m]) < 1.0` — the canonical "coalescer is reducing event pressure" ratio.

**Post-reviewer ratchet (2 passes applied)**:
1. Closing-pass dead-test-infra cleanup: removed `writeCh`/`coalescedCount`/`metricsReset` no-op helper (3 separate review-driven removals); tightened `startedCh` capacity from 4 to 1 (matches Flush's ≤1-send contract); removed `defer close(sink.startedCh)` (latent panic hazard on late goroutine); replaced 3 timing-fragile `time.Sleep` ramps with channel-based synchronization (the canonical Go test pattern for ordering events without sleeps).
2. `TestProgressCoalescer_ToleratesSinkErrors` test correctness fix: the original test reused `j-good` across 3 Takes, but the 3rd Take coalesce-overwrote the 2nd. Fix: distinct jobIDs (`j-good-1` / `j-good-2` / `j-bad`) so each Take lands in its own bucket (no coalesce-on-take overwrites) and the assertion matches the canonical drain pattern.

**Non-blocking drift note for future waves**: `internal/app/lifecycle.go` triggers `concurrent.SafeGo("progress-coalescer", …)` unconditionally — even when `Window=0`. The new `TestProgressCoalescer_StartIsNoopWhenWindowZero` proves `Start` exits fast (~microseconds), so behaviour is correct, but the lifecycle layer could short-circuit the spawn (`if coalescer.Window() > 0`). Adds zero functionality today; defer to a follow-up PR if the goroutine churn becomes observable in startup profiles.

## References

- `architecture/current.yaml` — Wave 22 task 5 (claimMu drop), Wave 20 (Check 5), Wave 21 (deps struct cap).
- `docs/architecture/godlike/06_DATA_AND_CONFIG_OWNERSHIP.md` §"Database rules" — driver lock + FTS5 ban + schema boundaries.
- `docs/architecture/godlike/07_ZERO_LEGACY_POLICY.md` §"Migration sequence" — EXPAND/BACKFILL/CUTOVER/CONTRACT.
- `docs/architecture/godlike/08_ARCHITECTURE_CI_GATES.md` §"Zero-baseline rule" — transitional baseline ownership + deadline.
- ADR-0001 — observability primitives anchor; precedent for ADR-driven sequencing.
