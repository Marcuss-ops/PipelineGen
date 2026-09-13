// Package cleanup — remove_drive_folder_recursive.go (RETIRED,
// POSTGRES-MEDIA-CUTOVER, September 2026).
//
// RunRemoveDriveFolderRecursive deleted a Drive folder tree and purged the
// matching rows from the SQLite media catalog. Its preflight
// (`checkMediaAssetsReady`) asserted that the SQLite `media_assets` table
// existed, and its planning queries (`listAssetsInFolder`,
// `listAssetsBySourceVideoIDs`) enumerated assets from that same table.
//
// `media_assets` is authoritative ONLY in PostgreSQL + pgvector now, so the
// planning half of that contract cannot be honoured: the tool would delete
// Drive folders while reading a quarantined catalog, and any DB-side "cleanup"
// it used to perform would desynchronise the SSOT instead of matching it. That
// is the silent-divergence failure mode AGENTS.md forbids (never report an
// unavailable backend as a successful no-op).
//
// The command stays REGISTERED through its `cmd/admin/internal/drive`
// compatibility forwarder and fails closed BEFORE touching Drive, with a typed
// retirement error. A PG-aware successor must plan through the canonical
// PostgreSQL media reader and delete through `persistence.AssetCommitter`
// (PostgresMediaCommitter delete saga) before this capability is reintroduced.
package cleanup

import (
	"errors"
	"fmt"
)

// ErrRemoveDriveFolderRecursiveRetired is returned by the retired command.
var ErrRemoveDriveFolderRecursiveRetired = errors.New("remove-drive-folder-recursive: the recursive Drive removal was retired with the PostgreSQL media-SSOT cutover (media demolition, September 2026) — its preflight and asset planning read the quarantined SQLite media_assets catalog, so it could delete Drive folders while leaving the authoritative PostgreSQL rows intact; plan through the PostgreSQL media reader and delete through persistence.AssetCommitter instead")

// RunRemoveDriveFolderRecursive is retained as a subcommand stub: it fails
// closed before any Drive mutation.
func RunRemoveDriveFolderRecursive(_ []string) error {
	return fmt.Errorf("%w", ErrRemoveDriveFolderRecursiveRetired)
}
