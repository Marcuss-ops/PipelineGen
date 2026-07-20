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
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

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
