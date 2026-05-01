package bootstrap

import (
	"go.uber.org/zap"
)

func buildCleanup(dbs *databases, jobs *backgroundJobs, log *zap.Logger) CleanupFunc {
	return func() {
		// Stop services
		if jobs.channelMonitor != nil {
			jobs.channelMonitor.Stop()
		}
		if jobs.stockScheduler != nil {
			jobs.stockScheduler.Stop()
		}
		if jobs.harvesterCronSvc != nil {
			jobs.harvesterCronSvc.Stop()
		}
		if jobs.catalogSyncJob != nil {
			jobs.catalogSyncJob.Stop()
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
	}
}
