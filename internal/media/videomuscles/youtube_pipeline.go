package videomuscles

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"velox/go-master/internal/config"
	"velox/go-master/internal/media/ffmpeg"
	"velox/go-master/internal/pkg/media/downloader"
	"velox/go-master/internal/pkg/metrics"
	"velox/go-master/internal/pkg/video_spec"
)

// Pipeline represents the core video processing muscles.
// It orchestrates downloading via yt-dlp and rendering via FFmpeg.
type Pipeline struct {
	cfg      *config.Config
	log      *zap.Logger
	ytdlp    *downloader.YTDLPDownloader
	renderer *ffmpeg.Service
}

// NewPipeline creates a new video processing pipeline.
func NewPipeline(cfg *config.Config, log *zap.Logger, renderer *ffmpeg.Service) *Pipeline {
	return &Pipeline{
		cfg:      cfg,
		log:      log,
		ytdlp:    downloader.NewYTDLP(cfg),
		renderer: renderer,
	}
}

// DownloadAndCutYouTubeVideo downloads a specific section of a YouTube video and uses FFmpeg to process it.
func (p *Pipeline) DownloadAndCutYouTubeVideo(ctx context.Context, url string, start, duration float64, outputName string) (string, error) {
	startTimer := time.Now()
	p.log.Info("starting youtube download and cut", zap.String("url", url))

	videoID := extractYouTubeVideoID(url)
	if videoID == "" {
		videoID = "unknown"
	}
	videoDir := filepath.Join(p.cfg.Storage.YoutubeClipsPath(), "yt "+videoID)
	if err := os.MkdirAll(videoDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create output dir: %w", err)
	}

	// 1. Check Cache
	safeOutputName := filepath.Base(strings.TrimSpace(outputName))
	if safeOutputName == "." || safeOutputName == string(filepath.Separator) || safeOutputName == "" {
		safeOutputName = "clip"
	}
	outputPath := filepath.Join(videoDir, safeOutputName+".mp4")
	if ok, err := usableCachedClip(outputPath); err != nil {
		p.log.Warn("failed to inspect cached youtube clip", zap.String("path", outputPath), zap.Error(err))
	} else if ok {
		p.log.Info("cache hit for youtube clip", zap.String("path", outputPath))
		return outputPath, nil
	}

	// 2. Download the specific section using yt-dlp
	tempVideoPath := filepath.Join(p.cfg.Storage.TempPath(), fmt.Sprintf("raw_%s.mp4", outputName))

	// Create section string like "*00:01:20-00:01:35"
	startStr := p.formatTime(start)
	endStr := p.formatTime(start + duration)
	section := fmt.Sprintf("*%s-%s", startStr, endStr)

	req := &downloader.DownloadRequest{
		URL:              url,
		OutputPath:       tempVideoPath,
		MergeFormat:      "mp4",
		DownloadSections: []string{section},
		ForceKeyframes:   true,
		Timeout:          10 * time.Minute,
	}

	downloadTimer := time.Now()
	segments, err := p.ytdlp.DownloadSections(ctx, req)
	if err != nil {
		metrics.DownloadTotal.WithLabelValues("youtube", "failed").Inc()
		p.log.Error("ytdlp download failed", zap.Error(err))
		return "", fmt.Errorf("failed to download youtube clip: %w", err)
	}
	metrics.DownloadDuration.WithLabelValues("youtube", "success").Observe(time.Since(downloadTimer).Seconds())
	metrics.DownloadTotal.WithLabelValues("youtube", "success").Inc()

	if len(segments) == 0 {
		return "", fmt.Errorf("no segments downloaded")
	}

	rawFile := segments[0].Path

	// 3. Prepare video scene plan
	plan := video_spec.MediaTimelinePlan{
		Tracks: []video_spec.VideoTrack{
			{
				IsPrimary: true,
				Segments: []video_spec.VideoSegment{
					{
						Path:          rawFile,
						Start:         0,
						Duration:      duration,
						TimelineStart: 0,
					},
				},
			},
		},
		Output: video_spec.OutputConfig{
			Path:       outputPath,
			Width:      1920,
			Height:     1080,
			FPS:        30,
			CRF:        23,
			VideoCodec: "libx264",
			AudioCodec: "aac",
		},
	}

	planPath := filepath.Join(p.cfg.Storage.TempPath(), fmt.Sprintf("plan_%s.json", outputName))
	planData, _ := json.MarshalIndent(plan, "", "  ")
	_ = os.WriteFile(planPath, planData, 0644)

	// 4. Run FFmpeg rendering
	renderTimer := time.Now()
	err = p.renderer.RenderScene(ctx, planPath, outputPath)

	status := "success"
	if err != nil {
		status = "failed"
	}

	metrics.VideoRenderDuration.WithLabelValues(status, "false").Observe(time.Since(renderTimer).Seconds())
	metrics.VideoRenderTotal.WithLabelValues(status, "false").Inc()

	if err != nil {
		p.log.Error("ffmpeg execution failed", zap.Error(err))
		return "", fmt.Errorf("video processing failed: %w", err)
	}

	// Cleanup
	_ = os.Remove(rawFile)
	_ = os.Remove(planPath)

	p.log.Info("successfully processed youtube clip", zap.Duration("total_duration", time.Since(startTimer)))
	return outputPath, nil
}

func (p *Pipeline) formatTime(sec float64) string {
	d := time.Duration(sec * float64(time.Second))
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	ms := (d - s*time.Second) / time.Millisecond
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}

func extractYouTubeVideoID(inputURL string) string {
	if idx := strings.Index(inputURL, "v="); idx != -1 {
		rest := inputURL[idx+2:]
		if amp := strings.Index(rest, "&"); amp != -1 {
			rest = rest[:amp]
		}
		if rest != "" {
			return rest
		}
	}
	return ""
}

func usableCachedClip(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if !info.Mode().IsRegular() {
		_ = os.Remove(path)
		return false, nil
	}
	if info.Size() <= 0 {
		_ = os.Remove(path)
		return false, nil
	}
	return true, nil
}
