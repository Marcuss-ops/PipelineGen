# Black-box Smoke Tests (`tests/operational/`)

This directory contains **end-to-end smoke tests** that exercise PipelineGen
purely through its external HTTP surface. They do **not** import Go code from
`internal/...`, do **not** mutate production scripts, and do **not** change
business logic. Every check is a live (or `--dry-run`) HTTP probe parsed with
`curl` + `jq`.

The suite is intentionally strict — assertions are not weakened to fit a
broken state of the worker. Exit code 1 is the honest failure signal. The
*initial-failure map* below documents which tests are *expected* to flip from
1 → 0 as the worker transitions to a fully green state.

---

## File inventory

| File                       | Purpose                                                                                  |
|----------------------------|------------------------------------------------------------------------------------------|
| `startup_smoke.sh`         | Verifies `/health`, `/ready`, `/api/system/doctor` all return HTTP 2xx.                  |
| `text_script_smoke.sh`     | Drives a text-only script job end-to-end (dispatch + poll + assert script content).      |
| `failed_job_smoke.sh`      | Verifies error paths: invalid payload → HTTP 4xx; nonexistent job_id → HTTP 404.         |
| `lib/common.sh`            | Source-able helpers: curl wrapper, jq response parsing, strict token redaction, `--dry-run`, color codes, timeout enforcement. |

The `lib/` directory is not executable on its own; it is sourced by each smoke
script via `source "$(dirname "$0")/lib/common.sh"`.

---

## Per-script arguments

All three smoke scripts accept:

| Flag    | Effect                                                                                   |
|---------|------------------------------------------------------------------------------------------|
| `--dry` | Print the would-be requests, exit 0. Does **not** hit the live server or read a token.  |
| `-h` / `--help` | Print the script's shebang docblock (first 14–17 lines) and exit 0.              |

`common.sh` rejects unknown flags with exit code 2 (no silent pass-through).

---

## Prerequisites

The smoke suite shells out to a small set of standard Unix tools. Every CI
image or dev box running the suite must have all of these present (otherwise
`tests/operational/lib/common.sh::smoke_require` exits 2 with the missing
names BEFORE any HTTP request fires):

| Binary    | Used for                                                              |
|-----------|-----------------------------------------------------------------------|
| `bash`    | Runner (>= 4.x recommended).                                          |
| `curl`    | All HTTP probes.                                                      |
| `date`    | Wall-clock + poll-deadline arithmetic.                                |
| `jq`      | JSON parsing of `/api/jobs/<id>/full` and `result.script` extraction. |
| `mktemp`  | Per-run workdir under `/tmp`.                                         |
| `tput`    | ANSI colour codes (no-op when `NO_COLOR=1`).                          |
| `uuidgen` | Preferred portable UUID source for the nonexistent-job probe.         |

A preflight check (`smoke_require jq`) runs unconditionally at source-time.
`failed_job_smoke.sh` falls back through `uuidgen → /proc/sys/kernel/random/uuid
→ python3 → epoch+RANDOM`, so `uuidgen` is NOT strictly required on macOS or
sandboxed Linux runners that lack `/proc`.

---

## Environment variables (all overridable)

| Variable                        | Default                           | Notes                                                                                  |
|---------------------------------|-----------------------------------|----------------------------------------------------------------------------------------|
| `API_BASE`                      | `127.0.0.1:${VELOX_PORT:-8080}`   | `host:port` shape; the script prepends `http://` itself.                               |
| `VELOX_PORT`                    | `8080`                            | Honoured by `API_BASE` resolution; matches the canonical default in `internal/platform/config/types.go`. |
| `VELOX_ADMIN_TOKEN`             | (ignored by canonical wrapper)   | The canonical `scripts/with-velox-auth` loader reads the admin token from `TOKEN_FILE` and never trusts an inherited value. Scripts that do not use the wrapper may supply the token explicitly; **never echo it** — every output path runs through `smoke_echo_safe`. |
| `TOKEN_FILE`                    | `/etc/pipelinegen/pipelinegen.env` | Path to the canonical env file containing `VELOX_ADMIN_TOKEN=…`; the wrapper reads this file authoritatively so callers cannot accidentally probe with a stale inherited token. |
| `SMOKE_TIMEOUT_SECONDS`         | `180`                             | Per-script overall wall clock. Exceeding this exits 124.                              |
| `SMOKE_POLL_TIMEOUT_SECONDS`    | `120`                             | Polling loop cap for `/api/jobs/<id>/full`. Exceeding this exits 124.                  |
| `SMOKE_POLL_INTERVAL_SECONDS`   | `2`                               | Sleep between polls.                                                                   |
| `SMOKE_HTTP_TIMEOUT_SECONDS`    | `8`                               | Per-`curl` `--max-time` ceiling.                                                       |
| `SMOKE_TOPIC` / `SMOKE_TITLE`   | tiny canned topic + title         | Overridable; otherwise `text_script_smoke.sh` uses a deterministic-smoke topic.          |
| `NO_COLOR`                      | (unset → colors enabled)          | If non-empty, suppresses ANSI colour codes.                                             |

