# artlist_live_e2e_verify.sh — LAST DRY-RUN EVIDENCE (July 12 2026)

**Context**: PR-LIVE-VERIFY (July 2026) ran the verify script in `--dry-run` mode
against the dev environment to capture which gates pass today and which
require follow-up. This file is the load-bearing evidence attachment for
`architecture/issues.yaml` — every entry under PR-LIVE-VERIFY-{1..5}
references this file via `evidence_filename`.

## Dry-run command

```bash
cd /home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored
bash tests/operational/artlist_live_e2e_verify.sh --dry-run
```

(Note: `--dry-run` skips the strict pre-flight `require VELOX_ADMIN_TOKEN`
gate and runs light read-only probes when a token IS set. The dry-run
also does NOT consume any Artlist download quota.)

## Configuration surfaced by the dry-run plan

```text
BASE_URL              = http://127.0.0.1:8000
SCRAPER_URL           = http://127.0.0.1:9123
QDRANT_URL            = http://127.0.0.1:6333/collections/media_assets_current
QDRANT_API_KEY        = <empty>
DB_PATH               = ./data/media/media.db.sqlite
TOKEN (VELOX_ADMIN)   = <empty>
ROOT_FOLDER_ID        = <empty — warning>
SEARCH_TERM           = 'boxing training'
LIMIT                 = 1
EXPECTED_GATE_MATCHES = 28 (matches gate11_scraper_failure_test.go meta-anchor)
```

(KEY DIFFERENCE: when the script body runs in non-dry-run mode, it
**refuses** to proceed with an empty token, an unreachable scraper, an
unreachable Qdrant, a missing DB, or a missing `VELOX_ADMIN_TOKEN`. The
dry-run above reveals the 5 distinct environmental gaps.)

## Per-gate signal table

