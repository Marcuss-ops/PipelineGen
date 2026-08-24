#!/usr/bin/env bash
# stock_e2e_db_assets_smoke.sh
#
# STK-E2E-D probe for architecture/current.yaml#STOCK-E2E-BATTERY-2026-07-05
# SQL probe against the canonical media_assets table:
#   SELECT rows WHERE source LIKE %stock% OR filename LIKE %stock%
#     OR folder_path LIKE 'Stock E2E %'
#   Verify file_hash, drive_file_id, drive_link are non-empty on every
#   returned row.
#   Output formatted padded table; exit 0 iff all rows have drive_file_id.
#
# Per godlike/06 SSOT one-canonical-owner-per-fact, the canonical
# media_assets schema is migration/sqlite/033_media_assets_youtube_video_id_index.sql
# (CREATE TABLE IF NOT EXISTS media_assets) + canonical media columns
# added by migrations/sqlite/059_canonical_media_columns.sql (folder_path,
# filename). The asserts in this probe are read-only SQL — no WRITE/
# INSERT/DELETE — so the probe is fully idempotent.
#
# Per action-plan §4 failure mapping:
#   - any row with empty drive_file_id -> PR-STOCK-MEDIA-ASSETS-DRIVE-LEAK
#     (canonical owner: internal/application/assets/providers/stock/
#      stockpipeline/upload_orchestration.go::Orchestrator.RunResilient
#      step 6 stock.finalize which writes media_assets via DB tx)
#   - any row with empty file_hash -> PR-STOCK-FILEHASH-PERSIST
#   - any row with empty drive_link -> PR-STOCK-DRIVE-LINK-PERSIST
#   - the 3-column empty set on the same row -> PR-STOCK-FINALIZER-PUBLISHER-RACE
#     (broker marked SUCCEEDED but finalizer tx was rolled back before
#      Drive metadata write)
#
# Exit codes per action-plan §5:
#   0 = PASS (zero rows found, OR all rows have non-empty columns)
#   1 = FAIL (some row missing drive_file_id / file_hash / drive_link)
#   2 = prereq missing (sqlite3 absent / db file absent / schema check fail)
#
# Self-checks: `bash -n tests/operational/stock_e2e_db_assets_smoke.sh`
# must exit 0 (validated at commit time per §5).
#
# Overridable env vars:
#   DB_PATH = data/media/media.db.sqlite
#   (NO auth/port overrides needed; pure local-file probe)

set -euo pipefail

# ---- Configuration --------------------------------------------------------
DB_PATH="${DB_PATH:-data/media/media.db.sqlite}"

# ---- Prerequisite checks (exit 2) ----------------------------------------
command -v sqlite3 >/dev/null 2>&1 || \
    { echo "FAIL: sqlite3 not on PATH (exit 2)" >&2; exit 2; }

if [ ! -f "$DB_PATH" ]; then
    echo "FAIL: $DB_PATH not found (exit 2)" >&2
    echo "  Suggested: start PipelineGen to create the DB, or point DB_PATH at the canonical path" >&2
    exit 2
fi

# ---- Schema presence probe (exit 2 if media_assets missing) --------------
# Pre-flight: ensure the table actually exists before the main query.
# Defends against DB files from earlier migrations that pre-date the
# media_assets table.
# Per code-reviewer NEEDS-FIX #1 round 1: distinguish "sqlite3 binary
# unreadable" from "table missing" via stderr capture so the operator
# gets the right canonical PR-STOCK-* (vs the previous misleading
# 'table not found' message that masked real I/O failures).
TMP_SCHEMA_ERR=$(mktemp /tmp/stock-schema-err.XXXXXX)
TABLE_CHECK=$(sqlite3 "$DB_PATH" \
    "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='media_assets';" \
    2>"$TMP_SCHEMA_ERR")
SQLITE_EXIT=$?
SQLITE_ERR=$(cat "$TMP_SCHEMA_ERR" 2>/dev/null || echo "")
rm -f "$TMP_SCHEMA_ERR"

if [ "$SQLITE_EXIT" -ne 0 ]; then
    echo "FAIL: sqlite3 cannot read $DB_PATH (exit $SQLITE_EXIT)" >&2
    [ -n "$SQLITE_ERR" ] && echo "  stderr: $SQLITE_ERR" >&2
    echo "  Suggested: verify $DB_PATH is readable + not corrupted" >&2
    exit 2
fi

if [ "$TABLE_CHECK" != "1" ]; then
    echo "FAIL: media_assets table not found in $DB_PATH (count=$TABLE_CHECK)" >&2
    echo "  Suggested: run migrations or use the canonical PipelineGen DB" >&2
    exit 2
