package channelmonitor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"velox/go-master/internal/youtube"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// downloadAndUploadClips downloads highlight clips from a YouTube video
// and uploads them to the specified Drive folder.
func (m *Monitor) downloadAndUploadClips(ctx context.Context, video youtube.SearchResult, highlights []HighlightSegment, folderID, folderPath string, _ bool, maxDuration int, decision CategoryDecision) ([]ClipResult, error) {
	if m.driveClient == nil {
		return nil, fmt.Errorf("drive client not configured")
	}

	var results []ClipResult
	stats := clipBatchStats{}
	tmpDir, err := os.MkdirTemp("", "channel-monitor-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Limit to 5 clips max
	maxClips := 5
	if len(highlights) < maxClips {
		maxClips = len(highlights)
	}

	for i := 0; i < maxClips; i++ {
		seg := highlights[i]
		clipName := fmt.Sprintf("clip_%s_%d", video.ID, i)
		clipFile := filepath.Join(tmpDir, clipName+".mp4")
		runKey := clipRunKey(video.ID, seg.StartSec, seg.EndSec)
		stats.attempted++
		if m.clipRunStore != nil && m.clipRunStore.Completed(runKey) {
			if rec, ok := m.clipRunStore.Get(runKey); ok {
				results = append(results, ClipResult{
					VideoID:      video.ID,
					VideoTitle:   video.Title,
					ClipFile:     rec.FileName,
					StartSec:     seg.StartSec,
					EndSec:       seg.EndSec,
					Duration:     seg.Duration,
					Description:  seg.Text,
					Confidence:   rec.Confidence,
					NeedsReview:  rec.NeedsReview,
					Status:       string(rec.Status),
					DriveFileID:  rec.DriveFileID,
					DriveFileURL: rec.DriveFileURL,
					TxtFileID:    rec.TxtFileID,
				})
				stats.reused++
				continue
			}
		}

		if m.clipRunStore != nil {
			_ = m.clipRunStore.Upsert(ClipRunRecord{
				RunKey:      runKey,
				VideoID:     video.ID,
				Title:       video.Title,
				FolderPath:  folderPath,
				Category:    decision.Category,
				Confidence:  decision.Confidence,
				NeedsReview: decision.NeedsReview,
				SegmentIdx:  i + 1,
				StartSec:    seg.StartSec,
				EndSec:      seg.EndSec,
				Duration:    seg.Duration,
				Status:      ClipRunStatusDownloading,
			})
		}

		// Download clip using yt-dlp with time range
		if err := m.downloadClipFn(ctx, video.ID, seg.StartSec, seg.Duration, clipFile); err != nil {
			logger.Warn("Failed to download clip",
				zap.String("video_id", video.ID),
				zap.Int("segment", i),
				zap.Error(err),
			)
			if m.clipRunStore != nil {
				_ = m.clipRunStore.MarkStatus(runKey, ClipRunStatusFailed, err.Error())
			}
			stats.downloadFailed++
			continue
		}

		// Check file exists and has reasonable size
		info, err := os.Stat(clipFile)
		if err != nil || info.Size() < 1000 {
			logger.Warn("Clip file too small or missing",
				zap.String("file", clipFile),
			)
			if m.clipRunStore != nil {
				_ = m.clipRunStore.MarkStatus(runKey, ClipRunStatusFailed, "clip too small or missing")
			}
			stats.downloadFailed++
			continue
		}

		renderStatus := ClipRunStatusRendered
		renderErr := renderClipTo1080p(ctx, clipFile, m.config.FFmpegPath)
		if renderErr != nil {
			logger.Warn("Failed to render clip to 1080p, keeping raw clip",
				zap.String("file", clipFile),
				zap.Error(renderErr),
			)
			renderStatus = ClipRunStatusNeedsReview
			stats.renderNeedsReview++
		}
		if m.clipRunStore != nil {
			_ = m.clipRunStore.MarkStatus(runKey, renderStatus, renderErrString(renderErr))
		}

		// Upload to Drive
		filename := sanitizeFolderName(video.Title) + fmt.Sprintf("_clip%d.mp4", i+1)
		driveFileID, err := m.driveClient.UploadFile(ctx, clipFile, folderID, filename)
		if err != nil {
			logger.Warn("Failed to upload clip to Drive",
				zap.String("video_id", video.ID),
				zap.Int("segment", i),
				zap.Error(err),
			)
			if m.clipRunStore != nil {
				_ = m.clipRunStore.MarkStatus(runKey, ClipRunStatusFailed, err.Error())
			}
			stats.uploadFailed++
			continue
		}

		driveFileURL := fmt.Sprintf("https://drive.google.com/file/d/%s/view", driveFileID)
		results = append(results, ClipResult{
			VideoID:      video.ID,
			VideoTitle:   video.Title,
			ClipFile:     filename,
			StartSec:     seg.StartSec,
			EndSec:       seg.EndSec,
			Duration:     seg.Duration,
			Description:  seg.Text,
			Confidence:   decision.Confidence,
			NeedsReview:  decision.NeedsReview || renderErr != nil,
			Status:       string(ClipRunStatusUploaded),
			DriveFileID:  driveFileID,
			DriveFileURL: driveFileURL,
		})

		if m.clipRunStore != nil {
			_ = m.clipRunStore.Upsert(ClipRunRecord{
				RunKey:       runKey,
				VideoID:      video.ID,
				Title:        video.Title,
				FolderPath:   folderPath,
				Category:     decision.Category,
				Confidence:   decision.Confidence,
				NeedsReview:  decision.NeedsReview || renderErr != nil,
				SegmentIdx:   i + 1,
				StartSec:     seg.StartSec,
				EndSec:       seg.EndSec,
				Duration:     seg.Duration,
				Status:       ClipRunStatusUploaded,
				FileName:     filename,
				DriveFileID:  driveFileID,
				DriveFileURL: driveFileURL,
				Error:        renderErrString(renderErr),
			})
		}
		stats.uploaded++
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("no clips could be downloaded/uploaded for video %s", video.ID)
	}

	txtFileID, err := uploadVideoSummaryText(ctx, m, tmpDir, video, results, folderID, decision)
	if err != nil {
		logger.Warn("Failed to upload video summary txt",
			zap.String("video_id", video.ID),
			zap.Error(err),
		)
	} else {
		for i := range results {
			results[i].TxtFileID = txtFileID
			if m.clipRunStore != nil {
				_ = m.clipRunStore.UpdateTxtFileID(video.ID, results[i].StartSec, results[i].EndSec, txtFileID)
			}
		}
	}

	if err := uploadVideoSummaryJSON(ctx, m, tmpDir, video, results, folderID, folderPath, decision, txtFileID); err != nil {
		logger.Warn("Failed to upload video summary json",
			zap.String("video_id", video.ID),
			zap.Error(err),
		)
	}

	logger.Info("Clip batch complete",
		zap.String("video_id", video.ID),
		zap.Int("attempted", stats.attempted),
		zap.Int("reused", stats.reused),
		zap.Int("uploaded", stats.uploaded),
		zap.Int("download_failed", stats.downloadFailed),
		zap.Int("render_needs_review", stats.renderNeedsReview),
		zap.Int("upload_failed", stats.uploadFailed),
		zap.String("folder_path", folderPath),
	)

	return results, nil
}

func uploadVideoSummaryText(ctx context.Context, m *Monitor, tmpDir string, video youtube.SearchResult, clips []ClipResult, folderID string, decision CategoryDecision) (string, error) {
	txtContent := buildVideoSummaryText(video, clips, decision)
	txtFile := filepath.Join(tmpDir, sanitizeFolderName(video.Title)+"_summary.txt")
	if err := os.WriteFile(txtFile, []byte(txtContent), 0644); err != nil {
		return "", err
	}
	txtFilename := sanitizeFolderName(video.Title) + "_summary.txt"
	txtFileID, err := m.driveClient.UploadFile(ctx, txtFile, folderID, txtFilename)
	if err != nil {
		return "", err
	}
	return txtFileID, nil
}

func uploadVideoSummaryJSON(ctx context.Context, m *Monitor, tmpDir string, video youtube.SearchResult, clips []ClipResult, folderID, folderPath string, decision CategoryDecision, txtFileID string) error {
	summary := ClipVideoSummary{
		VideoID:     video.ID,
		Title:       video.Title,
		FolderPath:  folderPath,
		Category:    decision.Category,
		Confidence:  decision.Confidence,
		NeedsReview: decision.NeedsReview,
		GeneratedAt: time.Now().UTC(),
		Clips:       make([]ClipVideoSummaryItem, 0, len(clips)),
	}
	for i, clip := range clips {
		summary.Clips = append(summary.Clips, ClipVideoSummaryItem{
			SegmentIdx:   i + 1,
			StartSec:     clip.StartSec,
			EndSec:       clip.EndSec,
			Duration:     clip.Duration,
			Confidence:   clip.Confidence,
			NeedsReview:  clip.NeedsReview,
			Status:       ClipRunStatus(clip.Status),
			DriveFileID:  clip.DriveFileID,
			DriveFileURL: clip.DriveFileURL,
			TxtFileID:    txtFileID,
		})
	}
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	jsonFile := filepath.Join(tmpDir, sanitizeFolderName(video.Title)+"_summary.json")
	if err := os.WriteFile(jsonFile, data, 0644); err != nil {
		return err
	}
	jsonFilename := sanitizeFolderName(video.Title) + "_summary.json"
	_, err = m.driveClient.UploadFile(ctx, jsonFile, folderID, jsonFilename)
	return err
}

func buildVideoSummaryText(video youtube.SearchResult, clips []ClipResult, decision CategoryDecision) string {
	title := strings.TrimSpace(video.Title)
	if title == "" {
		title = video.ID
	}
	var b strings.Builder
	fmt.Fprintf(&b, "📋 RIEPILOGO CLIP - %s\n", sanitizeFolderName(title))
	b.WriteString("================================================================================\n\n")
	fmt.Fprintf(&b, "🏷️  Categoria: %s\n", decision.Category)
	fmt.Fprintf(&b, "🎯 Confidence: %.2f\n", decision.Confidence)
	fmt.Fprintf(&b, "⚠️  Needs review: %t\n\n", decision.NeedsReview)
	fmt.Fprintf(&b, "📊 Totale clip: %d\n\n", len(clips))
	for i, clip := range clips {
		fmt.Fprintf(&b, "%d. [%s]\n", i+1, clip.ClipFile)
		fmt.Fprintf(&b, "   🔗 Link: %s\n", clip.DriveFileURL)
		fmt.Fprintf(&b, "   📁 File: %s\n", clip.ClipFile)
		fmt.Fprintf(&b, "   📊 Stato: ✅ Upload completato\n")
		fmt.Fprintf(&b, "   📝 Descrizione: %s\n", summarizeClipDescription(title, clip.Description))
		fmt.Fprintf(&b, "   🏷️  Tag: %s\n\n", guessClipTags(title, clip.Description))
	}
	return b.String()
}

func summarizeClipDescription(title, text string) string {
	base := strings.TrimSpace(text)
	if base == "" {
		base = "Segmento tratto dal video"
	}
	return fmt.Sprintf("La clip da '%s' mostra %s", title, base)
}

func guessClipTags(title, text string) string {
	lower := strings.ToLower(title + " " + text)
	tags := make([]string, 0, 5)
	add := func(tag string) {
		for _, existing := range tags {
			if existing == tag {
				return
			}
		}
		tags = append(tags, tag)
	}
	if strings.Contains(lower, "brain") || strings.Contains(lower, "science") || strings.Contains(lower, "podcast") {
		add("intervista")
		add("podcast")
		add("conversazione")
	}
	if strings.Contains(lower, "boxing") || strings.Contains(lower, "mayweather") || strings.Contains(lower, "fight") {
		add("boxing")
		add("sport")
	}
	if strings.Contains(lower, "music") || strings.Contains(lower, "rapper") {
		add("musica")
	}
	if len(tags) == 0 {
		add("various")
	}
	return strings.Join(tags, ", ")
}

func renderClipTo1080p(ctx context.Context, inputFile, ffmpegPath string) error {
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	outputFile := strings.TrimSuffix(inputFile, filepath.Ext(inputFile)) + ".rendered.mp4"
	args := []string{
		"-y",
		"-i", inputFile,
		"-vf", "scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2",
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-crf", "18",
		"-c:a", "aac",
		"-movflags", "+faststart",
		outputFile,
	}
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg render failed: %w\n%s", err, string(output))
	}
	if err := os.Rename(outputFile, inputFile); err != nil {
		return fmt.Errorf("replace rendered clip: %w", err)
	}
	return nil
}

func renderErrString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

type clipBatchStats struct {
	attempted         int
	reused            int
	uploaded          int
	downloadFailed    int
	renderNeedsReview int
	uploadFailed      int
}

// downloadClip downloads a segment of a YouTube video using yt-dlp
func (m *Monitor) downloadClip(ctx context.Context, videoID string, startSec, duration int, outputFile string) error {
	url := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)

	maxDuration := m.config.MaxClipDuration
	if maxDuration <= 0 {
		maxDuration = 60
	}
	if duration <= 0 || duration > maxDuration {
		duration = maxDuration
	}

	dlCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	return youtube.DownloadSection(dlCtx, youtube.SectionDownloadOptions{
		YtDlpPath:          m.config.YtDlpPath,
		URL:                url,
		OutputFile:         outputFile,
		StartSec:           startSec,
		Duration:           duration,
		MaxHeight:          1080,
		CookiesFile:        m.config.CookiesPath,
		DefaultCookiesFile: "",
		MaxFilesize:        "1G",
	})
}
