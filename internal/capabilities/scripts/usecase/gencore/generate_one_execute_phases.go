package gencore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	"go.uber.org/zap"
)

func (uc *GenerateOneUseCase) runPreparePhase(
	ctx context.Context,
	item scriptpkg.GenerationItemV2,
	preset scriptpkg.Preset,
	tracker *ProgressTracker,
) (*PreparedGeneration, error) {
	var prepared *PreparedGeneration
	_, err := kernobs.MeasureStageReport(ctx, scriptgen.StageScriptPrepare, func(stageCtx context.Context) error {
		var prepareErr error
		prepared, _, prepareErr = uc.preparer.Prepare(stageCtx, item, preset, tracker)
		return prepareErr
	})
	return prepared, err
}

func (uc *GenerateOneUseCase) runPostprocessPhase(
	ctx context.Context,
	item scriptpkg.GenerationItemV2,
	plan scriptpkg.ResolvedGenerationPlan,
	engineResult *EngineResult,
	tracker *ProgressTracker,
) (*ProcessedGeneration, error) {
	var processed *ProcessedGeneration
	_, err := kernobs.MeasureStageReport(ctx, scriptgen.StageScriptPostprocess, func(stageCtx context.Context) error {
		var ppErr error
		processed, ppErr = uc.postprocessor.Process(stageCtx, item, plan, engineResult, tracker)
		return ppErr
	})
	if err != nil {
		return nil, uc.logPhaseError(item, "postprocess", scriptpkg.ErrPostprocessFailed, err, tracker)
	}
	return processed, nil
}

func (uc *GenerateOneUseCase) finalizePhase(
	ctx context.Context,
	item scriptpkg.GenerationItemV2,
	plan scriptpkg.ResolvedGenerationPlan,
	engineResult *EngineResult,
	processed *ProcessedGeneration,
	tracker *ProgressTracker,
) (*scriptpkg.GenerationResult, error) {
	result, err := uc.finalizer.Finalize(ctx, FinalizeInputs{
		Item:         item,
		Plan:         plan,
		EngineResult: engineResult,
		PostResult:   processed.PostResult,
		Provenance:   processed.Provenance,
	}, tracker)
	if err == nil {
		return result, nil
	}
	var qErr *scriptpkg.QualityGateError
	var clipErr *scriptpkg.ClipNativePlanningError
	switch {
	case errors.As(err, &qErr):
		return result, uc.logPhaseError(item, "quality_gate", scriptpkg.ErrQualityGateFailed, err, tracker)
	case errors.As(err, &clipErr):
		return nil, uc.logPhaseError(item, "clip_native", scriptpkg.ErrClipNativePlanningFailed, err, tracker)
	default:
		return nil, uc.logPhaseError(item, "finalize", scriptpkg.ErrGenerationFailed, err, tracker)
	}
}

func (uc *GenerateOneUseCase) applyAudioMode(
	ctx context.Context,
	item scriptpkg.GenerationItemV2,
	plan scriptpkg.ResolvedGenerationPlan,
	result *scriptpkg.GenerationResult,
	processed *ProcessedGeneration,
	tracker *ProgressTracker,
) error {
	switch plan.AudioMode {
	case "COMBINED_TIMELINE":
		// audio.pipeline is a STAGE boundary; the canonical Run clock is the
		// only wall-time source. AudioPipelineMetrics is derived from the
		// completed canonical stage/operations, never timed independently.
		if _, audioErr := kernobs.MeasureStageReport(ctx, scriptgen.StageAudioPipeline, func(stageCtx context.Context) error {
			return uc.renderCombinedAudio(stageCtx, item, result, processed.PostResult)
		}); audioErr != nil {
			return uc.logPhaseError(item, "combined_audio", scriptpkg.ErrGenerationFailed, audioErr, tracker)
		}
	case "CHUNKED_VOICEOVER":
		result.AudioStrategy = "TIMELINE_MIX"
		if result.FinalAudio != nil {
			return uc.logPhaseError(item, "audio_mode", scriptpkg.ErrGenerationFailed, fmt.Errorf("CHUNKED_VOICEOVER must not produce final_audio"), tracker)
		}
	}
	return nil
}

