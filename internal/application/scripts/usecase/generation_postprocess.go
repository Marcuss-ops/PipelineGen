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

	for _, pp := range plan.Postprocessors {
		tracker.PhasePostprocess(pp)
	}

	procInput := adapters.ProcessInput{
		Text:              engineResult.Output.Text,
		WordCount:         engineResult.WordCount,
		SpecScene:         engineResult.Output.SpecScene,
		ModelUsed:         engineResult.Model,
		CacheStatus:       engineResult.CacheStatus,
		SourceTrace:       engineResult.ClipEvidence,
		Provenance:        provenance,
		EffectiveLanguage: strings.TrimSpace(plan.Language),
		StockEnabled:      plan.StockEnabled,
		StockBindings:     append([]scriptpkg.StockBindingInput(nil), plan.StockBindings...),
	}

	postResult, err := p.ppReg.Run(ctx, &plan, procInput)
	if err != nil {
		return nil, &scriptpkg.PostprocessError{
			ItemID:    item.ID,
			Processor: "registry",
			Inner:     err,
		}
	}

	postprocessMs := make(map[string]int64)
	if postResult != nil && len(postResult.StageDurations) > 0 {
		postprocessMs = maps.Clone(postResult.StageDurations)
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