---

## Exit code discipline

| Code  | Meaning                                                                      |
|-------|------------------------------------------------------------------------------|
| `0`   | Every assertion passed.                                                      |
| `1`   | One or more assertions failed.                                               |
| `2`   | Internal setup error (unknown flag, missing token, bad payload parse).       |
| `124` | Poll loop timed out (no terminal status within `SMOKE_POLL_TIMEOUT_SECONDS`). |

The wrappers print the failing HTTP code + a token-redacted body snippet to
stderr on every failure so operators can diagnose without re-running.

---

## Token redaction guarantee

All output is funnelled through `smoke_echo_safe()` (`lib/common.sh`). The
helper applies, in order, four `sed -E` substitutions that consume every
known shape of the token:

1. `Authorization: (Bearer|Basic) <value>`  →  `… <REDACTED>`
2. `Bearer <value>`                          →  `Bearer <REDACTED>`
3. JSON `"token":"<value>"`                 →  `"token":"<REDACTED>"`
4. `VELOX_ADMIN_TOKEN=<value>`              →  `VELOX_ADMIN_TOKEN=<REDACTED>`

The bearer token is **never** echoed via raw `printf` or `echo`. The `curl`
wrapper injects the header silently. `set` debug-output cannot reveal the
token because `WORK_DIR` is `mktemp -d`-isolated and the env var is exported
under a session-local name.

---

## Known initial-failure map

Per the spec, the smoke suite is strict and survives — but the worker
currently blocks some scripts because the build-fix branch landed without
its Wave-4A followup (`*youtube.Service.Extract`). The transition matrix is:

| Script                  | Expected current state | Unblocking commit / action                                                      |
|-------------------------|------------------------|--------------------------------------------------------------------------------|
| `startup_smoke.sh`      | **failing**            | Worker startup fails to bind; resolves when `Extract` forwarding method added to `internal/application/youtube/service_orchestrator.go` (mirror of `GetVideoInfo`). |
| `text_script_smoke.sh`  | **failing**            | Same root cause as above; script generation also depends on the patched `*Service` chain. |
| `failed_job_smoke.sh`   | **failing → passing**  | Validation runs *before* service wiring, so error-path HTTP codes should be reachable as soon as the binary can boot. Will still fail loudly if the binary itself refuses to start. |

Assertions stay strict on every run until the worker fully passes — no
shortcuts, no `--soft` mode. The exit code is the only honest signal.

See `architecture/current.yaml` (Wave 4A pending list) for the canonical
ownership of the `Extract` followup.

---

## Verification matrix (`verify-*`)

The Makefile gates that journal commits + deployments are split into a
**six-gate fail-closed chain** (July 2026). The four headless gates escalate
from daily development to pre-deploy certification; the live gate remains a
separate post-deploy surface. Each gate fails closed; an earlier dependency
failure halts the chain.

> **Migration note.** The `verify-*` tier gates are the **canonical pre-push
> / pre-deploy / post-deploy surface** for the Artlist and script batteries.
> Operators must use `make verify-artlist-*`, `make verify-script-live`, or
> `make verify-vidrush-live`; removed smoke aliases are not supported.

