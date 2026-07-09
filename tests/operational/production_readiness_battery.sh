#!/usr/bin/env bash
# production_readiness_battery.sh — 5-zone production readiness aggregator.
#
# Runs all 5 zone smoke tests sequentially against a live PipelineGen server
# (default port 8000) and prints a 30-point verdict PASS/FAIL checklist.
#
# Zones (from architecture/action-plans/2026-07-09-production-readiness-5-zone-testing.md):
#   Zone 1: Jobs / Worker / Runner       (7 assertions)
#   Zone 2: Outbox lifecycle             (5 assertions)
#   Zone 3: Media Assets + Drive + Qdrant (6 assertions)
#   Zone 4: Qdrant Indicizzazione        (6 assertions)
#   Zone 5: Search Aggregata             (6 assertions)
#                                      ─────────────
#                                      30 total sub-assertions
#
# Per godlike/06 SSOT one-canonical-owner-per-fact + godlike/07 NO-FAKE-AVAILABILITY,
# this wrapper is the canonical operator-facing receipt that the entire production
# readiness surface is green against a live PipelineGen server.
#
# Per godlike/07 minimum-blast-radius discipline (smoke probes are diagnostic,
# read-only): the wrapper NEVER mutates git state. Bookkeeping (if any) is
# operator-driven.
#
# Usage:
#   ./production_readiness_battery.sh              # run all 5 zones
#   ./production_readiness_battery.sh --dry        # print zones, exit 0
#   ./production_readiness_battery.sh --zone 3     # run only zone 3
#   BASE=http://host:8081 ./production_readiness_battery.sh  # custom host
#
# Exit codes:
#   0  all 30 sub-assertions passed (all 5 zones GREEN)
#   1  one or more zones FAILED
#   2  setup error (missing probe scripts, server unreachable)
#
# Environment variables (all overridable, forwarded to zone scripts):
#   BASE / API_BASE              host:port (default 127.0.0.1:8000, read by lib/common.sh as SMOKE_API_BASE)
#   VELOX_ADMIN_TOKEN            bearer token
#   DB_PATH / SMOKE_DB           path to SQLite DB
#   QDRANT_URL                   Qdrant REST root (default http://127.0.0.1:6333)
#   SMOKE_DRIVE_TOKEN_FILE       Google OAuth token file (default token.json)
#   SMOKE_DRIVE_ROOT             Drive folder_id for voiceover destination

set -euo pipefail

# ── Constants (godlike/06 SSOT) ─────────────────────────────────────────
PROBE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CO_AUTHOR="PipelineGen Agent <agent@pipelinegen.local>"

# Color codes (common.sh will re-set, but we need them before sourcing for
# the pre-flight and --dry path).
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
RESET='\033[0m'

# ── Per-zone table: script|points|tag|description ──────────────────────
# Points per zone = number of sub-assertions the probe script's exit 0
# canonically receipts (per godlike/07: the wrapper trusts exit 0).
ZONES=(
    "jobs_worker_runner_smoke.sh|7|ZONE-1|Jobs/Worker/Runner"
    "outbox_smoke.sh|5|ZONE-2|Outbox lifecycle"
    "media_assets_drive_qdrant_smoke.sh|6|ZONE-3|Media Assets+Drive+Qdrant"
    "qdrant_indexing_smoke.sh|6|ZONE-4|Qdrant Indicizzazione"
    "search_aggregata_smoke.sh|6|ZONE-5|Search Aggregata"
)
TOTAL_POINTS=30

# ── Cleanup-on-PASS trap (mirrors stock_e2e_full_battery.sh) ────────────
TMP_DIR="$(mktemp -d /tmp/production-readiness-battery.XXXXXX)"
cleanup() {
    local exit_code=$?
    if [ "$exit_code" -eq 0 ]; then
        rm -rf "$TMP_DIR" 2>/dev/null || true
    else
        echo "TMP_DIR preserved at $TMP_DIR for diagnostic inspection (exit $exit_code)" >&2
    fi
}
trap cleanup EXIT

# ── Parse arguments ─────────────────────────────────────────────────────
DRY_RUN=0
SINGLE_ZONE=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --dry|-n)
            DRY_RUN=1
            shift
            ;;
        --zone)
            SINGLE_ZONE="$2"
            shift 2
            ;;
        -h|--help)
            sed -n '2,38p' "$0"
            exit 0
            ;;
        *)
            echo "Unknown argument: $1" >&2
            exit 2
            ;;
    esac
done

