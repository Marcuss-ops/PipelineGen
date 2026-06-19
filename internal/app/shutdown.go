package app

import (
	"context"
	"sync"
	"time"
	"go.uber.org/zap"
	concurrent "github.com/Marcuss-ops/PipelineGen/internal/infrastructure"
)

// buildCleanup constructs a cleanup function that stops background jobs,
// waits for graceful shutdown, and closes the database.
func buildCleanup(dbs *databases, jobs *backgroundJobs, cancel context.CancelFunc, log *zap.Logger) CleanupFunc {
	return func() {
		// Cancel context to signal all background jobs to stop
		if cancel != nil {
			cancel()
		}

		// Give jobs a moment to stop
		time.Sleep(100 * time.Millisecond)

		// Stop services
		var wg sync.WaitGroup

		if jobs.channelMonitor != nil {
			wg.Add(1)
			concurrent.SafeGo("cleanup-channel-monitor", func() {
				defer wg.Done()
				jobs.channelMonitor.Stop()
			})
		}
		if jobs.driveSyncSchedule != nil {
			wg.Add(1)
			concurrent.SafeGo("cleanup-drive-sync", func() {
				defer wg.Done()
				jobs.driveSyncSchedule.Stop()
			})
		}

		// Wait for all stop operations with timeout
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

		if dbs.main != nil {
			if err := dbs.main.Close(); err != nil {
				log.Error("Failed to close main database", zap.Error(err))
			}
		}
	}
}
