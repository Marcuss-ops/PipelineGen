// Package finalizer — asset_finalizer_renditions.go (split from
// asset_finalizer_tx.go, July 2026): helper SQL for the
// asset_renditions + per-rendition asset_locations rows.
//
// Owns:
//
//  1. func (s *AssetTxFinalizer) upsertRenditionLocation — persist
//     one rendition as TWO coupled rows inside the caller's tx:
//     a) asset_locations (location_kind = the canonical physical
//     location kind, is_primary=0). The database contract permits only
//     local, drive, and object_storage; rendition kind belongs in
//     asset_renditions.kind, not in location_kind.
//     b) asset_renditions (ON CONFLICT(asset_id, kind) DO UPDATE),
//     the canonical storage for technical variant metadata
//     (container/codec/width/height/fps/bitrate/sha256/
//     size_bytes).
//
// The asset_locations row carries the URI; the asset_renditions
// row carries the codec + dimensions. They are joined by FK
// (asset_renditions.location_id → asset_locations.id). Because
// LastInsertId is unreliable on an ON CONFLICT path, the
// location_id is re-read via QueryRowContext on the unique key.
//
// Caller-owned-tx discipline (godlike/06 SSOT, non-negotiable
// architectural rule): same as sibling helpers. Does NOT own
// BeginTx.
//
// Skip semantics (godlike/07 fail-closed): a rendition with
// empty URI is a no-op (no asset_locations or asset_renditions
// row written) — the caller can supply empty renditions to
// opt-out per-rendition persistence without leaking partial
// state into the canonical tables.
//
// Mechanical split from asset_finalizer_tx.go. Zero behavior
// change. The receiver (s *AssetTxFinalizer) is unchanged so the
// orchestrator can call this helper as `s.upsertRenditionLocation(...)`
// without any wiring change.
package finalizer

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

// upsertRenditionLocation persists a single rendition as an
// asset_locations row and a matching asset_renditions row. The
// location is NOT marked as primary — the primary location is the
// one carried by PublishedArtifact.Location (written by
// asset_finalizer_locations.go).
func (s *AssetTxFinalizer) upsertRenditionLocation(
	ctx context.Context,
	tx finalization.Transaction,
	a *finalization.PublishedArtifact,
	r *finalization.AssetRenditionLocation,
	nowStr string,
) error {
	if r.URI == "" {
		return nil
	}
	committer, ok := s.committer.(persistence.AssetRenditionCommitter)
	if !ok || committer == nil {
		return fmt.Errorf("asset finalizer: canonical rendition committer is required")
	}
	sqlTx, ok := UnwrapSQLTx(tx)
	if !ok || sqlTx == nil {
		return fmt.Errorf("asset finalizer: rendition transaction is not a *sql.Tx")
	}
	return committer.CommitRenditionTx(ctx, sqlTx, a.ArtifactID, persistence.RenditionCommit{
		Kind:        r.Kind,
		Provider:    r.Provider,
		FileID:      r.FileID,
		URI:         r.URI,
		WebViewLink: r.WebViewLink,
		DownloadURL: r.DownloadLink,
		MimeType:    r.MimeType,
		SizeBytes:   r.SizeBytes,
		SHA256:      r.LegacyFileMD5,
		Width:       r.Width,
		Height:      r.Height,
		FPS:         r.FPS,
		Bitrate:     r.Bitrate,
		Container:   r.Container,
		Codec:       r.Codec,
	}, nowStr)
}