# ── Dry-run mode ────────────────────────────────────────────────────────
if [ "$DRY_RUN" -eq 1 ]; then
    echo "=== PRODUCTION READINESS BATTERY — DRY RUN ==="
    echo
    echo "Zones that would execute (sequential):"
    for entry in "${ZONES[@]}"; do
        IFS='|' read -r fname points tag desc <<<"$entry"
        printf '  %-10s  %-40s  %s assertion(s)\n' "$tag" "$desc" "$points"
    done
    echo
    echo "Total: $TOTAL_POINTS sub-assertions across ${#ZONES[@]} zones"
    echo
    echo "Run against: ${BASE:-http://127.0.0.1:8000}"
    exit 0
fi

# ── Header ──────────────────────────────────────────────────────────────
echo "========================================================================"
echo "  PRODUCTION READINESS BATTERY — 5-zone full suite"
echo "  $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
echo "  Target: ${BASE:-${API_BASE:-127.0.0.1:8000}}"
echo "========================================================================"
echo

# ── Pre-flight: all probe scripts exist on disk ─────────────────────────
echo "Pre-flight: verify zone probe scripts on disk"
echo

missing=0
for entry in "${ZONES[@]}"; do
    IFS='|' read -r fname _ tag _ <<<"$entry"
    # If --zone specified, only check that zone
    if [[ -n "$SINGLE_ZONE" && "$tag" != "$SINGLE_ZONE" ]]; then
        continue
    fi
    if [ ! -f "$PROBE_DIR/$fname" ]; then
        printf '  %sMISSING: %s (%s)%s\n' "$RED" "$fname" "$tag" "$RESET"
        missing=$((missing + 1))
    else
        printf '  %sOK:%s      %-40s %s\n' "$GREEN" "$RESET" "$fname" "$tag"
    fi
done

if [ "$missing" -gt 0 ]; then
    echo
    printf '%sFAIL: %d probe script(s) missing from tests/operational/%s\n' \
        "$RED" "$missing" "$RESET" >&2
    echo "  Per godlike/07 fail-closed at prerequisites:" >&2
    echo "  every zone probe MUST exist on disk before the battery runs." >&2
    exit 2
fi
echo
printf '%sPre-flight PASS: all zone probe scripts on disk.%s\n' "$GREEN" "$RESET"
echo

# ── Per-zone execution + point tally ────────────────────────────────────
echo "========================================================================"
echo "  Per-zone execution (sequential)"
echo "========================================================================"
echo

declare -A zone_exit
declare -A zone_points
declare -A zone_log

# Initialize all zones as "skip" upfront so the tally/verdict loops
# never hit an unbound variable when --zone filters to a single zone.
for entry in "${ZONES[@]}"; do
    IFS='|' read -r _ _ tag _ <<<"$entry"
    zone_exit[$tag]="skip"
    zone_points[$tag]="0"
done

for entry in "${ZONES[@]}"; do
    IFS='|' read -r fname points tag desc <<<"$entry"

    # If --zone specified, skip non-matching zones
    if [[ -n "$SINGLE_ZONE" && "$tag" != "$SINGLE_ZONE" ]]; then
        zone_exit[$tag]="skip"
        zone_points[$tag]="0"
        continue
    fi

    echo "--------------------------------------------------------------------"
    printf '  %s: %s (%s assertion(s))\n' "$tag" "$desc" "$points"
    echo "  Script: $fname"
    echo "--------------------------------------------------------------------"

    # Save output to tmp for diagnostic inspection on failure
    log_file="$TMP_DIR/${tag}.log"

    set +e
    bash "$PROBE_DIR/$fname" 2>&1 | tee "$log_file"
    exit_code=${PIPESTATUS[0]}
    set -e

    zone_exit[$tag]="$exit_code"
    zone_points[$tag]="$points"
    zone_log[$tag]="$log_file"

    if [ "$exit_code" -eq 0 ]; then
        printf '\n  %s%s PASS (exit 0) — %s sub-assertion(s) credited%s\n\n' \
            "$GREEN" "$tag" "$points" "$RESET"
    else
        printf '\n  %s%s FAIL (exit %s) — 0 sub-assertion(s) credited%s\n' \
            "$RED" "$tag" "$exit_code" "$RESET" >&2
        printf '  Log: %s\n\n' "$log_file" >&2
    fi
done

# ── Tally ───────────────────────────────────────────────────────────────
passed_points=0
zones_passed=0
zones_failed=0
zones_skipped=0

for entry in "${ZONES[@]}"; do
    IFS='|' read -r _ points tag _ <<<"$entry"
    if [ "${zone_exit[$tag]}" = "skip" ]; then
        zones_skipped=$((zones_skipped + 1))
        continue
    fi
    if [ "${zone_exit[$tag]}" -eq 0 ]; then
        passed_points=$((passed_points + points))
        zones_passed=$((zones_passed + 1))
    else
        zones_failed=$((zones_failed + 1))
    fi
done

