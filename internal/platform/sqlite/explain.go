// Package sqlite — explain.go (FASE 6 Cut 6.2, July 2026).
//
// Startup-only EXPLAIN QUERY PLAN instrumentation for the two
// canonical aggregation queries:
//
//   - ListAwaitingAggregation (parent-consumer side; aggregator
//     picks up WAITING_CHILDREN parents whose children's
//     tail-finish state must be reconciled against jobs)
//   - FinalizeAggregateParent (worker-side post-fan-out flip;
//     observed per call from lifecycle_finalize.go::FinalizeAggregateParent)
//
// The user spec ("EXPLAIN QUERY PLAN on jobs_list_awaiting_aggregation
// + parent-child aggregate") calls out these two queries
// explicitly. Both are written by the canonical inner-Kernel+
// SQLiteStore layer; their plans are stable across query invocations
// in WAL-mode SQLite (the index choices do not flip between calls).
//
// The dump runs ONCE at startup via DumpStartupPlans. It does NOT
// run per-query — that would add ~2× query cost to a hot path
// (godlike/07 fail-closed: instrumentation MUST NOT regress the
// hot path it observes).
//
// Failure mode (godlike/07 fail-closed):
//   - EXPLAIN does not match the schema (e.g. due to a forward-pointer
//     migration that adds/removes an index); DumpStartupPlans logs a
//     Warn with the err and continues. Production startup completes
//     even on EXPLAIN drift.
package sqlite

import (
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"context"
	"database/sql"
	"fmt"
	"strings"

	"go.uber.org/zap"
)

// QueryPlanRow mirrors the canonical SQLite EXPLAIN QUERY PLAN row
// shape: id, parent, notused, detail. The notused column is preserved
// as a string to keep the row opaque to schema drift; modern SQLite
// (3.8.x+) returns integer 0 in that slot but pre-3.8 versions used
// it for a `notused` integer field. godlike/07 fail-closed: the row
// is treated structurally — operators grep on Detail, not on the
// column count.
//
// godlike/06 SSOT: this is the canonical projection of an EXPLAIN
// QUERY PLAN row. Dashboards / alert rules that need access to the
// plan MUST consume this struct, not parse the Detail string ad-hoc.
type QueryPlanRow struct {
	ID      int
	Parent  int
	NotUsed string
	Detail  string
}

// ListAwaitingAggregationQueryFragment is the canonical SQL template
// for the parent-consumer aggregation query. The full query lives
// in jobs/repository_jobs_crud.go::ListAwaitingAggregation; this
// fragment mirrors the WHERE + LIMIT surface (sufficient for EXPLAIN
// purposes — the SELECT projection does not affect the plan choice
// because `jobColumns` is a fixed list of indexed columns).
//
// godlike/06 SSOT: the fragment is byte-equivalent with the
// ListAwaitingAggregation query (modulo the SELECT projection).
// Plan drift between this fragment and the production query is a
// drift bug; if the production query adds/removes a filter that
// flips the index choice, this fragment must be updated in tandem.
const ListAwaitingAggregationQueryFragment = `SELECT id FROM jobs
 WHERE type = ?
   AND status IN ('WAITING_CHILDREN','RUNNING','FINALIZING','SUCCEEDED')
   AND (parent_state_typed = 'waiting_children'
        OR (parent_state_typed = '' AND json_extract(result_json, '$.parent_state') = 'waiting_children'))
 ORDER BY created_at DESC
 LIMIT ?`

// FinalizeAggregateParentQueryFragment is the canonical SQL
// template for the worker-side post-fan-out parent flip.
// Mirrors lifecycle_finalize.go::FinalizeAggregateParent (the
// "no-lease CAS" UPDATE).
const FinalizeAggregateParentQueryFragment = `UPDATE jobs
 SET parent_state_typed = ?,
     revision = revision + 1,
     updated_at = ?
 WHERE id = ?
   AND parent_state_typed IN ('waiting_children', 'partial_success')
   AND status IN ('RUNNING', 'FINALIZING', 'SUCCEEDED')`

// startupPlanCatalog is the canonical (name, query) catalog of
// queries whose plan is dumped at startup. Add new entries here
// when a new canonical query lands in the aggregation path.
//
// godlike/06 SSOT: this catalog is the SOLE place that lists the
// "what does the launcher EXPLAIN at startup" surface. Future
// queries that should be explained at startup MUST be added here
// (NOT bypassed with ad-hoc log lines elsewhere).
var startupPlanCatalog = map[string]string{
	"jobs_list_awaiting_aggregation": ListAwaitingAggregationQueryFragment,
	"finalize_aggregate_parent_cas":  FinalizeAggregateParentQueryFragment,
}

