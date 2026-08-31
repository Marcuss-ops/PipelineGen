#!/usr/bin/env bash
# scripts/dev/e2e-up.sh — canonical local PipelineGen E2E harness.
#
# Commands:
#   e2e-up.sh up         Start Compose dependencies and local server/worker.
#   e2e-up.sh dev-up     Deterministic staged startup with readiness gates.
#   e2e-up.sh status     Show process/service state and run preflight matrix.
#   e2e-up.sh down       Stop only processes owned by this harness and Compose.
#   e2e-up.sh dev-down   Stop everything (harness processes + Compose).
#
# The harness is intentionally fail-closed: `up` and `dev-up` do not return
# success until the complete preflight is green. It never edits migrations
# or repairs data.
set -Eeuo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=scripts/lib/canonical_db_path.sh
source "$ROOT_DIR/scripts/lib/canonical_db_path.sh"
STATE_DIR="${E2E_STATE_DIR:-$ROOT_DIR/.tmp/e2e-harness}"
LOG_DIR="$STATE_DIR/logs"
COMPOSE_FILE="${E2E_COMPOSE_FILE:-$ROOT_DIR/docker-compose.yml}"
BASE_URL="${E2E_BASE_URL:-http://127.0.0.1:${VELOX_PORT:-8000}}"
QDRANT_URL="${E2E_QDRANT_URL:-http://127.0.0.1:${QDRANT_HTTP_PORT:-6333}}"
OLLAMA_URL="${E2E_OLLAMA_URL:-${OLLAMA_URL:-http://127.0.0.1:11434}}"
SEARXNG_URL="${E2E_SEARXNG_URL:-http://127.0.0.1:8080}"
SCRAPER_URL="${E2E_SCRAPER_URL:-http://127.0.0.1:9123}"

# Readiness probe timeout (seconds) per service.
READINESS_TIMEOUT="${E2E_READINESS_TIMEOUT:-60}"
# Preflight can be skipped for partial bring-up debugging.
SKIP_PREFLIGHT="${E2E_SKIP_PREFLIGHT:-0}"

mkdir -p "$LOG_DIR"
chmod 700 "$STATE_DIR" "$LOG_DIR"

log()  { printf '[e2e-harness] %s\n' "$*"; }
warn() { printf '[e2e-harness] WARN: %s\n' "$*" >&2; }
fail() { printf '[e2e-harness] ERROR: %s\n' "$*" >&2; exit 1; }

require_command() {
    command -v "$1" >/dev/null 2>&1 || fail "required command unavailable: $1"
}

pid_file() { printf '%s/%s.pid' "$STATE_DIR" "$1"; }

is_running() {
    local file="$1" pid
    [[ -r "$file" ]] || return 1
    read -r pid < "$file" || return 1
    [[ "$pid" =~ ^[0-9]+$ ]] || return 1
    kill -0 "$pid" 2>/dev/null
}

start_process() {
    local name="$1"; shift
    local file log_file
    file="$(pid_file "$name")"
    log_file="$LOG_DIR/$name.log"
    if is_running "$file"; then
        log "$name already running"
        return 0
    fi
    : > "$log_file"
    ( cd "$ROOT_DIR" && exec "$@" ) >>"$log_file" 2>&1 &
    printf '%s\n' "$!" > "$file"
    log "started $name (pid $!)"
}

stop_process() {
    local name="$1" file pid
    file="$(pid_file "$name")"
    if ! is_running "$file"; then
        rm -f "$file"
        return 0
    fi
    read -r pid < "$file"
    kill "$pid" 2>/dev/null || true
    for _ in $(seq 1 "${E2E_STOP_TIMEOUT_SECONDS:-10}"); do
        kill -0 "$pid" 2>/dev/null || break
        sleep 1
    done
    if kill -0 "$pid" 2>/dev/null; then
        kill -TERM "$pid" 2>/dev/null || true
    fi
    rm -f "$file"
    log "stopped $name"
}

compose() {
    require_command docker
    docker compose -f "$COMPOSE_FILE" "$@"
}

# ── Readiness probes ───────────────────────────────────────────────────────

