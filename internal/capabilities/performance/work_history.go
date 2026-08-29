package performance

// work_history.go owns the query-side contract that feeds the durable
// performance history (performance_operations) into the Preparation Fabric's
// work estimator. It is the read surface behind the scheduler intelligence
// rule: what the execution layer measured yesterday becomes the
// expected_work_ms of tomorrow's jobs.
//
// One row = one measured operation (elapsed_ms > 0), newest first. The row is
// measurement-shaped (operation name + wall time + workload facts) — it never
// pretends to know preparation unit kinds; the kind mapping lives in the
// consumer (internal/capabilities/jobs).

import "context"

// WorkHistoryRow is one measured operation projected from the durable
// performance history for work estimation. CacheHit is carried because a
// cache-hit execution is measurably cheaper than a cold one; consumers may
// weight or exclude it, but the projection never guesses.
type WorkHistoryRow struct {
	Operation        string
	ElapsedMS        int64
	SourceDurationMS int64
	SourceSizeBytes  int64
	OutputSizeBytes  int64
	Width            int
	Height           int
	FPS              float64
	CacheHit         bool
	CreatedAt        string
}

// WorkHistorySource is the narrow query-side port over performance_operations
// for the preparation fabric's learned work estimator. The concrete adapter is
// the platform OperationStore; an empty history is a valid answer (the
// estimator simply keeps its current state).
type WorkHistorySource interface {
	// ListWorkHistory returns up to limit measured operations with
	// elapsed_ms > 0, newest first. A non-positive limit selects the store
	// default.
	ListWorkHistory(ctx context.Context, limit int) ([]WorkHistoryRow, error)
}
