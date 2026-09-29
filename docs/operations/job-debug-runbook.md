# Job debug runbook

Per-job SQLite root-cause recipe for diagnosing `jobs` rows stuck in
`RETRY_WAIT` (or any other terminal / pending state with a suspect
error) directly via the canonical state store — bypassing the HTTP
admin API when it's unreachable or auth-blocked. Read-only by design.

godlike/07 NO-FAKE-AVAILABILITY: this runbook NEVER mutates
`./data/media/media.db.sqlite`. Every `sqlite3` invocation uses the
read-only URI form `file:./data/media/media.db.sqlite?mode=ro`
(equivalent CLI flag: `-readonly`). Reject any `INSERT / UPDATE /
DELETE / PRAGMA writable_schema / PRAGMA journal_mode` even on a copy
of the file — debugging is observation, not mutation.

## §0 — When to use this runbook

- The job is in `RETRY_WAIT`, `FAILED`, or `CANCELLED`, and the HTTP
  admin probe (`GET /api/jobs/:id/full`) returns 401 or stale data
  (token-rotation-in-flight, server binary not yet restarted — see the
  rotation procedure in `AGENTS.md`, § Authentication SSOT).
- The error message of a media-pipeline job reads as opaque
  (`discovery failed`, `no candidates found`) and the in-process
  finalizer log is unavailable (`journalctl` rolled, or the server
  binary is not currently running).
- A retention sweep or worker-side race obscured the cause — the
  canonical SQLite state is the only surviving ground truth.

## §1 — Pre-flight

```bash
DB='./data/media/media.db.sqlite'
test -f "$DB" || { echo "[FATAL] $DB not present"; exit 2; }
command -v sqlite3 >/dev/null || { echo "[FATAL] sqlite3 not on PATH"; exit 2; }
sqlite3 --version
```

If `sqlite3 --version` is not present (slim operator image), install
via the package manager. **Do not** substitute with a non-`sqlite3`
client (e.g. `python -m sqlite3`) — the CLI URI-mode form is the
canonical lockstep surface used in this runbook.

## §2 — Schema discovery (always first — column names may differ)

```bash
sqlite3 -readonly -header -column "file:$DB?mode=ro" \
  "SELECT name, type, [notnull], dflt_value, pk \
   FROM pragma_table_info('jobs') ORDER BY pk, cid;"

sqlite3 -readonly -header -column "file:$DB?mode=ro" \
  "SELECT name, type, [notnull], dflt_value, pk \
   FROM pragma_table_info('job_events') ORDER BY pk, cid;"
```

Pinned facts (per `migrations/sqlite/001_velox_core.sql:193`, the
canonical SQLite migration):

| Table        | Error-bearing column                | Status column                          | Id column | Timestamps                |
|--------------|-------------------------------------|----------------------------------------|-----------|---------------------------|
| `jobs`       | `error` (NOT `last_error`)          | `status`                               | `id`      | `created_at`, `updated_at`|
| `job_events` | `message` (text) + `data_json` (structured) | — (event class lives in `type`) | `id`      | `created_at`              |

> **Do not assume** `last_error` on `jobs` — the canonical column name
> is `error`. **Do not assume** an `error` column on `job_events` — the
> structured field is `data_json` and the human-readable text is
> `message`.

## §3 — Step 1: pull the `jobs` row

```bash
sqlite3 -readonly -header -column "file:$DB?mode=ro" <<'SQL'
SELECT
  id,
  type,
  status,
  COALESCE(worker_id, '')            AS worker_id,
  COALESCE(lease_id, '')             AS lease_id,
  COALESCE(lease_expiry, '')         AS lease_expiry,
  retry_count,
  COALESCE(max_retries, '')          AS max_retries,
  COALESCE(revision, '')             AS revision,
  COALESCE(correlation_id, '')       AS correlation_id,
  COALESCE(error, '')                AS error,
  created_at,
  updated_at
FROM jobs
WHERE id = 'job_1783924561995565623_559b55fa';
SQL
```

