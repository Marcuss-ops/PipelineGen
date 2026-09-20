#!/usr/bin/env bash
# scripts/deploy/pipelinegen_deploy.sh — the ONE command that turns "deploy"
# from a six-step hunt into build → install → restart → wait-for-ready →
# prove-the-running-binary-is-the-built-one.
#
# Why this exists (measured, not hypothetical)
# --------------------------------------------
# The previous procedure was manual and had three ways to lie:
#   1. `sudo -n install ... | tail` exited 0 even when sudo failed with
#      "a password is required", because the pipe masked the exit code; the
#      failure surfaced only later, by comparing sha1sum by hand.
#   2. The installed binary had to be moved into the path the unit executes
#      (WorkingDirectory/bin/pipelinegen) as a workaround.
#   3. Nothing waited for readiness: /health returned 000 for 45+ seconds and
#      the operator had to poll by eye.
#
# Contract
# --------
#   1. build    — `make build-server` (stamped through platform/buildinfo)
#   2. install  — the built binary IS bin/pipelinegen, the exact path the unit
#                 ExecStart runs, so no privileged install is required
#   3. restart  — scripts/systemd/pipelinegenctl restart (fail-closed `sudo -n`,
#                 never piped, so a denied restart fails the deploy)
#   4. ready    — block until /health answers, bounded by --health-timeout
#   5. identity — sha256 of /proc/<MainPID>/exe MUST equal the built binary
#
# Usage
# -----
#   scripts/deploy/pipelinegen_deploy.sh                 # full deploy
#   scripts/deploy/pipelinegen_deploy.sh --dry-run        # preflight + plan only
#   scripts/deploy/pipelinegen_deploy.sh --skip-build     # deploy the current bin
#   scripts/deploy/pipelinegen_deploy.sh --skip-restart   # install + verify only
#
# Exit codes
# ----------
#   0 success, 1 deploy/verification failure, 2 preflight/usage error.
#
# Testability: every external command is overridable via an env var so the
# self-test (scripts/deploy/deploy_selftest.sh) can exercise the restart,
# ready-wait and identity stages against fakes with no live systemd.
set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd -- "$SCRIPT_DIR/../.." && pwd)"

SERVICE="${DEPLOY_SERVICE:-pipelinegen.service}"
BASE_URL="${VELOX_BASE_URL:-http://127.0.0.1:8000}"
HEALTH_TIMEOUT="${DEPLOY_HEALTH_TIMEOUT:-90}"
READY_TIMEOUT="${DEPLOY_READY_TIMEOUT:-90}"

MAKE_BIN="${DEPLOY_MAKE_BIN:-make}"
CURL_BIN="${DEPLOY_CURL_BIN:-curl}"
SHA256_BIN="${DEPLOY_SHA256_BIN:-sha256sum}"
SHA1_BIN="${DEPLOY_SHA1_BIN:-sha1sum}"
SYSTEMCTL_BIN="${DEPLOY_SYSTEMCTL_BIN:-systemctl}"
PIPELINEGENCTL_BIN="${DEPLOY_PIPELINEGENCTL_BIN:-$ROOT_DIR/scripts/systemd/pipelinegenctl}"
PROC_ROOT="${DEPLOY_PROC_ROOT:-/proc}"

BIN_PATH="${DEPLOY_BIN_PATH:-$ROOT_DIR/bin/pipelinegen}"

DRY_RUN=0
SKIP_BUILD=0
SKIP_RESTART=0
for arg in "$@"; do
  case "$arg" in
    --dry-run)      DRY_RUN=1 ;;
    --skip-build)   SKIP_BUILD=1 ;;
    --skip-restart) SKIP_RESTART=1 ;;
    -h|--help)      sed -n '2,50p' "${BASH_SOURCE[0]}"; exit 0 ;;
    *) printf 'unknown argument: %s\n' "$arg" >&2; exit 2 ;;
  esac
done

log() { printf '[deploy] %s\n' "$*"; }
die() { printf '[deploy] ERROR: %s\n' "$*" >&2; exit 1; }

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

file_sha256() { "$SHA256_BIN" "$1" | cut -d' ' -f1; }

