#!/usr/bin/env bash
# scripts/bench/capture-fingerprint.sh — canonical environment fingerprint.
#
# Captures the exact state of every reproducibility-relevant dimension
# at benchmark start. The output is a structured JSON document that
# MUST be recorded alongside every benchmark report.
#
# Dimensions captured:
#   1. git_sha          — full commit SHA
#   2. git_branch       — current branch name
#   3. config_sha       — SHA-256 of config.yaml
#   4. db_sha           — SHA-2-256 of primary SQLite DB
#   5. worker_ids       — registered worker node IDs
#   6. worker_version   — VELOX_WORKER_VERSION of registered workers
#   7. chronon_version  — SHA-256 of chronon3d_cli binary
#   8. qdrant_collection — active Qdrant collection name + point count + status
#   9. ollama_models    — available Ollama model tags
#  10. timestamp        — ISO-8601 UTC capture time
#
# Usage:
#   scripts/bench/capture-fingerprint.sh                     # stdout JSON
#   scripts/bench/capture-fingerprint.sh > fingerprint.json  # file
#
# Exit codes: 0 success; 1 partial (some dimensions unavailable).
set -Eeuo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"

# ── Load environment ───────────────────────────────────────────────────────
# shellcheck source=scripts/lib/dotenv.sh
source "$ROOT_DIR/scripts/lib/dotenv.sh"
# shellcheck source=scripts/lib/canonical_db_path.sh
source "$ROOT_DIR/scripts/lib/canonical_db_path.sh"
load_dotenv_missing "$ROOT_DIR/.env"

BASE_URL="${FINGERPRINT_BASE_URL:-http://127.0.0.1:${VELOX_PORT:-8000}}"
QDRANT_URL="${FINGERPRINT_QDRANT_URL:-http://127.0.0.1:${QDRANT_HTTP_PORT:-6333}}"
OLLAMA_URL="${FINGERPRINT_OLLAMA_URL:-${OLLAMA_URL:-http://127.0.0.1:11434}}"
ADMIN_TOKEN="${VELOX_ADMIN_TOKEN:-}"
DB_PATH="${FINGERPRINT_DB_PATH:-${VELOX_DB_PATH:-$(canonical_primary_db_path "$ROOT_DIR")}}"
DB_PATH="$(validate_canonical_primary_db_path "$DB_PATH")" || exit 2

HAS_PARTIAL=0

# ── Helpers ────────────────────────────────────────────────────────────────
json_escape() {
    python3 -c "import json,sys; print(json.dumps(sys.stdin.read().strip()))" <<< "$1"
}

http_get() {
    local url="$1" timeout="${2:-5}"
    curl -fsS --max-time "$timeout" \
        -H "Authorization: Bearer $ADMIN_TOKEN" \
        "$url" 2>/dev/null || echo ""
}

# ── 1. Git state ───────────────────────────────────────────────────────────
GIT_SHA=$(git -C "$ROOT_DIR" rev-parse HEAD 2>/dev/null || echo "unknown")
GIT_BRANCH=$(git -C "$ROOT_DIR" symbolic-ref --quiet --short HEAD 2>/dev/null || echo "detached")
GIT_DIRTY=$(cd "$ROOT_DIR" && git diff --quiet HEAD 2>/dev/null && echo "false" || echo "true")

# ── 2. Config hash ─────────────────────────────────────────────────────────
CONFIG_SHA=$(sha256sum "$ROOT_DIR/config.yaml" 2>/dev/null | cut -d' ' -f1 || echo "absent")

# ── 3. Primary DB hash ─────────────────────────────────────────────────────
DB_SHA=$(sha256sum "$DB_PATH" 2>/dev/null | cut -d' ' -f1 || echo "absent")
DB_SIZE=$(stat -c%s "$DB_PATH" 2>/dev/null || stat -f%z "$DB_PATH" 2>/dev/null || echo "0")

