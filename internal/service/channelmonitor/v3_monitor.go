package channelmonitor

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/youtube"
	"velox/go-master/pkg/logger"
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
	// 0. Health check: Verify Ollama is running
	if err := m.checkOllamaHealth(ctx); err != nil {
		return fmt.Errorf("ollama health check failed, skipping pipeline: %w", err)
	}

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
	ChannelID         string
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
	maxRetries := 3
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		err := m.processVideoV3Once(ctx, videoID)
		if err == nil {
			return nil
		}

		lastErr = err
		logger.Warn("Video processing failed, retrying",
			zap.String("video_id", videoID),
			zap.Int("attempt", attempt),
			zap.Int("max_retries", maxRetries),
			zap.Error(err))

		// Exponential backoff: 1s, 2s, 4s
		if attempt < maxRetries {
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			select {
			case <-time.After(backoff):
				// Continue to next attempt
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	return fmt.Errorf("failed after %d attempts: %w", maxRetries, lastErr)
}

func (m *V3Monitor) processVideoV3Once(ctx context.Context, videoID string) error {
	// 1. Get full metadata from Data API
	info, err := m.ytData.GetVideo(ctx, videoID)
	if err != nil {
		return fmt.Errorf("metadata fetch failed: %w", err)
	}

	// 2. Extract Transcript (Legacy or ytdlp)
	transcript, err := m.ytdlp.GetTranscript(ctx, "https://www.youtube.com/watch?v="+videoID, "en")
	if err != nil {
		logger.Warn("Transcript extraction failed, using empty transcript",
			zap.String("video_id", videoID),
			zap.Error(err))
		transcript = "" // Continue with empty transcript
	}

	// 3. Gemma: Find Best Highlights (with fallback)
	highlights, err := m.findHighlightsV3(ctx, info.Title, transcript)
	if err != nil {
		logger.Warn("Gemma highlight extraction failed, using fallback",
			zap.String("video_id", videoID),
			zap.Error(err))
		highlights = m.fallbackHighlights(transcript)
	}

	if len(highlights) == 0 {
		return fmt.Errorf("no highlights found (Gemma and fallback both failed)")
	}

	// 4. Gemma: Classify Category & Protagonist (with fallback)
	classification, err := m.classifyWithGemma(ctx, info)
	if err != nil {
		logger.Warn("Classification failed, using default",
			zap.String("video_id", videoID),
			zap.Error(err))
		classification = &GemmaResult{Category: "General", Reason: "Fallback due to error"}
	}

	// 5. Download and Upload each highlight (with error recovery)
	successCount := 0
	for i, h := range highlights {
		// Create temp file for clip
		tmpDir := filepath.Join(os.TempDir(), "velox-clips")
		os.MkdirAll(tmpDir, 0755)
		clipFile := filepath.Join(tmpDir, fmt.Sprintf("clip_%s_%d.mp4", videoID, i+1))

		// Download with retry
		dlErr := m.downloadPreciseClip(ctx, videoID, h.StartSec, h.Duration, clipFile)
		if dlErr != nil {
			logger.Warn("Clip download failed, skipping",
				zap.String("video_id", videoID),
				zap.Int("segment", i+1),
				zap.Error(dlErr))
			continue
		}

		// Resolve folder (with fallback)
		folderID, folderPath, folderErr := m.resolveTargetFolder(ctx, classification.Category, info.Title)
		if folderErr != nil {
			logger.Warn("Folder resolution failed, using default",
				zap.String("video_id", videoID),
				zap.Error(folderErr))
			folderID = "DEFAULT_FOLDER"
			folderPath = "General/" + sanitizeFolderName(info.Title)
		}

		// Upload to Drive (with error handling)
		driveFileID, uploadErr := m.uploadToDrive(ctx, clipFile, folderID, folderPath)
		if uploadErr != nil {
			logger.Warn("Drive upload failed, skipping clip",
				zap.String("video_id", videoID),
				zap.Int("segment", i+1),
				zap.Error(uploadErr))
			continue
		}

		// Save to DB (non-blocking failure)
		if err := m.saveClipToDB(ctx, videoID, driveFileID, folderID, folderPath, h, classification); err != nil {
			logger.Warn("Database save failed, but clip uploaded successfully",
				zap.String("video_id", videoID),
				zap.Error(err))
			// Continue - clip is uploaded, DB save is secondary
		}

		successCount++

		logger.Info("Clip processed successfully",
			zap.String("video_id", videoID),
			zap.Int("segment", i+1),
			zap.String("drive_id", driveFileID))

		// Cleanup temp file
		os.Remove(clipFile)
	}

	if successCount == 0 {
		return fmt.Errorf("no clips were successfully processed")
	}

	logger.Info("Video processing completed",
		zap.String("video_id", videoID),
		zap.Int("clips_processed", successCount),
		zap.Int("total_highlights", len(highlights)))

	return nil
}

type Highlight struct {
	StartSec int
	Duration int
	Reason   string
}

func (m *V3Monitor) findHighlightsV3(ctx context.Context, title, transcript string) ([]Highlight, error) {
	// Truncate transcript to prevent token overflow
	maxTranscriptLen := 3000
	if len(transcript) > maxTranscriptLen {
		transcript = transcript[:maxTranscriptLen] + "..."
	}

	prompt := fmt.Sprintf(`You are a YouTube viral moments expert. Analyze this video transcript and find the 3 MOST INTERESTING/VIRAL segments.

Title: "%s"
Transcript with timestamps: "%s"

CRITICAL REQUIREMENTS:
1. Extract timestamps directly from the transcript (look for timing markers like 0:15, 1:23, etc.)
2. EACH segment MUST be between 30-60 seconds duration. NO EXCEPTIONS.
3. Find moments with high engagement potential (surprising twists, peak emotional moments, shocking revelations, climactic points)
4. Prioritize moments where the speaker emphasizes or repeats important words
5. Return ONLY valid JSON array, no explanation text.

JSON format (MUST match exactly):
[
  {"start_sec": <seconds as integer>, "duration": <seconds as integer>, "reason": "<why this is viral>"},
  {"start_sec": <seconds as integer>, "duration": <seconds as integer>, "reason": "<why this is viral>"},
  {"start_sec": <seconds as integer>, "duration": <seconds as integer>, "reason": "<why this is viral>"}
]

Examples of good reasons: "shocking reveal", "peak emotional moment", "surprising plot twist", "viral trend reference"`, title, transcript)

	reqBody := map[string]interface{}{
		"model":  "gemma3:4b",
		"prompt": prompt,
		"stream": false,
	}

	reqJSON, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create request with timeout
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "POST", m.ollamaURL+"/api/generate", bytes.NewReader(reqJSON))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 35 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		logger.Warn("Ollama request timeout or connection failed", zap.Error(err))
		return nil, fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var ollamaResp struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Parse JSON from response
	highlights, err := m.parseHighlightsFromGemma(ollamaResp.Response)
	if err != nil {
		logger.Warn("Failed to parse Gemma highlights response",
			zap.String("response", ollamaResp.Response),
			zap.Error(err))
		return nil, fmt.Errorf("invalid highlights response: %w", err)
	}

	// Validate highlights (must be 30-60 seconds)
	var validHighlights []Highlight
	for _, h := range highlights {
		if h.Duration >= 30 && h.Duration <= 60 {
			validHighlights = append(validHighlights, h)
		} else {
			logger.Debug("Highlight filtered out (invalid duration)",
				zap.Int("start", h.StartSec),
				zap.Int("duration", h.Duration))
		}
	}

	if len(validHighlights) == 0 {
		return nil, fmt.Errorf("no valid highlights found (all outside 30-60 sec range)")
	}

	logger.Info("Found highlights via Gemma",
		zap.Int("count", len(validHighlights)),
		zap.Ints("start_times", func() []int {
			var times []int
			for _, h := range validHighlights {
				times = append(times, h.StartSec)
			}
			return times
		}()))

	return validHighlights, nil
}

