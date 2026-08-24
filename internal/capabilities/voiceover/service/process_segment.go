// Package voiceover — usecase/process_segment.go (PR-VO-USECASE-PROCESS-DRY,
// P1 in VO-DECOMPOSITION-2026-07-04 wave, deadline 2026-08-15).
//
// ProcessSegmentUseCase is the SINGLE canonical per-item voiceover pipeline
// runner. It replaces the divergent per-item bodies in:
//
//   - usecase.go::processOneLanguage (batch path, pre-DRY): manual
//     TX orchestration (BeginTx → DeleteByIDTx → InsertTx →
//     TransactionalOutbox.EnqueueIndexEvent → Commit). MISSING the
//     dedupe gate (Step 1), media_assets projection (Step 4), and
//     cleanup outbox (Step 6) — silent-success gap (godlike/07
//     "no fake availability" violation that the post-DRY migration
//     closes).
//
//   - process_voiceover_item.go::Execute (per-item path, pre-DRY):
//     delegated to VoiceoverFinalizer (P0.4 Fase 3a, July 2026).
//     Carries all 6 finalization steps correctly.
//
// Post-DRY BOTH callers consume the same ProcessSegmentUseCase.Execute
// method. The batch path is migrated to the finalizer (gains the
// dedupe gate + media_assets projection + cleanup outbox that it
// was missing). The per-item path loses its inline TTS/AudioPost/
// Publish/TX code (still uses the finalizer under the hood).
//
// godlike/06 SSOT: ProcessSegmentUseCase is the single owner of the
// per-item pipeline. The 5 stage files (stage_synthesize.go etc.
// from PR-VO-STAGES-SPLIT) are file-level owners of legacy batch
// path stages; the ProcessSegmentUseCase is the cross-file neutral
// owner that BOTH the batch and per-item callers delegate to.
//
// godlike/07 honest-limitation: this extraction does NOT touch
// the bounded parallel fan-out in usecase.go::Execute (the
// *Executor field). The fan-out layer is the worker-pool; the
// ProcessSegmentUseCase is the per-task body. They are distinct
// concerns: fan-out = how to schedule N tasks, ProcessSegmentUseCase
// = how to execute ONE task. The bounded pool continues to call
// the use case's processOneTask → processOneLanguage which now
// delegates to the ProcessSegmentUseCase.
//
// Package note: this file lives in the `usecase/` subdirectory of
// the `voiceover` package but is declared `package voiceover` (same
// as the parent). The subdirectory is organizational only — it
// keeps the canonical use case co-located with future use-case
// siblings (e.g. process_segment_aggregate.go, process_segment_promo.go)
// without forcing a subpackage split that would create an import
// cycle on `*VoiceoverItemResult` (which lives in the parent
// `voiceover` package). Per AGENTS.md Pattern 0 / godlike/07
// minimum-blast-radius: the file location is canonical, the package
// boundary stays the same.
package voiceover

import (
	"context"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
)