# ── 4. Observability DB hash ───────────────────────────────────────────────
OBS_DB_PATH="${FINGERPRINT_OBS_DB_PATH:-$ROOT_DIR/data/observability/api_requests.db.sqlite}"
OBS_DB_SHA=$(sha256sum "$OBS_DB_PATH" 2>/dev/null | cut -d' ' -f1 || echo "absent")

# ── 5. Worker identity + version ───────────────────────────────────────────
WORKER_JSON="[]"
WORKER_IDS="[]"
WORKER_VERSIONS="[]"
if [[ -n "$ADMIN_TOKEN" ]]; then
    WORKER_RAW=$(http_get "$BASE_URL/api/workers" 10)
    if [[ -n "$WORKER_RAW" ]]; then
        WORKER_JSON="$WORKER_RAW"
        WORKER_IDS=$(echo "$WORKER_RAW" | python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
    if isinstance(data, list):
        ids = [w.get('id', w.get('worker_id', '')) for w in data if w.get('id') or w.get('worker_id')]
    elif isinstance(data, dict):
        workers = data.get('workers', [data])
        ids = [w.get('id', w.get('worker_id', '')) for w in workers if w.get('id') or w.get('worker_id')]
    else:
        ids = []
    print(json.dumps(ids))
except Exception:
    print('[]')
" 2>/dev/null || echo "[]")
        WORKER_VERSIONS=$(echo "$WORKER_RAW" | python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
    if isinstance(data, list):
        vers = [w.get('worker_version', w.get('version', '')) for w in data]
    elif isinstance(data, dict):
        workers = data.get('workers', [data])
        vers = [w.get('worker_version', w.get('version', '')) for w in workers]
    else:
        vers = []
    print(json.dumps([v for v in vers if v]))
except Exception:
    print('[]')
" 2>/dev/null || echo "[]")
    fi
fi

# ── 6. Chronon binary version ─────────────────────────────────────────────
CHRONON_BIN=""
for candidate in \
    "$ROOT_DIR/Chronon3d/.tmp/chronon-builds/native-verify/apps/chronon3d_cli/chronon3d_cli" \
    "/opt/chronon3d/bin/chronon3d_cli" \
    "$ROOT_DIR/bin/chronon3d_cli" \
    "${CHRONON_RENDER_BIN:-}"; do
    if [[ -n "$candidate" && -x "$candidate" ]]; then
        CHRONON_BIN="$candidate"
        break
    fi
done

CHRONON_SHA="absent"
CHRONON_VERSION="absent"
if [[ -n "$CHRONON_BIN" ]]; then
    CHRONON_SHA=$(sha256sum "$CHRONON_BIN" 2>/dev/null | cut -d' ' -f1 || echo "absent")
    CHRONON_VERSION=$("$CHRONON_BIN" --version 2>/dev/null || echo "$CHRONON_SHA")
fi

# ── 7. Qdrant collection state ────────────────────────────────────────────
QDRANT_COLLECTIONS="[]"
QDRANT_ACTIVE_COLLECTION="absent"
QDRANT_COLLECTION_STATUS="absent"
QDRANT_POINT_COUNT=0

QDRANT_RAW=$(curl -fsS --max-time 5 "$QDRANT_URL/collections" 2>/dev/null || echo "")
if [[ -n "$QDRANT_RAW" ]]; then
    QDRANT_COLLECTIONS=$(echo "$QDRANT_RAW" | python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
    result = data.get('result', data)
    collections = result.get('collections', [])
    names = [c.get('name', '') for c in collections if c.get('name')]
    print(json.dumps(names))
except Exception:
    print('[]')
" 2>/dev/null || echo "[]")

    # Get the active collection details (first collection with points)
    QDRANT_ACTIVE_COLLECTION=$(echo "$QDRANT_RAW" | python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
    result = data.get('result', data)
    collections = result.get('collections', [])
    for c in collections:
        name = c.get('name', '')
        if name:
            print(name)
            break
    else:
        print('absent')
except Exception:
    print('absent')
" 2>/dev/null || echo "absent")

    # Get detailed info for the first collection
    if [[ "$QDRANT_ACTIVE_COLLECTION" != "absent" ]]; then
        COLL_INFO=$(curl -fsS --max-time 5 "$QDRANT_URL/collections/$QDRANT_ACTIVE_COLLECTION" 2>/dev/null || echo "")
        if [[ -n "$COLL_INFO" ]]; then
            QDRANT_COLLECTION_STATUS=$(echo "$COLL_INFO" | python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
    result = data.get('result', data)
    print(result.get('status', 'unknown'))
except Exception:
    print('unknown')
" 2>/dev/null || echo "unknown")
            QDRANT_POINT_COUNT=$(echo "$COLL_INFO" | python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
    result = data.get('result', data)
    print(result.get('points_count', result.get('vectors_count', 0)))
except Exception:
    print(0)
" 2>/dev/null || echo "0")
        fi
    fi
fi

# ── 8. Ollama models ──────────────────────────────────────────────────────
OLLAMA_MODELS="[]"
OLLAMA_RAW=$(curl -fsS --max-time 5 "$OLLAMA_URL/api/tags" 2>/dev/null || echo "")
if [[ -n "$OLLAMA_RAW" ]]; then
    OLLAMA_MODELS=$(echo "$OLLAMA_RAW" | python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
    models = data.get('models', [])
    print(json.dumps([m.get('name', '') for m in models if m.get('name')]))
except Exception:
    print('[]')
" 2>/dev/null || echo "[]")
fi

# ── 9. Timestamp ───────────────────────────────────────────────────────────
TIMESTAMP=$(date -u +%Y-%m-%dT%H:%M:%SZ)

# ── 10. Migrations count ──────────────────────────────────────────────────
MIGRATIONS_COUNT=0
if [[ -d "$ROOT_DIR/migrations/sqlite" ]]; then
    MIGRATIONS_COUNT=$(ls -1 "$ROOT_DIR/migrations/sqlite/"*.sql 2>/dev/null | wc -l || echo "0")
fi

# ── Emit JSON ──────────────────────────────────────────────────────────────
python3 -c "
import json, sys

report = {
    'schema_version': 'pipelinegen-fingerprint-v1',
    'timestamp': sys.argv[1],
    'git': {
        'sha': sys.argv[2],
        'branch': sys.argv[3],
        'dirty': sys.argv[4] == 'true',
    },
    'config_sha': sys.argv[5],
    'database': {
        'primary_sha': sys.argv[6],
        'primary_size_bytes': int(sys.argv[7]),
        'observability_sha': sys.argv[8],
        'migrations_count': int(sys.argv[9]),
    },
    'workers': {
        'ids': json.loads(sys.argv[10]),
        'versions': json.loads(sys.argv[11]),
    },
    'chronon': {
        'binary_path': sys.argv[12],
        'sha256': sys.argv[13],
        'version': sys.argv[14],
    },
    'qdrant': {
        'url': sys.argv[15],
        'collections': json.loads(sys.argv[16]),
        'active_collection': sys.argv[17],
        'status': sys.argv[18],
        'point_count': int(sys.argv[19]),
    },
    'ollama': {
        'url': sys.argv[20],
        'models': json.loads(sys.argv[21]),
    },
}

json.dump(report, sys.stdout, indent=2)
print()  # trailing newline
" \
    "$TIMESTAMP" \
    "$GIT_SHA" \
    "$GIT_BRANCH" \
    "$GIT_DIRTY" \
    "$CONFIG_SHA" \
    "$DB_SHA" \
    "$DB_SIZE" \
    "$OBS_DB_SHA" \
    "$MIGRATIONS_COUNT" \
    "$WORKER_IDS" \
    "$WORKER_VERSIONS" \
    "$CHRONON_BIN" \
    "$CHRONON_SHA" \
    "$CHRONON_VERSION" \
    "$QDRANT_URL" \
    "$QDRANT_COLLECTIONS" \
    "$QDRANT_ACTIVE_COLLECTION" \
    "$QDRANT_COLLECTION_STATUS" \
    "$QDRANT_POINT_COUNT" \
    "$OLLAMA_URL" \
    "$OLLAMA_MODELS"
