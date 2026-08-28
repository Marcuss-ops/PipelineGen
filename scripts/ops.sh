#!/usr/bin/env bash
# scripts/ops.sh — canonical PipelineGen operations CLI.
#
# Eliminates ad-hoc shell commands by providing a single deterministic
# entry point for every common operation. Agents and operators should
# NEVER run raw `source .env`, `export VELOX_*`, `./server`, `./worker`,
# `curl .../api/...`, or `sqlite3 pipelinegen.db` directly.
#
# Usage:
#   velox <command> [args...]
#
# Commands:
#   up              Start full stack (deterministic staged startup)
#   down            Stop all services
#   status          Show service status + preflight matrix
#   doctor          Comprehensive health report
#
#   api <METHOD> <PATH> [<BODY>]   Authenticated HTTP request
#   query <SQL>                    Query primary SQLite database
#   query-obs <SQL>                Query observability SQLite database
#
#   env                           Show resolved environment (no secrets)
#   fingerprint                   Capture full environment fingerprint
#   log <service> [--tail N]      Tail service logs
#
#   build                         Build server + worker binaries
#   test [package]                Run tests (default: changed components)
#
# Environment:
#   VELOX_PORT            Server port (default: 8000)
#   VELOX_ADMIN_TOKEN     Admin token (auto-loaded from canonical env file)
#   TOKEN_FILE            Token file path (default: /etc/pipelinegen/pipelinegen.env)
#
# Exit codes: 0 success; 1 runtime error; 2 setup/configuration error.
set -Eeuo pipefail

# ── Resolve root directory ─────────────────────────────────────────────────
SCRIPTS_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd -- "$SCRIPTS_DIR/.." && pwd)"

# ── Load environment (deterministic, no clobber) ──────────────────────────
# shellcheck source=scripts/lib/dotenv.sh
source "$SCRIPTS_DIR/lib/dotenv.sh"
load_dotenv_missing "$ROOT_DIR/.env"

# Canonical token file (production). Only used when VELOX_ADMIN_TOKEN
# is not already set by the caller's environment.
TOKEN_FILE="${TOKEN_FILE:-/etc/pipelinegen/pipelinegen.env}"
if [[ -z "${VELOX_ADMIN_TOKEN:-}" && -r "$TOKEN_FILE" ]]; then
    VELOX_ADMIN_TOKEN="$(sed -n 's/^VELOX_ADMIN_TOKEN=//p' "$TOKEN_FILE" | tail -n 1)"
    export VELOX_ADMIN_TOKEN
fi

# ── Resolved defaults ──────────────────────────────────────────────────────
PORT="${VELOX_PORT:-8000}"
BASE_URL="http://127.0.0.1:${PORT}"
DB_PATH="${VELOX_DB_PATH:-$ROOT_DIR/data/pipelinegen.db}"
OBS_DB_PATH="${VELOX_OBSERVABILITY_DB_PATH:-$ROOT_DIR/data/observability/api_requests.db.sqlite}"
QDRANT_URL="${VELOX_QDRANT_URL:-http://127.0.0.1:${QDRANT_HTTP_PORT:-6333}}"
OLLAMA_URL="${OLLAMA_URL:-http://127.0.0.1:11434}"

# ── Helpers ────────────────────────────────────────────────────────────────
log()  { printf '[velox] %s\n' "$*"; }
warn() { printf '[velox] WARN: %s\n' "$*" >&2; }
fail() { printf '[velox] ERROR: %s\n' "$*" >&2; exit 1; }

require_server() {
    local code
    code=$(curl -fsS --max-time 3 -o /dev/null -w '%{http_code}' "$BASE_URL/health" 2>/dev/null || echo "000")
    [[ "$code" == "200" ]] || fail "PipelineGen server not reachable at $BASE_URL (HTTP $code). Run 'velox up' first."
}

require_db() {
    [[ -f "$DB_PATH" ]] || fail "Primary database not found: $DB_PATH"
    command -v sqlite3 >/dev/null 2>&1 || fail "sqlite3 not on PATH"
}

require_obs_db() {
    [[ -f "$OBS_DB_PATH" ]] || fail "Observability database not found: $OBS_DB_PATH"
    command -v sqlite3 >/dev/null 2>&1 || fail "sqlite3 not on PATH"
}

# ── Commands ───────────────────────────────────────────────────────────────

cmd_up() {
    exec bash "$SCRIPTS_DIR/dev/e2e-up.sh" dev-up "$@"
}

cmd_down() {
    exec bash "$SCRIPTS_DIR/dev/e2e-up.sh" dev-down "$@"
}

cmd_status() {
    exec bash "$SCRIPTS_DIR/dev/e2e-up.sh" status "$@"
}

