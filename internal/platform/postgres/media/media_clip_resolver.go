package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ResolveByMediaAssetID returns the canonical asset for an id, or (nil, nil)
// when the row is absent or soft-deleted. It mirrors the legacy SQLite
// ClipsRepository.ResolveByMediaAssetID contract so the script clip resolver
// can run against the PostgreSQL media SSOT without behavioural drift.
func (s *MediaSearcher) ResolveByMediaAssetID(ctx context.Context, id string) (*asset.Asset, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("postgres media: media read repository not wired")
	}
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return nil, nil
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+mediaAssetReadColumns+` FROM media_assets WHERE id = $1 AND COALESCE(lifecycle_state,'') <> 'DELETED' LIMIT 1`,
		trimmed)
	rec, err := scanMediaAssetRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres media: ResolveByMediaAssetID(%q): %w", trimmed, err)
	}
	return rec.HydrateAsset(), nil
}

// ResolveByYouTubeVideoID expands a YouTube video id into every live
// media_assets row whose id starts with `yt_<videoID>_`. It mirrors the legacy
// SQLite ClipsRepository.ResolveByYouTubeVideoID contract: each YouTube ingest
// segment is persisted with id = `yt_<videoID>_<start>_<n>`, so one video id
// fans out to N rows and the caller picks the subset to bind.
//
// LIKE metacharacters in the input are intentionally NOT escaped, exactly as in
// the retired SQLite form: a YouTube video id has a fixed 11-character
// base64url alphabet that contains no % or _, so a wildcard-bearing id is
// already malformed upstream.
func (s *MediaSearcher) ResolveByYouTubeVideoID(ctx context.Context, videoID string) ([]*asset.Asset, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("postgres media: media read repository not wired")
	}
	trimmed := strings.TrimSpace(videoID)
	if trimmed == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+mediaAssetReadColumns+` FROM media_assets WHERE id LIKE $1 AND COALESCE(lifecycle_state,'') <> 'DELETED' ORDER BY id ASC`,
		"yt_"+trimmed+"_%")
	if err != nil {
		return nil, fmt.Errorf("postgres media: ResolveByYouTubeVideoID(%q): %w", trimmed, err)
	}
	defer rows.Close()
	out := make([]*asset.Asset, 0)
	for rows.Next() {
		rec, err := scanMediaAssetRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres media: ResolveByYouTubeVideoID scan: %w", err)
		}
		out = append(out, rec.HydrateAsset())
	}
	return out, rows.Err()
}

// ResolveByExternalProviderID matches by provider + external_id, mirroring the
// retired SQLite ClipsRepository.ResolveByExternalProviderID contract: the
// google_drive provider resolves through the canonical drive_file_id column,
// every other provider through source + the metadata external_id projection.
//
// The metadata projection uses the same NULLIF(metadata_json,”)::jsonb cast as
// the other PostgreSQL media readers, guarded only against the empty-string
// NOT NULL default (which is not valid JSON).
func (s *MediaSearcher) ResolveByExternalProviderID(ctx context.Context, provider, externalID string) ([]*asset.Asset, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("postgres media: media read repository not wired")
	}
	provider = strings.TrimSpace(provider)
	externalID = strings.TrimSpace(externalID)
	if provider == "" || externalID == "" {
		return nil, nil
	}
	query := `SELECT ` + mediaAssetReadColumns + ` FROM media_assets WHERE drive_file_id = $1 AND COALESCE(lifecycle_state,'') <> 'DELETED' ORDER BY created_at ASC`
	args := []any{externalID}
	if provider != "google_drive" {
		query = `SELECT ` + mediaAssetReadColumns + ` FROM media_assets WHERE source = $1 AND (NULLIF(metadata_json, '')::jsonb)->>'external_id' = $2 AND COALESCE(lifecycle_state,'') <> 'DELETED' ORDER BY created_at ASC`
		args = []any{provider, externalID}
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres media: ResolveByExternalProviderID(%q, %q): %w", provider, externalID, err)
	}
	defer rows.Close()
	out := make([]*asset.Asset, 0)
	for rows.Next() {
		rec, err := scanMediaAssetRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres media: ResolveByExternalProviderID scan: %w", err)
		}
		out = append(out, rec.HydrateAsset())
	}
	return out, rows.Err()
}

// ResolveByDriveFileID expands a Drive file id into every live
// media_assets row that references it, ordered by creation time. It mirrors
// the legacy SQLite ClipsRepository.ResolveByDriveFileID contract.
func (s *MediaSearcher) ResolveByDriveFileID(ctx context.Context, fileID string) ([]*asset.Asset, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("postgres media: media read repository not wired")
	}
	trimmed := strings.TrimSpace(fileID)
	if trimmed == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+mediaAssetReadColumns+` FROM media_assets WHERE drive_file_id = $1 AND COALESCE(lifecycle_state,'') <> 'DELETED' ORDER BY created_at ASC`,
		trimmed)
	if err != nil {
		return nil, fmt.Errorf("postgres media: ResolveByDriveFileID(%q): %w", trimmed, err)
	}
	defer rows.Close()
	out := make([]*asset.Asset, 0)
	for rows.Next() {
		rec, err := scanMediaAssetRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres media: ResolveByDriveFileID scan: %w", err)
		}
		out = append(out, rec.HydrateAsset())
	}
	return out, rows.Err()
}
