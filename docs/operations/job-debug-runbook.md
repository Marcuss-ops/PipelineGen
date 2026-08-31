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
  (token-rotation-in-flight, server binary not yet restarted — see
  `docs/operations/stock-e2e-runbook.md#§11.2`).
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
- Verify the live scraper reachability via the
  `docs/operations/stock-e2e-runbook.md#§11` recipe (X-Velox-Admin-Token
  pre-flight + `/api/artlist/search/live?term=...&limit=...` form per
  `fix(tests): correct --data-urlencode curl invocations in artlist
  live e2e` — commit `6c7fc1f85`).
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
