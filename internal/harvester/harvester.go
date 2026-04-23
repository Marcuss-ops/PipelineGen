// Package harvester fornisce harvesting automatico di clip da YouTube → Drive
package harvester

import (
	"context"
	"fmt"
	"os"
	"time"

	"velox/go-master/internal/downloader"
	"velox/go-master/internal/queue"
	"velox/go-master/internal/runtime"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// Compile-time check that Harvester satisfies BackgroundService.
var _ runtime.BackgroundService = (*Harvester)(nil)

func NewHarvester(
	config *Config,
	ytClient YouTubeSearcher,
	dl downloader.Downloader,
	driveClient *drive.Client,
	db ClipDatabase,
	q queue.Queue,
) *Harvester {
	if config == nil {
		config = &Config{
			Enabled:            true,
			CheckInterval:      1 * time.Hour,
			SearchQueries:      []string{"interview", "highlights", "documentary"},
			Channels:           []string{},
			MaxResultsPerQuery: 20,
			MinViews:           10000,
			Timeframe:          "week",
			MaxConcurrentDls:   3,
			DownloadDir:        "./downloads",
			ProcessClips:       true,
		}
	}

	return &Harvester{
		config:        config,
		youtubeClient: ytClient,
		downloader:    dl,
		driveClient:   driveClient,
		db:            db,
		queue:         q,
		blacklist:     []BlacklistRecord{},
		downloadCh:    make(chan SearchResult, 100),
		resultCh:      make(chan HarvestResult, 100),
		stopCh:        make(chan struct{}),
	}
}

func (h *Harvester) Start(ctx context.Context) error {
	if !h.config.Enabled {
		logger.Info("Harvester is disabled")
		return nil
	}

	if h.running {
		return fmt.Errorf("harvester already running")
	}

	h.running = true

	if err := os.MkdirAll(h.config.DownloadDir, 0755); err != nil {
		return fmt.Errorf("failed to create download dir: %w", err)
	}

	for i := 0; i < h.config.MaxConcurrentDls; i++ {
		h.wg.Add(1)
		go h.worker(ctx, i)
	}

	go h.run(ctx)

	logger.Info("Harvester started",
		zap.Int("workers", h.config.MaxConcurrentDls),
		zap.Int("queries", len(h.config.SearchQueries)),
	)

	return nil
}

// Stop signals the harvester to shut down and waits for workers to finish.
// Safe to call multiple times (idempotent via sync.Once).
func (h *Harvester) Stop() error {
	h.stopOnce.Do(func() {
		close(h.stopCh)
		h.wg.Wait()
		h.running = false
		logger.Info("Harvester stopped")
	})
	return nil
}

// Name returns the service name for lifecycle logging.
func (h *Harvester) Name() string { return "Harvester" }
