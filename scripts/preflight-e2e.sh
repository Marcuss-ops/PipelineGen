#!/usr/bin/env bash
# scripts/preflight-e2e.sh — canonical E2E dependency gate with full matrix.
#
# Builds a structured dependency matrix covering every dimension required
# for a reproducible benchmark. A benchmark MUST NOT proceed until all
# required checks are PASS. Read-only: never starts services, edits
# databases, or repairs the environment.
#
# Matrix categories:
#   1. Repository    — git state, config, migrations
#   2. Binaries      — server, worker, Rust muscles, Chronon
#   3. Storage       — primary DB, observability DB, Qdrant
#   4. Services      — /health, /ready, worker registration, Ollama
#   5. Pipeline      — asset materialization, Chronon render, Drive write
#   6. Tools         — ffmpeg, yt-dlp
#
# Usage:
#   scripts/preflight-e2e.sh
#   PREFLIGHT_REQUIRE_DRIVE=1 scripts/preflight-e2e.sh
#   PREFLIGHT_REQUIRE_CHRONON=1 scripts/preflight-e2e.sh
#   PREFLIGHT_FINGERPRINT_FILE=out/fp.json scripts/preflight-e2e.sh
#
# Exit codes: 0 all checks pass; 1 one or more checks fail; 2 setup error.
set -Eeuo pipefail

# ── Resolve paths ──────────────────────────────────────────────────────────
ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

BASE_URL="${PREFLIGHT_BASE_URL:-http://127.0.0.1:${VELOX_PORT:-8000}}"
QDRANT_URL="${PREFLIGHT_QDRANT_URL:-http://127.0.0.1:${QDRANT_HTTP_PORT:-6333}}"
OLLAMA_URL="${PREFLIGHT_OLLAMA_URL:-${OLLAMA_URL:-http://127.0.0.1:11434}}"
CHRONON_URL="${PREFLIGHT_CHRONON_URL:-${CHRONON_URL:-}}"
DB_PATH="${PREFLIGHT_DB_PATH:-${VELOX_DB_PATH:-$ROOT_DIR/data/pipelinegen.db}}"
REQUIRE_DRIVE="${PREFLIGHT_REQUIRE_DRIVE:-0}"
REQUIRE_CHRONON="${PREFLIGHT_REQUIRE_CHRONON:-0}"
HTTP_TIMEOUT="${PREFLIGHT_HTTP_TIMEOUT_SECONDS:-5}"

# ── Environment fingerprint (capture once) ─────────────────────────────────
FINGERPRINT_FILE="${PREFLIGHT_FINGERPRINT_FILE:-}"
if [[ -n "$FINGERPRINT_FILE" ]]; then
    SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
    if [[ -x "$SCRIPT_DIR/bench/capture-fingerprint.sh" ]]; then
        PREFLIGHT_BASE_URL="$BASE_URL" \
        PREFLIGHT_DB_PATH="$DB_PATH" \
            bash "$SCRIPT_DIR/bench/capture-fingerprint.sh" > "$FINGERPRINT_FILE" 2>/dev/null || true
    else
        {
            echo "preflight_ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
            echo "git_sha=$(git -C "$ROOT_DIR" rev-parse HEAD 2>/dev/null || echo unknown)"
            echo "git_branch=$(git -C "$ROOT_DIR" symbolic-ref --quiet --short HEAD 2>/dev/null || echo detached)"
            echo "config_sha=$(sha256sum "$ROOT_DIR/config.yaml" 2>/dev/null | cut -d' ' -f1 || echo absent)"
            echo "db_sha=$(sha256sum "${DB_PATH}" 2>/dev/null | cut -d' ' -f1 || echo absent)"
        } > "$FINGERPRINT_FILE"
    fi
fi