| Gate | Probe | Outcome | Follow-up |
|------|-------|---------|-----------|
| server `/ready` | `GET http://127.0.0.1:8000/ready` | **PARTIAL**: `{"checks":{"clips_path":{"ok":true},"db":{"ok":true},"destination_clip":{"registered":true},"drive":...}}` — checks emitted but top-level `.status` missing so `jq -e '.status == "ready"'` filter fails | **PR-LIVE-VERIFY-2** |
| scraper `/health` | `GET http://127.0.0.1:9123/health` | **UNREACHABLE**: HTTP connection refused (Node sidecar not started in dev) | **PR-LIVE-VERIFY-1** |
| Qdrant `/collections` | `GET http://127.0.0.1:6333/collections` | **REACHABLE**: `{result:{collections:[media_assets, media_assets_v3_e5_768_siglip_768, ...]}}` | NOT A GAP |
| SQLite media.db.sqlite | `ls ./data/media/media.db.sqlite` | **PRESENT**: 32MB on disk | NOT A GAP |
| `VELOX_ADMIN_TOKEN` | shell environment | **MISSING**: script would `log_fail + exit 2` in non-dry-run mode | **PR-LIVE-VERIFY-4** |
| `ROOT_FOLDER_ID` | shell environment | **EMPTY**: warning emitted; falls back to PipelineGen configured default Drive folder | **PR-LIVE-VERIFY-3** |
| Drive OAuth credentials | service-account JSON path / OAuth tokens | **UNKNOWN** (the script doesn't probe this gate, but verifications 5/6 require REAL Drive authorization) | **PR-LIVE-VERIFY-5** |

## 9 verification points — current reachability

Per the user spec, the 9 verification points are:

1. **scraper /search** — NOT EXECUTED (scraper unreachable → can't probe).
   Closest follow-up: **PR-LIVE-VERIFY-1**.
2. **POST /api/artlist/run returns run_id** — NOT EXECUTED (server `/ready`
   NOT OK + token missing → script would exit 2 at pre-flight).
   Closest follow-ups: **PR-LIVE-VERIFY-2** + **PR-LIVE-VERIFY-4**.
3. **media.artlist job terminal SUCCEEDED within 180s** — NOT EXECUTED
   (cascaded failure from #2). Same follow-ups.
4. **artlist_download_audit.status='succeeded'** — NOT EXECUTED
   (cascaded from #3). Coverage comes online once 1-3 work.
5. **media_assets row + drive_file_id non-empty + drive_link +
   download_link + file_hash + source_version + index_state=INDEXED** —
   NOT EXECUTED (cascaded from #3). Drive authorization specifically
   requires **PR-LIVE-VERIFY-5** (OAuth) + **PR-LIVE-VERIFY-3** (folder).
6. **Drive Files.Get via /api/drive/resolve-by-id (size > 0, not
   trashed, name non-empty)** — NOT EXECUTED. Requires **PR-LIVE-VERIFY-3**
   + **PR-LIVE-VERIFY-5**.
7. **outbox_events status IN (completed|superseded)** — NOT EXECUTED
   (cascaded). Outbox dispatcher is wired but no asset-index event fires
   without a successful job in #3.
8. **Qdrant scroll returns point with payload.source='artlist' +
   media_type='video' + lifecycle_state='PUBLISHED'** — NOT EXECUTED
   (Qdrant IS reachable + has collections, but no points with the
   produced asset_id exist yet — depends on #3-7 succeeding).
9. **POST /api/media/search returns the produced asset** — NOT
   EXECUTED (depends on #3-8).

## Summary

| Status | Count |
|--------|-------|
| Gates passing already | 2 (Qdrant + SQLite) |
| Gates failing today | 5 (scraper, server /ready, ROOT_FOLDER_ID, VELOX_ADMIN_TOKEN, Drive OAuth) |
| 9 verification points fully passing | 0 (all 9 require the 5 follow-ups to land) |
| 9 verification points partially passing today | 2 (Qdrant reachable + SQLite present, but no scroll target without asset from #3-7) |

## Per-follow-up closure pathway

To close every issue and reach the 9 points ALL PASS:

1. **PR-LIVE-VERIFY-1** (scraper reachable)
   → bring up Node sidecar via `node node-scraper/artlist_server.js`
   with `CHROME_EXECUTABLE=/usr/bin/google-chrome`.
2. **PR-LIVE-VERIFY-2** (`/ready` reports `status=ready`)
   → patch `/ready` handler to emit top-level `.status` from the AND
   of all checks (current handler emits per-check status, no aggregate).
3. **PR-LIVE-VERIFY-3** (`VELOX_DRIVE_ARTLIST_ROOT` provisioned)
   → set `VELOX_DRIVE_ARTLIST_ROOT` in dev `.env` + CI secrets.
4. **PR-LIVE-VERIFY-4** (`VELOX_ADMIN_TOKEN` provisioned)
   → generate via canonical token rotation script + persist to env.
5. **PR-LIVE-VERIFY-5** (Drive OAuth credentials present)
   → service-account JSON path or OAuth refresh token provisioning,
   per AGENTS.md Operational rules (never commit credentials).

Once all 5 follow-ups are closed, this file will be replaced with a
"fully passing" dry-run / live-run evidence attachment.

## What the dry-run DELIBERATELY DOES NOT DO

- Does NOT consume an Artlist download (scratch quota).
- Does NOT enqueue a real `media.artlist` job.
- Does NOT pollute `media_assets` / `outbox_events` / `artlist_download_audit`.
- Does NOT call `puppeteer.launch` (the test pre-flight halts before
  boot-warmup).
- Does NOT exercise `POST /api/drive/resolve-by-id` (Drive side).

A live run (without `--dry-run`) after all 5 follow-ups land will execute
the full 9-point contract end-to-end and is expected to print
**VERDICT: PIPELINE ARTLIST LIVE CORRETTA** at the bottom of the script
with `PASS=22 WARN=0 FAIL=0`.
