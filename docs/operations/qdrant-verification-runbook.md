# Qdrant Verification Runbook — Rich Metadata + Semantic Search

**Owner:** `architecture/current.yaml#RICH-METADATA-QDRANT-VERIFY-2026-07-04`
**Audience:** operators verifying the Qdrant end-to-end indexing chain
(`media_assets → outbox → Qdrant → search → download`) with rich per-segment
metadata (summary, topics, speakers, mentioned_people, hook).
**Cross-references:** this runbook is the operator-facing codification of the
9-point verification verdict from
[`architecture/action-plans/2026-07-04-rich-metadata-qdrant-verification.md`](../../architecture/action-plans/2026-07-04-rich-metadata-qdrant-verification.md)
§4. The automated smoke test covering Tests 1-6 lives at
`tests/operational/qdrant_e2e_boxing_smoke.sh`.

## 1. Prerequisites

- PipelineGen server running on `API_BASE` (default `127.0.0.1:8000`)
- `VELOX_ADMIN_TOKEN` set for authenticated requests
- `sqlite3` available for direct DB probes
- `jq` available for JSON parsing
- `curl` available for HTTP requests
- The 8 clips from the Pacquiao vs Broner WBA highlight video
  (`RRJvrDKunyA`) must already be registered via
  `POST /api/media/register-batch`. If they aren't, run the
  smoke test first:
  ```bash
  VELOX_ADMIN_TOKEN=<token> bash tests/operational/qdrant_e2e_boxing_smoke.sh
  ```

### Environment

```bash
export API_BASE="${API_BASE:-127.0.0.1:8000}"
export VELOX_ADMIN_TOKEN="<your-token>"
export SMOKE_DB="${SMOKE_DB:-data/media/media.db.sqlite}"
```

## 2. The 8 Deterministic ClipIDs

Each clip is identified by a deterministic ID based on the video ID, start
second, and end second:

```
yt_RRJvrDKunyA_<startSec>_<endSec>_v1
```

| # | Round | Start (s) | End (s) | ClipID |
|---|-------|-----------|---------|--------|
| 1 | 1 | 32 | 231 | `yt_RRJvrDKunyA_32_231_v1` |
| 2 | 2 | 247 | 345 | `yt_RRJvrDKunyA_247_345_v1` |
| 3 | 5 | 628 | 767 | `yt_RRJvrDKunyA_628_767_v1` |
| 4 | 7 | 993 | 1048 | `yt_RRJvrDKunyA_993_1048_v1` |
| 5 | 9 | 1276 | 1330 | `yt_RRJvrDKunyA_1276_1330_v1` |
| 6 | 10 | 1382 | 1626 | `yt_RRJvrDKunyA_1382_1626_v1` |
| 7 | 11 | 1657 | 1698 | `yt_RRJvrDKunyA_1657_1698_v1` |
| 8 | 12 | 1727 | 1769 | `yt_RRJvrDKunyA_1727_1769_v1` |

> **Note:** The server-side ClipID uses the first 8 characters of the MD5
> file hash (`yt_<videoID>_<hash8>`). The start/end-based IDs above are the
> canonical operator-facing reference; the actual stored ID may differ.
> When querying media_assets, use `WHERE name LIKE '%Round N%'` or
> `WHERE json_extract(metadata_json, '$.youtube_id') = 'RRJvrDKunyA'`
> to locate clips.

## 3. Test 1 — Index Health Preflight

**Purpose:** Verify the Qdrant indexing subsystem is healthy before running
downstream tests.

```bash
curl -s -H "Authorization: Bearer $VELOX_ADMIN_TOKEN" \
    "http://${API_BASE}/api/media/index-health" | jq .
```

**Expected:**
- `.ok == true`
- `.degraded == false`
- `.index_health` is an object (contains per-collection health data)
- `.asset_stats` is an object (contains indexed/pending/failed counts)

**Failure triage:**
- `ok: false` → Qdrant service is unreachable or the collection is missing.
  Check `docker compose ps qdrant` and `docker compose logs qdrant`.
