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
	scriptdto "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/dto"
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

// ── Helpers ──────────────────────────────────────────────────────────

// buildResolutionContext constructs a SourceResolutionContext from a
// GenerationItemV2. PR 4 (June 2026): the resolver signature now
// expects resolution-context as a separate arg so resolvers see
// operator-side traits (language, tone, model, style, target words)
// without hijacking SourceSpec.Guidelines. SourceSpec.Guidelines
// remains for the pure-text editorial-overrides path; here we
// explicitly read operator intent from item-style fields.
//
// Field mapping:
//   - ItemID    — item.ID
//   - Title     — item.Title (canonical document title)
//   - Language  — item.Language (real target language; the curate
//     resolver previously hijacked Guidelines here —
//     the bug class)
//   - Tone      — item.Tone
//   - Model     — item.Model
//   - Style     — item.Style
//   - TargetWords — item.ScriptParams.TargetWords
func buildResolutionContext(item scriptpkg.GenerationItemV2) scriptpkg.SourceResolutionContext {
	return scriptpkg.SourceResolutionContext{
		ItemID:        item.ID,
		Title:         item.Title,
		Language:      item.Language,
		Tone:          item.Tone,
		Model:         item.Model,
		Style:         item.Style,
		TargetWords:   item.ScriptParams.TargetWords,
		NumClips:      item.Source.NumClips,
		SegmentWords:  item.ScriptParams.SegmentWords,
		SegmentTopics: append([]string(nil), item.ScriptParams.SegmentTopics...),
		// P0 #3 (June 2026): DriveLink is only required when the
		// caller wants document or scene images. For text-only
		// generation, clips without Drive links are still usable.
		RequireDriveLink: item.Output.GenerateDocument || item.Output.GenerateSceneImages,
	}
}

