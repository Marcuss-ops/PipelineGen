package imagesregistry

import (
	"context"
	"database/sql"
	"fmt"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// ── PR1 (June 2026) — file role ───────────────────────────────────────────
//
// clips_resolution.go holds the *ClipsRepository typed-lookup methods
// consumed by internal/application/scripts/adapters/clip_resolver.go
// (the ports.ClipResolver adapter) plus the two legacy-delegate
// wrappers (GetByDriveFileID, GetClipFolderByVideoID) that route to
// the embedded AssetStoreSQLite and the canonical folders helper.
// Counts live in clips_queries.go / clips_statistics.go / Wave 15
// clips_repository_queries.go. SetIndexState and SoftDelete live
// in clips_index_state.go. Tx-scoped mutations live in
// clips_transactions.go.

// ── Typed resolver methods ──────────────────────────────────────────────
//
// ResolveByMediaAssetID / ResolveByYouTubeVideoID / ResolveByDriveFileID
// / ResolveByExternalProviderID are the canonical typed DB lookups
// consumed by internal/application/scripts/adapters/clip_resolver.go
// (the ports.ClipResolver adapter). They replace the legacy
// clip_source_builder heuristic "try GetClip, then fall back to
// GetByDriveFileID" with EXPLICIT per-ReferenceType dispatch:
//
//   - media_asset_id       → ResolveByMediaAssetID        (returns 0..1)
//   - youtube_video_id     → ResolveByYouTubeVideoID      (LIKE yt_<videoID>_% fan-out)
//   - drive_file_id        → ResolveByDriveFileID         (exact match, 0..N)
//   - external_provider_id → ResolveByExternalProviderID  (per-provider routing)
//
// All four apply lifecycle_state SoftDeleteFilter so deleted assets
// never surface as resolved evidence (consistent with the
// SoftDelete audit contract from migration 052 + the canonical
// SoftDeleteFilter() helper). Each returns (nil, nil)/(empty, nil)
// for "not found" — NEVER a fake match — and propagates real DB
// errors unchanged. Compile-time pin lives next to the adapter.
// ResolveByMediaAssetID looks up the canonical media_assets row
// by its primary key. Mirrors r.Get but is the typed surface for
// the ports.ClipResolver adapter — distinguishing it from Get so
// a future signature change (extra fields, audit stamping) only
// ripples to the new path. Returns (nil, nil) on sql.ErrNoRows.
func (r *ClipsRepository) ResolveByMediaAssetID(ctx context.Context, id string) (*asset.Asset, error) {
	if id == "" {
		return nil, nil
	}
	row := r.db.QueryRowContext(ctx,
		"SELECT "+MediaAssetColumns+" FROM media_assets WHERE id = ? AND "+r.SoftDeleteFilter()+" LIMIT 1",
		id)
	a, err := ScanCanonicalAssetRowPublic(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("clips.ResolveByMediaAssetID(%q): %w", id, err)
	}
	return a, nil
}

// ResolveByYouTubeVideoID expands a YouTube video id into all
// media_assets rows whose id starts with `yt_<videoID>_`. Convention:
// each YouTube ingest segment is persisted with id =
// `yt_<videoID>_<start>_<n>` so a single video id fans out to N
// rows. The resolver returns the full fan-out — the caller decides
// which subset to bind.
//
// Empty videoID is a noop (returns nil, nil); LIKE wildcards in the
// input are intentionally NOT escaped at this layer — YouTube video
// ids have a fixed 11-character base64url alphabet that does not
// contain LIKE metacharacters (%/_), so a YtVID with a wildcard
// would already be malformed upstream and the canonical pattern
// misuse is unreachable from API input.
func (r *ClipsRepository) ResolveByYouTubeVideoID(ctx context.Context, videoID string) ([]*asset.Asset, error) {
	if videoID == "" {
		return nil, nil
	}
	pattern := "yt_" + videoID + "_%"
	rows, err := r.db.QueryContext(ctx,
		"SELECT "+MediaAssetColumns+" FROM media_assets WHERE id LIKE ? AND "+r.SoftDeleteFilter()+" ORDER BY id ASC",
		pattern)
	if err != nil {
		return nil, fmt.Errorf("clips.ResolveByYouTubeVideoID(%q): %w", videoID, err)
	}
	defer rows.Close()
	out := make([]*asset.Asset, 0)
	for rows.Next() {
		a, err := ScanCanonicalAssetRowsPublic(rows)
		if err != nil {
			return nil, fmt.Errorf("clips.ResolveByYouTubeVideoID scan: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ResolveByDriveFileID matches media_assets.drive_file_id exactly.
// In production today drive_file_id is unique per row (ingest
// dedupes before insert), but the resolver returns []Asset rather
// than *Asset so a future ingest dedup regression surfaces as "N
// results" instead of silent first-row wins. Returns ([]Asset{}, nil)
// on no match — distinct from the (nil, nil) of MediaAssetID.
func (r *ClipsRepository) ResolveByDriveFileID(ctx context.Context, fileID string) ([]*asset.Asset, error) {
	if fileID == "" {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx,
		"SELECT "+MediaAssetColumns+" FROM media_assets WHERE drive_file_id = ? AND "+r.SoftDeleteFilter()+" ORDER BY created_at ASC",
		fileID)
	if err != nil {
		return nil, fmt.Errorf("clips.ResolveByDriveFileID(%q): %w", fileID, err)
	}
	defer rows.Close()
	out := make([]*asset.Asset, 0)
	for rows.Next() {
		a, err := ScanCanonicalAssetRowsPublic(rows)
		if err != nil {
			return nil, fmt.Errorf("clips.ResolveByDriveFileID scan: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ResolveByExternalProviderID matches by provider + external_id.
// The provider switch mirrors the canonical pattern in
// assetRepositoryAdapter.FindByExternalRef (repo_queries.go):
// google_drive → drive_file_id; everything else →
// json_extract(metadata_json, '$.external_id') with source filter.
//
// Empty provider or external_id is a noop. The fan-out shape
// matches ResolveByDriveFileID for consistency.
func (r *ClipsRepository) ResolveByExternalProviderID(ctx context.Context, provider, externalID string) ([]*asset.Asset, error) {
	if provider == "" || externalID == "" {
		return nil, nil
	}
	var q string
	var args []any
	if provider == "google_drive" {
		q = "SELECT " + MediaAssetColumns + " FROM media_assets WHERE drive_file_id = ? AND " + r.SoftDeleteFilter() + " ORDER BY created_at ASC"
		args = []any{externalID}
	} else {
		q = "SELECT " + MediaAssetColumns + " FROM media_assets WHERE source = ? AND json_extract(COALESCE(metadata_json,'{}'), '$.external_id') = ? AND " + r.SoftDeleteFilter() + " ORDER BY created_at ASC"
		args = []any{provider, externalID}
	}
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("clips.ResolveByExternalProviderID(%q, %q): %w", provider, externalID, err)
	}
	defer rows.Close()
	out := make([]*asset.Asset, 0)
	for rows.Next() {
		a, err := ScanCanonicalAssetRowsPublic(rows)
		if err != nil {
			return nil, fmt.Errorf("clips.ResolveByExternalProviderID scan: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *ClipsRepository) GetByDriveFileID(ctx context.Context, fileID string) (*asset.Asset, error) {
	return r.GetClipByDriveFileID(ctx, fileID)
}

func (r *ClipsRepository) GetClipFolderByVideoID(ctx context.Context, videoID string) (*detail.ClipFolder, error) {
	return r.GetFolderByVideoID(ctx, videoID)
}