func (uc *GenerateOneUseCase) applyOverlayRender(
	ctx context.Context,
	item scriptpkg.GenerationItemV2,
	plan scriptpkg.ResolvedGenerationPlan,
	result *scriptpkg.GenerationResult,
	processed *ProcessedGeneration,
	tracker *ProgressTracker,
) error {
	// Semantic overlays are requested by output.render.enabled. A background
	// is optional: entity cards, images and phrases can be rendered over a
	// transparent canvas and composited downstream. Keep the explicit
	// overlay_background form as a compatibility trigger for older callers.
	if !item.Output.Render.Enabled && item.OverlayBackground == nil {
		return nil
	}
	planID := strings.TrimSpace(item.ID)
	if planID == "" {
		planID = plan.Title
	}
	planID += ":overlay"
	var timingArtifacts map[string]*capabilityaudio.SpeechTimingArtifact
	if processed != nil && processed.PostResult != nil {
		timingArtifacts = processed.PostResult.TimingArtifacts
	}
	var background *scriptpkg.OverlayBackgroundSpec
	if item.OverlayBackground != nil {
		resolvedBackground, backgroundErr := scriptgen.ResolveOverlayBackground(ctx, uc.overlayBackgroundSource, item.OverlayBackground)
		if backgroundErr != nil {
			return uc.logPhaseError(item, "overlay_render", scriptpkg.ErrGenerationFailed,
				fmt.Errorf("resolve overlay background: %w", backgroundErr), tracker)
		}
		background = resolvedBackground
	}
	// The run-level grounded-phrase ceiling is caller-selected
	// (item.max_phrase_overlays); zero keeps the certified default.
	overlayPlan, overlayErr := scriptgen.CompileOverlayPlanFromGenerationResult(
		ctx,
		result, scriptgen.Language(plan.Language), timingArtifacts,
		background, item.OverlayStyle, item.MaxPhraseOverlays, planID, plan.Project, uc.overlayPlanCompiler, uc.phraseAligner,
	)
	if overlayErr != nil {
		return uc.logPhaseError(item, "overlay_render", scriptpkg.ErrGenerationFailed, overlayErr, tracker)
	}
	if overlayPlan != nil {
		uc.log.Info("generate-one: overlay plan compiled",
			zap.String("plan_id", planID),
			zap.Int("items", len(overlayPlan.Items)),
			zap.Int("timed_scenes", overlayPlan.TimedScenes),
			zap.Int("recap_placements", overlayPlan.RecapPlacements),
			zap.Int("recap_cards", overlayPlan.RecapCards),
		)
	}
	if overlayPlan == nil || len(overlayPlan.Items) == 0 {
		return nil
	}
	if uc.overlayRenderEnqueuer == nil {
		return uc.logPhaseError(item, "overlay_render", scriptpkg.ErrGenerationFailed,
			fmt.Errorf("overlay render requested but Chronon enqueuer is not configured"), tracker)
	}
	ref, enqueueErr := uc.overlayRenderEnqueuer.EnqueueChrononPlanItems(ctx, *overlayPlan)
	if enqueueErr != nil {
		return uc.logPhaseError(item, "overlay_render", scriptpkg.ErrGenerationFailed,
			fmt.Errorf("enqueue Chronon overlay: %w", enqueueErr), tracker)
	}
	// Production QueueRenderEnqueuer may hand the certified artifact to
	// its bounded Drive/analytics pool before returning. A direct
	// generate_item caller has no durable Runner completion boundary, so
	// join that optional pool here before exposing the result.
	if waiter, ok := uc.overlayRenderEnqueuer.(interface{ Wait() error }); ok {
		if waitErr := waiter.Wait(); waitErr != nil {
			return uc.logPhaseError(item, "overlay_render", scriptpkg.ErrGenerationFailed,
				fmt.Errorf("overlay publication: %w", waitErr), tracker)
		}
	}
	// The batch result is the public contract returned by
	// script.generate_item. Preserve the certified identity here so
	// callers do not have to reconstruct it from logs or Drive.
	if ref.Artifact != nil {
		result.Artifacts.Overlay = &scriptpkg.DocumentOverlayRef{
			ArtifactID:   ref.Artifact.ID,
			JobID:        ref.JobID,
			URL:          ref.Artifact.URL,
			SHA256:       ref.Artifact.SHA256,
			DurationUS:   ref.Artifact.DurationUS,
			ProfileID:    ref.Artifact.ProfileID,
			CopyEligible: ref.Artifact.CopyEligible,
		}
	}
	if uc.log != nil {
		uc.log.Info("generate-one: Chronon overlay completed",
			zap.String("item_id", item.ID), zap.String("language", plan.Language),
			zap.String("plan_id", overlayPlan.PlanID), zap.Int("overlay_items", len(overlayPlan.Items)),
			zap.String("drive_link", func() string {
				if ref.Artifact != nil {
					return ref.Artifact.DriveLink
				}
				return ""
			}()))
	}
	return nil
}

func (uc *GenerateOneUseCase) recordGenerationCompletion(
	ctx context.Context,
	item scriptpkg.GenerationItemV2,
	plan scriptpkg.ResolvedGenerationPlan,
	result *scriptpkg.GenerationResult,
	resolved *scriptpkg.ResolvedSource,
	tracker *ProgressTracker,
) {
	if resolved != nil && len(resolved.SearchResults) > 0 {
		result.Source.SearchResults = resolved.SearchResults
	}

	tracker.PhaseComplete()
	if run := kernobs.FromContext(ctx); run != nil {
		result.Timings = projectCanonicalTimings(run.Report())
	}

	if uc.log != nil {
		totalMS := int64(0)
		if run := kernobs.FromContext(ctx); run != nil {
			totalMS = run.ElapsedMs()
		}
		uc.log.Info("generate-one: completed",
			zap.String("item_id", item.ID),
			zap.String("title", plan.Title),
			zap.Int("word_count", result.Output.WordCount),
			zap.String("cache_status", result.Cache.Status),
			zap.Int64("total_ms", totalMS))
	}

	// Sprint 1.3 (godlike/08): emit the canonical per-item Status
	// set by ClassifyGenerationStatus in generation_finalize.go
	// instead of the legacy hardcoded "success" string. The
	// verdict §"Usa sempre le costanti di dominio" forbids local
	// string literals; using result.Status keeps the emit surface
	// in lockstep with the classify phase.
	tracker.TrackEvent("job.completed", "Script generation completed", map[string]any{
		"item_id": item.ID,
		"status":  result.Status,
		"total_ms": func() int64 {
			if run := kernobs.FromContext(ctx); run != nil {
				return run.ElapsedMs()
			}
			return 0
		}(),
		"word_count": result.Output.WordCount,
	})
}