fi

# ---- Header / logging -----------------------------------------------------
echo "=================================================================="
echo "STK-E2E-D: media_assets DB probe (stock rows + drive_file_id non-empty)"
echo "  DB_PATH  = $DB_PATH"
echo "=================================================================="

# ---- Canonical query -----------------------------------------------------
# Per godlike/07 NO-FAKE-AVAILABILITY the WHERE clause is the canonical
# scope: a row is "stock" iff ANY of {source, filename, folder_path}
# matches the stock pattern (OR-semantics per user spec). The folder_path
# prefix 'Stock E2E ' is the canonical operator-supplied test fixture
# namespace (NOT production SSOT); the %stock% on source/filename
# captures the production stock-pipeline labels.
QUERY=$(cat <<'SQL_EOF'
SELECT
    id,
    COALESCE(source, '')     AS source,
    COALESCE(filename, '')   AS filename,
    COALESCE(folder_path, '') AS folder_path,
    COALESCE(file_hash, '')  AS file_hash,
    COALESCE(drive_file_id, '') AS drive_file_id,
    COALESCE(drive_link, '') AS drive_link
FROM media_assets
WHERE
    source   LIKE '%stock%'
    OR filename LIKE '%stock%'
    OR folder_path LIKE 'Stock E2E %'
ORDER BY created_at DESC, id ASC;
SQL_EOF
)

# Run query — output goes to temp file for two-pass analysis
TMP_RAW=$(mktemp /tmp/stock-db.XXXXXX)
trap 'rm -f "$TMP_RAW"' EXIT

sqlite3 -separator '|' -header "$DB_PATH" "$QUERY" > "$TMP_RAW" 2>/dev/null || {
    echo "FAIL: sqlite3 query failed (exit 2)" >&2
    cat "$TMP_RAW" >&2 || true
    exit 2
}

# ---- Formatted padded table output --------------------------------------
# Padded table via awk (printf format strings, column widths sized for
# readability without breaking TRUNCATION). Header is preserved from
# sqlite3 -header. We force a leading separator line before the header.
echo
echo "+-----+----------------------------------+---------+----------------------------------------------+----------------------------------------------+----------------------------------------------+"
echo "| No. | id                                | source  | folder_path                                   | file_hash                                     | drive_file_id                                  |"
echo "+-----+----------------------------------+---------+----------------------------------------------+----------------------------------------------+----------------------------------------------+"
echo "|  hdr | id | source | folder_path | file_hash | drive_file_id | drive_link"  # sqlite3 header row
awk -F'|' 'NR>1 && $1!="" {printf "| %3d | %-32s | %-7s | %-44s | %-44s | %-44s |\n", NR-1, $1, $2, $4, $5, $6}' "$TMP_RAW"
echo "+-----+----------------------------------+---------+----------------------------------------------+----------------------------------------------+----------------------------------------------+"
echo

# ---- Body verbatim dump (machine-parseable) -----------------------------
# Per AGENTS.md Pattern 6 (diagnostic-surface-first): the raw pipe-
# separated rows are also emitted to stdout under a marker header so
# downstream aggregator scripts (e.g. per-folder index for STK-E2E-H)
# can ingest with `awk -F'|'`.
echo "------- raw rows (sqlite -separator | -header, parser-friendly) -------"
cat "$TMP_RAW"
echo "---------- end raw rows ----------"
echo

# ---- Row count + per-row drive_file_id check ----------------------------
TOTAL_ROWS=$(awk -F'|' 'NR>1 && NF>=7 && $1!="" {n++} END{print n+0}' "$TMP_RAW")

if [ "$TOTAL_ROWS" -eq 0 ]; then
    # Per godlike/07 NO-FAKE-AVAILABILITY: zero rows is NOT a silent-PASS.
    # The probe could not find evidence of fail, but a baseline of zero
    # stock rows is unusual; log INFO so the operator notices if their
    # STK-E2E-A/B/C runs should have produced rows yet the probe sees
    # none (possible signals: finalizer tx rolled back pre-DB-write).
    echo "INFO: zero stock media_assets rows in $DB_PATH"
    echo "  Possible signals:"
    echo "    1. STK-E2E-A/B/C runs have not yet completed (or all failed)"
    echo "    2. finalizer tx rolled back before media_assets write (PR-STOCK-FINALIZER-PUBLISHER-RACE)"
    echo "    3. canonical DB is unreachable / connection refuted (canonical server down)"
    echo "  Verdict: exit 0 (no violations found; cannot validate drive_file_id without rows)"
    exit 0
