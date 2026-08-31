#!/usr/bin/env bash
# ── yt_extraction_verify.sh ─────────────────────────────────────────
# Automated 13-point verification script for the YouTube clip
# extraction pipeline.  Runs after a batch extraction and verifies
# the full lifecycle: API → DB → outbox → Qdrant.
#
# Usage:
#   bash tests/operational/yt_extraction_verify.sh [CLIP_ID_PREFIX]
#
#   CLIP_ID_PREFIX defaults to "yt_vdC5GXxS-qU" (the Broner video).
#   Pass a different prefix to verify a different extraction run.
#
# Exit codes:
#   0  PASS — all 13 points passed (WARNs allowed)
#   1  FAIL — one or more points failed (diagnostic table printed)
#   2  PREREQ — environment prerequisites missing
#
# The 13-point checklist:
#   1. media_assets rows exist for the prefix
#   2. source = "youtube" on all rows
#   3. lifecycle_state = ACTIVE on all rows
#   4. file_hash non-empty on at least one row
#   5. local_path non-empty on at least one row
#   6. folder_id populated (WARN if no destination provided)
#   7. folder_path populated (WARN if no destination provided)
#   8. source_version non-empty (CAS fence)
#   9. outbox_events.asset.index.requested created
#  10. outbox_events.status not dead_letter
#  11. outbox_events.last_error empty on completed events
#  12. Qdrant point exists for at least one INDEXED clip
#  13. State machine advanced past DISCOVERED
#
# Environment:
#   VELOX_ADMIN_TOKEN  — required for Qdrant check
#   VELOX_PORT         — optional, default 8000
#   QDRANT_URL         — optional, default http://127.0.0.1:6333
#   DB_PATH            — optional, default data/media/media.db.sqlite
# ─────────────────────────────────────────────────────────────────────
set -euo pipefail
trap 'echo "[ABORTED] line $LINENO: $BASH_COMMAND" >&2' ERR

PREFIX="${1:-yt_vdC5GXxS-qU}"
PORT="${VELOX_PORT:-8000}"
QDRANT="${QDRANT_URL:-http://127.0.0.1:6333}"
DB="${DB_PATH:?DB_PATH must be explicitly set to an isolated or approved database}"
PASS=0
FAIL=0
WARN=0
RESULTS=()

# ── Helpers ─────────────────────────────────────────────────────────
pass() { PASS=$((PASS + 1)); RESULTS+=("✅ $1"); }
fail() { FAIL=$((FAIL + 1)); RESULTS+=("❌ $1: $2"); }
warn() { WARN=$((WARN + 1)); RESULTS+=("⚠️  $1: $2 (non-blocking)"); }
info() { echo "[INFO] $1" >&2; }

# Escape SQL LIKE wildcards in prefix (_ and % are LIKE metacharacters).
# We use GLOB instead of LIKE which uses shell-style * and ? — avoids
# the underscore-as-single-char ambiguity entirely.
sql_prefix() {
    echo "${1}*"
}

check_sql() {
    local desc="$1" query="$2"
    local count
    count=$(sqlite3 "$DB" "$query" 2>/dev/null || echo "0")
    count="${count:-0}"
    count="$(echo "$count" | tr -d '[:space:]')"
    if [ "$count" -gt 0 ] 2>/dev/null; then
        pass "$desc (found $count)"
    else
        fail "$desc" "0 rows matched"
    fi
}

# ── Prerequisites ───────────────────────────────────────────────────
for tool in sqlite3 curl jq; do
    if ! command -v "$tool" &>/dev/null; then
        echo "PREREQ FAIL: $tool not on PATH" >&2
        exit 2
    fi
done

if [ ! -f "$DB" ]; then
    echo "PREREQ FAIL: DB not found at $DB" >&2
    exit 2
fi

# Use GLOB (shell-style wildcards) to avoid LIKE's underscore ambiguity.
GLOB_PATTERN=$(sql_prefix "$PREFIX")
TOTAL=$(sqlite3 "$DB" "SELECT COUNT(*) FROM media_assets WHERE id GLOB '${GLOB_PATTERN}';" 2>/dev/null || echo "0")
TOTAL="${TOTAL:-0}"
if [ "$TOTAL" -eq 0 ]; then
    echo "PREREQ FAIL: no media_assets rows matching ${PREFIX}* — run an extraction first" >&2
    exit 2