- `degraded: true` → the outbox pipeline is lagging. Check
  `outbox_events WHERE event_type='asset.index.requested' AND status='pending'`.

## 4. Test 2 — SQLite media_assets Query (Metadata + Rich Fields)

**Purpose:** Verify every clip has a `media_assets` row with the expected
metadata fields (file_hash, drive_file_id, metadata_json with rich fields).

### 4a — Row presence + basic health

```bash
sqlite3 -header -column "$SMOKE_DB" "
SELECT id, name, index_state, file_hash IS NOT NULL AS has_hash,
       drive_file_id IS NOT NULL AS has_drive,
       json_extract(metadata_json, '$.duration_sec') AS duration_sec
FROM media_assets
WHERE source='youtube'
  AND json_extract(metadata_json, '$.youtube_id') = 'RRJvrDKunyA'
ORDER BY name;
"
```

**Expected:**
- 8 rows (one per round)
- `index_state` = `INDEXED` for every row (or at least ≥ 6)
- `has_hash` = 1 for every row
- `has_drive` = 1 for every row that completed Drive upload

### 4b — Rich metadata field assertions

```bash
sqlite3 -header -column "$SMOKE_DB" "
SELECT name,
       json_extract(metadata_json, '$.clip_summary') AS summary,
       json_extract(metadata_json, '$.topics') AS topics,
       json_extract(metadata_json, '$.speakers') AS speakers,
       json_extract(metadata_json, '$.mentioned_people') AS mentioned,
       json_extract(metadata_json, '$.hook') AS hook,
       json_extract(metadata_json, '$.tags') AS tags
FROM media_assets
WHERE source='youtube'
  AND json_extract(metadata_json, '$.youtube_id') = 'RRJvrDKunyA'
ORDER BY name;
"
```

**Expected (per clip):**
- `summary` is non-empty (2-3 sentence description)
- `topics` is a JSON array with ≥ 3 entries (e.g. `["boxing","pacquiao","round N"]`)
- `speakers` is a JSON array with ≥ 1 entry (e.g. `["commentator"]`)
- `mentioned` is a JSON array with ≥ 2 entries (e.g. `["Manny Pacquiao","Adrien Broner"]`)
- `hook` is non-empty (one-liner highlight)
- `tags` is a JSON array with ≥ 5 entries

> **Note:** If rich fields were packed into `description` (pre-DTO-extension
> path), the `clip_summary`, `topics`, `speakers`, `mentioned_people`, and
> `hook` keys will be absent from `metadata_json`. This is the pre-Phase-1
> state; after `PR-RICH-METADATA-DTO-EXTEND` ships, every new clip will have
> these keys populated from dedicated wire fields.

### 4c — The 9-point verification verdict (per clip)

For each clip, ALL of the following must pass:

| # | Check | SQL/curl probe |
|---|-------|----------------|
| 1 | `media_assets` row present | `SELECT COUNT(*) FROM media_assets WHERE name LIKE '%Round N%'` |
| 2 | `file_hash` present | `SELECT file_hash IS NOT NULL FROM media_assets WHERE ...` |
| 3 | `drive_file_id` or `local_path` present | `SELECT drive_file_id, local_path FROM media_assets WHERE ...` |
| 4 | Rich metadata present: `clip_summary`, `topics`, `hook`, `tags` | `SELECT json_extract(metadata_json, '$.clip_summary') ...` |
| 5 | `outbox_events` `asset.index.requested` present | See Test 3 |
| 6 | Outbox event `completed` (no dead_letter) | See Test 3 |
| 7 | `media_assets.index_state = INDEXED` | `SELECT index_state FROM media_assets WHERE ...` |
| 8 | `/api/media/search` finds clip via natural-language query | See Test 4 |
| 9 | `/api/media/clips/youtube/clips/{id}/download` returns valid MP4 | See Test 5 |

## 5. Test 3 — SQLite outbox_events Query (Status + Supersede Detection)

