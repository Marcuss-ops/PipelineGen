package channelmonitor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"velox/go-master/internal/runtime"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/internal/youtube"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// Compile-time check that Monitor satisfies BackgroundService.
var _ runtime.BackgroundService = (*Monitor)(nil)

// Monitor is the channel monitor service
type Monitor struct {
	config          MonitorConfig
	ytClient        youtube.Client
	driveClient     *drive.Client
	folderCache     map[string]string               // category/protagonist -> folder ID
	processedVideos map[string]*ProcessedVideoEntry // videoID -> entry
	processedFile   string                          // path to processed videos JSON file
	ollamaURL       string                          // Ollama URL for AI classification
}

// NewMonitor creates a new channel monitor
func NewMonitor(cfg MonitorConfig, ytClient youtube.Client, driveClient *drive.Client, ollamaURL string) *Monitor {
	if cfg.CheckInterval == 0 {
		cfg.CheckInterval = 24 * time.Hour
	}
	if cfg.MaxClipDuration == 0 {
		cfg.MaxClipDuration = 60
	}
	if ollamaURL == "" {
		ollamaURL = "http://localhost:11434"
	}
	m := &Monitor{
		config:          cfg,
		ytClient:        ytClient,
		driveClient:     driveClient,
		folderCache:     make(map[string]string),
		processedVideos: make(map[string]*ProcessedVideoEntry),
		processedFile:   "data/channel_monitor_processed.json",
		ollamaURL:       ollamaURL,
	}

	// Load previously processed videos
	m.loadProcessedVideos()

	// Scan existing subfolders in each macro-folder
	m.scanExistingFolders()

	return m
}

// Start runs the monitor in a background goroutine until context is cancelled.
// Returns immediately (non-blocking) to satisfy BackgroundService.
func (m *Monitor) Start(ctx context.Context) error {
	go m.runLoop(ctx)
	return nil
}

// runLoop is the main monitoring loop, executed in a goroutine by Start.
func (m *Monitor) runLoop(ctx context.Context) {
	// Run initial scan
	results, err := m.RunOnce(ctx)
	if err != nil {
		logger.Error("Initial monitor run failed", zap.Error(err))
	} else {
		logger.Info("Initial monitor run completed",
			zap.Int("videos_processed", len(results)),
		)
	}

	// Run periodic scans
	ticker := time.NewTicker(m.config.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Channel monitor stopped")
			return
		case <-ticker.C:
			results, err := m.RunOnce(ctx)
			if err != nil {
				logger.Error("Monitor run failed", zap.Error(err))
			} else {
				logger.Info("Monitor run completed",
					zap.Int("videos_processed", len(results)),
				)
			}
		}
	}
}

// Stop is a no-op; goroutines exit via context cancellation from ServiceGroup.
func (m *Monitor) Stop() error {
	return nil
}

// Name returns the service name for lifecycle logging.
func (m *Monitor) Name() string { return "ChannelMonitor" }

// RunOnce performs one complete monitoring cycle
func (m *Monitor) RunOnce(ctx context.Context) ([]VideoResult, error) {
	logger.Info("Starting channel monitor cycle",
		zap.Int("channels", len(m.config.Channels)),
	)

	var allResults []VideoResult

	for _, ch := range m.config.Channels {
		results, err := m.processChannel(ctx, ch)
		if err != nil {
			logger.Error("Failed to process channel",
				zap.String("url", ch.URL),
				zap.Error(err),
			)
			continue
		}
		allResults = append(allResults, results...)
	}

	logger.Info("Channel monitor cycle completed",
		zap.Int("videos_processed", len(allResults)),
	)

	return allResults, nil
}

