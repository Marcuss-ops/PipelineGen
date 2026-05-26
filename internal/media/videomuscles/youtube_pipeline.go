package videomuscles

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"velox/go-master/internal/config"
	"velox/go-master/internal/pkg/fileutil"
	"velox/go-master/internal/pkg/media/downloader"
	pkgffmpeg "velox/go-master/internal/pkg/media/ffmpeg"
	"velox/go-master/internal/pkg/metrics"
)

// YouTubeCutRequest contains all parameters for downloading and cutting a YouTube clip.
type YouTubeCutRequest struct {
	URL            string
	VideoID        string
	Start          float64
	Duration       float64
	OutputName     string
	ForceKeyframes bool
	KeepAudio      bool
	Normalize      bool
	Strategy       string // verify (default), skip, replace
}

// Pipeline represents the core video processing muscles.
// It orchestrates downloading via yt-dlp and rendering via FFmpeg.
type Pipeline struct {
	cfg         *config.Config
	log         *zap.Logger
	ytdlp       *downloader.YTDLPDownloader
	clipProcess *pkgffmpeg.Processor
}

// NewPipeline creates a new video processing pipeline.
func NewPipeline(cfg *config.Config, log *zap.Logger, clipProcess *pkgffmpeg.Processor) *Pipeline {
	return &Pipeline{
		cfg:         cfg,
		log:         log,
		ytdlp:       downloader.NewYTDLP(cfg),
		clipProcess: clipProcess,
	}
}

// DownloadAndCutYouTubeVideo downloads a specific section of a YouTube video and uses FFmpeg to process it.
func (p *Pipeline) DownloadAndCutYouTubeVideo(ctx context.Context, req YouTubeCutRequest) (string, error) {
	startTimer := time.Now()
	p.log.Info("starting youtube download and cut", zap.String("url", req.URL), zap.String("video_id", req.VideoID))

	videoID := req.VideoID
	if videoID == "" {
		videoID = "unknown"
	}
	// Follow the new pattern: youtube/<group>/<gen_id>
	// We use "general" as the default group for now.
	videoDir := filepath.Join(p.cfg.Storage.DataDir, "media", "youtube", "general", videoID)
	if err := os.MkdirAll(videoDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create output dir: %w", err)
	}

	// 1. Check Cache
	safeOutputName := filepath.Base(strings.TrimSpace(req.OutputName))
	if safeOutputName == "." || safeOutputName == string(filepath.Separator) || safeOutputName == "" {
		safeOutputName = "clip"
	}
	outputPath := filepath.Join(videoDir, safeOutputName+".mp4")

	// Strategy: replace always skips cache
	if req.Strategy != "replace" {
		if ok, err := fileutil.UsableCachedClip(outputPath); err != nil {
			p.log.Warn("failed to inspect cached youtube clip", zap.String("path", outputPath), zap.Error(err))
		} else if ok {
			p.log.Info("cache hit for youtube clip", zap.String("path", outputPath), zap.String("strategy", req.Strategy))
			return outputPath, nil
		}
	}

	// 2. Download the specific section using yt-dlp
	tempVideoPath := filepath.Join(p.cfg.Storage.TempPath(), fmt.Sprintf("raw_%s.mp4", req.OutputName))

	// Create section string like "*00:01:20-00:01:35"
	startStr := p.formatTime(req.Start)
	endStr := p.formatTime(req.Start + req.Duration)
	section := fmt.Sprintf("*%s-%s", startStr, endStr)

	dlReq := &downloader.DownloadRequest{
		URL:              req.URL,
		OutputPath:       tempVideoPath,
		MergeFormat:      "mp4",
		DownloadSections: []string{section},
		ForceKeyframes:   req.ForceKeyframes,
		Timeout:          10 * time.Minute,
	}

	downloadTimer := time.Now()
	segments, err := p.ytdlp.DownloadSections(ctx, dlReq)
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

	// 3. Normalize the downloaded clip with the shared ffmpeg clip processor.
	videoCfg := p.cfg.Video.WithDefaults()
	if p.clipProcess == nil {
		return "", fmt.Errorf("ffmpeg clip processor not configured")
	}

	renderTimer := time.Now()
	normalizeErr := p.clipProcess.CutAndNormalize(ctx, rawFile, outputPath, "", "", pkgffmpeg.CutAndNormalizeOptions{
		Width:   videoCfg.Width,
		Height:  videoCfg.Height,
		FPS:     videoCfg.FPS,
		Codec:   videoCfg.Codec,
		Preset:  videoCfg.Preset,
		CRF:     videoCfg.CRF,
		NoAudio: !req.KeepAudio,
	})

	status := "success"
	if normalizeErr != nil {
		status = "failed"
	}

	metrics.VideoRenderDuration.WithLabelValues(status, "false").Observe(time.Since(renderTimer).Seconds())
	metrics.VideoRenderTotal.WithLabelValues(status, "false").Inc()

	if normalizeErr != nil {
		p.log.Error("ffmpeg clip processing failed", zap.Error(normalizeErr))
		return "", fmt.Errorf("video processing failed: %w", normalizeErr)
	}

	// Cleanup
	_ = os.Remove(rawFile)

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