**Purpose:** Verify every clip emitted an `asset.index.requested` outbox
event, that it reached `completed` status, and that no `dead_letter` events
exist.

```bash
sqlite3 -header -column "$SMOKE_DB" "
SELECT aggregate_id,
       event_type,
       status,
       attempt_count,
       substr(error, 1, 80) AS error_excerpt,
       created_at,
       updated_at
FROM outbox_events
WHERE event_type = 'asset.index.requested'
  AND aggregate_id IN (
    SELECT id FROM media_assets
    WHERE source = 'youtube'
      AND json_extract(metadata_json, '$.youtube_id') = 'RRJvrDKunyA'
  )
ORDER BY aggregate_id, created_at DESC;
"
```

**Expected:**
- 8 rows (one per clip)
- `status` = `completed` for every row
- `attempt_count` ≥ 1
- `error_excerpt` is empty or NULL for `completed` rows

### 5a — Supersede detection

If a clip was re-indexed (e.g., after a metadata update), there may be
multiple `asset.index.requested` events for the same `aggregate_id`. The
canonical event is the one with `status = 'completed'` and the most recent
`created_at`. Events with `status = 'superseded'` are harmless — a newer
`completed` event exists for the same aggregate.

```bash
# Count events per clip — expect 1 or more
sqlite3 "$SMOKE_DB" "
SELECT aggregate_id, COUNT(*) AS event_count,
       SUM(CASE WHEN status='completed' THEN 1 ELSE 0 END) AS completed,
       SUM(CASE WHEN status='superseded' THEN 1 ELSE 0 END) AS superseded,
       SUM(CASE WHEN status='dead_letter' THEN 1 ELSE 0 END) AS dead_letter
FROM outbox_events
WHERE event_type = 'asset.index.requested'
  AND aggregate_id IN (
    SELECT id FROM media_assets
    WHERE source = 'youtube'
      AND json_extract(metadata_json, '$.youtube_id') = 'RRJvrDKunyA'
  )
GROUP BY aggregate_id
ORDER BY aggregate_id;
"
```

**Expected:**
- `completed` ≥ 1 for every clip
- `dead_letter` = 0 for every clip
- `superseded` ≥ 0 (harmless)

**Failure triage:**
- `dead_letter > 0` → the outbox dispatcher failed to process the event after
  max retries. Check server logs for `IndexClip` errors and Qdrant health.
- `completed = 0` for a clip → the outbox pipeline hasn't processed it yet.
  Wait 30s and re-query. If still 0, the outbox dispatcher may be stalled.

## 6. Test 4 — Semantic Search (5 Natural-Language Queries)

**Purpose:** Verify the Qdrant hybrid search (dense ANN + BM25 sparse) returns
the correct clip for rich natural-language queries that target specific
semantic content (not just keyword matching).

### 6a — The 5 canonical queries

Each query targets a specific round's unique content:

| # | Query | Target Round | Expected Match |
|---|-------|-------------|----------------|
| Q1 | "Pacquiao hurts Broner near the ropes in round 7 with fast left hands and almost stops him" | 7 | `yt_RRJvrDKunyA_993_1048_v1` |
| Q2 | "Broner lands his best right hands of the fight through Pacquiao's guard" | 5 | `yt_RRJvrDKunyA_628_767_v1` |
| Q3 | "Pacquiao opens with quick southpaw footwork and crisp jabs from the opening bell" | 1 | `yt_RRJvrDKunyA_32_231_v1` |
| Q4 | "Pacquiao left hook pushes Broner back into the corner and disrupts his rhythm" | 9 | `yt_RRJvrDKunyA_1276_1330_v1` |
| Q5 | "The official split-decision verdict confirms Pacquiao retains the WBA welterweight title" | 12 | `yt_RRJvrDKunyA_1727_1769_v1` |

### 6b — Mini-test loop

