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
//   - NormalizationConfig: config-driven defaults
//   - SourceRegistry: resolves source → ResolvedSource
//   - Engine: calls ollama for script text
//   - PostProcessorRegistry: runs postprocessors (entities, metadata,
//     voiceover, images, document, persistence)
package scripts

import (
	"context"
	"fmt"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// GenerateOneUseCase orchestrates the unified pipeline for a single
// generation item. All dependencies are typed — no interface{} on
// the public surface.
type GenerateOneUseCase struct {
	cfg      NormalizationConfig
	registry *SourceRegistry
	engine   *Engine
	ppReg    *PostProcessorRegistry
	log      *zap.Logger
}

// NewGenerateOneUseCase constructs the use case. engine and registry
// must be non-nil; ppReg may be nil (postprocessors are skipped).
func NewGenerateOneUseCase(
	cfg NormalizationConfig,
	registry *SourceRegistry,
	engine *Engine,
	ppReg *PostProcessorRegistry,
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

// Execute runs the full pipeline for one item and returns a typed
// GenerationResult. Progress is reported through the tracker when
// non-nil.
func (uc *GenerateOneUseCase) Execute(
	ctx context.Context,
	item scriptpkg.GenerationItemV2,
	preset scriptpkg.Preset,
	tracker *ProgressTracker,
) (*scriptpkg.GenerationResult, error) {
	if uc == nil {
		return nil, fmt.Errorf("%w: use case not constructed", scriptpkg.ErrGenerationFailed)
	}
	if uc.engine == nil {
		return nil, fmt.Errorf("%w: engine not configured", scriptpkg.ErrGenerationFailed)
	}

	startAll := time.Now()
	timings := scriptpkg.GenerationTimings{}

	// ── Phase 1: Normalize ──────────────────────────────────────────
	tracker.PhaseNormalize()
	NormalizeItem(&item, preset, uc.cfg)

	// ── Phase 2: Validate ───────────────────────────────────────────
	tracker.PhaseValidate()
	if err := ValidateItem(item); err != nil {
		return nil, fmt.Errorf("%w: %w", scriptpkg.ErrPlanInvalid, err)
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
			return nil, fmt.Errorf("%w: %w", scriptpkg.ErrSourceResolutionFailed, resolveErr)
		}
	}
	timings.SourceResolveMs = time.Since(sourceStart).Milliseconds()

	// ── Phase 4: Build plan ─────────────────────────────────────────
	tracker.PhaseBuildPlan()
	planStart := time.Now()
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
			return nil, fmt.Errorf("%w: %w", scriptpkg.ErrPlanInvalid, err)
		}
	}

	// ── Phase 5: Generate script ────────────────────────────────────
	tracker.PhaseGenerateStart()
	engineStart := time.Now()

	// Pass the resolved plan directly to the engine.
	// The engine owns: memory gate check, ollama invocation,
	// optional DB persistence. No WriteScriptRequest needed.
	engineResult, engineErr := uc.engine.Generate(ctx, &plan)
	if engineErr != nil {
		return nil, &scriptpkg.GenerationError{
			ItemID: item.ID,
			Phase:  "engine",
			Inner:  fmt.Errorf("ollama generation failed: %w", engineErr),
		}
	}
	timings.EngineMs = time.Since(engineStart).Milliseconds()
	tracker.PhaseGenerateDone()

	// ── Phase 6: Postprocess ────────────────────────────────────────
	timings.PostprocessMs = make(map[string]int64)
	var postResult *PipelineResult
	if uc.ppReg != nil {
		for _, pp := range plan.Postprocessors {
			tracker.PhasePostprocess(pp)
		}
		ppStart := time.Now()
		var ppErr error
		// PR 5: build the typed ProcessInput envelope from the
		// engine result. PersistenceProcessor consumes the typed
		// fields (WordCount, SpecScene, ModelUsed, CacheStatus,
		// SourceTrace); non-persistence processors consume
		// input.Text. Plan.SaveToDB -> "persistence" in
		// Postprocessors list (set by generation_plan_builder.go
		// via buildPostprocessorList) is now the ONLY trigger for
		// script-table writes.
		procInput := ProcessInput{
			Text:        engineResult.Output.Text,
			WordCount:   engineResult.WordCount,
			SpecScene:   engineResult.Output.SpecScene,
			ModelUsed:   engineResult.Model,
			CacheStatus: engineResult.CacheStatus,
			SourceTrace: engineResult.ClipEvidence,
		}
		postResult, ppErr = uc.ppReg.Run(ctx, &plan, procInput)
		if ppErr != nil {
			return nil, &scriptpkg.PostprocessError{
				ItemID:    item.ID,
				Processor: "registry",
				Inner:     ppErr,
			}
		}
		// Approximate per-postprocessor timing.
		ppMs := time.Since(ppStart).Milliseconds()
		for _, pp := range plan.Postprocessors {
			timings.PostprocessMs[pp] = ppMs / int64(len(plan.Postprocessors))
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
//                 resolver previously hijacked Guidelines here —
//                 the bug class)
//   - Tone      — item.Tone
//   - Model     — item.Model
//   - Style     — item.Style
//   - TargetWords — item.ScriptParams.TargetWords
func buildResolutionContext(item scriptpkg.GenerationItemV2) scriptpkg.SourceResolutionContext {
	return scriptpkg.SourceResolutionContext{
		ItemID:      item.ID,
		Title:       item.Title,
		Language:    item.Language,
		Tone:        item.Tone,
		Model:       item.Model,
		Style:       item.Style,
		TargetWords: item.ScriptParams.TargetWords,
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
	postResult *PipelineResult,
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

	result := &scriptpkg.GenerationResult{
		ItemID:   item.ID,
		ScriptID: scriptIDFromPostprocess,
		Title:    plan.Title,
		Language: plan.Language,
		Model:    engineResult.Model,
		Output: scriptpkg.ScriptOutput{
			Text:      engineResult.Output.Text,
			WordCount: engineResult.WordCount,
			SpecScene: engineResult.Output.SpecScene,
		},
		Cache: scriptpkg.CacheResult{
			Status: engineResult.CacheStatus,
			Hit:    cacheHit,
		},
		Timings: timings,
	}

	// Populate Source trace.
	var sourceTrace scriptpkg.SourceTrace

	// PR 1: preserve the model-emitted SpecScene verbatim. Clip
	// evidence may only ENRICH missing Clip bindings — never
	// replaces existing model-authored text, IDs, or kind tags.
	// If the model emitted no scenes (pure prose generation), we
	// still carry the empty struct so consumers see a consistent
	// SpecSceneOutput shape across all generation flows.
	if engineResult.ClipEvidence != nil {
		sourceTrace.AcceptedClipIDs = engineResult.ClipEvidence.ClipIDs

		scenes := result.Output.SpecScene.Scenes
		for i, id := range engineResult.ClipEvidence.ClipIDs {
			if i >= len(scenes) {
				break
			}
			sc := &scenes[i]
			if sc.Bindings.Clip == nil {
				sc.Bindings.Clip = &scriptpkg.ClipBinding{}
			}
			// Only fill empty fields — never overwrite.
			if sc.Bindings.Clip.ClipID == "" {
				sc.Bindings.Clip.ClipID = id
			}
			if sc.Bindings.Clip.DriveLink == "" && engineResult.ClipEvidence.DriveLinks != nil {
				sc.Bindings.Clip.DriveLink = engineResult.ClipEvidence.DriveLinks[id]
			}
		}
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
			if raw, err := SerializeEntityResultRoundTrip(postResult.Entities); err == nil {
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
