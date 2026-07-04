#!/usr/bin/env bash
# =============================================================================
# recover_apollo_cosmos_pngs.sh — AZIONE 9 (P1, BACKFILL phase)
# =============================================================================
# Canonical recovery command for already-completed artifacts in the
# script.generate workflow. Recovers Apollo and Cosmos PNG fixtures from
# data/media/google-slides/ and re-registers them as READY artifacts
# without re-creating the scene generation pipeline.
#
# godlike/07 Typed-Error Contract: IDEMPOTENT — second run yields zero
# new artifacts (checks existing artifact status before registering).
#
# Usage:
#   bash scripts/recovery/recover_apollo_cosmos_pngs.sh [--dry-run]
#
#   --dry-run   Print what WOULD be done without modifying anything.
#
# Exit codes:
#   0 — Success (or dry-run passed validation).
#   1 — Missing fixture files at expected paths.
#   2 — Database migration required but not applied.
# =============================================================================

set -euo pipefail

DRY_RUN=false
if [[ "${1:-}" == "--dry-run" ]]; then
    DRY_RUN=true
    echo "[DRY-RUN] No modifications will be made."
fi

# ── Canonical paths ────────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
DATA_DIR="${PROJECT_ROOT}/data/media/google-slides"
DB_PATH="${PROJECT_ROOT}/data/media/media.db.sqlite"
ADMIN_BIN="${PROJECT_ROOT}/pipelinegen"

APOLLO_PNG="${DATA_DIR}/apollo.png"
COSMOS_PNG="${DATA_DIR}/cosmos.png"

# ── Pre-flight: fixture existence ──────────────────────────────────────────
MISSING=()
for f in "$APOLLO_PNG" "$COSMOS_PNG"; do
    if [[ ! -f "$f" ]]; then
        MISSING+=("$f")
    fi
done
if [[ ${#MISSING[@]} -gt 0 ]]; then
    echo "ERROR: Missing fixture files:" >&2
    for f in "${MISSING[@]}"; do
        echo "  - $f" >&2
    done
    echo "" >&2
    echo "These fixtures are expected to exist at the canonical paths above." >&2
    echo "If they were moved, locate them and symlink or copy them back." >&2
    exit 1
fi

# ── Pre-flight: database readiness ─────────────────────────────────────────
if [[ ! -f "$DB_PATH" ]]; then
    echo "ERROR: Database not found at $DB_PATH" >&2
    exit 2
fi

# ── Check migration status via sqlite3 ─────────────────────────────────────
if ! command -v sqlite3 &>/dev/null; then
    echo "ERROR: sqlite3 not found in PATH — required for migration check." >&2
    exit 2
fi

MIGRATION_CHECK=$(sqlite3 "$DB_PATH" \
    "SELECT COUNT(*) FROM schema_migrations WHERE filename = '130_rename_ready_to_staged.sql';" 2>/dev/null || echo "0")
if [[ "$MIGRATION_CHECK" == "0" ]]; then
    echo "WARNING: Migration 130 (rename READY → STAGED) has not been applied." >&2
    echo "The recovery script will use the canonical 'STAGED' status." >&2
    echo "If your DB still uses 'READY', run migrations first:" >&2
    echo "  go run ./cmd/admin migrate" >&2
fi

# ── Recovery logic ─────────────────────────────────────────────────────────
recover_png() {
    local label="$1"
    local png_path="$2"

    # Compute SHA-256 of the fixture.
    local sha
    sha=$(sha256sum "$png_path" 2>/dev/null | awk '{print $1}' || \
          shasum -a 256 "$png_path" 2>/dev/null | awk '{print $1}')
    if [[ -z "$sha" ]]; then
        echo "ERROR: Cannot compute SHA-256 for $png_path (sha256sum/shasum missing?)" >&2
        return 1
    fi

    # Check if this artifact already exists by SHA-256 (idempotency gate).
    local existing
    existing=$(sqlite3 "$DB_PATH" \
        "SELECT COUNT(*) FROM artifacts WHERE sha256 = '$sha' AND status IN ('READY', 'STAGED');" 2>/dev/null || echo "0")
    if [[ "$existing" != "0" ]]; then
        echo "  [$label] SKIP — artifact with SHA-256 $sha already exists (idempotent, no action)."
        return 0
    fi

    if $DRY_RUN; then
        echo "  [$label] WOULD REGISTER — SHA-256=$sha, path=$png_path"
        return 0
    fi

    # Register via the admin CLI (canonical artifact registration path).
    # The admin command reads the file, computes its own SHA-256, and
    # registers it with the canonical content-addressed storage.
    if [[ -x "$ADMIN_BIN" ]]; then
        "$ADMIN_BIN" artifact register \
            --kind image \
            --mime-type image/png \
            --file "$png_path" \
            --label "$label-recovery" 2>&1 || {
            echo "  [$label] ERROR — admin CLI failed (see above)." >&2
            return 1
        }
    else
        echo "  [$label] ERROR — admin binary not found at $ADMIN_BIN." >&2
        echo "  [$label] Build it first: go build -o pipelinegen ./cmd/admin/" >&2
        return 1
    fi

    echo "  [$label] RECOVERED — SHA-256=$sha, status=STAGED"
}

echo "=== Recovering Apollo + Cosmos PNG fixtures ==="
echo "Data dir:  $DATA_DIR"
echo "DB path:   $DB_PATH"
echo ""

FAILED=0
recover_png "apollo" "$APOLLO_PNG" || FAILED=$((FAILED + 1))
recover_png "cosmos" "$COSMOS_PNG" || FAILED=$((FAILED + 1))

if [[ $FAILED -gt 0 ]]; then
    echo ""
    echo "ERROR: $FAILED fixture(s) failed to recover. See above."
    exit 1
fi

echo ""
echo "Artifacts recovered successfully."
echo "Done."
