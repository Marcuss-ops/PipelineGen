// Package assets — clip list/count SQL queries (Wave C: moved from
// internal/kernel/asset/clips_list.go).
//
// After Wave C, the source `internal/kernel/asset/clips_list.go` is
// deleted (no types reside in it). The filesystem scanner
// (ScanDirectory + MediaFile) was migrated to
// `internal/platform/filesystem/scanner.go` in PR 3 of Blocco 1.
package imagesregistry

import (
	"context"
	"database/sql"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── SQL receivers (migrated from clips_list.go) ──────────────────────

// ListClips returns clips for a source (or all sources when source is
// empty / "all" / "unified"). No pagination — the paged read variant,
// ListClipsPaged, was DELETED on 2026-09-16 (P2-9): it was the operational
// text-search reader (its search branch called SearchClips -> SearchByTerms ->
// clip_search_terms) and the clips API now reads the PostgreSQL media SSOT.
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
