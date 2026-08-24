package voiceover

import (
	"context"
	"encoding/json"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"go.uber.org/zap"
)

// buildCachedResult constructs a VoiceoverItemResult from a cache hit
// without running TTS, audio post-processing, Drive upload, or DB
// finalize. The result carries the cached DriveFileID, DriveLink,
// DownloadLink, LocalPath, DurationMs, and Voice from the DB row.
//
// When the metadata column carries timing links, a VoiceoverTimingResult
// is reconstructed so downstream consumers (script binding, docs render)
// receive the same shape as a cold-run result. Word-level timing data
// (the SpeechTimingArtifact) is NOT reconstructed — only the summary
// fields and links are hydrated; consumers that need per-word timing
// must download the timing.json artifact from Drive.
func buildCachedResult(cmd *ProcessSegmentCommand, hit *VoiceoverCacheHit, timingPolicy audio.TimingRequest, log *zap.Logger) *VoiceoverItemResult {
	out := &VoiceoverItemResult{
		Language:      cmd.Language,
		Voice:         hit.Voice,
		Filename:      hit.Filename,
		ID:            hit.ID,
		Status:        StatusCompleted,
		CacheHit:      true,
		DriveFileID:   hit.DriveFileID,
		DriveLink:     hit.DriveLink,
		DownloadLink:  hit.DownloadLink,
		LocalPath:     hit.LocalPath,
		CleanedPath:   hit.CleanedPath,
		DurationMs:    hit.DurationMs,
		LegacyFileMD5: hit.LegacyFileMD5,
	}

	// Reconstruct the timing result from the persisted metadata when
	// timing was requested. The full word-level artifact is not stored
	// in the metadata column (only the SSOT timing.json on Drive has
	// it), so the summary fields are hydrated from metadata.
	if timingPolicy.Mode != audio.TimingDisabled && len(hit.MetaJSON) > 0 {
		var meta map[string]any
		if err := json.Unmarshal(hit.MetaJSON, &meta); err == nil {
			if jsonLink, ok := meta["timing_json_link"].(string); ok && jsonLink != "" {
				timingRes := &VoiceoverTimingResult{Status: TimingStatusCompleted}
				timingRes.JSONLink = jsonLink
				if srtLink, _ := meta["timing_srt_link"].(string); srtLink != "" {
					timingRes.SRTLink = srtLink
				}
				if vttLink, _ := meta["timing_vtt_link"].(string); vttLink != "" {
					timingRes.VTTLink = vttLink
				}
				if boundaryMode, _ := meta["timing_boundary_mode"].(string); boundaryMode != "" {
					timingRes.BoundaryMode = boundaryMode
				}
				if wordCount, ok := meta["timing_word_count"].(float64); ok {
					timingRes.WordCount = int(wordCount)
				}
				if durationUS, ok := meta["timing_duration_us"].(float64); ok {
					timingRes.DurationUS = int64(durationUS)
				}
				if audioSHA, _ := meta["audio_sha256"].(string); audioSHA != "" {
					timingRes.AudioSHA256 = audioSHA
				}
				out.Timing = timingRes
			}
		}
	}

	setFinalStageProgress(out, string(cmd.Language), cmd.JobID)

	log.Info("voiceover cache HIT result built",
		zap.String("id", out.ID),
		zap.String("drive_file_id", out.DriveFileID),
		zap.String("language", string(cmd.Language)),
		zap.Int64("duration_ms", out.DurationMs))

	return out
}

// enqueueOrphanCleanup is defined in process_segment_orphan.go.

