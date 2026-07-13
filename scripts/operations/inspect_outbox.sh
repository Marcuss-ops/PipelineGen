#!/usr/bin/env bash
# =============================================================================
# inspect_outbox.sh — read-only SQLite/outbox inspector.
#
# Mirrors inspect_media_asset.sh's CLI style but is concentrated on the
# outbox_events table (godlike/06 SSOT: separate inspector so the
# post-parallel-image-benchmark operator can drill into outbox status
# without re-reading asset rows).
#
# Schema (canonical):
#   outbox_events(id, event_type, status, attempt_count, max_attempts,
#                 lease_id, last_error, payload_json, completed_at,
#                 created_at, next_attempt_at, aggregate_id)
#
# Status state machine (per internal/infrastructure/database/sqlite/outboxevents/pool.go):
#   pending       → next_attempt_at > now OR being claimed by a worker
#   processing    → lease_id owned by a worker; lease expires after ProcessTimeout
#   completed     → terminal: handler succeeded, MarkCompleted called
#   dead_letter   → terminal: handler returned TerminalError OR attempts exhausted
#   superseded    → terminal: a newer aggregate version replaced this event
#
# godlike/07 NO-FAKE-AVAILABILITY:
#   - DB not found / unreadable → exit 3/4
#   - sqlite3 missing → exit 3
#   - aggregate_id not found / empty result → exit 2
#   - unknown subcommand / unknown flag → exit 7
#
# Subcommands:
#   stats                counts by status + oldest pending (operator overview)
#   list-pending         rows WHERE status='pending' (limit 50 by default)
#   list-processing      rows WHERE status='processing'
#   list-completed       rows WHERE status='completed'
#   list-stuck           rows WHERE status='pending' AND attempt_count >= max_attempts-2
#                        (the canonical pre-DLQ signal — pager-duty here)
#   list-dead-letter     rows WHERE status='dead_letter' or 'superseded' (terminal failure path)
#   lookup <aggregate_id>
#                        rows WHERE aggregate_id = '<aggregate_id>'
#
# Usage:
#   bash scripts/operations/inspect_outbox.sh stats
#   bash scripts/operations/inspect_outbox.sh list-stuck --db /var/lib/velox/velox.db
#   bash scripts/operations/inspect_outbox.sh lookup <aggregate_id>
#   bash scripts/operations/inspect_outbox.sh list-pending --limit 200 --json
#   bash scripts/operations/inspect_outbox.sh --help
#
# Common flags:
#   --db <PATH>       SQLite database path (overrides default lookup).
#   --limit <N>       Cap rows returned (default 50, max 1000).
#   --event-type <T>  Filter by canonical event_type (typically 'asset.index.requested',
#                     'asset.published', 'qdrant.upsert', etc.).
#   --json            Emit machine-readable single-line JSON instead of TSV.
#   -h | --help       Show USAGE.
#
# Exit codes:
#   0  Subcommand completed (results may be empty for lookup/list).
#   1  DB unreachable / connection refused.
#   2  Empty result (lookup: aggregate_id not found; list: 0 rows).
#   3  sqlite3 binary missing OR DB file not found / unreadable.
#   7  Bad CLI usage (missing subcommand, unknown flag, etc.).
#
# Companion runbook:
#   docs/operations/parallel-images-verification-runbook.md § 5 (Outbox inspector)
# =============================================================================

set -euo pipefail
# SIGPIPE-friendly trap — pagers piping to head/grep see the script's
# intended exit code (0..7) instead of 128+13=141 from the pipe-break signal.
trap '' PIPE

DB_PATH=""
JSON_MODE=0
LIMIT=50
EVENT_TYPE_FILTER=""
SUBCMD=""
ARG=""

# ── colour helpers (NO_COLOR respected) ────────────────────────────────────
if [ -t 1 ] && [ "${NO_COLOR:-0}" != "1" ]; then
  C_GREEN=$(printf '\033[32m'); C_RED=$(printf '\033[31m'); C_YELLOW=$(printf '\033[33m')
  C_CYAN=$(printf '\033[36m'); C_DIM=$(printf '\033[2m'); C_BOLD=$(printf '\033[1m'); C_RESET=$(printf '\033[0m')
