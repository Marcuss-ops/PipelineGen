#!/usr/bin/env bash
# backup_pipelinegen.sh — consistent backups for SQLite + Qdrant.
#
# Designed to run as a cron job (or systemd timer) while PipelineGen is alive.
# Both backends are snapshotted via API/CLI calls that produce consistent
# copies WITHOUT requiring PipelineGen to stop.
#
# Usage:
#   ./scripts/backup/backup_pipelinegen.sh                   # default paths from config.yaml
#   BACKUP_ROOT=/var/backups/pipelinegen ./scripts/backup/backup_pipelinegen.sh
#   QDRANT_URL=http://127.0.0.1:6333 ./scripts/backup/backup_pipelinegen.sh
#
# Cron (recommended):
#   0 2 * * *  /path/to/repo/scripts/backup/backup_pipelinegen.sh >> /var/log/pipelinegen-backup.log 2>&1
#
# Pre-requisites:
#   - sqlite3 CLI in PATH (apt: sqlite3)
#   - jq CLI in PATH (apt: jq)
#   - curl in PATH
#   - Qdrant reachable at $QDRANT_URL
#   - Read access to the SQLite database file

set -Eeuo pipefail

# ── Configuration (override via env vars) ───────────────────────────────
: "${BACKUP_ROOT:=/var/backups/pipelinegen}"
: "${PROJECT_ROOT:=/home/pierone/Pyt/Pipelinegen}"
: "${QDRANT_URL:=http://127.0.0.1:6333}"
: "${QDRANT_COLLECTION:=media_assets}"
: "${RETENTION_DAYS:=30}"