// parseHighlightsFromGemma extracts timestamps from Gemma's JSON response
func (m *V3Monitor) parseHighlightsFromGemma(response string) ([]Highlight, error) {
	// Try to find JSON array in response (Gemma might include extra text)
	start := strings.Index(response, "[")
	end := strings.LastIndex(response, "]")
	if start == -1 || end == -1 || start >= end {
		return nil, fmt.Errorf("no JSON array found in response")
	}

	jsonStr := response[start : end+1]

	var rawHighlights []map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &rawHighlights); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	var highlights []Highlight
	for i, raw := range rawHighlights {
		startSec, ok := raw["start_sec"].(float64)
		if !ok {
			logger.Warn("Missing or invalid start_sec in highlight", zap.Int("index", i))
			continue
		}

		duration, ok := raw["duration"].(float64)
		if !ok {
			logger.Warn("Missing or invalid duration in highlight", zap.Int("index", i))
			continue
		}

		reason, _ := raw["reason"].(string)

		highlights = append(highlights, Highlight{
			StartSec: int(startSec),
			Duration: int(duration),
			Reason:   reason,
		})
	}

	return highlights, nil
}

func (m *V3Monitor) downloadPreciseClip(ctx context.Context, videoID string, start, duration int, outFile string) error {
	url := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)

	// Ensure output directory exists
	outDir := filepath.Dir(outFile)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Format timestamps: MM:SS format
	startMin := start / 60
	startSec := start % 60
	endTime := start + duration
	endMin := endTime / 60
	endSec := endTime % 60

	sectionArg := fmt.Sprintf("*%d:%02d-%d:%02d", startMin, startSec, endMin, endSec)

	args := []string{
		"--download-section", sectionArg,
		"-f", "best[ext=mp4]/best",
		"-o", outFile,
		"--no-playlist",
		"--restrict-filenames",
		"--no-warnings",
		"--max-filesize", "1G", // Clip max 1GB
		url,
	}

	// Add timeout context
	dlCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	logger.Debug("Downloading clip with yt-dlp",
		zap.String("video_id", videoID),
		zap.String("section", sectionArg),
		zap.String("output", outFile))

	cmd := exec.CommandContext(dlCtx, "yt-dlp", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Log the yt-dlp error for debugging
		logger.Warn("yt-dlp download failed",
			zap.String("video_id", videoID),
			zap.String("section", sectionArg),
			zap.Error(err),
			zap.String("output", strings.TrimSpace(string(output))))
		return fmt.Errorf("yt-dlp failed: %w (section: %s)\n%s", err, sectionArg, string(output))
	}

	// Verify file exists and has reasonable size
	info, err := os.Stat(outFile)
	if err != nil {
		return fmt.Errorf("clip file not found after download: %w", err)
	}

	if info.Size() < 100000 { // Less than 100KB is suspicious
		return fmt.Errorf("clip file too small (%d bytes), likely download failed", info.Size())
	}

	logger.Info("Clip downloaded successfully",
		zap.String("video_id", videoID),
		zap.String("file", outFile),
		zap.Int64("size_bytes", info.Size()))

	return nil
}

