// Package usecase — process_segment_step3to5.go: canonical owner of
// Steps 3-5 (cut / retry / runtime fail-closed) + Step 4a pre-stage
// (SourceStager wiring, July 2026 wire-up).
//
// godlike/06 SSOT (one canonical owner per fact):
//   - Step 3 cut request shape (`youtubeports.VideoCutRequest`) lives ONLY here
//   - Step 4 retry.Do + RetryOptions{MaxAttempts=3, InitialBackoff=2s,
//     MaxBackoff=30s, IsRetryable=IsTransientExtractionError} lives ONLY here
//   - Step 4a shared SourceStager pre-stage + defer best-effort Cleanup
//     lives ONLY here (NIT-1 from the verification thinker: the `defer`
//     fires at the end of step3to5 (not Execute) — SAFE because the
//     staged source file is unused by Steps 5a-10 which only touch
//     the cut clip via localPath)
//   - Step 5 fail-closed gates (EmptyLocalPath / InvalidLocalArtifact
//     / HashFailed) live ONLY here
//
// godlike/07 min-blast-radius: the 4-step body preserves every inline
// detail verbatim (cutReq literal, retry.Do arg shape, per-iteration
// os.Remove, stat gate conditions, MD5File error classification).
// No behavior drift.
package usecase

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	retry "github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// step3to5_CutRetryHash returns (fileHash, localPath, err) after running
// the canonical Step 3 + Step 4 + Step 4a + Step 5 sequence:
//
//   - Pre-flight: video pipeline port nil-guard (typed error)
//   - Cut request composition (Step 3) with cmd.VideoURL / cmd.Strategy
//   - Step 4a: optional SourceStager pre-stage + defer best-effort cleanup
//     (NIT-1 from the verification thinker: cleanup runs at the end of
//     this method, after `retry.Do` returns → SAFE)
//   - Step 4: retry.Do{IsRetryable=IsTransientExtractionError} — 3 attempts
//     with 2s→30s exponential backoff, per-attempt os.Remove of any
//     stale partial slice in cmd.OutDir
//   - Step 5: runtime fail-closed on localPath (empty / missing / zero
//     size / hash failure) with typed *ExtractionError propagation
//
// Mutates `out` on success (Item.LocalPath / Item.Filename / Item.FileHash /
// FileHash → top-level out.FileHash). On any typed error path,
// `u.fail` has already populated out.Item.Status="failed" +
// out.Item.Error + out.Error.
//
// godlike/07 no-fake-availability: the Stager pre-stage is best-effort —
// on Stager.StageSource error, the pipeline LOGS Warn and continues with
// the legacy per-segment yt-dlp (no call site locked out). No silent
// failure: the Warn log surfaces to operator dashboards.
func (u *ProcessYouTubeSegmentUseCase) step3to5_CutRetryHash(
	ctx context.Context,
	cmd youtubetypes.ProcessSegmentCommand,
	out *youtubetypes.ProcessSegmentResult,
	clipID string,
	startSec int,
	endSec int,
	duration int,
	keepAudio bool,
) (fileHash string, localPath string, err error) {
	// policyVer is NOT threaded into step3to5: it's only consumed
	// downstream by Step 9 (buildClipAsset filename) + Step 10
	// (metadata writer audit-pin) via step6to9. Threading it here
	// would be a phantom param (godlike/07 minimum-blast-radius: no
	// YAGNI hooks; if a future step needs it, the orchestrator can
	// re-introduce it at that seam).

	// Step 3 — coords into VideoPipeline for cut/download.
	normalize := true
	if cmd.Normalize != nil {
		normalize = *cmd.Normalize
	}
	cutReq := youtubeports.VideoCutRequest{
		URL:            cmd.VideoURL,
		VideoID:        cmd.VideoID,
		Start:          float64(startSec),
		Duration:       float64(duration),
		OutputName:     strings.TrimSuffix(out.Item.Filename, ".mp4"),
		ForceKeyframes: cmd.ForceKeyframes,
		KeepAudio:      keepAudio,
		Normalize:      normalize,
		Strategy:       string(cmd.Strategy),
		OutputDir:      cmd.OutDir,
		// Explicit segment requests already carry the authoritative summary,
		// topics and speakers. Do not spawn a best-effort yt-dlp metadata
		// subprocess before every clip download.
		SkipMetadataFetch: true,
	}

	// Step 4 — retry download with exponential backoff. Pre-flight guard.
	if u.core.VideoPipeline == nil {
		typed := NewExtractionError(FailureCodeVideoProcessingFailed, false, "video pipeline port not wired", nil)
		return "", "", u.fail(out, typed)
	}

	// Step 4a — shared SourceStager pre-stage.
	//
	// When the Stager port is wired, download the FULL source video via the
	// shared SourceStager port BEFORE the retry loop. CutRequest.PreDownloadedPath
	// is set so the concrete VideoPipeline (videomuscles/youtube_pipeline.go:124-133)
	// SKIPS the yt-dlp download slice and uses ffmpeg -c copy on the local file —
	// saving N yt-dlp calls for the retry loop (the retry consumes the SAME
	// staged file).
	//
	// Graceful degradation: stager.StageSource failure is logged Warn and the
	// cutReq keeps PreDownloadedPath=""; the pipeline may retry acquisition.
	// All strategies, including YouTube Stock partial, use this same boundary.
	//
	// NIT-1 from the verification thinker: the `defer` fires at the end of
	// this method (after the typed error check on retry.Do) — SAFE because
	// the staged source file is consumed by the cut within Step 4 and is
	// unused by Steps 5a-10 which only touch the cut clip via localPath.
	if u.media.Stager != nil && cmd.VideoURL != "" {
		source := acquisition.SourceRef{URL: cmd.VideoURL, PolicyVersion: ProcessSegmentPolicyVersion}
		staged, stageErr := u.media.Stager.Prepare(ctx, acquisition.PrepareRequest{
			Source:         source,
			IdempotencyKey: "youtube.segment." + acquisition.DeriveIdempotencyKey(source),
			CallerRef:      "youtube.process_segment",
		})
		if stageErr != nil {
			u.core.Log.Warn("shared SourceStager pre-stage failed (continuing with legacy per-segment yt-dlp)",
				zap.String("clip_id", clipID),
				zap.String("video_url", cmd.VideoURL),
				zap.Error(stageErr))
		} else {
			if staged == nil || !staged.HasLocal() {
				u.core.Log.Warn("shared acquisition stager returned no local path",
					zap.String("clip_id", clipID),
					zap.String("video_url", cmd.VideoURL))
			} else {
				cutReq.PreDownloadedPath = staged.LocalPath
				u.core.Log.Info("shared acquisition SourceStager pre-staged full video for -c copy slicing",
					zap.String("clip_id", clipID),
					zap.String("video_url", cmd.VideoURL),
					zap.String("local_path", staged.LocalPath),
					zap.Int64("bytes", staged.SizeBytes))
				defer func(cleanupToken string) {
					if cleanupErr := u.media.Stager.Release(context.WithoutCancel(ctx), cleanupToken); cleanupErr != nil {
						u.core.Log.Warn("shared acquisition release failed (best-effort)",
							zap.String("local_path", staged.LocalPath),
							zap.Error(cleanupErr))
					}
				}(staged.CleanupToken)
			}
		}
	}

	var dlResult *youtubeports.VideoCutResult
	err = retry.Do(ctx, func() error {
		// Best-effort pre-cleanup of any stale partial slice from a prior
		// attempt: missing-file is the canonical no-op success (the file
		// never existed or already wiped); any other remove error is logged
		// Debug and ignored so the retry loop proceeds (godlike/07
		// NO-FAKE-AVAILABILITY: cleanup is sanitized, not silent).
		if rmStale := filepath.Join(cmd.OutDir, out.Item.Filename); rmStale != "" {
			if rmErr := os.Remove(rmStale); rmErr != nil && !os.IsNotExist(rmErr) {
				if u.core.Log != nil {
					u.core.Log.Debug("retry pre-cleanup failed (best-effort, proceeding)",
						zap.String("path", rmStale),
						zap.Error(rmErr))
				}
			}
		}
		var dlErr error
		dlResult, dlErr = u.core.VideoPipeline.DownloadAndCutYouTubeVideo(ctx, cutReq)
		return dlErr
	}, retry.RetryOptions{
		MaxAttempts:    3,
		InitialBackoff: 2 * time.Second,
		MaxBackoff:     30 * time.Second,
		IsRetryable:    IsTransientExtractionError,
	})
	if err != nil {
		typed := NewExtractionError(
			FailureCodeVideoProcessingFailed,
			IsTransientExtractionError(err),
			fmt.Sprintf("video processing failed: %v", err),
			err,
		)
		return "", "", u.fail(out, typed)
	}

	// Step 5 — runtime fail-closed (Commit 2/6 #5): empty path,
	// missing/zero-size file, and hash failure all fail-closed
	// with the typed ExtractionError taxonomy.
	if dlResult != nil {
		localPath = dlResult.LocalPath
	}
	if localPath == "" {
		typed := NewExtractionError(FailureCodeEmptyLocalPath, false,
			"video pipeline returned empty LocalPath", nil)
		return "", "", u.fail(out, typed)
	}
	if stat, statErr := os.Stat(localPath); statErr != nil || stat.Size() == 0 {
		typed := NewExtractionError(FailureCodeInvalidLocalArtifact, false,
			fmt.Sprintf("local artifact %q missing or zero-size (stat_err=%v)", localPath, statErr),
			statErr)
		return "", "", u.fail(out, typed)
	}
	if u.core.Hash != nil {
		var hashErr error
		fileHash, hashErr = u.core.Hash.MD5File(localPath)
		if hashErr != nil || fileHash == "" {
			typed := NewExtractionError(FailureCodeHashFailed, false,
				fmt.Sprintf("hash.MD5File failed for %q (err=%v)", localPath, hashErr),
				hashErr)
			return "", "", u.fail(out, typed)
		}
	}
	out.Item.LocalPath = localPath
	out.Item.Filename = filepath.Base(localPath)
	out.Item.FileHash = fileHash
	out.FileHash = fileHash
	return fileHash, localPath, nil
}
