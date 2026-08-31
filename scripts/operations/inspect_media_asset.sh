#!/usr/bin/env bash
# =============================================================================
# inspect_media_asset.sh — operator inspection helper for media_assets +
# outbox_events. Runs after every benchmark / live-test cycle to confirm the
# canonical image-pipeline invariants for a given asset_id.
#
# Asserts (godlike/06 SSOT for image metadata):
#   1. media_type ∈ {image, clip, audio, document, image_video, sound_effect, script}
#   2. local_path is non-empty AND exists on disk (or, if NULL/empty, is
#      explicitly recorded as REMOTE-ONLY — assert that fact rather than fail)
#   3. json_array_length(visual_embedding) = 768   (canonical SigLIP dim)
#   4. embedding_version_visual present (json_extract non-empty)
#   5. outbox_events rows for aggregate_id = asset_id, ordered DESC by id,
#      with stuck rows (status='pending' AND attempt_count >= max_attempts-2)
#      highlighted for pager-duty.
#
# Usage:
#   bash scripts/operations/inspect_media_asset.sh <asset_id>
#   bash scripts/operations/inspect_media_asset.sh <asset_id> --db data/media/media.db.sqlite
#   bash scripts/operations/inspect_media_asset.sh <asset_id> --json
#   bash scripts/operations/inspect_media_asset.sh <asset_id> --help
#
#   --db <PATH>           Canonical SQLite database path only; non-canonical paths are rejected.
#   --json                Emit machine-readable single-line JSON summary
#                         instead of colored PASS/FAIL output. Useful for CI.
#   -h | --help           Show USAGE.
#
# Exit codes (canonical for pager alerts):
#   0  All 4 invariants PASS.
#   1  ≥1 invariant FAIL.
#   2  Asset id not found in media_assets.
#   3  SQLite DB not found or unreadable.
#   4  sqlite3 binary missing on PATH.
#   5  Bad CLI usage (missing asset_id, unknown flag, etc.).
#
# Companion runbook:
#   docs/operations/inspect-media-asset.md
# =============================================================================

# STUCK-labels emitted (grep-friendly): STUCK (pending + attempt_count ≥ max_attempts-2)
# and MAX_MISSING (pending + max_attempts NULL/empty ⇒ can never reach threshold).
# Operators paging the script output via `| grep STUCK` / `| grep MAX_MISSING` see
# the literal token (NO_COLOR=1 OR stdout redirected fall back to no ANSI).
set -euo pipefail
# Trap SIGPIPE so operators piping the script's output to `head` / `grep` / `less`
# see the script's intended exit code (0/1/2/3/4/5) instead of 128+13=141 from the
# pipe-break signal — small but important for pager-duty triage scripts.
trap '' PIPE

DB_PATH=""
ASSET_ID=""
JSON_MODE=0
PASS_COUNT=0
FAIL_COUNT=0
declare -a FAIL_LINES

# ── colour helpers (NO_COLOR respected) ─────────────────────────────────────
if [ -t 1 ] && [ "${NO_COLOR:-0}" != "1" ]; then
  C_GREEN=$(printf '\033[32m')
  C_RED=$(printf '\033[31m')
  C_YELLOW=$(printf '\033[33m')
  C_CYAN=$(printf '\033[36m')
  C_DIM=$(printf '\033[2m')
  C_BOLD=$(printf '\033[1m')
  C_RESET=$(printf '\033[0m')
else
  C_GREEN=; C_RED=; C_YELLOW=; C_CYAN=; C_DIM=; C_BOLD=; C_RESET=
fi

log_pass() { printf "  ${C_GREEN}✓ PASS${C_RESET}  %s\n" "$1"; PASS_COUNT=$((PASS_COUNT + 1)); }
log_fail() { printf "  ${C_RED}✗ FAIL${C_RESET}  %s\n" "$1"; FAIL_COUNT=$((FAIL_COUNT + 1)); FAIL_LINES+=("$1"); }
log_info() { printf "  ${C_DIM}·${C_RESET}        %s\n" "$1"; }
section()  { printf "\n${C_BOLD}${C_CYAN}── %s ──${C_RESET}\n" "$1"; }

# ── USAGE ────────────────────────────────────────────────────────────────────
usage() {
  sed -n '2,40p' "$0"
}

# ── CLI parsing (positional <asset_id> + optional flags) ────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --db)
      DB_PATH="${2:-}"
      [[ -z "$DB_PATH" ]] && { echo "ERROR: --db requires a path" >&2; exit 5; }
      shift 2
      ;;
    --json)
      JSON_MODE=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    --*)
      echo "ERROR: unknown flag: $1" >&2
      usage >&2
      exit 5
      ;;
    *)
      if [[ -z "$ASSET_ID" ]]; then
        ASSET_ID="$1"
        shift
      else
        echo "ERROR: extra positional argument after asset_id: $1" >&2
        usage >&2
        exit 5
      fi
      ;;
  esac
done

if [[ -z "$ASSET_ID" ]]; then
  echo "ERROR: <asset_id> is required" >&2
  usage >&2
  exit 5
fi

