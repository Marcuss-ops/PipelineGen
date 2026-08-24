# Inspect Media Asset Runbook

Operator-facing procedure for `scripts/operations/inspect_media_asset.sh`.

## Purpose

`inspect_media_asset.sh` is the canonical post-benchmark / post-live-test
inspection helper for a single media asset. It reads the canonical
`media_assets` row + the `outbox_events` rows for that aggregate and asserts
the invariants that **must** hold for the image / clip / audio pipeline to be
considered operationally healthy:

| # | Invariant | Why |
|---|---|---|
| 1 | `media_type` ∈ `{image, clip, audio, document, image_video, sound_effect, script}` | Anything outside this set is a schema-drift symptom. |
| 2 | `local_path` exists on disk (or explicitly NULL/empty for a remote-only asset) | A populated `local_path` with no file on disk is the canonical two-transaction-gap symptom. |
| 3 | `json_array_length(visual_embedding) = 768` | The Qdrant `visual` named vector has 768 dims by manifest (`schema.VisualEmbeddingDim`). Wrong dims = upsert corruption. |
| 4 | `metadata_json.embedding_version_visual` populated (`2026-06-16-v1`) | Pins the SigLIP model version. Empty means the ingested row predates PR-CANONICAL-GENERATED-IMAGE-METADATA (July 2026) and should be reindexed. |
| 5 | `outbox_events WHERE aggregate_id = <asset_id>` ordered DESC, with STUCK rows highlighted | STUCK = `status='pending' AND attempt_count >= max_attempts - 2`. MAX_MISSING = pending row with `max_attempts` NULL or empty (the row can never reach its retry threshold because the threshold is missing). Both labels are emitted by the script and are pager-friendly grep tokens (operators can `\| grep MAX_MISSING` to find pending-forever rows). |

The script is the canonical **operator-side** companion to the production
write-back in
[`internal/application/images/storage_ingest_direct.go §18-…`](../internal/application/images/storage_ingest_direct.go).

## When to run

Run this script **after every benchmark / live-test cycle**, typically after
the aggregator has drained:

- `bash tests/operational/stock_e2e_full_battery.sh` (stock pipeline battery)
- `bash tests/operational/pacquiao_broner_script_smoke.sh` (script-generation smoke)
- Any one-shot benchmark or live-search probe that emitted an
  `asset.index.requested` outbox event

If a benchmark emits a known `asset_id` (or you have one from a probe), pipe
it through:

```bash
ASSET_ID="<the id from the benchmark probe>"
bash scripts/operations/inspect_media_asset.sh "$ASSET_ID"
```

For a list of recently-touched asset ids, run:

```bash
sqlite3 "$VELOX_DB" \
  "SELECT id, media_type, created_at FROM media_assets
   WHERE created_at > datetime('now','-1 hour') ORDER BY created_at DESC LIMIT 20;"
```

## Usage

```text
bash scripts/operations/inspect_media_asset.sh <asset_id>
  [--db <PATH>]
  [--json]
  [-h | --help]
```

- `--db <PATH>` — overrides the SQLite DB path lookup. Default priority:
  `$VELOX_DB` → `$PROJECT_ROOT/data/media/media.db.sqlite` →
  `$PROJECT_ROOT/data/velox.db` → `/var/lib/velox/velox.db`.
- `--json` — emit a single-line JSON summary for CI integration. The
  human-readable PASS/FAIL output is suppressed.
- `-h | --help` — show USAGE.

## Exit codes (canonical for pager alerts)