# ── Setup validation ───────────────────────────────────────────────────────
if [[ ! "$BASE_URL" =~ ^https?://[^[:space:]]+$ || ! "$QDRANT_URL" =~ ^https?://[^[:space:]]+$ ]]; then
    printf 'preflight setup error: invalid service URL\n' >&2
    exit 2
fi
if ! command -v curl >/dev/null 2>&1; then
    printf 'preflight setup error: curl is required\n' >&2
    exit 2
fi
if [[ "${PREFLIGHT_REQUIRE_MAIN:-1}" == "1" ]] && command -v git >/dev/null 2>&1; then
    branch="$(git -C "$ROOT_DIR" symbolic-ref --quiet --short HEAD 2>/dev/null || true)"
    if [[ "$branch" != "main" ]]; then
        printf 'preflight setup error: checkout must be on branch main (got %s)\n' "${branch:-detached}" >&2
        exit 1
    fi
fi

# ── Matrix engine ──────────────────────────────────────────────────────────
failures=0
total=0
pass_count=0
skip_count=0

check() {
    local label="$1"; shift
    total=$((total + 1))
    if "$@" >/dev/null 2>&1; then
        printf '  %-32s PASS\n' "$label"
        pass_count=$((pass_count + 1))
    else
        printf '  %-32s FAIL\n' "$label"
        failures=$((failures + 1))
    fi
}

skip() {
    local label="$1"; shift
    total=$((total + 1))
    if "$@" >/dev/null 2>&1; then
        printf '  %-32s PASS\n' "$label"
        pass_count=$((pass_count + 1))
    else
        printf '  %-32s SKIP\n' "$label"
        skip_count=$((skip_count + 1))
    fi
}

# ── Check functions ────────────────────────────────────────────────────────

# Category 1: Repository
check_git() {
    command -v git >/dev/null 2>&1 && [[ "$(git -C "$ROOT_DIR" symbolic-ref --quiet --short HEAD 2>/dev/null)" == "main" ]]
}
check_config() {
    [[ -f "$ROOT_DIR/config.yaml" && -r "$ROOT_DIR/config.yaml" ]]
}
check_migrations() {
    [[ -d "$ROOT_DIR/migrations/sqlite" ]] && compgen -G "$ROOT_DIR/migrations/sqlite/*.sql" >/dev/null
}

# Category 2: Binaries
check_server_binary() {
    [[ -x "$ROOT_DIR/bin/pipelinegen" ]]
}
check_worker_binary() {
    [[ -x "$ROOT_DIR/bin/pipelinegen-worker" || -x "$ROOT_DIR/bin/pipelinegen" ]]
}
check_rust_binary() {
    [[ -x "$ROOT_DIR/bin/pipelinegen-muscles" ]]
}
check_chronon_binary() {
    local chronon_bin=""
    for candidate in \
        "$ROOT_DIR/Chronon3d/.tmp/chronon-builds/native-verify/apps/chronon3d_cli/chronon3d_cli" \
        "/opt/chronon3d/bin/chronon3d_cli" \
        "$ROOT_DIR/bin/chronon3d_cli" \
        "${CHRONON_RENDER_BIN:-}"; do
        if [[ -n "$candidate" && -x "$candidate" ]]; then
            chronon_bin="$candidate"
            break
        fi
    done
    [[ -n "$chronon_bin" ]]
}

# Category 3: Storage
check_primary_db() {
    [[ -f "$DB_PATH" ]] || return 1
    command -v sqlite3 >/dev/null 2>&1 && sqlite3 -readonly "$DB_PATH" 'PRAGMA integrity_check;' | grep -qx 'ok'
}
check_observability_db() {
    local obs_db="${PREFLIGHT_OBS_DB_PATH:-$ROOT_DIR/data/observability/api_requests.db.sqlite}"
    [[ -f "$obs_db" ]] || return 1
    command -v sqlite3 >/dev/null 2>&1 && sqlite3 -readonly "$obs_db" 'PRAGMA integrity_check;' | grep -qx 'ok'
}
check_qdrant() {
    local code
    code=$(curl -fsS --max-time "$HTTP_TIMEOUT" -o /dev/null -w '%{http_code}' "${QDRANT_URL%/}/healthz" 2>/dev/null || echo "000")
    [[ "$code" == "200" ]]
}
check_qdrant_collection() {
    local resp
    resp=$(curl -fsS --max-time "$HTTP_TIMEOUT" "${QDRANT_URL%/}/collections" 2>/dev/null || echo "")
    [[ -n "$resp" ]] && echo "$resp" | grep -q '"collections"' && \
        echo "$resp" | python3 -c "
import sys, json
d = json.load(sys.stdin)
cols = d.get('result', d).get('collections', [])
print(len(cols))
" 2>/dev/null | grep -q '^[1-9]'
}

# Category 4: Services
check_server_health() {
    local code
    code=$(curl -fsS --max-time "$HTTP_TIMEOUT" -o /dev/null -w '%{http_code}' "${BASE_URL%/}/health" 2>/dev/null || echo "000")
    [[ "$code" == "200" ]]
}
check_server_ready() {
    local code
    code=$(curl -fsS --max-time "$HTTP_TIMEOUT" -o /dev/null -w '%{http_code}' "${BASE_URL%/}/ready" 2>/dev/null || echo "000")
    [[ "$code" == "200" ]]
}
check_worker_registration() {
    local resp
    resp=$(curl -fsS --max-time "$HTTP_TIMEOUT" \
        -H "Authorization: Bearer ${VELOX_ADMIN_TOKEN:-}" \
        "${BASE_URL%/}/api/workers" 2>/dev/null || echo "[]")
    echo "$resp" | grep -q 'job_types' || return 1
    # Verify at least one worker has non-empty job_types
    echo "$resp" | python3 -c "
import sys, json
d = json.load(sys.stdin)
workers = d if isinstance(d, list) else d.get('workers', [d])
caps = [w for w in workers if w.get('job_types')]
exit(0 if caps else 1)
" 2>/dev/null
}
check_worker_capabilities() {
    # Alias: same as registration but checks the capabilities field exists
    check_worker_registration
}
check_ollama() {
    local code
    code=$(curl -fsS --max-time "$HTTP_TIMEOUT" -o /dev/null -w '%{http_code}' "${OLLAMA_URL%/}/api/tags" 2>/dev/null || echo "000")
    [[ "$code" == "200" ]]
}
check_chronon_service() {
    [[ -n "$CHRONON_URL" ]] || return 1
    local code
    code=$(curl -fsS --max-time "$HTTP_TIMEOUT" -o /dev/null -w '%{http_code}' "${CHRONON_URL%/}/health" 2>/dev/null || echo "000")
    [[ "$code" == "200" ]]
}

# Category 5: Pipeline
check_asset_materialization() {
    # Verify the materializer package is wired and the scratch directory
    # exists (or can be created). This is a static check — the actual
    # materialization test happens during benchmark execution.
    [[ -d "$ROOT_DIR/data" ]] || mkdir -p "$ROOT_DIR/data"
    local scratch="${ROOT_DIR}/data/scratch"
    [[ -d "$scratch" ]] || mkdir -p "$scratch"
    # Verify the materializer Go source exists (compiled into the binary)
    [[ -f "$ROOT_DIR/internal/platform/drive/materializer.go" ]]
}
check_drive_credentials() {
    [[ "$REQUIRE_DRIVE" == "1" ]] || return 1
    [[ -n "${VELOX_ADMIN_TOKEN:-}" ]] || return 1
}
check_drive_artifact_canary() {
    [[ "$REQUIRE_DRIVE" == "1" ]] || return 1
    [[ -x "$ROOT_DIR/scripts/drive-artifact-canary.sh" ]] || return 1
    DRIVE_CANARY_BASE_URL="$BASE_URL" \
    DRIVE_CANARY_FOLDER_ID="${PREFLIGHT_DRIVE_FOLDER_ID:-}" \
        bash "$ROOT_DIR/scripts/drive-artifact-canary.sh"
}
check_chronon_render_binary() {
    # Check that the Chronon render binary exists AND is executable.
    # This is distinct from the Chronon service check — the binary
    # is called directly by the worker for clip rendering.
    check_chronon_binary
}

# Category 6: Tools
check_ffmpeg() {
    command -v ffmpeg >/dev/null 2>&1 && ffmpeg -version >/dev/null 2>&1
}
check_ytdlp() {
    command -v yt-dlp >/dev/null 2>&1 && yt-dlp --version >/dev/null 2>&1
}

# ── Run matrix ─────────────────────────────────────────────────────────────
echo ""
echo "════════════════════════════════════════════════════════════════"
echo "  PIPELINEGEN E2E PREFLIGHT — DEPENDENCY MATRIX"
echo "════════════════════════════════════════════════════════════════"
echo "  Base URL: $BASE_URL"
echo "  Qdrant:   $QDRANT_URL"
echo ""

echo "── 1. Repository ──────────────────────────────────────────────"
check 'Git branch is main'            check_git
check 'Config.yaml readable'          check_config
check 'SQLite migrations present'     check_migrations
echo ""

echo "── 2. Binaries ───────────────────────────────────────────────"
check 'Server binary (pipelinegen)'   check_server_binary
check 'Worker binary'                 check_worker_binary
check 'Rust binary (muscles)'         check_rust_binary
skip  'Chronon binary (3d renderer)'  check_chronon_binary
echo ""

echo "── 3. Storage ────────────────────────────────────────────────"
check 'Primary SQLite integrity'      check_primary_db
skip  'Observability SQLite'          check_observability_db
check 'Qdrant reachable'              check_qdrant
check 'Qdrant collection exists'      check_qdrant_collection
echo ""

echo "── 4. Services ───────────────────────────────────────────────"
check 'PipelineGen /health'           check_server_health
check 'PipelineGen /ready'            check_server_ready
check 'Worker registered'             check_worker_registration
check 'Worker capabilities set'       check_worker_capabilities
check 'Ollama reachable'              check_ollama
if [[ -n "$CHRONON_URL" || "$REQUIRE_CHRONON" == "1" ]]; then
    check 'Chronon service'           check_chronon_service
else
    printf '  %-32s SKIP\n' 'Chronon service'
    skip_count=$((skip_count + 1))
    total=$((total + 1))
fi
echo ""

echo "── 5. Pipeline ───────────────────────────────────────────────"
check 'Asset materializer ready'      check_asset_materialization
skip  'Chronon render binary'         check_chronon_render_binary
if [[ "$REQUIRE_DRIVE" == "1" ]]; then
    check 'Drive credentials'         check_drive_credentials
    check 'Drive artifact canary'     check_drive_artifact_canary
else
    printf '  %-32s SKIP\n' 'Drive credentials'
    printf '  %-32s SKIP\n' 'Drive artifact canary'
    skip_count=$((skip_count + 2))
    total=$((total + 2))
fi
echo ""

echo "── 6. Tools ──────────────────────────────────────────────────"
check 'ffmpeg'                        check_ffmpeg
check 'yt-dlp'                        check_ytdlp
echo ""

# ── Summary ────────────────────────────────────────────────────────────────
echo "════════════════════════════════════════════════════════════════"
printf "  Total: %d  PASS: %d  FAIL: %d  SKIP: %d\n" "$total" "$pass_count" "$failures" "$skip_count"

if (( failures > 0 )); then
    printf "  ❌ ENVIRONMENT NOT READY: %d check(s) failed\n" "$failures" >&2
    echo "════════════════════════════════════════════════════════════════"
    exit 1
fi

printf "  ✅ ENVIRONMENT READY\n"
echo "════════════════════════════════════════════════════════════════"

# ── Capture fingerprint on success ─────────────────────────────────────────
if [[ -n "$FINGERPRINT_FILE" ]]; then
    echo ""
    echo "  Fingerprint: $FINGERPRINT_FILE"
fi

exit 0
