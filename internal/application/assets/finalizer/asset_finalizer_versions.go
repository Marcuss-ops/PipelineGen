// Package finalizer — asset_finalizer_versions.go (split from
// asset_finalizer_tx.go, July 2026): helper SQL for the canonical
// asset_versions table (sequential versioning of an asset).
//
// Owns:
//
//  1. func (s *AssetTxFinalizer) insertAssetVersion — INSERT a new
//     asset_versions row keyed on asset_id + version_number, with
//     version_number computed INSIDE the caller's tx via
//     QueryRowContext to avoid concurrent writers colliding on the
//     (asset_id, version_number) UNIQUE constraint. file_hash,
//     file_size_bytes, mime_type are all sourced from the
//     PublishedArtifact; metadata_json is fixed at '{}' here
//     (per-version metadata is captured at the media_assets level).
//
// Caller-owned-tx discipline (godlike/06 SSOT, non-negotiable
// architectural rule): same as sibling helper files — uses
// finalization.Transaction (ExecContext + QueryRowContext). Does
// NOT own BeginTx / Commit / Rollback.
//
// Version-number contract: QueryRowContext reads
// COALESCE(MAX(version_number), 0) + 1 inside the caller's tx so
// concurrent writers CANNOT collide on the (asset_id,
// version_number) UNIQUE constraint. Test
// TestAssetTxFinalizer_RollbackOnError verifies the contract:
// even with manually-inserted version 999, the next MAX+1 = 1000
// passes the UNIQUE check.
//
// MAPPING NOTE: the per-prompt spec mentions "tracks" (presumed
// from the clip_atomic_writer sister pattern), but this finalizer
// does NOT write to a text tracks table — it writes to
// asset_versions for sequential content versioning. The "versions"
// file is the faithful mapping for the canonical DB schema; the
// atomic discipline (caller-owned-tx, helper-only SQL seams) is
// preserved verbatim.
//
// Mechanical split from asset_finalizer_tx.go. Zero behavior
// change. The receiver (s *AssetTxFinalizer) is unchanged so the
// orchestrator can call this helper as `s.insertAssetVersion(...)`
// without any wiring change.
package finalizer

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
)

// insertAssetVersion inserts a new asset_versions row with
// MAX(version_number)+1 computed inside the caller's tx.
func (s *AssetTxFinalizer) insertAssetVersion(
	ctx context.Context,
	tx finalization.Transaction,
	a *finalization.PublishedArtifact,
	nowStr string,
) (int, error) {
	// Compute next version_number inside the transaction.
	var nextVer int
	row := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version_number), 0) + 1 FROM asset_versions WHERE asset_id = ?`,
		a.ArtifactID,
	)
	if err := row.Scan(&nextVer); err != nil {
		return 0, fmt.Errorf("asset finalizer: compute next version for %s: %w", a.ArtifactID, err)
	}

	_, err := tx.ExecContext(ctx, `
		INSERT INTO asset_versions
			(asset_id, version_number, source_uri, legacy_file_md5, file_size_bytes, mime_type, metadata_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		a.ArtifactID,
		nextVer,
		a.Location.FileID, // source_uri — where this version came from
		a.SHA256,
		a.SizeBytes,
		a.MIMEType,
		"{}",
		nowStr,
	)
	if err != nil {
		return 0, fmt.Errorf("asset finalizer: insert version %d for %s: %w", nextVer, a.ArtifactID, err)
	}

	return nextVer, nil
}
