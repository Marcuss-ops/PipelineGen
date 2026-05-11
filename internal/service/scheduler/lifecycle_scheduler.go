package scheduler

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"
	"velox/go-master/pkg/config"
)

// LifecycleScheduler handles periodic system maintenance (Sync, Cleanup)
type LifecycleScheduler struct {
	cfg    *config.Config
	log    *zap.Logger
	apiURL string
	stopCh chan struct{}
}

// NewLifecycleScheduler creates a new lifecycle scheduler
func NewLifecycleScheduler(cfg *config.Config, log *zap.Logger) *LifecycleScheduler {
	apiURL := fmt.Sprintf("http://%s:%d", cfg.Server.Host, cfg.Server.Port)
	return &LifecycleScheduler{
		cfg:    cfg,
		log:    log,
		apiURL: apiURL,
		stopCh: make(chan struct{}),
	}
}

// Start begins the lifecycle scheduler
func (s *LifecycleScheduler) Start(ctx context.Context) {
	s.log.Info("Starting lifecycle scheduler")

	// 1. Catalog Sync Ticker (every 1 hour by default or from config)
	syncInterval := 1 * time.Hour
	if s.cfg.Harvester.CheckInterval != "" {
		if d, err := time.ParseDuration(s.cfg.Harvester.CheckInterval); err == nil {
			syncInterval = d
		}
	}

	// 2. Cleanup Ticker (every 24 hours)
	cleanupInterval := 24 * time.Hour

	syncTicker := time.NewTicker(syncInterval)
	cleanupTicker := time.NewTicker(cleanupInterval)
	defer syncTicker.Stop()
	defer cleanupTicker.Stop()

	s.log.Info("Lifecycle scheduler active",
		zap.Duration("sync_interval", syncInterval),
		zap.Duration("cleanup_interval", cleanupInterval))

	for {
		select {
		case <-syncTicker.C:
			s.triggerSync(ctx)
		case <-cleanupTicker.C:
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

// Stop stops the lifecycle scheduler
func (s *LifecycleScheduler) Stop() {
	close(s.stopCh)
}

func (s *LifecycleScheduler) triggerSync(ctx context.Context) {
	s.log.Info("Triggering periodic catalog sync")
	
	// Sources to sync
	sources := []string{"youtube", "artlist", "stock", "voiceover", "images"}
	
	for _, src := range sources {
		url := ""
		if src == "voiceover" || src == "images" {
			url = fmt.Sprintf("%s/api/%s/sync", s.apiURL, src)
		} else {
			url = fmt.Sprintf("%s/api/assets/%s/reconcile", s.apiURL, src)
		}

		go func(source, targetURL string) {
			req, err := http.NewRequestWithContext(ctx, "POST", targetURL, nil)
			if err != nil {
				s.log.Error("Failed to create sync request", zap.String("source", source), zap.Error(err))
				return
			}
			
			client := &http.Client{Timeout: 5 * time.Minute}
			resp, err := client.Do(req)
			if err != nil {
				s.log.Error("Failed to execute sync", zap.String("source", source), zap.Error(err))
				return
			}
			defer resp.Body.Close()
			s.log.Info("Sync triggered successfully", zap.String("source", source), zap.Int("status", resp.StatusCode))
		}(src, url)
	}
}

func (s *LifecycleScheduler) triggerCleanup(ctx context.Context) {
	s.log.Info("Triggering periodic deep cleanup")
	
	url := fmt.Sprintf("%s/api/assets/all/cleanup?deep=true", s.apiURL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		s.log.Error("Failed to create cleanup request", zap.Error(err))
		return
	}
	
	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		s.log.Error("Failed to execute cleanup", zap.Error(err))
		return
	}
	defer resp.Body.Close()
	s.log.Info("Deep cleanup triggered successfully", zap.Int("status", resp.StatusCode))
}