// buildGenerationResult constructs a GenerationResult from the
// engine and postprocessor outputs. PR 13: populates ONLY the
// canonical nested fields (Output, Source, Cache, Artifacts).
// The deprecated flat fields were removed in PR 13.
func buildGenerationResult(
	item scriptpkg.GenerationItemV2,
	plan scriptpkg.ResolvedGenerationPlan,
	engineResult *EngineResult,
	postResult *adapters.PipelineResult,
	timings scriptpkg.GenerationTimings,
) *scriptpkg.GenerationResult {
	cacheHit := engineResult.CacheStatus == "exact_hit"

	// PR 5: ScriptID is sourced from postResult.ScriptID (set by
	// PersistenceProcessor), NOT from engineResult.ScriptID (which
	// no longer exists post-PR 5). When the persistence processor
	// is not in the plan's Postprocessors list, ScriptID is zero.
	scriptIDFromPostprocess := int64(0)
	if postResult != nil {
		scriptIDFromPostprocess = postResult.ScriptID
	}

	// Issue #1 (June 2026): prefer postResult.FinalSpecScene over
	// engineResult.Output.SpecScene when populated. The
	// clip-bindings prose-fallback heuristic (FASE 3) can
	// synthesise scenes from prose when the model returns no
	// SpecScene. Pre-fix: buildGenerationResult always read the
	// pre-walk engineResult.Output.SpecScene, so the canonical
	// GenerationResult carried an empty SpecScene even when the
	// registry's PipelineResult.SynthesizedScenes held the
	// synthesised bundle — the JSON response, document body,
	// persistence row, image prompts, and voiceover plan all saw
	// empty scenes. Post-fix: registry.Run captures the post-walk
	// envelope in PipelineResult.FinalSpecScene; below selects it
	// when non-empty. The empty-aware guard keeps the
	// normal-model-output path unaffected (when the engine emits
	// scenes AND the heuristic does NOT engage, postResult
	// .FinalSpecScene mirrors input.SpecScene == engineResult
	// .Output.SpecScene, so the swap is a no-op).
	specScene := engineResult.Output.SpecScene
	if postResult != nil && len(postResult.FinalSpecScene.Scenes) > 0 {
		specScene = postResult.FinalSpecScene
	}

	result := &scriptpkg.GenerationResult{
		ItemID:   item.ID,
		ScriptID: scriptIDFromPostprocess,
		Title:    plan.Title,
		Language: plan.Language,
		Model:    engineResult.Model,
		Output: scriptpkg.ScriptOutput{
			Text:      engineResult.Output.Text,
			WordCount: engineResult.WordCount,
			SpecScene: specScene,
		},
		Cache: scriptpkg.CacheResult{
			Status: engineResult.CacheStatus,
			Hit:    cacheHit,
		},
		Timings: timings,
	}

	// Populate Source trace.
	var sourceTrace scriptpkg.SourceTrace

	// PR 7 (June 2026): the model-emitted SpecScene goes through
	// the post-processor walk BEFORE this function is called. The
	// walkway runs ClipBindingsProcessor (when "clip_bindings" is
	// in plan.Postprocessors, which buildPostprocessorList always
	// inserts) which assigns `scene.Bindings.Clip = &ClipBinding{
	// ClipID: canonical, DriveLink: canonical_url }` UNCONDITIONALLY
	// for every scene. The slice header of
	// `result.Output.SpecScene.Scenes` is shared with the caller's
	// `engineResult.Output.SpecScene.Scenes` and `procInput.SpecScene
	// .Scenes`, so the mutations propagate to:
	//   1. DocumentProcessor when it builds the Google Doc HTML
	//      body (consumes the post-walk SpecScene).
	//   2. buildGenerationResult's `result.Output.SpecScene.Scenes`
	//      (consumed by the JSON response writer downstream).
	// Both paths now read the SAME final binding set; the pre-PR-7
	// duplicate loop that did "fill empty only" against a different
	// source-of-truth (engineResult.ClipEvidence) is REMOVED.
	//
	// PR 1 (June 2026): preserve the model-emitted SpecScene verbatim
	// for kind/text/id — the postprocessor walk never mutates those
	// fields. The binder only touches `scene.Bindings.Clip`.
	if engineResult.ClipEvidence != nil {
		// Issue #2 (June 2026): ClipEvidence.ClipIDs renamed to
		// AcceptedClipIDs (transcript-usable set). The SourceTrace
		// field already called this AcceptedClipIDs (per legacy
		// contract) so the assignment is semantically a 1:1
		// pass-through — the SourceTrace field description is
		// unchanged and now matches the ClipEvidence source by
		// name.
		clipIDs := engineResult.ClipEvidence.AcceptedClipIDs
		if plan.NumClips > 0 && plan.NumClips < len(clipIDs) {
			clipIDs = clipIDs[:plan.NumClips]
		}
		sourceTrace.AcceptedClipIDs = append([]string(nil), clipIDs...)
	}

	// Merge postprocessor results into canonical Artifacts.
	if postResult != nil {
		// Entities (PR 3, June 2026): copy the typed
		// *scriptpkg.EntityResult from postResult to
		// result.Artifacts.Entities (canonical V1). The
		// read-only EntitiesJSON artefact is derived by
		// JSON-marshalling the typed result at the boundary
		// (NEW producers MUST populate Entities directly;
		// consumers MUST read fields rather than parsing the
		// raw JSON). Persists only for downstream consumers
		// that have not yet migrated to the typed shape.
		result.Artifacts.Entities = postResult.Entities
		if postResult.Entities != nil {
			if raw, err := scriptdto.SerializeEntityResultRoundTrip(postResult.Entities); err == nil {
				result.Artifacts.EntitiesJSON = raw
			}
		}

		// Metadata.
		if len(postResult.VideoMetadata) > 0 {
			meta := make([]scriptpkg.VideoMetadata, len(postResult.VideoMetadata))
			for i, m := range postResult.VideoMetadata {
				meta[i] = scriptpkg.VideoMetadata{
					Language:    m.Language,
					Title:       m.Title,
					Description: m.Description,
					Tags:        m.Tags,
				}
			}
			result.Artifacts.Metadata = meta
		}

		// Scene images — enrich SpecScene bindings.
		if len(postResult.Scenes) > 0 {
			for _, s := range postResult.Scenes {
				if s.Index < len(result.Output.SpecScene.Scenes) {
					sc := &result.Output.SpecScene.Scenes[s.Index]
					if sc.Bindings.Image == nil {
						sc.Bindings.Image = &scriptpkg.ImageBinding{}
					}
					sc.Bindings.Image.URL = s.URL
					sc.Bindings.Image.Status = "generated"
				}
			}
		}

		// Voiceovers — enrich SpecScene bindings.
		if len(postResult.Voiceovers) > 0 {
			for _, v := range postResult.Voiceovers {
				if v.SceneIndex < len(result.Output.SpecScene.Scenes) {
					sc := &result.Output.SpecScene.Scenes[v.SceneIndex]
					if sc.Bindings.Voiceover == nil {
						sc.Bindings.Voiceover = &scriptpkg.VoiceoverBinding{}
					}
					sc.Bindings.Voiceover.Status = v.Status
					sc.Bindings.Voiceover.Link = v.Link
					sc.Bindings.Voiceover.LocalPath = v.LocalPath
				}
			}
		}

		// Document.
		if postResult.DocLink != "" {
			result.Artifacts.Document = &scriptpkg.DocumentArtifact{
				DocLink: postResult.DocLink,
				DocID:   postResult.DocID,
				Status:  "completed",
			}
		}
	}

	// PR 2 (June 2026): propagate per-postprocessor warnings (best-effort
	// failures + missing-registered-at-runtime observations) into the
	// canonical GenerationResult.Warnings. GenerationResult.Warnings is
	// already serialised downstream by generation_job.go + response.go.
	if postResult != nil && len(postResult.Warnings) > 0 {
		result.Warnings = append(result.Warnings, postResult.Warnings...)
	}

	result.Source = sourceTrace

	return result
}

