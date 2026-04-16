// Package harvester fornisce harvesting automatico di clip da YouTube → Drive
package harvester

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"velox/go-master/internal/downloader"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

type Config struct {
	Enabled            bool          `json:"enabled"`
	CheckInterval      time.Duration `json:"check_interval"`
	SearchQueries      []string      `json:"search_queries"`
	Channels           []string      `json:"channels"`
	MaxResultsPerQuery int           `json:"max_results_per_query"`
	MinViews           int64         `json:"min_views"`
	Timeframe          string        `json:"timeframe"`
	MaxConcurrentDls   int           `json:"max_concurrent_downloads"`
	DownloadDir        string        `json:"download_dir"`
	ProcessClips       bool          `json:"process_clips"`
	DriveFolderID      string        `json:"drive_folder_id"`
}

type SearchResult struct {
	VideoID    string    `json:"video_id"`
	Title      string    `json:"title"`
	URL        string    `json:"url"`
	Views      int64     `json:"views"`
	Duration   int       `json:"duration"`
	Channel    string    `json:"channel"`
	UploadedAt time.Time `json:"uploaded_at"`
	Thumbnail  string    `json:"thumbnail"`
}

type HarvestResult struct {
	VideoID     string `json:"video_id"`
	Title       string `json:"title"`
	Downloaded  bool   `json:"downloaded"`
	Processed   bool   `json:"processed"`
	Uploaded    bool   `json:"uploaded"`
	DriveFileID string `json:"drive_file_id,omitempty"`
	DriveURL    string `json:"drive_url,omitempty"`
	Error       string `json:"error,omitempty"`
}

type BlacklistRecord struct {
	VideoID       string    `json:"video_id"`
	Reason        string    `json:"reason"`
	Score         float64   `json:"score"`
	BlacklistedAt time.Time `json:"blacklisted_at"`
}

type Harvester struct {
	config        *Config
	youtubeClient YouTubeSearcher
	downloader    downloader.Downloader
	driveClient   *drive.Client
	db            ClipDatabase
	blacklist     []BlacklistRecord
	downloadCh    chan SearchResult
	resultCh      chan HarvestResult
	wg            sync.WaitGroup
	running       bool
	stopCh        chan struct{}
}

type ClipDatabase interface {
	AddClip(record *ClipRecord) error
	GetClip(videoID string) (*ClipRecord, error)
	ClipExists(videoID string) (bool, error)
	UpdateClip(record *ClipRecord) error
}

type ClipRecord struct {
	VideoID      string    `json:"video_id"`
	Title        string    `json:"title"`
	URL          string    `json:"url"`
	Views        int64     `json:"views"`
	Duration     int       `json:"duration"`
	Channel      string    `json:"channel"`
	Downloaded   bool      `json:"downloaded"`
	DownloadPath string    `json:"download_path"`
	DriveFileID  string    `json:"drive_file_id"`
	DriveURL     string    `json:"drive_url"`
	FolderPath   string    `json:"folder_path"`
	ProcessedAt  time.Time `json:"processed_at"`
	UploadedAt   time.Time `json:"uploaded_at"`
	CreatedAt    time.Time `json:"created_at"`
}

type YouTubeSearcher interface {
	Search(ctx context.Context, query string, opts *SearchOptions) ([]SearchResult, error)
	SearchByChannel(ctx context.Context, channelID string, opts *SearchOptions) ([]SearchResult, error)
	GetVideoStats(ctx context.Context, videoID string) (*SearchResult, error)
}

type SearchOptions struct {
	MaxResults int
	SortBy     string
	Timeframe  string
	ChannelID  string
}

func NewHarvester(
	config *Config,
	ytClient YouTubeSearcher,
	dl downloader.Downloader,
	driveClient *drive.Client,
	db ClipDatabase,
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

func (h *Harvester) Stop() error {
	if !h.running {
		return nil
	}

	close(h.stopCh)
	h.wg.Wait()
	h.running = false

	logger.Info("Harvester stopped")
	return nil
}

func (h *Harvester) run(ctx context.Context) {
	ticker := time.NewTicker(h.config.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-h.stopCh:
			return
		case <-ticker.C:
			h.executeCycle(ctx)
		}
	}
}

