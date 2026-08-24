// Package deletion — stuck_row_scanner.go (Blocco 3.2 commit 2/2, June 2026)
//
// Production adapter for the application-layer
// assets.deletion.reconciler.Scanner port. ListStuckRows queries
// media_assets for rows in any of the 3 deletion-chain states
// (DELETE_REQUESTED, DRIVE_DELETE_PENDING, INDEX_DELETE_PENDING)
// whose updated_at is strictly older than now-threshold.
//
// The query is the canonical Blocco 3.2 reconciled surface. It
// must agree field-by-field with the index_state IsValidTransition
// semantics (committed in commit 42a2e5aa) — anything in those 3
// states is normal; anything outside them is a row that has either
// completed (state in {DELETED, lowercase "deleted"}) or been
// cancelled/restored (state in {ACTIVE, STAGING, ...}).
//
// Sort order: ascending by updated_at so the OLDEST stuck row is
// surfaced first — matches operator-dashboard skim expectations
// (the row that's been stuck the longest is what the operator
// wants to see first). Per-tick limit: 100 rows (the canonical
// Blocco 3.2 batch size mirrors the qdrant/reconciler precedent
// + outbox-pool worker count to balance per-tick DB load against
// catch-up throughput).
package deletion

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/deletion/reconciler"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// Scanner is the production concrete adapter for the
// application-layer reconciler.Scanner port. Pattern 0 from
// AGENTS.md — the application-layer declares the port; the
// infrastructure-layer produces this adapter.
type Scanner struct {
	db        *sql.DB
	batchSize int
}

// Compile-time assertion: Scanner satisfies reconciler.Scanner.
// Drift in any method signature becomes a build failure here
// rather than a runtime panic in the first dispatcher call.
var _ reconciler.Scanner = (*Scanner)(nil)

// NewScanner wires a Scanner backed by db. batchSize <= 0 falls
// back to the canonical default (100 rows/tick matches outbox-
// pool worker concurrency × typical stuck-row density on the
// 100-worker reference deployment).
func NewScanner(db *sql.DB, batchSize int) *Scanner {
	if db == nil {
		panic("deletion.NewScanner: db is required")
	}
	if batchSize <= 0 {
		batchSize = 100
	}
	return &Scanner{db: db, batchSize: batchSize}
}

// ListStuckRows returns up to batchSize rows from media_assets
// matching the deletion-chain-states filter + updated_at older
// than now-threshold, sorted by updated_at ASC.
//
// Includes the lowercase "deleted" legacy casings only as an
// invariant (the production schema is uppercase-only since PR-1;
// the lowercase compat survives purely to absorb any in-flight
// migration that pre-dates Blocco 3.1 commit 2/3's EnqueueDriveDelete
// rewrite — those rows are still in "deleted" briefly during
// the SoftDeleteFilter's "lower OR upper" accept window).
//
// Errors:
//   - sql.ErrNoRows: never returned (we scan 0..N rows).
//   - non-nil on any SQL error (driver-level or DB schema drift).
//     ReconcileOnce surfaces this as a fail-close error per the
//     Phase-1 fail-close doctrine (qdrant/reconciler/scanner.go).
func (s *Scanner) ListStuckRows(now time.Time, threshold time.Duration) ([]reconciler.StuckRow, error) {
	cutoff := now.Add(-threshold)
	cutoffStr := timeutil.FormatRFC3339(cutoff)
	rows, err := s.db.QueryContext(context.Background(), `
		SELECT id, lifecycle_state, updated_at
		FROM media_assets
		WHERE lifecycle_state IN (
		  'DELETE_REQUESTED',
		  'DRIVE_DELETE_PENDING',
		  'INDEX_DELETE_PENDING'
		)
		AND updated_at < ?
		ORDER BY updated_at ASC
		LIMIT ?
	`, cutoffStr, s.batchSize)
	if err != nil {
		return nil, fmt.Errorf("deletion. Scanner.ListStuckRows: query: %w", err)
	}
	defer rows.Close()

	var out []reconciler.StuckRow
	for rows.Next() {
		var (
			id           string
			stateStr     string
			updatedAtStr string
		)
		if err := rows.Scan(&id, &stateStr, &updatedAtStr); err != nil {
			return nil, fmt.Errorf("deletion. Scanner.ListStuckRows: scan: %w", err)
		}
		updatedAt := timeutil.ParseRFC3339(updatedAtStr)
		if updatedAt.IsZero() {
			return nil, fmt.Errorf("deletion. Scanner.ListStuckRows: row %s has zero updated_at (RFC3339 parse failed)", id)
		}
		out = append(out, reconciler.StuckRow{
			AssetID:   id,
			State:     stateStr,
			UpdatedAt: updatedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("deletion. Scanner.ListStuckRows: rows.Err: %w", err)
	}
	return out, nil
}
