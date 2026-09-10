package videomuscles

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	coredl "github.com/Marcuss-ops/PipelineGen/internal/platform/downloader"
	fileutil "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
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
	// CutMode is the SINGLE media-operation decision resolved by the
	// canonical CutModeResolver (mediaexec) before this call. The pipeline
	// executes it verbatim: CutModeCopy stream-copies the source interval
	// straight to the final artifact; CutModeNormalize renders it exactly
	// once. Both operations are NEVER chained for one segment
	// (Sept 2026 single-pass contract). When empty (legacy caller), the
	// pipeline defaults to CutModeNormalize (fail-closed — the canonical
	// profile is mandatory).
	CutMode  mediaexec.CutMode
	Strategy string // verify (default), skip, replace
	// OutputDir is the target directory for the final clip.
	// When empty, falls back to DataDir/media/clips/general/{videoID}.
	OutputDir string
	// PreDownloadedPath is optional. When set, yt-dlp download is SKIPPED and
	// the clip is cut locally from this file (stream-copy when CutModeCopy,
	// one canonical render when CutModeNormalize). This enables the
	// "download once, cut N times" optimization.
	PreDownloadedPath string
	SkipMetadataFetch bool
}

// Pipeline represents the core video processing muscles.
// It orchestrates downloading via yt-dlp and rendering via FFmpeg.
type Pipeline struct {
	cfg         *config.Config
	log         *zap.Logger
	ytdlp       *coredl.YTDLPDownloader
	clipProcess ClipProcessor
}

// ClipProcessor is the execution port for YouTube media mechanics. The
// application owns download/cache/lifecycle policy; the injected adapter owns
// cutting and normalization execution. Watermark composition is NOT part of
// the ingest contract (Sept 2026): visual composition belongs to
// RenderingGen/Chronon, so ingest always produces clean source clips.
type ClipProcessor interface {
	CutCopy(context.Context, string, string, string, string, bool) error
	CutAndNormalize(context.Context, string, string, string, string, mediaexec.CutAndNormalizeOptions) error
}

// YouTubeCutResult wraps the output of a YouTube cut operation with the local file path
// and the full video metadata captured from yt-dlp.
type YouTubeCutResult struct {
	LocalPath string
	Metadata  *coredl.YouTubeMetadata
}

// NewPipeline creates a new video processing pipeline.
func NewPipeline(cfg *config.Config, log *zap.Logger, clipProcess ClipProcessor) *Pipeline {
	return &Pipeline{
		cfg:         cfg,
		log:         log,
		ytdlp:       coredl.NewYTDLP(cfg),
		clipProcess: clipProcess,
	}
}

