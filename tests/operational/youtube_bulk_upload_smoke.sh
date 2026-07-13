#!/usr/bin/env bash
#
# youtube_bulk_upload_smoke.sh — 6-layer canary E2E for the youtube bulk-upload pipeline.
# godlike/06 SSOT (one-canonical-owner-per-fact): this wrapper reads the canonical
# payload from tests/operational/youtube_bulk_upload_test.json and applies env-driven
# overrides (Idempotency-Key, dry-run, batch-size) via lib/common.sh.
# godlike/07 fail-closed: a capability-gate refuses to register as green
# when qdrant_health / storage / clips are NOT_MOUNTED. AGENTS.md
# "Never represent an unavailable backend as a successful no-op" —
# we DO NOT fake green.
#
# Usage:
#   ./youtube_bulk_upload_smoke.sh            # full 6-layer run
#   ./youtube_bulk_upload_smoke.sh --dry-only # dry-run only (layer 1)
#   ./youtube_bulk_upload_smoke.sh --no-qd    # skip Qdrant layer (for sandbox)
#
# Required env (resolved by lib/common.sh::smoke_resolve_token):
#   VELOX_ADMIN_TOKEN    admin bearer token (mandatory if TOKEN_FILE unset)
#   TOKEN_FILE           env file containing VELOX_ADMIN_TOKEN=...

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

PAYLOAD_FILE="${PAYLOAD_FILE:-$DIR/youtube_bulk_upload_test.json}"

DRY_RUN="0"
SKIP_QDRANT="0"
MODE_DRY_ONLY="0"

while [[ $# -gt 0 ]]; do
    case "$1" in
        -h|--help)
            sed -n '2,30p' "$0"; exit 0 ;;
        --dry)
            DRY_RUN="1"; shift ;;
        --dry-only)
            MODE_DRY_ONLY="1"; DRY_RUN="1"; shift ;;
        --no-qd)
            SKIP_QDRANT="1"; shift ;;
        *) printf 'unknown arg: %s\n' "$1" >&2; exit 2 ;;
    esac
done

smoke_require jq sqlite3 curl

if [[ ! -f "$PAYLOAD_FILE" ]]; then
    printf '%ssetup error: payload not found: %s%s\n' "$RED" "$PAYLOAD_FILE" "$RESET" >&2
    exit 2
fi