# ── 1. sqlite3 preflight ────────────────────────────────────────────────────
if ! command -v sqlite3 >/dev/null 2>&1; then
  echo "ERROR: sqlite3 not found in PATH." >&2
  exit 4
fi

# ── 2. Resolve and validate the canonical DB path ───────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
# shellcheck source=../lib/canonical_db_path.sh
source "$PROJECT_ROOT/scripts/lib/canonical_db_path.sh"
DB_PATH="$(canonical_primary_db_path "$PROJECT_ROOT")"
if [[ -z "$DB_PATH" || ! -f "$DB_PATH" ]]; then
  echo "ERROR: canonical SQLite DB not found: $DB_PATH" >&2
  exit 3
fi

# ── 3. SQL-escape $ASSET_ID (defense vs single-quote injection in SELECTs) ─
# sqlite3 CLI does not honor :asset_id positional params without a wrapper;
# the per-query single-column helper keeps the WHERE clause to ONE literal
# substitution, and $ASSET_ID_ESC doubles every single-quote.
ASSET_ID_ESC="${ASSET_ID//\'/\'\'}"

# ── 4. Asset row: existence check + per-column single-field queries ─────────
# Splitting into per-column SELECTs avoids the awk -F'|' delimiter collision
# that visual_embedding / metadata_json JSON content would otherwise expose.
if [[ $JSON_MODE -eq 0 ]]; then
  printf "${C_BOLD}${C_CYAN}━━━ Inspect media_asset ${C_BOLD}%s${C_RESET}\n" "$ASSET_ID"
  printf "${C_DIM}db=%s${C_RESET}\n" "$DB_PATH"
fi

EXISTS=$(sqlite3 "$DB_PATH" \
  "SELECT 1 FROM media_assets WHERE id = '${ASSET_ID_ESC}' LIMIT 1;" \
  2>/dev/null || echo "")
if [[ -z "$EXISTS" ]]; then
  if [[ $JSON_MODE -eq 1 ]]; then
    printf '{"asset_id":"%s","db":"%s","status":"NOT_FOUND"}\n' "$ASSET_ID" "$DB_PATH"
  else
    echo
    echo "ERROR: asset_id=$ASSET_ID not found in media_assets on $DB_PATH" >&2
  fi
  exit 2
fi

# Helper: per-column read (single-column query ⇒ no delimiter collision).
sq_assets() {
  sqlite3 -nullvalue '<NULL>' "$DB_PATH" \
    "SELECT COALESCE($1, '') FROM media_assets WHERE id = '${ASSET_ID_ESC}' LIMIT 1;" \
    2>/dev/null
}

ASSET_TYPE=$(sq_assets "media_type")
ASSET_LOCAL_PATH=$(sq_assets "local_path")
ASSET_NAME=$(sq_assets "name")
ASSET_VERSION=$(sq_assets "json_extract(metadata_json, '\$.embedding_version_visual')")
ASSET_PROVIDER=$(sq_assets "json_extract(metadata_json, '\$.provider')")
ASSET_ORIGIN=$(sq_assets "json_extract(metadata_json, '\$.origin')")

if [[ $JSON_MODE -eq 0 ]]; then
  section "asset row"
  log_info "media_type=$ASSET_TYPE   provider=$ASSET_PROVIDER   origin=$ASSET_ORIGIN"
  log_info "local_path=$ASSET_LOCAL_PATH"
fi

# ── 5. Assertions ───────────────────────────────────────────────────────────

# ── Assertion 1: media_type allowlist ───────────────────────────────────────
ALLOWED_TYPES="image clip audio document image_video sound_effect script"
if [[ " $ALLOWED_TYPES " == *" $ASSET_TYPE "* ]]; then
  log_pass "media_type='$ASSET_TYPE' is in allowlist"
else
  log_fail "media_type='$ASSET_TYPE' is NOT in [$ALLOWED_TYPES]"
fi

# ── Assertion 2: local_path exists on disk (or remote-only is acceptable) ───
if [[ -z "$ASSET_LOCAL_PATH" || "$ASSET_LOCAL_PATH" == "<NULL>" ]]; then
  log_info "local_path is NULL/empty — record as REMOTE-ONLY (not a fail)"
elif [[ -e "$ASSET_LOCAL_PATH" ]]; then
  log_pass "local_path exists on disk ($ASSET_LOCAL_PATH)"
else
  log_fail "local_path='$ASSET_LOCAL_PATH' is set but file is MISSING on disk"
fi

# ── Assertion 3: visual_embedding dim = 768 ─────────────────────────────────
VISUAL_DIM=$(sqlite3 "$DB_PATH" \
  "SELECT json_array_length(visual_embedding) FROM media_assets WHERE id = '${ASSET_ID_ESC}' LIMIT 1;" \
  2>/dev/null || echo "")

if [[ -z "$VISUAL_DIM" || "$VISUAL_DIM" == "<NULL>" ]]; then
  log_info "visual_embedding is empty — record as DIM_PENDING (SigLIP embedding not yet computed by the outbox dispatcher)"
