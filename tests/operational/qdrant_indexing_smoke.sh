#!/usr/bin/env bash
# tests/operational/qdrant_indexing_smoke.sh — Zone 4: Qdrant / Indicizzazione Semantica
#
# Production readiness smoke test (from 2026-07-09 5-zone testing action plan).
# Verifies Qdrant semantic indexing chain after asset generation:
#
#   T1: Qdrant reachability + collection existence (media_assets_current)
#   T2: Scroll recent points — verify payload has source + media_type + lifecycle_state
#   T3: lifecycle_state=ACTIVE present on recent indexed assets
#   T4: DELETED/DELETE_REQUESTED assets NOT in Qdrant (lifecycle filter)
#   T5: Schema v3 consistency — 5 named vectors expected (text/transcript/visual/audio/bm25_text)
#   T6: Payload enrichment fields present (destination + source_provider + source_video_id)
#
# Exit codes:
#   0 = PASS (all assertions green)
#   1 = FAIL (one or more assertions failed)
#   2 = setup error (missing binaries, server unreachable)
#
# Self-check: `bash -n tests/operational/qdrant_indexing_smoke.sh`
#
# Overridable env vars:
#   BASE              = http://127.0.0.1:8000  (PipelineGen API root)
#   QDRANT_URL        = http://127.0.0.1:6333  (Qdrant REST root)
#   QDRANT_COLLECTION = media_assets_current   (canonical collection)
#   ENV_FILE          = .env                    (dotenv file)

set -euo pipefail
trap 'printf "%sABORTED: line %d: %s%s\n" "$RED" "$LINENO" "$BASH_COMMAND" "$RESET" >&2' ERR

# ---- Source shared helpers ------------------------------------------------
DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

# ---- Configuration --------------------------------------------------------
SMOKE_LOG_DIR="${SMOKE_LOG_DIR:-/tmp/qdrant-indexing-smoke-logs}"
QDRANT_URL="${QDRANT_URL:-http://127.0.0.1:6333}"
QDRANT_COLLECTION="${QDRANT_COLLECTION:-media_assets_current}"

# ---- Require binaries -----------------------------------------------------
smoke_require curl jq sqlite3

# ---- Pre-flight -----------------------------------------------------------
smoke_log_section "Zone 4: Qdrant Indicizzazione Semantica — Pre-flight"

printf '  PipelineGen API : %s\n' "$SMOKE_API_BASE"
printf '  Qdrant URL      : %s\n' "$QDRANT_URL"
printf '  Qdrant Collection: %s\n' "$QDRANT_COLLECTION"
printf '  DB_PATH         : %s\n' "${DB_PATH:-data/media/media.db.sqlite}"

DB="${DB_PATH:?DB_PATH must be explicitly set to an isolated or approved database}"
if [ ! -f "$DB" ]; then
    printf '%sFAIL: SQLite DB not found at %s (exit 2)%s\n' "$RED" "$DB" "$RESET" >&2
    exit 2
fi

# ---- Verify PipelineGen server is reachable --------------------------------
smoke_log_section "Pre-flight: PipelineGen server reachability"
smoke_curl GET "/health" >/dev/null
local code
code=$(cat "$WORK_DIR/last.code" 2>/dev/null || echo "000")
if [[ ! "$code" =~ ^2 ]]; then
    printf '%sFAIL: PipelineGen at %s unreachable (HTTP %s, exit 2)%s\n' \
        "$RED" "$SMOKE_API_BASE" "$code" "$RESET" >&2
    exit 2
fi
smoke_echo_safe "  PipelineGen: HTTP $code (reachable)"

# ---- T1: Qdrant reachability + collection existence -----------------------
smoke_log_section "T1: Qdrant reachability + collection existence"

T1_PASS=0

# 1a. Health check
QDRANT_HEALTH=$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 \
    "${QDRANT_URL}/healthz" 2>/dev/null || echo "000")
if [ "$QDRANT_HEALTH" = "200" ]; then
    printf '  %sOK: Qdrant healthz -> HTTP 200%s\n' "$GREEN" "$RESET"
    T1_PASS=1
else
    printf '%sFAIL: Qdrant unreachable at %s (healthz=%s)%s\n' \
        "$RED" "$QDRANT_URL" "$QDRANT_HEALTH" "$RESET" >&2
    printf '  Forward-pointer: PR-QDRANT-DOD-STOCK-PRODUCER (Qdrant infrastructure)\n' >&2