cmd_doctor() {
    log "PipelineGen Doctor"
    echo ""

    # Git state
    local git_sha git_branch
    git_sha=$(git -C "$ROOT_DIR" rev-parse HEAD 2>/dev/null || echo "unknown")
    git_branch=$(git -C "$ROOT_DIR" symbolic-ref --quiet --short HEAD 2>/dev/null || echo "detached")
    printf "  %-24s %s (%s)\n" "Git:" "${git_sha:0:8}" "$git_branch"

    # Config
    if [[ -f "$ROOT_DIR/config.yaml" ]]; then
        printf "  %-24s %s\n" "Config:" "readable"
    else
        printf "  %-24s %s\n" "Config:" "MISSING"
    fi

    # Binaries
    if [[ -x "$ROOT_DIR/bin/pipelinegen" ]]; then
        printf "  %-24s %s\n" "Server binary:" "built"
    else
        printf "  %-24s %s\n" "Server binary:" "MISSING (run make build)"
    fi
    if [[ -x "$ROOT_DIR/bin/pipelinegen-worker" ]]; then
        printf "  %-24s %s\n" "Worker binary:" "built"
    else
        printf "  %-24s %s\n" "Worker binary:" "MISSING (run make build)"
    fi

    # Database
    if [[ -f "$DB_PATH" ]]; then
        local db_size
        db_size=$(stat -c%s "$DB_PATH" 2>/dev/null || stat -f%z "$DB_PATH" 2>/dev/null || echo "?")
        printf "  %-24s %s (%s bytes)\n" "Primary DB:" "$DB_PATH" "$db_size"
    else
        printf "  %-24s %s\n" "Primary DB:" "NOT FOUND"
    fi

    # HTTP probes
    echo ""
    log "HTTP probes:"
    local services=(
        "Server /health|$BASE_URL/health|200"
        "Server /ready|$BASE_URL/ready|200"
        "Qdrant|$QDRANT_URL/healthz|200"
        "Ollama|$OLLAMA_URL/api/tags|200"
    )
    for entry in "${services[@]}"; do
        IFS='|' read -r label url expected <<< "$entry"
        local code
        code=$(curl -fsS --max-time 3 -o /dev/null -w '%{http_code}' "$url" 2>/dev/null || echo "000")
        if [[ "$code" == "$expected" ]]; then
            printf "  %-24s %s\n" "$label:" "PASS ($code)"
        else
            printf "  %-24s %s\n" "$label:" "FAIL ($code)"
        fi
    done

    # Workers
    if [[ -n "${VELOX_ADMIN_TOKEN:-}" ]]; then
        echo ""
        log "Workers:"
        local worker_resp
        worker_resp=$(curl -fsS --max-time 5 \
            -H "Authorization: Bearer $VELOX_ADMIN_TOKEN" \
            "$BASE_URL/api/workers" 2>/dev/null || echo "[]")
        local worker_count
        worker_count=$(echo "$worker_resp" | python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
    if isinstance(data, list): print(len(data))
    elif isinstance(data, dict): print(len(data.get('workers', [])))
    else: print(0)
except: print(0)
" 2>/dev/null || echo "0")
        printf "  %-24s %s\n" "Registered workers:" "$worker_count"
    fi

    # Migrations
    echo ""
    log "Migrations:"
    if [[ -d "$ROOT_DIR/migrations/sqlite" ]]; then
        local mig_count
        mig_count=$(ls -1 "$ROOT_DIR/migrations/sqlite/"*.sql 2>/dev/null | wc -l || echo "0")
        printf "  %-24s %s\n" "SQLite migrations:" "$mig_count files"
    fi

    echo ""
    log "Doctor complete."
}

cmd_api() {
    local method="${1:-}"
    local path="${2:-}"
    local body="${3:-}"

    [[ -n "$method" ]] || fail "Usage: velox api <METHOD> <PATH> [<BODY>]"
    [[ -n "$path" ]]   || fail "Usage: velox api <METHOD> <PATH> [<BODY>]"
    [[ -n "${VELOX_ADMIN_TOKEN:-}" ]] || fail "VELOX_ADMIN_TOKEN not set. Run 'velox doctor' or set TOKEN_FILE."

    local -a curl_args=(
        -X "$method"
        -H "Authorization: Bearer $VELOX_ADMIN_TOKEN"
        -H "Content-Type: application/json"
    )
    [[ -n "$body" ]] && curl_args+=(-d "$body")

    curl -fsS --max-time 30 "${curl_args[@]}" "${BASE_URL}${path}"
}

cmd_query() {
    local sql="${1:-}"
    [[ -n "$sql" ]] || fail "Usage: velox query <SQL>"
    require_db

    sqlite3 -header -column "$DB_PATH" "$sql"
}

cmd_query_obs() {
    local sql="${1:-}"
    [[ -n "$sql" ]] || fail "Usage: velox query-obs <SQL>"
    require_obs_db

    sqlite3 -header -column "$OBS_DB_PATH" "$sql"
}

cmd_env() {
    log "Resolved environment (secrets redacted):"
    echo ""
    printf "  %-30s %s\n" "VELOX_PORT:" "$PORT"
    printf "  %-30s %s\n" "BASE_URL:" "$BASE_URL"
    printf "  %-30s %s\n" "DB_PATH:" "$DB_PATH"
    printf "  %-30s %s\n" "OBS_DB_PATH:" "$OBS_DB_PATH"
    printf "  %-30s %s\n" "QDRANT_URL:" "$QDRANT_URL"
    printf "  %-30s %s\n" "OLLAMA_URL:" "$OLLAMA_URL"
    if [[ -n "${VELOX_ADMIN_TOKEN:-}" ]]; then
        printf "  %-30s SET (%d chars)\n" "VELOX_ADMIN_TOKEN:" "${#VELOX_ADMIN_TOKEN}"
    else
        printf "  %-30s NOT SET\n" "VELOX_ADMIN_TOKEN:"
    fi
    printf "  %-30s %s\n" "ROOT_DIR:" "$ROOT_DIR"
    printf "  %-30s %s\n" "TOKEN_FILE:" "$TOKEN_FILE"
}

cmd_fingerprint() {
    exec bash "$SCRIPTS_DIR/bench/capture-fingerprint.sh" "$@"
}

cmd_log() {
    local service="${1:-}"
    local tail_lines="${2:-50}"

    [[ -n "$service" ]] || fail "Usage: velox log <server|worker> [--tail N]"

    local log_dir="$ROOT_DIR/.tmp/e2e-harness/logs"
    local log_file="$log_dir/${service}.log"

    if [[ -f "$log_file" ]]; then
        tail -n "$tail_lines" "$log_file"
    else
        warn "No log file found for '$service' at $log_file"
        warn "Services started via Docker Compose log to: docker compose logs $service"
    fi
}

cmd_build() {
    log "Building server and worker..."
    cd "$ROOT_DIR"
    make build
}

cmd_test() {
    local package="${1:-./...}"
    cd "$ROOT_DIR"
    log "Running tests for: $package"
    go test "$package" -count=1
}

# ── Usage ──────────────────────────────────────────────────────────────────
usage() {
    cat >&2 <<'USAGE'
velox — canonical PipelineGen operations CLI.

USAGE:
  velox <command> [args...]

LIFECYCLE:
  up                Start full stack (deterministic staged startup)
  down              Stop all services + remove orphans
  status            Show service status + preflight matrix
  doctor            Comprehensive health report

API:
  api <METHOD> <PATH> [<BODY>]   Authenticated HTTP request
                                  Example: velox api GET /api/workers

DATABASE:
  query <SQL>                     Query primary SQLite database
                                  Example: velox query "SELECT COUNT(*) FROM media_assets"
  query-obs <SQL>                 Query observability database

ENVIRONMENT:
  env                             Show resolved environment (secrets redacted)
  fingerprint                     Capture full environment fingerprint JSON
  log <service> [--tail N]        Tail harness log file

BUILD:
  build                           Build server + worker binaries
  test [package]                  Run tests (default: ./...)

EXAMPLES:
  velox up                        # Start everything
  velox doctor                    # Check health
  velox api GET /health           # Probe server
  velox api POST /api/script/generate '{"version":2,...}'
  velox query "SELECT id, name FROM media_assets LIMIT 5"
  velox env                       # Show resolved config
  velox down                      # Stop everything
USAGE
    exit 2
}

# ── Dispatch ───────────────────────────────────────────────────────────────
case "${1:-}" in
    up)         shift; cmd_up "$@" ;;
    down)       shift; cmd_down "$@" ;;
    status)     shift; cmd_status "$@" ;;
    doctor)     shift; cmd_doctor "$@" ;;
    api)        shift; cmd_api "$@" ;;
    query)      shift; cmd_query "$@" ;;
    query-obs)  shift; cmd_query_obs "$@" ;;
    env)        shift; cmd_env "$@" ;;
    fingerprint) shift; cmd_fingerprint "$@" ;;
    log)        shift; cmd_log "$@" ;;
    build)      shift; cmd_build "$@" ;;
    test)       shift; cmd_test "$@" ;;
    --help|-h)  usage ;;
    *)          usage ;;
esac