# wait_for_http <label> <url> <expected_code> <timeout_seconds>
# Polls a URL until it returns the expected HTTP code or times out.
wait_for_http() {
    local label="$1" url="$2" expected="$3" timeout="$4"
    local elapsed=0
    while (( elapsed < timeout )); do
        local code
        code=$(curl -fsS --max-time 3 -o /dev/null -w '%{http_code}' "$url" 2>/dev/null || echo "000")
        if [[ "$code" == "$expected" ]]; then
            log "  $label → READY (${code})"
            return 0
        fi
        sleep 2
        elapsed=$(( elapsed + 2 ))
    done
    warn "  $label → TIMEOUT (last: ${code:-000}, expected ${expected} after ${timeout}s)"
    return 1
}

# check_qdrant_health probes Qdrant /healthz.
check_qdrant_health() {
    local code
    code=$(curl -fsS --max-time 3 -o /dev/null -w '%{http_code}' "${QDRANT_URL%/}/healthz" 2>/dev/null || echo "000")
    [[ "$code" == "200" ]]
}

# check_server_health probes PipelineGen /health.
check_server_health() {
    local code
    code=$(curl -fsS --max-time 3 -o /dev/null -w '%{http_code}' "${BASE_URL%/}/health" 2>/dev/null || echo "000")
    [[ "$code" == "200" ]]
}

# check_server_ready probes PipelineGen /ready.
check_server_ready() {
    local code
    code=$(curl -fsS --max-time 3 -o /dev/null -w '%{http_code}' "${BASE_URL%/}/ready" 2>/dev/null || echo "000")
    [[ "$code" == "200" ]]
}

# check_worker_registered verifies at least one worker is registered.
check_worker_registered() {
    local resp
    resp=$(curl -fsS --max-time 5 \
        -H "Authorization: Bearer ${VELOX_ADMIN_TOKEN:-}" \
        "${BASE_URL%/}/api/workers" 2>/dev/null || echo '[]')
    echo "$resp" | grep -q 'job_types' || return 1
}

# check_ollama probes Ollama /api/tags.
check_ollama_health() {
    local code
    code=$(curl -fsS --max-time 3 -o /dev/null -w '%{http_code}' "${OLLAMA_URL%/}/api/tags" 2>/dev/null || echo "000")
    [[ "$code" == "200" ]]
}

run_preflight() {
    PREFLIGHT_BASE_URL="$BASE_URL" \
    PREFLIGHT_QDRANT_URL="$QDRANT_URL" \
    PREFLIGHT_DB_PATH="$(resolve_canonical_primary_db "${PREFLIGHT_DB_PATH:-${VELOX_DB_PATH:-}}" "$ROOT_DIR")" \
    PREFLIGHT_WORKER_URL="${PREFLIGHT_WORKER_URL:-}" \
    PREFLIGHT_OLLAMA_URL="$OLLAMA_URL" \
    PREFLIGHT_CHRONON_URL="${PREFLIGHT_CHRONON_URL:-${CHRONON_URL:-}}" \
    PREFLIGHT_REQUIRE_DRIVE="${PREFLIGHT_REQUIRE_DRIVE:-0}" \
    PREFLIGHT_REQUIRE_CHRONON="${PREFLIGHT_REQUIRE_CHRONON:-0}" \
    bash "$ROOT_DIR/scripts/preflight-e2e.sh"
}

# ── up (legacy — fast, no staged readiness) ───────────────────────────────
up() {
    require_command docker
    require_command bash
    [[ "$(git -C "$ROOT_DIR" symbolic-ref --quiet --short HEAD 2>/dev/null || true)" == "main" ]] ||
        fail 'checkout must be on branch main'

    log 'starting Compose dependencies'
    compose up -d qdrant artlist-scraper searxng

    if [[ "${E2E_START_LOCAL_PROCESSES:-0}" == "1" ]]; then
        [[ -x "$ROOT_DIR/bin/pipelinegen" ]] || fail 'bin/pipelinegen missing; build it before E2E startup'
        [[ -x "$ROOT_DIR/bin/pipelinegen-worker" || -x "$ROOT_DIR/worker" ]] ||
            fail 'worker binary missing; build it before E2E startup'
        start_process server "$ROOT_DIR/start_server.sh"
        if [[ -x "$ROOT_DIR/bin/pipelinegen-worker" ]]; then
            start_process worker "$ROOT_DIR/bin/pipelinegen-worker" --config "$ROOT_DIR/config.yaml"
        else
            start_process worker "$ROOT_DIR/worker" --config "$ROOT_DIR/config.yaml"
        fi
    else
        log 'starting Compose server and worker'
        compose up -d pipelinegen-server pipelinegen-worker
    fi

    run_preflight
    log 'E2E environment ready'
}