fi

# Count rows per failure mode (using awk over the raw pipe-separated rows)
ROWS_NO_FILEHASH=$(awk -F'|' 'NR>1 && NF>=7 && $1!="" && ($5=="" || $5==" ") {n++} END{print n+0}' "$TMP_RAW")
ROWS_NO_DRIVE_FILE_ID=$(awk -F'|' 'NR>1 && NF>=7 && $1!="" && ($6=="" || $6==" ") {n++} END{print n+0}' "$TMP_RAW")
ROWS_NO_DRIVE_LINK=$(awk -F'|' 'NR>1 && NF>=7 && $1!="" && ($7=="" || $7==" ") {n++} END{print n+0}' "$TMP_RAW")

# Compute the canonical "all 3 missing on the same row" set (PR-STOCK-FINALIZER-PUBLISHER-RACE)
ROWS_ALL_THREE_MISSING=$(awk -F'|' 'NR>1 && NF>=7 && $1!="" && ($5=="" || $5==" ") && ($6=="" || $6==" ") && ($7=="" || $7==" ") {n++} END{print n+0}' "$TMP_RAW")

echo "Summary:"
echo "  total_rows                       = $TOTAL_ROWS"
echo "  empty file_hash                  = $ROWS_NO_FILEHASH"
echo "  empty drive_file_id              = $ROWS_NO_DRIVE_FILE_ID"
echo "  empty drive_link                 = $ROWS_NO_DRIVE_LINK"
echo "  empty [file_hash+drive_file_id+drive_link] (all 3) = $ROWS_ALL_THREE_MISSING"
echo

# ---- Canonical PR-STOCK-* failure mapping (per action plan §4) ----------
if [ "$ROWS_NO_DRIVE_FILE_ID" -gt 0 ] || [ "$ROWS_NO_FILEHASH" -gt 0 ] || [ "$ROWS_NO_DRIVE_LINK" -gt 0 ]; then
    echo "FAIL: $TOTAL_ROWS stock media_assets rows; violations detected" >&2
    if [ "$ROWS_NO_DRIVE_FILE_ID" -gt 0 ]; then
        echo "FAIL canonical: PR-STOCK-MEDIA-ASSETS-DRIVE-LEAK" >&2
        echo "  $ROWS_NO_DRIVE_FILE_ID rows with empty drive_file_id" >&2
        echo "  Canonical owner: upload_orchestration.go::Orchestrator.RunResilient step 6 stock.finalize" >&2
        echo "  Likely root cause: finalizer tx rolled back before Drive metadata write" >&2
        echo "  Forward-pointer: PR-STOCK-FINALIZER-PUBLISHER-RACE pre-condition" >&2
    fi
    if [ "$ROWS_NO_FILEHASH" -gt 0 ]; then
        echo "FAIL canonical: PR-STOCK-FILEHASH-PERSIST" >&2
        echo "  $ROWS_NO_FILEHASH rows with empty file_hash" >&2
        echo "  Canonical owner: internal/application/assets/providers/stock/stockpipeline/upload_orchestration.go" >&2
        echo "  Likely root cause: StockRenderWriteStep emitted without computing hash" >&2
    fi
    if [ "$ROWS_NO_DRIVE_LINK" -gt 0 ]; then
        echo "FAIL canonical: PR-STOCK-DRIVE-LINK-PERSIST" >&2
        echo "  $ROWS_NO_DRIVE_LINK rows with empty drive_link" >&2
        echo "  Canonical owner: internal/platform/drive/publisher.go::Publish" >&2
        echo "  Likely root cause: Drive upload succeeded but Web link not constructed" >&2
    fi
    if [ "$ROWS_ALL_THREE_MISSING" -gt 0 ] && [ "$ROWS_ALL_THREE_MISSING" -ne "$ROWS_NO_DRIVE_FILE_ID" ]; then
        # partial overlap (rows with all 3 missing BUT not classified as
        # the dominant empty-cell) — diagnostic hint
        echo "INFO: $ROWS_ALL_THREE_MISSING rows have ALL 3 columns empty (PR-STOCK-FINALIZER-PUBLISHER-RACE)" >&2
        echo "  These rows are broker-marked-SUCCEEDED but finalizer tx rolled back" >&2
    fi
    exit 1
fi

echo "PASS: $TOTAL_ROWS stock media_assets rows; all rows have non-empty file_hash/drive_file_id/drive_link"
exit 0
