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
	"strings"
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
		resolved, resolveErr = uc.registry.Resolve(ctx, item.Source, item.ID)
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
		// input), not Prompt (model input).
		if resolved.Fingerprint != "" {
			plan.SourceFingerprint = resolved.Fingerprint
		}
		if resolved.Type != "" {
			plan.SourceKind = string(resolved.Type)
		}
	}
	plan.CacheKey = scriptpkg.BuildCacheKey(&plan)
	timings.PlanBuildMs = time.Since(planStart).Milliseconds()

	// ── Phase 5: Generate script ────────────────────────────────────
	tracker.PhaseGenerateStart()
	engineStart := time.Now()

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
	var postArtifact *PostProcessArtifact
	if uc.ppReg != nil {
		for _, pp := range plan.Postprocessors {
			tracker.PhasePostprocess(pp)
		}
		ppStart := time.Now()
		var ppErr error
		// PR 3 (June 2026): pass the canonical typed
		// *scriptpkg.ModelScriptOutputV1 directly to the
		// registry. Processors walk model.SpecScene.Scenes by
		// reference and write back into scene.Bindings.{Image,
		// Voiceover}. PostProcessArtifact replaces the
		// pre-PR-3 PostProcessResult / PipelineResult aggregate.
		postArtifact, ppErr = uc.ppReg.Run(ctx, &plan, &engineResult.Output)
		if ppErr != nil {
			return nil, &scriptpkg.PostprocessError{
				ItemID:    item.ID,
				Processor: "registry",
				Inner:     ppErr,
			}
		}
		ppMs := time.Since(ppStart).Milliseconds()
		for _, pp := range plan.Postprocessors {
			timings.PostprocessMs[pp] = ppMs / int64(len(plan.Postprocessors))
		}
	}

	// ── Phase 7: Build result ───────────────────────────────────────
	result := buildGenerationResult(item, plan, engineResult, postArtifact, timings)
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

// buildGenerationResult constructs a GenerationResult from the
// engine and postprocessor outputs.
//
// PR 3 (June 2026):
//   - postArtifact *PostProcessArtifact replaces the pre-PR-3
//     aggregate shape. The Document / Metadata / Entities / ScriptID
//     flow directly into result.Artifacts.{Document, Metadata,
//     Entities, ScriptID} (no flat field re-mapping).
//   - The pre-PR-3 overwriting clip-binding loop is gone —
//     processors (images, voiceover) write scene.Bindings.{Image,
//     Voiceover} directly into the canonical typed model during
//     ppReg.Run. The same struct is referenced through
//     engineResult.Output.SpecScene.Scenes, so result.Output already
//     carries the populated bindings.
//   - The pre-PR-3 split-ProC-Result / VideoMetadata merge loops
//     are gone — replaced by the canonical aggregate read.
func buildGenerationResult(
	item scriptpkg.GenerationItemV2,
	plan scriptpkg.ResolvedGenerationPlan,
	engineResult *EngineResult,
	postArtifact *PostProcessArtifact,
	timings scriptpkg.GenerationTimings,
) *scriptpkg.GenerationResult {
	cacheHit := engineResult.CacheStatus == "exact_hit"

	// PR 3: ScriptID is sourced from postArtifact.ScriptID (set by
	// PersistenceProcessor when its plan runs). When persistence is
	// disabled, ScriptID is zero.
	scriptIDFromArtifact := int64(0)
	if postArtifact != nil {
		scriptIDFromArtifact = postArtifact.ScriptID
	}

	result := &scriptpkg.GenerationResult{
		ItemID:   item.ID,
		ScriptID: scriptIDFromArtifact,
		Title:    plan.Title,
		Language: plan.Language,
		Model:    engineResult.Model,
		Output: scriptpkg.ScriptOutput{
			Text:      engineResult.Output.Text,
			WordCount: engineResult.WordCount,
			// SpecScene shares the same backing array as
			// engineResult.Output.SpecScene. The processors
			// mutated scene.Bindings directly during ppReg.Run,
			// so the populated bindings flow through here
			// without a re-mapping walk.
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
	if engineResult.ClipEvidence != nil {
		sourceTrace.AcceptedClipIDs = engineResult.ClipEvidence.ClipIDs
	}
	// PR 3: the pre-PR-3 overwriting loop that enriched
	}
	// PR 3: the pre-PR-3 overwriting loop that enriched
	// scene.Bindings.Clip.ClipID/DriveLink from
	// engineResult.ClipEvidence is gone. Clip bindings are now
	// model-emitted authors of the canonical V1 contract; if the
	// model emits a Clip.binding, that binding is already on the
	// scene at this point. DriveLink enrichment (where the model
	// emitted ClipID but not DriveLink) is post-PR-7 work.

	// PR 3: copy typed post-processor artifacts into the
	// canonical GenerationResult.Artifacts.
	if postArtifact != nil {
		if postArtifact.Document != nil {
			result.Artifacts.Document = postArtifact.Document
		}
		if len(postArtifact.Metadata) > 0 {
			// Convert applyapplication.VideoMetadata to
			// domain/script.VideoMetadata (typed destination —
			// same shape).
			meta := make([]scriptpkg.VideoMetadata, len(postArtifact.Metadata))
			for i, m := range postArtifact.Metadata {
				meta[i] = scriptpkg.VideoMetadata{
					Language:    m.Language,
					Title:       m.Title,
					Description: m.Description,
					Tags:        m.Tags,
				}
			}
			result.Artifacts.Metadata = meta
		}
		if postArtifact.Entities != nil {
			result.Artifacts.Entities = postArtifact.Entities
		}
	}

	result.Source = sourceTrace

	return result
}
