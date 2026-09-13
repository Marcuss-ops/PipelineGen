// Package maintenance — stock_reset.go (RETIRED, POSTGRES-MEDIA-CUTOVER,
// September 2026).
//
// RunResetStockDrive deleted the non-kept Drive folders under the stock root
// and then PURGED the matching rows from the SQLite catalog
// (`media_assets`, `clip_folders`, plus the legacy `asset_links` /
// `asset_index` / `script_stock_matches` tables).
//
// The database leg can no longer be honoured: `media_assets` is authoritative
// ONLY in PostgreSQL + pgvector, and this command has no PostgreSQL writer.
// Running it would wipe Drive while leaving the authoritative PG rows intact —
// a silent divergence between the SSOT and the delivered bytes, and precisely
// the "unavailable backend reported as a successful no-op" failure mode
// AGENTS.md forbids. `percheck_media_assets_writer_canonical` flags the
// `DELETE FROM media_assets` as a forbidden non-canonical media write.
//
// The command therefore stays REGISTERED and fails closed BEFORE any Drive
// mutation, with a typed retirement error. A PG-aware successor must delete
// through the canonical `persistence.AssetCommitter` (PostgresMediaCommitter
// delete saga) before this capability can be reintroduced.
package maintenance

import (
	"errors"
	"fmt"
)

// ErrStockResetRetired is returned by the retired stock drive reset.
var ErrStockResetRetired = errors.New("stock-reset: purging stock rows from the SQLite media catalog was retired with the PostgreSQL media-SSOT cutover (media demolition, September 2026) — media_assets is authoritative ONLY in PostgreSQL, so deleting Drive folders without a PostgreSQL-side delete would desynchronise the SSOT; use the canonical committer delete path against PostgreSQL")

// RunResetStockDrive is retained as a subcommand stub. It fails closed BEFORE
// touching Drive so a partial reset cannot leave the PostgreSQL media catalog
// describing folders that no longer exist.
func RunResetStockDrive(_ []string) error {
	return fmt.Errorf("%w", ErrStockResetRetired)
}