// Deprecated: error types and helpers from the legacy GenerationSpec
// bridge were removed in PR 3; processors now consume the typed
// EntityExtractor / MetadataGenerator ports directly.

// ── SCRIPT-T03-USECASE (P0, 2026-07-15) godlike/07 typed-error gate ──

// logPhaseError is the canonical usecase-boundary error-logging helper
// for the single-item orchestrator. Per godlike/07 typed-error contract
// + NO_FAKE_AVAILABILITY, every `return nil, err` at the orchestrator
// boundary MUST log the diagnostic context (item_id, phase, error)
// BEFORE returning the typed error. The typed error remains the
// propagation surface (handler reads it via errors.Is for HTTP status
// mapping) but operators now have a log trail for every failure.
//
// The structured log fields let operators correlate the typed error
// (which surfaces to the client as a 4xx/5xx) with the diagnostic log
// entry (which carries the full error chain + item_id + phase). This
// is the canonical "log+typed-propagate" pattern per godlike/07.
//
// PR-ERROR-SURFACING (2026-07-04): the return value is a TERTIARY
// wrapped error (umbrella + phase-sentinel + inner) so callers can
// errors.Is walk to ANY of the three layers:
//
//	errors.Is(err, scriptpkg.ErrScriptGenerationFailed)        → umbrella (script.generate capability)
//	errors.Is(err, scriptpkg.ErrPlanInvalid|ErrSourceResolutionFailed|ErrPostprocessFailed)  → phase
//	errors.Is(err, errAlreadyWrapped)                          → fine-grained (e.g. ErrEntityExtractorUnavailable)
//
// The umbrella wrap is canonical SSOT for "any script.generate failure";
// the phase + fine-grained sentinels preserve granular classification.
//
// PR-ERROR-SURFACING commit-5 (2026-07-04): umbrella coverage = 7/7 paths.
// Every error-return path inside `Execute` routes through one of three
// canonical wrap helpers so callers can `errors.Is(err,
// scriptpkg.ErrScriptGenerationFailed)` uniformly:
//   - 6 phase paths → logPhaseError (Phase 2 validate, Phase 3
//     source_resolve, Phase 4 voiceover_resolve, Phase 4
//     registry_validate, Phase 5 engine, Phase 6 postprocess).
//   - 2 pre-construction paths (counted as 1 "pre-construction"
//     category in 7/7: uc=nil + engine=nil) → preConstructError /
//     generateOnePreConstructError.
//
// All three helpers emit `fmt.Errorf("%w: ...: %w: %w",
// ErrScriptGenerationFailed, <label>, phaseSentinel, innerErr)` so the
// chain reaches the umbrella + the phase sentinel + the inner error
// (Go 1.20+ N-ary `%w` semantics — errors.Is walks boolean OR). godlike/07
// typed-error contract: NO path emits a plain unwrapped error.
// Inner errors may carry their own typed structs (GenerationError,
// PostprocessError) so `errors.As` continues to work for fine-grained
// classification alongside the umbrella.
//
// Returns the wrapped error so callers can write
//
//	return nil, uc.logPhaseError(item, "validate", scriptpkg.ErrPlanInvalid, err)
//
// for compactness.
func (uc *GenerateOneUseCase) logPhaseError(
	item scriptpkg.GenerationItemV2,
	phase string,
	sentinel error,
	err error,
) error {
	if uc.log != nil {
		uc.log.Warn("generate-one: phase failed",
			zap.String("item_id", item.ID),
			zap.String("phase", phase),
			zap.Error(err))
	}
	// Tertiary wrap: umbrella (ErrScriptGenerationFailed) + phase
	// sentinel + inner error. Go 1.20+ supports N-ary `%w` verbs via
	// errors.Join semantics, so all three errors remain
	// errors.Is-walkable. The string format produced is:
	//   "generation: script generation failed: <phase>: <inner>"
	// which is grep-friendly for operators triaging /api/jobs/{id}/full.
	return fmt.Errorf("%w: %w: %w", scriptpkg.ErrScriptGenerationFailed, sentinel, err)
}