else
  C_GREEN=; C_RED=; C_YELLOW=; C_CYAN=; C_DIM=; C_BOLD=; C_RESET=
fi

section() { [[ $JSON_MODE -eq 1 ]] && return 0; printf "\n${C_BOLD}${C_CYAN}── %s ──${C_RESET}\n" "$1"; }
log_pass() { [[ $JSON_MODE -eq 1 ]] && return 0; printf "  ${C_GREEN}✓ PASS${C_RESET}  %s\n" "$1"; }
log_fail() { [[ $JSON_MODE -eq 1 ]] && return 0; printf "  ${C_RED}✗ FAIL${C_RESET}  %s\n" "$1"; }
log_info() { [[ $JSON_MODE -eq 1 ]] && return 0; printf "  ${C_DIM}·${C_RESET}        %s\n" "$1"; }

USAGE=$(sed -n '2,52p' "$0")

# ── CLI parsing ────────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --db) DB_PATH="${2:-}"; [[ -z "$DB_PATH" ]] && { echo "ERROR: --db requires a path" >&2; exit 7; }; shift 2 ;;
    --limit) LIMIT="${2:-}"; [[ ! "$LIMIT" =~ ^[1-9][0-9]*$ ]] || [[ "$LIMIT" -gt 1000 ]] && { echo "ERROR: --limit must be 1..1000" >&2; exit 7; }; shift 2 ;;
    --event-type) EVENT_TYPE_FILTER="${2:-}"; shift 2 ;;
    --json) JSON_MODE=1; shift ;;
    -h|--help) printf "%s\n" "$USAGE"; exit 0 ;;
    stats|list-pending|list-processing|list-completed|list-stuck|list-dead-letter)
      [[ -z "$SUBCMD" ]] || { echo "ERROR: multiple subcommands: $SUBCMD + $1" >&2; exit 7; }
      SUBCMD="$1"; shift ;;
    lookup)
      [[ -z "$SUBCMD" ]] || { echo "ERROR: multiple subcommands: $SUBCMD + $1" >&2; exit 7; }
      SUBCMD="lookup"
      ARG="${2:-}"
      [[ -z "$ARG" ]] && { echo "ERROR: lookup requires an aggregate_id" >&2; exit 7; }
      shift 2 ;;
    --*) echo "ERROR: unknown flag: $1" >&2; exit 7 ;;
    *) echo "ERROR: extra positional argument: $1" >&2; exit 7 ;;
  esac
done

[[ -z "$SUBCMD" ]] && { echo "ERROR: missing subcommand (stats | list-* | lookup <aggregate_id>)" >&2; printf "%s\n" "$USAGE"; exit 7; }

# ── 1. sqlite3 preflight ────────────────────────────────────────────────────
command -v sqlite3 >/dev/null 2>&1 || { echo "ERROR: sqlite3 not found in PATH." >&2; exit 3; }

# ── 2. Resolve DB path ────────────────────────────────────────────────────
if [[ -z "$DB_PATH" ]]; then DB_PATH="${VELOX_DB:-}"; fi
if [[ -z "$DB_PATH" ]]; then
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
  for candidate in \
      "$PROJECT_ROOT/data/media/media.db.sqlite" \
      "$PROJECT_ROOT/data/velox.db" \
      "/var/lib/velox/velox.db"; do
    if [[ -f "$candidate" ]]; then
      DB_PATH="$candidate"
      break
    fi
  done
fi

if [[ -z "$DB_PATH" || ! -f "$DB_PATH" ]]; then
  echo "ERROR: SQLite DB not found. Tried \$VELOX_DB, project defaults, /var/lib/velox/velox.db. Pass --db <PATH>." >&2
  exit 3
fi

# Sanity probe: confirm we can read (sqlite3 .tables includes outbox_events).
# -readonly here too so even accidental mutation via .schema/.tables plumbing
# is rejected by sqlite3 itself (godlike/07 fail-closed).
if ! sqlite3 -readonly "$DB_PATH" ".tables" 2>/dev/null | grep -q "outbox_events"; then
  echo "ERROR: sqlite3 cannot read $DB_PATH or outbox_events table missing" >&2
  exit 1
fi

