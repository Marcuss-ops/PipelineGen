// Package app — graceful shutdown (PR4: takes *ComposeRoot).
//
// Before PR4 this file took *services + *backgroundJobs. After PR4 it takes
// *ComposeRoot + the backgroundJobs handle returned from
// lifecycle.go::startBackgroundJobs. The body is structurally identical
// (LIFO orchestration, 100ms settle, goroutine Stop with timeout, DB close)
// but consumes the new types.
package app

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// buildCleanup constructs a cleanup function that stops background jobs,
// waits for graceful shutdown, and closes the database.
//
// PR4a: Takes the assembled *ComposeRoot + the backgroundJobs handle +
// cancel + log. The signature replaces the previous
// `buildCleanup(dbs *databases, jobs *backgroundJobs, cancel context.CancelFunc, log *zap.Logger)`.
//
// Orchestration order (LIFO):
//   1. cancel() — signals all goroutines
//   2. settle 100ms — give goroutines time to notice
//   3. parallel Stop for: channelMonitor, driveSyncSchedule
//   4. wait (5-second timeout)
//   5. close main database
func buildCleanup(dbs *databases, root *ComposeRoot, jobs *backgroundJobs, cancel context.CancelFunc, log *zap.Logger) CleanupFunc {
	_ = root // placeholder: future per-bundle teardown hooks (Outbox pool stop, etc.) live here
	return func() {
		// 1. Cancel parent context to signal all background jobs to stop
		if cancel != nil {
			cancel()
		}

		// 2. Give jobs a moment to stop
		time.Sleep(100 * time.Millisecond)

	// 3. Stop services in parallel
	var wg sync.WaitGroup

	if jobs != nil && jobs.channelMonitor != nil {
		wg.Add(1)
		concurrent.SafeGo("cleanup-channel-monitor", func() {
			defer wg.Done()
			jobs.channelMonitor.Stop()
		})
	}
	if jobs != nil && jobs.driveSyncSchedule != nil {
		wg.Add(1)
		concurrent.SafeGo("cleanup-drive-sync", func() {
			defer wg.Done()
			jobs.driveSyncSchedule.Stop()
		})
	}
	// PR4.E-followup-2: explicit Stop for the outbox-events pool started
	// in lifecycle.go. We do NOT rely on outboxevents.Pool's internal
	// ctx.Done handling so in-flight work is drained gracefully even if
	// the pool implementation loses that behaviour in the future.
	//
	// Timeout strategy: the outer wg.Wait has a 5 s budget. We pass a
	// 4 s timeout to eventsPool.Stop so the inner drain can complete
	// (with up to ~1 s margin to the outer cap) and we still benefit
	// from an early return if the pool drains faster. Picking 15 s here
	// would be unreachable — the outer cap would always fire first.
	if root != nil && root.Outbox != nil && root.Outbox.EventsPool != nil {
		const eventsPoolStopTimeout = 4 * time.Second
		wg.Add(1)
		concurrent.SafeGo("cleanup-outbox-events-pool", func() {
			defer wg.Done()
			if err := root.Outbox.EventsPool.Stop(eventsPoolStopTimeout); err != nil {
				log.Warn("outbox events pool stop returned error", zap.Error(err))
			}
		})
	}

		// 4. Wait for all stop operations with timeout
		done := make(chan struct{})
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Error("panic in cleanup wait goroutine", zap.Any("recover", r))
				}
				close(done)
			}()
			wg.Wait()
		}()
		select {
		case <-done:
			log.Info("All background jobs stopped")
		case <-time.After(5 * time.Second):
			log.Warn("Timeout waiting for background jobs to stop")
		}

		// 5. Close database connection
		if dbs.main != nil {
			if err := dbs.main.Close(); err != nil {
				log.Error("Failed to close main database", zap.Error(err))
			}
		}
	}
}
