// Package usecase — generate_one_usecase.go is the canonical
// single-item script-generation orchestrator. It executes the
// unified pipeline for exactly one GenerationItemV2:
//
//	normalize → validate → resolve source → build plan → generate → postprocess → typed result
//
// This use case replaces the 3-way switch (clip-explicit /
// auto-search / text-only) in pipeline_run.go and the duplicated
// engine-call + postprocess logic across pipeline_handlers.go,
// catalog_job.go, curation_job.go, and media_curator.go.
//
// Split topology (July 2026):
//
//   - plan_resolution.go — GenerateOneUseCase struct + ctor + SetVoiceoverRouting +
//     buildResolutionContext
//   - engine_invoke.go          — logPhaseError + preConstructError + generateOnePreConstructError
//   - generation_postprocess.go — GenerationPostprocessor + ProcessedGeneration
//   - generation_finalize.go    — GenerationFinalizer + FinalizeInputs
//   - persistence.go            — buildGenerationResult
//   - generate_one_usecase.go (this file) — Execute orchestrator
//
// Dependencies:
//   - adapters.NormalizationConfig: config-driven defaults
//   - adapters.SourceRegistry: resolves source → ResolvedSource
//   - Engine: calls ollama for script text
//   - adapters.PostProcessorRegistry: runs postprocessors (entities, metadata,
//     voiceover, images, document, persistence)
//   - ports.VoiceoverGroupResolver + parent ID: optional. Set via
//     SetVoiceoverRouting so callers passing only `voiceover_group`
//     have their item.Output.VoiceoverFolderID populated BEFORE
//     BuildPlan runs (fix/voiceover-group-resolver, June 2026).
package usecase

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"

	"go.uber.org/zap"
)

func microseconds(milliseconds int64) (int64, error) {
	return capabilityaudio.MicrosecondsFromMilliseconds(milliseconds)
}

