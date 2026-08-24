package imagesregistry

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

func (r *ClipsRepository) CountAll(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM media_assets WHERE "+r.SoftDeleteFilter()).Scan(&n)
	return n, err
}

func (r *ClipsRepository) CountIndexed(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_assets WHERE `+r.SoftDeleteFilter()+`
		   AND embedding_json IS NOT NULL AND embedding_json != '' AND embedding_json != '[]'`).Scan(&n)
	return n, err
}

// CountIndexable returns the count of assets eligible for indexing —
// those in DISCOVERED, INDEX_PENDING, or INDEXING state, OR already
// indexed (have embeddings). Used by IndexHealth diagnostics.
func (r *ClipsRepository) CountIndexable(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_assets WHERE `+r.SoftDeleteFilter()+`
		   AND (index_state IN ('DISCOVERED','INDEX_PENDING','INDEXING')
		     OR (embedding_json IS NOT NULL AND embedding_json != '' AND embedding_json != '[]'))`).Scan(&n)
	return n, err
}

// CountPendingOutbox returns the count of outbox events in 'pending' status.
// Delegates to outboxevents.Repository.CountByStatus when wired.
func (r *ClipsRepository) CountPendingOutbox(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM outbox_events WHERE status = 'pending'").Scan(&n)
	return n, err
}

// CountDeadLetter returns the count of outbox events in 'dead_letter' status.
// Delegates to outboxevents.Repository.CountByStatus when wired.
func (r *ClipsRepository) CountDeadLetter(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM outbox_events WHERE status = 'dead_letter'").Scan(&n)
	return n, err
}

func (r *ClipsRepository) ListIndexedIDs(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 {
		return []string{}, nil
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id FROM media_assets WHERE `+r.SoftDeleteFilter()+`
		   AND embedding_json IS NOT NULL AND embedding_json != '' AND embedding_json != '[]' LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (r *ClipsRepository) List(ctx context.Context, filter asset.Filter) ([]*asset.Asset, error) {
	args := []any{}
	conds := []string{"1=1"}
	if filter.Source != "" {
		conds = append(conds, "source = ?")
		args = append(args, filter.Source)
	}
	if filter.MediaType != "" {
		conds = append(conds, "media_type = ?")
		args = append(args, filter.MediaType)
	}
	if len(filter.States) > 0 {
		conds = append(conds, inClause(len(filter.States), "lifecycle_state"))
		for _, s := range filter.States {
			args = append(args, s)
		}
	}
	if len(filter.IDs) > 0 {
		conds = append(conds, inClause(len(filter.IDs), "id"))
		for _, id := range filter.IDs {
			args = append(args, id)
		}
	}
	if len(filter.ExcludeIDs) > 0 {
		conds = append(conds, inClause(len(filter.ExcludeIDs), "id", "NOT"))
		for _, id := range filter.ExcludeIDs {
			args = append(args, id)
		}
	}
	if filter.IsFolder != nil {
		conds = append(conds, "is_folder = ?")
		isFolderInt := 0
		if *filter.IsFolder {
			isFolderInt = 1
		}
		args = append(args, isFolderInt)
	}
	// QDRANT-001 (June 2026) — workspace_id SQL isolation. The
	// teardown mirrors the documented Filter contract: empty
	// WorkspaceID = no filter (legacy / internal admin tooling),
	// IsAdmin=true = bypass, otherwise enforce `workspace_id = ?`.
	// The Go-level guard keeps the query plan simple and lets us log
	// which branch was taken without an `OR` in the WHERE clause.
	if filter.WorkspaceID != "" && !filter.IsAdmin {
		conds = append(conds, "workspace_id = ?")
		args = append(args, filter.WorkspaceID)
	}

	query := "SELECT " + MediaAssetColumns + " FROM media_assets WHERE " +
		strings.Join(conds, " AND ") + " ORDER BY created_at DESC"
	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
		if filter.Offset > 0 {
			query += " OFFSET ?"
			args = append(args, filter.Offset)
		}
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("clips.List: %w", err)
	}
	defer rows.Close()

	var out []*asset.Asset
	for rows.Next() {
		m, err := asset.ScanCanonicalAssetRowsPublic(rows)
		if err != nil {
			return nil, fmt.Errorf("clips.List scan: %w", err)
		}
		out = append(out, m)
	}
	return out, nil
}

func (r *ClipsRepository) StreamAssetIDs(ctx context.Context, pageSize int, onPage func([]string) error) error {
	if pageSize <= 0 {
		pageSize = 1000
	}
	offset := 0
	for {
		rows, err := r.db.QueryContext(ctx, `SELECT id FROM media_assets LIMIT ? OFFSET ?`, pageSize, offset)
		if err != nil {
			return fmt.Errorf("stream asset ids (limit=%d, offset=%d): %w", pageSize, offset, err)
		}
		batch := make([]string, 0, pageSize)
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return fmt.Errorf("scan asset id at offset %d: %w", offset, err)
			}
			batch = append(batch, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate asset ids: %w", err)
		}
		if len(batch) == 0 {
			return nil
		}
		if err := onPage(batch); err != nil {
			return err
		}
		if len(batch) < pageSize {
			return nil
		}
		offset += pageSize
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

func inClause(n int, col string, notOpt ...string) string {
	if n <= 0 {
		return "1=1"
	}
	op := "IN"
	if len(notOpt) > 0 && strings.EqualFold(notOpt[0], "NOT") {
		op = "NOT IN"
	}
	placeholders := make([]string, n)
	for i := 0; i < n; i++ {
		placeholders[i] = "?"
	}
	return col + " " + op + " (" + strings.Join(placeholders, ",") + ")"
}

type AdvancedSearchRequest = asset.AdvancedSearchRequest
type AdvancedSearchResult = asset.AdvancedSearchResult
type SegmentEmbeddingRecord = asset.SegmentEmbeddingRecord