# ── 1. build ──────────────────────────────────────────────────────────
step_build() {
  if [ "$SKIP_BUILD" = "1" ]; then
    log "build: skipped (--skip-build); using $BIN_PATH"
  else
    log "build: make build-server"
    require_cmd "$MAKE_BIN"
    "$MAKE_BIN" -C "$ROOT_DIR" build-server
  fi
  [ -f "$BIN_PATH" ] || die "built binary not found at $BIN_PATH"
  [ -x "$BIN_PATH" ] || die "built binary is not executable: $BIN_PATH"
  BUILT_SHA="$(file_sha256 "$BIN_PATH")"
  BUILT_SHA1="$("$SHA1_BIN" "$BIN_PATH" | cut -d' ' -f1)"
  log "build: sha256=$BUILT_SHA"
}

# ── 2. install ────────────────────────────────────────────────────────
# No privileged install: the unit executes $ROOT_DIR/bin/pipelinegen, which is
# exactly where `make build-server` writes. Replacing the file is atomic from
# the running process's point of view (Linux keeps the old inode open), and the
# restart below is what makes the new inode the running one.
step_install() {
  log "install: $BIN_PATH is already the unit's ExecStart path — no sudo install"
}

# ── 3. restart ────────────────────────────────────────────────────────
step_restart() {
  if [ "$SKIP_RESTART" = "1" ]; then
    log "restart: skipped (--skip-restart)"
    return 0
  fi
  if [ ! -x "$PIPELINEGENCTL_BIN" ]; then
    die "pipelinegenctl not found or not executable: $PIPELINEGENCTL_BIN"
  fi
  require_cmd "$SYSTEMCTL_BIN"
  log "restart: pipelinegenctl restart (fail-closed sudo -n)"
  # IMPORTANT: never pipe this. A pipe would mask the exit code — the exact
  # failure that let a denied `sudo -n install` report success.
  if ! DEPLOY_SYSTEMCTL_BIN="$SYSTEMCTL_BIN" "$PIPELINEGENCTL_BIN" restart; then
    die "restart failed (check the restricted sudoers rule for $SERVICE)"
  fi
}

# ── 4. wait for ready ─────────────────────────────────────────────────
step_wait_health() {
  require_cmd "$CURL_BIN"
  local deadline=$((SECONDS + HEALTH_TIMEOUT))
  log "ready: waiting for $BASE_URL/health (max ${HEALTH_TIMEOUT}s)"
  while :; do
    if "$CURL_BIN" -sf --max-time 2 "$BASE_URL/health" >/dev/null 2>&1; then
      log "ready: /health answered"
      return 0
    fi
    if [ "$SECONDS" -ge "$deadline" ]; then
      die "/health did not answer within ${HEALTH_TIMEOUT}s"
    fi
    sleep 2
  done
}

# ── 5. identity ───────────────────────────────────────────────────────
main_pid() {
  "$SYSTEMCTL_BIN" show -p MainPID --value "$SERVICE" 2>/dev/null | tr -d '[:space:]'
}

step_identity() {
  if [ "$SKIP_RESTART" = "1" ]; then
    log "identity: skipped (--skip-restart)"
    return 0
  fi
  require_cmd "$SYSTEMCTL_BIN"
  local pid running_sha deadline
  deadline=$((SECONDS + READY_TIMEOUT))
  while :; do
    pid="$(main_pid)"
    if [ -n "$pid" ] && [ "$pid" != "0" ] && [ -e "$PROC_ROOT/$pid/exe" ]; then
      break
    fi
    [ "$SECONDS" -ge "$deadline" ] && die "no MainPID for $SERVICE after restart"
    sleep 1
  done
  running_sha="$(file_sha256 "$PROC_ROOT/$pid/exe")"
  if [ "$running_sha" != "$BUILT_SHA" ]; then
    die "running binary ($running_sha) is NOT the built binary ($BUILT_SHA)"
  fi
  log "identity: MainPID=$pid sha256=$running_sha — running IS the built binary"
}

if [ "$DRY_RUN" = "1" ]; then
  log "DRY RUN — plan only"
  log "  service:      $SERVICE"
  log "  base url:     $BASE_URL"
  log "  binary:       $BIN_PATH"
  log "  build:        $([ "$SKIP_BUILD" = "1" ] && echo skip || echo 'make build-server')"
  log "  restart:      $([ "$SKIP_RESTART" = "1" ] && echo skip || echo "$PIPELINEGENCTL_BIN restart")"
  log "  ready probe:  $BASE_URL/health (timeout ${HEALTH_TIMEOUT}s)"
  log "  identity:     sha256($PROC_ROOT/<MainPID>/exe) == sha256($BIN_PATH)"
  exit 0
fi

step_build
step_install
step_restart
step_wait_health
step_identity

printf '[deploy] OK  sha256=%s  sha1=%s\n' "$BUILT_SHA" "$BUILT_SHA1"