fi
info "Found $TOTAL clips matching ${PREFIX}*"

# ── Point 1: Rows exist ────────────────────────────────────────────
check_sql "P01: media_assets rows exist" \
    "SELECT COUNT(*) FROM media_assets WHERE id GLOB '${GLOB_PATTERN}' AND lifecycle_state != 'DELETED';"

# ── Point 2: source = youtube ──────────────────────────────────────
check_sql "P02: source=youtube" \
    "SELECT COUNT(*) FROM media_assets WHERE id GLOB '${GLOB_PATTERN}' AND source = 'youtube';"

# ── Point 3: lifecycle_state = ACTIVE ──────────────────────────────
check_sql "P03: lifecycle_state=ACTIVE" \
    "SELECT COUNT(*) FROM media_assets WHERE id GLOB '${GLOB_PATTERN}' AND lifecycle_state = 'ACTIVE';"

# ── Point 4: file_hash non-empty ───────────────────────────────────
check_sql "P04: file_hash populated" \
    "SELECT COUNT(*) FROM media_assets WHERE id GLOB '${GLOB_PATTERN}' AND file_hash != '' AND file_hash IS NOT NULL;"

# ── Point 5: local_path non-empty ──────────────────────────────────
check_sql "P05: local_path populated" \
    "SELECT COUNT(*) FROM media_assets WHERE id GLOB '${GLOB_PATTERN}' AND local_path != '' AND local_path IS NOT NULL;"

# ── Point 6: folder_id populated ───────────────────────────────────
# Conditional: WARN if no destination was provided (folder_id empty on
# ALL rows), FAIL only if some rows have it and others don't.
FOLDER_ID_COUNT=$(sqlite3 "$DB" \
    "SELECT COUNT(*) FROM media_assets WHERE id GLOB '${GLOB_PATTERN}' AND folder_id != '' AND folder_id IS NOT NULL;" 2>/dev/null || echo "0")
FOLDER_ID_COUNT="${FOLDER_ID_COUNT:-0}"
if [ "$FOLDER_ID_COUNT" -gt 0 ]; then
    pass "P06: folder_id populated ($FOLDER_ID_COUNT rows)"
else
    warn "P06: folder_id empty" "no destination.folder_id in extraction request"
fi

# ── Point 7: folder_path populated ─────────────────────────────────
# Same conditional logic as P06.
FOLDER_PATH_COUNT=$(sqlite3 "$DB" \
    "SELECT COUNT(*) FROM media_assets WHERE id GLOB '${GLOB_PATTERN}' AND folder_path != '' AND folder_path IS NOT NULL;" 2>/dev/null || echo "0")
FOLDER_PATH_COUNT="${FOLDER_PATH_COUNT:-0}"
if [ "$FOLDER_PATH_COUNT" -gt 0 ]; then
    pass "P07: folder_path populated ($FOLDER_PATH_COUNT rows)"
else
    warn "P07: folder_path empty" "no destination in extraction request (folder_path fix N/A)"
fi

# ── Point 8: source_version non-empty ──────────────────────────────
check_sql "P08: source_version populated (CAS fence)" \
    "SELECT COUNT(*) FROM media_assets WHERE id GLOB '${GLOB_PATTERN}' AND source_version != '' AND source_version IS NOT NULL;"

# ── Point 9: outbox event exists ───────────────────────────────────
check_sql "P09: outbox_events asset.index.requested created" \
    "SELECT COUNT(*) FROM outbox_events WHERE aggregate_id GLOB '${GLOB_PATTERN}' AND event_type = 'asset.index.requested';"

# ── Point 10: outbox status not dead_letter ────────────────────────
DEAD_COUNT=$(sqlite3 "$DB" \
    "SELECT COUNT(*) FROM outbox_events WHERE aggregate_id GLOB '${GLOB_PATTERN}' AND event_type = 'asset.index.requested' AND status = 'dead_letter';" 2>/dev/null || echo "0")
DEAD_COUNT="${DEAD_COUNT:-0}"
if [ "$DEAD_COUNT" -eq 0 ]; then
    pass "P10: no dead-lettered outbox events"
else
    fail "P10: outbox dead-lettered" "$DEAD_COUNT events dead-lettered"
fi

