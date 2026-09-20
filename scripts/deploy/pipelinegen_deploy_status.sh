#!/usr/bin/env bash
# scripts/deploy/pipelinegen_deploy_status.sh — the single command that answers
# "is the running pipelinegen the binary on disk, and is it healthy?".
#
# Before this existed the answer was assembled by hand from `systemctl status`,
# a sha1sum of the file, and `strings /proc/<pid>/exe | grep failure_code` —
# which is precisely how a stale binary was mistaken for a deployed one.
#
# It prints, in one block:
#   - the on-disk binary hash (and its mtime)
#   - the RUNNING binary hash via /proc/<MainPID>/exe
#   - a MATCH / MISMATCH verdict
#   - process uptime
#   - /health and /ready status
#
# Usage:
#   scripts/deploy/pipelinegen_deploy_status.sh
#   scripts/deploy/pipelinegen_deploy_status.sh --json
#
# Exit codes: 0 when MATCH and /health is OK, 1 otherwise, 2 usage.
set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd -- "$SCRIPT_DIR/../.." && pwd)"

SERVICE="${DEPLOY_SERVICE:-pipelinegen.service}"
BASE_URL="${VELOX_BASE_URL:-http://127.0.0.1:8000}"
BIN_PATH="${DEPLOY_BIN_PATH:-$ROOT_DIR/bin/pipelinegen}"

SYSTEMCTL_BIN="${DEPLOY_SYSTEMCTL_BIN:-systemctl}"
CURL_BIN="${DEPLOY_CURL_BIN:-curl}"
SHA256_BIN="${DEPLOY_SHA256_BIN:-sha256sum}"
PS_BIN="${DEPLOY_PS_BIN:-ps}"
PROC_ROOT="${DEPLOY_PROC_ROOT:-/proc}"

JSON=0
for arg in "$@"; do
  case "$arg" in
    --json) JSON=1 ;;
    -h|--help) sed -n '2,30p' "${BASH_SOURCE[0]}"; exit 0 ;;
    *) printf 'unknown argument: %s\n' "$arg" >&2; exit 2 ;;
  esac
done

sha256_of() { "$SHA256_BIN" "$1" 2>/dev/null | cut -d' ' -f1; }

DISK_SHA=""
DISK_MTIME=""
if [ -f "$BIN_PATH" ]; then
  DISK_SHA="$(sha256_of "$BIN_PATH")"
  if command -v stat >/dev/null 2>&1; then
    DISK_MTIME="$(stat -c '%y' "$BIN_PATH" 2>/dev/null || stat -f '%Sm' "$BIN_PATH" 2>/dev/null || echo '')"
  fi
fi

MAIN_PID=""
if command -v "$SYSTEMCTL_BIN" >/dev/null 2>&1; then
  MAIN_PID="$("$SYSTEMCTL_BIN" show -p MainPID --value "$SERVICE" 2>/dev/null | tr -d '[:space:]' || true)"
fi
[ "$MAIN_PID" = "0" ] && MAIN_PID=""

RUN_SHA=""
UPTIME=""
if [ -n "$MAIN_PID" ] && [ -e "$PROC_ROOT/$MAIN_PID/exe" ]; then
  RUN_SHA="$(sha256_of "$PROC_ROOT/$MAIN_PID/exe")"
  if command -v "$PS_BIN" >/dev/null 2>&1; then
    UPTIME="$("$PS_BIN" -o etime= -p "$MAIN_PID" 2>/dev/null | tr -d '[:space:]' || true)"
  fi
fi

VERDICT="UNKNOWN"
if [ -n "$DISK_SHA" ] && [ -n "$RUN_SHA" ]; then
  if [ "$DISK_SHA" = "$RUN_SHA" ]; then VERDICT="MATCH"; else VERDICT="MISMATCH"; fi
elif [ -z "$RUN_SHA" ]; then
  VERDICT="NOT_RUNNING"
fi

HEALTH_CODE="000"
HEALTH_BODY=""
if command -v "$CURL_BIN" >/dev/null 2>&1; then
  HEALTH_BODY="$("$CURL_BIN" -s --max-time 3 "$BASE_URL/health" 2>/dev/null || true)"
  HEALTH_CODE="$("$CURL_BIN" -s -o /dev/null -w '%{http_code}' --max-time 3 "$BASE_URL/health" 2>/dev/null || echo 000)"
fi

READY_CODE="000"
if command -v "$CURL_BIN" >/dev/null 2>&1; then
  READY_CODE="$("$CURL_BIN" -s -o /dev/null -w '%{http_code}' --max-time 3 "$BASE_URL/ready" 2>/dev/null || echo 000)"
fi

if [ "$JSON" = "1" ]; then
  printf '{"service":"%s","bin_path":"%s","disk_sha256":"%s","disk_mtime":"%s","main_pid":"%s","running_sha256":"%s","verdict":"%s","uptime":"%s","health_code":"%s","ready_code":"%s"}\n' \
    "$SERVICE" "$BIN_PATH" "$DISK_SHA" "$DISK_MTIME" "$MAIN_PID" "$RUN_SHA" "$VERDICT" "$UPTIME" "$HEALTH_CODE" "$READY_CODE"
else
  printf 'service        : %s\n' "$SERVICE"
  printf 'binary on disk : %s\n' "${BIN_PATH:-<missing>}"
  printf '  sha256       : %s\n' "${DISK_SHA:-<missing>}"
  printf '  mtime        : %s\n' "${DISK_MTIME:-<unknown>}"
  printf 'running pid    : %s\n' "${MAIN_PID:-<none>}"
  printf '  sha256       : %s\n' "${RUN_SHA:-<unavailable>}"
  printf '  uptime       : %s\n' "${UPTIME:-<unknown>}"
  printf 'verdict        : %s\n' "$VERDICT"
  printf 'health         : HTTP %s %s\n' "$HEALTH_CODE" "$HEALTH_BODY"
  printf 'ready          : HTTP %s\n' "$READY_CODE"
fi

if [ "$VERDICT" = "MATCH" ] && [ "$HEALTH_CODE" = "200" ]; then
  exit 0
fi
exit 1
