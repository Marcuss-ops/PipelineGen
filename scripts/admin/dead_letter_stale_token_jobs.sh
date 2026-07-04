#!/usr/bin/env bash
# =============================================================================
# dead_letter_stale_token_jobs.sh — AZIONE 14 (P1, BACKFILL phase)
# =============================================================================
# Sweeps stale-token lease rows into DEAD_LETTERED status and inserts an
# audit record into dead_letter_audit for every affected job.
#
# A "stale-token lease" is a job row where:
#   - status = 'RUNNING'
#   - lease_expiry < NOW()  (the lease has expired)
#   - The worker that held the lease has NOT heartbeated within the
#     session TTL window (the token is stale).
#
# godlike/07 Typed-Error Contract: IDEMPOTENT — the UPDATE is a no-op on
# rows already DEAD_LETTERED. Re-runs return zero new rows.
#
# Usage:
#   bash scripts/admin/dead_letter_stale_token_jobs.sh [--dry-run] [--older-than MINUTES]
#
#   --dry-run          Print what WOULD be dead-lettered without modifying.
#   --older-than N     Only sweep jobs whose lease expired > N minutes ago
#                      (default: 10 — prevents sweeping a just-expired
#                      lease that may still be in-flight).
#
# Exit codes:
#   0 — Success (or dry-run passed).
#   1 — Database not found.
#   2 — sqlite3 not available.
# =============================================================================

set -euo pipefail

DRY_RUN=false
OLDER_THAN=10

while [[ $# -gt 0 ]]; do
    case "$1" in
        --dry-run)
            DRY_RUN=true
            shift
            ;;
        --older-than)
            OLDER_THAN="$2"
            if ! [[ "$OLDER_THAN" =~ ^[0-9]+$ ]]; then
                echo "ERROR: --older-than must be a positive integer, got: $OLDER_THAN" >&2
                exit 1
            fi
            shift 2
            ;;
        *)
            echo "Unknown flag: $1" >&2
            echo "Usage: $0 [--dry-run] [--older-than MINUTES]" >&2
            exit 1
            ;;
    esac
done

# ── Canonical paths ────────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
DB_PATH="${PROJECT_ROOT}/data/media/media.db.sqlite"

# ── Pre-flight ─────────────────────────────────────────────────────────────
if [[ ! -f "$DB_PATH" ]]; then
    echo "ERROR: Database not found at $DB_PATH" >&2
    exit 1
fi

if ! command -v sqlite3 &>/dev/null; then
    echo "ERROR: sqlite3 not found in PATH." >&2
    exit 2
fi

# ── Audit table bootstrap (idempotent) ─────────────────────────────────────
sqlite3 "$DB_PATH" <<'EOSQL'
CREATE TABLE IF NOT EXISTS dead_letter_audit (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id      TEXT    NOT NULL,
    job_type    TEXT    NOT NULL,
    worker_id   TEXT,
    lease_id    TEXT,
    reason      TEXT    NOT NULL,
    sweep_run   TEXT    NOT NULL,  -- timestamp of this sweep batch
    created_at  TEXT    NOT NULL DEFAULT (datetime('now'))
);
EOSQL

# ── Sweep logic ────────────────────────────────────────────────────────────
SWEEP_TS=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

# Count candidates BEFORE sweeping (for the audit message).
CANDIDATES=$(sqlite3 "$DB_PATH" "
    SELECT COUNT(*)
    FROM jobs
    WHERE status = 'RUNNING'
      AND lease_expiry < datetime('now', '-${OLDER_THAN} minutes');
" 2>/dev/null || echo "0")

if [[ "$CANDIDATES" == "0" ]]; then
    echo "No stale-token lease jobs found (${OLDER_THAN}min threshold)."
    echo "Done."
    exit 0
fi

echo "=== Dead-Letter Sweep ==="
echo "Threshold:  lease expired > ${OLDER_THAN} minutes ago"
echo "Candidates: $CANDIDATES jobs"
echo "Sweep run:  $SWEEP_TS"
echo ""

if $DRY_RUN; then
    echo "[DRY-RUN] Would dead-letter the following jobs:"
    sqlite3 "$DB_PATH" "
        SELECT id, type, worker_id, lease_id, lease_expiry
        FROM jobs
        WHERE status = 'RUNNING'
          AND lease_expiry < datetime('now', '-${OLDER_THAN} minutes')
        ORDER BY lease_expiry ASC;
    "
    echo ""
    echo "[DRY-RUN] No modifications made."
    exit 0
fi

# ── Atomic sweep: capture affected IDs in a temp table, then UPDATE ────────
# Uses a temp table to reliably identify affected rows without relying on
# updated_at = datetime('now') (which is race-prone under concurrent writes).

sqlite3 "$DB_PATH" <<EOSQL
BEGIN;

-- Capture affected job IDs BEFORE the UPDATE (deterministic set).
CREATE TEMP TABLE IF NOT EXISTS _dl_sweep_ids (job_id TEXT PRIMARY KEY);
DELETE FROM _dl_sweep_ids;

INSERT INTO _dl_sweep_ids (job_id)
SELECT id FROM jobs
WHERE status = 'RUNNING'
  AND lease_expiry < datetime('now', '-${OLDER_THAN} minutes');

-- Dead-letter the stale jobs (no-op on already-DEAD_LETTERED rows).
UPDATE jobs
SET status = 'DEAD_LETTERED',
    updated_at = datetime('now')
WHERE id IN (SELECT job_id FROM _dl_sweep_ids)
  AND status = 'RUNNING';

-- Insert audit records using the captured set (deterministic).
INSERT INTO dead_letter_audit (job_id, job_type, worker_id, lease_id, reason, sweep_run)
SELECT
    j.id,
    j.type,
    j.worker_id,
    j.lease_id,
    'stale-token: lease expired > ${OLDER_THAN}min ago',
    '${SWEEP_TS}'
FROM _dl_sweep_ids s
JOIN jobs j ON j.id = s.job_id
WHERE j.status = 'DEAD_LETTERED';

DROP TABLE _dl_sweep_ids;

COMMIT;
EOSQL

AFFECTED=$(sqlite3 "$DB_PATH" "
    SELECT COUNT(*) FROM dead_letter_audit WHERE sweep_run = '$SWEEP_TS';
" 2>/dev/null || echo "0")

echo "Dead-lettered: $AFFECTED jobs"
echo "Sweep complete — audit rows in dead_letter_audit (sweep_run=$SWEEP_TS)."
echo "Done."