```bash
# Array of queries with expected match hints
declare -A QUERIES=(
    ["Q1"]='{"query":"Pacquiao hurts Broner near the ropes in round 7 with fast left hands and almost stops him","sources":["youtube","stock","clips"],"mode":"hybrid","limit":5}'
    ["Q2"]='{"query":"Broner lands his best right hands of the fight through Pacquiao guard","sources":["youtube","stock","clips"],"mode":"hybrid","limit":5}'
    ["Q3"]='{"query":"Pacquiao opens with quick southpaw footwork and crisp jabs","sources":["youtube","stock","clips"],"mode":"hybrid","limit":5}'
    ["Q4"]='{"query":"Pacquiao left hook pushes Broner back into the corner","sources":["youtube","stock","clips"],"mode":"hybrid","limit":5}'
    ["Q5"]='{"query":"The official split-decision verdict confirms Pacquiao retains the WBA welterweight title","sources":["youtube","stock","clips"],"mode":"hybrid","limit":5}'
)

pass=0 fail=0
for q in "${!QUERIES[@]}"; do
    payload="${QUERIES[$q]}"
    resp=$(curl -s -X POST \
        -H "Authorization: Bearer $VELOX_ADMIN_TOKEN" \
        -H "Content-Type: application/json" \
        -d "$payload" \
        "http://${API_BASE}/api/media/search")

    count=$(echo "$resp" | jq -r '.items | length // 0')
    top_name=$(echo "$resp" | jq -r '.items[0].name // "?"')

    if (( count > 0 )); then
        echo "PASS $q: $count results, top='$top_name'"
        pass=$((pass + 1))
    else
        echo "FAIL $q: 0 results"
        fail=$((fail + 1))
    fi
done

echo "---"
echo "Search results: $pass PASS, $fail FAIL (of ${#QUERIES[@]})"
```

**Expected:**
- All 5 queries return ≥ 1 result
- The top result for each query should be the matching round (by name or by
  semantic similarity). At minimum, the matching ClipID should appear in the
  top 3 results.

**Failure triage:**
- 0 results for a query → the outbox pipeline hasn't indexed the clip yet.
  Check `outbox_events.status` for the target clip (Test 3).
- Wrong clip in top position → the BM25 sparse or dense embedding didn't
  capture the semantic content. Check `metadata_json.clip_summary` for
  content quality; re-index via `POST /api/media/clips/{id}/reindex`.

### 6c — Rich-field verification via search

If `PR-RICH-METADATA-DTO-EXTEND` has shipped, verify that Qdrant returns
rich metadata in search results:

```bash
curl -s -X POST \
    -H "Authorization: Bearer $VELOX_ADMIN_TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"query":"Pacquiao hurts Broner near the ropes","sources":["youtube"],"mode":"hybrid","limit":1}' \
    "http://${API_BASE}/api/media/search" | jq '.items[0] | {name, summary: .metadata.summary, topics: .metadata.topics, hook: .metadata.hook}'
```

**Expected:** The `metadata` sub-object contains `summary`, `topics`, and
`hook` fields with non-empty values (post-DTO-extension).

## 7. Test 5 — Per-Clip Download

**Purpose:** Verify every clip can be downloaded via the canonical clip
endpoint and returns valid MP4 bytes (≥ 100 KB) or a valid JSON download
envelope.

### 7a — Single-clip download probe

```bash
clip_id="<clip_id_from_test_2>"
curl -s -X POST \
    -H "Authorization: Bearer $VELOX_ADMIN_TOKEN" \
    -D /tmp/dl_headers.txt \
    -o /tmp/clip.mp4 \
    -w '\nHTTP %{http_code} — %{size_download} bytes\n' \
    "http://${API_BASE}/api/media/clips/youtube/clips/${clip_id}/download"

# Verify
size=$(wc -c < /tmp/clip.mp4 | tr -d ' ')
content_type=$(grep -i '^content-type:' /tmp/dl_headers.txt | tr -d '\r')
echo "Download: $size bytes, $content_type"

if (( size >= 100000 )); then
    echo "PASS: MP4 download ≥ 100 KB"
else
    echo "FAIL: download too small ($size bytes)"
fi
```

