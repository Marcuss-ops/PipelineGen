package performance

// work_history.go owns the platform adapter for the work-history read
// surface: ListWorkHistory projects measured operations from
// performance_operations (newest first, elapsed_ms > 0) for the Preparation
// Fabric's learned work estimator. It is a pure read projection — it never
// mutates the registry and never re-measures anything.

import (
	"context"
	"fmt"

	capperformance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/performance"
)

var _ capperformance.WorkHistorySource = (*OperationStore)(nil)

// ListWorkHistory returns up to limit measured operations with elapsed_ms > 0,
// newest first (rowid breaks created_at ties in insertion order). It is the
// bootstrap feed that turns the durable performance history into
// expected_work_ms estimates for future jobs. Zero/negative elapsed rows are
// excluded: a measured-0 phase would drag every estimate toward nothing.
func (s *OperationStore) ListWorkHistory(ctx context.Context, limit int) ([]capperformance.WorkHistoryRow, error) {
	if limit <= 0 {
		limit = 1000
	}
	if limit > 5000 {
		limit = 5000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT operation, elapsed_ms, source_duration_ms, source_size_bytes,
			output_size_bytes, width, height, fps, cache_hit, created_at
		FROM performance_operations
		WHERE elapsed_ms > 0
		ORDER BY created_at DESC, rowid DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list performance work history: %w", err)
	}
	defer rows.Close()

	out := []capperformance.WorkHistoryRow{}
	for rows.Next() {
		var (
			row       capperformance.WorkHistoryRow
			cacheHit  int
			createdAt string
		)
		if err := rows.Scan(&row.Operation, &row.ElapsedMS, &row.SourceDurationMS,
			&row.SourceSizeBytes, &row.OutputSizeBytes, &row.Width, &row.Height,
			&row.FPS, &cacheHit, &createdAt); err != nil {
			return nil, fmt.Errorf("scan performance work history: %w", err)
		}
		row.CacheHit = cacheHit == 1
		row.CreatedAt = createdAt
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate performance work history: %w", err)
	}
	return out, nil
}
