// Package jobs — worker_lease.go (PR7 split, June 2026).
//
// Lease renewal ticker extracted from worker.go. Owns:
//
//  1. func (w *Worker) renewLeaseLoop — ticker-driven loop that
//     periodically calls w.repo.RenewLease(ctx, jobID, w.id,
//     w.leaseTTL). Returns early on stop-channel close or ctx
//     cancellation; signals completion by closing the done channel.
//
// ACQUIRE/RELEASE: lease acquire is the broker path (ClaimNext at the
// top of worker.go::Start's poll loop) and lease release is the
// finalisation hooks inside worker_execution.go::runJob (ScheduleRetry
// / Complete / Fail / DeadLetter all accept leaseID + revision as
// parameters and atomically transition the row out of running state,
// implicitly releasing the lease as part of the SQL UPDATE). So the
// "acquire/renew/release" lifecycle spans worker.go (acquire via
// ClaimNext), worker_lease.go (renew here), and worker_execution.go
// (release implicitly in the finalisation hooks). NO new abstraction
// is added — the renew ticker is the ONLY piece that lives full-time
// while a job is executing.
//
// Mechanical split, zero behavior change. ONLY relocated + import-redistributed.
package jobs

import (
	"context"
	"time"

	"go.uber.org/zap"
)

func (w *Worker) renewLeaseLoop(ctx context.Context, jobID string, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	interval := w.leaseTTL / 3
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.repo.RenewLease(ctx, jobID, w.id, w.leaseTTL); err != nil {
				w.log.Warn("failed to renew lease", zap.String("job_id", jobID), zap.Error(err))
			}
		}
	}
}