# Resolve SQLite path (from config.yaml: storage.data_dir + media.db.sqlite)
DATA_DIR="${PROJECT_ROOT}/data"
if [[ -f "${PROJECT_ROOT}/config.yaml" ]]; then
    # Grep data_dir from config.yaml; default = ./data
    RESOLVED_DIR=$(grep -E '^\s*data_dir:' "${PROJECT_ROOT}/config.yaml" | head -1 | awk -F': ' '{print $2}' | tr -d '"\n ' || true)
    if [[ -n "${RESOLVED_DIR:-}" ]]; then
        if [[ "${RESOLVED_DIR}" = ./* ]] || [[ "${RESOLVED_DIR}" = /* ]]; then
            DATA_DIR="${PROJECT_ROOT}/${RESOLVED_DIR#./}"
        elif [[ "${RESOLVED_DIR}" != /* ]]; then
            DATA_DIR="${PROJECT_ROOT}/${RESOLVED_DIR}"
        else
            DATA_DIR="${RESOLVED_DIR}"
        fi
    fi
fi
SQLITE_PATH="${DATA_DIR}/media.db.sqlite"

# ── Pre-flight checks ──────────────────────────────────────────────────
for cmd in sqlite3 jq curl; do
    command -v "${cmd}" >/dev/null 2>&1 || {
        echo "[FATAL] required command not found: ${cmd}" >&2
        exit 1
    }
done

if [[ ! -f "${SQLITE_PATH}" ]]; then
    echo "[FATAL] SQLite database not found at ${SQLITE_PATH}" >&2
    echo "        Set SQLITE_PATH or update DATA_DIR resolution." >&2
    exit 1
fi

# ── Setup timestamp + dirs ─────────────────────────────────────────────
TIMESTAMP=$(date -u +'%Y-%m-%dT%H%M%SZ')
BACKUP_DIR="${BACKUP_ROOT}/${TIMESTAMP}"
mkdir -p "${BACKUP_DIR}"
echo "[INFO] backup target: ${BACKUP_DIR}"

# ── 1. SQLite: VACUUM INTO (consistent copy while PipelineGen writes) ─
#
# VACUUM INTO is atomic and produces a defragmented, fully consistent copy
# even while the source database is being written to. WAL mode ensures the
# source connection sees only committed pages.
#
# Reference: https://www.sqlite.org/lang_vacuum.html
SQLITE_BACKUP="${BACKUP_DIR}/media.db.sqlite"
echo "[INFO] backing up SQLite via VACUUM INTO (${SQLITE_PATH})"

if ! sqlite3 "${SQLITE_PATH}" "VACUUM INTO '${SQLITE_BACKUP}'"; then
    echo "[ERROR] SQLite VACUUM INTO failed" >&2
    rm -rf "${BACKUP_DIR}"
    exit 1
fi

# Verify backup integrity
echo "[INFO] verifying SQLite backup integrity"
INTEGRITY=$(sqlite3 "${SQLITE_BACKUP}" "PRAGMA integrity_check;")
if [[ "${INTEGRITY}" != "ok" ]]; then
    echo "[ERROR] SQLite backup integrity check failed: ${INTEGRITY}" >&2
    rm -rf "${BACKUP_DIR}"
    exit 1
fi

# Compact WAL on backup (zero out -wal/-shm side files)
sqlite3 "${SQLITE_BACKUP}" "PRAGMA wal_checkpoint(TRUNCATE);" || true

# ── 2. Qdrant: create snapshot via REST API ────────────────────────────
#
# Qdrant snapshots include collection config + all points + payloads.
# Aliases (e.g. pipelinegen_clips_current) must be exported separately
# since they're a separate API resource.
#
# Reference: https://qdrant.tech/documentation/concepts/snapshots/
QDRANT_BACKUP="${BACKUP_DIR}/qdrant-snapshot.json.tmp"
echo "[INFO] requesting Qdrant snapshot for collection ${QDRANT_COLLECTION}"

SNAPSHOT_RESPONSE=$(curl --silent --fail-with-body \
    -X POST "${QDRANT_URL}/collections/${QDRANT_COLLECTION}/snapshots" \
    -H "Content-Type: application/json" \
    --data '{}' \
    --max-time 600) || {
        echo "[WARN] Qdrant snapshot creation failed (collection may not exist yet)" >&2
        SNAPSHOT_RESPONSE=""
    }

if [[ -n "${SNAPSHOT_RESPONSE}" ]]; then
    SNAPSHOT_NAME=$(echo "${SNAPSHOT_RESPONSE}" | jq -r '.result.name // empty')
    if [[ -n "${SNAPSHOT_NAME}" ]]; then
        echo "[INFO] Qdrant snapshot created: ${SNAPSHOT_NAME}"
        # Download snapshot to local backup dir
        SNAPSHOT_FILE="${BACKUP_DIR}/qdrant-${SNAPSHOT_NAME}.snapshot"
        if curl --silent --fail \
            -o "${SNAPSHOT_FILE}" \
            "${QDRANT_URL}/collections/${QDRANT_COLLECTION}/snapshots/${SNAPSHOT_NAME}" \
            --max-time 1200; then
            echo "[INFO] Qdrant snapshot downloaded: ${SNAPSHOT_FILE}"
            # Remove remote snapshot after successful download (cleanup)
            curl --silent --fail-with-body \
                -X DELETE "${QDRANT_URL}/collections/${QDRANT_COLLECTION}/snapshots/${SNAPSHOT_NAME}" \
                --max-time 60 || true
        else
            echo "[WARN] Qdrant snapshot download failed" >&2
        fi
    fi
fi

# ── 3. Export aliases via /aliases/collection endpoint (for safe migration) ─
#
# If aliases exist, save them so a migration can recreate them.
echo "[INFO] exporting Qdrant collection aliases"
ALIASES_RESPONSE=$(curl --silent --fail-with-body \
    "${QDRANT_URL}/collections/${QDRANT_COLLECTION}/aliases" \
    --max-time 30) || {
        echo "[WARN] alias export failed" >&2
        ALIASES_RESPONSE=""
    }
if [[ -n "${ALIASES_RESPONSE}" ]]; then
    echo "${ALIASES_RESPONSE}" | jq '.' > "${BACKUP_DIR}/qdrant-aliases.json" || true
fi

# ── 4. Write backup manifest ──────────────────────────────────────────
cat > "${BACKUP_DIR}/manifest.json" <<EOF
{
  "timestamp": "${TIMESTAMP}",
  "pipelinegen_version": "$(git -C "${PROJECT_ROOT}" describe --tags --always 2>/dev/null || echo unknown)",
  "sqlite_backup": "media.db.sqlite",
  "sqlite_size_bytes": $(stat -c %s "${SQLITE_BACKUP}" 2>/dev/null || echo 0),
  "qdrant_url": "${QDRANT_URL}",
  "qdrant_collection": "${QDRANT_COLLECTION}",
  "components": [
    "media.db.sqlite",
    "qdrant-*.snapshot",
    "qdrant-aliases.json"
  ]
}
EOF

# ── 5. Retention: remove backups older than RETENTION_DAYS ─────────────
echo "[INFO] pruning backups older than ${RETENTION_DAYS} days"
find "${BACKUP_ROOT}" -maxdepth 1 -mindepth 1 -type d -mtime "+${RETENTION_DAYS}" -exec rm -rf {} \; 2>/dev/null || true

BACKUP_SIZE=$(du -sh "${BACKUP_DIR}" | cut -f1)
echo "[OK] backup completed in ${BACKUP_DIR} (size: ${BACKUP_SIZE})"
