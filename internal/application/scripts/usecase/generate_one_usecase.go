// Package scripts — generate_one_usecase.go is the canonical
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
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// GenerateOneUseCase orchestrates the unified pipeline for a single
// generation item. All dependencies are typed — no interface{} on
// the public surface.
type GenerateOneUseCase struct {
	cfg             adapters.NormalizationConfig
	registry        *adapters.SourceRegistry
	engine          *Engine
	ppReg           *adapters.PostProcessorRegistry
	log             *zap.Logger
	voGroupResolver scriptports.VoiceoverGroupResolver
	voRootID        string
}

// NewGenerateOneUseCase constructs the use case. engine and registry
// must be non-nil; ppReg may be nil (postprocessors are skipped).
// The voiceover_group resolver is optional — composition root wires
// it via SetVoiceoverRouting (post-construction, additive) so test
// fixtures that don't exercise routing continue to work without
// parameter churn.
func NewGenerateOneUseCase(
	cfg adapters.NormalizationConfig,
	registry *adapters.SourceRegistry,
	engine *Engine,
	ppReg *adapters.PostProcessorRegistry,
	log *zap.Logger,
) *GenerateOneUseCase {
	return &GenerateOneUseCase{
		cfg:      cfg,
		registry: registry,
		engine:   engine,
		ppReg:    ppReg,
		log:      log,
	}
}