# ── 3. SQL escape helper ───────────────────────────────────────────────────
# Outbox event_type / aggregate_id are app-controlled strings (typically
# UUIDv4 or 'sha256:...' or short tag names), but defense in depth: double
# every single-quote so a bad input cannot close the WHERE literal.
esc() { printf "%s" "${1//\'/\'\'}"; }

EVENT_TYPE_SQL=""
if [[ -n "$EVENT_TYPE_FILTER" ]]; then
  ET_ESC=$(esc "$EVENT_TYPE_FILTER")
  EVENT_TYPE_SQL="AND event_type = '${ET_ESC}'"
fi

# ── 4. Subcommand dispatch ─────────────────────────────────────────────────
case "$SUBCMD" in
  stats)
    section "stats: outbox_events counts (db=$DB_PATH)"
    if [[ -n "$EVENT_TYPE_FILTER" ]]; then
      log_info "filter: event_type='$EVENT_TYPE_FILTER'"
    fi
    ROWS=$(sqlite3 -readonly -separator $'\t' "$DB_PATH" "
      SELECT status, COUNT(*) FROM outbox_events
      WHERE 1=1 $EVENT_TYPE_SQL
      GROUP BY status
      ORDER BY status;
    " 2>/dev/null || echo "")
    TOTAL=0
    declare -A COUNTS
    if [[ -n "$ROWS" ]]; then
      while IFS=$'\t' read -r st cn; do
        COUNTS[$st]=$cn
        TOTAL=$((TOTAL + cn))
      done <<<"$ROWS"
    fi
    if [[ $JSON_MODE -eq 1 ]]; then
      printf '{"db":"%s","event_type_filter":"%s","total":%d,"counts":{' \
        "$DB_PATH" "${EVENT_TYPE_FILTER:-}" "$TOTAL"
      FIRST=1
      for st in pending processing completed dead_letter superseded; do
        cn=${COUNTS[$st]:-0}
        if [[ $FIRST -eq 1 ]]; then FIRST=0; else printf ","; fi
        printf '"%s":%d' "$st" "$cn"
      done
      printf '}}\n'
    else
      printf "  ${C_DIM}%-14s %8s${C_RESET}\n" "status" "count"
      for st in pending processing completed dead_letter superseded; do
        cn=${COUNTS[$st]:-0}
        if [[ $cn -gt 0 ]]; then
          printf "  %-14s %8d\n" "$st" "$cn"
        fi
      done
      printf "  ${C_DIM}%-14s %8d${C_RESET}\n" "TOTAL" "$TOTAL"
    fi

    # Oldest pending — pager-duty signal.
    OLDEST_PENDING=$(sqlite3 -readonly "$DB_PATH" "
      SELECT COALESCE(MIN(created_at), '<none>') FROM outbox_events
      WHERE status='pending' $EVENT_TYPE_SQL;
    " 2>/dev/null || echo "<none>")
    if [[ -n "$OLDEST_PENDING" && "$OLDEST_PENDING" != "<none>" ]]; then
      log_info "oldest pending event created_at: $OLDEST_PENDING (operator-visible — append to pager triage if >5min ago)"
    fi
    exit 0
    ;;

  list-pending)
    WHERE="WHERE status='pending'"; ORDER="ORDER BY id ASC" ;;
  list-processing)
    WHERE="WHERE status='processing'"; ORDER="ORDER BY lease_expires_at ASC" ;;
  list-completed)
    WHERE="WHERE status='completed'"; ORDER="ORDER BY completed_at DESC" ;;
  list-stuck)
    # STUCK = pending AND attempt_count >= max_attempts - 2 (canonical pre-DLQ)
    # MAX_MISSING = pending AND max_attempts IS NULL OR 0 (can never reach threshold)
    WHERE="WHERE status='pending' AND ( (max_attempts IS NOT NULL AND max_attempts > 0 AND attempt_count >= max_attempts - 2) OR max_attempts IS NULL OR max_attempts = 0 )"; ORDER="ORDER BY attempt_count DESC" ;;
  list-dead-letter)
    WHERE="WHERE status IN ('dead_letter','superseded')"; ORDER="ORDER BY id DESC" ;;

  lookup)
    AGG_ESC=$(esc "$ARG")
    WHERE="WHERE aggregate_id = '${AGG_ESC}'"; ORDER="ORDER BY id DESC"
    log_info "lookup aggregate_id=$ARG (db=$DB_PATH)"
    ;;
