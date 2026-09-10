// Package usecase — generation_postprocess.go owns the canonical
// postprocess phase for single-item script generation.
//
// Responsibilities:
//   - build the provisional provenance block
//   - emit per-processor progress tracker events
//   - run the PostProcessorRegistry
//   - return the merged PipelineResult, provenance, and timings
//
// The postprocess phase is intentionally stateless except for its
// dependency on the canonical PostProcessorRegistry. It returns a
// ProcessedGeneration value object that feeds the finalize phase.
package usecase

import (
	"context"
	"fmt"
	"strings"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// ProcessedGeneration holds everything produced by the postprocess
// phase that the finalize phase needs.
type ProcessedGeneration struct {
	PostResult    *adapters.PipelineResult
	Provenance    *scriptpkg.GenerationProvenance
	PostprocessMs map[string]int64 // compatibility projection from RunReport
}

// GenerationPostprocessor runs the postprocessor registry for a
// single prepared plan. It is constructed once per use case and
// reused across calls.
type GenerationPostprocessor struct {
	ppReg    *adapters.PostProcessorRegistry
	enricher scriptgen.SegmentEnricher
}

// NewGenerationPostprocessor constructs a GenerationPostprocessor.
// ppReg may be nil (postprocessors are skipped).
func NewGenerationPostprocessor(ppReg *adapters.PostProcessorRegistry) *GenerationPostprocessor {
	return &GenerationPostprocessor{ppReg: ppReg}
}

// SetSegmentEnricher wires the canonical SceneIR/VisualNER boundary used by
// the batch postprocessor path. The durable runner has the same boundary in
// its incremental coordinator; keeping this setter optional preserves the
// lightweight unit-test composition while ensuring production batch jobs do
// not enter media planning with an empty VidRush scene surface.
func (p *GenerationPostprocessor) SetSegmentEnricher(enricher scriptgen.SegmentEnricher) {
	if p != nil {
		p.enricher = enricher
	}
}

// Process runs the postprocessor pipeline and returns a
// ProcessedGeneration. When the registry is nil, it returns an
// empty processed generation with only the provenance block built.
func (p *GenerationPostprocessor) Process(
	ctx context.Context,
	item scriptpkg.GenerationItemV2,
	plan scriptpkg.ResolvedGenerationPlan,
	engineResult *EngineResult,
	tracker *ProgressTracker,
) (*ProcessedGeneration, error) {
	if engineResult == nil {
		return nil, &scriptpkg.PostprocessError{
			ItemID:    item.ID,
			Processor: "engine",
			Inner:     fmt.Errorf("engine result is nil"),
		}
	}

	modeInfo := provisionalModeInfo(plan, engineResult)
	provenance := buildProvenance(plan, engineResult, modeInfo)

	if p == nil {
		return nil, &scriptpkg.PostprocessError{
			ItemID:    item.ID,
			Processor: "postprocessor",
			Inner:     fmt.Errorf("postprocessor not configured"),
		}
	}

	if p.ppReg == nil {
		return &ProcessedGeneration{
			Provenance: provenance, PostprocessMs: map[string]int64{},
		}, nil
	}

	procInput := adapters.ProcessInput{
		Text:              engineResult.Output.Text,
		WordCount:         engineResult.WordCount,
		SpecScene:         engineResult.Output.SpecScene,
		OriginalSpecScene: engineResult.Output.SpecScene,
		ModelUsed:         engineResult.Model,
		CacheStatus:       engineResult.CacheStatus,
		SourceTrace:       engineResult.ClipEvidence,
		Provenance:        provenance,
		EffectiveLanguage: strings.TrimSpace(plan.Language),
		StockEnabled:      plan.StockEnabled,
		StockBindings:     append([]scriptpkg.StockBindingInput(nil), plan.StockBindings...),
		ResearchSources:   append([]scriptpkg.SourceReference(nil), plan.ResearchSources...),
	}
	if mediaPostprocessingRequested(plan) {
		if err := seedVidRushPostprocessInput(ctx, &plan, &procInput, p.enricher); err != nil {
			return nil, &scriptpkg.PostprocessError{
				ItemID:    item.ID,
				Processor: "vidrush_nlp",
				Inner:     err,
			}
		}
	}

	postResult, err := p.ppReg.RunWithProgress(ctx, &plan, procInput, func(event adapters.ProcessorProgressEvent) {
		tracker.PhasePostprocessEvent(
			event.Index,
			event.Total,
			string(event.Name),
			event.Status,
			event.Duration,
			event.Err,
		)
	})
	if err != nil {
		return nil, &scriptpkg.PostprocessError{
			ItemID:    item.ID,
			Processor: "registry",
			Inner:     err,
		}
	}
	if item.ScriptParams.SingleScene && postResult != nil {
		postResult.FinalSpecScene = collapseSpecSceneOutput(engineResult.Output.Text, postResult.FinalSpecScene)
	}

	if postResult != nil && len(postResult.StageProgress) > 0 {
		tracker.SetStageProgress(postResult.StageProgress)
	}

	if plan.ClipEvidence != nil && len(plan.ClipEvidence.AcceptedClipIDs) > 0 {
		tracker.TrackEvent("clips.bound", "Clip bindings applied", map[string]any{
			"item_id":    item.ID,
			"clip_count": len(plan.ClipEvidence.AcceptedClipIDs),
		})
	}

	postprocessMs := map[string]int64{}
	if run := kernobs.FromContext(ctx); run != nil {
		for _, stage := range run.Report().Stages {
			postprocessMs[stage.Name] = stage.DurationMs
		}
	}
	return &ProcessedGeneration{
		PostResult:    postResult,
		Provenance:    provenance,
		PostprocessMs: postprocessMs,
	}, nil
}

// mediaPostprocessingRequested identifies the batch path that consumes the
// canonical per-scene semantic surface. Text-only runs without extraction or
// media planning stay unchanged.
func mediaPostprocessingRequested(plan scriptpkg.ResolvedGenerationPlan) bool {
	for _, raw := range plan.Postprocessors {
		switch adapters.ProcessorName(raw) {
		case adapters.ProcessorClipSearch,
			adapters.ProcessorInternetImages,
			adapters.ProcessorVidRushMaterialization,
			adapters.ProcessorVisualPlanning:
			return true
		}
	}
	return false
}

// seedVidRushPostprocessInput creates the internal segment surface from the
// generated SpecScene. This is deliberately done before ClipSearch,
// InternetImages, VisualPlanning and Materialization: those processors are
// consumers of the surface and must not be asked to infer topology from a
// zero-length input. The semantic enricher runs in bounded parallelism so
// multiple scenes/languages can progress concurrently.
func seedVidRushPostprocessInput(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	input *adapters.ProcessInput,
	enricher scriptgen.SegmentEnricher,
) error {
	if input == nil {
		return fmt.Errorf("vidrush nlp: process input is nil")
	}
	scenes := append([]scriptpkg.SpecScene(nil), input.SpecScene.Scenes...)
	if len(scenes) == 0 && strings.TrimSpace(input.Text) != "" {
		scenes = []scriptpkg.SpecScene{{
			ID: "scene-0", Index: 0, Kind: scriptpkg.SceneNarration,
			Text: strings.TrimSpace(input.Text),
		}}
		input.SpecScene = scriptpkg.SpecSceneOutput{Version: 1, Scenes: scenes}
		input.OriginalSpecScene = input.SpecScene
	}
	if len(scenes) == 0 {
		return fmt.Errorf("vidrush nlp: generated text has no scene surface")
	}

	seeds := make([]scriptpkg.SpecScene, 0, len(scenes))
	for i, scene := range scenes {
		if scene.ExecutionMode.IsFixedMedia() {
			continue
		}
		if strings.TrimSpace(scene.Text) == "" && plan != nil && i < len(plan.Segments) {
			scene.Text = strings.TrimSpace(plan.Segments[i].SourceText)
		}
		if strings.TrimSpace(scene.Text) == "" {
			continue
		}
		if strings.TrimSpace(scene.ID) == "" {
			scene.ID = fmt.Sprintf("scene-%d", i)
		}
		if scene.Index < 0 {
			scene.Index = i
		}
		seeds = append(seeds, scene)
	}
	if len(seeds) == 0 {
		return fmt.Errorf("vidrush nlp: generated scene surface contains no narrative text")
	}

	if enricher == nil {
		input.VidRushSegments = make([]scriptpkg.VidRushSegmentResult, 0, len(seeds))
		for _, scene := range seeds {
			segmentID := strings.TrimSpace(scene.SegmentID)
			if segmentID == "" {
				segmentID = scene.ID
			}
			input.VidRushSegments = append(input.VidRushSegments, scriptpkg.VidRushSegmentResult{
				SegmentID: segmentID,
				SceneID:   scene.ID,
				Position:  scene.Index,
				Text:      scene.Text,
				TextHash:  scriptpkg.ComputeCanonicalSegmentTextHash(scene.Text),
			})
		}
		return nil
	}

	workers := 2
	if plan != nil && plan.Concurrency > 0 {
		workers = plan.Concurrency
	}
	enriched, err := concurrent.Map(ctx, seeds, workers, func(ctx context.Context, _ int, scene scriptpkg.SpecScene) (scriptpkg.VidRushSegmentResult, error) {
		return enricher.Enrich(ctx, plan, scene)
	})
	if err != nil {
		return fmt.Errorf("vidrush nlp: enrich scenes: %w", err)
	}
	input.VidRushSegments = enriched
	return nil
}

// VidRushTimingFields is a compatibility projection from canonical stage
// durations. It performs no measurement and owns no timing state.
func VidRushTimingFields(stageDurations map[string]int64) scriptpkg.GenerationTimings {
	var t scriptpkg.GenerationTimings
	// VisualNER extraction runs in the semantic runner before the
	// postprocessor walk; there is no legacy entities stage to time here.
	t.SegmentExtractionMs = 0
	t.QueryGenerationMs = t.SegmentExtractionMs
	t.ArtlistSearchMs = stageDurations[string(adapters.ProcessorClipSearch)]
	t.InternetImageSearchMs = stageDurations[string(adapters.ProcessorInternetImages)]
	t.ImageGenerationMs = stageDurations[string(adapters.ProcessorImages)]
	t.SQLiteMs = stageDurations[string(adapters.ProcessorPersistence)]
	t.BindingMs = stageDurations[string(adapters.ProcessorClipBindings)]
	return t
}
func collapseSpecSceneOutput(text string, current scriptpkg.SpecSceneOutput) scriptpkg.SpecSceneOutput {
	scene := scriptpkg.SpecScene{
		ID: "scene-0", Index: 0, Kind: scriptpkg.SceneNarration,
	}
	if len(current.Scenes) > 0 {
		// Preserve annotations and segment identity produced by the entity and
		// asset processors. The single-scene collapse changes only the public
		// text envelope; rebuilding the scene here would silently discard the
		// entity-image binding surface.
		scene = current.Scenes[0]
		scene.ID = "scene-0"
		scene.Index = 0
		scene.Kind = scriptpkg.SceneNarration
	}
	scene.Text = strings.TrimSpace(text)
	return scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes:  []scriptpkg.SpecScene{scene},
	}
}