func (u *ProcessSegmentUseCase) Execute(ctx context.Context, cmd *ProcessSegmentCommand) (*VoiceoverItemResult, error) {
	// Stage 0: nil-safe + required fields check.
	if cmd == nil {
		return nil, fmt.Errorf("ProcessSegmentUseCase.Execute: nil input")
	}
	if cmd.Dest == nil || cmd.Dest.FolderID == "" {
		observability.VoiceoverJobsTotal.WithLabelValues("failed").Inc()
		out := &VoiceoverItemResult{
			Language:  cmd.Language,
			Status:    StatusFailed,
			ErrorCode: string(FailureMissingFolder),
			Error:     "missing_folder_id: voiceover destination has no FolderID for upload",
		}
		setFinalStageProgress(out, string(cmd.Language), cmd.JobID)
		return out, newPipelineErrorCode(StageDestinationResolve, false, FailureMissingFolder, fmt.Errorf("%s", out.Error))
	}

	out := &VoiceoverItemResult{
		Language: cmd.Language,
		Voice:    cmd.Voice,
		Filename: cmd.Filename,
		ID:       cmd.ID,
		Status:   StatusFailed,
	}

	// ── Structured logging helper ───────────────────────────────────
	// The shared stageLog helper emits lifecycle logs; canonical runs own
	// timing observations.
	log := u.deps.Logger

	// ── Cross-run voiceover cache check ─────────────────────────────
	// Before invoking TTS (expensive) + Drive upload + timing publish,
	// check whether a previous run already produced the same voiceover
	// for the same content fingerprint (textHash + language + voice +
	// folderID + timing policy + silence policy). A verified cache hit
	// short-circuits the entire 4-stage pipeline and returns the
	// existing result — 0 TTS calls, 0 Drive uploads.
	timingPolicy := audio.DefaultTimingRequest()
	if cmd.Timing != nil {
		timingPolicy = cmd.Timing.Normalized()
	}
	if u.deps.Cache.VoiceoverCache != nil {
		fingerprint := BuildVoiceoverContentFingerprint(cmd.TextHash, cmd.Language, cmd.Voice, cmd.Dest.FolderID, cmd.Timing, cmd.RemoveSilence)
		hit, lookupErr := u.deps.Cache.VoiceoverCache.Lookup(ctx, fingerprint, timingPolicy.Mode != audio.TimingDisabled)
		if lookupErr != nil {
			log.Warn("voiceover cache lookup error — falling through to full pipeline",
				zap.String("fingerprint", fingerprint),
				zap.String("language", string(cmd.Language)),
				zap.Error(lookupErr))
		} else if hit != nil {
			log.Info("voiceover cache HIT — reusing existing audio, skipping TTS + upload + finalize",
				zap.String("fingerprint", fingerprint),
				zap.String("cached_id", hit.ID),
				zap.String("drive_file_id", hit.DriveFileID),
				zap.String("language", string(cmd.Language)))
			observability.VoiceoverJobsTotal.WithLabelValues("completed").Inc()
			return buildCachedResult(cmd, hit, timingPolicy, log), nil
		}
		log.Info("voiceover cache MISS — full pipeline will run",
			zap.String("fingerprint", fingerprint),
			zap.String("language", string(cmd.Language)))
	}

	// Stage 1: TTSProvider.Synthesize.
	// P0.2 Fase 2c (July 2026): RemoveSilence is ALWAYS false here.
	// AudioPostProcessor owns silence removal (Stage 2), not the TTS
	// provider. Passing cmd.RemoveSilence=true to Synthesize would
	// cause the TTS bridge to strip silence inline, and then
	// AudioPostProcessor would re-process an already-cleaned file —
	// double-processing that wastes CPU and risks audio artifacts.
	emitTTS := stageLog(log, cmd.RequestID, cmd.ID, cmd.Project, "tts", string(cmd.Language))
	// Timing capture lifecycle: capture.started fires before synthesis and
	// capture.completed after, so operators can trace boundary capture
	// without ever logging the per-word array.
	// timingPolicy is already resolved before the cache check above.
	if timingPolicy.Mode != audio.TimingDisabled {
		timingEvent(log, "voiceover.timing.capture.started", cmd, "", "", 0, 0)
	}
	// Stage 1 timing is recorded by the canonical Run (MeasureStage), so the
	// TTS duration flows into run_stage_observations via the SQLiteRecorder;
	// stageLog remains the structured lifecycle log only.
	var ttsOut TTSOutput
	err := kernobs.MeasureStage(ctx, canonicalStageTTS, func(stageCtx context.Context) error {
		var synthErr error
		ttsOut, synthErr = u.deps.TTSProvider.Synthesize(stageCtx, TTSInput{
			Text:          cmd.Text,
			Language:      cmd.Language,
			Voice:         cmd.Voice,
			Filename:      cmd.Filename,
			OutputDir:     cmd.Dest.FolderPath,
			RemoveSilence: false, // P0.2 Fase 2c: never delegate to TTS
			// Timing is the canonical timing policy; nil means the provider
			// applies the defaults. The provider only returns RAW boundaries;
			// the canonical artifact is built later from the final audio.
			Timing: cmd.Timing,
		})
		return synthErr
	})
	if err != nil {
		emitTTS("failed")
		observability.TTSFailuresTotal.Inc()
		observability.VoiceoverJobsTotal.WithLabelValues("failed").Inc()
		out.ErrorCode = string(FailureTTS)
		out.Error = fmt.Sprintf("%s: %v", FailureTTS, err)
		setFinalStageProgress(out, string(cmd.Language), cmd.JobID)
		return out, newPipelineErrorCode(StageTTS, true, FailureTTS, err)
	}
	emitTTS("completed")
	if timingPolicy.Mode != audio.TimingDisabled {
		timingEvent(log, "voiceover.timing.capture.completed", cmd, ttsOut.Provider, ttsOut.BoundaryMode, len(ttsOut.WordBoundaries), ttsOut.Duration.Microseconds())
	}
	out.LocalPath = ttsOut.LocalPath
	out.CleanedPath = ttsOut.CleanedPath
	out.DurationMs = ttsOut.Duration.Milliseconds()
	if ttsOut.Voice != "" {
		out.Voice = ttsOut.Voice
	}
	out.LegacyFileMD5 = ttsOut.LegacyFileMD5

	// Stage 2: optional AudioPostProcessor (silence removal). Nil-safe.
	// The post output (cleaned path + edit map) is forwarded to the
	// publish stage so timing boundaries are remapped onto the CLEANED
	// timeline instead of producing timestamps for the pre-clean audio.
	var post *AudioPostOutput
	if cmd.RemoveSilence && u.deps.AudioPostProcessor != nil && ttsOut.LocalPath != "" {
		emitPost := stageLog(log, cmd.RequestID, cmd.ID, cmd.Project, "audio_post", string(cmd.Language))
		var postOut AudioPostOutput
		err = kernobs.MeasureStage(ctx, canonicalStageAudioPost, func(stageCtx context.Context) error {
			var postErr error
			postOut, postErr = u.deps.AudioPostProcessor.Process(stageCtx, AudioPostInput{
				LocalPath: ttsOut.LocalPath,
				OutputDir: cmd.Dest.FolderPath,
				Filename:  cmd.Filename,
			})
			return postErr
		})
		if err != nil {
			emitPost("failed")
			observability.VoiceoverJobsTotal.WithLabelValues("failed").Inc()
			out.ErrorCode = string(FailureAudioPost)
			out.Error = fmt.Sprintf("%s: %v", FailureAudioPost, err)
			setFinalStageProgress(out, string(cmd.Language), cmd.JobID)
			return out, newPipelineErrorCode(StageAudioPost, false, FailureAudioPost, err)
		}
		emitPost("completed")
		if postOut.CleanedPath != "" {
			out.CleanedPath = postOut.CleanedPath
		}
		post = &postOut
		// Summarize the silence removal for observability: the original
		// pre-clean duration, the leading/trailing trims, and the cleaned
		// duration the timeline must use.
		out.SilenceCleanup = BuildSilenceCleanupReport(ttsOut.Duration.Microseconds(), postOut.DurationUS, postOut.EditMap)
		// Emit the structured observability event so operators can verify
		// "Edge aveva N ms di silenzio artificiale, li ho rimossi, la
		// timeline usa la durata pulita" without re-probing the file.
		silenceCleanupEvent(log, cmd, out.SilenceCleanup)
	}

	if out.LocalPath == "" && out.CleanedPath == "" {
		observability.VoiceoverJobsTotal.WithLabelValues("failed").Inc()
		out.ErrorCode = string(FailureNoLocalPayload)
		out.Error = "no_local_payload: TTSProvider + AudioPostProcessor produced no local path"
		setFinalStageProgress(out, string(cmd.Language), cmd.JobID)
		return out, newPipelineErrorCode(StageTTS, false, FailureNoLocalPayload, fmt.Errorf("%s", out.Error))
	}

	// ── Async publish path (P0.4: separate TTS pool from publish pool) ──
	// When AsyncPublish is wired, Stage 3 (Drive upload + timing) and
	// Stage 4 (SQLite finalize) run in a background goroutine via the
	// bounded publish pool. The TTS slot is freed immediately after
	// synthesis so the next scene can start TTS while Drive upload and
	// DB commit run concurrently.
	//
	// The partial result carries local file paths (LocalPath, CleanedPath,
	// DurationMs, Voice, Filename, ID) and a StatusGenerated status.
	// DriveFileID, DriveLink, DownloadLink, and Timing are NOT populated —
	// they are committed to the DB by the async goroutine and become
	// visible to downstream DB readers after the publish pool drains.
	if u.deps.Cache.AsyncPublish != nil {
		out.Status = StatusGenerated
		setFinalStageProgress(out, string(cmd.Language), cmd.JobID)
		observability.VoiceoverJobsTotal.WithLabelValues("generated").Inc()

		// Capture by-value copies for the closure so there is no race
		// between the caller (which reads the returned partial result)
		// and the background goroutine (which mutates its own copy
		// during publish+finalize).
		capturedCmd := *cmd
		capturedOut := *out
		capturedTTS := ttsOut
		var capturedPost *AudioPostOutput
		if post != nil {
			cp := *post
			capturedPost = &cp
		}

		log.Info("voiceover: async publish submitted — TTS slot freed",
			zap.String("scene_id", cmd.ID),
			zap.String("language", string(cmd.Language)),
			zap.Int64("duration_ms", out.DurationMs))

		u.deps.Cache.AsyncPublish.Submit(ctx, func() {
			u.runAsyncPublish(ctx, &capturedCmd, &capturedOut, &capturedTTS, capturedPost, log)
		})

		return out, nil
	}

	// Stage 3: VoiceoverPublisher.Publish — delegates to publishStage
	// (process_segment_publish.go) for metadata building + idempotency
	// key derivation + Drive upload + the timing bundle publish (audio
	// + timing.json + optional SRT/VTT with required/best-effort/disabled
	// semantics).
	var pub *publishStageResult
	err = kernobs.MeasureStage(ctx, canonicalStagePublish, func(stageCtx context.Context) error {
		var pubErr error
		pub, pubErr = u.publishStage(stageCtx, cmd, out, &ttsOut, post, log)
		return pubErr
	})
	if err != nil {
		observability.VoiceoverJobsTotal.WithLabelValues("failed").Inc()
		var pipelineErr *PipelineError
		if errors.As(err, &pipelineErr) {
			out.ErrorCode = string(pipelineErr.FailureCode())
			out.Error = pipelineErr.Error()
			setFinalStageProgress(out, string(cmd.Language), cmd.JobID)
			return out, err
		}
		if errors.Is(err, delivery.ErrDestinationParentMismatch) {
			observability.DriveUploadFailuresTotal.Inc()
			out.ErrorCode = VoiceoverDestinationMismatchCode
			out.Error = fmt.Sprintf("%s: %v", VoiceoverDestinationMismatchCode, err)
			setFinalStageProgress(out, string(cmd.Language), cmd.JobID)
			return out, newPipelineErrorCode(StageUpload, false, FailureDestinationMismatch, err)
		}
		observability.DriveUploadFailuresTotal.Inc()
		out.ErrorCode = string(FailureUpload)
		out.Error = fmt.Sprintf("%s: %v", FailureUpload, err)
		setFinalStageProgress(out, string(cmd.Language), cmd.JobID)
		return out, newPipelineErrorCode(StageUpload, true, FailureUpload, err)
	}

	// Stage 4: BeginTx + Finalize + Commit (delegated to finalizer).
	// PR-VO-USECASE-PROCESS-DRY migration: the batch path now
	// delegates to the finalizer (gains the dedupe gate +
	// media_assets projection + cleanup outbox that it was
	// missing pre-DRY). The per-item path was already using the
	// finalizer; the only change is that the body now lives in
	// the ProcessSegmentUseCase.
	emitFinalize := stageLog(log, cmd.RequestID, cmd.ID, cmd.Project, "finalize", string(cmd.Language))
	tx, err := u.deps.VoiceoverRepository.BeginTx(ctx)
	if err != nil {
		emitFinalize("failed")
		observability.VoiceoverJobsTotal.WithLabelValues("failed").Inc()
		out.ErrorCode = string(FailureTxBegin)
		out.Error = fmt.Sprintf("%s: %v", FailureTxBegin, err)
		setFinalStageProgress(out, string(cmd.Language), cmd.JobID)
		return out, newPipelineErrorCode(StageTxBegin, true, FailureTxBegin, err)
	}
	defer func() { _ = tx.Rollback() }() // safe after Commit

	finalizeCmd := &FinalizeCommand{
		Fingerprint:     BuildVoiceoverContentFingerprint(cmd.TextHash, cmd.Language, out.Voice, cmd.Dest.FolderID, cmd.Timing, cmd.RemoveSilence),
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

	var finalizeRes *FinalizeResult
	err = kernobs.MeasureStage(ctx, canonicalStageFinalize, func(stageCtx context.Context) error {
		var finalizeErr error
		finalizeRes, finalizeErr = u.deps.Finalizer.Finalize(stageCtx, tx, finalizeCmd)
		return finalizeErr
	})
	if err != nil {
		emitFinalize("failed")
		observability.VoiceoverJobsTotal.WithLabelValues("failed").Inc()
		out.ErrorCode = string(FailureFinalize)
		out.Error = fmt.Sprintf("%s: %v", FailureFinalize, err)

		// Rollback the transaction immediately to release the connection pool lock
		_ = tx.Rollback()

		// FASE 4 (July 2026): orphan-cleanup path — when Drive upload
		// succeeded (Stage 3) but DB finalize failed (Stage 4), the
		// Drive file is orphaned (exists on Drive with no DB row).
		// Open a SEPARATE tx (the Finalize tx is about to be rolled
		// back by defer) and enqueue a voiceover.cleanup.requested
		// outbox event so the orphan sweeper can trash the Drive
		// file. When TxOutboxEnqueuer is nil (pre-FASE-4 callers),
		// this path is silently skipped — the background orphan
		// sweeper will eventually recover the file.
		//
		// godlike/07 NO-FAKE-AVAILABILITY: the cleanup event is
		// committed in its OWN tx, independent of the rolled-back
		// Finalize tx. A failure to enqueue the cleanup event (e.g.
		// DB unreachable) is logged at Warn level but does NOT
		// change the finalize_failed error — the caller still sees
		// the original Finalize error.
		if out.DriveFileID != "" && u.deps.TxOutboxEnqueuer != nil {
			u.enqueueOrphanCleanup(ctx, cmd.ID, out.DriveFileID, out.LocalPath, out.CleanedPath)
		}

		return out, newPipelineErrorCode(StageTxCommit, true, FailureFinalize, err)
	}

	// If the dedupe gate matched an existing row (Reused=true),
	// adopt the matched ID as the canonical identifier.
	if finalizeRes != nil && finalizeRes.Reused {
		out.ID = finalizeRes.ID
	}

	if err := tx.Commit(); err != nil {
		emitFinalize("failed")
		observability.VoiceoverJobsTotal.WithLabelValues("failed").Inc()
		out.ErrorCode = string(FailureTxCommit)
		out.Error = fmt.Sprintf("%s: %v", FailureTxCommit, err)
		setFinalStageProgress(out, string(cmd.Language), cmd.JobID)
		return out, newPipelineErrorCode(StageTxCommit, true, FailureTxCommit, err)
	}
	emitFinalize("completed")

	out.Status = StatusCompleted
	setFinalStageProgress(out, string(cmd.Language), cmd.JobID)
	observability.VoiceoverJobsTotal.WithLabelValues("completed").Inc()
	return out, nil
}

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
