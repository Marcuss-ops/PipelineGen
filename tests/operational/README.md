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
| `VELOX_ADMIN_TOKEN`             | (mandatory if `TOKEN_FILE` unset) | Bearer token. **Never echo the env var directly** — every output path runs through `smoke_echo_safe`. |
| `TOKEN_FILE`                    | (unset)                           | Path to a file containing `VELOX_ADMIN_TOKEN=…`; used as a fallback when the env var is not set (compatible with the existing `scripts/diagnostics/marker_audit.sh` convention). |
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

The Makefile gates that journal commits + deployments have been split into a
**four-tier fail-closed chain** (July 2026, `build(make): split verify-main
into fast/main/release/live gates`). Each tier runs sequentially; an earlier
failure halts the chain.

| Tier             | Make target          | When to run                       | Browser / Drive / Qdrant live? | Headless? | Typical runtime |
|------------------|----------------------|-----------------------------------|--------------------------------|-----------|-----------------|
| dev loop         | `verify-fast`        | During active development         | No                             | Yes       | ~30 s           |
| **pre-push gate**| `verify-main`        | Before every `git push` to `main` | **No**                         | Yes       | ~5 min          |
| pre-deploy gate  | `verify-release`     | Before every release              | No (adds `verify-integration`) | Yes       | ~5 min          |
| post-deploy gate | `verify-live`        | After deploy / on operational hosts | **Yes** (scraper + Drive + Qdrant + browser) | No (full stack) | 10–30 min |

**Failure isolation.** None of `verify-fast`, `verify-main`, or
`verify-release` pulls any script under `tests/operational/artlist/`
(validated via `make -n` plus a manual absence grep). Only `verify-live`
composes the four live batteries:

- `tests/operational/images_e2e.sh`
- `tests/operational/script_generate_smoke.sh`
- `tests/operational/vidrush_media_e2e.sh`
- `tests/operational/artlist/run_all.sh`

(`artlist_e2e.sh` is retained as a backward-compat `exec` shim that
forwards to `artlist/run_all.sh`.)

### Artlist DoD sub-scripts

The Artlist DoD battery has been **split out of the monolithic
`artlist_e2e.sh`** (July 2026) into nine fail-closed sub-scripts under
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

# Lightweight path (build + startup + error-path probes):
make smoke

# Heavy path (text-only script generation — slow, depends on worker health):
make smoke-script

# Dry-run (prints the would-be requests, ignores token, never touches the server):
SMOKE_DRY_RUN=1 make smoke-script
```
