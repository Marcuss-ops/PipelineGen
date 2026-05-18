package videomuscles

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"

	"velox/go-master/pkg/config"
	"velox/go-master/pkg/metrics"
	"velox/go-master/pkg/media/downloader"
	"velox/go-master/pkg/tachyon_spec"
	"velox/go-master/internal/service/tachyon"
)

// Pipeline represents the core video processing muscles.
// It orchestrates downloading via yt-dlp and rendering via TACHYON.
type Pipeline struct {
	cfg            *config.Config
	log            *zap.Logger
	ytdlp          *downloader.YTDLPDownloader
	tachyonService *tachyon.Service
}

// NewPipeline creates a new video processing pipeline.
func NewPipeline(cfg *config.Config, log *zap.Logger, tachyonSvc *tachyon.Service) *Pipeline {
	return &Pipeline{
		cfg:            cfg,
		log:            log,
		ytdlp:          downloader.NewYTDLP(cfg),
		tachyonService: tachyonSvc,
	}
}

// DownloadAndCutYouTubeVideo downloads a specific section of a YouTube video and uses TACHYON to process it.
func (p *Pipeline) DownloadAndCutYouTubeVideo(ctx context.Context, url string, start, duration float64, outputName string) (string, error) {
	startTimer := time.Now()
	p.log.Info("starting youtube download and cut", zap.String("url", url))

	// 1. Check Cache
	outputPath := filepath.Join(p.cfg.Storage.YoutubeClipsPath(), fmt.Sprintf("%s.mp4", outputName))
	if _, err := os.Stat(outputPath); err == nil {
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

	// 3. Prepare TACHYON Scene Plan
	plan := tachyon_spec.MediaTimelinePlan{
		Tracks: []tachyon_spec.VideoTrack{
			{
				IsPrimary: true,
				Segments: []tachyon_spec.VideoSegment{
					{
						Path:          rawFile,
						Start:         0,
						Duration:      duration,
						TimelineStart: 0,
					},
				},
			},
		},
		Output: tachyon_spec.OutputConfig{
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

	// 4. Run TACHYON rendering
	renderTimer := time.Now()
	err = p.tachyonService.RenderScene(ctx, planPath, outputPath)
	
	status := "success"
	if err != nil {
		status = "failed"
	}
	
	metrics.TachyonRenderDuration.WithLabelValues(status, "false").Observe(time.Since(renderTimer).Seconds())
	metrics.TachyonRenderTotal.WithLabelValues(status, "false").Inc()

	if err != nil {
		p.log.Error("tachyon execution failed", zap.Error(err))
		return "", fmt.Errorf("tachyon processing failed: %w", err)
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
