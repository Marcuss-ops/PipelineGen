package scheduler

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/config"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/upload/drive"
)

// LifecycleScheduler handles periodic system maintenance (Sync, Cleanup)
type LifecycleScheduler struct {
	cfg     *config.Config
	log     *zap.Logger
	jobsSvc *jobservice.Service
	apiURL  string
	stopCh  chan struct{}
}

// NewLifecycleScheduler creates a new lifecycle scheduler
func NewLifecycleScheduler(cfg *config.Config, jobsSvc *jobservice.Service, log *zap.Logger) *LifecycleScheduler {
	apiURL := fmt.Sprintf("http://%s:%d", cfg.Server.Host, cfg.Server.Port)
	return &LifecycleScheduler{
		cfg:     cfg,
		log:     log,
		jobsSvc: jobsSvc,
		apiURL:  apiURL,
		stopCh:  make(chan struct{}),
	}
}

// Start begins the lifecycle scheduler
func (s *LifecycleScheduler) Start(ctx context.Context) {
	s.log.Info("Starting lifecycle scheduler")

	// 1. Catalog Sync Ticker
	syncInterval := 6 * time.Hour
	if s.cfg.Jobs.CatalogSyncInterval != "" {
		if d, err := time.ParseDuration(s.cfg.Jobs.CatalogSyncInterval); err == nil {
			syncInterval = d
		}
	}

	// 2. Maintenance Ticker
	maintenanceInterval := 24 * time.Hour
	if s.cfg.Jobs.MaintenanceInterval != "" {
		if d, err := time.ParseDuration(s.cfg.Jobs.MaintenanceInterval); err == nil {
			maintenanceInterval = d
		}
	}

	syncTicker := time.NewTicker(syncInterval)
	maintenanceTicker := time.NewTicker(maintenanceInterval)
	defer syncTicker.Stop()
	defer maintenanceTicker.Stop()

	s.log.Info("Lifecycle scheduler active",
		zap.Duration("sync_interval", syncInterval),
		zap.Duration("maintenance_interval", maintenanceInterval))

	for {
		select {
		case <-syncTicker.C:
			s.triggerSync(ctx)
		case <-maintenanceTicker.C:
			s.triggerCleanup(ctx)
		case <-s.stopCh:
			s.log.Info("Lifecycle scheduler stopped")
			return
		case <-ctx.Done():
			s.log.Info("Lifecycle scheduler stopped via context")
			return
		}
	}
}

func (s *LifecycleScheduler) triggerSync(ctx context.Context) {
	s.log.Info("Triggering periodic catalog sync via job system")

	if s.jobsSvc == nil {
		s.log.Warn("Jobs service not available, skipping periodic sync")
		return
	}

	// Sources to sync - only those with configured root folders
	var sources []string
	if s.cfg.Drive.ClipsRootFolder != "" {
		sources = append(sources, "youtube")
	}
	if drive.ResolveArtlistRootFolderID(s.cfg) != "" {
		sources = append(sources, "artlist")
	}
	if s.cfg.Drive.StockRootFolder != "" {
		sources = append(sources, "stock")
	}
	if s.cfg.Drive.VoiceoverRootFolder != "" {
		sources = append(sources, "voiceover")
	}
	if s.cfg.Drive.ImagesRootFolder != "" {
		sources = append(sources, "images")
	}

	if len(sources) == 0 {
		s.log.Info("No sources configured for periodic sync")
		return
	}

	for _, src := range sources {
		payload := map[string]any{
			"source": src,
		}

		job, err := s.jobsSvc.Enqueue(ctx, &jobservice.EnqueueRequest{
			Type:      models.JobTypeCatalogSync,
			Payload:   payload,
			Priority:  10,
			ActiveKey: fmt.Sprintf("sync_%s_%s", src, time.Now().Format("2006-01-02-15")), // One per hour max
		})
		if err != nil {
			s.log.Error("Failed to enqueue sync job", zap.String("source", src), zap.Error(err))
			continue
		}
		s.log.Info("Sync job enqueued", zap.String("source", src), zap.String("job_id", job.ID))
	}
}

func (s *LifecycleScheduler) triggerCleanup(ctx context.Context) {
	s.log.Info("Triggering periodic deep cleanup via job system")

	if s.jobsSvc == nil {
		s.log.Warn("Jobs service not available, skipping periodic cleanup")
		return
	}

	payload := map[string]any{
		"deep":       true,
		"assets_dir": s.cfg.Storage.AssetsPath(),
	}

	job, err := s.jobsSvc.Enqueue(ctx, &jobservice.EnqueueRequest{
		Type:      models.JobTypeSystemCleanup,
		Payload:   payload,
		Priority:  5,
		ActiveKey: "system_maintenance_periodic", // Allow only one active periodic maintenance job
	})
	if err != nil {
		s.log.Error("Failed to enqueue cleanup job", zap.Error(err))
		return
	}
	s.log.Info("Cleanup job enqueued", zap.String("job_id", job.ID))
}
