// Package jobs — worker_metrics.go (PR7 split, June 2026).
//
// Metric refresher + counters' scheduler extracted from worker.go.
// Owns:
//
//  1. type MetricRefresher interface — the per-job-store contract for
//     refreshing the Prometheus counters served by metrics.RR_*
//     gauges. Satisfied by the concrete *jobs.Repository (defined in
//     internal/infrastructure/database/sqlite/jobs).
//  2. func StartMetricsRefresher — package-level free function that
//     launches a goroutine refreshing metrics on a ticker. The
//     Worker's poll loop also bumps individual counters (e.g.
//     metrics.WorkerIdleTicksTotal) but those are inline at the call
//     sites; this file owns the OUT-OF-BAND refresh loop.
//
// Mechanical split, zero behavior change. ONLY relocated + import-redistributed.
package jobs

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// MetricRefresher is satisfied by the concrete jobs.Repository.
type MetricRefresher interface {
	RefreshMetrics(ctx context.Context) error
}

func StartMetricsRefresher(ctx context.Context, repo MetricRefresher, interval time.Duration, log *zap.Logger) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	go func() {
		if err := repo.RefreshMetrics(ctx); err != nil {
			log.Warn("metrics refresh failed (immediate tick)", zap.Error(err))
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := repo.RefreshMetrics(ctx); err != nil {
					log.Warn("metrics refresh failed", zap.Error(err))
				}
			}
		}
	}()
}
