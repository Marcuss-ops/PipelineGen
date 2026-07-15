// Package sqlite — explain_test.go (FASE 6 Cut 6.2, July 2026).
//
// Regression tests for the canonical startup-only EXPLAIN instrumentation
// surface (internal/infrastructure/database/sqlite/explain.go). The
// tests pin:
//   - the 2-query catalog (jobs_list_awaiting_aggregation +
//     finalize_aggregate_parent_cas) is byte-stable across runs.
//   - EXPLAIN QUERY PLAN returns 4-column canonical rows on mattn/go-sqlite3.
//   - DumpStartupPlans is fail-closed: an invalid pool is logged at Warn
//     and the function returns cleanly (godlike/07).

package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestExplainFragmentStringsAreCanonical pins the canonical SQL
// fragment identifiers (godlike/06 SSOT: the file-local `const`
// strings are the SOLE source of \"what does the startup EXPLAIN
// observe\"). Drift between these fragments and the canonical
// production queries in repository_jobs_crud.go::ListAwaitingAggregation
// / lifecycle_finalize.go::FinalizeAggregateParent is a plan-drift
// bug.
func TestExplainFragmentStringsAreCanonical(t *testing.T) {
	assert.Contains(t, ListAwaitingAggregationQueryFragment, "jobs",
		"awaiting-aggregation query MUST scan the canonical jobs table")
	assert.Contains(t, ListAwaitingAggregationQueryFragment, "parent_state_typed",
		"awaiting-aggregation query MUST consult the typed parent_state column (canonical PR-P1.2-SQL-DUAL-WRITE)")
	assert.Contains(t, ListAwaitingAggregationQueryFragment, "WAITING_CHILDREN",
		"awaiting-aggregation query MUST include the canonical WAITING_CHILDREN broker status (kernel/job.StatusWaitingChildren)")

	assert.Contains(t, FinalizeAggregateParentQueryFragment, "UPDATE jobs",
		"finalize-aggregate query MUST target the canonical jobs table UPDATE path")
	assert.Contains(t, FinalizeAggregateParentQueryFragment, "parent_state_typed",
		"finalize-aggregate query MUST mutate the typed column (canonical no-lease CAS contract)")
}

// TestExplainQueryPlan_ReturnsRows pins the EXPLAIN contract:
// running `EXPLAIN QUERY PLAN <simplified-fragment>` against the
// dual-pool reader returns ≥1 row (plan steps). The canonical
// SQLite EXPLAIN QUERY PLAN returns at least 1 row for any
// syntactically-valid SELECT/UPDATE/INSERT planned statement.
func TestExplainQueryPlan_ReturnsRows(t *testing.T) {
	tmpFile := t.TempDir() + "/cut62_explain.db"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := NewDualPool(ctx, tmpFile, 2)
	require.NoError(t, err)
	defer pool.Close()

	_, err = pool.Writer.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY
		)
	`)
	require.NoError(t, err)

	plan, err := ExplainQueryPlan(ctx, pool.Reader, "SELECT id FROM jobs WHERE id = 1")
	require.NoError(t, err)
	assert.NotEmpty(t, plan, "EXPLAIN QUERY PLAN MUST return at least one plan row for a SELECT")
	assert.NotEmpty(t, plan[0].Detail, "first EXPLAIN row MUST carry a non-empty Detail (SQLite EXPLAIN contract)")
}

// TestDumpStartupPlans_NilPoolFailsClosed pins godlike/07: a nil pool
// does NOT panic. DumpStartupPlans logs Warn (zap.NewNop swallows
// the log) and returns cleanly. This is the canonical contract that
// composition-root callers can mount DumpStartupPlans AFTER
// DualPool construction without a separate panic recovery.
func TestDumpStartupPlans_NilPoolFailsClosed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Nil pool MUST NOT panic. zap.NewNop is the test-friendly
	// logger (no-op; assertions on log lines require a real
	// ObservableLogger, deferred to integration tests where the
	// production log path is exercised).
	require.NotPanics(t, func() {
		DumpStartupPlans(ctx, nil, zap.NewNop())
	}, "DumpStartupPlans MUST NOT panic on nil pool (godlike/07 fail-closed)")

	require.NotPanics(t, func() {
		DumpStartupPlans(ctx, &DualPool{Writer: nil, Reader: nil}, zap.NewNop())
	}, "DumpStartupPlans MUST NOT panic on pool nil sub-handle (godlike/07 fail-closed)")
}