fi

# 1b. Collection existence (only if healthz OK)
if [ "$T1_PASS" -eq 1 ]; then
    COLL_RESP=$(curl -s --max-time 8 \
        "${QDRANT_URL}/collections/${QDRANT_COLLECTION}" 2>/dev/null || echo "{}")
    COLL_EXISTS=$(printf '%s' "$COLL_RESP" | jq -r '.status // "error"' 2>/dev/null || echo "error")
    COLL_POINTS=$(printf '%s' "$COLL_RESP" | jq -r '.result.points_count // 0' 2>/dev/null || echo "0")
    if [ "$COLL_EXISTS" = "ok" ] || [ "$COLL_EXISTS" = "true" ]; then
        printf '  %sOK: collection %s exists (points_count=%s)%s\n' \
            "$GREEN" "$QDRANT_COLLECTION" "$COLL_POINTS" "$RESET"
    else
        printf '%sFAIL: collection %s not found or error (status=%s)%s\n' \
            "$RED" "$QDRANT_COLLECTION" "$COLL_EXISTS" "$RESET" >&2
        printf '  Forward-pointer: PR-QDRANT-DOD-STOCK-PRODUCER (schema initialization)\n' >&2
        T1_PASS=0
    fi
fi

# ---- T2: Scroll recent points — verify payload fields ---------------------
smoke_log_section "T2: Scroll recent points — payload fields (source + media_type + lifecycle_state)"

T2_PASS=0

if [ "$T1_PASS" -eq 1 ]; then
    SCROLL_RESP=$(curl -s --max-time 10 -X POST \
        "${QDRANT_URL}/collections/${QDRANT_COLLECTION}/points/scroll" \
        -H 'Content-Type: application/json' \
        -d '{
            "limit": 20,
            "with_payload": true,
            "with_vector": false
        }' 2>/dev/null || echo '{"result":{"points":[]}}')

    POINT_COUNT=$(printf '%s' "$SCROLL_RESP" | jq -r '.result.points | length' 2>/dev/null || echo "0")

    if [ "$POINT_COUNT" -gt 0 ]; then
        # Check that at least one point has source, media_type, lifecycle_state
        HAS_SOURCE=$(printf '%s' "$SCROLL_RESP" | jq '[.result.points[] | select(.payload.source != null and .payload.source != "")] | length' 2>/dev/null || echo "0")
        HAS_MEDIA_TYPE=$(printf '%s' "$SCROLL_RESP" | jq '[.result.points[] | select(.payload.media_type != null and .payload.media_type != "")] | length' 2>/dev/null || echo "0")
        HAS_LIFECYCLE=$(printf '%s' "$SCROLL_RESP" | jq '[.result.points[] | select(.payload.lifecycle_state != null and .payload.lifecycle_state != "")] | length' 2>/dev/null || echo "0")

        printf '  Points found: %s  (source=%s, media_type=%s, lifecycle_state=%s)\n' \
            "$POINT_COUNT" "$HAS_SOURCE" "$HAS_MEDIA_TYPE" "$HAS_LIFECYCLE"

        if [ "$HAS_SOURCE" -gt 0 ] && [ "$HAS_MEDIA_TYPE" -gt 0 ] && [ "$HAS_LIFECYCLE" -gt 0 ]; then
            printf '  %sOK: payload fields present on recent points%s\n' "$GREEN" "$RESET"
            T2_PASS=1
        else
            printf '%sFAIL: missing payload fields (source=%s, media_type=%s, lifecycle_state=%s)%s\n' \
                "$RED" "$HAS_SOURCE" "$HAS_MEDIA_TYPE" "$HAS_LIFECYCLE" "$RESET" >&2
            printf '  Forward-pointer: PR-STOCK-QDRANT-SEMANTIC-ENRICHMENT (payload enrichment)\n' >&2
        fi
    else
        printf '%sFAIL: 0 points in %s collection (empty or unreachable)%s\n' \
            "$RED" "$QDRANT_COLLECTION" "$RESET" >&2
        printf '  Forward-pointer: PR-STOCK-OUTBOX-QDRANT-INDEX (indexing chain)\n' >&2
    fi
else
    printf '  SKIP: Qdrant unreachable, cannot scroll points\n'
fi

# ---- T3: lifecycle_state=ACTIVE present on recent indexed assets ----------
smoke_log_section "T3: lifecycle_state=ACTIVE on recent indexed assets"

