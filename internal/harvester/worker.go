package harvester

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"velox/go-master/internal/downloader"
	"velox/go-master/internal/queue"
	"velox/go-master/pkg/logger"
	"velox/go-master/pkg/models"

	"go.uber.org/zap"
)

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

	results, err := h.youtubeClient.SearchByChannel(ctx, channel, opts)
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

	// If a distributed queue is available, offload the work
	if h.queue != nil {
		payloadData := map[string]string{"url": result.URL}
		payloadBytes, _ := json.Marshal(payloadData)

		msg := queue.Message{
			ID:      fmt.Sprintf("harv_%s_%d", result.VideoID, time.Now().Unix()),
			Topic:   string(models.TypeStockDownload),
			JobID:   result.VideoID,
			Payload: payloadBytes,
		}

		if err := h.queue.Publish(ctx, msg); err == nil {
			logger.Info("Video offloaded to distributed queue", zap.String("video_id", result.VideoID))
			return
		} else {
			logger.Warn("Failed to publish to queue, falling back to local processing", zap.Error(err))
		}
	}

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

func (h *Harvester) RunNow(ctx context.Context) {
	logger.Info("Running harvest cycle manually")
	go h.executeCycle(ctx)
}
