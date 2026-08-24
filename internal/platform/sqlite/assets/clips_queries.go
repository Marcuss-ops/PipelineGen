package assets

import (
	"context"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── PR1 (June 2026) — file role ───────────────────────────────────────────
//
// clips_queries.go holds the FILTERED query methods on *ClipsRepository:
// the per-call narrowing of the media_assets row set by caller-supplied
// criteria (Source, MediaType, lifecycle State set). The UN-filtered
// metric/aggregation surface stays in clips_repository_queries.go
// (Wave 15, June 2026) where CountAll/CountIndexed/CountIndexable/
// CountPendingOutbox/CountDeadLetter already live.
//
// Distinct semantic boundary (deliberate co-existence):
//   * clips_queries.go here = query-shaped, caller-filtered row count
//   * clips_repository_queries.go (Wave 15) = metric-shaped, no-filter
//     aggregation (CountAll, CountIndexed, ...)
//
// Future PRs that add Count-by-X helpers MUST land in whichever file
// matches the call shape (filtered → here; un-filtered → Wave 15 file).

func (r *ClipsRepository) Count(ctx context.Context, filter asset.Filter) (int64, error) {
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
	query := "SELECT COUNT(*) FROM media_assets WHERE " + strings.Join(conds, " AND ")
	var n int64
	err := r.db.QueryRowContext(ctx, query, args...).Scan(&n)
	return n, err
}