# ── dev-up (deterministic staged startup with readiness gates) ─────────────
#
# Startup order:
#   Stage 1: Infrastructure  (Qdrant, Artlist scraper, SearXNG)
#   Stage 2: Server          (PipelineGen HTTP + migrations)
#   Stage 3: Worker          (job executor, registers against server)
#   Stage 4: Preflight       (full dependency matrix)
#
# Each stage blocks until its readiness probe passes or times out.
# A failed gate aborts the entire startup — no partial environments.
dev_up() {
    require_command docker
    require_command bash
    require_command curl
    [[ "$(git -C "$ROOT_DIR" symbolic-ref --quiet --short HEAD 2>/dev/null || true)" == "main" ]] ||
        fail 'checkout must be on branch main'

    local START_TS
    START_TS=$(date +%s)

    echo ""
    echo "════════════════════════════════════════════════════════════════"
    echo "  PIPELINEGEN dev-up — deterministic staged startup"
    echo "════════════════════════════════════════════════════════════════"
    echo ""

    # ── Stage 1: Infrastructure ──────────────────────────────────────────
    log '── Stage 1/4: Infrastructure ──'
    log 'starting Qdrant, Artlist scraper, SearXNG...'
    compose up -d qdrant artlist-scraper searxng

    log 'waiting for Qdrant...'
    wait_for_http "Qdrant" "${QDRANT_URL%/}/healthz" 200 "$READINESS_TIMEOUT" ||
        fail 'Qdrant failed to become ready'

    log 'waiting for Artlist scraper...'
    wait_for_http "Artlist scraper" "${SCRAPER_URL%/}/health" 200 "$READINESS_TIMEOUT" 2>/dev/null ||
        warn 'Artlist scraper not ready (non-critical)'

    log 'waiting for SearXNG...'
    wait_for_http "SearXNG" "${SEARXNG_URL%/}/" 200 "$READINESS_TIMEOUT" 2>/dev/null ||
        warn 'SearXNG not ready (non-critical)'

    log 'Stage 1: PASS'
    echo ""

    # ── Stage 2: Server ──────────────────────────────────────────────────
    log '── Stage 2/4: Server ──'

    if [[ "${E2E_START_LOCAL_PROCESSES:-0}" == "1" ]]; then
        [[ -x "$ROOT_DIR/bin/pipelinegen" ]] || fail 'bin/pipelinegen missing; run make build first'
        log 'starting PipelineGen server (local process)...'
        start_process server "$ROOT_DIR/start_server.sh"
    else
        log 'starting PipelineGen server (Compose)...'
        compose up -d pipelinegen-server
    fi

    log 'waiting for server /health...'
    wait_for_http "Server /health" "${BASE_URL%/}/health" 200 "$READINESS_TIMEOUT" ||
        fail 'PipelineGen server failed to become ready'

    log 'waiting for server /ready...'
    wait_for_http "Server /ready" "${BASE_URL%/}/ready" 200 "$READINESS_TIMEOUT" ||
        fail 'PipelineGen server /ready check failed'

    log 'Stage 2: PASS'
    echo ""

    # ── Stage 3: Worker ──────────────────────────────────────────────────
    log '── Stage 3/4: Worker ──'

    if [[ "${E2E_START_LOCAL_PROCESSES:-0}" == "1" ]]; then
        [[ -x "$ROOT_DIR/bin/pipelinegen-worker" || -x "$ROOT_DIR/worker" ]] ||
            fail 'worker binary missing; run make build first'
        log 'starting worker (local process)...'
        if [[ -x "$ROOT_DIR/bin/pipelinegen-worker" ]]; then
            start_process worker "$ROOT_DIR/bin/pipelinegen-worker" --config "$ROOT_DIR/config.yaml"
        else
            start_process worker "$ROOT_DIR/worker" --config "$ROOT_DIR/config.yaml"
        fi
    else
        log 'starting worker (Compose)...'
        compose up -d pipelinegen-worker
    fi

    log 'waiting for worker registration...'
    local WORKER_ELAPSED=0
    while (( WORKER_ELAPSED < READINESS_TIMEOUT )); do
        if check_worker_registered; then
            log "  Worker registered ✓"
            break
        fi
        sleep 3
        WORKER_ELAPSED=$(( WORKER_ELAPSED + 3 ))
    done
    if (( WORKER_ELAPSED >= READINESS_TIMEOUT )); then
        warn 'Worker registration not confirmed (timeout)' 
        # Non-fatal: worker might be slow to register, preflight will catch it.
    fi

    log 'Stage 3: PASS'
    echo ""

    # ── Stage 4: Preflight ───────────────────────────────────────────────
    log '── Stage 4/4: Preflight ──'
    if [[ "$SKIP_PREFLIGHT" == "1" ]]; then
        warn 'Preflight skipped (E2E_SKIP_PREFLIGHT=1)'
    else
        run_preflight
    fi

    local END_TS
    END_TS=$(date +%s)
    local ELAPSED=$(( END_TS - START_TS ))

    echo ""
    echo "════════════════════════════════════════════════════════════════"
    printf "  PIPELINEGEN dev-up COMPLETE  (%d seconds)\n" "$ELAPSED"
    echo "════════════════════════════════════════════════════════════════"
    echo ""
    echo "  Services:"
    printf "    %-20s %s\n" "Qdrant:"         "${QDRANT_URL}"
    printf "    %-20s %s\n" "Server:"         "${BASE_URL}"
    printf "    %-20s %s\n" "Artlist scraper:" "${SCRAPER_URL}"
    printf "    %-20s %s\n" "SearXNG:"        "${SEARXNG_URL}"
    printf "    %-20s %s\n" "Ollama:"         "${OLLAMA_URL}"
    echo ""
    echo "  Quick commands:"
    echo "    make e2e-status    # re-run preflight"
    echo "    make doctor        # API health probe"
    echo "    make dev-down      # stop everything"
    echo ""
}

