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
