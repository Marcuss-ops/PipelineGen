// Package scripts — generate_one_usecase.go is the canonical
// single-item script-generation orchestrator. It executes the
// unified pipeline for exactly one GenerationItemV2:
//
//   normalize → validate → resolve source → build plan → generate → postprocess → typed result
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
		if resolved.Fingerprint != "" {
			plan.Prompt = resolved.Fingerprint
		}
	}
	timings.PlanBuildMs = time.Since(planStart).Milliseconds()

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
		postResult, ppErr = uc.ppReg.Run(ctx, &plan, engineResult.Script)
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
		result.SearchResults = resolved.SearchResults
	}

	tracker.PhaseComplete()

	if uc.log != nil {
		uc.log.Info("generate-one: completed",
			zap.String("item_id", item.ID),
			zap.String("title", plan.Title),
			zap.Int("word_count", result.WordCount),
			zap.String("cache_status", result.CacheStatus),
			zap.Int64("total_ms", timings.TotalMs))
	}

	return result, nil
}

// ── Helpers ──────────────────────────────────────────────────────────

// buildGenerationResult constructs a GenerationResult from the
// engine and postprocessor outputs. PR 9: populates BOTH the
// deprecated flat fields AND the canonical nested fields (Output,
// Source, Cache, Artifacts). New consumers read the nested fields;
// legacy consumers still read the flat fields.
func buildGenerationResult(
	item scriptpkg.GenerationItemV2,
	plan scriptpkg.ResolvedGenerationPlan,
	engineResult *EngineResult,
	postResult *PipelineResult,
	timings scriptpkg.GenerationTimings,
) *scriptpkg.GenerationResult {
	cacheHit := engineResult.CacheStatus == "exact_hit"

	result := &scriptpkg.GenerationResult{
		// Canonical.
		ItemID:   item.ID,
		ScriptID: engineResult.ScriptID,
		Title:    plan.Title,
		Language: plan.Language,
		Model:    engineResult.Model,
		Output: scriptpkg.ScriptOutput{
			Text:      engineResult.Script,
			WordCount: engineResult.WordCount,
		},
		Cache: scriptpkg.CacheResult{
			Status: engineResult.CacheStatus,
			Hit:    cacheHit,
		},
		Timings: timings,

		// Deprecated (migration window).
		ID:          item.ID,
		Script:      engineResult.Script,
		WordCount:   engineResult.WordCount,
		CacheStatus: engineResult.CacheStatus,
		CacheHit:    cacheHit,
	}

	// Populate Source trace.
	var sourceTrace scriptpkg.SourceTrace

	// Merge clip evidence into both deprecated ClipScenes and
	// canonical Output.SpecScene.
	//
	// NOTE: SpecScene.Text is empty at this point — the model output
	// is not yet structured (DecodeModelOutput from PR 2 is not yet
	// integrated into the engine). When the engine starts producing
	// ModelScriptOutputV1, the specscene texts will be filled from
	// the model output and merged with these evidence skeletons.
	if engineResult.ClipEvidence != nil {
		specScenes := make([]scriptpkg.SpecScene, 0, len(engineResult.ClipEvidence.ClipIDs))
		clipScenes := make([]scriptpkg.ClipSceneResult, 0, len(engineResult.ClipEvidence.ClipIDs))
		for i, id := range engineResult.ClipEvidence.ClipIDs {
			link := ""
			if engineResult.ClipEvidence.DriveLinks != nil {
				link = engineResult.ClipEvidence.DriveLinks[id]
			}
			sceneID := fmt.Sprintf("scene-%d", i)
			specScenes = append(specScenes, scriptpkg.SpecScene{
				ID:    sceneID,
				Index: i,
				Text:  "", // text filled by model output, not evidence
				Kind:  scriptpkg.SceneClip,
				Bindings: scriptpkg.SceneBindings{
					Clip: &scriptpkg.ClipBinding{
						ClipID:    id,
						DriveLink: link,
					},
				},
			})
			clipScenes = append(clipScenes, scriptpkg.ClipSceneResult{
				SceneIndex: i,
				ClipID:     id,
				DriveLink:  link,
				Kind:       "clip",
			})
		}
		result.Output.SpecScene = scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes:  specScenes,
		}
		result.ClipScenes = clipScenes
		result.AcceptedClipIDs = engineResult.ClipEvidence.ClipIDs
		sourceTrace.AcceptedClipIDs = engineResult.ClipEvidence.ClipIDs
	}

	// Merge postprocessor results into both deprecated and canonical.
	if postResult != nil {
		// Entities.
		result.EntitiesJSON = postResult.EntitiesJSON
		result.Artifacts.EntitiesJSON = postResult.EntitiesJSON

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
			result.Metadata = meta
			result.Artifacts.Metadata = meta
		}

		// Scene images — enrich SpecScene bindings.
		if len(postResult.Scenes) > 0 {
			scenes := make([]scriptpkg.SceneImageResult, len(postResult.Scenes))
			for i, s := range postResult.Scenes {
				scenes[i] = scriptpkg.SceneImageResult{
					SceneIndex: s.Index,
					Text:       s.Text,
					ImageURL:   s.URL,
				}
				// Enrich the matching SpecScene if it exists.
				if s.Index < len(result.Output.SpecScene.Scenes) {
					sc := &result.Output.SpecScene.Scenes[s.Index]
					if sc.Bindings.Image == nil {
						sc.Bindings.Image = &scriptpkg.ImageBinding{}
					}
					sc.Bindings.Image.URL = s.URL
					sc.Bindings.Image.Status = "generated"
				}
			}
			result.SceneImages = scenes
		}

		// Voiceovers — enrich SpecScene bindings.
		if len(postResult.Voiceovers) > 0 {
			vos := make([]scriptpkg.VoiceoverResult, len(postResult.Voiceovers))
			for i, v := range postResult.Voiceovers {
				vos[i] = scriptpkg.VoiceoverResult{
					SceneIndex: v.SceneIndex,
					Status:     v.Status,
					Link:       v.Link,
					LocalPath:  v.LocalPath,
				}
				// Enrich the matching SpecScene if it exists.
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
			result.Voiceovers = vos
		}

		// Document.
		if postResult.DocLink != "" {
			result.DocLink = postResult.DocLink
			result.DocID = postResult.DocID
			result.Artifacts.Document = &scriptpkg.DocumentArtifact{
				DocLink: postResult.DocLink,
				DocID:   postResult.DocID,
				Status:  "completed",
			}
		}
	}

	result.Source = sourceTrace

	return result
}

// legacySpecFromPlan moved to generation_helpers.go — shared by
// processors (entities, metadata) and the use case.

