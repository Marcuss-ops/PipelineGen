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
	ffmpegtypes "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg/types"
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

// buildYouTubeSectionDownloadRequest keeps section acquisition stream-copy-only.
// The downloaded section may include keyframe padding; CutAndNormalize below
// trims that padding and performs the single canonical encode. Setting
// ForceKeyframes here would make yt-dlp invoke its own ffmpeg re-encode first,
// creating a hidden double encode before the canonical render.
func buildYouTubeSectionDownloadRequest(req YouTubeCutRequest, outputPath, section string, useCookies bool) *downloader.DownloadRequest {
	return &downloader.DownloadRequest{
		URL:              req.URL,
		OutputPath:       outputPath,
		MergeFormat:      "mp4",
		DownloadSections: []string{section},
		ForceKeyframes:   false,
		UseCookies:       useCookies,
		Timeout:          10 * time.Minute,
	}
}

// canonicalYouTubeCutOptions keeps encoder selection in the FFmpeg processor's
// configured policy. The videomuscles application owns the clip intent (duration
// and audio), but must not select a concrete video encoder such as libx264.
func canonicalYouTubeCutOptions(keepAudio bool) pkgffmpeg.CutAndNormalizeOptions {
	return pkgffmpeg.CutAndNormalizeOptions{
		NoAudio: !keepAudio,
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
		rawFile = p.tempCutPath(req.OutputName)

		if err := p.clipProcess.CutCopy(ctx, req.PreDownloadedPath, rawFile, startStr, endStr, false); err != nil {
			return nil, fmt.Errorf("failed to cut segment from pre-downloaded file: %w", err)
		}
	} else {
		// Download the specific section using yt-dlp
		tempVideoPath := p.tempRawPath(req.OutputName)

		startStr := p.formatTime(req.Start)
		endStr := p.formatTime(req.Start + req.Duration)
		section := fmt.Sprintf("*%s-%s", startStr, endStr)

		dlReq := buildYouTubeSectionDownloadRequest(req, tempVideoPath, section, p.hasYouTubeCookies())

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

	// 4. Process the downloaded clip with ffmpeg.
	//
	// Every persisted YouTube clip must be materialized through the
	// canonical profile. Normalize=false remains accepted at the port for
	// compatibility, but is no longer allowed to select a stream-copy
	// output.
	if p.clipProcess == nil {
		return nil, fmt.Errorf("ffmpeg clip processor not configured")
	}
	if !req.Normalize {
		p.log.Warn("normalization override ignored; canonical clip profile is mandatory",
			zap.String("video_id", videoID))
		req.Normalize = true
	}

	renderTimer := time.Now()
	var normalizeErr error
	if req.Normalize {
		// yt-dlp's download section may include keyframe padding.  The raw
		// file is therefore not itself a trustworthy 4-second clip.  Bound
		// the canonical render to the requested duration so the persisted
		// artifact, Drive object, and SQLite metadata agree physically.
		// Encoder selection is intentionally delegated to clipProcess, which
		// was constructed from the central VideoConfig policy.
		normalizeErr = p.clipProcess.CutAndNormalize(ctx, rawFile, outputPath, "0", p.formatTime(req.Duration), canonicalYouTubeCutOptions(req.KeepAudio))
	} else {
		// Raw fetch: stream-copy the already-cut segment — no re-encode.
		// CutCopy with empty start/end is a pure container remux.
		normalizeErr = p.clipProcess.CutCopy(ctx, rawFile, outputPath, "", "", !req.KeepAudio)
	}

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
		wmOpts := ffmpegtypes.DefaultWatermarkOptions(watermarkPath)
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
	if p == nil || p.cfg == nil {
		return false
	}
	return p.cfg.External.ResolveYouTubeCookiesPath() != ""
}

// tempRawPath returns a unique temp file path for yt-dlp downloads.
// The random suffix prevents concurrent requests for the same video ID
// from colliding (e.g. normal download + no_audio download on same video).
func (p *Pipeline) tempRawPath(outputName string) string {
	return filepath.Join(p.cfg.Storage.TempPath(), fmt.Sprintf("raw_%s_%s.mp4", outputName, fileutil.RandomString(8)))
}

// tempCutPath returns a unique temp file path for pre-downloaded video cuts.
// Same random-suffix contract as tempRawPath.
func (p *Pipeline) tempCutPath(outputName string) string {
	return filepath.Join(p.cfg.Storage.TempPath(), fmt.Sprintf("cut_%s_%s.mp4", outputName, fileutil.RandomString(8)))
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
