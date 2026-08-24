// Package assets — clip list/count SQL queries (Wave C: moved from
// internal/kernel/asset/clips_list.go).
//
// After Wave C, the source `internal/kernel/asset/clips_list.go` is
// deleted (no types reside in it). The filesystem scanner
// (ScanDirectory + MediaFile) was migrated to
// `internal/infrastructure/files/scanner.go` in PR 3 of Blocco 1.
package imagesregistry

import (
	"context"
	"database/sql"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── SQL receivers (migrated from clips_list.go) ──────────────────────

// ListClips returns clips for a source (or all sources when source is
// empty / "all" / "unified"). No pagination — callers needing paged
// reads should use ListClipsPaged instead.
func (s *AssetStoreSQLite) ListClips(ctx context.Context, source string) ([]*asset.Asset, error) {
	query := buildMediaAssetQuery(source)
	args := []any{}
	if source != "" && source != "all" && source != "unified" {
		args = append(args, source)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*asset.Asset
	for rows.Next() {
		clip, err := ScanCanonicalAssetRowsPublic(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// ListClipsPaged returns clips with pagination and optional search.
// If q is non-empty, performs a search via SearchClips and ignores the
// pagination input (result still capped at the configured limit).
func (s *AssetStoreSQLite) ListClipsPaged(ctx context.Context, source string, limit, offset int, q string) ([]*asset.Asset, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 10000 {
		limit = 10000
	}
	if offset < 0 {
		offset = 0
	}

	if strings.TrimSpace(q) != "" {
		return s.SearchClips(ctx, source, q)
	}

	query := buildMediaAssetQuery(source) + " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	args := []any{}
	if source != "" && source != "all" && source != "unified" {
		args = append(args, source)
	}
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*asset.Asset
	for rows.Next() {
		clip, err := ScanCanonicalAssetRowsPublic(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// CountClips returns the total number of clips (excluding
// soft-deleted).
func (s *AssetStoreSQLite) CountClips(ctx context.Context) (int, error) {
	row := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM media_assets WHERE "+SoftDeleteFilter())
	var count int
	err := row.Scan(&count)
	return count, err
}

// LastUpdatedAtForTerm returns the most recent created_at value for
// clips matching a term. Uses LIKE search on tags (artlist source
// only).
func (s *AssetStoreSQLite) LastUpdatedAtForTerm(ctx context.Context, term string) (*string, error) {
	term = strings.TrimSpace(term)

	var lastUpdated sql.NullString
	row := s.db.QueryRowContext(ctx, `
		SELECT MAX(created_at)
		FROM media_assets
		WHERE source = 'artlist' AND tags LIKE ?
	`, "%"+term+"%")

	if err := row.Scan(&lastUpdated); err != nil {
		return nil, err
	}
	if !lastUpdated.Valid || strings.TrimSpace(lastUpdated.String) == "" {
		return nil, nil
	}
	return &lastUpdated.String, nil
}