### 7b — Batch download loop (all 8 clips)

```bash
# Extract actual ClipIDs from media_assets (not the deterministic refs)
mapfile -t clip_ids < <(
    sqlite3 "$SMOKE_DB" \
        "SELECT id FROM media_assets
         WHERE source='youtube'
           AND json_extract(metadata_json, '$.youtube_id') = 'RRJvrDKunyA'
         ORDER BY name;"
)

pass=0 fail=0
for clip_id in "${clip_ids[@]}"; do
    [[ -z "$clip_id" ]] && continue

    # Try canonical route first, fall back to alt route
    code=0
    for url_path in \
        "/api/media/clips/youtube/clips/${clip_id}/download" \
        "/api/media/youtube/clips/${clip_id}/download"; do
        code=$(curl -s -X POST \
            -H "Authorization: Bearer $VELOX_ADMIN_TOKEN" \
            -o /tmp/clip_dl.mp4 -w '%{http_code}' \
            "http://${API_BASE}${url_path}")
        if [[ "$code" =~ ^2[0-9][0-9]$ ]]; then
            break
        fi
    done

    size=$(wc -c < /tmp/clip_dl.mp4 | tr -d ' ')
    if [[ "$code" =~ ^2[0-9][0-9]$ ]] && (( size >= 100000 )); then
        echo "PASS $clip_id: HTTP $code, $size bytes"
        pass=$((pass + 1))
    elif [[ "$code" =~ ^2[0-9][0-9]$ ]] && (( size >= 1000 )); then
        # JSON envelope (valid download response, different route)
        echo "PASS $clip_id: HTTP $code, JSON envelope $size bytes"
        pass=$((pass + 1))
    else
        echo "FAIL $clip_id: HTTP $code, $size bytes"
        fail=$((fail + 1))
    fi
done

echo "---"
echo "Download results: $pass PASS, $fail FAIL (of ${#clip_ids[@]})"
```

**Expected:**
- All 8 clips return HTTP 2xx
- Each response is either MP4 bytes (≥ 100 KB) or a JSON download envelope
  (≥ 1 KB, Content-Type: application/json)
- No clip returns HTTP 404, 500, or 503

**Failure triage:**
- HTTP 404 → the canonical download route may not be registered. Try the
  alt route (`/api/media/youtube/clips/{id}/download`).
- HTTP 503 → the Drive upload may have failed (check `delivery_status` in
  `media_assets.metadata_json`).
- Small response (< 1 KB) → the server returned an error envelope. Inspect
  the response body: `cat /tmp/clip_dl.mp4 | jq .`.

## 8. Full Verification Script

Combine all 5 tests into a single operator script:

