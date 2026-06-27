package videomuscles

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	downloader "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	fileutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	pkgffmpeg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
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
	// OutputDir is the target directory for the final clip.
	// When empty, falls back to DataDir/media/clips/general/{videoID}.
	OutputDir string
	// PreDownloadedPath is optional. When set, yt-dlp download is SKIPPED and
	// the clip is cut locally from this file using ffmpeg -c copy (very fast).
	// This enables the "download once, cut N times" optimization.
	PreDownloadedPath string
}

// Pipeline represents the core video processing muscles.
// It orchestrates downloading via yt-dlp and rendering via FFmpeg.
type Pipeline struct {
	cfg         *config.Config
	log         *zap.Logger
	ytdlp       *downloader.YTDLPDownloader
	clipProcess *pkgffmpeg.Processor
}

// YouTubeCutResult wraps the output of a YouTube cut operation with the local file path
// and the full video metadata captured from yt-dlp.
type YouTubeCutResult struct {
	LocalPath string
	Metadata  *downloader.YouTubeMetadata
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
// Returns the local path and full YouTube metadata (title, description, tags, language, etc.).
func (p *Pipeline) DownloadAndCutYouTubeVideo(ctx context.Context, req YouTubeCutRequest) (*YouTubeCutResult, error) {
	startTimer := time.Now()
	p.log.Info("starting youtube download and cut", zap.String("url", req.URL), zap.String("video_id", req.VideoID))

	videoID := req.VideoID
	if videoID == "" {
		videoID = "unknown"
	}

	videoDir := req.OutputDir
	if videoDir == "" {
		videoDir = filepath.Join(p.cfg.Storage.DataDir, "media", "clips", "general", videoID)
	}
	if err := os.MkdirAll(videoDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output dir: %w", err)
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
			return &YouTubeCutResult{
				LocalPath: outputPath,
				Metadata:  nil,
			}, nil
		}
	}

	// 2. Fetch YouTube metadata FIRST (title, description, tags, language, chapters)
	//    This runs before the download so metadata is available even if download partially fails.
	meta, metaErr := p.ytdlp.GetVideoMetadata(ctx, req.URL)
	if metaErr != nil {
		p.log.Warn("failed to fetch YouTube metadata, continuing without it",
			zap.String("url", req.URL),
			zap.Error(metaErr))
	} else {
		p.log.Info("fetched YouTube metadata",
			zap.String("title", meta.Title),
			zap.Int("tags", len(meta.Tags)),
			zap.String("language", meta.Language),
			zap.Int("chapters", len(meta.Chapters)))
	}

	// 3. Get the raw video file — either from a pre-downloaded source or via yt-dlp
	var rawFile string

	if req.PreDownloadedPath != "" {
		p.log.Info("using pre-downloaded video, cutting locally with ffmpeg -c copy",
			zap.String("source", req.PreDownloadedPath))

		// Cut the specific segment from the pre-downloaded file using ffmpeg -c copy (instant)
		startStr := p.formatTime(req.Start)
		endStr := p.formatTime(req.Start + req.Duration)
		rawFile = filepath.Join(p.cfg.Storage.TempPath(), fmt.Sprintf("cut_%s.mp4", req.OutputName))

		if err := p.clipProcess.CutCopy(ctx, req.PreDownloadedPath, rawFile, startStr, endStr); err != nil {
			return nil, fmt.Errorf("failed to cut segment from pre-downloaded file: %w", err)
		}
	} else {
		// Download the specific section using yt-dlp
		tempVideoPath := filepath.Join(p.cfg.Storage.TempPath(), fmt.Sprintf("raw_%s.mp4", req.OutputName))

		startStr := p.formatTime(req.Start)
		endStr := p.formatTime(req.Start + req.Duration)
		section := fmt.Sprintf("*%s-%s", startStr, endStr)

		dlReq := &downloader.DownloadRequest{
			URL:              req.URL,
			OutputPath:       tempVideoPath,
			MergeFormat:      "mp4",
			DownloadSections: []string{section},
			ForceKeyframes:   req.ForceKeyframes,
			UseCookies:       p.hasYouTubeCookies(),
			Timeout:          10 * time.Minute,
		}

		downloadTimer := time.Now()
		segments, err := p.ytdlp.DownloadSections(ctx, dlReq)
		if err != nil {
			metrics.DownloadTotal.WithLabelValues("youtube", "failed").Inc()
			p.log.Error("ytdlp download failed", zap.Error(err))
			return nil, fmt.Errorf("failed to download youtube clip: %w", err)
		}
		metrics.DownloadDuration.WithLabelValues("youtube", "success").Observe(time.Since(downloadTimer).Seconds())
		metrics.DownloadTotal.WithLabelValues("youtube", "success").Inc()

		if len(segments) == 0 {
			return nil, fmt.Errorf("no segments downloaded")
		}

		rawFile = segments[0].Path
	}

	// 4. Normalize the downloaded clip with the shared ffmpeg clip processor.
	videoCfg := p.cfg.Video.WithDefaults()
	if p.clipProcess == nil {
		return nil, fmt.Errorf("ffmpeg clip processor not configured")
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
		return nil, fmt.Errorf("video processing failed: %w", normalizeErr)
	}

	// 5. Apply watermark overlay if watermark.png exists in the config directory
	//    The watermark is a green screen PNG that gets chroma-keyed out with 25% opacity.
	watermarkPath := "config/watermark.png"
	if _, wmErr := os.Stat(watermarkPath); wmErr == nil {
		p.log.Info("applying watermark overlay",
			zap.String("watermark", watermarkPath),
			zap.String("clip", outputPath))

		watermarkedPath := outputPath + ".wm.mp4"
		wmOpts := pkgffmpeg.DefaultWatermarkOptions(watermarkPath)
		wmOpts.Position = "center"
		wmOpts.Opacity = 0.25
		wmOpts.ScalePercent = 20

		watermarkErr := p.clipProcess.ApplyWatermark(ctx, outputPath, watermarkedPath, wmOpts)
		if watermarkErr != nil {
			p.log.Warn("watermark overlay failed, using clip without watermark",
				zap.Error(watermarkErr))
			os.Remove(watermarkedPath)
		} else {
			os.Remove(outputPath)
			os.Rename(watermarkedPath, outputPath)
			p.log.Info("watermark overlay applied successfully",
				zap.String("path", outputPath))
		}
	}

	// Cleanup
	_ = os.Remove(rawFile)

	p.log.Info("successfully processed youtube clip", zap.Duration("total_duration", time.Since(startTimer)))

	return &YouTubeCutResult{
		LocalPath: outputPath,
		Metadata:  meta,
	}, nil
}

func (p *Pipeline) hasYouTubeCookies() bool {
	cookiesPath := p.cfg.External.YouTubeCookiesPath
	if cookiesPath == "" {
		cookiesPath = "config/youtube_cookies.txt"
	}
	_, err := os.Stat(cookiesPath)
	return err == nil
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