func (h *Harvester) executeCycle(ctx context.Context) {
	logger.Info("Starting harvest cycle")

	var wg sync.WaitGroup

	for _, query := range h.config.SearchQueries {
		wg.Add(1)
		go func(q string) {
			defer wg.Done()
			h.searchAndQueue(ctx, q)
		}(query)
	}

	for _, channel := range h.config.Channels {
		wg.Add(1)
		go func(ch string) {
			defer wg.Done()
			h.searchChannel(ctx, ch)
		}(channel)
	}

	wg.Wait()

	logger.Info("Harvest cycle completed")
}

func (h *Harvester) searchAndQueue(ctx context.Context, query string) {
	logger.Info("Searching YouTube", zap.String("query", query))

	opts := &SearchOptions{
		MaxResults: h.config.MaxResultsPerQuery,
		SortBy:     "viewCount",
		Timeframe:  h.config.Timeframe,
	}

	results, err := h.youtubeClient.Search(ctx, query, opts)
	if err != nil {
		logger.Warn("YouTube search failed", zap.Error(err), zap.String("query", query))
		return
	}

	for _, r := range results {
		if r.Views < h.config.MinViews {
			continue
		}

		if h.isBlacklisted(r.VideoID) {
			logger.Info("Skipping blacklisted video", zap.String("video_id", r.VideoID))
			continue
		}

		exists, _ := h.db.ClipExists(r.VideoID)
		if exists {
			continue
		}

		h.downloadCh <- r

		record := &ClipRecord{
			VideoID:   r.VideoID,
			Title:     r.Title,
			URL:       r.URL,
			Views:     r.Views,
			Duration:  r.Duration,
			Channel:   r.Channel,
			CreatedAt: time.Now(),
		}
		h.db.AddClip(record)
	}

	logger.Info("Queued for download", zap.Int("count", len(results)), zap.String("query", query))
}

func (h *Harvester) searchChannel(ctx context.Context, channel string) {
	logger.Info("Searching channel", zap.String("channel", channel))

	opts := &SearchOptions{
		MaxResults: h.config.MaxResultsPerQuery,
		SortBy:     "viewCount",
		Timeframe:  h.config.Timeframe,
		ChannelID:  channel,
	}

	results, err := h.youtubeClient.Search(ctx, channel, opts)
	if err != nil {
		logger.Warn("Channel search failed", zap.Error(err), zap.String("channel", channel))
		return
	}

	for _, r := range results {
		if r.Views < h.config.MinViews {
			continue
		}

		if h.isBlacklisted(r.VideoID) {
			continue
		}

		exists, _ := h.db.ClipExists(r.VideoID)
		if exists {
			continue
		}

		h.downloadCh <- r

		record := &ClipRecord{
			VideoID:   r.VideoID,
			Title:     r.Title,
			URL:       r.URL,
			Views:     r.Views,
			Duration:  r.Duration,
			Channel:   r.Channel,
			CreatedAt: time.Now(),
		}
		h.db.AddClip(record)
	}
}

func (h *Harvester) worker(ctx context.Context, id int) {
	defer h.wg.Done()

	logger.Info("Worker started", zap.Int("id", id))

	for {
		select {
		case <-ctx.Done():
			return
		case <-h.stopCh:
			return
		case result, ok := <-h.downloadCh:
			if !ok {
				return
			}
			h.processVideo(ctx, result)
		}
	}
}

func (h *Harvester) processVideo(ctx context.Context, result SearchResult) {
	logger.Info("Processing video", zap.String("video_id", result.VideoID), zap.String("title", result.Title))

	hr := HarvestResult{
		VideoID: result.VideoID,
		Title:   result.Title,
	}

	localPath, err := h.downloadVideo(ctx, result)
	if err != nil {
		hr.Error = err.Error()
		h.resultCh <- hr
		return
	}

	hr.Downloaded = true

	if h.config.ProcessClips && h.driveClient != nil {
		fileID, err := h.uploadToDrive(ctx, localPath, result)
		if err != nil {
			hr.Error = err.Error()
			h.resultCh <- hr
			return
		}

		hr.Uploaded = true
		hr.DriveFileID = fileID
		hr.DriveURL = fmt.Sprintf("https://drive.google.com/file/d/%s/view", fileID)

		record := &ClipRecord{
			VideoID:      result.VideoID,
			Downloaded:   true,
			DownloadPath: localPath,
			DriveFileID:  fileID,
			DriveURL:     hr.DriveURL,
			FolderPath:   "Clips/" + h.extractTopic(result.Title),
			ProcessedAt:  time.Now(),
		}
		h.db.UpdateClip(record)
	}

	h.resultCh <- hr
}

