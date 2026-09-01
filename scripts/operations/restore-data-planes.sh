#!/usr/bin/env bash
# Real backup/restore smoke for all durable planes. Never mutates live DBs.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
DATA_DIR="${CERTIFY_DATA_DIR:-data}"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
fail=0
for spec in "media:$DATA_DIR/media/media.db.sqlite" "jobs:$DATA_DIR/jobs/jobs.db.sqlite" "observability:$DATA_DIR/observability/api_requests.db.sqlite"; do
  name="${spec%%:*}"; src="${spec#*:}"; backup="$TMP/$name-backup.sqlite"; restored="$TMP/$name-restored.sqlite"
  [[ -f "$src" ]] || { echo "FAIL $name source missing" >&2; exit 1; }
  sqlite3 "$src" "VACUUM INTO '$backup'"
  sqlite3 "$backup" "VACUUM INTO '$restored'"
  [[ "$(sqlite3 "$restored" 'PRAGMA integrity_check;')" == ok ]] || { echo "FAIL $name integrity" >&2; fail=1; }
  [[ -z "$(sqlite3 "$restored" 'PRAGMA foreign_key_check;')" ]] || { echo "FAIL $name foreign keys" >&2; fail=1; }
  src_hash="$(sqlite3 -csv "$src" "SELECT name,sql FROM sqlite_master WHERE type IN ('table','index','trigger','view') ORDER BY type,name;" | sha256sum | awk '{print $1}')"
  dst_hash="$(sqlite3 -csv "$restored" "SELECT name,sql FROM sqlite_master WHERE type IN ('table','index','trigger','view') ORDER BY type,name;" | sha256sum | awk '{print $1}')"
  [[ "$src_hash" == "$dst_hash" ]] || { echo "FAIL $name schema parity" >&2; fail=1; }
  echo "PASS $name restore integrity/fk/schema"
done
[[ ! -e "$TMP/cache-backup.sqlite" ]] || { echo "FAIL cache must be excluded" >&2; fail=1; }
echo "PASS cache excluded from durable restore set"
exit "$fail"