// DumpStartupPlans runs EXPLAIN QUERY PLAN once per entry in the
// catalog and logs the plan rows at Info with the query name. Failures
// are logged at Warn (NOT Error — godlike/07: a plan-format drift
// must NOT prevent startup from completing). Called from the
// composition root at boot-time, after DualPool is constructed.
//
// godlike/07 fail-closed: empty pool is a no-op (logged at Warn).
// On a closed pool, the EXPLAIN errors are surfaced verbatim via
// the error log line (operators see the cause-and-effect).
func DumpStartupPlans(ctx context.Context, pool *DualPool, log *zap.Logger) {
	if log == nil {
		log = zap.NewNop()
	}
	if pool == nil || pool.Reader == nil {
		log.Warn("sqlite.DumpStartupPlans: reader pool nil; skipping EXPLAIN instrumentation (operators see zero plan rows on /metrics)")
		return
	}
	for name, query := range startupPlanCatalog {
		plan, err := ExplainQueryPlan(ctx, pool.Reader, query)
		if err != nil {
			log.Warn("sqlite.DumpStartupPlans: EXPLAIN failed",
				zap.String("query_name", name),
				zap.Error(err),
			)
			continue
		}
		log.Info("sqlite.DumpStartupPlans: query plan recorded",
			zap.String("query_name", name),
			zap.Int("row_count", len(plan)),
			zap.String("plan", formatPlanForLog(plan)),
		)
	}
}

// ExplainQueryPlan returns the EXPLAIN QUERY PLAN rows for the
// supplied query as a canonical QueryPlanRow slice. The query is
// passed verbatim to the driver with the EXPLAIN QUERY PLAN prefix.
//
// godlike/07 fail-closed: the row scan tolerates legacy and modern
// SQLite column shapes (notused may be INT or TEXT depending on the
// driver binding); the 4-column canonical shape is preferred but a
// nullable 3-column fallback is honoured.
func ExplainQueryPlan(ctx context.Context, db *sql.DB, query string) ([]QueryPlanRow, error) {
	if db == nil {
		return nil, fmt.Errorf("sqlite.ExplainQueryPlan: db is nil (fail-closed)")
	}
	rows, err := db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+strings.TrimSpace(query))
	if err != nil {
		return nil, fmt.Errorf("sqlite.ExplainQueryPlan: query: %w", err)
	}
	defer rows.Close()

	var plan []QueryPlanRow
	for rows.Next() {
		var r QueryPlanRow
		// Canonical 4-column scan: id, parent, notused, detail.
		// The mattn/go-sqlite3 driver pins 3.16+ shape, but
		// sub-3.16 versions used INTEGER for notused; this scan
		// uses Text-column shape so the type is consistent.
		var notused int64
		if err := rows.Scan(&r.ID, &r.Parent, &notused, &r.Detail); err != nil {
			// Fall back: assume 3-column shape (id, parent, detail)
			// — produced by some pre-driver-bind SQLite variants.
			rows2, err2 := db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+strings.TrimSpace(query))
			if err2 != nil {
				return nil, fmt.Errorf("sqlite.ExplainQueryPlan: 3-col fallback query: %w", err2)
			}
			defer rows2.Close()
			for rows2.Next() {
				var r3 QueryPlanRow
				if err := rows2.Scan(&r3.ID, &r3.Parent, &r3.Detail); err != nil {
					return nil, fmt.Errorf("sqlite.ExplainQueryPlan: 3-col scan: %w", err)
				}
				plan = append(plan, r3)
			}
			return plan, nil
		}
		r.NotUsed = fmt.Sprintf("%d", notused)
		plan = append(plan, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite.ExplainQueryPlan: rows.Err: %w", err)
	}
	return plan, nil
}

// formatPlanForLog joins the plan rows with newlines for a single
// structured-log field. Operators can grep `/metrics`-adjacent
// logs for `plan=...` to see the EXPLAIN QUERY PLAN output without
// re-launching `sqlite3` interactively.
func formatPlanForLog(plan []QueryPlanRow) string {
	if len(plan) == 0 {
		return "(empty)"
	}
	var sb strings.Builder
	for _, row := range plan {
		sb.WriteString(fmt.Sprintf("[id=%d parent=%d detail=%q] ", row.ID, row.Parent, row.Detail))
	}
	return sb.String()
}