// runAsyncPublish executes Stage 3 (Drive upload + timing publish) and
// Stage 4 (SQLite finalize) in a background goroutine via the async
// publish pool. It is only called when AsyncPublish is wired.
//
// The method accepts by-pointer copies of the command, partial result,
// and TTS output — the originals are owned by the caller (which already
// returned the partial result to free the TTS slot). Errors are logged
// at Warn level; the caller cannot observe the outcome synchronously
// (by design — the TTS slot is freed before publish completes).
//
// godlike/07 honest-limitation: publish failures in the async path are
// NOT surfaced to the immediate caller. The DB row carries the failure
// in its status/error columns; the run result's per-scene Voiceover
// reference remains StatusGenerated with local paths. Downstream
// observability dashboards must read the DB to detect async failures.
func (u *ProcessSegmentUseCase) runAsyncPublish(
	ctx context.Context,
	cmd *ProcessSegmentCommand,
	out *VoiceoverItemResult,
	ttsOut *TTSOutput,
	post *AudioPostOutput,
	log *zap.Logger,
) {
	// Stage 3: publish (Drive upload + timing bundle).
	pub, pubErr := u.publishStage(ctx, cmd, out, ttsOut, post, log)
	if pubErr != nil {
		log.Warn("voiceover async publish failed",
			zap.String("scene_id", cmd.ID),
			zap.String("language", string(cmd.Language)),
			zap.Error(pubErr))
		observability.VoiceoverJobsTotal.WithLabelValues("failed").Inc()
		return
	}

	// Stage 4: BeginTx + Finalize + Commit.
	tx, txErr := u.deps.VoiceoverRepository.BeginTx(ctx)
	if txErr != nil {
		log.Warn("voiceover async finalize tx begin failed",
			zap.String("scene_id", cmd.ID),
			zap.String("language", string(cmd.Language)),
			zap.Error(txErr))
		observability.VoiceoverJobsTotal.WithLabelValues("failed").Inc()
		// FASE 4 orphan-cleanup: the Drive file was uploaded but
		// the DB tx couldn't start. Enqueue a cleanup event.
		if out.DriveFileID != "" && u.deps.TxOutboxEnqueuer != nil {
			u.enqueueOrphanCleanup(ctx, cmd.ID, out.DriveFileID, out.LocalPath, out.CleanedPath)
		}
		return
	}
	defer func() { _ = tx.Rollback() }()

	fingerprint := BuildVoiceoverContentFingerprint(cmd.TextHash, cmd.Language, out.Voice, cmd.Dest.FolderID, cmd.Timing, cmd.RemoveSilence)
	finalizeCmd := &FinalizeCommand{
		Fingerprint:     fingerprint,
		IdempotencyKey:  pub.IdemKey,
		JobID:           cmd.JobID,
		ID:              cmd.ID,
		RequestID:       cmd.RequestID,
		TextHash:        string(cmd.TextHash),
		Text:            cmd.Text,
		Language:        cmd.Language,
		Voice:           out.Voice,
		Filename:        cmd.Filename,
		Strategy:        cmd.Strategy,
		MetaJSON:        pub.MetaJSON,
		LocalPath:       out.LocalPath,
		CleanedPath:     out.CleanedPath,
		LegacyFileMD5:   out.LegacyFileMD5,
		DurationSeconds: ttsOut.Duration.Seconds(),
		FolderID:        cmd.Dest.FolderID,
		FolderPath:      cmd.Dest.FolderPath,
		DriveFileID:     out.DriveFileID,
		DriveLink:       out.DriveLink,
		DownloadLink:    out.DownloadLink,
		ShouldSwap:      cmd.ShouldSwap,
		OldDriveFileID:  cmd.OldDriveFileID,
		OldLocalPath:    cmd.OldLocalPath,
		OldCleanedPath:  cmd.OldCleanedPath,
	}

	finalizeRes, finalizeErr := u.deps.Finalizer.Finalize(ctx, tx, finalizeCmd)
	if finalizeErr != nil {
		log.Warn("voiceover async finalize failed",
			zap.String("scene_id", cmd.ID),
			zap.String("language", string(cmd.Language)),
			zap.Error(finalizeErr))
		observability.VoiceoverJobsTotal.WithLabelValues("failed").Inc()
		_ = tx.Rollback()
		if out.DriveFileID != "" && u.deps.TxOutboxEnqueuer != nil {
			u.enqueueOrphanCleanup(ctx, cmd.ID, out.DriveFileID, out.LocalPath, out.CleanedPath)
		}
		return
	}

	if finalizeRes != nil && finalizeRes.Reused {
		out.ID = finalizeRes.ID
	}

	if commitErr := tx.Commit(); commitErr != nil {
		log.Warn("voiceover async finalize commit failed",
			zap.String("scene_id", cmd.ID),
			zap.String("language", string(cmd.Language)),
			zap.Error(commitErr))
		observability.VoiceoverJobsTotal.WithLabelValues("failed").Inc()
		return
	}

	log.Info("voiceover async publish+finalize completed",
		zap.String("scene_id", cmd.ID),
		zap.String("language", string(cmd.Language)),
		zap.String("drive_file_id", out.DriveFileID))
	observability.VoiceoverJobsTotal.WithLabelValues("completed").Inc()
}
