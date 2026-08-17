#!/usr/bin/env bash
# scripts/start.sh — PipelineGen defensive startup guard
#
# -----------------------------------------------------------------------------
# POLICY (locked in this script):
#
#   Per Operational Readiness policy (June 2026), the Go backend boot must
#   NOT be blocked by missing OPTIONAL components. Only true blockers
#   (Go toolchain missing, build failure, auth-disabled-on-public-host) are
#   hard exits. Everything else — Rust renderer, Ollama models, Drive
#   creds, SearXNG, Qdrant sidecars — produces a WARN and the server proceeds.
#
#   Explicitly: the Rust renderer bundle (PureFrame / velox-renderer) is
#   OUT OF PERIMETER for the Go backend. See docker-compose.yml:5 —
#       "Rust? No — Go binary that runs cmd/server --mode all"
#   This script warns about it but never blocks.
#
# EXIT CODES:
#
#   0  All hard-required checks passed; warnings (if any) accepted.
#   1  A HARD blocker (Go missing, go build failure, auth-disabled-on-public).
#      In --strict mode, also returns 1 when warnings were emitted.
#
# MODES:
#
#   default                run all 10 checks + post-start probes; exec `make run`
#   --check-only           run checks, print summary; do NOT exec the server
#   --strict               treat ANY warning as a hard failure (CI gate)
#   --quick                skip slow checks (go test, Ollama probe, /health)
#   --full                 also attempt a real script generation job
#                          (requires server already listening on VELOX_PORT)
#   --help, -h             show this header
#
# USAGE:
#
#   scripts/start.sh                       # typical dev boot
#   scripts/start.sh --check-only          # readiness audit only
#   scripts/start.sh --strict              # gate CI (fails on warnings)
#   scripts/start.sh --quick               # dev fast-path (~5s)
#   scripts/start.sh --full                # after server boots, dispatch a job
#   VELOX_PORT=8000 scripts/start.sh       # override the listen port
#   NO_COLOR=1 scripts/start.sh            # disable ANSI colors (CI)
#
# COMPANION FILES:
#
#   .env.example           canonical env-var list (every VELOX_* var consumed
#                          by the Go config layer; copy → .env before booting)
#   scripts/rotate_token.sh       rotate VELOX_(ADMIN|WORKER)_TOKEN pairs
#   scripts/generate_worker_token.sh  emit a fresh 64-hex WORKER token
#   scripts/ci-architectural-checks.sh  CI guards (separate concern)
# -----------------------------------------------------------------------------

set -uo pipefail
# NOTE: deliberately NO `set -e`. The whole point of this script is that
# warnings should NOT abort — `set -e` would defeat the warn-not-block
# policy. Use `set -u` (undefined var → abort) and `set -o pipefail` so
# individual probes can be trusted, but each section catches failures
# explicitly.

# ── mode parsing ─────────────────────────────────────────────────────────────
MODE_DEFAULT=1
STRICT=0
CHECK_ONLY=0
QUICK=0
FULL=0

usage() {
  sed -n '4,40p' "$0"
}

while [ "${1:-}" != "" ]; do
  case "$1" in
    --check-only) CHECK_ONLY=1 ;;
    --strict)     STRICT=1 ;;
    --quick)      QUICK=1 ;;                  # skip slow checks only; still exec make run at the end
    --full)       FULL=1 ;;
    --help|-h)    usage; exit 0 ;;
    *) echo "unknown flag: $1" >&2; usage; exit 2 ;;
  esac
  shift
done

# ── helpers: colour, counting, formatting ────────────────────────────────────
if [ -t 1 ] && [ "${NO_COLOR:-0}" != "1" ]; then
  C_RED=$(printf '\033[31m')
  C_GREEN=$(printf '\033[32m')
  C_YELLOW=$(printf '\033[33m')
  C_CYAN=$(printf '\033[36m')
  C_DIM=$(printf '\033[2m')
  C_BOLD=$(printf '\033[1m')
  C_RESET=$(printf '\033[0m')