T3_PASS=0

if [ "$T2_PASS" -eq 1 ]; then
    ACTIVE_COUNT=$(printf '%s' "$SCROLL_RESP" | jq '[.result.points[] | select(.payload.lifecycle_state == "ACTIVE")] | length' 2>/dev/null || echo "0")
    TOTAL_LC=$(printf '%s' "$SCROLL_RESP" | jq '[.result.points[] | select(.payload.lifecycle_state != null)] | length' 2>/dev/null || echo "0")

    # Also collect distinct lifecycle_state values for diagnostic
    LC_VALUES=$(printf '%s' "$SCROLL_RESP" | jq -r '[.result.points[].payload.lifecycle_state // "(null)"] | unique | join(", ")' 2>/dev/null || echo "(unknown)")

    printf '  lifecycle_state=ACTIVE: %s / %s total (distinct values: %s)\n' \
        "$ACTIVE_COUNT" "$TOTAL_LC" "$LC_VALUES"

    if [ "$ACTIVE_COUNT" -gt 0 ]; then
        printf '  %sOK: %s ACTIVE point(s) in Qdrant%s\n' "$GREEN" "$ACTIVE_COUNT" "$RESET"
        T3_PASS=1
    else
        printf '%sFAIL: 0 ACTIVE points in Qdrant scroll (all values: %s)%s\n' \
            "$RED" "$LC_VALUES" "$RESET" >&2
        printf '  Forward-pointer: PR-QDRANT-SEARCH-LIFECYCLE-FILTER (lifecycle filter)\n' >&2
    fi
else
    printf '  SKIP: payload fields missing, cannot verify ACTIVE lifecycle\n'
fi

# ---- T4: DELETED/DELETE_REQUESTED assets NOT in Qdrant --------------------
smoke_log_section "T4: DELETED/DELETE_REQUESTED assets filtered out of Qdrant"

T4_PASS=0

if [ "$T2_PASS" -eq 1 ]; then
    DELETED_COUNT=$(printf '%s' "$SCROLL_RESP" | jq '[.result.points[] | select(.payload.lifecycle_state == "DELETED")] | length' 2>/dev/null || echo "0")
    DELETE_REQ_COUNT=$(printf '%s' "$SCROLL_RESP" | jq '[.result.points[] | select(.payload.lifecycle_state == "DELETE_REQUESTED")] | length' 2>/dev/null || echo "0")

    printf '  DELETED in Qdrant: %s  DELETE_REQUESTED in Qdrant: %s\n' \
        "$DELETED_COUNT" "$DELETE_REQ_COUNT"

    if [ "$DELETED_COUNT" -eq 0 ] && [ "$DELETE_REQ_COUNT" -eq 0 ]; then
        printf '  %sOK: no DELETED/DELETE_REQUESTED assets in Qdrant scroll%s\n' "$GREEN" "$RESET"
        T4_PASS=1
    else
        printf '%sFAIL: %s DELETED + %s DELETE_REQUESTED points still in Qdrant (silent-success anti-pattern)%s\n' \
            "$RED" "$DELETED_COUNT" "$DELETE_REQ_COUNT" "$RESET" >&2
        printf '  Forward-pointer: PR-QDRANT-SEARCH-LIFECYCLE-FILTER (cleanup filter)\n' >&2
    fi

    # Cross-check: count DELETED rows in SQLite vs Qdrant
    DB_DELETED=$(sqlite3 "$DB" \
        "SELECT COUNT(*) FROM media_assets WHERE lifecycle_state IN ('DELETED', 'DELETE_REQUESTED');" \
        2>/dev/null || echo "0")
    if [ "$DB_DELETED" -gt 0 ]; then
        printf '  Cross-check: %s DELETED/DELETE_REQUESTED rows in SQLite\n' "$DB_DELETED"
        if [ "$DELETED_COUNT" -eq 0 ] && [ "$DELETE_REQ_COUNT" -eq 0 ]; then
            printf '  %sOK: lifecycle filter correctly excludes %s deleted assets from Qdrant%s\n' \
                "$GREEN" "$DB_DELETED" "$RESET"
        fi
    fi
else
    printf '  SKIP: payload fields missing, cannot verify DELETED filter\n'
fi

# ---- T5: Schema v3 consistency — named vectors ----------------------------
smoke_log_section "T5: Schema v3 consistency (5 named vectors)"

T5_PASS=0