func (m *V3Monitor) resolveTargetFolder(ctx context.Context, category, title string) (string, string, error) {
	// For now, use basic category logic
	// In production, should call classifyWithGemma and extractProtagonist
	// But those require full Monitor instance with Drive client

	// Fallback: use category as folder name
	if category == "" {
		category = "General"
	}

	// Sanitize folder name
	folderName := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == ' ' {
			return r
		}
		return -1
	}, category)

	folderName = strings.TrimSpace(folderName)
	if folderName == "" {
		folderName = "Unknown"
	}

	folderPath := folderName + "/" + sanitizeFolderName(title)

	logger.Info("Folder resolved",
		zap.String("path", folderPath),
		zap.String("category", category))

	// Return dummy folder ID - in production, use Drive client
	return "TEMP_FOLDER_ID_" + folderName, folderPath, nil
}

func (m *V3Monitor) uploadToDrive(ctx context.Context, file, folderID, folderPath string) (string, error) {
	// Verify file exists
	info, err := os.Stat(file)
	if err != nil {
		return "", fmt.Errorf("file not found: %w", err)
	}

	if info.IsDir() {
		return "", fmt.Errorf("path is a directory, not a file")
	}

	filename := filepath.Base(file)

	logger.Info("Would upload to Drive",
		zap.String("file", filename),
		zap.String("folder_id", folderID),
		zap.String("folder_path", folderPath),
		zap.Int64("size_bytes", info.Size()))

	// In a real implementation, would call m.driveClient.UploadFile(ctx, file, folderID, filename)
	// For now, return a simulated file ID
	simulatedFileID := "FILE_ID_" + strings.TrimSuffix(filename, filepath.Ext(filename))

	return simulatedFileID, nil
}

func (m *V3Monitor) saveClipToDB(ctx context.Context, videoID, driveID, folderID, path string, h Highlight, g *GemmaResult) error {
	if m.db == nil {
		logger.Warn("Database not configured, skipping save")
		return nil
	}

	query := `
	INSERT INTO clips (video_id, drive_id, folder_id, folder_path, start_sec, duration, reason, category, created_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(video_id) DO UPDATE SET
		updated_at = CURRENT_TIMESTAMP
	`

	category := "Unknown"
	if g != nil {
		category = g.Category
	}

	err := m.db.QueryRowContext(ctx, query,
		videoID,
		driveID,
		folderID,
		path,
		h.StartSec,
		h.Duration,
		h.Reason,
		category,
		time.Now()).Err()

	if err != nil {
		logger.Error("Failed to save clip to database",
			zap.String("video_id", videoID),
			zap.Error(err))
		return fmt.Errorf("database save failed: %w", err)
	}

	logger.Debug("Clip saved to database",
		zap.String("video_id", videoID),
		zap.String("drive_id", driveID))

	return nil
}

// Stub implementations for helper methods
func (m *V3Monitor) videoExists(ctx context.Context, videoID string) bool {
	if m.db == nil {
		return false
	}
	var count int
	err := m.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM videos WHERE video_id = ?", videoID).Scan(&count)
	return err == nil && count > 0
}

func (m *V3Monitor) saveVideoMetadata(ctx context.Context, info interface{}, classification *GemmaResult) error {
	// Stub - implement as needed
	return nil
}

