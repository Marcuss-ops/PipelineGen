package media

import (
	"context"
	"fmt"
	"strings"
)

// ClipNameSearchResult is the narrow PostgreSQL projection used by the
// lightweight script clip-discovery endpoint. MediaSearcher remains the
// single media read authority; callers never query a SQLite media mirror.
type ClipNameSearchResult struct {
	AssetID     string
	Name        string
	Source      string
	DriveFileID string
}

// SearchClipsByName performs a bounded lexical lookup against the canonical
// media_assets table. Only searchable video rows participate, matching the
// semantic media read lifecycle contract.
func (s *MediaSearcher) SearchClipsByName(ctx context.Context, query string, limit int) ([]ClipNameSearchResult, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("postgres media searcher: name search not wired")
	}
	needle := strings.ToLower(strings.TrimSpace(query))
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, COALESCE(name, ''), COALESCE(source, ''), COALESCE(drive_file_id, '')
		FROM media_assets
		WHERE LOWER(name) LIKE $1
		  AND media_type IN ('video', 'clip')
		  AND lifecycle_state IN ('ACTIVE', 'INDEXED', 'READY', 'PUBLISHED')
		ORDER BY name
		LIMIT $2
	`, "%"+needle+"%", limit)
	if err != nil {
		return nil, fmt.Errorf("postgres media searcher: name search: %w", err)
	}
	defer rows.Close()

	out := make([]ClipNameSearchResult, 0, limit)
	for rows.Next() {
		var hit ClipNameSearchResult
		if err := rows.Scan(&hit.AssetID, &hit.Name, &hit.Source, &hit.DriveFileID); err != nil {
			return nil, fmt.Errorf("postgres media searcher: scan name result: %w", err)
		}
		out = append(out, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres media searcher: iterate name results: %w", err)
	}
	return out, nil
}
