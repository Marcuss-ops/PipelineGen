package bootstrap

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
		if jobs.stockScheduler != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				jobs.stockScheduler.Stop()
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

		if dbs.images != nil {
			if err := dbs.images.Backup(); err != nil {
				log.Warn("Failed to create images backup on shutdown", zap.Error(err))
			}
			if err := dbs.images.Close(); err != nil {
				log.Error("Failed to close images database", zap.Error(err))
			}
		}
		if dbs.main != nil {
			if err := dbs.main.Backup(); err != nil {
				log.Warn("Failed to create backup on shutdown", zap.Error(err))
			}
			if err := dbs.main.Close(); err != nil {
				log.Error("Failed to close main database", zap.Error(err))
			}
		}
		if dbs.stock != nil {
			if err := dbs.stock.Backup(); err != nil {
				log.Warn("Failed to create stock backup on shutdown", zap.Error(err))
			}
			if err := dbs.stock.Close(); err != nil {
				log.Error("Failed to close stock database", zap.Error(err))
			}
		}
		if dbs.clips != nil {
			if err := dbs.clips.Backup(); err != nil {
				log.Warn("Failed to create clips backup on shutdown", zap.Error(err))
			}
			if err := dbs.clips.Close(); err != nil {
				log.Error("Failed to close clips database", zap.Error(err))
			}
		}
		if dbs.artlist != nil {
			if err := dbs.artlist.Backup(); err != nil {
				log.Warn("Failed to create artlist backup on shutdown", zap.Error(err))
			}
			if err := dbs.artlist.Close(); err != nil {
				log.Error("Failed to close artlist database", zap.Error(err))
			}
		}
		if dbs.voiceover != nil {
			if err := dbs.voiceover.Backup(); err != nil {
				log.Warn("Failed to create voiceover backup on shutdown", zap.Error(err))
			}
			if err := dbs.voiceover.Close(); err != nil {
				log.Error("Failed to close voiceover database", zap.Error(err))
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