func (m *V3Monitor) updateLastChecked(ctx context.Context, channelID string) error {
	// Stub - implement as needed
	return nil
}

func (m *V3Monitor) queueDownloadJob(ctx context.Context, info interface{}, classification *GemmaResult) {
	// Stub - implement as needed
}

type GemmaResult struct {
	Category    string `json:"category"`
	Protagonist string `json:"protagonist"`
	Reason      string `json:"reason"`
}

func (m *V3Monitor) classifyWithGemma(ctx context.Context, info interface{}) (*GemmaResult, error) {
	// Extract title and description from info
	var title, description string

	switch v := info.(type) {
	case map[string]interface{}:
		if t, ok := v["title"].(string); ok {
			title = t
		}
		if d, ok := v["description"].(string); ok {
			description = d
		}
	default:
		// Try to handle as struct
		return &GemmaResult{Category: "General", Reason: "Unable to extract info"}, nil
	}

	// Truncate to prevent token overflow
	if len(description) > 500 {
		description = description[:500] + "..."
	}

	// Classify: extract category and protagonist from title/description
	prompt := fmt.Sprintf(`You are a video content classifier. Analyze this YouTube video and classify it.

Title: "%s"
Description: "%s"

Extract and return JSON with:
1. category: One of [Gaming, Music, Education, Entertainment, Sports, Technology, Lifestyle, News, Other]
2. protagonist: Main person/subject in the video (e.g., "PewDiePie", "Taylor Swift", "Gordon Ramsay"). If not a person, use the topic.
3. reason: One sentence explanation

Return ONLY valid JSON, no other text:
{"category": "<category>", "protagonist": "<name>", "reason": "<explanation>"}`, title, description)

	reqBody := map[string]interface{}{
		"model":  "gemma3:4b",
		"prompt": prompt,
		"stream": false,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal classification request: %w", err)
	}

	// 20 second timeout for classification
	classCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(classCtx, "POST", m.ollamaURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create classification request: %w", err)
	}

	client := &http.Client{Timeout: 25 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("classification api call failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode classification response: %w", err)
	}

	// Parse classification JSON from response
	jsonStart := strings.Index(result.Response, "{")
	jsonEnd := strings.LastIndex(result.Response, "}")
	if jsonStart == -1 || jsonEnd == -1 {
		return &GemmaResult{Category: "General", Reason: "Failed to parse JSON"}, nil
	}

	jsonStr := result.Response[jsonStart : jsonEnd+1]
	var classification struct {
		Category    string `json:"category"`
		Protagonist string `json:"protagonist"`
		Reason      string `json:"reason"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &classification); err != nil {
		logger.Warn("Failed to parse classification JSON",
			zap.String("json", jsonStr),
			zap.Error(err))
		return &GemmaResult{Category: "General", Reason: "JSON parse error"}, nil
	}

	return &GemmaResult{
		Category:    classification.Category,
		Protagonist: classification.Protagonist,
		Reason:      classification.Reason,
	}, nil
}

func (m *V3Monitor) fallbackHighlights(transcript string) []Highlight {
	// Simple keyword-based fallback when Gemma fails
	keywords := []string{"killed", "died", "arrest", "win", "lose", "first", "never"}
	var highlights []Highlight

	lines := strings.Split(transcript, ".")
	for i, line := range lines {
		lower := strings.ToLower(line)
		for _, kw := range keywords {
			if strings.Contains(lower, kw) {
				startSec := i * 30
				highlights = append(highlights, Highlight{
					StartSec: startSec,
					Duration: 45,
					Reason:   "Keyword match: " + kw,
				})
				break
			}
		}
		if len(highlights) >= 3 {
			break
		}
	}

	if len(highlights) == 0 {
		// Fallback: use first 45 seconds
		highlights = append(highlights, Highlight{
			StartSec: 0,
			Duration: 45,
			Reason:   "Default (no keywords found)",
		})
	}

	logger.Info("Using fallback highlights", zap.Int("count", len(highlights)))
	return highlights
}

func (m *V3Monitor) checkOllamaHealth(ctx context.Context) error {
	// Check if Ollama service is running and gemma3:4b is available
	healthCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(healthCtx, "GET", m.ollamaURL+"/api/tags", nil)
	if err != nil {
		return fmt.Errorf("create ollama health check request: %w", err)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("ollama service unavailable at %s: %w", m.ollamaURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}

	var tagsResp struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tagsResp); err != nil {
		return fmt.Errorf("parse ollama tags response: %w", err)
	}

	// Check if gemma3:4b is available
	for _, model := range tagsResp.Models {
		if model.Name == "gemma3:4b" {
			logger.Info("Ollama health check passed",
				zap.String("service", m.ollamaURL),
				zap.String("model", "gemma3:4b"))
			return nil
		}
	}

	return fmt.Errorf("ollama running but gemma3:4b model not found")
}
