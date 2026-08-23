#!/usr/bin/env bash
# scripts/operate_script_generate.sh — automatic operational pipeline
# for script.generate.
#
# Pipeline: build → test → controlled restart → readiness probe → smoke test.
# No manual server restart is required; the script tears down any existing
# PipelineGen server, boots a fresh one, probes it, runs the smoke test,
# and cleans up on exit.
#
# Environment (all optional):
#   VELOX_PORT                  listen port (default: 8000)
#   VELOX_HOST                  bind host (default: 127.0.0.1)
#   VELOX_ADMIN_TOKEN           admin bearer token (default: generated)
#   VELOX_WORKER_TOKEN          worker token (default: generated)
#   VELOX_DELIVERY_HMAC_SECRET  HMAC secret (default: generated)
#   SMOKE_POLL_TIMEOUT_SECONDS  smoke poll ceiling (default: 120)
#   TEST_TARGET                 make target for tests (default: test)
#   VELOX_KEEP_SERVER           if 1, leave the server running on success
#
# Usage:
#   scripts/operate_script_generate.sh
#
# Exit codes:
#   0  pipeline completed successfully
#   1  build/test/smoke failure
#   2  missing required tool or token

set -euo pipefail

cd "$(dirname "$0")/.."
ROOT=$(pwd)

# ── Optional .env sourcing ───────────────────────────────────────────────
# Explicit environment wins: .env only fills variables that are unset or
# empty, so a caller-provided VELOX_ADMIN_TOKEN is never silently overridden
# (the `set -a; source .env` pattern would clobber a live token and turn
# every request into a 401).
# shellcheck source=scripts/lib/dotenv.sh
source "$ROOT/scripts/lib/dotenv.sh"
load_dotenv_missing .env

# ── Configuration ────────────────────────────────────────────────────────
VELOX_PORT="${VELOX_PORT:-8000}"
VELOX_HOST="${VELOX_HOST:-127.0.0.1}"
SMOKE_API_BASE="${VELOX_HOST}:${VELOX_PORT}"
TEST_TARGET="${TEST_TARGET:-test}"
KEEP_SERVER="${VELOX_KEEP_SERVER:-0}"
export VELOX_PORT VELOX_HOST SMOKE_API_BASE

# ── Token generation ───────────────────────────────────────────────────
# Generate strong random tokens when the operator did not provide them.
# This lets the pipeline run out-of-the-box in CI/dev boxes.
generate_or_export() {
    local var_name="$1"
    if [ -z "${!var_name:-}" ]; then
        local val
        val=$(openssl rand -hex 32)
        export "$var_name=$val"
    fi
}

for tool in go curl jq openssl; do
    if ! command -v "$tool" >/dev/null 2>&1; then
        echo "❌ Missing required tool: $tool" >&2
        exit 2
    fi
done

generate_or_export VELOX_ADMIN_TOKEN
generate_or_export VELOX_WORKER_TOKEN
generate_or_export VELOX_DELIVERY_HMAC_SECRET

# ── Cleanup ──────────────────────────────────────────────────────────────
cleanup_server() {
    if [ "$KEEP_SERVER" == "1" ]; then
        echo "→ Leaving PipelineGen server running on ${VELOX_HOST}:${VELOX_PORT} (VELOX_KEEP_SERVER=1)."
        return 0
    fi

    echo "→ Cleaning up PipelineGen server..."
    local pid_file="/tmp/pipelinegen.${VELOX_PORT}.pid"
    if [ -f "$pid_file" ]; then
        local pid
        pid=$(cat "$pid_file" 2>/dev/null || true)
        if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
            # Kill the whole process group created by setsid.
            kill -TERM -"$pid" 2>/dev/null || true
            sleep 1
            kill -KILL -"$pid" 2>/dev/null || true
        fi
        rm -f "$pid_file"
    fi
    # Best-effort fallback: terminate any pipelinegen still listening on this port.
    pkill -f 'pipelinegen --mode all' 2>/dev/null || true
    fuser -k "${VELOX_PORT}/tcp" 2>/dev/null || true
}
trap cleanup_server EXIT INT TERM

# ── Step 1: Build ───────────────────────────────────────────────────────
echo ""
echo "=== Step 1/5: Build ==="
make build

# ── Step 2: Test ────────────────────────────────────────────────────────
echo ""
echo "=== Step 2/5: Tests (target: ${TEST_TARGET}) ==="
make "${TEST_TARGET}"

# ── Step 3: Controlled restart ──────────────────────────────────────────
echo ""
echo "=== Step 3/5: Controlled restart ==="
# Kill any existing server on this port.
PID_FILE="/tmp/pipelinegen.${VELOX_PORT}.pid"
if [ -f "$PID_FILE" ]; then
    old_pid=$(cat "$PID_FILE" 2>/dev/null || true)
    if [ -n "$old_pid" ] && kill -0 "$old_pid" 2>/dev/null; then
        kill -TERM -"$old_pid" 2>/dev/null || true
        sleep 1
        kill -KILL -"$old_pid" 2>/dev/null || true
    fi
    rm -f "$PID_FILE"
fi
pkill -9 -f "pipelinegen" 2>/dev/null || true
sleep 1
fuser -k "${VELOX_PORT}/tcp" 2>/dev/null || true
sleep 1

# Launch detached.
LOG_FILE="/tmp/pipelinegen.${VELOX_PORT}.log"
if command -v setsid >/dev/null 2>&1; then
  setsid "${ROOT}/bin/pipelinegen" --mode all </dev/null >"$LOG_FILE" 2>&1 &
else
  nohup "${ROOT}/bin/pipelinegen" --mode all </dev/null >"$LOG_FILE" 2>&1 &
fi
SERVER_PID=$!
echo "$SERVER_PID" > "$PID_FILE"
echo "Launched PID=$SERVER_PID on ${VELOX_HOST}:${VELOX_PORT}"

# Wait for startup (max 15s).
for i in $(seq 1 15); do
  sleep 1
  HTTP=$(curl -s -o /dev/null -w "%{http_code}" --max-time 2 "http://${VELOX_HOST}:${VELOX_PORT}/health" 2>/dev/null || echo "000")
  if [[ "$HTTP" == "200" ]]; then
    echo "Server healthy after ${i}s (HTTP $HTTP)"
    break
  fi
done
# ── Step 4: Readiness probe ────────────────────────────────────────────
echo ""
echo "=== Step 4/5: Readiness probe ==="
bash tests/operational/startup_smoke.sh

# ── Step 5: Smoke test ──────────────────────────────────────────────────
echo ""
echo "=== Step 5/5: Smoke test /api/script/generate ==="
bash tests/operational/generate/run.sh basic.json

# ── Success ─────────────────────────────────────────────────────────────
# By default, keep the server running after a successful pipeline so the
# operator can continue using it. Respect an explicit VELOX_KEEP_SERVER=0
# to tear it down on exit.
KEEP_SERVER=1
if [ "${VELOX_KEEP_SERVER:-1}" == "0" ]; then
    KEEP_SERVER=0
fi

echo ""
echo "✅ Operational pipeline for script.generate completed successfully."
echo "   Server is running on ${VELOX_HOST}:${VELOX_PORT}"
if [ "$KEEP_SERVER" == "1" ]; then
    echo "   It will be kept running. Set VELOX_KEEP_SERVER=0 to stop it on exit."
else
    echo "   It will be stopped on exit. Set VELOX_KEEP_SERVER=1 to keep it running."
fi