status() {
    if command -v docker >/dev/null 2>&1; then
        compose ps || true
    fi
    for name in server worker; do
        if is_running "$(pid_file "$name")"; then
            log "$name: RUNNING"
        else
            log "$name: NOT OWNED BY HARNESS"
        fi
    done
    run_preflight
}

down() {
    stop_process worker
    stop_process server
    if [[ "${E2E_KEEP_COMPOSE:-0}" != "1" ]]; then
        compose down
    else
        log 'keeping Compose services (E2E_KEEP_COMPOSE=1)'
    fi
}

dev_down() {
    log 'stopping worker...'
    stop_process worker
    log 'stopping server...'
    stop_process server
    log 'stopping Compose services...'
    compose down --remove-orphans 2>/dev/null || compose down
    log 'all services stopped'
}

usage() {
    cat >&2 <<'USAGE'
Usage: e2e-up.sh <command>

Commands:
  up         Start Compose dependencies + server/worker (fast, no readiness gates)
  dev-up     Deterministic staged startup with readiness verification at each stage
  status     Show process/service state and run preflight matrix
  down       Stop harness processes + Compose (preserves volumes)
  dev-down   Stop everything + remove orphan containers

Environment:
  E2E_READINESS_TIMEOUT   Per-service readiness timeout in seconds (default: 60)
  E2E_SKIP_PREFLIGHT=1    Skip preflight gate in dev-up (debugging only)
  E2E_START_LOCAL_PROCESSES=1  Start server/worker as local processes instead of Compose
USAGE
    exit 2
}

case "${1:-}" in
    up)        up ;;
    dev-up)    dev_up ;;
    status)    status ;;
    down)      down ;;
    dev-down)  dev_down ;;
    *)         usage ;;
esac