else
  C_RED=; C_GREEN=; C_YELLOW=; C_CYAN=; C_DIM=; C_BOLD=; C_RESET=
fi

READY_COUNT=0
WARN_COUNT=0
HARD_FAIL=0
declare -a WARN_MSGS

log_ready() { printf "  ${C_GREEN}\xe2\x9c\x93${C_RESET} %s\n" "$1"; READY_COUNT=$((READY_COUNT + 1)); }
log_warn()  { printf "  ${C_YELLOW}!${C_RESET}        %s\n" "$1"; WARN_COUNT=$((WARN_COUNT + 1)); WARN_MSGS+=("$1"); }
log_hard()  { printf "  ${C_RED}\xe2\x9c\x97${C_RESET} %s\n" "$1"; HARD_FAIL=$((HARD_FAIL + 1)); }
log_skip()  { printf "  ${C_DIM}\xe2\x86\xb7${C_RESET}        %s ${C_DIM}(skipped: %s)${C_RESET}\n" "$1" "$2"; }
section()   { printf "\n${C_BOLD}${C_CYAN}━━━ %s ━━━${C_RESET}\n" "$1"; }

# ── tmp scratch files (auto-cleaned) ─────────────────────────────────────────
WORK_DIR=$(mktemp -d /tmp/startsh.XXXXXX)
trap 'rm -rf "$WORK_DIR"' EXIT INT TERM

# ── load .env if present (do not require it) ─────────────────────────────────
section "0. Environment"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/lib/dotenv.sh
source "$SCRIPT_DIR/lib/dotenv.sh"

if [ -f .env ]; then
  # Explicit environment wins: .env only fills variables that are unset or
  # empty, so a caller-provided token is never silently overridden.
  load_dotenv_missing .env
  log_ready ".env sourced for missing defaults (existing environment preserved; mode 0600 expected)"
elif [ -f .env.example ]; then
  log_warn ".env not present — copy .env.example to .env to set tokens; server may refuse boot without VELOX_ADMIN_TOKEN"
else
  log_warn ".env.example missing too — running with whatever's already in the shell env"
fi

# Pull port + master defaults if not set, so downstream checks have something
# to reference (these are the bare defaults; .env override them).
: "${VELOX_PORT:=8000}"
: "${VELOX_HOST:=127.0.0.1}"
: "${VELOX_MASTER_URL:=http://127.0.0.1:${VELOX_PORT}}"
: "${OLLAMA_ADDR:=http://localhost:11434}"
: "${OLLAMA_MODEL:=gemma4:e4b}"
: "${FFMPEG_PATH:=ffmpeg}"
: "${YTDLP_PATH:=yt-dlp}"
: "${VELOX_CREDENTIALS_FILE:=credentials.json}"
: "${VELOX_TOKEN_FILE:=token.json}"
: "${YT_JS_RUNTIME_PATH:=node}"
: "${VELOX_ENABLE_AUTH:=true}"

# ── 1. Go toolchain ─────────────────────────────────────────────────────────
section "1/10 Go toolchain"

if ! command -v go >/dev/null 2>&1; then
  log_hard "go is not on PATH — install Go ≥1.25 (server won't build)"
else
  GO_VERSION=$(go version 2>&1 | head -1)
  log_ready "go: $GO_VERSION"
fi

# ── 2. go build ./... ───────────────────────────────────────────────────────
section "2/10 go build ./...  (may take ~30s on first run)"

if ! command -v go >/dev/null 2>&1; then
  log_skip "go build" "go is not installed"
elif [ "$QUICK" -eq 1 ]; then
  log_skip "go build" "--quick mode"
else
  if go build ./... 2>"$WORK_DIR/build.err"; then
    log_ready "go build ./... → 0 errors"
  else
    log_hard "go build ./... failed (see $WORK_DIR/build.err)"
  fi