| Code | Meaning | Suggested action |
|---|---|---|
| 0 | All 4 invariants PASS | None. |
| 1 | ≥1 invariant FAIL | Re-run, capture `FAIL_LINES`. For dim failures, check the SigLIP sidecar; for `embedding_version_visual` failures, check the embedding-version constant in `internal/platform/qdrant/schema/schema.go`. |
| 2 | `asset_id` not found in `media_assets` | Verify the id — typos are common; `media_assets.id` is a `TEXT PRIMARY KEY` (no auto-increment). |
| 3 | SQLite DB not found or unreadable | Set `$VELOX_DB` or pass `--db` explicitly. |
| 4 | `sqlite3` not on PATH | `apt install sqlite3` (Debian/Ubuntu) or `brew install sqlite` (macOS). |
| 5 | Bad CLI usage | Re-read USAGE; `<asset_id>` is required. |

## Typical invocation

```bash
# After a stock pipeline benchmark:
ASSET_ID="sha256:abc123…"   # from the benchmark probe output
bash scripts/operations/inspect_media_asset.sh "$ASSET_ID"
```

Sample output (PASS case):

```
━━━ Inspect media_asset sha256:abc123… ━━
db=/srv/velox/data/media/media.db.sqlite

── asset row ──
·        media_type=image   provider=google-slides   origin=generated
·        local_path=/srv/velox/data/media/images/2026/07/12/sha256-abc123.png

── outbox_events for aggregate_id=sha256:abc123… (ordered DESC) ──
  id     event_type                       status     att     max  last_error                                 completed_at          created_at
  42     asset.index.requested            completed  1       10   <none>                                     2026-07-12T18:42:09Z  2026-07-12T18:42:08Z
  39     asset.published                  completed  1       10   <none>                                     2026-07-12T18:41:55Z  2026-07-12T18:41:54Z

── summary ──
  passed: 5
  failed: 0
```

Sample output (FAIL case, dim drift):

```
━━━ Inspect media_asset sha256:def456… ━━
db=/srv/velox/data/media/media.db.sqlite

── asset row ──
·        media_type=image   provider=google-slides   origin=generated
·        local_path=/srv/velox/data/media/images/2026/07/12/sha256-def456.png
  ✓ PASS  media_type='image' is in allowlist
  ✓ PASS  local_path exists on disk (/srv/velox/data/media/images/2026/07/12/sha256-def456.png)
  ✗ FAIL  json_array_length(visual_embedding) = 512, expected 768
  ✓ PASS  embedding_version_visual='2026-06-16-v1' (canonical SigLIP model version)

── outbox_events for aggregate_id=sha256:def456… (ordered DESC) ──
…

── summary ──
  passed: 3
  failed: 1
```

## Forward pointers

- **Schema SSOT**: `internal/platform/qdrant/schema/schema.go::VisualEmbeddingDim` + `internal/platform/qdrant/schema/schema.go::VisualEmbeddingModelVersion`.
- **Write-back code**: `internal/application/images/storage_ingest_direct.go` (lines 110-… for the canonical 10-key metadata map).
- **Dim guard**: `internal/platform/qdrant/search/embedders.go::validateVisualEmbeddingDim` + `ErrInvalidVisualEmbeddingDim`.
- **Outbox DDL**: `migrations/sqlite/092_create_outbox_events.sql`.
- **media_assets DDL**: `migrations/sqlite/033_media_assets_youtube_video_id_index.sql` (table declared for the partial-index purpose; full schema accumulates incrementally).
- **Stock E2E battery runbook**: [`docs/operations/stock-e2e-runbook.md#§10.9`](./stock-e2e-runbook.md#109--post-run-inspection-forward-pointer). Bidirectional forward-pointer — this runbook IS the SSOT for post-run inspection of any battery that emits an `asset_id`.

## When this script itself should be updated

This runbook is the **canonical SSOT** for `scripts/operations/inspect_media_asset.sh` per godlike/06 one-canonical-owner-per-fact. Edit ONLY this file when the script's CLI shape or assertions change; bump [`docs/operations/stock-e2e-runbook.md#§10.9`](./stock-e2e-runbook.md#109--post-run-inspection-forward-pointer) in lockstep (machine-checkable: `git grep -l 'inspect-media-asset.md' docs/operations/`).
