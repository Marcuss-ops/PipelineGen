# ADR 0003: jobs.db.sqlite split — yes / no / when

> **Status:** **ACCEPTED — Option C** (SPLIT + BENCH hybrid).
> Decided 2026-06-27 by the user-facing PR-Queue-Split decision gate.
> The single-DB canonical shape stays AS-IS for the bench window;
> PR-Queue-Split-EXPAND lands behind `cfg.Jobs.SplitDBEnabled` with
> `default=false`; once both shapes compile + boot cleanly, a bench
> harness measures each shape under representative write load; results
> publish to `architecture/decisions/bench-results/queue-db-split-2026q3.md`;
> at the bench-result point the field flips `default=true` (CUTOVER)
> or stays `default=false` (CONTRACT-only — drop the EXPAND-shape code
> if bench shows the split is not worth its cost).
>
> The original tracking entry for this decision lives in ADR-0002 §D6.6
> (supersession note appended at line 87; tracking row flips at line 239
> from "P2 bridging" to "D6.6 DONE — PR-Queue-Split-EXPAND (Option C bench-driven)").
>
> **Deciders:** Architecture + observability + jobs subsystem owner + the
> future PR-B (multi-node `job.JobBroker` PostgreSQL adapter) author, if
> PR-B is on the near horizon.
>
> **Scope:**
>   - Decide whether to spin `jobs.db.sqlite` (WAL, separate pool) for the
>     three jobs-domain tables (`jobs`, `job_events`, `dead_letter_jobs`)
>     out of the canonical single file `media.db.sqlite`.
>   - If yes: lay out the migration-window policy (legacy `*SQLiteStore`
>     alias for reads during the cutover; ratchet to zero-baseline via
>     godlike/07's EXPAND/BACKFILL/CUTOVER/CONTRACT sequence).
>   - If no or when: lay out the trigger conditions that would re-open
>     the decision.
>
> **Authority carve-out:**
>   - godlike/07 §"Migration sequence" — EXPAND/BACKFILL/CUTOVER/CONTRACT
>     and the "no permanent dual-write" rule are canonical for any
>     migration captured by this ADR.
>   - godlike/06 §"Database rules" — single driver lock (`mattn/go-sqlite3`),
>     FTS5 ban, "one owner per table" — are canonical for both branches.
>   - ADR-0002 §D6.6 — the originating tracking entry that this ADR either
>     ratifies or supersedes.

## Context

PipelineGen's durable metadata store has been consolidated into a single
SQLite file (`<DataDir>/media/media.db.sqlite`) since the Wave 14–18 PR2.6
sub-wave in June 2026, which collapsed the formerly separate `artlist.db.sqlite`
into `media.db.sqlite`. Today the same file holds:

| Domain group | Tables (representative) | Write-rate profile |
|---|---|---|
| Asset / media | `media_assets`, `asset_locations`, `asset_processing`, `asset_versions`, `clip_folders`, … | Bursty read-heavy (Qdrant projection source). |
| Scripts / voiceover / cache | `scripts`, `script_versions`, `voiceovers`, `youtube_cache`, `gemma_memory`, `search_queries`, … | Mixed; mostly append-only. |
| Queue / jobs (this ADR's scope) | `jobs`, `job_events`, `dead_letter_jobs` | High-churn: every `ClaimNext`, `Complete`/`Fail`/`ScheduleRetry`, `Cancel`, `SetProgress`, `RequeueExpiredLeases`, retention sweep tick, and ad-hoc `AddEvent` write hits the shared WAL. |

The queue tables are write-heavy because *every* job lifecycle transition is
a single SQLite transaction (atomic UPDATE + INSERT into `job_events`
INSERTED via the same `BeginTx`, per ADR-0002 §D1 atomic-claim shape) and
the RetentionSweeper (PR-Retention, §D6.3) issues bounded DELETEs against
`job_events` on every 12-hour tick.

All three queue tables live in `media.db.sqlite` and share the canonical
SQLite PRAGMA surface that godlike/06 §"Database rules" pins:

- `journal_mode = WAL` — single WAL file for the entire DB.
- `busy_timeout = 5000` — applied at the connection layer.
- `synchronous = NORMAL` — single writer-serialization point.

Under WAL, SQLite writes land in `-wal` and a shared reserved lock
serialises concurrent writers (one writer at a time; many readers). The
queue write-rate therefore contends with the asset / scripts / cache
writers for the same single-writer token. **No benchmark has been run to
quantify this contention today** — the contention is hypothetical, not
empirical.

Two facts anchor the decision:

1. **All queue writes are self-contained today.** Re-grepping the three
   write paths (ClaimNext, RequeueExpiredLeases, Complete/Fail/ScheduleRetry/Cancel/Retry/DeadLetter/AddEvent/SetProgress) confirms zero cross-table
   references to non-queue tables. Every write is `jobs` + `job_events` +
   optionally `dead_letter_jobs`, all in one `BeginTx`, all in the same
   one `media.db.sqlite`-backed `*sql.DB` handle.

2. **PR-B (multi-node `job.JobBroker` PostgreSQL adapter) is the explicit
   coupling risk.** PR-B is repeatedly foreshadowed across the codebase
   in compile-time assertion comments (`internal/application/jobs/service.go:60`,
   `internal/infrastructure/database/sqlite/jobs/retention.go:56`,
   `internal/infrastructure/database/sqlite/jobs/scanner.go:15`,
   `internal/application/jobs/notifier.go:7`,
   `internal/infrastructure/database/sqlite/jobs/notifier.go:28`),
   but `*pgbroker.Store` does **not** exist on `main` HEAD as of
   2026-06-27. The composition-root seam in
   `internal/app/module_media.go:448` (`Repo *sqljobs.SQLiteStore`) is the
   documented future-port rewiring point.

## Decision

### Status

The yes/no/when branch is **unresolved**. This ADR presents three options
(A: split-now, B: defer, C: split-then-defer) with quantified trade-offs
and an explicit recommendation. No migration work lands until a decider
ratifies one branch and flips the Status header to **ACCEPTED**.

### Option A — SPLIT NOW (PR-Queue-Split as written in §D6.6)

**Shape**

- New file `data/jobs/jobs.db.sqlite` opened via
  `storage.OpenSQLiteDB(cfg.Storage.JobsDBPath(), log)`.
- Independent WAL, independent connection pool, independent PRAGMA
  contract application.
- Composition-root rewires: `sqljobs.NewSQLiteStore(jobsDB.DB, log)`,
  `Tests.ScanTable` and any other infra-direct caller that opens
  `cfg.Storage.PrimaryDBPath()` exclusively for queue tables moves to
  the new path.
- Migration sequence per godlike/07:
  - **EXPAND**: introduce `jobs.db.sqlite`; migration ledger acquires
    the ledger; jobs migrations go against the new ledger; media-domain
    migrations stay on the media ledger.
  - **BACKFILL**: copy `jobs`, `job_events`, `dead_letter_jobs` rows
    from `media.db.sqlite` to `jobs.db.sqlite` in a single `INSERT … SELECT`
    pass at boot OR via an explicit admin migration subcommand.
  - **CUTOVER**: composition root points `*SQLiteStore` at `jobs.db.sqlite`.
    Operations read from `jobs.db.sqlite` only. The legacy compatibility
    alias (read-only `media.db.sqlite` jobs-tables reader for operators
    auditing the migration period) lives behind a feature-flag.
  - **CONTRACT**: drop the legacy alias once the
    `architecture/deprecations.yaml` PR-QUEUE-SPLIT ratchet entry's
    usage metric (`SELECT COUNT(*) FROM media_legacy_jobs_reader` or
    equivalent) registers zero sustained reads for the canonical
    guidance window (>=7 days post-CUTOVER).
- Net deletion of data: zero. Net addition: one DB file, one migration
  ledger, ~50–80 LoC of composition-root wiring, ~40–60 LoC of migration
  subcommand surface, plus the alias surface tracked to zero-baseline.

**Pros**

- Eliminates WAL contention between queue writes and media writes.
- Pre-paves the queue externalization story: jobs.db.sqlite is one step
  closer in shape to "queue lives in its own durable store" than a
  single-DB monolith.
- Zero-baseline migration window is explicit and time-bounded
  (godlike/07 CONTRACT deadline + usage metric).
- Canonical-free shape: a future PR-B can land against jobs.db.sqlite
  OR against media.db.sqlite — neither is locked in.

**Cons**

- **Inverts 5+ years of consolidation momentum.** PR2.6 (June 2026,
  wave 14–18) collapsed `artlist.db.sqlite` into `media.db.sqlite`
  *deliberately*; the same wave explicitly removed `media.db.sqlite`
  (the old path) as "unused". Reverting that direction is a non-trivial
  signal to future maintainers.
- **Coupling risk with PR-B is not mitigated.** If PR-B lands within
  90 days of CUTOVER, the `jobs.db.sqlite` shape becomes transient —
  jobs.db.sqlite → alias (jobs.db.sqlite + pg jobs) → drop both. Two
  migration windows back-to-back.
- **Migration-window policy per godlike/07 forbids permanent dual-write.**
  The `architecture/deprecations.yaml` entry MUST carry:
  - introduction date = this ADR's ACCEPTED date
  - removal deadline = CUTOVER + ~30 days (CONTRACT window)
  - usage metric = legacy-`media.db.sqlite`-jobs-tables reader counter
  - compatibility test = the legacy reader path's test surface
  An expired deprecation fails CI (godlike/07).
- **Cross-DB ACID is dead forever.** A future PR that legitimately needs
  to atomically update a queue row + a media row (e.g. a job completion
  that updates an asset row in the same transaction) cannot do it
  atomically across `jobs.db.sqlite` + `media.db.sqlite`. SQLite has no
  XA. Two-phase commit at the application layer is brittle; an outbox
  pattern is the only viable approach, and it changes the semantics from
  in-tx to eventual-consistency.
- **Composition-root wiring churn:** a single `BuildJobsBundle(db, log)`
  call site splits into `(jobsDB, mediaDB)` arguments; `module_media.go`
  already has 12 fields, growing further.
- **Bench required but not run:** no WAL contention measurement exists
  today. Option A's "real benefit" is therefore argued from intuition,
  not evidence.

### Option B — DEFER TO PR-B (recommended primary)

**Shape**

- Status of §D6.6 changes from "P2 bridging" to "DEFERRED — superseded
  by PR-B on landing". The tracking row in
  `architecture/decisions/0002-p2-p3-roadmap.md:239` is annotated but
  not deleted.
- No code changes. The single-DB shape stays canonical until PR-B lands.
- This ADR is filed as Accepted with branch B; the next infrastructure-
  shaping PR (PR-B) inherits the §D6.6 backlog.

**Pros**

- **PR-B cleanly replaces `*SQLiteStore` with `*pgbroker.Store`.** No
  jobs.db.sqlite legacy windows anywhere. The historical artifact of
  this ADR is the rationale for not splitting, archived in git
  (no permanent doc on `main`).
- **Zero migration-window policy needed.** godlike/07's not violated
  because there is no migration.
- **Composition-root churn is deferred to PR-B.** Today's code stays
  as-is; PR-B introduces a per-adapter helper facade (per ADR-0002
  audit table line 201), which is the single structural change that
  obsoletes the §D6.6 backlog.
- **Cross-DB ACID risk never materializes** at the SQLite layer. PR-B
  moves jobs to PG; PG + SQLite cross-store writes use the canonical
  outbox pattern (godlike/06 §"Qdrant projection" same idea), which is
  mature in the codebase.

**Cons**

- **WAL contention remains hypothetical** (no benchmark). If it bites
  in production, Option B's deferral becomes "fix forward", which costs
  reactive engineering capacity vs Option A's proactive shape.
- **PR-B has no firm landing date.** ADR-0002 lists PR-B as DONE for
  the `interface JobBroker` declaration but NOT for the PostgreSQL
  adapter (the table at ADR-0002:239 has no PR-B-postgres row). Until
  PR-B-postgres lands, the "single-DB shape stays canonical" is the
  canonical shape for an indeterminate period.

**Trigger conditions that would re-open Option A under Option B**

- PR-B-postgres is delayed beyond Q4 2026 AND a WAL contention incident
  is observed in production (operator dashboard `JobClaimDuration*`
  spikes >100ms p99 for sustained periods under non-degenerate queue
  write pressure).
- The composition-root seams (LeaseReaper, JobEventsRetainer,
  QueueNotifier, ProgressSink local interfaces) prove insufficient to
  swap to a separate adapter, requiring cross-table tx for a future
  feature that godlike/06 §"One owner per fact" cannot split otherwise.

### Option C — SPLIT + BENCH + CONTINUOUS-CONFIGURATION (hybrid)

**Shape**

- Land EXPAND and BACKFILL of Option A.
- Land CUTOVER behind a feature flag (`cfg.Jobs.SplitDBEnabled`) that
  defaults to `false`.
- Run a bench on both shapes (single-DB and split-DB) under
  representative write load; publish results in
  `architecture/decisions/bench-results/queue-db-split-2026q3.md`.
- Either flip the default to `true` (bench wins) OR archive Option A
  (bench loses) at the bench-result point in time.

**Pros**

- Combines Option A's proactive shape with Option B's evidence-based
  gate. Default-off means zero operator disruption until bench wins.
- The feature flag + bench workflow is a known-good shape (cf.
  retention sweep knob, progress coalesce window knob).

**Cons**

- **Doubles the composition-root wiring from day one.** Both single-DB
  AND split-DB paths must compile + boot + pass CI for the entire
  bench-collect window.
- **Bench methodology becomes the decider**, not the architecture
  review. Without an explicit bench harness, the project will defer the
  closure indefinitely.
- **godlike/07 "no fake availability" applies to the split-DB path
  until CUTOVER**, which means the split-DB implementation must be
  complete (not just EXPAND-shaped stubs) before bench can run. That's
  Option A's full cost before Option B's gate kicks in.
- **Net cost > max(Option A, Option B) individually.**

## Recommendation

**Option B (DEFER)** with two follow-ups:

1. **File a TRIGGER CONDITIONS** section in this ADR (drafted below).
   Any pull request that meets a trigger condition re-opens Option A
   (with this ADR as the rationale anchor).
2. **Add `JobClaimDurationSeconds` histogram** to
   `internal/infrastructure/observability/metrics.go` to make trigger-
   condition #1 observable. This is a forward-looking observability
   PR; the metric is small (5 lines), the bench harness (cf. PR-Retention
   smoke bench at `internal/infrastructure/database/sqlite/jobs/retention_test.go::BenchmarkDeleteJobEventsOlderThan_N10kOldEvents`)
   reuses the existing seed → measure → re-seed pattern.

The recommendation rests on three load-bearing points:

- **PR-B coupling is the dominant risk.** A split-before-PR-B has to be
  torn out within 1-2 PR-B follow-ups if PR-B lands in the window
  (highest probability Q3-Q4 2026). The pull-of-the-thread is real even
  with a tight migration-window policy.
- **No empirical data justifies the split's "real benefit."** Option A's
  pro-#1 (WAL contention) is ranked hypothetical, not measured. Basing
  a structural change on intuition is exactly the anti-pattern the ADR
  process exists to surface.
- **The composition-root seams are already mechanically ready for Option
  A to land cheaply when needed.** LeaseReaper, JobEventsRetainer,
  QueueNotifier, ProgressSink local interfaces + the canonical
  `job.JobBroker` port — five adapters in place; the split is then
  mostly a composition-root + config change. Option B captures the
  "no work now" half of the deal; Option A's adapter surface is already
  paved.

## Migration-window policy (for Option A — recorded now so the policy
isn't contested at decision-time)

If the user accepts Option A, the migration sequence MUST follow
godlike/07 strictly:

### EXPAND

- `migrations/sqlite_jobs/0XX_*.sql` — fresh ledger, parallel to
  `migrations/sqlite/`.
- New `JobsMigrationRunner` constructs the canonical
  `journal_mode=WAL, busy_timeout=5000, synchronous=NORMAL` PRAGMA
  surface on `jobs.db.sqlite`.
- `internal/platform/config/media.go` adds `JobsDBPath string` with
  default `<DataDir>/jobs/jobs.db.sqlite`.
- The composition root opens BOTH `media.db.sqlite` AND `jobs.db.sqlite`
  in EXPAND; reads from `media.db.sqlite` (legacy pre-cutover state).

### BACKFILL

- One-shot `INSERT … SELECT` from `media.db.sqlite` to `jobs.db.sqlite`
  for each of the three tables, in a single `BeginTx` per table, with
  `row_count` crosschecks at the end of each.
- Optionally exposed via `cmd/admin/migrate_jobs_db.go` (manual operator
  trigger) AND a startup-time automated check (config-driven; default
  to operator-trigger).
- Idempotent: re-running the migration with the same source of truth
  is a no-op via `INSERT OR IGNORE` (jobs PK is TEXT → check row id
  conflicts; job_events PK is `evt_*` → check row id conflicts;
  dead_letter_jobs PK is INTEGER AUTOINCREMENT → unambiguous).

### CUTOVER

- Composition-root points `*SQLiteStore` exclusively at `jobs.db.sqlite`.
- The legacy compatibility alias
  (`media_legacy_jobs_reader.go`, read-only against the jobs tables
  in `media.db.sqlite`) lives behind `cfg.Jobs.LegacyAliasEnabled`
  with default `true` during CUTOVER window (~30 days).
- `architecture/deprecations.yaml` gains PR-QUEUE-SPLIT entry with:
  - `id`: `PR-QUEUE-SPLIT`
  - `owner_capability`: `internal/infrastructure/database/sqlite/jobs`
  - `exact_symbol`: `media_legacy_jobs_reader.Read*`, the legacy alias file
  - `file`: `internal/infrastructure/database/sqlite/jobs/legacy.go` (NEW)
  - `introduction_date`: ADR-0003 ACCEPTED date
  - `removal_date`: CUTOVER + 30 days
  - `tracking_issue`: TBD (or use the PR-Queue-Split tracking issue
    cited at ADR-0002:239)
  - `compatibility_test`: `media_legacy_jobs_reader_test.go`:
    the legacy reader's read path is tested in CI for the entire
    compatibility window
  - `usage_metric`: `media_legacy_jobs_reads_total` Prometheus counter,
    added in `internal/infrastructure/observability/metrics.go` at
    CUTOVER, decremented / observed for the compat window. Operators
    alert on >0 sustained usage as a "migration in flight".
- `scripts/ci-architectural-checks.sh` gains Check 25 (or extension
  of an existing check) that fails CI on `media.db.sqlite:*` writes
  from `internal/infrastructure/database/sqlite/jobs/*` files
  (the ONLY post-CUTOVER source of fatal writes would be the legacy
  alias itself, which is documented to be a no-op on writes).

### CONTRACT

- After `media_legacy_jobs_reads_total` registers zero sustained reads
  for ≥7 consecutive days AND the canonical observability dashboards
  show no degradation of write-path latencies:
  - `internal/infrastructure/database/sqlite/jobs/legacy.go` deleted.
  - `media_legacy_jobs_reads_total` metric deleted.
  - `cfg.Jobs.LegacyAliasEnabled` config key deleted.
  - `architecture/deprecations.yaml` PR-QUEUE-SPLIT entry status
    flipped to `removed` with the commit verbatim.
  - `migration sqlite_jobs/*.sql` historical record retained in git,
    not deleted (audit trail per godlike/07).

## Trigger conditions (for Option B — re-open Option A)

Any ONE of the following is sufficient to re-open this ADR with Option A
as the chosen branch:

1. **WAL contention empirically observed.** A 7-day rolling window of
   `JobClaimDurationSeconds{quantile="0.99"} > 100ms` observed under
   non-degenerate queue write pressure (≥10 enqueue/s sustained).
2. **PR-B-postgres confirmed landing date > Q4 2026.** Without a firm
   date, the deferral is open-ended; the SEPARATE branch re-evaluation
   should re-open Option A if PR-B-postgres is delayed past the cabal's
   patience.
3. **Future cross-table tx required.** Any feature design that needs
   to atomically update `jobs + <non-queue table>` AND cannot be
   expressed via the canonical outbox pattern (godlike/06 §"Qdrant
   projection" method) must re-open this ADR as Option A, with a
   follow-up ADR for the cross-table feature's specific invariants.
4. **Operator dashboard signal.** A documented operator-observed
   paper cut tied to single-DB contention (e.g. lossless backup
   compression ratio degraded by queue-table churn, WAL file growth
   under disk pressure) escalates to the deciders.

## Consequences (all options)

### Positive

- Decision is explicit and traceable to an ADR.
- Trade-offs are documented once and reused by future maintainers
  re-evaluating the question.
- The composition-root seams (LeaseReaper / JobEventsRetainer /
  QueueNotifier / ProgressSink + `job.JobBroker` port) are documented
  as the canonical split-cost surface; future implementers know what
  they inherit if they pick Option A.

### Negative

- ADR-0002 §D6.6 backlog remains open until bench results land. The "D6 backlog"
  has a half-dozen sister items that may grow stale if the bench window is
  not bounded (trigger #2 above + the bench-results publication as a
  deadline are the mitigations).
- The decider chose Option C against the recommendation (Option B), so
  the bench-driven gate becomes the canonical de-facto decision surface
  instead of the architecture review. Captured in the §"Decider choice"
  section below.

### Neutral

- No production code changes until the decision lands. The State of the
  single-DB is unchanged.
- No CI gate changes until the decision lands.

## Decider choice

**Date:** 2026-06-27.
**Picked:** Option C — SPLIT + BENCH (hybrid).
**Against recommendation:** the recommendation in §"Recommendation" was
Option B (DEFER); the user picked Option C, so the bench-driven gate
becomes the canonical decision surface.

### Why against the recommendation

- The user accepted that PR-B coupling risk exists but chose to get
  empirical data before deferring — the bench answers "is the contention
  real?" before the PR-B PostgreSQL adapter "is the local store obsolete?".
- The feature-flagged EXPAND means zero operator exposure during the
  bench window (`cfg.SplitDBEnabled` defaults `false`).
- The composition-root seam costs (mirrored Builds + bench harness) are
  bounded to ~200 LoC + one bench test file; the option is reversible
  (drop the EXPAND-shape code at the bench-result point if bench says
  no).

### What lands next (the canonical post-decision work order)

1. **PR-Queue-Split-EXPAND** (this option's primary landing):
   - `internal/platform/config/media.go` adds `JobsDBPath`,
     `SplitDBEnabled` (default false), `LegacyAliasEnabled` knobs.
   - `internal/infrastructure/database/storage.OpenSQLiteDB` (or a
     variant op) supports the new path; PRAGMA contract re-applied
     (godlike/06 §"Database rules").
   - `migrations/sqlite_jobs/0XX_*.sql` — fresh ledger, parallel to
     `migrations/sqlite/`. Today the jobs migrations (001, 022, 053,
     …) duplicate under this path; the runner picks which file applies
     based on a per-migration marker in `_migrations` or a parallel
     ledger-table convention.
   - `internal/infrastructure/database/sqlite/jobs/repository.go`
     (or a thin adapter of it) reads from either DB based on the
     feature flag. CUTOVER-mode (the bench's "split" arm) points at
     `jobs.db.sqlite`; CUTOVER-off mode points at `media.db.sqlite`.
   - Legacy alias reader is gated by `LegacyAliasEnabled` and
     unavailable until CUTOVER-mode is selected.
   - Composition-root wiring in
     `internal/app/module_media.go:448` accepts the new `jobsDB` opened
     alongside `mediaDB`. The `*SQLiteStore` is constructed against
     the chosen channel.
2. **PR-Queue-Split-Bench** (this option's empirical gate):
   - `internal/infrastructure/database/sqlite/jobs/bench_db_split_test.go`
     — the bench harness: seed identical workload on both shapes,
     measure `JobClaimDurationSeconds`, `JobProgressEventsTotal`,
     `JobEventsRetentionSweepDuration` under each; report
     effectiveness ratio + tail-latency ratio.
   - `scripts/diagnostics/queue_db_split.sh` (optional) — operator-
     runnable equivalent of the bench harness with a populated DB.
3. **PR-Queue-Split-CUTOVER** gates on bench results:
   - If bench says the split wins (≥+20% throughput OR ≥-30% p99 tail
     latency OR ≥-50% WAL growth), `cfg.Jobs.SplitDBEnabled` flips
     to `default=true`.
   - If bench says no, drop the EXPAND-shape code (CONTRACT-only;
     the backfill/CUTOVER/CONTRACT phases are FORGONE).
4. **PR-Queue-Split-CONTRACT** lands ≥30 days after CUTOVER + zero
   legacy reads for ≥7 days. Same migration-window policy captured
   above for Option A.

### Decision audit

- The two follow-ups in §"Recommendation" (JobClaimDurationSeconds
  histogram + this ADR's TRIGGER CONDITIONS section) are PROMOTED to
  "immediate": the histogram lands in PR-Queue-Split-EXPAND's scope
  (small addition), and trigger-condition #1 becomes the bench's
  "is the split real?" criterion, captured by the histogram's
  post-bench report.
- Option B's "defer entirely" branch is shelved; if the bench
  lands in Option A territory (split wins), Option B's case for
  PR-B-postgres supersession becomes a follow-up ADR rather than
  a state change to this one.

## Implementation status

### This ADR (the deliverable)

- ACCEPTED — Option C (June 2026).
- ADR-0003 is the canonical record. ADR-0002 §D6.6 is superseded once
  this ADR is ACCEPTED.

### PR-Queue-Split-EXPAND (Option C, §Decider choice PR #1) — landing 2026-06-27

Scope: EXPAND only (no CUTOVER). Five of six pieces landed; the sixth
(ClaimDuration observation in `*SQLiteStore.ClaimNext`) deferred to a
follow-up PR that first restores the canonical symbol surface.

Files changed:

| # | File | Change |
|---|------|--------|
| 1 | `migrations/sqlite_jobs/000_initial_jobs_schema.sql` (NEW) | BOOTSTRAP-FRESH migration carrying the canonical final jobs/job_events/dead_letter_jobs shape (9 objects: 3 tables + 6 indexes). |
| 2 | `internal/platform/config/media.go` | `JobsConfig.SplitDBEnabled bool \`yaml:"split_db_enabled"\`` + `JobsDBPath` + `LegacyAliasEnabled` knobs (defaults all OFF / empty). |
| 3 | `internal/infrastructure/observability/metrics.go` | `JobClaimDurationSeconds` histogram registered via `promauto.NewHistogram` with HELP/TYPE + buckets `[0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5]` — the 0.1 bucket is the explicit 100 ms trigger-condition §1 boundary. ALWAYS-ON (NOT gated behind `SplitDBEnabled`). |
| 4 | `internal/app/databases_helpers.go` | `databases.jobs *storage.SQLiteDB` field; `initDatabases` opens it iff `cfg.Jobs.SplitDBEnabled`; `Close()` orders jobs-before-set; `runAllMigrations` runs `migrations/sqlite_jobs/` iff jobs is non-nil; `jobsDBPathFromPrimary` derives sibling path when `JobsDBPath` empty. |
| 5 | `internal/app/composition.go` | `NewComposition` picks `jobsDB := dbs.main` default with `if dbs.jobs != nil { jobsDB = dbs.jobs }` override. `BuildJobsBundle` signature unchanged. |
| 6 | `internal/infrastructure/database/sqlite/jobs/repository_claims.go` | **DEFERRED**. The working tree has a pre-existing inconsistency between `repository.go` (post-PR-Polling notifier design, no `claimMu` field) and `repository_claims.go` (still references `r.claimMu` + the `StartJob` type from `repository_commands.go`, which is not present). Restoring the symbol surface is a separate PR (call it PR-PrePRPolling-Sync or treat as part of an upcoming PR-Polling follow-up). Once `repository_claims.go` is consistent with `repository.go`, the `ClaimDuration` observation edit lands as a tab-complete 2-line change. |

Default behaviour at landing: `SplitDBEnabled=false` means `dbs.jobs == nil`, migration directory is never scanned, `BuildJobsBundle` receives `dbs.main` unchanged — today's production deployments are unaffected. The histogram is registered but emits ZERO samples until the observation site lands; the §Trigger conditions §1 watchpoint remains unobservable but the metric surface is in place for the bench.

Status: APPROVED with the implementer naming corrected (the user-requested config field name is `SplitDBEnabled`; the initial draft used `JobsDBEnabled` and was renamed via sed before merge). The deferred sixth piece is documented here so a future maintainer picks it up cleanly.

### Option B (if accepted)

- Zero code changes.
- This ADR's ADR-0002 §D6.6 backlog annotation:
  - `architecture/decisions/0002-p2-p3-roadmap.md:87` — append a
    supersession note pointing back to ADR-0003.
  - `architecture/decisions/0002-p2-p3-roadmap.md:239` — flip the
    row's status from "P2 bridging" to "DEFERRED — superseded by
    ADR-0003 (PR-B)". The PR-Queue-Split row is retained as a git
    record; the canonical eval lives here.
- Add `JobClaimDurationSeconds` histogram to
  `internal/infrastructure/observability/metrics.go`. Small PR (~10
  LoC + 1 registration call). Triggers Option A re-evaluation when
  the histogram breaches the trigger condition.

### Option A (if accepted)

- Lands as PR-Queue-Split proper, hitting the migration-window policy
  captured above.
- Tracking entry: `architecture/decisions/0002-p2-p3-roadmap.md:239`
  updates from "D6.6 backlog" to "D6.6 DONE — kept in evidence
  table as PR-QUEUE-SPLIT."

### Option C (if accepted)

- Lands as PR-Queue-Split (Option A) + PR-Queue-Split-Bench (the
  bench+configuration shape). The bench lives in
  `internal/infrastructure/database/sqlite/jobs/bench_db_split_test.go`
  (or similar) with seed → measure → re-seed on each shape. Published
  results in `architecture/decisions/bench-results/queue-db-split-2026q3.md`.

## References

- ADR-0002 §D6.1 (PR-Reaper), §D6.3 (PR-Retention), §D6.4 (PR-Progress),
  §D6.5 (PR-Polling) — sister P2 backlog items, demonstrating the
  adapter-surface pattern (local interfaces + JobBroker port) that
  Option A inherits.
- `docs/architecture/godlike/06_DATA_AND_CONFIG_OWNERSHIP.md` §"Database rules"
  — driver lock + FTS5 ban + schema boundaries.
- `docs/architecture/godlike/07_ZERO_LEGACY_POLICY.md` §"Migration
  sequence" — EXPAND/BACKFILL/CUTOVER/CONTRACT.
- `architecture/decisions/0002-p2-p3-roadmap.md:201` — composition-root
  seam marker for the future PR-postgres rewrite.
- `internal/app/module_media.go:448` — current
  `Repo *sqljobs.SQLiteStore` composition-root site (the single future
  rewiring point per PR-postgres).
- PR2.6 commits in the Wave 14–18 closure (June 2026) — the
  consolidation wave that removed `artlist.db.sqlite` and the legacy
  `<DataDir>/media.db.sqlite` path in favour of
  `<DataDir>/media/media.db.sqlite`. Inversion of this direction is
  not free.