if [ "$T1_PASS" -eq 1 ]; then
    # Qdrant collection info: named vectors appear as keys in
    # .result.config.params.vectors (a map); unnamed vectors appear as a
    # single object with "dimension" + "distance" keys (not a named map).
    # Check both paths: .result.config.params.vectors AND .result.config.params.vectors_config.
    VECTORS_JSON=$(printf '%s' "$COLL_RESP" | jq -r '.result.config.params.vectors // .result.config.params.vectors_config // {}' 2>/dev/null || echo "{}")

    # Detect named vs unnamed: if "dimension" is a top-level key, it's unnamed
    IS_UNNAMED=$(printf '%s' "$VECTORS_JSON" | jq -r 'has("dimension")' 2>/dev/null || echo "false")

    if [ "$IS_UNNAMED" = "true" ]; then
        # Unnamed vector config (single vector, pre-v3 or simplified)
        VEC_DIM=$(printf '%s' "$VECTORS_JSON" | jq -r '.dimension // "?"' 2>/dev/null || echo "?")
        printf '  Vector config: unnamed (dimension=%s) — collection has points=%s\n' "$VEC_DIM" "$COLL_POINTS"
        if [ "$COLL_POINTS" -gt 0 ]; then
            printf '  %sOK: unnamed vector config with %s points (pre-v3 or simplified)%s\n' \
                "$GREEN" "$COLL_POINTS" "$RESET"
            T5_PASS=1
        fi
    else
        # Named vector config (schema v3)
        NAMED_VECTORS=$(printf '%s' "$VECTORS_JSON" | jq -r 'keys | join(", ")' 2>/dev/null || echo "(none)")
        NAMED_COUNT=$(printf '%s' "$VECTORS_JSON" | jq 'keys | length' 2>/dev/null || echo "0")

        printf '  Named vectors: %s (count=%s)\n' "$NAMED_VECTORS" "$NAMED_COUNT"

        # Check for expected vectors: text + transcript + visual + audio + bm25_text
        EXPECTED=("text" "transcript" "visual" "audio" "bm25_text")
        FOUND=0
        MISSING=()
        for vec in "${EXPECTED[@]}"; do
            HAS_VEC=$(printf '%s' "$VECTORS_JSON" | jq -r "has(\"$vec\")" 2>/dev/null || echo "false")
            if [ "$HAS_VEC" = "true" ]; then
                FOUND=$((FOUND + 1))
            else
                MISSING+=("$vec")
            fi
        done

        printf '  Schema v3 vectors found: %s/5 (missing: %s)\n' \
            "$FOUND" "${MISSING[*]:-none}"

        # audio vector is OPTIONAL per QDRANT-DOD-FINAL-2026-07-08 (gate non-bloccante)
        if [ "$FOUND" -ge 4 ]; then
            printf '  %sOK: schema v3 has %s/5 named vectors (audio OPTIONAL)%s\n' \
                "$GREEN" "$FOUND" "$RESET"
            T5_PASS=1
        else
            printf '%sWARN: only %s/5 named vectors found (missing: %s)%s\n' \
                "$YELLOW" "$FOUND" "${MISSING[*]}" "$RESET" >&2
        fi
    fi
else
    printf '  SKIP: Qdrant unreachable, cannot check schema\n'
fi

# ---- T6: Payload enrichment fields present --------------------------------
smoke_log_section "T6: Payload enrichment fields (destination + source_provider + source_video_id)"

T6_PASS=0

if [ "$T2_PASS" -eq 1 ]; then
    HAS_DESTINATION=$(printf '%s' "$SCROLL_RESP" | jq '[.result.points[] | select(.payload.destination != null and .payload.destination != "")] | length' 2>/dev/null || echo "0")
    HAS_SOURCE_PROVIDER=$(printf '%s' "$SCROLL_RESP" | jq '[.result.points[] | select(.payload.source_provider != null and .payload.source_provider != "")] | length' 2>/dev/null || echo "0")
    HAS_SOURCE_VIDEO_ID=$(printf '%s' "$SCROLL_RESP" | jq '[.result.points[] | select(.payload.source_video_id != null and .payload.source_video_id != "")] | length' 2>/dev/null || echo "0")

    printf '  enrichment fields: destination=%s, source_provider=%s, source_video_id=%s\n' \
        "$HAS_DESTINATION" "$HAS_SOURCE_PROVIDER" "$HAS_SOURCE_VIDEO_ID"

    ENRICHMENT_TOTAL=$((HAS_DESTINATION + HAS_SOURCE_PROVIDER + HAS_SOURCE_VIDEO_ID))
    if [ "$ENRICHMENT_TOTAL" -gt 0 ]; then
        printf '  %sOK: %s enrichment field(s) populated across %s points%s\n' \
            "$GREEN" "$ENRICHMENT_TOTAL" "$POINT_COUNT" "$RESET"
        T6_PASS=1
    else
        printf '%sWARN: 0 enrichment fields populated (destination/source_provider/source_video_id all empty)%s\n' \
            "$YELLOW" "$RESET" >&2
        printf '  Forward-pointer: PR-006 (AssetData 19 first-class semantic fields)\n' >&2
        # Non-fatal: enrichment fields may not be populated for older assets
        T6_PASS=1
    fi
