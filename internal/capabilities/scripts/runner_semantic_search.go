// Package scriptgeneration — runner_semantic_search.go owns the batch
// execution of the semantic production plan against the real retrieval
// provider. It is deliberately a small, optional phase: without a wired
// planner the run keeps its offline plan and pending feedback unchanged.
package scriptgeneration

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// semanticSearchBatchTimeout bounds the whole planned retrieval phase. The
// planner already bounds each query; this is the run-level ceiling that keeps a
// slow provider from consuming the render budget.
const semanticSearchBatchTimeout = 5 * time.Minute

// executeSemanticSearchBatch runs every pending composition of the plan,
// records the measured outcomes and publishes the report on the result. It is
// fail-soft: a provider outage degrades to `skip_visual` feedback (and a
// warning), never to a failed generation.
func (r *Runner) executeSemanticSearchBatch(ctx context.Context, language Language, result *GenerateResult) {
	if r == nil || r.semanticSearchPlanner == nil || result == nil || result.SemanticProductionPlan == nil {
		return
	}
	if result.SemanticProductionPlan.SearchableUnits() == 0 {
		return
	}
	batchCtx, cancel := context.WithTimeout(ctx, semanticSearchBatchTimeout)
	defer cancel()
	report, err := r.semanticSearchPlanner.Run(batchCtx, result.SemanticProductionPlan, string(language))
	if report != nil {
		result.SemanticSearchReport = report
		result.SemanticSearchFeedback = report.Feedback
	}
	if err != nil {
		if r.log != nil {
			r.log.Warn("semantic search batch incomplete; planning feedback records the misses",
				zap.Error(err))
		}
		return
	}
	if r.log != nil {
		r.log.Info("semantic search batch complete",
			zap.Int("queries", report.Metrics.Queries),
			zap.Int("hits", report.Metrics.Hits),
			zap.Int("misses", report.Metrics.Misses),
			zap.Float64("hit_rate", report.Metrics.HitRate),
			zap.Int64("p50_ms", report.Metrics.P50MS),
			zap.Int64("p95_ms", report.Metrics.P95MS),
			zap.Int64("total_ms", report.Metrics.TotalMS))
	}
}
