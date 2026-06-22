#!/usr/bin/env bash
# scripts/db-restore-drill.sh (June 2026 codex/db-doctor-restore):
#
# Canonical restore drill end-to-end:
#   1) backup the production Primary DB to /tmp/pipelinegen-drill-backup.sqlite
#   2) restore the backup to a fresh staging dir
#   3) the restore itself runs integrity_check + foreign_key_check + E2E smoke
#   4) print RTO + RPO
# Exit 0 if every probe passes.
set -euo pipefail

DATA_DIR="${VELOX_DATA_DIR:-./data}"
DRILL_BACKUP="${DRILL_BACKUP:-/tmp/pipelinegen-drill-backup.sqlite}"
DRILL_STAGING_DIR="${DRILL_STAGING_DIR:-/tmp/pipelinegen-drill-staging}"
DRILL_RESTORED="${DRILL_STAGING_DIR}/media/media.db.sqlite"

# Pick whichever on-disk file matches the current primary path
# (legacy `<DataDir>/media.db.sqlite` OR canonical `<DataDir>/media/media.db.sqlite`).
PROD_DB="${DATA_DIR}/media/media.db.sqlite"
if [ ! -f "$PROD_DB" ]; then
  PROD_DB="${DATA_DIR}/media.db.sqlite"
fi
if [ ! -f "$PROD_DB" ]; then
  echo "FAIL: no production DB found at ${DATA_DIR}/media/media.db.sqlite or ${DATA_DIR}/media.db.sqlite"
  exit 1
fi
echo "production DB: $PROD_DB"

echo "=== STEP 1: backup the production DB ==="
go run ./cmd/admin db backup -src "$PROD_DB" -out "$DRILL_BACKUP" -data-dir "$DATA_DIR"

echo ""
echo "=== STEP 2: clean staging + restore backup ==="
rm -rf "$DRILL_STAGING_DIR"
mkdir -p "$(dirname "$DRILL_RESTORED")"

go run ./cmd/admin db restore -src "$DRILL_BACKUP" -dst "$DRILL_RESTORED" --verify -data-dir "$DATA_DIR"

echo ""
echo "=== STEP 3: drill complete ==="
echo "  backup: $DRILL_BACKUP"
echo "  restored: $DRILL_RESTORED"
echo "  (RTO + RPO printed in the JSON output above)"
