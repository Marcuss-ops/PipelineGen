# Artlist Definition of Done — Gate Map

> Operational battery: `tests/operational/artlist_e2e.sh`
> Inherits `smoke_*` from `lib/common.sh` and `velox_*` from `lib/velox_domain.sh`.
> This file maps the eleven hard gates defined by the DoD to the helper invocations consumed inside `artlist_e2e.sh`, and ties them back to the 15-point Verdict checklist.

## Why this exists

`vidrush_media_e2e.sh` already checks most Artlist behaviour end-to-end but the assertions are interleaved with image business logic and use one-off inline helpers. Hard-factoring them into a sibling battery with a shared library keeps the surface clean: each gate is a single function, each field check is a one-line call into `lib/velox_domain.sh`.

## Gate-to-helper map

| # | Gate                           | Status    | Helper / call                                                                                     | Verdict checklist point |
|---|--------------------------------|-----------|---------------------------------------------------------------------------------------------------|--------------------------|
| 0 | Clean reproducible environment | hard PASS | `smoke_curl GET /health`, `/ready`, scraper `/health`, `pgrep -af node.*artlist_server`, `command -v ffmpeg/ffprobe/jq`, `smoke_no_tampering_save` (PRE) + `smoke_no_tampering_verify` (POST) inside `run_all.sh` | 0, 11–15 |
| 1 | `/detail` stream hard gate      | hard PASS | future `velox_artlist_detail` (returns `STREAM_NOT_FOUND` on miss)                                | 1 |
| 2 | `/download` direct with ffprobe | hard PASS | DoD-exact `ffprobe -show_entries format=duration,size:stream=codec_name,width,height -of json` + jq contract on `.streams[0].width` and `.streams[0].height` (FIRST stream must be a valid video stream); fail-closed on HTTP non-2xx, missing/zero-byte local_path, MIME != video/mp4, or any ffprobe field <= 0. | 2 |
| 3 | `/api/artlist/search/live` × 3  | hard PASS | 3 LIVE_QUERIES (office/gym/arena); timeout 60s with `SEARCH_TIMEOUT` sentinel; per-clip Title ≠ 'Artlist' / page_url artlist.io / clip_id / RawMetadata + Keywords[]; relevance HARD GATE (≥1 clip must have ≥1 query-token match in Title+Tags+Categories+Keywords corpus); promoted from warning July 2026 | 3 |
| 4 | First fresh run 3/3             | hard PASS | `velox_artlist_pipeline_run $ARTLIST_TERM 3` → poll terminal via `smoke_poll_terminal` (no `RETRY_WAIT`) | 4 |
| 5 | Per-clip DB + file validation   | hard PASS | `smoke_sqlite_query $DB -json "SELECT … WHERE id='…'"` + `smoke_ffprobe_check`                    | 5 |
| 6 | Drive resolve-by-id hard gate   | hard PASS | `velox_drive_resolve $drive_file_id` per clip                                                    | 6 |
| 7 | SQLite + outbox integrity       | hard PASS | `smoke_sqlite_query` row-count, then `smoke_outbox_chain_verify` for terminal status              | 7 |
| 8 | Qdrant + media search hard gate | hard PASS | `velox_qdrant_assert $clip_id $COLLECTION $QDRANT_URL` + `POST /api/media_search sources=[artlist]` | 8 |
| 9 | Cache replay (cache_hit=true)   | hard PASS | replay `velox_artlist_pipeline_run` → compare tuples `clip_id|drive_file_id|file_hash`           | 9 (incl. `cache_hit=true`/`cache_source=sqlite`) |
| 10| Negative tests                  | hard PASS | 3x probes: SESSION_EXPIRED, STREAM_NOT_FOUND, SCRAPER_UNAVAILABLE                                 | 10 |
| R | Restart test                    | hard PASS | (a) full PASS before restart → (b) PipelineGen + node-scraper restart without manual hacks → (c) full PASS after | 11–15 |

## 15-point Verdict checklist

Each verdict point must be checked off before Artlist is **DONE**:

1. `POST /detail` returns a real stream URL (m3u8 / MP4) (Gate 1)
2. `POST /download` returns an ffprobe-valid MP4 (Gate 2)
3. Three live-search queries return real Artlist clips ≤ 60s each (Gate 3)
4. Fresh pipeline completes 3/3 clips with `processed=3 failed=0` (Gate 4)
5. Each clip: `source=artlist media_type=video lifecycle_state=PUBLISHED index_state=INDEXED duration 6.5–8.5s 1920×1080 drive_* populated file_hash + source_provider + source_version metadata_origin=artlist provider_tags + provider_categories + discovered_by_queries` (Gate 5)
6. Each clip: `POST /api/drive/resolve-by-id` returns non-trashed file with size > 0 in the Artlist folder (Gate 6)
7. Each clip: exactly one row in `media_assets`, hash coherent, outbox terminal `completed` or `superseded` (Gate 7)
8. Each clip: at least one Qdrant point, payload `source=artlist media_type=video lifecycle_state=PUBLISHED`, and `POST /api/media/search sources=[artlist]` must return the clip (Gate 8)
9. Replay returns same `clip_id|drive_file_id|file_hash` tuples and the response carries `cache_hit=true cache_source=sqlite` (Gate 9)
10. Negative paths return explicit sentinels (`SESSION_EXPIRED`, `AUTH_REQUIRED`, `STREAM_NOT_FOUND`, `SCRAPER_UNAVAILABLE`, `SEARCH_TIMEOUT`) — no false positives, no infinite `RETRY_WAIT` (Gate 10)
11. No manual `npm rebuild better-sqlite3` during the run
12. No manual `kill` of Chrome / scraper processes
13. No manual `sqlite3` / `rm` writes against live DB or local file tree
14. PipelineGen restart brings up `/health` + `/ready` + `/api/artlist/diagnostics` cleanly
15. node-scraper restart on 9123 recovers session without manual profile or cookie edits

