#!/usr/bin/env bash
# Move execution and derived cache state out of media.db.sqlite.
# Default is dry-run. --apply creates a source snapshot, initializes the
# destination schemas, copies common columns, and writes a manifest. It never
# drops or renames source tables.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
DATA_DIR="${DATA_DIR:-data}"
MEDIA_DB="${MEDIA_DB:-$DATA_DIR/media/media.db.sqlite}"
JOBS_DB="${JOBS_DB:-$DATA_DIR/jobs/jobs.db.sqlite}"
CACHE_DB="${CACHE_DB:-$DATA_DIR/cache/cache.db.sqlite}"
BACKUP_DIR="${BACKUP_DIR:-$DATA_DIR/backups/data-plane-migration-$(date -u +%Y%m%dT%H%M%SZ)}"
APPLY=0
for arg in "$@"; do
  case "$arg" in
    --apply) APPLY=1 ;;
    --data-dir=*) DATA_DIR="${arg#*=}"; MEDIA_DB="$DATA_DIR/media/media.db.sqlite"; JOBS_DB="$DATA_DIR/jobs/jobs.db.sqlite"; CACHE_DB="$DATA_DIR/cache/cache.db.sqlite" ;;
    --backup-dir=*) BACKUP_DIR="${arg#*=}" ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

[[ -f "$MEDIA_DB" ]] || { echo "source media DB missing: $MEDIA_DB" >&2; exit 1; }
command -v sqlite3 >/dev/null || { echo "sqlite3 is required" >&2; exit 1; }

job_tables=(jobs job_events job_results job_steps job_registry_events job_registry_metrics job_asset_relations job_checkpoints dead_letter_jobs artifact_stages preparation_units preparation_job_units preparation_dependencies preparation_claim_snapshots preparation_attempts)
cache_tables=(research_cache artlist_search_cache transcript_cache translation_cache stock_source_cache vidrush_provider_cache media_query_cache artifact_cache_entries artifact_cache_metrics)
printf 'DATA PLANE MIGRATION\nsource=%s\njobs=%s\ncache=%s\nmode=%s\n' "$MEDIA_DB" "$JOBS_DB" "$CACHE_DB" "$([[ $APPLY -eq 1 ]] && echo APPLY || echo DRY-RUN)"

table_exists() { [[ "$(sqlite3 "$1" "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='$2';")" == 1 ]]; }
row_count() { sqlite3 "$1" "SELECT COUNT(*) FROM \"$2\";" 2>/dev/null || echo 0; }
common_columns() {
  local src="$1" dst="$2" table="$3" out=() col
  while IFS= read -r col; do
    [[ -n "$col" ]] || continue
    if sqlite3 "$dst" "SELECT 1 FROM pragma_table_info('$table') WHERE name='$col';" | grep -q 1; then
      out+=("\"$col\"")
    fi
  done < <(sqlite3 "$src" "SELECT name FROM pragma_table_info('$table') ORDER BY cid;")
  (IFS=,; echo "${out[*]}")
}
copy_table() {
  local table="$1" src="$2" dst="$3" cols
  table_exists "$src" "$table" || return 0
  table_exists "$dst" "$table" || { echo "  SKIP $table (destination schema has no table)"; return 0; }
  cols="$(common_columns "$src" "$dst" "$table")"
  [[ -n "$cols" ]] || { echo "  FAIL $table (no common columns)" >&2; return 1; }
  sqlite3 "$dst" <<SQL
ATTACH DATABASE '$src' AS source_db;
BEGIN IMMEDIATE;
INSERT OR IGNORE INTO "$table" ($cols) SELECT $cols FROM source_db."$table";
COMMIT;
DETACH DATABASE source_db;
SQL
  echo "  $table: $(row_count "$src" "$table") → $(row_count "$dst" "$table")"
}

if [[ $APPLY -eq 0 ]]; then
  echo "DRY-RUN source counts"
  for t in "${job_tables[@]}" "${cache_tables[@]}"; do
    if table_exists "$MEDIA_DB" "$t"; then printf '%-35s %s\n' "$t" "$(row_count "$MEDIA_DB" "$t")"; fi
  done
  echo "No files changed. Re-run with --apply after reviewing counts."
  exit 0
fi

mkdir -p "$(dirname "$JOBS_DB")" "$(dirname "$CACHE_DB")" "$BACKUP_DIR"
MEDIA_SNAPSHOT="$BACKUP_DIR/media-before.sqlite"
sqlite3 "$MEDIA_DB" "VACUUM INTO '$(cd "$(dirname "$MEDIA_SNAPSHOT")" && pwd)/$(basename "$MEDIA_SNAPSHOT")';"
[[ -s "$MEDIA_SNAPSHOT" ]] || { echo "media snapshot was not created" >&2; exit 1; }

[[ ! -e "$JOBS_DB" ]] || { echo "destination exists: $JOBS_DB" >&2; exit 1; }
[[ ! -e "$CACHE_DB" ]] || { echo "destination exists: $CACHE_DB" >&2; exit 1; }
sqlite3 "$JOBS_DB" ".read $ROOT/migrations/sqlite_jobs/001_jobs_plane.sql"
sqlite3 "$CACHE_DB" ".read $ROOT/migrations/sqlite/260_cache_plane.sql"

echo "Migrating jobs plane"
for t in "${job_tables[@]}"; do copy_table "$t" "$MEDIA_SNAPSHOT" "$JOBS_DB"; done
if table_exists "$MEDIA_SNAPSHOT" jobs; then
  sqlite3 "$JOBS_DB" <<SQL
ATTACH DATABASE '$MEDIA_SNAPSHOT' AS source_db;
INSERT OR IGNORE INTO job_payloads (job_id, codec_id, payload, payload_hash, created_at)
SELECT id, 'json', COALESCE(payload_json, '{}'), '', COALESCE(created_at, datetime('now'))
FROM source_db.jobs WHERE COALESCE(payload_json, '') NOT IN ('', 'null');
DETACH DATABASE source_db;
SQL
fi

echo "Migrating cache plane"
for t in "${cache_tables[@]}"; do copy_table "$t" "$MEDIA_SNAPSHOT" "$CACHE_DB"; done

sha256sum "$MEDIA_SNAPSHOT" "$JOBS_DB" "$CACHE_DB" >"$BACKUP_DIR/manifest.sha256"
python3 - "$BACKUP_DIR" "$MEDIA_SNAPSHOT" "$JOBS_DB" "$CACHE_DB" <<'PY'
import hashlib, json, os, sqlite3, sys
out, *paths = sys.argv[1:]
def info(path):
    con=sqlite3.connect(path)
    tables={n: con.execute(f'SELECT COUNT(*) FROM "{n}"').fetchone()[0]
            for (n,) in con.execute("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
            if n != 'sqlite_sequence'}
    con.close()
    h=hashlib.sha256()
    with open(path,'rb') as f:
        for chunk in iter(lambda:f.read(1024*1024), b''): h.update(chunk)
    return {'path':os.path.abspath(path),'bytes':os.path.getsize(path),'sha256':h.hexdigest(),'tables':tables}
manifest={'format':'pipelinegen-data-plane-migration/v1','source_snapshot':info(paths[0]),'jobs':info(paths[1]),'cache':info(paths[2]),'source_tables_retained':True}
with open(os.path.join(out,'manifest.json'),'w',encoding='utf-8') as f:
    json.dump(manifest,f,indent=2); f.write('\n')
PY
echo "Manifest: $BACKUP_DIR/manifest.json"
echo "Migration complete without modifying source tables."

