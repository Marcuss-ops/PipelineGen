package cliprender

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// RenderCachePruner is the PostgreSQL retention adapter for clip_render_cache.
// Deleting a cache row only forces a future cache miss; it never deletes the
// media_asset or its bytes (CR-11).
type RenderCachePruner struct {
	db *sql.DB
}

// NewRenderCachePruner constructs the adapter. A nil DB fails closed at call
// time rather than silently skipping retention.
func NewRenderCachePruner(db *sql.DB) *RenderCachePruner {
	return &RenderCachePruner{db: db}
}

// PruneOlderThan counts (dryRun=true) or deletes cache rows older than cutoff.
// created_at is written by Put as NOW()::text, so the ::timestamptz cast is
// explicit and the comparison does not depend on a lexical prefix.
func (p *RenderCachePruner) PruneOlderThan(ctx context.Context, cutoff time.Time, dryRun bool) (int64, error) {
	if p == nil || p.db == nil {
		return 0, fmt.Errorf("clip.render cache prune: media DB is nil")
	}
	if dryRun {
		var rows int64
		if err := p.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM clip_render_cache WHERE created_at::timestamptz < $1`,
			cutoff.UTC()).Scan(&rows); err != nil {
			return 0, fmt.Errorf("clip.render cache prune: count: %w", err)
		}
		return rows, nil
	}
	res, err := p.db.ExecContext(ctx,
		`DELETE FROM clip_render_cache WHERE created_at::timestamptz < $1`,
		cutoff.UTC())
	if err != nil {
		return 0, fmt.Errorf("clip.render cache prune: delete: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("clip.render cache prune: rows affected: %w", err)
	}
	return rows, nil
}