## Source-of-truth dependencies

| Helper              | Source lib                              | Re-implemented inside battery? |
|---------------------|-----------------------------------------|--------------------------------|
| assert_eq           | `smoke_assert_eq` in `lib/common.sh`    | **No** (reused)                 |
| http_call           | `smoke_http_call` in `lib/common.sh`    | **No** (reused)                 |
| ffprobe_check       | `smoke_ffprobe_check` in `lib/common.sh`| **No** (reused)                 |
| sqlite_query        | `smoke_sqlite_query` in `lib/common.sh` | **No** (reused)                 |
| qdrant_assert       | `velox_qdrant_assert` in `lib/velox_domain.sh` | **No** (reused)         |
| drive_resolve       | `velox_drive_resolve` in `lib/velox_domain.sh` | **No** (reused)         |
| no_tampering_save   | `smoke_no_tampering_save` in `lib/common.sh` | **No** (reused) — Gate 0 PRE-snapshot at `01_startup.sh::main`         |
| no_tampering_verify | `smoke_no_tampering_verify` in `lib/common.sh` | **No** (reused) — Gate 0 POST-snapshot diff at `run_all.sh` post-chain |
| dry_run             | `DRY_RUN` flag from `lib/common.sh`     | **No** (reused)                 |

## Gate 0 anti-tampering snapshot contract

Gate 0 also captures / verifies a per-run "no manual intervention"
fingerprint so the DoD can disqualify any run during which the operator
or a flaky CI layer performed one of:

| # | Disqualifying action                            | Verdict point | Fingerprint field                                |
|---|--------------------------------------------------|----------------|--------------------------------------------------|
| 11| `npm rebuild better-sqlite3` mid-run             | 11             | `better_sqlite3.module_sha256`                  |
| 12| manual `kill` of Chrome / scraper                | 12             | `chrome.pid_set`, `scraper.pid_set`             |
| 13| manual `sqlite3` / `rm` writes against live DB   | 13             | `sqlite.head1m_sha256`, `sqlite.size_bytes`, `sqlite.mtime_epoch` |
| 14| restart improvvisato of PipelineGen              | 14             | `pipelinegen.pid_set`                            |
| 15| restart improvvisato or cookie / session edit    | 15             | `scraper.starttime_set`, `profile.mtime_epoch`  |

Call sequence (auto-wired):

1. `run_all.sh` exports `ARTLIST_DOD_RUN_ID=$(date -u +%Y%m%dT%H%M%SZ)`
2. `01_startup.sh::main` calls `smoke_no_tampering_save` after `gate_preflight` PASS — writes `${VELOX_DATA_DIR:-./data}/.artlist_dod_fingerprint/${ARTLIST_DOD_RUN_ID}/pre.json`
3. Each sub-script in the chain runs as its own bash sub-shell (per-run `WORK_DIR` is fresh each time — no leakage).
4. After the chain passes, `run_all.sh` calls `smoke_no_tampering_verify 'run_all.post_chain'` — writes `post.json` + `diff.json`. Any drift field triggers a fail-closed `[FAIL]` line and exit 1.

The verify step is robust to:
- DB absent at pre OR post (records `"?"` sentinel → eq-treated).
- `$VELOX_DATA_DIR` read-only (fallback path under `/tmp/artlist_dod_fingerprint/...`).
- macOS + Linux (`stat -c %s` GNU + `stat -f %z` BSD are both probed).
- DRY_RUN=1 (no-ops in both save and verify).
- 01_startup.sh run standalone outside `run_all.sh` (verify emits a `[WARN]` and returns 0 so the standalone run isn't a confusing failure).

Operators can `rm -rf ./data/.artlist_dod_fingerprint/` to clean history; the directory is rotated per run.

## How to extend

Each new hard gate is one PR. The scaffold in `artlist_e2e.sh` references `gate_*` functions in numerical order; replace the stub line with the real implementation and add a row above. Increment `PASS / WARN / FAIL` only via the in-battery `log_pass / log_warn / log_fail` helpers — never mutate counters from inside `lib/velox_domain.sh` (helpers stay pure).

When all eleven gates + the restart test pass, the battery prints:

```
============================================
  VidRush Media E2E Battery (artlist_e2e)
  PASS=N WARN=0 FAIL=0
============================================
VERDICT: PASS
```

…and Artlist is **DONE**.