// SetVoiceoverRouting wires the resolver and parent ID used by the
// pre-BuildPlan step (fix/voiceover-group-resolver, June 2026).
// Optional: if not called, resolver is nil and
// ResolveVoiceoverFolderForItem is a no-op (the existing test
// fixtures and default compositions skip this call, preserving
// behavior parity with pre-PR scripts).
//
// Pass an empty parentID to disable routing at runtime without
// nil-checking the resolver; an empty parentID makes the resolver
// return immediately because parentID == "" is rejected by the
// underlying GroupsResolver.
func (uc *GenerateOneUseCase) SetVoiceoverRouting(resolver scriptports.VoiceoverGroupResolver, parentID string) {
	if uc == nil {
		return
	}
	uc.voGroupResolver = resolver
	uc.voRootID = parentID
}

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
		// PR-ERROR-SURFACING commit-5 (2026-07-04): route the uc=nil
		// pre-construction path through a zero-item logPhaseError
		// variant so errors.Is(err, ErrScriptGenerationFailed)
		// matches (was missing pre-commit-5). The construction-failure
		// envelope shares the umbrella sentinel with every execute-
		// time phase. uc.log is nil here (uc itself is nil) so the
		// log entry is suppressed; the typed-error chain is still the
		// canonical propagation surface for handlers to read via
		// errors.Is (per godlike/07 fail-closed).
		return nil, generateOnePreConstructError(nil, "uc_nil", scriptpkg.ErrGenerationFailed, fmt.Errorf("use case not constructed"))
	}
	if uc.engine == nil {
		// PR-ERROR-SURFACING commit-5 (2026-07-04): route the
		// engine=nil pre-construction path through the same umbrella
		// envelope. uc is non-nil here, so the original
		// `uc.log.Warn("generate-one: construction failed",
		// zap.String("reason", "engine_nil"))` diagnostic line is
		// PRESERVED inside generateOnePreConstructError (it inspects
		// the non-nil uc via the receiver).
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
		// PR 4 (June 2026): build SourceResolutionContext from the
		// item so resolvers see operator-side traits
		// (Language, Tone, Model, Style, TargetWords) instead of
		// hijacking SourceSpec.Guidelines as a stand-in for language.
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
	// fix/voiceover-group-resolver (June 2026): resolve
	// item.Output.VoiceoverGroup → item.Output.VoiceoverFolderID
	// BEFORE BuildPlan copies the field onto plan.VoiceoverFolderID,
	// so the voiceover processor uses GenerateWithDestination with
	// the resolved folder rather than falling back to the default
	// folder + warning the operator. Idempotent no-op when the
	// resolver is nil (test fixtures / compositions without
	// routing support) or when the group name is empty.
	resolvedItem, resolveVOErr := ResolveVoiceoverFolderForItem(
		ctx, item, uc.voGroupResolver, uc.voRootID, uc.log,
	)
	// PR-ERROR-SURFACING commit-5 (2026-07-04): route voiceover_resolve
	// through logPhaseError (was inline log + bare-escape). Adds the
	// umbrella sentinel ErrScriptGenerationFailed to the chain so
	// godlike/07 callers can `errors.Is(err, ErrScriptGenerationFailed)`
	// uniformly across ALL phase failure paths. Phase sentinel is
	// ErrVoiceoverResolveFailed — NOT ErrSourceResolutionFailed — to
	// keep failure-domain classification clean (clip-search vs
	// folder-routing are distinct domains per godlike/06 SSOT).
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
		// PR 2: fingerprint goes to SourceFingerprint (cache-key
		// input), not Prompt (model input). Editorial guidelines
		// already came from item.Source.Guidelines via BuildPlan.
		if resolved.Fingerprint != "" {
			plan.SourceFingerprint = resolved.Fingerprint
		}
		if resolved.Type != "" {
			plan.SourceKind = string(resolved.Type)
		}
	}
	// PR 2: compute the canonical cache key once the plan is fully
	// resolved — the engine feeds it to the memory gate.
	plan.CacheKey = scriptpkg.BuildCacheKey(&plan)
	timings.PlanBuildMs = time.Since(planStart).Milliseconds()

	// PR 2 (June 2026): postprocessor preflight. After the plan
	// is fully resolved AND the cache key is computed, check the
	// postprocessor registry BEFORE invoking the model. A
	// ProcessorRequired processor that the caller requested but
	// composition failed to register (e.g. typing under refactor)
	// causes Execute to short-circuit with a typed PlanInvalidError
	// — no Ollama call is issued. Best-effort missing processors
	// are tolerated (registry.Run will warn).
	if uc.ppReg != nil {
		if err := uc.ppReg.ValidateRequested(plan.Postprocessors); err != nil {
			return nil, uc.logPhaseError(item, "registry_validate", scriptpkg.ErrPlanInvalid, err)
		}
	}

	// ── Phase 5: Generate script ────────────────────────────────────
	tracker.PhaseGenerateStart()
	engineStart := time.Now()

	// Pass the resolved plan directly to the engine.
	// The engine owns: memory gate check, ollama invocation,
	// optional DB persistence. No WriteScriptRequest needed.
	engineResult, engineErr := uc.engine.Generate(ctx, &plan)
	// PR-ERROR-SURFACING commit-5 (2026-07-04): route engine
	// errors through logPhaseError so the umbrella sentinel
	// ErrScriptGenerationFailed + phase sentinel ErrGenerationFailed
	// + typed *scriptpkg.GenerationError struct ALL appear in the
	// error chain. errors.Is(err, ErrScriptGenerationFailed) ✓;
	// errors.Is(err, ErrGenerationFailed) ✓ (via struct Unwrap);
	// errors.As(err, &GenerationError{}) ✓ (struct itself is in the
	// Go 1.20+ multi-`%w` chain via logPhaseError).
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
		var ppErr error
		// PR 5: build the typed adapters.ProcessInput envelope from the
		// engine result. PersistenceProcessor consumes the typed
		// fields (WordCount, SpecScene, ModelUsed, CacheStatus,
		// SourceTrace); non-persistence processors consume
		// input.Text. Plan.SaveToDB -> "persistence" in
		// Postprocessors list (set by generation_plan_builder.go
		// via buildPostprocessorList) is now the ONLY trigger for
		// script-table writes.
		procInput := adapters.ProcessInput{
			Text:        engineResult.Output.Text,
			WordCount:   engineResult.WordCount,
			SpecScene:   engineResult.Output.SpecScene,
			ModelUsed:   engineResult.Model,
			CacheStatus: engineResult.CacheStatus,
			SourceTrace: engineResult.ClipEvidence,
		}
		postResult, ppErr = uc.ppReg.Run(ctx, &plan, procInput)
		// PR-ERROR-SURFACING commit-5 (2026-07-04): route postprocess
		// errors through logPhaseError so the umbrella sentinel
		// ErrScriptGenerationFailed + phase sentinel ErrPostprocessFailed
		// + typed *scriptpkg.PostprocessError struct ALL appear in the
		// error chain. Mirror of the engine rewrap above.
		if ppErr != nil {
			ppErrStruct := &scriptpkg.PostprocessError{
				ItemID:    item.ID,
				Processor: "registry",
				Inner:     ppErr,
			}
			return nil, uc.logPhaseError(item, "postprocess", scriptpkg.ErrPostprocessFailed, ppErrStruct)
		}
		// Issue #3 (June 2026): stream per-processor wall-clock timing
		// straight from the registry's StageDurations map. Upstream
		// `Run()` in PostProcessorRegistry records `elapsed =
		// time.Since(start)` for every enabled processor in the
		// loop (success + nil-result + error paths at
		// postprocessor_registry.go around lines 497, 513, 524). The
		// previous uniform-division approximation
		// (`ppMs / int64(len(plan.Postprocessors))`) hid real
		// per-stage variance in the timings payload — Issue #3
		// replaces it with the registry's authoritative per-stage
		// measurement so `GenerationTimings.PostprocessMs` reflects
		// actual elapsed wall-clock for each processor that ran.
		//
		// `maps.Clone` (Go 1.21+) copies the map header so future
		// mutations on the registry side cannot reach into the
		// timings payload (defensive against the pipeline later
		// sharing the same map reference).
		//
		// Empty-or-nil StageDurations guard: when the loop
		// short-circuited (empty `Postprocessors` list) or every
		// requested processor was missing-registered (registry
		// doesn't write a StageDurations entry for skipped names)
		// the StageDurations map stays empty — `timings.PostprocessMs`
		// keeps its zero-init from the line above so consumers
		// reading `len(timings.PostprocessMs) == 0` continue to see
		// "no postprocessing happened" without an explicit zero
		// sentinel.
		if postResult != nil && len(postResult.StageDurations) > 0 {
			timings.PostprocessMs = maps.Clone(postResult.StageDurations)
		}
	}

	// ── Phase 7: Build result ───────────────────────────────────────
	result := buildGenerationResult(item, plan, engineResult, postResult, timings)
	timings.TotalMs = time.Since(startAll).Milliseconds()
	result.Timings = timings

	// Merge search results from resolved source.
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

// ── Helpers (extracted to generate_one_usecase_plan.go, _execute.go, _persist.go) ──
//
// Plan phase:    buildResolutionContext → generate_one_usecase_plan.go
// Execute phase: logPhaseError, preConstructError, generateOnePreConstructError → generate_one_usecase_execute.go
// Persist phase: buildGenerationResult → generate_one_usecase_persist.go
