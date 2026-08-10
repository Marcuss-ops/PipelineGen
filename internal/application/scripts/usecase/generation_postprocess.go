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
	"maps"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ProcessedGeneration holds everything produced by the postprocess
// phase that the finalize phase needs.
type ProcessedGeneration struct {
	PostResult    *adapters.PipelineResult
	Provenance    *scriptpkg.GenerationProvenance
	PostprocessMs map[string]int64
}

// GenerationPostprocessor runs the postprocessor registry for a
// single prepared plan. It is constructed once per use case and
// reused across calls.
type GenerationPostprocessor struct {
	ppReg *adapters.PostProcessorRegistry
}

// NewGenerationPostprocessor constructs a GenerationPostprocessor.
// ppReg may be nil (postprocessors are skipped).
func NewGenerationPostprocessor(ppReg *adapters.PostProcessorRegistry) *GenerationPostprocessor {
	return &GenerationPostprocessor{ppReg: ppReg}
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
			Provenance:    provenance,
			PostprocessMs: make(map[string]int64),
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

	postprocessMs := make(map[string]int64)
	if postResult != nil && len(postResult.StageDurations) > 0 {
		postprocessMs = maps.Clone(postResult.StageDurations)
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

	return &ProcessedGeneration{
		PostResult:    postResult,
		Provenance:    provenance,
		PostprocessMs: postprocessMs,
	}, nil
}

// VidRushTimingFields projects the processor timings onto the stable flat
// fields used by operational reports. The detailed map remains the source of
// truth; this projection only names the stages that already exist in the
// canonical postprocessor registry.
func VidRushTimingFields(stageDurations map[string]int64) scriptpkg.GenerationTimings {
	var timings scriptpkg.GenerationTimings
	if stageDurations == nil {
		return timings
	}
	timings.SegmentExtractionMs = stageDurations[string(adapters.ProcessorEntities)]
	timings.QueryGenerationMs = timings.SegmentExtractionMs
	timings.ArtlistSearchMs = stageDurations[string(adapters.ProcessorClipSearch)]
	timings.InternetImageSearchMs = stageDurations[string(adapters.ProcessorInternetImages)]
	timings.ImageGenerationMs = stageDurations[string(adapters.ProcessorImages)]
	timings.SQLiteMs = stageDurations[string(adapters.ProcessorPersistence)]
	timings.BindingMs = stageDurations[string(adapters.ProcessorClipBindings)]
	return timings
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