esac

QUERY="SELECT id, event_type, status, attempt_count, COALESCE(max_attempts, 0), COALESCE(lease_id, '<NULL>'), CASE WHEN COALESCE(last_error,'') = '' THEN '<none>' ELSE substr(last_error, 1, 80) END, COALESCE(completed_at, '<NULL>'), COALESCE(created_at, '<NULL>'), COALESCE(aggregate_id, '<NULL>') FROM outbox_events $WHERE $EVENT_TYPE_SQL $ORDER LIMIT $LIMIT;"
# godlike/07: -readonly opens the DB in read-only mode so even a mistaken
# INSERT/UPDATE/DELETE in this script is rejected by sqlite3 itself.
ROWS=$(sqlite3 -readonly -separator $'\t' -nullvalue '<NULL>' "$DB_PATH" "$QUERY" 2>/dev/null || echo "")

if [[ -z "$ROWS" ]]; then
  if [[ $JSON_MODE -eq 1 ]]; then
    printf '{"db":"%s","subcommand":"%s","rows":[],"empty":true}\n' "$DB_PATH" "$SUBCMD"
  else
    log_info "no rows matched (subcommand=$SUBCMD)"
  fi
  exit 2
fi

COUNT=0
declare -a ROW_LINES
while IFS=$'\t' read -r line; do
  ROW_LINES+=("$line")
  COUNT=$((COUNT + 1))
done <<<"$ROWS"

if [[ $JSON_MODE -eq 1 ]]; then
  printf '{"db":"%s","subcommand":"%s","limit":%d,"count":%d,"rows":[' "$DB_PATH" "$SUBCMD" "$LIMIT" "$COUNT"
  FIRST=1
  for line in "${ROW_LINES[@]}"; do
    if [[ $FIRST -eq 1 ]]; then FIRST=0; else printf ","; fi
    IFS=$'\t' read -r id et st att mx ls err c_at created agg <<<"$line"
    jq -nc \
      --argjson id "$id" --arg et "$et" --arg st "$st" \
      --argjson att "$att" --argjson mx "$mx" --arg ls "$ls" --arg err "$err" \
      --arg c_at "$c_at" --arg created "$created" --arg agg "$agg" \
      '{id:$id, event_type:$et, status:$st, attempt_count:$att, max_attempts:$mx, lease_id:$ls, last_error:$err, completed_at:$c_at, created_at:$created, aggregate_id:$agg}'
  done
  printf ']}\n'
else
  printf "  ${C_DIM}%-7s %-30s %-12s %-4s %-4s %-40s %-22s %-22s %s${C_RESET}\n" \
    "id" "event_type" "status" "att" "max" "last_error" "completed_at" "created_at" "aggregate_id"
  STUCK_COUNT=0
  MAX_MISSING_COUNT=0
  for line in "${ROW_LINES[@]}"; do
    IFS=$'\t' read -r id et st att mx ls err c_at created agg <<<"$line"
    TAG=""
    if [[ "$st" == "pending" ]]; then
      if [[ "$mx" -le 0 ]]; then
        TAG="${C_RED}MAX_MISSING${C_RESET}"
        MAX_MISSING_COUNT=$((MAX_MISSING_COUNT + 1))
      elif [[ "$att" -ge $((mx - 2)) ]]; then
        TAG="${C_RED}STUCK${C_RESET}"
        STUCK_COUNT=$((STUCK_COUNT + 1))
      fi
    fi
    printf "  %-7s %-30s %-12s %-4s %-4s %-40s %-22s %-22s %s %s\n" \
      "$id" "$et" "$st" "$att" "$mx" "$err" "$c_at" "$created" "$agg" "$TAG"
  done
  log_info "matched=$COUNT rows (subcommand=$SUBCMD limit=$LIMIT)"
  if [[ $STUCK_COUNT -gt 0 ]]; then
    log_fail "outbox has $STUCK_COUNT STUCK row(s) (pending + attempts ≥ max-2) — escalate"
  fi
  if [[ $MAX_MISSING_COUNT -gt 0 ]]; then
    log_fail "outbox has $MAX_MISSING_COUNT MAX_MISSING row(s) (pending + max_attempts NULL/0) — escalate"
  fi
fi

exit 0