# godlike/07 — capability gate fires BEFORE any assertion so we never
# report "green" against an unavailable backend.
CAPS=$(curl -s --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" \
        "${SMOKE_API_BASE%/}/api/capabilities")
CLIPS_CAP=$(echo "$CAPS" | jq -r '.capabilities.clips // "UNKNOWN"')
QDRANT_CAP=$(echo "$CAPS" | jq -r '.capabilities.qdrant_health // "UNKNOWN"')
STORAGE_CAP=$(echo "$CAPS" | jq -r '.capabilities.storage // "UNKNOWN"')

smoke_log_section "Capability gate"
printf '  clips:         %s\n' "$CLIPS_CAP"
printf '  qdrant_health: %s\n' "$QDRANT_CAP"
printf '  storage:       %s\n' "$STORAGE_CAP"

if [[ "$CLIPS_CAP" != "MOUNTED" ]]; then
    printf '%sFAIL: clips capability is %s (required MOUNTED)%s\n' "$RED" "$CLIPS_CAP" "$RESET" >&2
    exit 1
fi
if [[ "$QDRANT_CAP" != "MOUNTED" && "$SKIP_QDRANT" != "1" ]]; then
    printf '%sFAIL: qdrant_health is %s (required MOUNTED; pass --no-qd to skip)%s\n' "$RED" "$QDRANT_CAP" "$RESET" >&2
    exit 1
fi
if [[ "$STORAGE_CAP" != "MOUNTED" ]]; then
    printf '%sFAIL: storage capability is %s (required MOUNTED for layer 4)%s\n' "$RED" "$STORAGE_CAP" "$RESET" >&2
    exit 1
fi

# Discover canonical DB + clips table.
DB_PATH=""
for cand in \
    /tmp/velox-sandbox-e2e/media/media.db.sqlite \
    "${VELOX_DATA_DIR:-}/media/media.db.sqlite" \
    /home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/data/media/media.db.sqlite ; do
    if [[ -n "$cand" && -f "$cand" ]]; then
        DB_PATH="$cand"
        if [[ "$DB_PATH" == "/tmp/velox-sandbox-e2e/media/media.db.sqlite" ]]; then break; fi
    fi
done
if [[ -z "$DB_PATH" ]]; then
    printf '%ssetup error: canonical media DB not found%s\n' "$RED" "$RESET" >&2
    exit 2
fi
printf '  db_path:      %s\n' "$DB_PATH"
CLIPS_TABLE=$(sqlite3 "$DB_PATH" "SELECT name FROM sqlite_master WHERE type='table' AND name IN ('clips','media_assets') ORDER BY name LIMIT 1;")
if [[ -z "$CLIPS_TABLE" ]]; then
    printf '%ssetup error: neither clips nor media_assets table present in %s%s\n' "$RED" "$DB_PATH" "$RESET" >&2
    exit 2
fi
printf '  clips_table:  %s\n' "$CLIPS_TABLE"

SOURCE=$(jq -r '.source' "$PAYLOAD_FILE")
EXPECTED_COUNT=$(jq -r '.options.expected_local_count' "$PAYLOAD_FILE")
EXPECTED_DIM=$(jq -r '.options.embed_dimensions // 768' "$PAYLOAD_FILE")
BULK_PATH="/api/media/clips/$SOURCE/clips/bulk-upload-youtube-clips"

# ── Layer 1: POST dry-run ────────────────────────────────────────────────
smoke_log_section "Layer 1: POST dry-run"
smoke_curl POST "${BULK_PATH}?dry_run=true" --data @"$PAYLOAD_FILE" >/dev/null
HTTP1="$SMOKE_LAST_HTTP"
if [[ "$HTTP1" != "200" && "$HTTP1" != "202" ]]; then
    printf '%sFAIL [layer 1]: dry-run HTTP %s (accepted: 200, 202)%s\n' "$RED" "$HTTP1" "$RESET" >&2
    smoke_echo_safe "$(head -c 600 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi
DRY_FOUND=$(jq -r '.local_found // .candidates // 0' "$SMOKE_LAST_BODY" 2>/dev/null || echo 0)
if [[ "$DRY_FOUND" != "$EXPECTED_COUNT" ]]; then
    printf '%sFAIL [layer 1]: dry-run local_found=%s (expected %s)%s\n' "$RED" "$DRY_FOUND" "$EXPECTED_COUNT" "$RESET" >&2
    smoke_echo_safe "$(head -c 600 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi
printf '  dry-run local_found: %s\n' "$DRY_FOUND"

if [[ "$MODE_DRY_ONLY" == "1" ]]; then
    printf '%sOK: dry-run only passed%s\n' "$GREEN" "$RESET"
    exit 0
fi

# Count rows BEFORE real-run so the idempotency layer (8) can detect duplicates.
PRE_RUN_COUNT=$(sqlite3 "$DB_PATH" "SELECT count(*) FROM $CLIPS_TABLE WHERE source='$SOURCE';")
printf '  pre-run clip rows for source=%s: %s\n' "$SOURCE" "$PRE_RUN_COUNT"

# ── Layer 2: POST real-run ────────────────────────────────────────────────
smoke_log_section "Layer 2: POST real-run"
IDEM_KEY=$(smoke_gen_uuid)
smoke_curl POST "$BULK_PATH" \
    -H "Idempotency-Key: $IDEM_KEY" \
    --data @"$PAYLOAD_FILE" >/dev/null
HTTP2="$SMOKE_LAST_HTTP"
if [[ "$HTTP2" != "202" && "$HTTP2" != "200" ]]; then
    printf '%sFAIL [layer 2]: real-run HTTP %s (accepted: 200, 202)%s\n' "$RED" "$HTTP2" "$RESET" >&2
    smoke_echo_safe "$(head -c 600 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi
JOB_ID=$(jq -r '.job_id // ""' "$SMOKE_LAST_BODY")
if [[ -z "$JOB_ID" || "$JOB_ID" == "null" ]]; then
    printf '%sFAIL [layer 2]: real-run returned no job_id%s\n' "$RED" "$RESET" >&2
    smoke_echo_safe "$(head -c 600 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi
printf '  job_id:  %s\n' "$JOB_ID"
printf '  idem:    %s\n' "$IDEM_KEY"

# ── Layer 3: Poll job to terminal ──────────────────────────────────────────
smoke_log_section "Layer 3: poll /api/jobs/${JOB_ID}/full"
if ! smoke_poll_terminal "$JOB_ID"; then
    rc=$?
    printf '%sFAIL [layer 3]: polling did not reach terminal (rc=%d, status=%s)%s\n' "$RED" "$rc" "${SMOKE_LAST_STATUS:-?}" "$RESET" >&2
    exit 1
fi
FINAL_STATUS="$SMOKE_LAST_STATUS"
if [[ "$FINAL_STATUS" != "completed" && "$FINAL_STATUS" != "SUCCEEDED" ]]; then
    printf '%sFAIL [layer 3]: terminal status=%s (expected completed|SUCCEEDED)%s\n' "$RED" "$FINAL_STATUS" "$RESET" >&2
    exit 1
fi
printf '  final status: %s\n' "$FINAL_STATUS"

# ── Layer 4: SQLite verify ─────────────────────────────────────────────────
smoke_log_section "Layer 4: SQLite (clips table state)"
ACTIVE_IDX=$(sqlite3 "$DB_PATH" \
    "SELECT count(*) FROM $CLIPS_TABLE WHERE source='$SOURCE'
        AND lifecycle_state='ACTIVE' AND index_state='INDEXED'
        AND embed_blob IS NOT NULL AND local_path IS NOT NULL
        AND local_path != '' AND drive_link IS NOT NULL AND drive_link != '';")
if [[ "$ACTIVE_IDX" != "$EXPECTED_COUNT" ]]; then
    printf '%sFAIL [layer 4]: ACTIVE+INDEXED+embedded+local+drive rows=%s (expected %s)%s\n' "$RED" "$ACTIVE_IDX" "$EXPECTED_COUNT" "$RESET" >&2
    sqlite3 -header -column "$DB_PATH" \
        "SELECT id, source, lifecycle_state, index_state, length(embed_blob) AS embed_len,
                local_path, drive_link
         FROM $CLIPS_TABLE WHERE source='$SOURCE';" >&2
    exit 1
fi
printf '  ACTIVE+INDEXED+embedded+local+drive rows: %s\n' "$ACTIVE_IDX"

SEARCH_ROOTS=()
[[ -n "${VELOX_DATA_DIR:-}" ]] && SEARCH_ROOTS+=("$VELOX_DATA_DIR")
DB_DATA_ROOT="$(dirname "$DB_PATH")/../data"
[[ -d "$DB_DATA_ROOT" ]] && SEARCH_ROOTS+=("$DB_DATA_ROOT")
SEARCH_ROOTS+=(/tmp/velox-sandbox-e2e /home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored)
DISTINCT_PATHS=$(sqlite3 "$DB_PATH" \
    "SELECT DISTINCT local_path FROM $CLIPS_TABLE WHERE source='$SOURCE' AND local_path IS NOT NULL AND local_path != '';")
MISSING_ON_DISK=()
while IFS= read -r lp; do
    [[ -z "$lp" ]] && continue
    found=0
    if [[ -f "$lp" ]]; then found=1; else
        for root in "${SEARCH_ROOTS[@]}"; do
            [[ -f "$root/$lp" ]] && found=1 && break
        done
    fi
    [[ "$found" -eq 0 ]] && MISSING_ON_DISK+=("$lp")
done <<< "$DISTINCT_PATHS"
if [[ "${#MISSING_ON_DISK[@]}" -gt 0 ]]; then
    printf '%sFAIL [layer 4]: %s local_path files missing on disk:%s\n' "$RED" "${#MISSING_ON_DISK[@]}" "$RESET" >&2
    for p in "${MISSING_ON_DISK[@]}"; do printf '  missing: %s\n' "$p" >&2; done
    exit 1
fi
printf '  local_path files present on disk: OK (%s distinct paths)\n' "$(echo "$DISTINCT_PATHS" | wc -l)"

# ── Layer 5: Outbox ────────────────────────────────────────────────────────
smoke_log_section "Layer 5: Outbox"
OUTBOX_TABLE=$(sqlite3 "$DB_PATH" \
    "SELECT name FROM sqlite_master WHERE type='table' AND name='outbox' LIMIT 1;")
if [[ -z "$OUTBOX_TABLE" ]]; then
    printf '  outbox table not present — skipping layer 5 (no outbox in this composition)\n'
else
    PENDING_OUTBOX=$(sqlite3 "$DB_PATH" \
        "SELECT count(*) FROM outbox WHERE status IN ('pending','processing','retry','failed');")
    if [[ "$PENDING_OUTBOX" -ne 0 ]]; then
        printf '%sFAIL [layer 5]: outbox pending=%s (expected 0)%s\n' "$RED" "$PENDING_OUTBOX" "$RESET" >&2
        sqlite3 -header -column "$DB_PATH" \
            "SELECT id, status, attempts, last_error FROM outbox WHERE status IN ('pending','processing','retry','failed') LIMIT 10;" >&2
        exit 1
    fi
    printf '  outbox pending/processing/retry/failed rows: 0\n'
fi

# ── Layer 6: Qdrant ────────────────────────────────────────────────────────
if [[ "$SKIP_QDRANT" != "1" ]]; then
    smoke_log_section "Layer 6: Qdrant"
    QDRANT_BASE=$(jq -r '.qdrant_url // "http://127.0.0.1:6333"' "$PAYLOAD_FILE" 2>/dev/null || echo "http://127.0.0.1:6333")
    QCOL=$(curl -s --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" "$QDRANT_BASE/collections" | jq -r '.result.collections[].name' 2>/dev/null | head -1)
    if [[ -z "$QCOL" ]]; then
        printf '%sFAIL [layer 6]: no Qdrant collection found at %s%s\n' "$RED" "$QDRANT_BASE" "$RESET" >&2
        exit 1
    fi
    POINTS=$(curl -s --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" "$QDRANT_BASE/collections/$QCOL" | jq -r '.result.points_count // 0')
    VECTOR_DIM=$(curl -s --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" "$QDRANT_BASE/collections/$QCOL" | jq -r '.result.config.params.vectors.size // 0')
    if [[ -z "$POINTS" || "$POINTS" -lt "$EXPECTED_COUNT" ]]; then
        printf '%sFAIL [layer 6]: Qdrant points_count=%s for collection=%s (expected >= %s)%s\n' "$RED" "$POINTS" "$QCOL" "$EXPECTED_COUNT" "$RESET" >&2
        exit 1
    fi
    printf '  Qdrant collection:  %s\n' "$QCOL"
    printf '  Qdrant points_count: %s\n' "$POINTS"
    printf '  Qdrant vector dim:  %s\n' "$VECTOR_DIM"
    if [[ "$VECTOR_DIM" != "$EXPECTED_DIM" ]]; then
        printf '%sFAIL [layer 6]: Qdrant vector_dim=%s (expected %s) — 6-layer contract violated%s\n' "$RED" "$VECTOR_DIM" "$EXPECTED_DIM" "$RESET" >&2
        exit 1
    fi
fi

# ── Layer 7: Search ────────────────────────────────────────────────────────
smoke_log_section "Layer 7: GET /api/media/clips/search"
smoke_curl GET "/api/media/clips/search?source=$SOURCE&limit=10" >/dev/null
SEARCH_HTTP="$SMOKE_LAST_HTTP"
if [[ "$SEARCH_HTTP" != "200" && "$SEARCH_HTTP" != "202" ]]; then
    printf '%sFAIL [layer 7]: search HTTP %s (expected 200|202)%s\n' "$RED" "$SEARCH_HTTP" "$RESET" >&2
    exit 1
fi
SEARCH_COUNT=$(jq -r '(.results // .items // .clips // []) | length' "$SMOKE_LAST_BODY" 2>/dev/null || echo 0)
if [[ "$SEARCH_COUNT" -lt "$EXPECTED_COUNT" ]]; then
    printf '%sFAIL [layer 7]: search returned %s clips (expected >= %s)%s\n' "$RED" "$SEARCH_COUNT" "$EXPECTED_COUNT" "$RESET" >&2
    exit 1
fi
printf '  search hits: %s\n' "$SEARCH_COUNT"

# ── Layer 8: Idempotency re-run ────────────────────────────────────────────
smoke_log_section "Layer 8: idempotency re-run with same Idempotency-Key"
smoke_curl POST "$BULK_PATH" \
    -H "Idempotency-Key: $IDEM_KEY" \
    --data @"$PAYLOAD_FILE" >/dev/null
IDEMP_HTTP="$SMOKE_LAST_HTTP"
IDEMP_MSG=$(jq -r '.message // ""' "$SMOKE_LAST_BODY" 2>/dev/null || echo "")
printf '  idempotent re-run HTTP: %s  message: %s\n' "$IDEMP_HTTP" "$IDEMP_MSG"
if [[ "$IDEMP_HTTP" != "200" && "$IDEMP_HTTP" != "202" && "$IDEMP_HTTP" != "409" ]]; then
    printf '%sFAIL [layer 8]: idempotent re-run HTTP %s (expected 200|202|409)%s\n' "$RED" "$IDEMP_HTTP" "$RESET" >&2
    exit 1
fi
POST_RUN_COUNT=$(sqlite3 "$DB_PATH" \
    "SELECT count(*) FROM $CLIPS_TABLE WHERE source='$SOURCE';")
if [[ "$POST_RUN_COUNT" -ne "$PRE_RUN_COUNT" ]]; then
    printf '%sFAIL [layer 8]: row count changed %s -> %s (idempotency broken)%s\n' "$RED" "$PRE_RUN_COUNT" "$POST_RUN_COUNT" "$RESET" >&2
    exit 1
fi
printf '  clip row count unchanged: %s (idempotency OK)\n' "$POST_RUN_COUNT"

printf '\n%sOK: 6-layer youtube-bulk-upload canary smoke PASSED on %s%s\n' "$GREEN" "$(date -Iseconds)" "$RESET"
exit 0
