package lifecycle

import (
	"context"
	"time"

	"go.uber.org/zap"
)

const (
	staleJobSweepInterval = time.Minute
	staleJobSweepGrace    = 15 * time.Minute
	staleJobSweepReason   = "stale job lease expired (automatic lifecycle sweeper)"
)

// StaleJobSweeperStore is the narrow infrastructure seam used by the
// automatic recovery sweep. The SQLite jobs store implements it today.
type StaleJobSweeperStore interface {
	MarkRunningJobsOlderThanFailed(ctx context.Context, cutoff time.Time, reason string) (int, error)
}

// StartStaleJobSweeper periodically fails jobs whose lease expired well past
// the grace window. This closes the manual-only recovery gap: a dead worker
// no longer leaves a job RUNNING forever waiting for an operator CLI.
func StartStaleJobSweeper(ctx context.Context, store StaleJobSweeperStore, log *zap.Logger) {
	if store == nil {
		if log != nil {
			log.Warn("stale job sweeper not started: jobs store unavailable")
		}
		return
	}
	if log == nil {
		log = zap.NewNop()
	}

	sweep := func() {
		sweepStaleJobs(ctx, store, staleJobSweepGrace, log)
	}
	sweep()

	ticker := time.NewTicker(staleJobSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep()
		}
	}
}

func sweepStaleJobs(ctx context.Context, store StaleJobSweeperStore, grace time.Duration, log *zap.Logger) {
	sweepCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cutoff := time.Now().UTC().Add(-grace)
	n, err := store.MarkRunningJobsOlderThanFailed(sweepCtx, cutoff, staleJobSweepReason)
	if err != nil {
		log.Error("stale job sweep failed", zap.Error(err), zap.Time("cutoff", cutoff))
		return
	}
	if n > 0 {
		log.Warn("stale jobs marked failed", zap.Int("count", n), zap.Time("cutoff", cutoff))
	}
}