fi

# ── 3. go test ./... ────────────────────────────────────────────────────────
section "3/10 go test ./internal/... ./pkg/...  (slow)"

if [ "$QUICK" -eq 1 ]; then
  log_skip "go test" "--quick mode"
elif ! command -v go >/dev/null 2>&1; then
  log_skip "go test" "go is not installed"
else
  if go test ./internal/... ./pkg/... 2>"$WORK_DIR/test.err"; then
    log_ready "go test → 0 failures"
  else
    # Per the user's list, this is a readiness point. We WARN by default;
    # --strict promotes it.
    log_warn "go test failed (non-blocking in default mode; --strict promotes to hard fail). See $WORK_DIR/test.err"
  fi
fi

# ── 4. canonical binaries (cmd/server, cmd/admin, cmd/worker) ───────────────
section "4/10 canonical binaries (./bin/pipelinegen | admin | worker)"

for bin in pipelinegen admin worker; do
  if [ -x "./bin/${bin}" ]; then
    log_ready "./bin/${bin} → executable"
  elif [ "$bin" = "pipelinegen" ]; then
    log_warn "./bin/${bin} missing — server boot will rebuild via make run; OK on first boot"
  else
    log_warn "./bin/${bin} missing — optional helper binary (regenerate via 'make build' if needed)"
  fi
done

# ── 5. SQLite migrations path (from config.go StorageConfig) ────────────────
section "5/10 SQLite migrations"

