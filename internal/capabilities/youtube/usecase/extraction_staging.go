// Package usecase — extraction_staging.go: optional "download once, cut N with ffmpeg -c copy"
// optimization for multi-segment YouTube extracts.
//
// Speed audit P0.1 (Sept 2026): every segment goroutine previously spawned its own
// `yt-dlp --download-sections "*HH:MM:SS-HH:MM:SS"` subprocess (60-70% of wall time on
// 9-clip batches, throttled by the CDN). The canonical pipeline already supports
// `YouTubeCutRequest.PreDownloadedPath` → local cut (see
// videomuscles/youtube_pipeline.go). This file wires the orchestrator side:
//
//   - When VELOX_YOUTUBE_DOWNLOAD_ONCE=true|1 (the default) AND the per-segment pipeline's
//     acquisition.SourceStager is wired, the full source is staged ONCE before fanout
//     (Prepare with empty DownloadSection). Each segment then cuts locally via
//     PreDownloadedPath (no per-segment network round-trip).
//
//   - Otherwise (flag off, stager nil, single-segment batch, prepare failure) the
//     helper returns a nil receipt and the fanout falls back to the per-segment yt-dlp
//     path (backwards compatible, zero behaviour change).
//
// Concurrency contract (Sept 2026): ExtractionService is a SHARED service across
// concurrent Extract() requests, so the staged file receipt must never live on the
// service struct (a second request could overwrite or release the first request's
// token). stageFullSourceOnce therefore returns the full acquisition.PrepareContext
// receipt; the caller keeps it on ITS OWN call stack and releases it in a deferred
// best-effort call after fanout completes. The FilesystemStager is already safe for
// concurrent access (keyed locking per stage ID), so no extra locking is needed here.
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
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
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

// ProbeSourceFacts probes the staged full source ONCE and returns the
// immutable facts the CutModeResolver needs. It is the single-probe owner
// of the extraction fanout (Sept 2026): segments never re-probe the source.
// A nil FFProbe port or any probe error returns (nil, err) — the caller
// degrades every segment to CutModeNormalize (fail-closed, no unproven
// stream-copy).
func (u *ProcessYouTubeSegmentUseCase) ProbeSourceFacts(ctx context.Context, localPath string) (*mediaexec.MediaFacts, error) {
	if u == nil || u.media.FFProbe == nil || localPath == "" {
		return nil, nil
	}
	facts, err := u.media.FFProbe.ProbeFacts(ctx, localPath)
	if err != nil {
		return nil, err
	}
	return facts, nil
}

// stageFullSourceOnce attempts to stage the full YouTube source once before fanout.
// Returns the acquisition receipt (prepared + stager) on success, (nil, nil) on any
// skip/failure (caller must fall back to per-segment download). The RECEIPT belongs
// to the caller's call stack: the caller MUST release it (best-effort) via
// stager.Release(ctx, prepared.CleanupToken) after the fanout completes. Nothing is
// stored on the shared ExtractionService (concurrency contract above).
func (s *ExtractionService) stageFullSourceOnce(ctx context.Context, req *youtubetypes.ExtractRequest, videoID string, segments []youtubetypes.Segment) (*acquisition.PrepareContext, acquisition.SourceStager) {
	if s == nil || req == nil || s.processSeg == nil {
		return nil, nil
	}
	if len(segments) < 2 {
		return nil, nil
	}
	if !isDownloadOnceEnabled() {
		return nil, nil
	}
	stager := s.processSeg.FullSourceStager()
	if stager == nil {
		if s.log != nil {
			s.log.Debug("youtube download-once requested but SourceStager not wired; falling back to per-segment yt-dlp",
				zap.String("video_id", videoID))
		}
		return nil, nil
	}
	url := strings.TrimSpace(req.URL)
	if url == "" {
		return nil, nil
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
		return nil, nil
	}
	if prepareCtx == nil || prepareCtx.LocalPath == "" {
		return nil, nil
	}
	if s.log != nil {
		s.log.Info("youtube download-once staged full source",
			zap.String("video_id", videoID),
			zap.String("local_path", prepareCtx.LocalPath),
			zap.Int64("size_bytes", prepareCtx.SizeBytes),
			zap.String("sha256", prepareCtx.SHA256))
	}
	return prepareCtx, stager
}

func isDownloadOnceEnabled() bool {
	v := strings.TrimSpace(os.Getenv("VELOX_YOUTUBE_DOWNLOAD_ONCE"))
	if v == "" {
		return true
	}
	if strings.EqualFold(v, "false") || v == "0" || strings.EqualFold(v, "off") || strings.EqualFold(v, "no") {
		return false
	}
	return true
}