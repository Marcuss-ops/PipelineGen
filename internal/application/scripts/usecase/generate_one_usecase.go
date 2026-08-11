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
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

func (uc *GenerateOneUseCase) renderCombinedAudio(ctx context.Context, item scriptpkg.GenerationItemV2, result *scriptpkg.GenerationResult, post *adapters.PipelineResult) error {
	started := time.Now()
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
	var start int64
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
		intent := capabilityaudio.AudioIntent{Mode: mode, SourceInMS: scene.AudioSourceInMS, SourceOutMS: scene.AudioSourceOutMS}
		video := capabilityaudio.VideoSegment{}
		if scene.Bindings.Clip != nil {
			video.AssetID = scene.Bindings.Clip.ClipID
			video.SourceInMS = scene.Bindings.Clip.StartMs
			video.SourceOutMS = scene.Bindings.Clip.EndMs
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
		timeline.Segments = append(timeline.Segments, capabilityaudio.TimelineSegment{ID: scene.ID, Index: i, TimelineStartMS: start, DurationMS: duration, Video: video, Audio: intent})
		start += duration
	}
	timeline.DurationMS = start
	plan, err := capabilityaudio.Compile(timeline, capabilityaudio.DefaultAudioProfile())
	if err != nil {
		return err
	}
	output := filepath.Join(os.TempDir(), "pipelinegen-final-audio-"+plan.PlanSHA256+".m4a")
	asset, err := uc.audioProcessor.RenderAudioPlan(ctx, plan, assets, output)
	if err != nil {
		return fmt.Errorf("render final audio: %w", err)
	}
	if err := capabilityaudio.ValidateFinalAudio(asset, plan); err != nil {
		return fmt.Errorf("final audio certification failed: %w", err)
	}
	result.AudioStrategy = "FINAL_AUDIO_COPY"
	result.Timings.AudioPipelineTotalMs = time.Since(started).Milliseconds()
	result.Timings.AudioEncodePasses = 1
	result.Timings.FinalAudioDurationMS = asset.DurationMS
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

	startAll := time.Now()
	timings := scriptpkg.GenerationTimings{}

	// ── Phases 1-4: Prepare ─────────────────────────────────────────
	prepared, err := uc.preparer.Prepare(ctx, item, preset, tracker)
	if err != nil {
		return nil, err
	}
	item = prepared.Item
	plan := prepared.Plan
	resolved := prepared.ResolvedSource
	timings.SourceResolveMs = prepared.SourceResolveMs
	timings.PlanBuildMs = prepared.PlanBuildMs

	// ── Phase 5: Generate script ────────────────────────────────────
	draft, err := uc.engineRunner.Generate(ctx, item, plan, tracker)
	if err != nil {
		return nil, uc.logPhaseError(item, "engine", scriptpkg.ErrGenerationFailed, err, tracker)
	}
	engineResult := draft.EngineResult
	timings.EngineMs = draft.EngineMs

	// ── Phase 6: Postprocess ────────────────────────────────────────
	processed, err := uc.postprocessor.Process(ctx, item, plan, engineResult, tracker)
	if err != nil {
		return nil, uc.logPhaseError(item, "postprocess", scriptpkg.ErrPostprocessFailed, err, tracker)
	}
	postResult := processed.PostResult
	timings.PostprocessMs = processed.PostprocessMs
	vidrushTimings := VidRushTimingFields(processed.PostprocessMs)
	timings.SegmentExtractionMs = vidrushTimings.SegmentExtractionMs
	timings.QueryGenerationMs = vidrushTimings.QueryGenerationMs
	timings.ArtlistSearchMs = vidrushTimings.ArtlistSearchMs
	timings.InternetImageSearchMs = vidrushTimings.InternetImageSearchMs
	timings.ImageGenerationMs = vidrushTimings.ImageGenerationMs
	timings.SQLiteMs = vidrushTimings.SQLiteMs
	timings.BindingMs = vidrushTimings.BindingMs
	provenance := processed.Provenance

	// ── Phase 7-9: Finalize ────────────────────────────────────────
	result, err := uc.finalizer.Finalize(ctx, FinalizeInputs{
		Item:         item,
		Plan:         plan,
		EngineResult: engineResult,
		PostResult:   postResult,
		Provenance:   provenance,
		Timings:      timings,
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
		if err := uc.renderCombinedAudio(ctx, item, result, processed.PostResult); err != nil {
			return nil, uc.logPhaseError(item, "combined_audio", scriptpkg.ErrGenerationFailed, err, tracker)
		}
		timings.AudioPipelineTotalMs = result.Timings.AudioPipelineTotalMs
		timings.AudioEncodePasses = result.Timings.AudioEncodePasses
		timings.FinalAudioDurationMS = result.Timings.FinalAudioDurationMS
	} else if plan.AudioMode == "CHUNKED_VOICEOVER" {
		result.AudioStrategy = "TIMELINE_MIX"
		if result.FinalAudio != nil {
			return nil, uc.logPhaseError(item, "audio_mode", scriptpkg.ErrGenerationFailed, fmt.Errorf("CHUNKED_VOICEOVER must not produce final_audio"), tracker)
		}
	}

	timings.TotalMs = time.Since(startAll).Milliseconds()
	result.Timings = timings

	if resolved != nil && len(resolved.SearchResults) > 0 {
		result.Source.SearchResults = resolved.SearchResults
	}

	tracker.PhaseComplete()

	if uc.log != nil {
		uc.log.Info("generate-one: completed",
			zap.String("item_id", item.ID),
			zap.String("title", plan.Title),
			zap.Int("word_count", result.Output.WordCount),
			zap.String("cache_status", result.Cache.Status),
			zap.Int64("total_ms", timings.TotalMs))
	}

	// Sprint 1.3 (godlike/08): emit the canonical per-item Status
	// set by ClassifyGenerationStatus in generation_finalize.go
	// instead of the legacy hardcoded "success" string. The
	// verdict §"Usa sempre le costanti di dominio" forbids local
	// string literals; using result.Status keeps the emit surface
	// in lockstep with the classify phase.
	tracker.TrackEvent("job.completed", "Script generation completed", map[string]any{
		"item_id":    item.ID,
		"status":     result.Status,
		"total_ms":   timings.TotalMs,
		"word_count": result.Output.WordCount,
	})

	return result, nil
}