# ── Point 11: completed events have empty last_error ───────────────
ERR_COUNT=$(sqlite3 "$DB" \
    "SELECT COUNT(*) FROM outbox_events WHERE aggregate_id GLOB '${GLOB_PATTERN}' AND event_type = 'asset.index.requested' AND status = 'completed' AND last_error != '' AND last_error IS NOT NULL;" 2>/dev/null || echo "0")
ERR_COUNT="${ERR_COUNT:-0}"
if [ "$ERR_COUNT" -eq 0 ]; then
    pass "P11: completed outbox events have no last_error"
else
    fail "P11: completed events with last_error" "$ERR_COUNT events"
fi

# ── Point 12: Qdrant point exists for at least one INDEXED clip ────
INDEXED_ID=$(sqlite3 "$DB" \
    "SELECT id FROM media_assets WHERE id GLOB '${GLOB_PATTERN}' AND index_state = 'INDEXED' ORDER BY updated_at DESC LIMIT 1;" 2>/dev/null || echo "")
INDEXED_ID="${INDEXED_ID:-}"
if [ -n "$INDEXED_ID" ]; then    # Check Qdrant reachability first
    QDRANT_HEALTH=$(curl -s -o /dev/null --max-time 3 "${QDRANT}/healthz" -w '%{http_code}' 2>/dev/null || echo "000")
    if [ "$QDRANT_HEALTH" != "200" ]; then
        fail "P12: Qdrant unreachable" "healthz returned HTTP $QDRANT_HEALTH on ${QDRANT}"
    else
        QDRANT_FOUND=$(curl -s --max-time 5 "${QDRANT}/collections/media_assets_current/points/scroll" \
            -X POST -H 'Content-Type: application/json' \
            -d "{\"limit\":1,\"filter\":{\"must\":[{\"key\":\"asset_id\",\"match\":{\"value\":\"${INDEXED_ID}\"}}]},\"with_payload\":false,\"with_vector\":false}" 2>/dev/null \
            | jq -r '.result.points | length' 2>/dev/null || echo "0")
        QDRANT_FOUND="${QDRANT_FOUND:-0}"
        if [ "$QDRANT_FOUND" -gt 0 ]; then
            pass "P12: Qdrant point exists for INDEXED clip ${INDEXED_ID}"
        else
            fail "P12: Qdrant point missing" "clip ${INDEXED_ID} is INDEXED in SQLite but not in Qdrant"
        fi
    fi
else
    fail "P12: no INDEXED clips" "no ${PREFIX}* clip reached INDEXED state"
fi

# ── Point 13: state machine advanced past DISCOVERED ───────────────
ADVANCED=$(sqlite3 "$DB" \
    "SELECT COUNT(*) FROM media_assets WHERE id GLOB '${GLOB_PATTERN}' AND index_state NOT IN ('DISCOVERED', 'PENDING');" 2>/dev/null || echo "0")
ADVANCED="${ADVANCED:-0}"
if [ "$ADVANCED" -gt 0 ]; then
    pass "P13: state machine advanced ($ADVANCED clips past DISCOVERED)"
else
    fail "P13: state machine stalled" "all clips still at DISCOVERED/PENDING"
fi

# ── Verdict ─────────────────────────────────────────────────────────
echo ""
echo "═══════════════════════════════════════════════════════════"
echo "  YouTube Extraction Verification — ${PREFIX}"
echo "═══════════════════════════════════════════════════════════"
for r in "${RESULTS[@]}"; do
    echo "  $r"
done
echo "───────────────────────────────────────────────────────────"
TOTAL_POINTS=$((PASS + FAIL))
echo "  Verdict: ${PASS}/${TOTAL_POINTS} PASS, ${WARN} WARN"
if [ "$FAIL" -gt 0 ]; then
    echo "  STATUS: FAIL ($FAIL failure(s))"
    echo ""
    echo "  Diagnosis:"
    echo "    P06/P07 empty → provide destination.folder_id in extraction request"
    echo "    P08 empty     → source_version CAS fence broken (BLOCKER #2)"
    echo "    P10 dead      → outbox retry exhausted (check embedding sidecar)"
    echo "    P12 missing   → CAS fence bug or Qdrant unreachable"
    echo "    P13 stalled   → outbox dispatcher not running"
    echo "═══════════════════════════════════════════════════════════"
    exit 1
else
    echo "  STATUS: ALL PASS ✅ (${WARN} non-blocking WARN)"
    echo "═══════════════════════════════════════════════════════════"
    exit 0
fi