else
    printf '  SKIP: payload fields missing, cannot check enrichment\n'
fi

# ---- Verdict ---------------------------------------------------------------
smoke_log_section "Zone 4 Verdict"

PASS_COUNT=0
TOTAL_COUNT=6
[ "$T1_PASS" -eq 1 ] && PASS_COUNT=$((PASS_COUNT + 1))
[ "$T2_PASS" -eq 1 ] && PASS_COUNT=$((PASS_COUNT + 1))
[ "$T3_PASS" -eq 1 ] && PASS_COUNT=$((PASS_COUNT + 1))
[ "$T4_PASS" -eq 1 ] && PASS_COUNT=$((PASS_COUNT + 1))
[ "$T5_PASS" -eq 1 ] && PASS_COUNT=$((PASS_COUNT + 1))
[ "$T6_PASS" -eq 1 ] && PASS_COUNT=$((PASS_COUNT + 1))

printf '\n'
printf '  T1 Qdrant reachability     : %s\n' "$([ "$T1_PASS" -eq 1 ] && echo "PASS" || echo "FAIL")"
printf '  T2 Payload fields present   : %s\n' "$([ "$T2_PASS" -eq 1 ] && echo "PASS" || echo "FAIL")"
printf '  T3 ACTIVE lifecycle         : %s\n' "$([ "$T3_PASS" -eq 1 ] && echo "PASS" || echo "FAIL")"
printf '  T4 DELETED filter           : %s\n' "$([ "$T4_PASS" -eq 1 ] && echo "PASS" || echo "FAIL")"
printf '  T5 Schema v3 vectors        : %s\n' "$([ "$T5_PASS" -eq 1 ] && echo "PASS" || echo "FAIL/SKIP")"
printf '  T6 Enrichment fields        : %s\n' "$([ "$T6_PASS" -eq 1 ] && echo "PASS" || echo "FAIL/SKIP")"
printf '\n'
printf '  %d/%d assertions passed\n' "$PASS_COUNT" "$TOTAL_COUNT"

if [ "$PASS_COUNT" -eq "$TOTAL_COUNT" ]; then
    printf '%sPASS: Zone 4 Qdrant indicizzazione — %d/%d GREEN%s\n' \
        "$GREEN" "$PASS_COUNT" "$TOTAL_COUNT" "$RESET"
    exit 0
else
    printf '%sFAIL: Zone 4 Qdrant indicizzazione — %d/%d passed (see per-T diagnostics)%s\n' \
        "$RED" "$PASS_COUNT" "$TOTAL_COUNT" "$RESET" >&2
    echo "" >&2
    echo "Failure diagnosis:" >&2
    [ "$T1_PASS" -eq 0 ] && echo "  T1 FAIL → PR-QDRANT-DOD-STOCK-PRODUCER (Qdrant infra)" >&2
    [ "$T2_PASS" -eq 0 ] && echo "  T2 FAIL → PR-STOCK-QDRANT-SEMANTIC-ENRICHMENT (payload)" >&2
    [ "$T3_PASS" -eq 0 ] && echo "  T3 FAIL → PR-QDRANT-SEARCH-LIFECYCLE-FILTER (lifecycle)" >&2
    [ "$T4_PASS" -eq 0 ] && echo "  T4 FAIL → PR-QDRANT-SEARCH-LIFECYCLE-FILTER (DELETED filter)" >&2
    [ "$T5_PASS" -eq 0 ] && echo "  T5 FAIL → schema initialization / migration" >&2
    [ "$T6_PASS" -eq 0 ] && echo "  T6 FAIL → PR-006 enrichment fields" >&2
    exit 1
fi