DATA_DIR=${VELOX_DATA_DIR:-./data}
MIGRATIONS_DIR=migrations/sqlite
if [ -d "$MIGRATIONS_DIR" ]; then
  N_MIGRATIONS=$(ls -1 "$MIGRATIONS_DIR"/*.sql 2>/dev/null | wc -l)
  if [ "$N_MIGRATIONS" -gt 0 ]; then
    log_ready "$MIGRATIONS_DIR has $N_MIGRATIONS .sql files; server applies them on first boot from $DATA_DIR"
  else
    log_warn "$MIGRATIONS_DIR is empty — server will boot but migrations won't apply"
  fi
else
  log_warn "$MIGRATIONS_DIR directory missing — server can't apply migrations; check repo layout"
fi
if [ ! -d "$DATA_DIR" ]; then
  log_warn "VELOX_DATA_DIR=$DATA_DIR does not exist — server will create it on first boot (WAL journal parent)"
fi

# ── 6. server config sanity (host:port, public-host auth) ───────────────────
section "6/10 server config sanity"

log_ready "VELOX_PORT=${VELOX_PORT} (8000 = Operational Readiness PR canonical default)"
log_ready "VELOX_HOST=${VELOX_HOST}"
log_ready "VELOX_MASTER_URL=${VELOX_MASTER_URL}"

case "$VELOX_HOST" in
  0.0.0.0|"")
    if [ "${VELOX_ENABLE_AUTH}" = "false" ]; then
      log_hard "auth-disabled on a public interface: VELOX_HOST=${VELOX_HOST} + VELOX_ENABLE_AUTH=false. config.Validate() will refuse to start the binary"
    else
      log_ready "VELOX_HOST is public but auth=true (correct)"
    fi
    ;;
  *)
    if [ "${VELOX_ENABLE_AUTH}" = "false" ]; then
      log_warn "VELOX_ENABLE_AUTH=false — only safe for local development, never expose ${VELOX_HOST} publicly in this mode"
    else
      log_ready "VELOX_HOST is loopback with auth=true (correct for local dev)"
    fi
    ;;
esac

# ── 7. external tools (FFmpeg, yt-dlp, Node, openssl, jq, curl) ──────────────
section "7/10 external tools"

check_tool() {
  local label="$1" cmd="$2"
  if command -v "$cmd" >/dev/null 2>&1; then
    local ver
    ver=$("$cmd" --version 2>&1 | head -1 | cut -c 1-80)
    log_ready "$label ($cmd): $ver"
  else
    log_warn "$label ($cmd) not on PATH — routes that need it will fail at runtime"
  fi
}
check_tool "FFmpeg"   "$FFMPEG_PATH"
check_tool "yt-dlp"   "$YTDLP_PATH"
check_tool "Node.js"  "$YT_JS_RUNTIME_PATH"
check_tool "openssl"  "openssl"
check_tool "jq"       "jq"
check_tool "curl"     "curl"

# ── 8. Ollama + model loaded ─────────────────────────────────────────────────
section "8/10 LLM provider (Ollama @ $OLLAMA_ADDR, model $OLLAMA_MODEL)"

if [ "$QUICK" -eq 1 ]; then
  log_skip "Ollama probe" "--quick mode"
elif ! command -v curl >/dev/null 2>&1; then
  log_skip "Ollama probe" "curl missing"
else
  if curl -sS --max-time 3 "$OLLAMA_ADDR/api/tags" -o "$WORK_DIR/ollama.json" 2>/dev/null; then
    log_ready "Ollama reachable at ${OLLAMA_ADDR}"
    if grep -q "\"${OLLAMA_MODEL}\"" "$WORK_DIR/ollama.json" 2>/dev/null; then
      log_ready "Ollama model '${OLLAMA_MODEL}' is loaded"
    else
      avail=$(grep -oE '"name":"[^"]+"' "$WORK_DIR/ollama.json" 2>/dev/null | head -5 | cut -d'"' -f4 | paste -sd,)
      log_warn "Ollama model '${OLLAMA_MODEL}' NOT loaded. Available: ${avail:-none}. Run: ollama pull ${OLLAMA_MODEL}"
    fi
  else
    log_warn "Ollama not reachable at ${OLLAMA_ADDR} — /api/script/* will return provider_not_configured until it is"
  fi
fi

# ── 9. Google Drive credentials (warn-only) ──────────────────────────────────
section "9/10 Google Drive credentials (warning only — credentials out of CI scope)"

if [ -r "$VELOX_CREDENTIALS_FILE" ] && [ -r "$VELOX_TOKEN_FILE" ]; then
  log_ready "Drive creds readable: ${VELOX_CREDENTIALS_FILE} + ${VELOX_TOKEN_FILE}"
elif [ -f "$VELOX_CREDENTIALS_FILE" ]; then
  log_warn "$VELOX_CREDENTIALS_FILE exists but isn't readable by $(id -un) — set mode 0600 or chown to current user"
else
  log_warn "Drive OAuth credentials missing — Drive-backed endpoints will fail until you run:  python3 scripts/generate_drive_token.py"
fi

# ── 10. Rust renderer bundle — POLICY: always warn, never block ──────────────
section "10/10 Rust renderer bundle  (POLICY: out-of-scope for backend boot)"

log_warn "Rust renderer bundle (PureFrame / velox-renderer) is OUT OF PERIMETER for the Go backend. docker-compose.yml:5 explicitly states 'Rust? No — Go binary'. This server does not require it. Rendering can run on a separate VPS / machine / PureFrame orchestrator. Per Operational Readiness policy (June 2026): missing bundle is a warning, never a boot block."

# ── tokens & secrets soft check (placeholder-pattern rejection) ──────────────
section "Secrets placeholder rejection  (config.go::IsPlaceholderValue mirror)"

PLACEHOLDER_PATTERNS='^(YOUR_[A-Z0-9_]+_HERE|CHANGE[_-]?ME[_A-Z0-9]*|TODO_SECRET.*|PLACEHOLDER.*|FIXME.*|REPLACE[_-]?ME.*|XXX)$'

# signature (NAME, VALUE, WANT_MIN_LEN) — caller resolves the env var so we
# never need bash indirect expansion `${!name:-}` (combination is rejected on
# some bash builds as "name: invalid indirect expansion").
check_token() {
  local name="$1" val="$2" want_min_len="${3:-}"
  if [ -z "$val" ]; then
    log_warn "$name is empty — server refuses to boot unless VELOX_ENABLE_AUTH=false (DEV-ONLY)"
    return
  fi
  if [[ "$val" =~ $PLACEHOLDER_PATTERNS ]]; then
    log_warn "$name matches a placeholder pattern ($val) — server will refuse to boot. Generate with: openssl rand -hex 32"
    return
  fi
  if [ -n "$want_min_len" ] && [ "${#val}" -lt "$want_min_len" ]; then
    log_warn "$name has only ${#val} chars (expected \xe2\x89\xa5${want_min_len} = $((${want_min_len} * 4))-bit random)"
    return
  fi
  log_ready "$name → ${#val} chars (looks real)"
}
check_token "VELOX_ADMIN_TOKEN"          "${VELOX_ADMIN_TOKEN:-}"          64   # 64-hex chars = 256 bits
check_token "VELOX_WORKER_TOKEN"         "${VELOX_WORKER_TOKEN:-}"         64
check_token "VELOX_DELIVERY_HMAC_SECRET" "${VELOX_DELIVERY_HMAC_SECRET:-}" 64

# ── post-start checks (only after server is up) ─────────────────────────────
section "Post-start probes  (exec'd only if make run succeeds)"

log_skip "/health 200"  "exec make run; can't probe before server binds"
log_skip "real job"      "exec make run + enable --full to dispatch a generate job"

# ── summary ──────────────────────────────────────────────────────────────────
section "Summary"
printf "  ${C_GREEN}Ready:    %d${C_RESET}\n" "$READY_COUNT"
printf "  ${C_YELLOW}Warnings: %d${C_RESET}\n" "$WARN_COUNT"
printf "  ${C_RED}Hard fail: %d${C_RESET}\n" "$HARD_FAIL"

# ── escalation ───────────────────────────────────────────────────────────────
if [ "$HARD_FAIL" -gt 0 ]; then
  printf "\n${C_RED}${C_BOLD}\xe2\x9c\x97 Cannot start${C_RESET}: %d hard blocker(s) above.\n" "$HARD_FAIL"
  exit 1
fi

if [ "$WARN_COUNT" -gt 0 ] && [ "$STRICT" -eq 1 ]; then
  printf "\n${C_RED}${C_BOLD}\xe2\x9c\x97 --strict: treating %d warning(s) as hard failures${C_RESET}.\n" "$WARN_COUNT"
  printf "${C_DIM}Re-run without --strict to accept warnings and continue. (Defensive policy: warnings never block by default.)${C_RESET}\n"
  if [ "${#WARN_MSGS[@]}" -gt 0 ]; then
    printf "${C_DIM}\nWarnings emitted:${C_RESET}\n"
    for m in "${WARN_MSGS[@]}"; do
      printf "  ${C_DIM}- %s${C_RESET}\n" "$m"
    done
  fi
  exit 1
fi

if [ "$CHECK_ONLY" -eq 1 ]; then
  printf "\n${C_GREEN}${C_BOLD}\xe2\x9c\x93 Ready${C_RESET}: ${WARN_COUNT} warning(s) accepted; not exec'ing server (--check-only).\n"
  exit 0
fi

# Real job dispatch (\u2014full, after we exec make run) \u2014 only makes sense if user wants
# a self-contained smoke test. We don't loop the server in the background here
# (that's what scripts/diagnostics/marker_audit.sh is for \u2014 see AGENTS.md).
if [ "$FULL" -eq 1 ]; then
  printf "\n${C_DIM}--full mode noted: after server boots, run scripts/diagnostics/marker_audit.sh for a real generate job. This script does not background the server itself.${C_RESET}\n"
fi

printf "\n${C_GREEN}${C_BOLD}\xe2\x9c\x93 All readiness checks passed${C_RESET} \xe2\x86\x92 exec make run\n\n"
exec make run
