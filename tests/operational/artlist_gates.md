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
| 4 | First fresh run 3/3             | hard PASS | precondition `smoke_sqlite_query` row-count on `artlist_runs` for `term=$ARTLIST_TERM` MUST be 0 (no prior matching row → fresh-install gate) → POST `/api/artlist/run` with canonical 9-field JSON body `{term,limit:3,strategy:replace,clip_duration:7,width:1920,height:1080,fps:30,concurrency:1,dry_run:false}` → extract `.run_id` (job_id, since handler enqueues via `h.jobsService.Enqueue`) → poll `/api/jobs/<run_id>/full` via `smoke_poll_terminal` (returns 124/non-zero on RETRY_WAIT so infinite backoff auto-fails Gate 4) → GET `/api/artlist/runs/<run_id>` for the finalized `RunTagResponse` → 8 hard invariants: inv-1 HTTP 2xx, inv-2 `.run_id` present, inv-3 `.status ∈ {SUCCEEDED, completed}` (PARTIALLY_SUCCEEDED/RETRY_WAIT/RUNNING/QUEUED/FAILED/CANCELLED/DEAD_LETTER fail-closed), inv-4 `.items` length exactly 3, inv-5 zero items with `status startswith "blocked_"` (Fase 6 typed-error block surface), inv-6 `.processed==3`, inv-7 `.failed==0`, inv-8 `SELECT COUNT(*) FROM jobs WHERE id=<run_id> AND status='RETRY_WAIT'` must return 0 (belt-and-braces for infinite backoff) | 4 |
| 5 | Per-clip DB + file validation   | hard PASS | consume hand-off `${WORK_DIR}/clip_ids.txt` (Gate 4 wrote it via `jq -r '.items[]?.clip_id // empty'` over the finalized RunTagResponse) → for each clip_id: `smoke_sqlite_query $DB_PATH -json "SELECT ma.source, ma.media_type, ma.lifecycle_state, ma.index_state, ma.start_ms, ma.end_ms, ma.width, ma.height, ma.file_hash, ma.source_provider, ma.source_version, json_extract(ma.metadata_json, '$.metadata_origin') AS metadata_origin, json_extract(ma.metadata_json, '$.provider_tags') AS provider_tags_json, json_extract(ma.metadata_json, '$.provider_categories') AS provider_categories_json, json_extract(ma.metadata_json, '$.discovered_by_queries') AS discovered_by_queries_json, ma.drive_file_id, ma.drive_link, ma.download_link, ma.local_path FROM media_assets ma WHERE ma.id='<clip_id>'"` → jq -e composite check (18 invariants: source=artlist media_type=video lifecycle_state=PUBLISHED index_state=INDEXED duration (end_ms − start_ms)/1000.0 ∈ [6.5, 8.5] width=1920 height=1080 file_hash/source_provider/source_version/drive_*/local_path present metadata_origin=artlist provider_tags/provider_categories/discovered_by_queries non-empty via `fromjson? // [] | length >= 1`) → `smoke_ffprobe_check local_path 0` (reused verbatim from Gate 2 — DoD-exact ffprobe contract) → inline ffprobe with the same Gate 2 flag set for codec/container: `.format.format_name matches mp4|mov|m4a` AND `.streams[] | select(.codec_type=="video") | .codec_name | any(. == "h264")` → ALL clips must pass (DoD forbids partial-pass) | 5 |
| 6 | Drive resolve-by-id hard gate   | hard PASS | preflight `[[ -n "${ARTLIST_ROOT_FOLDER:-}" ]]` (fail-closed with handler-typed sentinel otherwise) → consume `${WORK_DIR}/clip_ids.txt` hand-off (Gate 4 wrote it, shared $WORK_DIR inside one script invocation) → for each clip_id: `smoke_sqlite_query $DB_PATH -json "SELECT drive_file_id FROM media_assets ma WHERE ma.id='<clip_id>'"` (Gate 5 cross-check that drive_file_id is non-empty) → `velox_drive_resolve <drive_file_id>` (lib/velox_domain.sh, rc=0 only on canonical shape contract `.ok AND .resolved_count>=1 AND .resolved[0].trashed==false AND .resolved[0].size>0`; writes body to `${WORK_DIR}/velox_drive_<id>.json`) → INLINE jq -e parent membership: `.resolved[0].parents // [] | any(. == $ARTLIST_ROOT_FOLDER)` (raw Drive folder ID string-equal; empty parents[] fails closed) → INLINE `curl -sS --max-time 6 -o /dev/null -w '%{http_code}' -I <webViewLink>` link-probe (2xx OR 3xx = PASS — Drive's public-share view often returns 302 → accounts.google.com; 4xx/5xx/timeout=000 = FAIL) → ALL clips must pass (DoD forbids partial-pass) | 6 |
| 7 | SQLite + outbox integrity       | hard PASS | consume hand-off `${WORK_DIR}/clip_ids.txt` (Gate 4 wrote it; shared $WORK_DIR across all 4 gates in `05_pipeline_fresh.sh`) → per clip_id **5 hard invariants** (promote-to-HARD per DoD refactor): **inv-1** `smoke_sqlite_query $DB_PATH "SELECT COUNT(*) FROM media_assets WHERE id='<clip_id>'"` MUST be **exactly 1** (no duplicates); **inv-2** `smoke_sqlite_query $DB_PATH "SELECT file_hash, local_path FROM media_assets WHERE id='<clip_id>'"` → `file_hash` MUST equal `sha256sum local_path | awk '{print $1}'` (one-shot SELECT for both columns, default pipe-delimited split); **inv-3** `smoke_sqlite_query $DB_PATH "SELECT COUNT(*) FROM asset_locations WHERE asset_id='<clip_id>' AND location_kind='local'"` MUST be ≥ 1 (per migration 055_ASSET_LOCATIONS.SQL); **inv-4** same SELECT with `location_kind='drive'` MUST be ≥ 1 (Drive mirror row); **inv-5** `smoke_sqlite_query $DB_PATH "SELECT SUM(CASE WHEN status='completed' THEN 1 ELSE 0 END) AS completed, SUM(CASE WHEN status='superseded' THEN 1 ELSE 0 END) AS superseded, SUM(CASE WHEN status='dead_letter' THEN 1 ELSE 0 END) AS dead_letter, COUNT(*) AS total FROM outbox_events WHERE event_type='asset.index.requested' AND aggregate_id='<clip_id>'"` → jq -e composite: `(completed+superseded) >= 1 AND dead_letter == 0 AND total >= 1` (per-clip inline per AGENTS.md no-duplicate-classification — accepts SUPERSEDED as terminal per migration 092 + outboxevents.MarkSuperseded writer); **post-loop forensic**: `smoke_outbox_chain_verify $DB_PATH $clip_file || true` (helper's rc=1 on SUPERSEDED is stricter than Gate 7's DoD surface; `|| true` makes it diagnostic-only without false-failing); **ALL clips must pass** (DoD forbids partial-pass — mirrors Gates 4/5/6 contract). | 7 |
| 8 | Qdrant + media search hard gate | hard PASS | preflight `local q_coll="${QDRANT_COLLECTION:-media_assets_collection}"; local q_url="${QDRANT_URL:-http://127.0.0.1:6333}"` (fail-closed if operator never set the env defaults — matches operational suite household) → consume `${WORK_DIR}/clip_ids.txt` hand-off (Gate 4 wrote it; shared $WORK_DIR across all 5 gates in this script invocation) → per clip_id **3 Qdrant invariants (one call)**: `velox_qdrant_assert <clip_id> <q_coll> <q_url> artlist video PUBLISHED <QDRANT_API_KEY>` covers /points/scroll existence + payload.source=artlist + payload.media_type=video + payload.lifecycle_state=PUBLISHED in a single reusable helper (lib/velox_domain.sh) → 3-way `case $?` operator diagnostic: rc=1 SHAPE drift (jq contract returned false on payload fields) → inscribe `${WORK_DIR}/velox_qdrant_${clip_id}.json` rc=2 TRANSPORT/HTTP non-2xx → verify QDRANT_URL/QDRANT_API_KEY freshness rc=* default → unexpected reload pattern → Qdrant-phase aggregate: ALL clips MUST pass (DoD rejects partial-pass — mirrors Gates 4/5/6/7) → **semantic recovery hard gate** (PROMOTED warning → HARD per DoD refactor): `search_body=$(jq -nc --arg q "$ARTLIST_TERM" '{query:$q, sources:["artlist"], limit:3}')` then `smoke_curl POST /api/media/search -d $search_body` (handler contract from internal/api/assets/search/handler.go:Search; 200→{items,next_cursor,partial,provider_errors}) → fail-closed on non-2xx → `jq -r '.items[]?.asset_id // empty' $SMOKE_LAST_BODY > $WORK_DIR/search_assets.txt` (set-e × jq guard via `|| :`, matching Gate 4 hand-off pattern) → `grep -Fxcf $clip_file $search_assets_file` returns line-level intersection count (fixed-string -F, full-line -x, count -c, file-of-patterns -f; rc=0 on ≥1 match rc=1 on 0 — fail-closed on no recoupment) → recoupment count >= 1 OR hard fail with token-redacted forensic dump of `$SMOKE_LAST_BODY.items[0..10]` (smoke_echo_safe ironclad bearer redaction). Reuses ONLY: velox_qdrant_assert (lib/velox_domain.sh) + smoke_curl (lib/common.sh) + jq -r + grep -Fxcf. NO new helpers. AGENTS.md single-focus rule honoured. | 8 |
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
