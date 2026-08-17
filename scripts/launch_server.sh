#!/usr/bin/env bash
# Launch PipelineGen server fully detached from terminal.
#
# Environment (all optional):
#   PIPELINEGEN_BIN  path to server binary (default: ./pipelinegen)
#   VELOX_PORT       listen port (default: 8000)
#   VELOX_HOST       bind host (default: 127.0.0.1)
#
# Writes the launched PID to /tmp/pipelinegen.pid and logs to
# /tmp/pipelinegen.log.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR/.."

PIPELINEGEN_BIN="${PIPELINEGEN_BIN:-./pipelinegen}"
PORT="${VELOX_PORT:-8000}"
HOST="${VELOX_HOST:-127.0.0.1}"
HEALTH_HOST="${VELOX_HEALTH_HOST:-127.0.0.1}"
PID_FILE="${PIPELINEGEN_PID_FILE:-/tmp/pipelinegen.${PORT}.pid}"
LOG_FILE="${PIPELINEGEN_LOG_FILE:-/tmp/pipelinegen.${PORT}.log}"

export VELOX_HOST="$HOST"
export VELOX_PORT="$PORT"

# shellcheck source=scripts/lib/dotenv.sh
source "$SCRIPT_DIR/lib/dotenv.sh"

# Determine the admin-token provenance BEFORE loading .env so the boot log can
# report where the token came from without ever printing the token itself.
if [ -n "${VELOX_ADMIN_TOKEN:-}" ]; then
    AUTH_SOURCE="environment"
else
    AUTH_SOURCE="missing"
fi

# Explicit environment wins: .env only fills variables that are unset or
# empty, so a caller-provided VELOX_ADMIN_TOKEN is never silently overridden.
load_dotenv_missing .env

if [ -n "${VELOX_ADMIN_TOKEN:-}" ]; then
    TOKEN_PRESENT="true"
    if [ "$AUTH_SOURCE" = "missing" ]; then
        AUTH_SOURCE="dotenv"
    fi
else
    TOKEN_PRESENT="false"
    if [ "$AUTH_SOURCE" = "missing" ]; then
        AUTH_SOURCE="none"
    fi
fi

export VELOX_FEATURE_IMAGES_ENABLED=true

# Boot diagnostics: provenance + presence only. The token value is never
# printed, logged, or exposed to any command that could echo it.
printf 'auth_source=%s\n' "$AUTH_SOURCE"
printf 'token_present=%s\n' "$TOKEN_PRESENT"

# Kill any existing server on this port, preferring the stored PID file.
if [ -f "$PID_FILE" ]; then
    old_pid=$(cat "$PID_FILE" 2>/dev/null || true)
    if [ -n "$old_pid" ] && kill -0 "$old_pid" 2>/dev/null; then
        kill -TERM -"$old_pid" 2>/dev/null || true
        sleep 1
        kill -KILL -"$old_pid" 2>/dev/null || true
    fi
    rm -f "$PID_FILE"
fi
pkill -9 -f "$(basename "$PIPELINEGEN_BIN")" 2>/dev/null || true
sleep 1

# Ensure port is free.
fuser -k "${PORT}/tcp" 2>/dev/null || true
sleep 1

# Launch fully detached. Prefer setsid (Linux); fall back to nohup on macOS.
if command -v setsid >/dev/null 2>&1; then
  setsid "$PIPELINEGEN_BIN" --mode all </dev/null >"$LOG_FILE" 2>&1 &
else
  nohup "$PIPELINEGEN_BIN" --mode all </dev/null >"$LOG_FILE" 2>&1 &
fi
PID=$!
echo "$PID" > "$PID_FILE"
echo "Launched PID=$PID on ${HOST}:${PORT}"

# Wait for startup (max 15s).
for i in $(seq 1 15); do
  sleep 1
  HTTP=$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 "http://${HEALTH_HOST}:${PORT}/health" 2>/dev/null || echo "000")
  if [[ "$HTTP" == "200" ]]; then
    echo "Server healthy after ${i}s (HTTP $HTTP)"
    exit 0
  fi
done

echo "WARN: server not healthy after 15s"
tail -5 "$LOG_FILE"
exit 1