| Tier / gate      | Make target          | Composition                                      | When to run                       | Browser / Drive / Qdrant live? | Headless? |
|------------------|----------------------|--------------------------------------------------|-----------------------------------|--------------------------------|-----------|
| dev loop         | `verify-fast`        | foundation + static                              | During active development         | No                             | Yes       |
| **daily gate**   | `verify-main`        | `verify-push + verify-node-native + verify-architecture` | Before every `git push` to `main` | **No**                         | Yes       |
| explicit race    | `verify-race`        | `verify-unit-race`                               | Release/concurrency-sensitive changes | No                         | Yes       |
| complete headless| `verify-full`        | `verify-main + verify-race + verify-node-tests` | CI/release certification          | No                             | Yes       |
| pre-deploy       | `verify-release`     | `verify-full + verify-integration`              | Before every release              | No                             | Yes       |
| post-deploy      | `verify-live`        | live operational batteries                       | After deploy / operational hosts  | **Yes**                        | No        |

**Failure isolation.** None of `verify-fast`, `verify-main`, `verify-race`,
`verify-full`, or `verify-release` pulls any script under
`tests/operational/artlist/` (validated via `make -n` plus a manual absence
grep). Only `verify-live` composes the live batteries:

- `tests/operational/images_e2e.sh`
- `tests/operational/generate/run.sh` (canonical runner; `basic.json` scenario)\n- `tests/operational/script_generate_smoke.sh` (compatibility wrapper for the canonical runner)
- `tests/operational/vidrush_script_generate_e2e.sh`
- `tests/operational/artlist/run_all.sh`

### Artlist DoD sub-scripts

The Artlist DoD battery has been **split out of the monolithic
the former monolithic Artlist battery (July 2026) into nine fail-closed sub-scripts under
`tests/operational/artlist/`, plus an orchestrator wrapper. Each sub-script
sources `../lib/common.sh` + `../lib/artlist.sh` (and the relevant area-lib
where applicable) and can be run standalone via `bash`:

| Sub-script                          | Gate(s)         | What it probes                                                          |
|-------------------------------------|-----------------|--------------------------------------------------------------------------|
| `artlist/01_startup.sh`             | Gate 0          | `/health`, `/ready`, artlist job-consumer, scraper `/health`, single Chrome headless, Chrome/ffmpeg/ffprobe on PATH, Qdrant reachable, Artlist session authenticated. |
| `artlist/02_search_live.sh`         | Gate 3          | `/api/artlist/search/live` × 3 queries, 60s `SEARCH_TIMEOUT` sentinel on overrun, per-clip shape walk (clip_id, page_url, title, RawMetadata, Keywords). |
| `artlist/03_detail_stream.sh`       | Gate 1          | `POST /detail` happy-path AND `STREAM_NOT_FOUND` negative-path, with clip_page_url sampled from the live-search surface. |
| `artlist/04_download.sh`            | Gate 2          | `POST /download` + ffprobe DoD-check (duration > 0, size > 0, width/height > 0, MIME `video/mp4`). |
| `artlist/05_pipeline_fresh.sh`      | Gates 4 + 5     | Fresh run 3/3 + per-clip DB + local file validation. *(stub)*           |
| `artlist/06_drive.sh`              | Gate 6          | `POST /api/drive/resolve-by-id` (file on Drive, not trashed, MIME + size > 0). *(stub)* |
| `artlist/07_index.sh`              | Gates 7 + 8     | SQLite single-row + outbox chain COMPLETED; Qdrant point + `/api/media/search`. *(stub)* |
| `artlist/08_cache_replay.sh`        | Gate 9          | Re-run same term: `cache_hit=true`, `cache_source=sqlite`, identical clip_ids + file_hash. *(stub)* |
| `artlist/09_failure_modes.sh`      | Gate 10 + Restart | `SESSION_EXPIRED` + `STREAM_NOT_FOUND` + `SCRAPER_UNAVAILABLE`; restart preserves clip_ids + `drive_file_id` + `file_hash`. *(stub)* |
| `artlist/run_all.sh`               | Orchestrator    | Fail-closed chain over `01..09`. This is what `make verify-artlist-live` invokes. |

