package lifecycle

import (
	"context"
	"time"

	scriptreconciliation "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/reconciliation"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/scripts"
	"go.uber.org/zap"
)

// StartScriptRunReconciliationSweeper repairs old submission runs that were
// persisted before enqueue but never correlated with the durable job row.
// The broker remains authoritative: lookup failures never mutate run state.
func StartScriptRunReconciliationSweeper(ctx context.Context, runs *sqlitescripts.SQLiteRunRepository, jobs scriptreconciliation.JobReader, log *zap.Logger) {
	if runs == nil || jobs == nil {
		if log != nil {
			log.Warn("script run reconciliation sweeper not started: run repository or jobs service is unavailable")
		}
		return
	}
	if log == nil {
		log = zap.NewNop()
	}
	reconciler := scriptreconciliation.Reconciler{Runs: runs, Jobs: jobs, Grace: 15 * time.Minute, Limit: 100}
	interval := time.Minute
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	sweep := func() {
		sweepCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		report, err := reconciler.Reconcile(sweepCtx, time.Now().UTC())
		if err != nil {
			log.Error("script run reconciliation sweep failed", zap.Error(err),
				zap.Int("inspected", report.Inspected), zap.Int("linked", report.Linked),
				zap.Int("completed", report.Completed), zap.Int("failed", report.Failed),
				zap.Int("ambiguous", report.Ambiguous), zap.Int("deferred", report.Deferred))
			return
		}
		if report.Linked > 0 || report.Failed > 0 || report.Ambiguous > 0 {
			log.Info("script run reconciliation sweep completed", zap.Int("inspected", report.Inspected),
				zap.Int("linked", report.Linked), zap.Int("completed", report.Completed),
				zap.Int("failed", report.Failed), zap.Int("ambiguous", report.Ambiguous),
				zap.Int("deferred", report.Deferred))
		}
	}
	sweep()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep()
		}
	}
}