`jobs.error` is the canonical final-state error string (per
`internal/kernel/job/finalize_commands.go:226` — "jobs.error AND
job_events.message"). It is the first place to look for the
RETRY_WAIT cause.

## §4 — Step 2: pull the chronological `job_events` timeline

```bash
sqlite3 -readonly -header -column "file:$DB?mode=ro" <<'SQL'
SELECT id, job_id, type, message, data_json, created_at
FROM job_events
WHERE job_id = 'job_1783924561995565623_559b55fa'
ORDER BY datetime(created_at) ASC;
SQL
```

For each row, also `jq`-parse the structured `data_json` so an
embedded `.error` / `.cause` / `.detail` field surfaces on one line:

```bash
sqlite3 -readonly -json "file:$DB?mode=ro" <<'SQL' > /tmp/job_debug_events.json
SELECT id, job_id, type, message, data_json, created_at
FROM job_events
WHERE job_id = 'job_1783924561995565623_559b55fa'
ORDER BY datetime(created_at) ASC;
SQL

jq -r '
  .[]
  | "[" + .created_at + "] "
    + .type
    + " | message=" + (.message | @json)
    + " | data_json="
    + ((try (.data_json | fromjson | .error // .cause // .detail // "-")
         catch "(unparseable)") | @json)
' /tmp/job_debug_events.json
```

`job_events.type` taxonomy (per `internal/kernel/job/job.go`,
aliased to `kerneljob.StatusRetryWait`):

| `job_events.type`  | Meaning                                                                  |
|--------------------|--------------------------------------------------------------------------|
| `job_running`      | Worker picked the job up                                                  |
| `leased`           | Worker-specific lease ack                                                 |
| `error`            | Stage-level error (e.g. Artlist discovery failed)                         |
| `job_retry_wait`   | Finalizer classified the error as `RETRY_WAIT`                            |
| `job_failed`       | Terminal `FAILED` (retry_count ≥ max_retries, OR classifier)               |
| `job_completed`    | Terminal `SUCCEEDED`                                                      |

## §5 — Step 3: cross-correlate sibling tables (best-effort)

Sibling tables may exist in your build (`dead_letter_jobs`,
`job_attempts`, `job_retries`, `job_artifacts`, `outbox_events`,
`media_assets`); enumerate them and pull any rows referencing this
job:

```bash
for tbl in dead_letter_jobs job_attempts job_retries job_artifacts outbox_events media_assets; do
  if sqlite3 -readonly "file:$DB?mode=ro" \
       "SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type='table' AND name='$tbl');" \
       | grep -q 1; then
    echo "-- $tbl rows referencing job_id --"
    sqlite3 -readonly -header -column "file:$DB?mode=ro" \
      "SELECT * FROM $tbl WHERE job_id='job_1783924561995565623_559b55fa' LIMIT 20;" \
      || true
  fi
done
```

`outbox_events` and `media_assets` typically reference the
`correlation_id` (not `job_id`). If §3 yielded a correlation_id,
pull its cross-references too:

```bash
COR=$(sqlite3 -readonly "file:$DB?mode=ro" \
  "SELECT correlation_id FROM jobs WHERE id='job_1783924561995565623_559b55fa';")
if [[ -n "$COR" ]]; then
  for tbl in outbox_events media_assets; do
    if sqlite3 -readonly "file:$DB?mode=ro" \
         "SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type='table' AND name='$tbl');" \
         | grep -q 1; then
      echo "-- $tbl rows WHERE correlation_id='$COR' --"
      sqlite3 -readonly -header -column "file:$DB?mode=ro" \
        "SELECT * FROM $tbl WHERE correlation_id='$COR' LIMIT 5;" \
        || true
    fi
  done
fi
```

## §6 — Worked example: `job_1783924561995565623_559b55fa`

`jobs` row (verbatim from `./data/media/media.db.sqlite` via §3):

```
id                      job_1783924561995565623_559b55fa
type                    media.artlist
status                  CANCELLED          (final state, retry exhausted)
retry_count             1                  (out of originally max_retries)
error                   no candidates found (canonical jobs.error column)
worker_id               YOutube_626773_worker-5
correlation_id          20260713-063601-5c84c41b7e27c2e5
created_at              2026-07-13T06:36:01Z
updated_at              2026-07-13T06:36:02Z
```

Chronological `job_events` (verbatim, via §4):

| created_at             | type             | message                                       | data_json                            |
|------------------------|------------------|-----------------------------------------------|--------------------------------------|
| 2026-07-13T06:36:01Z   | `job_running`    | `job.Job started`                             | `{}`                                 |
| 2026-07-13T06:36:02Z   | `leased`         | `job claimed by worker YOutube_626773_worker-5` | `{}`                              |
| 2026-07-13T06:36:02Z   | `error`          | `artlist run failed`                          | `{"error":"no candidates found"}`    |
| 2026-07-13T06:36:02Z   | `job_retry_wait` | `job.Job scheduled for retry`                 | `{"error":"no candidates found"}`    |

**Root-cause verdict:**

The job was leased at `06:36:02Z` by worker `YOutube_626773_worker-5`.
The Artlist discovery stage logged an `error` event whose `data_json`
carried `{"error":"no candidates found"}`. The finalizer then emitted
`job_retry_wait` with the same payload — the canonical mapping is
`stageDiscoverClips → resp.Error = "no candidates found"` (literal
per `internal/capabilities/assets/providers/artlist/run_orchestrator_stages.go:52`).

The full retry path (classification → RETRY_WAIT → retry attempt →
terminal CANCELLED) is sourced from `internal/kernel/job/finalize_commands.go`
and `internal/kernel/job/job.go` (`StatusRetryWait`).

Operator-facing next steps (CROSS-REFERENCE — this runbook is
diagnostic-only, not an action plan):

- Inspect the Artlist searcher chain produced by
  `internal/capabilities/assets/providers/artlist/search_core.go::buildSearcherChain`;
  confirm the search term yields ≥1 candidate against the configured
  provider precedence.
- Verify the live scraper reachability via `make auth-check` (the canonical
  fail-closed credential probe, which already signs a live `/api/artlist/...`
  request) and `make doctor`. The artlist-live recipe that used to live in
  `docs/operations/stock-e2e-runbook.md` §11 was retired together with its
  driver (commit `7e6965aab`); it is not documented anywhere today.
- Verify the live scraper connects within `SCRAPER_CONNECT_TIMEOUT_SECONDS=5`
  AND responds within `SCROLL_TIMEOUT=120` (per the `fix(scraper)`
  series — commits `9b7a60ffa` / `ee97a769a` / `9646f1077` / `f5a3dc9c5`).

## §7 — Lockstep cross-references (godlike/06 SSOT)

| Fact                                                             | Canonical reference                                                   |
|------------------------------------------------------------------|----------------------------------------------------------------------|
| `jobs` and `job_events` table schemas                            | `migrations/sqlite/001_velox_core.sql:193`                           |
| Canonical SELECT projection for `job_events`                     | `internal/platform/sqlite/jobs/repository_events.go`  |
| Job INSERTs (status / error / retry_count writers)               | `internal/platform/sqlite/jobs/finalize_attempt.go` + `internal/platform/sqlite/jobs/lifecycle_complete.go` + `internal/platform/sqlite/jobs/lifecycle_finalize.go` + `internal/platform/sqlite/jobs/lifecycle_aggregation.go` + `internal/platform/sqlite/jobs/lifecycle_progress.go` + `internal/platform/sqlite/jobs/repository_claims.go` + `internal/capabilities/jobs/finalize/job_finalizer.go` + `internal/capabilities/jobs/worker_finalize_paths.go` (lines 108, 138) |
| Finalizer rows-error mapping (`jobs.error` ← `job_events.message`)| `internal/kernel/job/finalize_commands.go:226`                     |
| `"no candidates found"` literal origin                            | `internal/capabilities/assets/providers/artlist/run_orchestrator_stages.go:52` |
| `stageDiscoverClips` function entry                              | `internal/capabilities/assets/providers/artlist/run_orchestrator_stages.go:44` |
| `StatusRetryWait` definition                                     | `internal/kernel/job/job.go` → `kerneljob.StatusRetryWait`         |
| `StatusSucceeded` / `StatusFailed` enum split                    | `internal/kernel/job/job.go`                               |

A rewrite of any of these canonical references (column rename,
literal rename, schema-creator split) MUST update both the code and
this §7 table in lockstep. The drift-detection grep in §8 is the
operator-runnable guard for this lockstep.

## §8 — Drift-detection grep (operator-runnable, pure shell)

```bash
{
  echo '-- canonical jobs + job_events schema owner --'
  grep -nE 'CREATE TABLE IF NOT EXISTS (jobs|job_events)\b' \
    migrations/sqlite/*.sql
  echo
  echo '-- canonical job_events INSERT writers (BOTH dirs; per godlike/06 SSOT lockstep) --'
  grep -rnE 'INSERT INTO job_events\b' \
    internal/platform/sqlite/jobs/ \
    internal/capabilities/jobs/ \
    | grep -v _test.go
  echo
  echo '-- canonical jobs.error writer --'
  grep -rnE 'UPDATE jobs SET error\b|jobs\.error\s*=' \
    internal/platform/sqlite/jobs/ internal/kernel/job/ \
    | grep -v _test.go
  echo
  echo '-- canonical "no candidates found" literal --'
  grep -rnE '"no candidates found"' \
    internal/capabilities/assets/providers/artlist/
  echo
  echo '-- runbook §7 lockstep references MUST resolve to real files --'
  for f in \
    migrations/sqlite/001_velox_core.sql \
    internal/platform/sqlite/jobs/repository_events.go \
    internal/platform/sqlite/jobs/finalize_attempt.go \
    internal/kernel/job/finalize_commands.go \
    internal/capabilities/assets/providers/artlist/run_orchestrator_stages.go \
    internal/kernel/job/job.go; do
    if [[ -f "$f" ]]; then
      echo "[OK]  $f"
    else
      echo "[FAIL] $f MISSING — update runbook §7 lockstep table"
    fi
  done
}
```

Failure modes the grep surfaces:

- A blank `"no candidates found"` hit (literal renamed / file moved)
  then this runbook's §6 verdict and §7 lockstep row are stale;
  update both atomically per godlike/06 SSOT.
- A new `CREATE TABLE IF NOT EXISTS jobs|job_events` in any other
  migration file (rare — would indicate the schema is being shadowed
  by a parallel migration owner); resolve by domain-driven-design
  ownership clarification (canonical = `001_velox_core.sql`).
- A missing canonical file in the §7 row list indicates an upstream
  rename; search `git log --diff-filter=R -- "$f"` for the rename
  target and update §7 + §8 atomically.

## §9 — Deferred scheduling + per-stage status (the 5000 job/day lane)

A job submitted with a future start time is persisted as `SCHEDULED` —
a real broker status — and is **not claimable**: the claim query
(`repository_claims.go`) only ever selects `QUEUED`, and the lease reaper
only touches `LEASED`/`RUNNING`. The scheduler is the ONLY writer of the
`SCHEDULED → QUEUED` promotion, so scheduling never races execution.

Canonical owners (godlike/06 SSOT — update these together):

| Fact | Owner |
| --- | --- |
| Ports (`ScheduleStore`, `JobStageStatusStore`, `JobStageStatus`) | `internal/kernel/job/schedule.go` |
| `SCHEDULED` status semantics | `internal/kernel/job/job.go` |
| Admission policy (quota + concurrency) | `internal/capabilities/jobs/scheduling/admission.go` |
| Promotion loop | `internal/capabilities/jobs/scheduling/scheduler.go` |
| Tables | `migrations/sqlite_jobs/005_job_scheduling.sql` |
| Persistence | `internal/platform/sqlite/jobs/job_scheduling_store.go` |
| Startup step | `internal/app/wiring/lifecycle_job_runner.go::buildJobSchedulerStep` |

### Submitting a day of work

```bash
# Single deferred job (any type the catalog accepts).
curl -sS -X POST "$API/api/jobs" -H 'Content-Type: application/json' \
  -d '{"type":"video.create","payload":{"...":"..."},"scheduled_at":"2026-10-02T16:00:00Z","idempotency_key":"day1-item1"}'

# Batch (up to 500 items = 10 requests for a 5000-job day). Fail-soft per item:
# the response is 202 with the per-item outcome, so one malformed entry never
# rejects the other 499.
curl -sS -X POST "$API/api/jobs/schedule" -H 'Content-Type: application/json' \
  -d '{"jobs":[{"type":"video.create","scheduled_at":"2026-10-02T16:00:00Z","idempotency_key":"day1-item1"}]}'
```

Idempotency is unchanged from the immediate path: replaying the same
`active_key`, `correlation_id`, or (`client_id`, `idempotency_key`) returns the
**same** `job_id` — a retried batch is deduped, never duplicated.

### Admission: quota + lane

| Knob | Default | Meaning |
| --- | --- | --- |
| `VELOX_SCHEDULER_DAILY_QUOTA` | `5000` | Promotions per UTC day; `0` = unbounded. The 5001st due job stays `SCHEDULED` and rolls into the next day |
| `VELOX_SCHEDULER_MAX_CONCURRENT` | `0` | In-flight (`LEASED`/`RUNNING`/`FINALIZING`) cap at admission; `0` = unbounded. **Set `1` for a strictly serial "one behind the other" lane** |
| `VELOX_SCHEDULER_INTERVAL` | `30s` | Base cadence of the promotion loop |

A due job blocked by either ceiling is **deferred, not lost**: it keeps its
`SCHEDULED` row and its FIFO position (`ORDER BY run_at`, oldest first).

### Inspecting the backlog

```bash
curl -sS "$API/api/jobs/scheduled?limit=200"   # job_id, job_type, status, run_at, due

DB="$HOME/.local/state/pipelinegen/jobs/jobs.db.sqlite"
sqlite3 "$DB" "SELECT s.job_id, s.run_at, j.type, j.status FROM job_schedules s JOIN jobs j ON j.id = s.job_id ORDER BY s.run_at LIMIT 20;"
sqlite3 "$DB" "SELECT day, promoted FROM job_scheduler_counters ORDER BY day DESC LIMIT 7;"
sqlite3 "$DB" "SELECT COUNT(*) FROM jobs WHERE status = 'SCHEDULED';"
```

### Per-stage sub-status

The durable per-(job, stage) projection reuses the canonical workflow
vocabulary (`internal/kernel/job/stage_progress.go`): stages
`script | clips | stock | translation | voiceover | overlay | render | upload |
persistence`, statuses `queued | running | completed | failed | skipped`. A
stage name outside that set is rejected — the API cannot advertise progress
nobody produces.

```bash
curl -sS -X PATCH "$API/api/jobs/$JOB_ID/stages/overlay" \
  -H 'Content-Type: application/json' -d '{"status":"completed"}'   # progress defaults to 100
curl -sS "$API/api/jobs/$JOB_ID/stages"
```

#### Producers: the table is written by the work, not by hand

`video.create` is a PRODUCER of this table. Its coordinator emits one row per
step transition (`running` at the step's band-start, then `completed` /
`skipped` / `failed` with the failure reason), so a render populates its own
stage rows and PATCH is only needed for out-of-band reporting.

Canonical owner: `internal/capabilities/videocreate/stage_status.go`
(`stepCanonicalStage` = the ONE step → canonical-stage mapping). The mapping is
MANY-TO-ONE because the kernel vocabulary is coarser than the workflow ladder;
each canonical stage row always shows the LATEST producing step:

| video.create step | canonical stage | why |
| --- | --- | --- |
| `01_script` | `script` | the script + scene plan IS the script stage |
| `02_media_search` | `stock` | discovery searches the stock/asset plane |
| `03_media_acquire` | `clips` | download + cut + normalize + register |
| `04_voiceover` | `voiceover` | the voiceover stage proper |
| `05_audio_master` | `voiceover` | the audio master belongs to that stage |
| `06_overlay_plan` | `overlay` | the overlay layer plan |
| `07_render` | `render` | localized render fan-out |
| `08_assemble` | `render` | assembly completes the rendered artifact |
| `09_audio_mux` | `voiceover` | the final mux concludes the audio |
| `10_verify` | `persistence` | ffprobe + SHA-256 = the artifact's durable proof |
| `11_publish` | `upload` | Drive publication + media-registry registration |

`translation` is absent on purpose: no video.create step translates (that is a
child-job projection owned by the script capability), and a row for work nobody
performs would be fake availability.

The emission is **fail-soft** by contract: a locked database or a cancelled
context logs a warning and never fails the render. A resumed job re-emits the
rows of the steps that were already durable (without touching the progress
bar), so a restarted render still shows a complete table instead of only the
stages that ran after the restart.

### Failure modes

- **503 on every scheduling route** — the wired broker has no scheduling plane
  (e.g. the in-process `platform/jobs/local` broker). The surface degrades
explicitly instead of panicking or pretending success.
- **Typed fail-closed enqueue** — a future `scheduled_at` on a broker without a
  `ScheduleStore` returns `job %q requested scheduled_at=... but the schedule
  store is not wired`; it does NOT silently run the job early.
- **Cancelling a scheduled job** — `POST /api/jobs/{id}/cancel` succeeds on
  `SCHEDULED`, the promotion CAS loses, and the `job_schedules` row is dropped
  (no orphaned schedule keeps a cancelled row in the due set forever).
- **Promotion evidence** — a successful promotion writes a `job_queued` event,
  so §4's timeline shows exactly when the scheduler released the job.

## §10 — Remote final-job handoff (PREPARE → FINALIZE → poll)

A `script.generate` run with `final_job=true` ends by handing its compiled media
plan to the Master (`creator-77`), which renders the final video. That handoff is
a three-phase job, and the phase boundaries are what make the wait resumable:

| phase | transport call | owner |
|---|---|---|
| PREPARE | `POST /api/v1/jobs/pre` → `job_id` | the submitting attempt |
| FINALIZE | `POST /api/v1/jobs/{id}/finalize` | the submitting attempt |
| poll | `GET /api/v1/jobs/{id}` (one read = one decision) | whoever holds the id |

The id is the only address of a render that outlives this process, so it is kept
between PREPARE and the wait and **persisted in the run's durable result**
(`result.remote_final_job`) before an attempt ends. The attach decision reads
that persisted receipt, not just the in-flight result: an attempt that rebuilt
its result (a replay, or a fresh process resuming the run) still finds the
handle instead of submitting a second render.

### The run-level hand-off contract

This is the RUN-level hand-off (the run's retry machinery). It is a different
mechanism from the JOB-level deferral in §11: here the run fails with
`REMOTE_RENDER_PENDING` and is retried, because `script.generate` is the job.

- A wait that outlives the attempt's budget is **not a failure**. The attempt
  writes the receipt (job id, raw status, `yields`) and ends with the run's
  `error_code = REMOTE_RENDER_PENDING` and a scheduled retry.
- The attempt that follows **attaches** to that job id — no second PREPARE, so
  the Master is never asked to render the same work twice.
- The wait is handed back **at most once per run**: the first wait is bounded by
  `VELOX_FINAL_JOB_ATTACH_SECONDS` (default `60`, chosen above the observed
  40-60s render), and every wait after it runs to completion with the client's
  full 2h poll timeout. A render longer than the whole retry window therefore
  still finishes instead of dying on an exhausted attempt budget.
- `VELOX_FINAL_JOB_ATTACH_SECONDS=0` disables the hand-off entirely and restores
  the blocking wait. A malformed value is a configuration error: the composition
  root fails closed instead of silently falling back.
- A receipt already `SUCCEEDED` (a later phase failed the run) is reused as-is —
  the step is a no-op, not a resubmit.

### Triage

- **`REMOTE_RENDER_PENDING`** — expected while a render is in flight. Look at
  `remote_final_job.job_id` in the run's result, and `yields`: `1` means one
  attempt already handed the wait back. It becomes an incident only if the code
  repeats with no status change on the Master side.
- **last attempt blocks** — with `yields >= 1` the retry deliberately waits to
  completion. A worker sitting on this stage for minutes is the designed
  behaviour for that single attempt, not a leak.
- **`cannot resume it` failure** — a pending receipt exists but the wired
  submitter does not implement the attach half of the port. The run fails
  closed on purpose: the alternative is submitting a duplicate render.
- **`remote final job ... ended FAILED`** — a real Master-side failure; it is
  reported as `PUBLISHING_DOCUMENTS_FAILED` (or `PROVIDER_*` per §3's heuristics),
  never as a deferral.

## §11 — Non-consuming deferral: a WAIT is not a retry

Background work regularly waits on something outside the jobs plane: a remote
render that is still running, a provider window that has not opened. The job
plane's retry budget used to be the only vocabulary for that, and it is the
wrong one — a retry means "the work failed, do it again", so charging a wait to
that budget turns a long external wait into a terminal `FAILED` job
(`retry_count == max_retries` while the remote work is still alive).

A **deferral** is the successor state: the handler hands the attempt back while
stating the fact, and the row returns to `RETRY_WAIT` with `retry_count`
**unchanged**.

| layer | owner | what it does |
|---|---|---|
| contract | `internal/kernel/job/deferral.go` | `job.Deferred(reason)` / `job.DeferredAfter(delay, reason)`; `job.AsDeferral(err)` reads it back through any `%w` envelope |
| outcome | `kernel/job.FinalizeAttemptCommand.Outcome = OutcomeDeferred` | the canonical finalize verb; lands non-terminal, never downgraded to `FAILED` (`sqlite/jobs/finalize_policy.go`) |
| row | `jobs.deferred_until` + `idx_jobs_deferred_until` (migration `006`) | the instant the row may be re-dispatched. `NULL` is meaningful: a plain retry keeps its `retry_count`-derived backoff |
| dispatch | `capabilities/jobs/scheduling.RetryDue` | a deferral is due at `deferred_until`; without one, `DefaultDeferralDelay` applies from the row's last update |
| worker | `Worker.finalizeJobDeferral` | checked **before** any failure logging and before `retry.IsTransient` / `DecideRetry`, so an error that is both transient and a deferral defers |

Guarantees, in the order a reviewer should check them:

1. **No retry is spent.** `decideFinalizeAttempt` does not increment `retry_count`
   for `OutcomeDeferred`, and the deferral gate runs ahead of the retry-budget
   gate — a deferral at `retry_count == max_retries` still waits.
2. **A deferral is never terminal.** There is no budget to exhaust, so it is
   never rewritten to `FAILED` and never dead-lettered.
3. **The delay is stated once.** A handler states the fact; the worker fills the
   deployment default (`jobscheduling.DefaultDeferralDelay`) when the handler did
   not name a cadence. The store rejects a deferral with no instant at all
   (`ErrFinalizeAttemptDeferralDelayMissing`), fail-closed, so a deferral can
   never park a row in `RETRY_WAIT` forever.
4. **A failed deferral write is loud, never a completion.** On a lost lease or a
   store error the row keeps its lease until it expires, the reaper requeues it,
   and the handler simply runs again.
5. **`Retry` clears the hint.** Leaving `RETRY_WAIT` (`scheduling.Retry`) sets
   `deferred_until = NULL`, so no stale instant sits on a `QUEUED` row.

### The script.generate durable run (the second production user)

A `script.generate` job runs a durable RUN inside itself, and that run has its
OWN retry schedule (`attempt_count` / `next_retry_at`, `scriptgen.MaxRetries`
= 3). The job attempt is what drives it: nothing else resumes a failed run
(`ShouldRetry` has no production caller by design).

So when the run fails with a retry still scheduled — the observed case is
`PUBLISHING_DOCUMENTS` waiting on the Master's final job — the handler does NOT
fail the job. It returns `job.DeferredAfter(delay, …)` where `delay` is the wait
until `next_retry_at`, which means:

- the job goes to `RETRY_WAIT` with **`retry_count` unchanged**, emits
  `job_deferred`, and is re-dispatched by the requeue sweep at that instant;
- the next attempt re-enters `ExecuteWithContext`, which resumes the run from
  its checkpoint (and, with the durable final-job receipt, ATTACHES to the
  remote render instead of asking the Master for a second one);
- the loop is bounded by the RUN's budget: once the run writes no
  `next_retry_at` (attempt budget spent), the failure is terminal for both.

Observed live on 2026-09-28 (real 5-scene generate): `RUNNING → job_deferred
(delay_ms=4929, run attempt 1/3) → job_queued → RUNNING`, with the job row
sitting at `retry_count = 0/3` throughout — the wait cost the job nothing.

Triage: `job_deferred` on a `script.generate` names the run, the failed stage,
the run's attempt count and the instant it comes back. If those events repeat
more than three times, the run's budget is not being written (check that
`failRunWithRetry` persists `next_retry_at`).

### The clip.render settle hand-off (the first production user)

`clip.render` submits a remote render and then waits for it. That wait is now
bounded per attempt and handed back as a deferral instead of parking a worker
lane for the whole render:

| knob | default | meaning |
|---|---|---|
| `VELOX_CLIP_RENDER_SETTLE_WAIT_SECONDS` | `60` | how long ONE settle attempt holds its lane. `0` disables the hand-off: every settle waits the render out (the pre-deferral behaviour). Malformed values fail closed. |
| `VELOX_CLIP_RENDER_SETTLE_WINDOW_SECONDS` | `1200` | how long a settle continuation may keep asking (one bounded attempt per dispatch). Past it the settle waits the render out in a single attempt — the loop guard. |
| `VELOX_CLIP_RENDER_SETTLE_WORKERS` | `16` | the dedicated lane pool for settle continuations (see §9) |

The deferral delay IS the budget the attempt was willing to wait, so the row
comes back on the cadence the operator configured. Bounded by construction: the
`SettleWithin` boundary returns `cliprender.ErrRenderPending` for a render that
is merely **not terminal yet** — a render that FAILED, an unreachable queue, and
a failed certification still propagate as real errors.

Cost trade-off, stated honestly: the default budget deliberately sits above the
observed short-clip render, so the common case still finishes in a single
attempt with no added latency. A render that outlives the budget costs one
extra dispatch plus the deferral delay — and buys back the lane. Going
aggressive (e.g. `30`) trades lane occupancy for more attempts and more churn.

### Triage

A deferred job looks like this — `RETRY_WAIT`, `retry_count` NOT climbing, and a
reason on the row:

```sql
SELECT id, type, status, retry_count, deferred_until, error
  FROM jobs WHERE status = 'RETRY_WAIT' ORDER BY deferred_until LIMIT 20;
SELECT job_id, type, created_at FROM job_events
 WHERE type = 'job_deferred' ORDER BY created_at DESC LIMIT 20;
```

- **`RETRY_WAIT` with `retry_count` flat across several dispatches** — the
  designed state for a wait. `deferred_until` says when it comes back.
- **`deferred_until` in the past and the row still `RETRY_WAIT`** — the requeue
  sweep has not run (worker down, or the sweep interval is longer than the
  delay). Not a stuck job.
- **Repeated `job_deferred` for `clip.render` with no status change** — the
  remote render is genuinely long or wedged. Check the render queue directly and
  the `render_job_id` on the job; the settle keeps asking until the settle
  window, then waits the render out.
- **`WorkerJobDeferredTotal` climbing per type** — the metric
  (`platform/observability/worker_metrics.go`) is labelled by job type and final
  status, which is how a wait on one surface is separated from a wait loop.
- **Disable the hand-off for a bisect** — set
  `VELOX_CLIP_RENDER_SETTLE_WAIT_SECONDS=0` (and restart): settles block again,
  and no row is deferred. If the symptom persists, the deferral is not the
  cause.
- **A job bouncing between `RETRY_WAIT` and `RUNNING` every few seconds, with
  `revision` climbing and `retry_count` flat at `0/N` — the deferral hot loop.**
  Observed live on 2026-09-28 (`script.generate`, `revision` 277 in ~9 minutes,
  Ollama answering 404): the run fails at `GENERATING_SCENE_TEXT` — before any
  resumable state exists — and each dispatch starts a NEW run with a fresh
  attempt counter, so the run-level budget never exhausts and `pendingRunRetry`
  keeps answering "still waiting". The loop is bounded by the RUN's budget only
  while the same run is resumed; a fresh run per dispatch resets it.
  ```sql
  SELECT status, retry_count, revision, updated_at FROM jobs WHERE id='<job_id>';
  SELECT created_at, type, substr(message,1,80) FROM job_events
   WHERE job_id='<job_id>' AND type IN ('job_deferred','job_queued') ORDER BY created_at;
  ```
  Stop it with `POST /api/jobs/<job_id>/cancel` (admin bearer), fix the
  underlying stage failure, then resubmit. Do NOT widen the deferral window as a
  workaround: the loop is a missing run-identity bound, not a timing problem.

## §12 — Schema drift on an already-migrated database (added 2026-09-28)

A migration recorded as APPLIED proves the FILE ran. It does not prove the
OBJECT survived. The ledger is skipped by version and its checksum only guards
the file, so a table that an applied migration declares can be missing while
every check stays green. Two real shapes, both seen on 2026-09-28:

| shape | how it looks | what repairs it |
|---|---|---|
| the object is gone | `job_checkpoints` absent, ledger row for `216` present with a byte-matching checksum, every checkpoint write failing with `no such table` | a NEW numbered migration (`272`) |
| the object exists but an index silently skipped | the index name exists — on a QUARANTINED copy of the old table (`legacy_job_checkpoints`), because SQLite keeps index names unique per DATABASE and `CREATE INDEX IF NOT EXISTS` is then a no-op | a NEW numbered migration (`273`) |

### The boot-time check

After the apply loop, `platform/sqlite/migrations_verify.go`
(`verifyDeclaredTables`) walks every in-scope APPLIED migration and asserts each
declared table and index exists — and, for indexes, that the declared name is
owned by the table that declared it. It is non-fatal by design: a drifted
database must still boot so the operator can apply the forward migration.

```bash
journalctl -u pipelinegen --since "10 minutes ago" | grep -E "declares a (table|index)"
```

Each line carries `declared_by`, `version`, `target_db` and the remedy. Two
fields deserve attention: `declared_by` is the NEWEST numbered migration that
promised the object (the actionable file — never the 267-file baseline), and for
indexes `live_owner` names the table that actually holds the name.

### What is NOT drift

The check excludes only four things (see `migrations_verify.go`):

1. a later in-scope migration removes the object after its last declaration
   (order-aware, so rebuilds and re-created tables are not written off);
2. a name on `executionPlaneArchivedTables` — the execution-plane tables the
   data-plane archival documented by migration `265` may have moved to the jobs
   database. **`job_checkpoints` is deliberately not on that list**: it is
   declared by both planes, but the primary runtime still opens the primary copy
   (the durable checkpoint resolver), so its absence is drift;
3. an applied ledger row whose file is gone from the corpus and whose recorded
   filename names a drop of the object (`253_drop_assembly_sessions.sql`);
4. the object is present (trivially).

### Triage

- **A table is reported and you do not recognise it** — query the live schema
  and the ledger before touching anything:
  ```sql
  SELECT name FROM sqlite_master WHERE type='table' ORDER BY name;                 -- live
  SELECT version, filename, applied_at FROM schema_migrations ORDER BY version;    -- claims
  ```
  A `legacy_<name>` twin means the out-of-band archival ran; a sibling plane
  (`migrations/sqlite_jobs/`) declaring the same name means the object moved.
- **An index is reported with a `live_owner` you do not expect** — this is the
  silent no-op. The declared table has no access path even though the name
exists, so plans regress without a single error being logged anywhere.
- **Never edit the applied file.** The runner skips it by version and rejects a
  changed checksum, so the fix would be invisible on exactly the databases that
  need it. Always add the next number.
- **Adding a name to `executionPlaneArchivedTables`** is a claim that no code
  reads the primary copy any more. `TestSanctionedArchiveListMatchesTheJobsPlaneCorpus`
  keeps the list anchored to the jobs-plane corpus; check the readers before
  you extend it.
- **Re-running the check by hand** — restart the service (`sudo -n systemctl
  restart pipelinegen`) and read the log; the check has no CLI surface.