# Adjust total for skipped zones
effective_total=$TOTAL_POINTS
if [ "$zones_skipped" -gt 0 ]; then
    effective_total=0
    for entry in "${ZONES[@]}"; do
        IFS='|' read -r _ points tag _ <<<"$entry"
        if [ "${zone_exit[$tag]}" != "skip" ]; then
            effective_total=$((effective_total + points))
        fi
    done
fi

# ── Verdict ─────────────────────────────────────────────────────────────
echo
echo "========================================================================"
echo "  VERDICT — Production Readiness Battery"
echo "========================================================================"
echo

# Per-zone checklist
for entry in "${ZONES[@]}"; do
    IFS='|' read -r _ points tag desc <<<"$entry"
    if [ "${zone_exit[$tag]}" = "skip" ]; then
        printf '  %-10s  %-40s  %s (skipped)\n' "$tag" "$desc" "${YELLOW}SKIP${RESET}"
    elif [ "${zone_exit[$tag]}" -eq 0 ]; then
        printf '  %-10s  %-40s  %s %s/%s\n' "$tag" "$desc" "${GREEN}PASS${RESET}" "$points" "$points"
    else
        printf '  %-10s  %-40s  %s 0/%s\n' "$tag" "$desc" "${RED}FAIL${RESET}" "$points"
    fi
done

echo
printf '  Sub-assertions PASS: %s / %s\n' "$passed_points" "$effective_total"
printf '  Zones: %s passed, %s failed' "$zones_passed" "$zones_failed"
if [ "$zones_skipped" -gt 0 ]; then
    printf ', %s skipped' "$zones_skipped"
fi
echo
echo

if [ "$passed_points" -eq "$effective_total" ] && [ "$zones_failed" -eq 0 ]; then
    printf '%sVERDICT: %d/%d PASS — ALL ZONES GREEN%s\n' \
        "$GREEN" "$passed_points" "$effective_total" "$RESET"
    echo
    echo "Per godlike/07 NO-FAKE-AVAILABILITY: every zone probe's exit 0"
    echo "is the canonical receipt that all its sub-assertions PASS."
    echo
    exit 0
else
    printf '%sVERDICT: FAIL (%d/%d sub-assertions PASS, %d zone(s) failed)%s\n' \
        "$RED" "$passed_points" "$effective_total" "$zones_failed" "$RESET" >&2
    echo >&2
    echo "Per godlike/07 NO-FAKE-AVAILABILITY: the battery FAIL is real." >&2
    echo "Per-zone failure diagnosis (see per-zone logs in $TMP_DIR):" >&2
    echo >&2

    for entry in "${ZONES[@]}"; do
        IFS='|' read -r fname _ tag desc <<<"$entry"
        if [ "${zone_exit[$tag]}" = "skip" ]; then
            continue
        fi
        if [ "${zone_exit[$tag]}" -ne 0 ]; then
            case "$tag" in
                ZONE-1)
                    printf '  %s FAIL → see jobs_worker_runner_smoke.sh diagnostics\n' "$tag" >&2
                    echo "    Possible: broker timeout / worker not running / DB locked" >&2
                    ;;
                ZONE-2)
                    printf '  %s FAIL → see outbox_smoke.sh diagnostics\n' "$tag" >&2
                    echo "    Possible: outbox dispatcher not running / Qdrant down / dead_letter" >&2
                    ;;
                ZONE-3)
                    printf '  %s FAIL → see media_assets_drive_qdrant_smoke.sh diagnostics\n' "$tag" >&2
                    echo "    Possible: voiceover pipeline / Drive upload / Qdrant index" >&2
                    echo "    PR mapping: PR-VOICEOVER-PIPELINE-DEBUG / PR-STOCK-OUTBOX-QDRANT-INDEX" >&2
                    ;;
                ZONE-4)
                    printf '  %s FAIL → see qdrant_indexing_smoke.sh diagnostics\n' "$tag" >&2
                    echo "    Possible: Qdrant down / schema mismatch / lifecycle filter" >&2
                    echo "    PR mapping: PR-QDRANT-DOD-STOCK-PRODUCER / PR-QDRANT-SEARCH-LIFECYCLE-FILTER" >&2
                    ;;
                ZONE-5)
                    printf '  %s FAIL → see search_aggregata_smoke.sh diagnostics\n' "$tag" >&2
                    echo "    Possible: search handler / source filter / cursor pagination" >&2
                    echo "    PR mapping: PR-STOCK-SEARCH-SOURCE-FILTER / PR-STOCK-SEARCH-HANDLER-VALIDATION" >&2
                    ;;
            esac
            echo "    Log: ${zone_log[$tag]}" >&2
            echo >&2
        fi
    done

    exit 1
fi