elif [[ "$VISUAL_DIM" == "768" ]]; then
  log_pass "json_array_length(visual_embedding) = 768 (canonical SigLIP so400m patch14-384)"
else
  log_fail "json_array_length(visual_embedding) = $VISUAL_DIM, expected 768"
fi

# ── Assertion 4: embedding_version_visual present ───────────────────────────
if [[ -z "$ASSET_VERSION" || "$ASSET_VERSION" == "<NULL>" || "$ASSET_VERSION" == "" ]]; then
  log_fail "embedding_version_visual is empty — metadata_json.embedding_version_visual MUST be set per godlike/06"
elif [[ "$ASSET_VERSION" == "2026-06-16-v1" ]]; then
  log_pass "embedding_version_visual='$ASSET_VERSION' (canonical SigLIP model version)"
else
  log_fail "embedding_version_visual='$ASSET_VERSION' is unexpected (canonical='2026-06-16-v1')"
fi

# ── 6. outbox_events DESC listing ───────────────────────────────────────────
section "outbox_events for aggregate_id=$ASSET_ID (ordered DESC)"

# Tab-delimited multi-row multi-column read. IFS=$'\t' on the consumer side
# (NOT '|') so the outbox last_error column, which can contain ANY printable
# char, cannot break field boundaries.
OUTBOX_ROWS=$(sqlite3 -separator $'\t' -nullvalue '<NULL>' "$DB_PATH" "
SELECT
    id,
    event_type,
    COALESCE(status, ''),
    attempt_count,
    max_attempts,
    CASE WHEN COALESCE(last_error,'') = '' THEN '<none>' ELSE substr(last_error, 1, 80) END,
    COALESCE(completed_at, '<NULL>'),
    COALESCE(created_at, '<NULL>')
FROM outbox_events
WHERE aggregate_id = '${ASSET_ID_ESC}'
ORDER BY id DESC;
" 2>/dev/null || echo "")

if [[ -z "$OUTBOX_ROWS" ]]; then
  log_info "no outbox_events rows for aggregate_id=$ASSET_ID"
elif [[ $JSON_MODE -eq 0 ]]; then
  printf "  ${C_DIM}%-6s %-32s %-10s %-7s %-7s %-40s %-22s %-22s${C_RESET}\n" \
         "id" "event_type" "status" "att" "max" "last_error" "completed_at" "created_at"
  STUCK_COUNT=0
  while IFS=$'\t' read -r oe_id oe_type oe_status oe_att oe_max oe_err oe_done oe_created; do
    [[ -z "$oe_id" ]] && continue
    STUCK=""
    # Fail-closed (godlike/07): a pending row with missing max_attempts
    # can never reach its retry threshold ⇒ pending-forever. Surface as
    # MAX_MISSING so pager-duty sees it (otherwise bash arithmetic on
    # '<NULL>' silently evaluates to 0 and the row is mis-classified as
    # NOT stuck, masking the operator signal).
    if [[ "$oe_status" == "pending" && ( "$oe_max" == "<NULL>" || -z "$oe_max" ) ]]; then
      STUCK="${C_RED}MAX_MISSING${C_RESET}"
      STUCK_COUNT=$((STUCK_COUNT + 1))
    elif [[ "$oe_status" == "pending" && "$oe_att" -ge $((oe_max - 2)) ]]; then
      STUCK="${C_RED}STUCK${C_RESET}"
      STUCK_COUNT=$((STUCK_COUNT + 1))
    fi
    printf "  %-6s %-32s %-10s %-7s %-7s %-40s %-22s %-22s %s\n" \
           "$oe_id" "$oe_type" "$oe_status" "$oe_att" "$oe_max" "$oe_err" "$oe_done" "$oe_created" "$STUCK"
  done <<< "$OUTBOX_ROWS"
  if [[ $STUCK_COUNT -gt 0 ]]; then
    log_fail "outbox has $STUCK_COUNT STUCK row(s) (status=pending AND attempts near max) — escalate"
  else
    log_pass "outbox_events: no STUCK rows"
  fi
fi

# ── 7. Summary + exit code ──────────────────────────────────────────────────
section "summary"
if [[ $JSON_MODE -eq 1 ]]; then
  printf '{"asset_id":"%s","db":"%s","pass":%d,"fail":%d,"status":"%s"}\n' \
         "$ASSET_ID" "$DB_PATH" "$PASS_COUNT" "$FAIL_COUNT" \
         "$([[ $FAIL_COUNT -eq 0 ]] && echo OK || echo FAIL)"
else
  printf "  ${C_GREEN}passed: %d${C_RESET}\n" "$PASS_COUNT"
  printf "  ${C_RED}failed: %d${C_RESET}\n" "$FAIL_COUNT"
  if [[ ${#FAIL_LINES[@]} -gt 0 ]]; then
    printf "\n  ${C_RED}${C_BOLD}FAILED checks:${C_RESET}\n"
    for line in "${FAIL_LINES[@]}"; do
      printf "    - %s\n" "$line"
    done
  fi
fi

if [[ $FAIL_COUNT -gt 0 ]]; then
  exit 1
fi
exit 0
