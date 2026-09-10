// Package usecase — extraction_staging.go: optional \"download once, cut N with ffmpeg -c copy\"
// optimization for multi-segment YouTube extracts.
//
// Speed audit P0.1 (Sept 2026): every segment goroutine previously spawned its own
// `yt-dlp --download-sections \"*HH:MM:SS-HH:MM:SS\"` subprocess (60-70% of wall time on
// 9-clip batches, throttled by the CDN). The canonical pipeline already supports
// `YouTubeCutRequest.PreDownloadedPath` → `ffmpeg -c copy` local cut (see
// videomuscles/youtube_pipeline.go). This file wires the orchestrator side:
//
//   - When VELOX_YOUTUBE_DOWNLOAD_ONCE=true|1 AND the per-segment pipeline's
//     acquisition.SourceStager is wired, the full source is staged ONCE before fanout
//     (Prepare with empty DownloadSection). Each segment then cuts locally via
//     PreDownloadedPath (very fast, no network).
//
//   - Otherwise (flag off, stager nil, single-segment batch, prepare failure) the
//     helper returns \"\" and the fanout falls back to the per-segment yt-dlp path
//     (backwards compatible, zero behaviour change).
//
// The staged file lives with a 24h TTL (FilesystemStager default) and is released
// best-effort after fanout completes so a hot retry within the TTL hits the cache.
package usecase

import (
	"context"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
)

// FullSourceStager accessor — exposed for extraction_staging to read the stager
// without breaking encapsulation. Returns nil when the use case is nil.
func (u *ProcessYouTubeSegmentUseCase) FullSourceStager() acquisition.SourceStager {
	if u == nil {
		return nil
	}
	return u.media.Stager
}

// stageFullSourceOnce attempts to stage the full YouTube source once before fanout.
// Returns the local path on success, empty string on any skip/failure (caller must
// fall back to per-segment download). The caller is responsible for releasing the
// staged file after fanout completes (via releaseStagedSource).
func (s *ExtractionService) stageFullSourceOnce(ctx context.Context, req *youtubetypes.ExtractRequest, videoID string, segments []youtubetypes.Segment) string {
	if s == nil || req == nil || s.processSeg == nil {
		return ""
	}
	if len(segments) < 2 {
		return ""
	}
	enabled := isDownloadOnceEnabled()
	if !enabled {
		return ""
	}
	stager := s.processSeg.FullSourceStager()
	if stager == nil {
		if s.log != nil {
			s.log.Debug("youtube download-once requested but SourceStager not wired; falling back to per-segment yt-dlp",
				zap.String("video_id", videoID))
		}
		return ""
	}
	url := strings.TrimSpace(req.URL)
	if url == "" {
		return ""
	}
	prepareCtx, err := stager.Prepare(ctx, acquisition.PrepareRequest{
		Source: acquisition.SourceRef{
			URL:               url,
			DownloadSection:   "",
			MergeFormat:       "mp4",
			PolicyVersion:     ProcessSegmentPolicyVersion,
			SuggestedFilename: videoID + ".mp4",
		},
		CallerRef:      "youtube.extract.download-once:" + videoID,
		IdempotencyKey: acquisition.DeriveIdempotencyKey(acquisition.SourceRef{URL: url, PolicyVersion: ProcessSegmentPolicyVersion}),
		Timeout:        12 * time.Minute,
		TTL:            24 * time.Hour,
	})
	if err != nil {
		if s.log != nil {
			s.log.Warn("youtube download-once prepare failed; falling back to per-segment yt-dlp",
				zap.String("video_id", videoID), zap.Error(err))
		}
		return ""
	}
	if prepareCtx == nil || prepareCtx.LocalPath == "" {
		return ""
	}
	if s.log != nil {
		s.log.Info("youtube download-once staged full source",
			zap.String("video_id", videoID),
			zap.String("local_path", prepareCtx.LocalPath),
			zap.Int64("size_bytes", prepareCtx.SizeBytes),
			zap.String("sha256", prepareCtx.SHA256))
	}
	// Record token for deferred release after fanout (best-effort, no error propagation).
	s.recordStagedToken(prepareCtx.CleanupToken, stager)
	return prepareCtx.LocalPath
}

func isDownloadOnceEnabled() bool {
	v := strings.TrimSpace(os.Getenv("VELOX_YOUTUBE_DOWNLOAD_ONCE"))
	return strings.EqualFold(v, "true") || v == "1"
}

func (s *ExtractionService) recordStagedToken(token string, stager acquisition.SourceStager) {
	if token == "" || stager == nil || s == nil {
		return
	}
	// Per-extraction token is kept on the service instance so concurrent extracts
	// for different videoIDs do not clobber each other via a package global.
	// The release happens once per extractFanOut; the token lives only for that call.
	s.stagedToken = token
	s.stagedStager = stager
}

// releaseStagedSources is called after fanout Wait() to clean up the full-source
// staged file. Best-effort: TTL GC would clean it anyway within 24h.
func (s *ExtractionService) releaseStagedSources(ctx context.Context) {
	if s == nil || s.stagedToken == "" || s.stagedStager == nil {
		return
	}
	if err := s.stagedStager.Release(ctx, s.stagedToken); err != nil && s.log != nil {
		s.log.Debug("youtube download-once release after fanout", zap.Error(err))
	}
	s.stagedToken = ""
	s.stagedStager = nil
}