func (uc *GenerateOneUseCase) renderCombinedAudio(ctx context.Context, item scriptpkg.GenerationItemV2, result *scriptpkg.GenerationResult, post *adapters.PipelineResult) error {
	if uc == nil || uc.audioProcessor == nil {
		return fmt.Errorf("COMBINED_TIMELINE requires a configured audio processor")
	}
	if result == nil {
		return fmt.Errorf("combined audio result is nil")
	}
	timeline := capabilityaudio.CanonicalTimeline{Version: capabilityaudio.TimelineVersion}
	assets := capabilityaudio.ResolvedAudioAssets{}
	seen := map[string]bool{}
	add := func(id, path string) error {
		if strings.TrimSpace(id) == "" || strings.TrimSpace(path) == "" || !fileExists(path) {
			return fmt.Errorf("required audio asset %q is unavailable", id)
		}
		if !seen[id] {
			assets = append(assets, capabilityaudio.ResolvedAudioAsset{AssetID: id, Path: path})
			seen[id] = true
		}
		return nil
	}
	var startUS int64
	for i, scene := range result.Output.SpecScene.Scenes {
		if scene.Index != i || strings.TrimSpace(scene.ID) == "" {
			return fmt.Errorf("canonical audio scene %d is invalid", i)
		}
		duration := sceneDurationMS(scene)
		if duration <= 0 {
			return fmt.Errorf("scene %s has no canonical duration", scene.ID)
		}
		mode := capabilityaudio.AudioSegmentMode(strings.ToUpper(strings.TrimSpace(scene.AudioMode)))
		if mode != capabilityaudio.AudioVoiceover && mode != capabilityaudio.AudioClip && mode != capabilityaudio.AudioSilence {
			return fmt.Errorf("scene %s requires explicit audio_mode", scene.ID)
		}
		audioSourceInUS, err := microseconds(scene.AudioSourceInMS)
		if err != nil {
			return fmt.Errorf("scene %s audio source start: %w", scene.ID, err)
		}
		if scene.AudioSourceOutMS < scene.AudioSourceInMS {
			return fmt.Errorf("scene %s audio source range is inverted", scene.ID)
		}
		audioSourceDurationMS := int64(0)
		if scene.AudioSourceOutMS > scene.AudioSourceInMS {
			audioSourceDurationMS = scene.AudioSourceOutMS - scene.AudioSourceInMS
		}
		audioSourceDurationUS, err := microseconds(audioSourceDurationMS)
		if err != nil {
			return fmt.Errorf("scene %s audio source duration: %w", scene.ID, err)
		}
		intent := capabilityaudio.AudioIntent{Mode: mode, SourceInUS: audioSourceInUS, SourceDurationUS: audioSourceDurationUS}
		video := capabilityaudio.VideoSegment{}
		if scene.Bindings.Clip != nil {
			clipStartUS, err := microseconds(scene.Bindings.Clip.StartMs)
			if err != nil {
				return fmt.Errorf("scene %s video source start: %w", scene.ID, err)
			}
			clipDurationUS, err := microseconds(scene.Bindings.Clip.EndMs - scene.Bindings.Clip.StartMs)
			if err != nil {
				return fmt.Errorf("scene %s video source duration: %w", scene.ID, err)
			}
			video = capabilityaudio.VideoSegment{AssetID: scene.Bindings.Clip.ClipID, SourceInUS: clipStartUS, SourceDurationUS: clipDurationUS}
		}
		switch mode {
		case capabilityaudio.AudioVoiceover:
			binding := scene.Bindings.Voiceover
			if binding == nil || binding.Status == "failed" {
				return fmt.Errorf("scene %s voiceover is missing", scene.ID)
			}
			id := fmt.Sprintf("vo:%s:%s:%s", item.ID, item.Language, scene.ID)
			intent.VoiceoverAssetID = id
			if err := add(id, binding.LocalPath); err != nil {
				return err
			}
		case capabilityaudio.AudioClip:
			if strings.TrimSpace(scene.AudioAssetID) == "" || scene.AudioSourceOutMS <= scene.AudioSourceInMS {
				return fmt.Errorf("scene %s clip audio intent is incomplete", scene.ID)
			}
			intent.ClipAssetID = scene.AudioAssetID
			path := findSegmentAssetPath(post, scene.AudioAssetID)
			if err := add(scene.AudioAssetID, path); err != nil {
				return err
			}
		}
		durationUS, err := microseconds(duration)
		if err != nil {
			return fmt.Errorf("scene %s duration: %w", scene.ID, err)
		}
		timeline.Segments = append(timeline.Segments, capabilityaudio.TimelineSegment{ID: scene.ID, Index: i, TimelineStartUS: startUS, DurationUS: durationUS, Video: video, Audio: intent})
		if startUS > math.MaxInt64-durationUS {
			return fmt.Errorf("scene %s timeline duration overflows", scene.ID)
		}
		startUS += durationUS
	}
	timeline.DurationUS = startUS
	plan, err := capabilityaudio.Compile(timeline, capabilityaudio.DefaultAudioProfile())
	if err != nil {
		return err
	}
	output := filepath.Join(os.TempDir(), "pipelinegen-final-audio-"+plan.PlanSHA256+".m4a")
	// rust.audio_render is the Rust media-plane boundary; it nests under the
	// audio.pipeline STAGE and shares the canonical Run clock.
	var asset capabilityaudio.FinalAudioAsset
	if err := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
		Stage:     scriptgen.StageAudioPipeline,
		Component: kernobs.ComponentName("rust"),
		Operation: kernobs.OperationName("audio_render"),
	}, func(opCtx context.Context) error {
		var renderErr error
		asset, renderErr = uc.audioProcessor.RenderAudioPlan(opCtx, plan, assets, output)
		return renderErr
	}); err != nil {
		return fmt.Errorf("render final audio: %w", err)
	}
	if err := capabilityaudio.ValidateFinalAudio(asset, plan); err != nil {
		return fmt.Errorf("final audio certification failed: %w", err)
	}
	result.AudioStrategy = "FINAL_AUDIO_COPY"
	result.FinalAudio = &scriptpkg.FinalAudioArtifact{AssetID: asset.AssetID, Path: output, AudioContractVersion: asset.AudioContractVersion, AudioPlanVersion: asset.AudioPlanVersion, AudioPlanSHA256: asset.AudioPlanSHA256, FinalAudioSHA256: asset.FinalAudioSHA256, Codec: asset.Codec, Profile: asset.Profile, SampleRate: asset.SampleRate, Channels: asset.Channels, ChannelLayout: asset.ChannelLayout, Bitrate: asset.Bitrate, DurationMS: asset.DurationMS, StartPTS: asset.StartPTS, SizeBytes: asset.SizeBytes, FinalMix: asset.FinalMix, CopyEligible: asset.CopyEligible}
	return nil
}

func sceneDurationMS(scene scriptpkg.SpecScene) int64 {
	if scene.Bindings.Clip != nil && scene.Bindings.Clip.DurationMs > 0 {
		return scene.Bindings.Clip.DurationMs
	}
	if scene.Bindings.Stock != nil && scene.Bindings.Stock.DurationMs > 0 {
		return scene.Bindings.Stock.DurationMs
	}
	if scene.Bindings.Voiceover != nil {
		return scene.Bindings.Voiceover.DurationMs
	}
	return 0
}