// ── PR-ERROR-SURFACING commit-5 pre-construction helpers ──

// preConstructError wraps a non-nil uc's pre-construction failure
// through the canonical umbrella + phase-sentinel chain. Mirrors the
// logPhaseError helper above but WITHOUT the per-item log fields
// (these paths run BEFORE any item is parsed in, so there is no item
// correlation). The receiver remains non-nil — the engine=nil check
// runs AFTER the uc=nil test in Execute, so by the time this helper
// is invoked for engine=nil, uc is guaranteed non-nil.
//
// godlike/07 minimum-blast-radius: keeps the original
// `uc.log.Warn("generate-one: construction failed", reason="...")`
// diagnostic line verbatim — operators reading the old log-formatting
// string see no surprise. Only the error-construction shift changed.
func (uc *GenerateOneUseCase) preConstructError(
	reason string,
	sentinel error,
	err error,
) error {
	if uc.log != nil {
		uc.log.Warn("generate-one: construction failed",
			zap.String("reason", reason))
	}
	// Umbrella + reason-as-label + phase-sentinel + inner error.
	// N-ary %w verbs (Go 1.20+) keep every %w'd error in the
	// walkable chain; the %s reason string is JUST a label
	// (informational string in the error output, NOT in the
	// errors.Is chain). The chain reaches:
	//   errors.Is(err, ErrScriptGenerationFailed)   ✓
	//   errors.Is(err, ErrGenerationFailed)         ✓ (phase sentinel)
	//   errors.Is(err, err)                          ✓ (or errors.As)
	return fmt.Errorf("%w: %s: %w: %w", scriptpkg.ErrScriptGenerationFailed, reason, sentinel, err)
}

// generateOnePreConstructError is the package-level wrapper used when
// the receiver uc is NIL itself (the first pre-construction check in
// Execute). Cannot log because no logger accessor exists without a
// receiver; the typed-error chain is the canonical propagation
// surface for handlers / dashboards.
//
// Mirror of (uc) preConstructError but for the receiver-is-nil case.
// Same N-ary %w chain produces the same errors.Is walkability for
// ErrScriptGenerationFailed + phase-sentinel + inner err.
func generateOnePreConstructError(
	// Reserved for future "default logger" wiring — currently nil.
	_ *GenerateOneUseCase,
	reason string,
	sentinel error,
	err error,
) error {
	return fmt.Errorf("%w: %s: %w: %w", scriptpkg.ErrScriptGenerationFailed, reason, sentinel, err)
}
