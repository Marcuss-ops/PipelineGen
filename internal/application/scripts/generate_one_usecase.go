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
//   - Pipeline: runs postprocessors (entities, metadata, voiceover,
//     images, document, persistence)
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
	pipeline *Pipeline
	log      *zap.Logger
}

// NewGenerateOneUseCase constructs the use case. engine and registry
// must be non-nil; pipeline may be nil (postprocessors are skipped).
func NewGenerateOneUseCase(
	cfg NormalizationConfig,
	registry *SourceRegistry,
	engine *Engine,
	pipeline *Pipeline,
	log *zap.Logger,
) *GenerateOneUseCase {
	return &GenerateOneUseCase{
		cfg:      cfg,
		registry: registry,
		engine:   engine,
		pipeline: pipeline,
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

	writeReq := WriteScriptRequest{
		// Plan left nil — the engine type-asserts to *ScriptGenerationPlan
		// (old type), not *ResolvedGenerationPlan. All fields are set
		// explicitly below, so the nil Plan fallback is never needed.
		Topic:       plan.Topic,
		Title:       plan.Title,
		Language:    plan.Language,
		Tone:        plan.Tone,
		Model:       plan.Model,
		Mode:        plan.Mode,
		SourceText:  plan.SourceText,
		MinWords:    plan.TargetWords,
		MaxChars:    plan.MaxChars,
		Prompt:      plan.Prompt,
		UseMemory:   plan.UseMemory,
		SaveToDB:    plan.SaveToDB,
		SaveTimeout: 60,
		ClipPack:    clipEvidenceToPack(plan.ClipEvidence),
	}

	writeResult, engineErr := uc.engine.WriteScript(ctx, writeReq)
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
	if uc.pipeline != nil {
		for _, pp := range plan.Postprocessors {
			tracker.PhasePostprocess(pp)
		}
		ppStart := time.Now()
		// The existing Pipeline.Run expects the legacy GenerationSpec.
		// Build a minimal spec from the plan for back-compat.
		legacySpec := legacySpecFromPlan(plan)
		var ppErr error
		postResult, ppErr = uc.pipeline.Run(ctx, legacySpec, writeResult.Script, nil)
		if ppErr != nil {
			return nil, &scriptpkg.PostprocessError{
				ItemID:    item.ID,
				Processor: "pipeline",
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
	result := buildGenerationResult(item, plan, writeResult, postResult, timings)
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
// pipeline outputs.
func buildGenerationResult(
	item scriptpkg.GenerationItemV2,
	plan scriptpkg.ResolvedGenerationPlan,
	writeResult *WriteScriptResult,
	postResult *PipelineResult,
	timings scriptpkg.GenerationTimings,
) *scriptpkg.GenerationResult {
	result := &scriptpkg.GenerationResult{
		ID:          item.ID,
		Script:      writeResult.Script,
		WordCount:   writeResult.WordCount,
		Title:       plan.Title,
		Language:    plan.Language,
		Model:       writeResult.Model,
		CacheStatus: writeResult.CacheStatus,
		CacheHit:    writeResult.CacheHit,
		Timings:     timings,
	}

	// Merge postprocessor results.
	if postResult != nil {
		result.EntitiesJSON = postResult.EntitiesJSON

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
		}

		if len(postResult.Scenes) > 0 {
			scenes := make([]scriptpkg.SceneImageResult, len(postResult.Scenes))
			for i, s := range postResult.Scenes {
				scenes[i] = scriptpkg.SceneImageResult{
					SceneIndex: s.Index,
					Text:       s.Text,
					ImageURL:   s.URL,
				}
			}
			result.SceneImages = scenes
		}

		if len(postResult.Voiceovers) > 0 {
			vos := make([]scriptpkg.VoiceoverResult, len(postResult.Voiceovers))
			for i, v := range postResult.Voiceovers {
				vos[i] = scriptpkg.VoiceoverResult{
					SceneIndex: v.SceneIndex,
					Status:     v.Status,
					Link:       v.Link,
					LocalPath:  v.LocalPath,
				}
			}
			result.Voiceovers = vos
		}
		if postResult.DocLink != "" {
			result.DocLink = postResult.DocLink
			result.DocID = postResult.DocID
		}
	}

	// Merge clip evidence from plan into result.
	if plan.ClipEvidence != nil {
		clipScenes := make([]scriptpkg.ClipSceneResult, 0, len(plan.ClipEvidence.ClipIDs))
		for i, id := range plan.ClipEvidence.ClipIDs {
			link := ""
			if plan.ClipEvidence.DriveLinks != nil {
				link = plan.ClipEvidence.DriveLinks[id]
			}
			clipScenes = append(clipScenes, scriptpkg.ClipSceneResult{
				SceneIndex: i,
				ClipID:     id,
				DriveLink:  link,
				Kind:       "clip",
			})
		}
		result.ClipScenes = clipScenes
		result.AcceptedClipIDs = plan.ClipEvidence.ClipIDs
	}

	return result
}

// clipEvidenceToPack converts a ClipEvidence to a map[string]any
// for the engine's ClipPack field (transitional adapter — the engine
// still expects map[string]any from the old ClipSourceBuilder pack).
func clipEvidenceToPack(ev *scriptpkg.ClipEvidence) map[string]any {
	if ev == nil {
		return nil
	}
	return map[string]any{
		"clip_ids":   ev.ClipIDs,
		"clip_count": ev.ClipCount,
	}
}

// legacySpecFromPlan builds a legacy GenerationSpec from a
// ResolvedGenerationPlan for back-compat with the existing
// Pipeline.Run which still consumes *GenerationSpec.
func legacySpecFromPlan(plan scriptpkg.ResolvedGenerationPlan) *scriptpkg.GenerationSpec {
	spec := &scriptpkg.GenerationSpec{
		Title:         plan.Title,
		Language:      plan.Language,
		Tone:          plan.Tone,
		Model:         plan.Model,
		TargetWords:   plan.TargetWords,
		Duration:      plan.Duration,
		MinWords:      plan.MinWords,
		SentencesPerImage: plan.SentencesPerImage,
		ImagesPerScene:    plan.ImagesPerScene,
		Style:         plan.Style,
		Guidelines:    plan.Guidelines,
		MaxChars:      plan.MaxChars,
		OutputFmt:     plan.OutputFmt,
		SaveToDB:      plan.SaveToDB,		ExtractEntities:   plan.HasPostprocessor("entities"),
		GenerateMetadata:  plan.HasPostprocessor("metadata"),
		GenerateVoiceover: plan.HasPostprocessor("voiceover"),
		PromptVersion:       plan.PromptVersion,
		EditorPromptVersion: plan.EditorPromptVersion,
		QAPromptVersion:     plan.QAPromptVersion,
		ForceRefresh:        plan.ForceRefresh,
		Languages:            plan.Languages,
	}
	if plan.ClipEvidence != nil {
		spec.ClipIDs = plan.ClipEvidence.ClipIDs
		spec.NumClips = plan.ClipEvidence.ClipCount
	}
	return spec
}