func (h *Harvester) downloadVideo(ctx context.Context, result SearchResult) (string, error) {
	req := &downloader.DownloadRequest{
		URL:       result.URL,
		OutputDir: h.config.DownloadDir,
	}

	dlResult, err := h.downloader.Download(ctx, req)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}

	return dlResult.FilePath, nil
}

func (h *Harvester) uploadToDrive(ctx context.Context, localPath string, result SearchResult) (string, error) {
	filename := filepath.Base(localPath)
	topic := h.extractTopic(result.Title)

	folderPath := fmt.Sprintf("Clips/%s", topic)
	folderID := h.config.DriveFolderID

	if h.driveClient != nil {
		folder, err := h.driveClient.GetOrCreateFolder(ctx, folderPath, h.config.DriveFolderID)
		if err != nil {
			logger.Warn("Failed to get/create folder", zap.Error(err), zap.String("path", folderPath))
		} else {
			folderID = folder
		}
	}

	fileID, err := h.driveClient.UploadVideo(ctx, localPath, folderID, filename)
	if err != nil {
		return "", fmt.Errorf("upload failed: %w", err)
	}

	return fileID, nil
}

func (h *Harvester) extractTopic(title string) string {
	titleLower := strings.ToLower(title)

	topics := []string{"boxing", "mma", "ufc", "interview", "documentary", "highlights", "business", "technology"}
	for _, t := range topics {
		if strings.Contains(titleLower, t) {
			return strings.Title(t)
		}
	}

	return "General"
}

func (h *Harvester) AddQuery(query string) {
	h.config.SearchQueries = append(h.config.SearchQueries, query)
	logger.Info("Added query", zap.String("query", query))
}

func (h *Harvester) AddChannel(channel string) {
	h.config.Channels = append(h.config.Channels, channel)
	logger.Info("Added channel", zap.String("channel", channel))
}

func (h *Harvester) RemoveQuery(query string) {
	for i, q := range h.config.SearchQueries {
		if q == query {
			h.config.SearchQueries = append(h.config.SearchQueries[:i], h.config.SearchQueries[i+1:]...)
			logger.Info("Removed query", zap.String("query", query))
			return
		}
	}
}

func (h *Harvester) RemoveChannel(channel string) {
	for i, c := range h.config.Channels {
		if c == channel {
			h.config.Channels = append(h.config.Channels[:i], h.config.Channels[i+1:]...)
			logger.Info("Removed channel", zap.String("channel", channel))
			return
		}
	}
}

func (h *Harvester) BlacklistVideo(videoID, reason string, score float64) {
	h.blacklist = append(h.blacklist, BlacklistRecord{
		VideoID:       videoID,
		Reason:        reason,
		Score:         score,
		BlacklistedAt: time.Now(),
	})
	logger.Info("Video blacklisted", zap.String("video_id", videoID), zap.String("reason", reason))
}

func (h *Harvester) UnblacklistVideo(videoID string) {
	for i, b := range h.blacklist {
		if b.VideoID == videoID {
			h.blacklist = append(h.blacklist[:i], h.blacklist[i+1:]...)
			logger.Info("Video unblacklisted", zap.String("video_id", videoID))
			return
		}
	}
}

func (h *Harvester) isBlacklisted(videoID string) bool {
	for _, b := range h.blacklist {
		if b.VideoID == videoID {
			return true
		}
	}
	return false
}

func (h *Harvester) GetBlacklist() []BlacklistRecord {
	return h.blacklist
}

func (h *Harvester) GetQueries() []string {
	return h.config.SearchQueries
}

func (h *Harvester) GetChannels() []string {
	return h.config.Channels
}

func (h *Harvester) GetResults() <-chan HarvestResult {
	return h.resultCh
}

func (h *Harvester) GetStats() map[string]int {
	return map[string]int{
		"workers":     h.config.MaxConcurrentDls,
		"queries":     len(h.config.SearchQueries),
		"channels":    len(h.config.Channels),
		"blacklist":   len(h.blacklist),
		"downloading": len(h.downloadCh),
	}
}

func (h *Harvester) RunNow(ctx context.Context) {
	logger.Info("Running harvest cycle manually")
	go h.executeCycle(ctx)
}
