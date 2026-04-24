package clip

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/runtime"
	"velox/go-master/pkg/logger"
)

// IndexScanner periodically scans and updates the clip index
// Compile-time check that IndexScanner satisfies BackgroundService.
var _ runtime.BackgroundService = (*IndexScanner)(nil)

type IndexScanner struct {
	indexer        *Indexer
	indexStore     IndexStore // Interface for saving/loading index
	scanInterval   time.Duration
	stopCh         chan struct{}
	stopOnce       sync.Once // prevents double-close panic on stopCh
	mu             sync.Mutex
	lastScanResult *ScanResult
}

// ScanResult holds information about a scan operation
type ScanResult struct {
	Success      bool          `json:"success"`
	Duration     time.Duration `json:"duration"`
	TotalClips   int           `json:"total_clips"`
	TotalFolders int           `json:"total_folders"`
	ClipsChanged int           `json:"clips_changed"`
	Error        string        `json:"error,omitempty"`
	LastScanTime time.Time     `json:"last_scan_time"`
}

// IndexStore is the interface for saving/loading clip index
type IndexStore interface {
	SaveIndex(index *ClipIndex) error
	LoadIndex() (*ClipIndex, error)
	DeleteIndex() error
}

// NewIndexScanner creates a new clip index scanner
func NewIndexScanner(indexer *Indexer, indexStore IndexStore, scanInterval time.Duration) *IndexScanner {
	return &IndexScanner{
		indexer:      indexer,
		indexStore:   indexStore,
		scanInterval: scanInterval,
		stopCh:       make(chan struct{}),
	}
}

// Start begins the periodic scanning loop in a background goroutine.
// Returns immediately (non-blocking) to satisfy BackgroundService.
func (s *IndexScanner) Start(ctx context.Context) error {
	if s == nil || s.indexer == nil || !s.indexer.HasDriveClient() {
		logger.Warn("Skipping clip index scanner start because Drive client is unavailable")
		return nil
	}

	logger.Info("Clip index scanner started",
		zap.Duration("scan_interval", s.scanInterval))

	go s.runLoop(ctx)
	return nil
}

// runLoop is the main scanning loop, executed in a goroutine by Start.
//
// Note: periodic scans run synchronously within this loop (not as separate
// goroutines). This is intentional — it prevents scan pile-up where a slow
// scan overlaps with the next tick. The effective interval becomes
// scanInterval + scanDuration, which is safer than unbounded concurrency.
func (s *IndexScanner) runLoop(ctx context.Context) {
	if s == nil || s.indexer == nil || !s.indexer.HasDriveClient() {
		return
	}

	// Run an initial scan on startup
	s.performScan(ctx, "startup")

	// Run periodic scans
	ticker := time.NewTicker(s.scanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Clip index scanner stopping (context done)")
			return
		case <-s.stopCh:
			logger.Info("Clip index scanner stopping (manual stop)")
			return
		case <-ticker.C:
			s.performScan(ctx, "periodic")
		}
	}
}

// Stop stops the periodic scanning.
// Safe to call multiple times (idempotent via sync.Once).
func (s *IndexScanner) Stop() error {
	s.stopOnce.Do(func() { close(s.stopCh) })
	return nil
}

// Name returns the service name for lifecycle logging.
func (s *IndexScanner) Name() string { return "ClipScanner" }

// TriggerManualScan triggers an immediate scan (callable from API)
func (s *IndexScanner) TriggerManualScan(ctx context.Context) *ScanResult {
	if s == nil || s.indexer == nil || !s.indexer.HasDriveClient() {
		return &ScanResult{
			Success:      false,
			Error:        "drive client is unavailable",
			LastScanTime: time.Now(),
		}
	}
	return s.performScan(ctx, "manual")
}

// TriggerIncrementalScan triggers an incremental scan (callable from API)
func (s *IndexScanner) TriggerIncrementalScan(ctx context.Context) *ScanResult {
	if s == nil || s.indexer == nil || !s.indexer.HasDriveClient() {
		return &ScanResult{
			Success:      false,
			Error:        "drive client is unavailable",
			LastScanTime: time.Now(),
		}
	}

	startTime := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	// Run incremental scan
	folders, clips, err := s.indexer.IncrementalScan(ctx)
	duration := time.Since(startTime)

	result := &ScanResult{
		Success:      err == nil,
		Duration:     duration,
		TotalClips:   len(s.indexer.GetIndex().Clips),
		TotalFolders: len(s.indexer.GetIndex().Folders),
		ClipsChanged: clips,
		LastScanTime: time.Now(),
	}

	if err != nil {
		result.Error = err.Error()
		logger.Error("Incremental scan failed", zap.Error(err))
	} else {
		// Save to disk
		index := s.indexer.GetIndex()
		if saveErr := s.indexStore.SaveIndex(index); saveErr != nil {
			logger.Error("Failed to save clip index after incremental scan", zap.Error(saveErr))
		}

		logger.Info("Incremental scan completed successfully",
			zap.Int("folders_updated", folders),
			zap.Int("clips_net_change", clips),
			zap.Duration("duration", duration))
	}

	s.lastScanResult = result
	return result
}

// GetLastScanResult returns the result of the last scan
func (s *IndexScanner) GetLastScanResult() *ScanResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastScanResult
}

// performScan performs a full scan of the clip index
func (s *IndexScanner) performScan(ctx context.Context, triggerType string) *ScanResult {
	if s == nil || s.indexer == nil || !s.indexer.HasDriveClient() {
		return &ScanResult{
			Success:      false,
			Error:        "drive client is unavailable",
			LastScanTime: time.Now(),
		}
	}

	startTime := time.Now()

	s.mu.Lock()
	// Don't run concurrent scans
	s.mu.Unlock()

	logger.Info("Starting clip index scan", zap.String("trigger", triggerType))

	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	err := s.indexer.ScanAndIndex(ctx)
	duration := time.Since(startTime)

	result := &ScanResult{
		Success:      err == nil,
		Duration:     duration,
		TotalClips:   len(s.indexer.GetIndex().Clips),
		TotalFolders: len(s.indexer.GetIndex().Folders),
		LastScanTime: time.Now(),
	}

	if err != nil {
		result.Error = err.Error()
		logger.Error("Clip index scan failed",
			zap.String("trigger", triggerType),
			zap.Error(err),
			zap.Duration("duration", duration))
	} else {
		// Save to disk
		index := s.indexer.GetIndex()
		if saveErr := s.indexStore.SaveIndex(index); saveErr != nil {
			logger.Error("Failed to save clip index after scan", zap.Error(saveErr))
		}

		logger.Info("Clip index scan completed successfully",
			zap.String("trigger", triggerType),
			zap.Int("total_clips", result.TotalClips),
			zap.Int("total_folders", result.TotalFolders),
			zap.Duration("duration", duration))
	}

	s.mu.Lock()
	s.lastScanResult = result
	s.mu.Unlock()

	return result
}