func findSegmentAssetPath(post *adapters.PipelineResult, assetID string) string {
	if post == nil {
		return ""
	}
	for _, segment := range post.VidRushSegments {
		if segment.Assets.PrimaryVideo != nil && segment.Assets.PrimaryVideo.AssetID == assetID {
			return segment.Assets.PrimaryVideo.LocalPath
		}
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

var _ mediaexec.AudioProcessor

// Execute runs the full pipeline for one item and returns a typed
// GenerationResult. Progress is reported through the tracker when
// non-nil.
//
// godlike/07 typed-error gate (SCRIPT-T03-USECASE closure, July 2026):
// every `return nil, err` at the orchestrator boundary logs the
// diagnostic context (item_id, phase, error) via uc.log.Warn BEFORE
// returning the typed error. The typed error remains the propagation
// surface (handler reads it via errors.Is for HTTP status mapping)
// but the operator now has a log trail for every failure. This is
// the canonical "log+typed-propagate" pattern per godlike/07
// NO_FAKE_AVAILABILITY + TYPED_ERROR contract.
func (uc *GenerateOneUseCase) Execute(
	ctx context.Context,
	item scriptpkg.GenerationItemV2,
	preset scriptpkg.Preset,
	tracker *ProgressTracker,
) (*scriptpkg.GenerationResult, error) {
	if uc == nil {
		return nil, generateOnePreConstructError(nil, "uc_nil", scriptpkg.ErrGenerationFailed, fmt.Errorf("use case not constructed"))
	}
	if uc.engineRunner == nil {
		return nil, uc.preConstructError("engine_nil", scriptpkg.ErrGenerationFailed, fmt.Errorf("engine not configured"))
	}

	// ── Phases 1-4: Prepare ─────────────────────────────────────────
	var prepared *PreparedGeneration
	_, err := kernobs.MeasureStageReport(ctx, scriptgen.StageScriptPrepare, func(stageCtx context.Context) error {
		var prepareErr error
		prepared, _, prepareErr = uc.preparer.Prepare(stageCtx, item, preset, tracker)
		return prepareErr
	})
	if err != nil {
		return nil, err
	}
	item = prepared.Item
	plan := prepared.Plan
	resolved := prepared.ResolvedSource

	// ── Phase 5: Generate script ────────────────────────────────────
	draft, err := uc.engineRunner.Generate(ctx, item, plan, tracker)
	if err != nil {
		return nil, uc.logPhaseError(item, "engine", scriptpkg.ErrGenerationFailed, err, tracker)
	}
	engineResult := draft.EngineResult

	// ── Phase 6: Postprocess ────────────────────────────────────────
	// script.postprocess is the parent STAGE; the per-processor stages
	// (entities, persistence, document, ...) are already recorded inside
	// the registry on the same canonical clock.
	var processed *ProcessedGeneration
	if _, err = kernobs.MeasureStageReport(ctx, scriptgen.StageScriptPostprocess, func(stageCtx context.Context) error {
		var ppErr error
		processed, ppErr = uc.postprocessor.Process(stageCtx, item, plan, engineResult, tracker)
		return ppErr
	}); err != nil {
		return nil, uc.logPhaseError(item, "postprocess", scriptpkg.ErrPostprocessFailed, err, tracker)
	}
	postResult := processed.PostResult
	provenance := processed.Provenance

	// ── Phase 7-9: Finalize ────────────────────────────────────────
	result, err := uc.finalizer.Finalize(ctx, FinalizeInputs{
		Item:         item,
		Plan:         plan,
		EngineResult: engineResult,
		PostResult:   postResult,
		Provenance:   provenance,
	}, tracker)
	if err != nil {
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
	if plan.AudioMode == "COMBINED_TIMELINE" {
		// audio.pipeline is a STAGE boundary; the canonical Run clock is the
		// only wall-time source (the legacy AudioPipelineTotalMs field remains
		// a compatibility projection populated by renderCombinedAudio).
		if _, audioErr := kernobs.MeasureStageReport(ctx, scriptgen.StageAudioPipeline, func(stageCtx context.Context) error {
			return uc.renderCombinedAudio(stageCtx, item, result, processed.PostResult)
		}); audioErr != nil {
			return nil, uc.logPhaseError(item, "combined_audio", scriptpkg.ErrGenerationFailed, audioErr, tracker)
		}
	} else if plan.AudioMode == "CHUNKED_VOICEOVER" {
		result.AudioStrategy = "TIMELINE_MIX"
		if result.FinalAudio != nil {
			return nil, uc.logPhaseError(item, "audio_mode", scriptpkg.ErrGenerationFailed, fmt.Errorf("CHUNKED_VOICEOVER must not produce final_audio"), tracker)
		}
	}

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

	return result, nil
}

func projectCanonicalTimings(report *kernobs.RunReport) scriptpkg.GenerationTimings {
	var out scriptpkg.GenerationTimings
	if report == nil {
		return out
	}
	out.PostprocessMs = make(map[string]int64)
	for _, stage := range report.Stages {
		switch stage.Name {
		case "source.resolve":
			out.SourceResolveMs = stage.DurationMs
		case "script.plan":
			out.PlanBuildMs = stage.DurationMs
		case "script.engine":
			out.EngineMs = stage.DurationMs
		case "script.prepare", "script.normalize", "script.validate", "script.postprocess", "audio.pipeline":
			// These are canonical orchestration stages, not postprocessor
			// operations. Keep them in RunReport only; PostprocessMs is a
			// compatibility projection of actual postprocessor observations.
		default:
			out.PostprocessMs[stage.Name] = stage.DurationMs
		}
	}
	out.TotalMs = report.WallTimeMs
	return out
}