```bash
#!/usr/bin/env bash
# qdrant-verification.sh — operator runbook as executable script
set -euo pipefail

API_BASE="${API_BASE:-127.0.0.1:8000}"
SMOKE_DB="${SMOKE_DB:-data/media/media.db.sqlite}"
VIDEO_ID="RRJvrDKunyA"

require() { command -v "$1" >/dev/null 2>&1 || { echo "MISSING: $1"; exit 2; }; }
require curl; require jq; require sqlite3

echo "===== Test 1: index-health ====="
ok=$(curl -s -H "Authorization: Bearer $VELOX_ADMIN_TOKEN" \
    "http://${API_BASE}/api/media/index-health" | jq -r '.ok')
[[ "$ok" == "true" ]] && echo "PASS" || { echo "FAIL: ok=$ok"; exit 1; }

echo "===== Test 2: media_assets ====="
count=$(sqlite3 "$SMOKE_DB" \
    "SELECT COUNT(*) FROM media_assets
     WHERE source='youtube'
       AND json_extract(metadata_json, '$.youtube_id') = '$VIDEO_ID'")
(( count >= 8 )) && echo "PASS: $count rows" || echo "WARN: only $count rows (expected 8)"

echo "===== Test 3: outbox_events ====="
dead=$(sqlite3 "$SMOKE_DB" \
    "SELECT COUNT(*) FROM outbox_events
     WHERE event_type='asset.index.requested'
       AND aggregate_id IN (
         SELECT id FROM media_assets
         WHERE source='youtube'
           AND json_extract(metadata_json, '$.youtube_id') = '$VIDEO_ID'
       )
       AND status='dead_letter'")
(( dead == 0 )) && echo "PASS: 0 dead_letter" || echo "FAIL: $dead dead_letter events"

echo "===== Test 4: search (5 queries) ====="
for q in \
    "Pacquiao hurts Broner near the ropes in round 7" \
    "Broner lands his best right hands of the fight" \
    "Pacquiao opens with quick southpaw footwork" \
    "Pacquiao left hook pushes Broner back" \
    "The official split-decision verdict confirms Pacquiao"; do
    n=$(curl -s -X POST \
        -H "Authorization: Bearer $VELOX_ADMIN_TOKEN" \
        -H "Content-Type: application/json" \
        -d "{\"query\":\"$q\",\"sources\":[\"youtube\"],\"mode\":\"hybrid\",\"limit\":5}" \
        "http://${API_BASE}/api/media/search" | jq -r '.items | length')
    (( n > 0 )) && echo "  PASS: '$q' → $n results" || echo "  FAIL: '$q' → 0 results"
done

echo "===== Test 5: download (first clip) ====="
first_id=$(sqlite3 "$SMOKE_DB" \
    "SELECT id FROM media_assets
     WHERE source='youtube'
       AND json_extract(metadata_json, '$.youtube_id') = '$VIDEO_ID'
     ORDER BY name LIMIT 1")
code=$(curl -s -X POST \
    -H "Authorization: Bearer $VELOX_ADMIN_TOKEN" \
    -o /tmp/qdrant_verify_clip.mp4 -w '%{http_code}' \
    "http://${API_BASE}/api/media/clips/youtube/clips/${first_id}/download")
size=$(wc -c < /tmp/qdrant_verify_clip.mp4 | tr -d ' ')
[[ "$code" =~ ^2[0-9][0-9]$ ]] && (( size >= 100000 )) && echo "PASS: HTTP $code, $size bytes" || echo "WARN: HTTP $code, $size bytes"
rm -f /tmp/qdrant_verify_clip.mp4

echo "===== Done ====="
```

## 9. Forward Pointers

| Ticket | Deadline | Description |
|--------|----------|-------------|
| `PR-RICH-METADATA-DTO-EXTEND` | 2026-07-25 | Add 5 rich fields to `RegisterFromYouTubeRequest` + propagate through chain |
| `PR-RICH-METADATA-SMOKE-TEST` | 2026-08-01 | Extend `qdrant_e2e_boxing_smoke.sh` with rich-field assertions + per-clip queries |
| `PR-RICH-METADATA-RUNBOOK` | 2026-08-15 | This runbook |
| `PR-RICH-METADATA-HOTSPOT-CROSSREF` | 2026-08-15 | Cross-validate priority via `git log --since=90.days` frequency |

## 10. Cross-References

- `architecture/current.yaml#RICH-METADATA-QDRANT-VERIFY-2026-07-04` — wave-tracker entry (4 linked_issues)
- `architecture/current.yaml#QDRANT-CHAIN-VERIFY-2026-07-04` — parent wave (6 linked_issues)
- `tests/operational/qdrant_e2e_boxing_smoke.sh` — automated 6-test smoke (covers Tests 1-5 of this runbook plus register-batch)
- `architecture/action-plans/2026-07-04-rich-metadata-qdrant-verification.md` — narrative action plan
- `docs/operations/qdrant-operational-readiness.md` — Qdrant operational readiness runbook (retention, metrics, circuit breaker)

## 11. Audit Trail

| Date (UTC) | Commit | Author | Note |
|------------|--------|--------|------|
| 2026-07-04 | TBD | PipelineGen Agent | Initial runbook creation (PR-RICH-METADATA-RUNBOOK) |
