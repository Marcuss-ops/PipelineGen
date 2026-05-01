package cron

import (
	"context"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/service/catalogsync"
)

type CatalogSyncJob struct {
	service *catalogsync.Service
	log     *zap.Logger
	stopCh  chan struct{}
}

func NewCatalogSyncJob(service *catalogsync.Service, log *zap.Logger) *CatalogSyncJob {
	return &CatalogSyncJob{
		service: service,
		log:     log,
		stopCh:  make(chan struct{}),
	}
}

func (j *CatalogSyncJob) Start(ctx context.Context, interval time.Duration) {
	if j.service == nil {
		j.log.Info("catalog sync job disabled: service not configured")
		return
	}
	if interval <= 0 {
		interval = 6 * time.Hour
	}

	j.log.Info("Starting catalog sync job", zap.Duration("interval", interval))
	j.run(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			j.run(ctx)
		case <-j.stopCh:
			j.log.Info("Catalog sync job stopped")
			return
		case <-ctx.Done():
			j.log.Info("Catalog sync job stopped via context")
			return
		}
	}
}

func (j *CatalogSyncJob) Stop() {
	close(j.stopCh)
}

func (j *CatalogSyncJob) run(ctx context.Context) {
	started := time.Now()
	summary, err := j.service.SyncAll(ctx)
	if err != nil {
		j.log.Error("catalog sync failed", zap.Error(err))
		return
	}
	j.log.Info("catalog sync completed",
		zap.Bool("ok", summary.OK),
		zap.Int("synced", summary.Synced),
		zap.Int("failed", summary.Failed),
		zap.Int("roots", len(summary.Roots)),
		zap.Duration("elapsed", time.Since(started)),
	)
}
