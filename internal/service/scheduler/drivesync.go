package scheduler

import (
	"context"
	"time"

	"go.uber.org/zap"

	"velox/go-master/internal/service/catalogsync"
	imgservice "velox/go-master/internal/service/images"
	"velox/go-master/internal/service/voiceoversync"
)

// DriveSyncScheduler handles periodic Drive synchronization of all asset types.
type DriveSyncScheduler struct {
	catalogSync   *catalogsync.Service
	voiceoverSync *voiceoversync.Service
	imageService  *imgservice.Service
	log           *zap.Logger
	interval      time.Duration
	stopCh        chan struct{}
}

// NewDriveSyncScheduler creates a new Drive sync scheduler.
func NewDriveSyncScheduler(
	catalogSync *catalogsync.Service,
	voiceoverSync *voiceoversync.Service,
	imageService *imgservice.Service,
	log *zap.Logger,
	interval time.Duration,
) *DriveSyncScheduler {
	if interval <= 0 {
		interval = 6 * time.Hour // default every 6 hours
	}
	return &DriveSyncScheduler{
		catalogSync:   catalogSync,
		voiceoverSync: voiceoverSync,
		imageService:  imageService,
		log:           log,
		interval:      interval,
		stopCh:        make(chan struct{}),
	}
}

// Start begins the periodic Drive sync loop.
func (s *DriveSyncScheduler) Start(ctx context.Context) {
	s.log.Info("Starting Drive sync scheduler",
		zap.Duration("interval", s.interval),
		zap.Bool("catalog_enabled", s.catalogSync != nil),
		zap.Bool("voiceover_enabled", s.voiceoverSync != nil),
		zap.Bool("images_enabled", s.imageService != nil),
	)

	if s.catalogSync == nil && s.voiceoverSync == nil && s.imageService == nil {
		s.log.Warn("Drive sync scheduler: no sync services available, skipping")
		return
	}

	// Run initial sync on startup
	s.syncAll(ctx)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.syncAll(ctx)
		case <-s.stopCh:
			s.log.Info("Drive sync scheduler stopped via stop channel")
			return
		case <-ctx.Done():
			s.log.Info("Drive sync scheduler stopped via context")
			return
		}
	}
}

// Stop stops the Drive sync scheduler.
func (s *DriveSyncScheduler) Stop() {
	close(s.stopCh)
}

// syncAll runs all three sync operations sequentially.
func (s *DriveSyncScheduler) syncAll(ctx context.Context) {
	s.log.Info("Starting periodic Drive sync cycle")

	// 1. Catalog sync (stock, clips, artlist)
	if s.catalogSync != nil {
		s.log.Info("Syncing catalog (stock, clips, artlist) from Drive...")
		summary, err := s.catalogSync.SyncAll(ctx)
		if err != nil {
			s.log.Error("Catalog sync failed", zap.Error(err))
		} else {
			s.log.Info("Catalog sync completed",
				zap.Int("synced", summary.Synced),
				zap.Int("failed", summary.Failed),
			)
			for _, root := range summary.Roots {
				s.log.Info("  root sync result",
					zap.String("name", root.Name),
					zap.Int("synced", root.Synced),
					zap.Int("failed", root.Failed),
				)
			}
		}
	}

	// 2. Voiceover sync
	if s.voiceoverSync != nil {
		s.log.Info("Syncing voiceovers from Drive...")
		summary, err := s.voiceoverSync.Sync(ctx)
		if err != nil {
			s.log.Error("Voiceover sync failed", zap.Error(err))
		} else {
			s.log.Info("Voiceover sync completed",
				zap.Int("synced", summary.Synced),
				zap.Int("failed", summary.Failed),
			)
		}
	}

	// 3. Image sync
	if s.imageService != nil {
		s.log.Info("Syncing images from Drive...")
		err := s.imageService.SyncFromDrive(ctx)
		if err != nil {
			s.log.Error("Image sync failed", zap.Error(err))
		} else {
			s.log.Info("Image sync completed")
		}
	}

	s.log.Info("Periodic Drive sync cycle completed")
}