// buildYouTubeSectionDownloadRequest keeps section acquisition stream-copy-only.
// The downloaded section may include keyframe padding; CutAndNormalize below
// trims that padding and performs the single canonical encode. Setting
// ForceKeyframes here would make yt-dlp invoke its own ffmpeg re-encode first,
// creating a hidden double encode before the canonical render.
func buildYouTubeSectionDownloadRequest(req YouTubeCutRequest, outputPath, section string, useCookies bool) *coredl.DownloadRequest {
	return &coredl.DownloadRequest{
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
func canonicalYouTubeCutOptions(keepAudio bool) mediaexec.CutAndNormalizeOptions {
	return mediaexec.CutAndNormalizeOptions{
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

	// 2. Metadata is optional for explicit clip extraction: the segment payload
	// already carries summary/topics/speakers and the canonical asset builder
	// persists them. Avoid putting a second yt-dlp subprocess on the critical
	// path when the caller explicitly opted out.
	var meta *coredl.YouTubeMetadata
	if !req.SkipMetadataFetch {
		meta, _ = p.ytdlp.GetVideoMetadata(ctx, req.URL)
	}

	// 3. Get the raw video file — either from a pre-downloaded source or via yt-dlp.
	//
	// Single-pass contract (Sept 2026): the CutModeResolver has ALREADY
	// decided copy-vs-render for this segment (req.CutMode). The executor
	// below performs EXACTLY ONE media operation per segment — never a
	// copy-then-render chain. This removes the old CutCopy→temp→
	// CutAndNormalize double pass that cost 1 extra temp file, 1 remux and
	// 1 extra full decode/encode per segment on the download-once path.
	if p.clipProcess == nil {
		return nil, fmt.Errorf("ffmpeg clip processor not configured")
	}

	if req.PreDownloadedPath != "" {
		p.log.Info("using pre-downloaded video, cutting locally",
			zap.String("source", req.PreDownloadedPath),
			zap.String("cut_mode", string(req.CutMode)))

		startStr := p.formatTime(req.Start)
		endStr := p.formatTime(req.Start + req.Duration)

		renderTimer := time.Now()
		var cutErr error
		if req.CutMode == mediaexec.CutModeCopy {
			// Stream-copy the segment straight to the final artifact: the
			// source is already canonical (resolver-verified conformance),
			// so no re-encode is needed.
			cutErr = p.clipProcess.CutCopy(ctx, req.PreDownloadedPath, outputPath, startStr, endStr, !req.KeepAudio)
		} else {
			// Single canonical render DIRECTLY from the full source (never
			// through a temp copy). The source interval is exact; encoder
			// selection is delegated to clipProcess (central VideoConfig
			// policy).
			cutErr = p.clipProcess.CutAndNormalize(ctx, req.PreDownloadedPath, outputPath, startStr, endStr, canonicalYouTubeCutOptions(req.KeepAudio))
		}

		status := "success"
		if cutErr != nil {
			status = "failed"
		}
		metrics.VideoRenderDuration.WithLabelValues(status, "false").Observe(time.Since(renderTimer).Seconds())
		metrics.VideoRenderTotal.WithLabelValues(status, "false").Inc()

		if cutErr != nil {
			p.log.Error("local cut from pre-downloaded source failed", zap.Error(cutErr))
			return nil, fmt.Errorf("failed to cut segment from pre-downloaded file: %w", cutErr)
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

		rawFile := segments[0].Path

		// 4. Process the downloaded section with ffmpeg.
		//
		// Every persisted YouTube clip must be materialized through the
		// canonical profile. yt-dlp's download section may include keyframe
		// padding, so the raw section is NOT itself a trustworthy clip:
		// bound the canonical render to the requested duration so the
		// persisted artifact, Drive object, and SQLite metadata agree
		// physically. Exactly ONE render — no copy chain. (The per-segment
		// download path is the fallback when download-once staging is
		// unavailable; the segment is always normalized here.)
		renderTimer := time.Now()
		normalizeErr := p.clipProcess.CutAndNormalize(ctx, rawFile, outputPath, "0", p.formatTime(req.Duration), canonicalYouTubeCutOptions(req.KeepAudio))

		status := "success"
		if normalizeErr != nil {
			status = "failed"
		}
		metrics.VideoRenderDuration.WithLabelValues(status, "false").Observe(time.Since(renderTimer).Seconds())
		metrics.VideoRenderTotal.WithLabelValues(status, "false").Inc()

		if normalizeErr != nil {
			p.log.Error("ffmpeg clip processing failed", zap.Error(normalizeErr))
			_ = os.Remove(rawFile)
			return nil, fmt.Errorf("video processing failed: %w", normalizeErr)
		}
		_ = os.Remove(rawFile)
	}

	// 5. (REMOVED — Sept 2026) The legacy YouTube watermark overlay
	// (ApplyWatermark + VELOX_YOUTUBE_WATERMARK_ENABLED) is gone from the
	// ingest path: watermark composition is owned by RenderingGen/Chronon,
	// and ingest must produce clean source clips. No second full re-encode
	// is ever performed here.

	p.log.Info("successfully processed youtube clip", zap.Duration("total_duration", time.Since(startTimer)))

	return &YouTubeCutResult{
		LocalPath: outputPath,
		Metadata:  meta,
	}, nil
}

func (p *Pipeline) hasYouTubeCookies() bool {
	if p == nil || p.cfg == nil {
		return strings.TrimSpace(os.Getenv("VELOX_YOUTUBE_COOKIES_FILE")) != ""
	}
	return p.cfg.External.ResolveYouTubeCookiesPath() != "" ||
		strings.TrimSpace(os.Getenv("VELOX_YOUTUBE_COOKIES_FILE")) != ""
}

// tempRawPath returns a unique temp file path for yt-dlp downloads.
// The random suffix prevents concurrent requests for the same video ID
// from colliding (e.g. normal download + no_audio download on same video).
func (p *Pipeline) tempRawPath(outputName string) string {
	return filepath.Join(p.cfg.Storage.TempPath(), fmt.Sprintf("raw_%s_%s.mp4", outputName, fileutil.RandomString(8)))
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
