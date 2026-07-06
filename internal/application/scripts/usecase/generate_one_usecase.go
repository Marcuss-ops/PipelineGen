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
//   - engine_invoke.go   — logPhaseError + preConstructError + generateOnePreConstructError
//   - postprocessing.go  — reserved for future postprocess-phase extraction
//   - persistence.go     — buildGenerationResult
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
	"fmt"
	"maps"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
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
	if uc.engine == nil {
		return nil, uc.preConstructError("engine_nil", scriptpkg.ErrGenerationFailed, fmt.Errorf("engine not configured"))
	}

	startAll := time.Now()
	timings := scriptpkg.GenerationTimings{}

	// ── Phase 1: Normalize ──────────────────────────────────────────
	tracker.PhaseNormalize()
	adapters.NormalizeItem(&item, preset, uc.cfg)

	// ── Phase 2: Validate ───────────────────────────────────────────
	tracker.PhaseValidate()
	if err := ValidateItem(item); err != nil {
		return nil, uc.logPhaseError(item, "validate", scriptpkg.ErrPlanInvalid, err)
	}

	// ── Phase 3: Resolve source ─────────────────────────────────────
	tracker.PhaseResolveSource()
	sourceStart := time.Now()
	var resolved *scriptpkg.ResolvedSource
	if uc.registry != nil {
		var resolveErr error
		resCtx := buildResolutionContext(item)
		resolved, resolveErr = uc.registry.Resolve(ctx, item.Source, resCtx)
		if resolveErr != nil {
			return nil, uc.logPhaseError(item, "source_resolve", scriptpkg.ErrSourceResolutionFailed, resolveErr)
		}
	}
	timings.SourceResolveMs = time.Since(sourceStart).Milliseconds()

	// ── Phase 4: Build plan ─────────────────────────────────────────
	tracker.PhaseBuildPlan()
	planStart := time.Now()
	resolvedItem, resolveVOErr := ResolveVoiceoverFolderForItem(
		ctx, item, uc.voGroupResolver, uc.voRootID, uc.log,
	)
	if resolveVOErr != nil {
		return nil, uc.logPhaseError(item, "voiceover_resolve", scriptpkg.ErrVoiceoverResolveFailed, resolveVOErr)
	}
	item = resolvedItem
	plan := BuildPlan(item)

	// Merge resolved source into plan.
	if resolved != nil {
		if resolved.Topic != "" {
			plan.Topic = resolved.Topic
		}
		if resolved.Title != "" {
			plan.Title = resolved.Title
		}
		if resolved.SourceText != "" {
			plan.SourceText = resolved.SourceText
		}
		if resolved.ClipEvidence != nil {
			plan.ClipEvidence = resolved.ClipEvidence
		}
		if resolved.Fingerprint != "" {
			plan.SourceFingerprint = resolved.Fingerprint
		}
		if resolved.Type != "" {
			plan.SourceKind = string(resolved.Type)
		}
	}
	plan.CacheKey = scriptpkg.BuildCacheKey(&plan)
	timings.PlanBuildMs = time.Since(planStart).Milliseconds()

	if uc.ppReg != nil {
		if err := uc.ppReg.ValidateRequested(plan.Postprocessors); err != nil {
			return nil, uc.logPhaseError(item, "registry_validate", scriptpkg.ErrPlanInvalid, err)
		}
	}

	// ── Phase 5: Generate script ────────────────────────────────────
	tracker.PhaseGenerateStart()
	engineStart := time.Now()
	engineResult, engineErr := uc.engine.Generate(ctx, &plan)
	if engineErr != nil {
		genErr := &scriptpkg.GenerationError{
			ItemID: item.ID,
			Phase:  "engine",
			Inner:  fmt.Errorf("ollama generation failed: %w", engineErr),
		}
		return nil, uc.logPhaseError(item, "engine", scriptpkg.ErrGenerationFailed, genErr)
	}
	timings.EngineMs = time.Since(engineStart).Milliseconds()
	tracker.PhaseGenerateDone()

	// ── Phase 6: Postprocess ────────────────────────────────────────
	timings.PostprocessMs = make(map[string]int64)
	var postResult *adapters.PipelineResult
	if uc.ppReg != nil {
		for _, pp := range plan.Postprocessors {
			tracker.PhasePostprocess(pp)
		}
		procInput := adapters.ProcessInput{
			Text:        engineResult.Output.Text,
			WordCount:   engineResult.WordCount,
			SpecScene:   engineResult.Output.SpecScene,
			ModelUsed:   engineResult.Model,
			CacheStatus: engineResult.CacheStatus,
			SourceTrace: engineResult.ClipEvidence,
		}
		var ppErr error
		postResult, ppErr = uc.ppReg.Run(ctx, &plan, procInput)
		if ppErr != nil {
			ppErrStruct := &scriptpkg.PostprocessError{
				ItemID:    item.ID,
				Processor: "registry",
				Inner:     ppErr,
			}
			return nil, uc.logPhaseError(item, "postprocess", scriptpkg.ErrPostprocessFailed, ppErrStruct)
		}
		if postResult != nil && len(postResult.StageDurations) > 0 {
			timings.PostprocessMs = maps.Clone(postResult.StageDurations)
		}
	}

	// ── Phase 7: Build result ───────────────────────────────────────
	result := buildGenerationResult(item, plan, engineResult, postResult, timings)
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

	return result, nil
}
