package clips

import (
	"context"
	"database/sql"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
)

// ListClips returns clips for a source (or all sources when source is empty
// / "all" / "unified"). No pagination — callers needing paged reads should
// use ListClipsPaged instead.
func (r *Repository) ListClips(ctx context.Context, source string) ([]*models.MediaAsset, error) {
	query := r.buildMediaAssetQuery(source)
	args := []any{}
	if source != "" && source != "all" && source != "unified" {
		args = append(args, source)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.MediaAsset
	for rows.Next() {
		clip, err := scanMediaAssetRows(rows)
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
func (r *Repository) ListClipsPaged(ctx context.Context, source string, limit, offset int, q string) ([]*models.MediaAsset, error) {
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
		return r.SearchClips(ctx, source, q)
	}

	query := r.buildMediaAssetQuery(source) + " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	args := []any{}
	if source != "" && source != "all" && source != "unified" {
		args = append(args, source)
	}
	args = append(args, limit, offset)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.MediaAsset
	for rows.Next() {
		clip, err := scanMediaAssetRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// CountClips returns the total number of clips (excluding soft-deleted).
func (r *Repository) CountClips(ctx context.Context) (int, error) {
	query := "SELECT COUNT(*) FROM media_assets WHERE " + r.SoftDeleteFilter()
	row := r.db.QueryRowContext(ctx, query)
	var count int
	err := row.Scan(&count)
	return count, err
}

// LastUpdatedAtForTerm returns the most recent created_at value for clips
// matching a term. Uses LIKE search on tags (kept narrow — only the
// artlist source tag pattern is searched).
func (r *Repository) LastUpdatedAtForTerm(ctx context.Context, term string) (*string, error) {
	term = strings.TrimSpace(term)

	var lastUpdated sql.NullString
	query := `
		SELECT MAX(created_at)
		FROM media_assets
		WHERE source = 'artlist' AND tags LIKE ?
	`
	row := r.db.QueryRowContext(ctx, query, "%"+term+"%")

	if err := row.Scan(&lastUpdated); err != nil {
		return nil, err
	}
	if !lastUpdated.Valid || strings.TrimSpace(lastUpdated.String) == "" {
		return nil, nil
	}
	return &lastUpdated.String, nil
}