Stubs are intentionally declared (rather than absent) so the Makefile
targets parse and operators can dry-run the wiring. They will be filled
out gate-by-gate after this commit lands.

#### Per-step artifacts

Each non-stub sub-script emits durable per-run files under `$WORK_DIR`
(sourced from `lib/common.sh`'s `mktemp -d /tmp/smoke.…`). Operators
debugging failures should look at these before re-running the gate:

| Gate(s)                   | Sub-script               | Artifacts emitted                                                        |
|---------------------------|--------------------------|---------------------------------------------------------------------------|
| Gate 0                    | `01_startup.sh`          | Announce-only; PASS/WARN/FAIL counters in stdout (no JSON dump).        |
| Gate 1 (happy + negative) | `03_detail_stream.sh`    | `gate1_detail_ok.json` (happy), `gate1_detail_snf.json` (STREAM_NOT_FOUND). |
| Gate 2                    | `04_download.sh`         | `gate2_download.json` (scraper response) + `gate2_dl/<mp4>` (consumes Artlist quota). |
| Gate 3 (× 3 queries)      | `02_search_live.sh`      | `gate3_search_<idx>.json` per query (idx 0, 1, 2).                       |
| Gates 4 + 5               | `05_pipeline_fresh.sh`   | *(stub — no artifacts yet; will land with implementation)*               |
| Gate 6                    | `06_drive.sh`            | *(stub — will land with implementation)*                                |
| Gates 7 + 8               | `07_index.sh`            | *(stub — will land with implementation)*                                |
| Gate 9                    | `08_cache_replay.sh`     | *(stub — will land with implementation)*                                |
| Gate 10 + Restart         | `09_failure_modes.sh`    | *(stub — will land with implementation)*                                |

The full battery's `run_all.sh` orchestrates the chain but emits no global
artifact baseline yet; per-sub-script stdout verdicts (PASS/WARN/FAIL
counters + VERDICT line) are the live-readable source of truth during a
run.

### Surgical debug via granular targets

When iterating on a single gate, use the granular `verify-artlist-*`
targets to bypass the full battery. Each one routes one-to-one to its
sub-script:

| Make target                | Sub-script              | What it debugs                                  |
|----------------------------|-------------------------|--------------------------------------------------|
| `verify-artlist-startup`   | `01_startup.sh`         | Preflight — server / scraper / SQLite / Qdrant / session. |
| `verify-artlist-search`    | `02_search_live.sh`     | `/api/artlist/search/live` × 3 + timeout sentinel. |
| `verify-artlist-stream`    | `03_detail_stream.sh`   | `/detail` stream contract + STREAM_NOT_FOUND.    |
| `verify-artlist-download`  | `04_download.sh`        | `/download` + ffprobe DoD-check.                 |
| `verify-artlist-pipeline`  | `05_pipeline_fresh.sh`  | Fresh run 3/3 + per-clip validation.             |
| `verify-artlist-drive`     | `06_drive.sh`           | `resolve-by-id` end-to-end.                      |
| `verify-artlist-index`     | `07_index.sh`           | SQLite outbox + Qdrant point + grep search.      |
| `verify-artlist-cache`     | `08_cache_replay.sh`    | Replay with `cache_hit=true`.                    |
| `verify-artlist-errors`    | `09_failure_modes.sh`   | Negative paths + post-restart continuity.        |
| `verify-artlist-live`      | `run_all.sh`            | Full DoD battery (slow, fail-closed chain).      |

For headless announces-only runs of any sub-script, prepend
`SMOKE_DRY_RUN=1`:

```bash
SMOKE_DRY_RUN=1 make verify-artlist-stream
```

Under `dry-run`, the sub-scripts print their would-be probes and exit 0
without touching the server, the scraper, or the queue — useful for
debugging the wiring of a single gate before paying the live cost.

---

## How to run

```bash
# Compile + lint (CI gate):
make vet

# Static check every script for parse errors (no live calls):
for s in tests/operational/lib/common.sh \
         tests/operational/startup_smoke.sh \
         tests/operational/text_script_smoke.sh \
         tests/operational/failed_job_smoke.sh; do
    bash -n "$s" || exit 1
done

# Canonical live paths:
make verify-script-live
make verify-vidrush-live
```
