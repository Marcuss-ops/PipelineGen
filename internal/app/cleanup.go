package app

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
)

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
			go func() {
				defer wg.Done()
				jobs.channelMonitor.Stop()
			}()
		}
		if jobs.driveSyncSchedule != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				jobs.driveSyncSchedule.Stop()
			}()
		}
		// NOTE: harvesterCronSvc and catalogSyncJob removed (cron system eliminated)
		// These should be migrated to the job system

		// Wait for all stop operations with timeout
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()
		select {
		case <-done:
			log.Info("All background jobs stopped")
		case <-time.After(5 * time.Second):
			log.Warn("Timeout waiting for background jobs to stop")
		}

		if dbs.main != nil {
			if err := dbs.main.Backup(); err != nil {
				log.Warn("Failed to create main backup on shutdown", zap.Error(err))
			}
			if err := dbs.main.Close(); err != nil {
				log.Error("Failed to close main database", zap.Error(err))
			}
		}

		if dbs.media != nil {
			if err := dbs.media.Backup(); err != nil {
				log.Warn("Failed to create media backup on shutdown", zap.Error(err))
			}
			if err := dbs.media.Close(); err != nil {
				log.Error("Failed to close media database", zap.Error(err))
			}
		}
		if dbs.jobs != nil {
			if err := dbs.jobs.Backup(); err != nil {
				log.Warn("Failed to create jobs backup on shutdown", zap.Error(err))
			}
			if err := dbs.jobs.Close(); err != nil {
				log.Error("Failed to close jobs database", zap.Error(err))
			}
		}
	}
}