// ProcessVideo handles processing of a single video, from metadata to clip upload.
func (m *Monitor) ProcessVideo(ctx context.Context, videoID, categoryOverride string) (*VideoResult, error) {
	// 1. Get video info
	videoInfo, err := m.ytClient.GetVideo(ctx, videoID)
	var videoTitle string
	if err != nil {
		logger.Warn("API GetVideo failed, using fallback", zap.String("video_id", videoID), zap.Error(err))
		videoTitle = m.GetVideoTitleFallback(ctx, videoID)
	} else {
		videoTitle = videoInfo.Title
	}

	// 2. Determine category
	category := categoryOverride
	if category == "" {
		category = "HipHop" // Default
	}

	// 3. Setup config for folder resolution
	chConfig := ChannelConfig{
		Category:        category,
		MaxClipDuration: m.config.MaxClipDuration,
	}
	if chConfig.MaxClipDuration == 0 {
		chConfig.MaxClipDuration = 60
	}

	// 4. Extract transcript
	transcript, err := m.extractTranscript(ctx, videoID)
	if err != nil {
		return nil, fmt.Errorf("transcript extraction failed: %w", err)
	}

	// 5. Find highlights
	highlights := m.findHighlights(transcript)
	if len(highlights) == 0 {
		return nil, fmt.Errorf("no highlights found in transcript")
	}

	// 6. Resolve folder
	folderPath, folderID, folderExisted, err := m.resolveFolder(ctx, chConfig, videoTitle)
	if err != nil {
		return nil, fmt.Errorf("folder resolution failed: %w", err)
	}

	// 7. Download and upload clips
	clips, err := m.downloadAndUploadClips(ctx, youtube.SearchResult{
		ID:    videoID,
		Title: videoTitle,
	}, highlights, folderID, folderPath, folderExisted, chConfig.MaxClipDuration)
	if err != nil {
		logger.Warn("Some clips failed to process", zap.Error(err))
	}

	return &VideoResult{
		VideoID:    videoID,
		Title:      videoTitle,
		Highlights: highlights,
		Clips:      clips,
		FolderPath: folderPath,
	}, nil
}

// GetVideoTitleFallback uses yt-dlp to get video title when API fails
func (m *Monitor) GetVideoTitleFallback(ctx context.Context, videoID string) string {
	if m.ytClient == nil {
		return fmt.Sprintf("Video_%s", videoID)
	}

	info, err := m.ytClient.GetVideo(ctx, videoID)
	if err != nil || info == nil {
		logger.Warn("yt-dlp title fallback failed", zap.Error(err))
		return fmt.Sprintf("Video_%s", videoID)
	}

	if title := strings.TrimSpace(info.Title); title != "" {
		return title
	}
	return fmt.Sprintf("Video_%s", videoID)
}

// ExtractTranscript extracts transcript from a YouTube video
func (m *Monitor) ExtractTranscript(ctx context.Context, videoID string) (string, error) {
	return m.extractTranscript(ctx, videoID)
}

// FindHighlights extracts interesting segments from a transcript
func (m *Monitor) FindHighlights(transcript string) []HighlightSegment {
	return m.findHighlights(transcript)
}

// ResolveFolder determines the Drive folder for a video's clips
func (m *Monitor) ResolveFolder(ctx context.Context, ch ChannelConfig, videoTitle string) (string, string, bool, error) {
	return m.resolveFolder(ctx, ch, videoTitle)
}

// DownloadAndUploadClips downloads highlight clips and uploads them to Drive
func (m *Monitor) DownloadAndUploadClips(ctx context.Context, video youtube.SearchResult, highlights []HighlightSegment, folderID, folderPath string, folderExisted bool, maxDuration int) ([]ClipResult, error) {
	return m.downloadAndUploadClips(ctx, video, highlights, folderID, folderPath, folderExisted, maxDuration)
}

// ClassifyCategory resolves the best category for a video title using Gemma + guardrails.
func (m *Monitor) ClassifyCategory(ctx context.Context, title string) (string, error) {
	return m.classifyEntity(ctx, title, extractProtagonist(title))
}

// CategoryChoices returns the 6 canonical categories used by monitor routing.
func (m *Monitor) CategoryChoices(ctx context.Context) []string {
	return m.categoryChoices(ctx)
}
