package scriptgeneration

import (
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/timeline"
	kernelscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func (r *Runner) runVoiceoverPhase(ctx context.Context, runID string, req contract.GenerateRequest, routing kernelscript.ArtifactRoutingContext, exec ExecutionContext, resumeIdx int, result *contract.GenerateResult) bool {
	// ── Stage 4: Generate Voiceovers (scene-level idempotent) ───
	voiceoverStep, startErr := r.startExecutionStep(ctx, exec, "VOICEOVER", "audio")
	if startErr != nil {
		r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, startErr)
		return false
	}
	// Safety net for direct-runner/resume paths that bypass the generate-phase
	// gate: never start TTS on scenes whose source ranges cannot form a
	// canonical timeline. The normal path already failed before translation.
	if result != nil {
		if err := timeline.ValidateAudioSourceRanges(result.Scenes); err != nil {
			cause := fmt.Errorf("voiceover phase: audio source range preflight: %w", err)
			r.failExecutionStepBestEffort(ctx, exec, voiceoverStep, cause)
			r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, cause)
			return false
		}
	}
	// Voiceover generation is gated on the resolved audio mode: only
	// CHUNKED_VOICEOVER and COMBINED_TIMELINE produce voiceover assets.
	// audio.mode NONE (or omitted → NONE) must not trigger TTS nor
	// stage/publish voiceover artifacts — a timeline-only run stays
	// metadata-only and a plain script run pays no unrequested TTS cost.
	mode, modeErr := capabilityaudio.ResolveAudioMode(req.Audio, false)
	if modeErr != nil {
		// Envelope validation and the builder reject invalid audio-mode
		// combinations earlier; fail closed here for direct-runner callers.
		cause := fmt.Errorf("voiceover phase: resolve audio mode: %w", modeErr)
		r.failExecutionStepBestEffort(ctx, exec, voiceoverStep, cause)
		r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, cause)
		return false
	}
	needsVoiceover := mode == capabilityaudio.AudioModeChunkedVoiceover || mode == capabilityaudio.AudioModeCombinedTimeline
	// On retry, scenes that already have a voiceover for a language
	// are skipped, so a retry never duplicates TTS work.
	voiceoverSkipped := stageSkipped(resumeIdx, StageGeneratingVoiceovers) || r.voiceoverGen == nil || !needsVoiceover
	if !voiceoverSkipped {
		if err := r.updateStage(ctx, runID, RunStatusRunning, StageGeneratingVoiceovers); err != nil {
			r.failExecutionStepBestEffort(ctx, exec, voiceoverStep, err)
			r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, err)
			return false
		}
		if result.AudioMetrics == nil {
			result.AudioMetrics = &contract.AudioPipelineMetrics{}
		}
		// A streaming SceneReady coordinator may have completed TTS before
		// this stage opened. Register those already-materialized assets here;
		// buildVoiceoverWork will correctly skip synthesis for them.
		existingOrdinal := 0
		for _, scene := range result.Scenes {
			for _, ref := range scene.Voiceover {
				if ref.ID == "" {
					continue
				}
				if err := r.attachOutputAsset(ctx, exec, voiceoverStep.StepID, ref.ID, existingOrdinal); err != nil {
					r.failExecutionStepBestEffort(ctx, exec, voiceoverStep, err)
					r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, err)
					return false
				}
				existingOrdinal++
			}
		}
		// ── TTS worker pool ───────────────────────────────────────────
		// The voiceover branch fans out scene×language synthesis through a
		// bounded worker pool (r.ttsConcurrency). TTS depends only on the
		// final scene text — never on entities/phrases/words — so it runs in
		// parallel with SceneAnalysis from the SceneTextReady boundary. Each
		// item is independent; results are applied in canonical order below.
		work := buildVoiceoverWorkForLanguages(result.Scenes, req.SourceLanguage, req.Languages, req.VoiceoverLanguages)
		// A request containing only protected fixed media has no TTS work.
		// Do not require the voiceover publication project in that case:
		// fixed_media is intentionally excluded from this phase.
		if len(work) > 0 && routing.Project == "" {
			cause := fmt.Errorf("%w: voiceover publishing requires a resolved Project", ErrProjectRequired)
			r.failExecutionStepBestEffort(ctx, exec, voiceoverStep, cause)
			r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, cause)
			return false
		}
		requested, _ := r.auditVoiceoverDispatch(runID, req, result, work)
		var dbCacheHits int
		var renderWg sync.WaitGroup
		// Every callback can report an error before the join starts draining the
		// channel. Size it for the complete scene × render-unit × language
		// matrix, including reused voiceovers (which are dispatched later by
		// projectVoiceoverMetrics) and fixed intro/outro media. Counting only
		// fresh TTS items deadlocks cache-hit jobs as soon as several Chronon
		// requests fail concurrently.
		renderErrors := make(chan error, localizedRenderErrorCapacity(req, result.Scenes))
		if len(work) > 0 {

			results, err := r.synthesizeVoiceoverWork(ctx, runID, req, routing, exec, mode, result, work, &renderWg, renderErrors)
			if err != nil {
				// concurrent.Map has joined the TTS workers, but successful
				// siblings may already have launched localized render callbacks.
				// Join them before taking the failure snapshot so the partial
				// result is race-free and includes every completed sibling.
				renderWg.Wait()
				if snapshot, snapshotErr := detachCheckpointResult(result); snapshotErr != nil {
					r.log.Warn("voiceover failure checkpoint snapshot failed", zap.String("run_id", runID), zap.Error(snapshotErr))
				} else if snapshot != nil {
					r.checkpoint(ctx, runID, snapshot)
				}
				cause := fmt.Errorf("voiceover generation failed: %w", err)
				r.failExecutionStepBestEffort(ctx, exec, voiceoverStep, cause)
				r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, cause)
				return false
			}
			// ── Cross-run DB cache accounting ─────────────────────────
			// Each result carries the Cached flag surfaced by the per-item
			// pipeline: true means the SQLite fingerprint lookup hit and the
			// entire TTS + upload + finalize work was skipped for the item.
			dbCacheHits = 0
			for i := range results {
				if results[i].audioRef.Cached {
					dbCacheHits++
				}
			}
			result.AudioMetrics.VoiceoverDBCacheHits = dbCacheHits
			r.log.Info("voiceover db cache audit",
				zap.String("run_id", runID),
				zap.Int("requested", requested),
				zap.Int("generated", len(work)),
				zap.Int("db_cache_hits", dbCacheHits),
				zap.Int("db_cache_misses", len(work)-dbCacheHits),
				zap.String("db_cache_status", "consulted"),
			)
			// Voiceover references and narration-driven durations were applied
			// per unit inside the workers (durable per-unit checkpoint). This
			// loop only projects the canonical-order observability: per-scene
			// TTS metrics and output-asset lineage ordinals stay deterministic.
			for i := range work {
				res := results[i]
				result.AudioMetrics.TTSScenes = append(result.AudioMetrics.TTSScenes, res.metric)
				if err := r.attachOutputAsset(ctx, exec, voiceoverStep.StepID, res.audioRef.ID, i); err != nil {
					r.failExecutionStepBestEffort(ctx, exec, voiceoverStep, err)
					r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, err)
					return false
				}
				// Per-(scene, language) TTS correlation: record the produced
				// voiceover asset so translation → TTS → render → Drive is
				// joinable on (scene_id, language, asset_id).
				if err := r.recordArtifactOperation(ctx, exec, ArtifactOperation{
					OperationID: artifactOperationID(exec.Attempt, OperationTTS, work[i].sceneID, string(work[i].lang)),
					Kind:        OperationTTS,
					SceneID:     work[i].sceneID,
					Language:    work[i].lang,
					AssetID:     res.audioRef.ID,
					Status:      "COMPLETED",
				}); err != nil {
					r.failExecutionStepBestEffort(ctx, exec, voiceoverStep, err)
					r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, err)
					return false
				}
			}
		}
		if !r.projectVoiceoverMetrics(ctx, runID, req, routing, exec, mode, result, work, dbCacheHits, &renderWg, renderErrors, voiceoverStep) {
			return false
		}
		r.log.Info("stage complete", zap.String("run_id", runID), zap.String("stage", string(StageGeneratingVoiceovers)))
	}
	if voiceoverSkipped {
		if err := r.skipExecutionStep(ctx, exec, voiceoverStep); err != nil {
			r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, err)
			return false
		}
	} else if err := r.completeExecutionStep(ctx, exec, voiceoverStep); err != nil {
		r.failExecutionStepBestEffort(ctx, exec, voiceoverStep, err)
		r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, err)
		return false
	}

	return true
}

// ── Bounded TTS fan-out + per-item render handoff ──────────────────
// synthesizeVoiceoverWork owns the bounded TTS fan-out and the per-item
// checkpoint/render handoff. Results retain the input order supplied by
// concurrent.Map, so lineage is projected deterministically by the caller.
