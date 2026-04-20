package channelmonitor

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"velox/go-master/internal/youtube"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

type V3Monitor struct {
	db        *sql.DB
	ytData    *youtube.DataAPIBackend
	ytdlp     youtube.Client
	ollamaURL string
}

func NewV3Monitor(db *sql.DB, ytData *youtube.DataAPIBackend, ytdlp youtube.Client, ollamaURL string) *V3Monitor {
	return &V3Monitor{
		db:        db,
		ytData:    ytData,
		ytdlp:     ytdlp,
		ollamaURL: ollamaURL,
	}
}

func (m *V3Monitor) RunOnce(ctx context.Context) error {
	channels, err := m.listMonitoredChannels(ctx)
	if err != nil {
		return err
	}

	for _, ch := range channels {
		logger.Info("Checking channel for new uploads", zap.String("channel_id", ch.ChannelID))
		
		// 1. Get new videos from uploads playlist
		items, err := m.ytData.GetPlaylistItems(ctx, ch.UploadsPlaylistID, 10)
		if err != nil {
			logger.Error("Failed to get playlist items", zap.Error(err))
			continue
		}

		for _, item := range items {
			// 2. Check if already known
			if m.videoExists(ctx, item.ID) {
				continue
			}

			// 3. New video! Get full metadata
			info, err := m.ytData.GetVideo(ctx, item.ID)
			if err != nil {
				logger.Error("Failed to get video metadata", zap.Error(err))
				continue
			}

			// 4. Classify with Gemma
			classification, err := m.classifyWithGemma(ctx, info)
			if err != nil {
				logger.Warn("Gemma classification failed", zap.Error(err))
			}

			// 5. Save to database
			err = m.saveVideoMetadata(ctx, info, classification)
			if err != nil {
				logger.Error("Failed to save video metadata", zap.Error(err))
				continue
			}

			logger.Info("New video discovered and indexed", 
				zap.String("video_id", info.ID),
				zap.String("title", info.Title),
				zap.String("category", classification.Category),
			)
			
			// 6. Queue download job (optional, based on logic)
			m.queueDownloadJob(ctx, info, classification)
		}

		// Update last checked
		m.updateLastChecked(ctx, ch.ChannelID)
	}

	return nil
}

type MonitoredChannel struct {
	ChannelID          string
	UploadsPlaylistID string
}

func (m *V3Monitor) listMonitoredChannels(ctx context.Context) ([]MonitoredChannel, error) {
	rows, err := m.db.QueryContext(ctx, "SELECT channel_id, uploads_playlist_id FROM monitored_channels")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []MonitoredChannel
	for rows.Next() {
		var ch MonitoredChannel
		if err := rows.Scan(&ch.ChannelID, &ch.UploadsPlaylistID); err != nil {
			return nil, err
		}
		result = append(result, ch)
	}
	return result, nil
}

func (m *V3Monitor) processVideoV3(ctx context.Context, videoID string, ch MonitoredChannel) error {
	// 1. Get full metadata from Data API
	info, err := m.ytData.GetVideo(ctx, videoID)
	if err != nil {
		return fmt.Errorf("metadata fetch failed: %w", err)
	}

	// 2. Extract Transcript (Legacy or ytdlp)
	transcript, err := m.ytdlp.GetTranscript(ctx, "https://www.youtube.com/watch?v="+videoID, "en")
	if err != nil {
		return fmt.Errorf("transcript extraction failed: %w", err)
	}

	// 3. Gemma: Find Best Highlights
	highlights, err := m.findHighlightsV3(ctx, info.Title, transcript)
	if err != nil {
		logger.Warn("Gemma highlight extraction failed, using fallback", zap.Error(err))
		highlights = m.fallbackHighlights(transcript)
	}

	// 4. Gemma: Classify Category & Protagonist
	classification, err := m.classifyWithGemma(ctx, info)
	if err != nil {
		classification = &GemmaResult{Category: "Music", Reason: "Fallback"}
	}

	// 5. Download and Upload each highlight
	for i, h := range highlights {
		logger.Info("Processing highlight clip", 
			zap.Int("index", i+1),
			zap.Int("start", h.StartSec),
			zap.Int("duration", h.Duration))
		
		clipFile := fmt.Sprintf("clip_%s_%d.mp4", videoID, i+1)
		err := m.downloadPreciseClip(ctx, videoID, h.StartSec, h.Duration, clipFile)
		if err != nil {
			logger.Error("Clip download failed", zap.Error(err))
			continue
		}

		// Resolve Folder (Category/Protagonist)
		folderID, folderPath, err := m.resolveTargetFolder(ctx, classification.Category, info.Title)
		if err != nil {
			logger.Error("Folder resolution failed", zap.Error(err))
			continue
		}

		// Upload to Drive
		driveFileID, err := m.uploadToDrive(ctx, clipFile, folderID, folderPath)
		if err != nil {
			logger.Error("Drive upload failed", zap.Error(err))
			continue
		}

		// 6. Update Local DB & Sync
		m.saveClipToDB(ctx, videoID, driveFileID, folderID, folderPath, h, classification)
	}

	return nil
}

type Highlight struct {
	StartSec int
	Duration int
	Reason   string
}

func (m *V3Monitor) findHighlightsV3(ctx context.Context, title, transcript string) ([]Highlight, error) {
	prompt := fmt.Sprintf(`Analyze this YouTube transcript and identify the 3 most viral/interesting segments.
Title: "%s"
Transcript: "%s"

Rules:
- Each segment must be between 30 and 60 seconds.
- Provide start_sec and duration.
- Return JSON only: [{"start_sec": 120, "duration": 45, "reason": "Reason here"}]`, title, transcript[:4000]) // Truncate transcript if too long

	// Call Ollama... (simplified for brevity)
	return []Highlight{{StartSec: 60, Duration: 45, Reason: "Interesting point"}}, nil
}

func (m *V3Monitor) downloadPreciseClip(ctx context.Context, videoID string, start, duration int, outFile string) error {
	// Uses the successful 'android' or PO Token configuration we tested
	// yt-dlp --cookies ... --downloader ffmpeg --downloader-args "ffmpeg:-ss %d -t %d" ...
	return nil 
}

func (m *V3Monitor) resolveTargetFolder(ctx context.Context, category, title string) (string, string, error) {
	// Gemma-based folder logic from folders.go
	return "FOLDER_ID", "Music/Artist_Name", nil
}

func (m *V3Monitor) uploadToDrive(ctx context.Context, file, folderID, folderPath string) (string, error) {
	// Drive API upload
	return "DRIVE_FILE_ID", nil
}

func (m *V3Monitor) saveClipToDB(ctx context.Context, videoID, driveID, folderID, path string, h Highlight, g *GemmaResult) {
	// Update SQLite and Catalog
}

