#!/usr/bin/env bash
# inventory.sh — read-only inventory of the canonical media database
# (PostgreSQL + pgvector, `pipelinegen_media`) that backs every job manifest.
#
# It answers "what material do we actually have to send in a pre-job?" without
# going through the API, by querying the media SSOT directly.
#
# Usage:
#   ops/jobs/remote/inventory.sh
#   ops/jobs/remote/inventory.sh --source stock
#   ops/jobs/remote/inventory.sh --source youtube --limit 10 --with-drive
#
# The DSN is read from the repository .env (PIPELINEGEN_MEDIA_POSTGRES_DSN) and
# rewritten to the in-container port; it is never echoed.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
ENV_FILE="${VELOX_ENV_FILE:-$REPO_ROOT/.env}"
PG_CONTAINER="${VELOX_PG_CONTAINER:-pipelinegen-postgres-test}"

SOURCE=""
LIMIT="25"
WITH_DRIVE="0"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --source) SOURCE="${2:?--source needs a value}"; shift 2 ;;
    --limit) LIMIT="${2:?--limit needs a value}"; shift 2 ;;
    --with-drive) WITH_DRIVE="1"; shift ;;
    -h|--help) sed -n '2,14p' "${BASH_SOURCE[0]}"; exit 0 ;;
    *) echo "inventory: unknown argument $1" >&2; exit 1 ;;
  esac
done

if [[ ! -f "$ENV_FILE" ]]; then
  echo "inventory: FAIL — $ENV_FILE not found" >&2
  exit 1
fi
DSN_RAW="$(rg -N '^PIPELINEGEN_MEDIA_POSTGRES_DSN=' "$ENV_FILE" | cut -d= -f2- || true)"
if [[ -z "$DSN_RAW" ]]; then
  echo "inventory: FAIL — PIPELINEGEN_MEDIA_POSTGRES_DSN not set in $ENV_FILE" >&2
  exit 1
fi
DSN_IN="$(printf '%s' "$DSN_RAW" | sed -E 's#@[^/]+/#@127.0.0.1:5432/#')"

psql_q() { docker exec "$PG_CONTAINER" psql -q "$DSN_IN" "$@"; }

if [[ -n "$SOURCE" ]]; then
  echo "== media_assets: source=$SOURCE (limit $LIMIT) =="
  psql_q -c "SELECT name, media_type, duration_ms, drive_file_id, lifecycle_state
             FROM media_assets
             WHERE source = '$SOURCE'$([[ "$WITH_DRIVE" == "1" ]] && echo " AND drive_file_id <> ''")
             ORDER BY duration_ms DESC, name
             LIMIT $LIMIT;"
  exit 0
fi

echo "== media SSOT inventory (db=$(printf '%s' "$DSN_IN" | sed -E 's#.*/([^?]+).*#\1#')) =="
psql_q -c "SELECT source, media_type, lifecycle_state, count(*) AS n
           FROM media_assets
           GROUP BY 1,2,3
           ORDER BY n DESC;"
echo "== drive-backed material and total duration =="
psql_q -c "SELECT source,
                  count(*) AS n,
                  count(*) FILTER (WHERE drive_file_id <> '') AS with_drive,
                  round(sum(duration_ms)/3600000.0, 2) AS hours,
                  count(*) FILTER (WHERE index_state = 'INDEXED') AS indexed,
                  count(*) FILTER (WHERE coalesce(binary_sha256,'') <> '' ) AS with_sha256
           FROM media_assets
           GROUP BY 1
           ORDER BY n DESC;"
echo "== stock collection (source='stock') =="
# metadata_json is TEXT in the Postgres mirror (SQLite parity), so the ->>
# extraction needs an explicit jsonb cast.
psql_q -c "SELECT name, media_type, duration_ms, drive_file_id,
                  left(coalesce(metadata_json::jsonb ->> 'content_hash',''), 16) AS content_hash,
                  coalesce(metadata_json::jsonb ->> 'size_bytes','') AS size_bytes
           FROM media_assets
           WHERE source = 'stock'
           ORDER BY name;"
echo "== youtube clips usable as scene sources (newest Drive-backed, 30s+) =="
psql_q -c "SELECT id, duration_ms, drive_file_id, folder_path, left(name, 48) AS name
           FROM media_assets
           WHERE source = 'youtube' AND drive_file_id <> '' AND duration_ms >= 30000
           ORDER BY duration_ms DESC
           LIMIT 8;"
