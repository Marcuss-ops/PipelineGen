// Package usecase — generation_engine.go owns the canonical
// engine phase for single-item script generation.
//
// Responsibilities:
//   - invoke the script generation engine
//   - measure engine execution time
//   - emit progress tracker events
//   - return a typed GeneratedDraft
//
// The engine phase is intentionally stateless except for its
// dependency on the canonical Engine. It returns a GeneratedDraft
// value object that feeds the postprocess and finalize phases.
package usecase

import (
	"context"
	"fmt"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// GeneratedDraft holds the canonical output of the engine phase.
type GeneratedDraft struct {
	EngineResult *EngineResult
	EngineMs     int64
}

// GenerationEngineRunner invokes the script generation engine for a
// single prepared plan. It is constructed once per use case and
// reused across calls.
type GenerationEngineRunner struct {
	engine *Engine
}

// NewGenerationEngineRunner constructs a GenerationEngineRunner.
// Returns nil when engine is nil so callers can treat a missing
// engine as an construction-time failure (preserves the pre-PR
// GenerateOneUseCase engine=nil behavior).
func NewGenerationEngineRunner(engine *Engine) *GenerationEngineRunner {
	if engine == nil {
		return nil
	}
	return &GenerationEngineRunner{engine: engine}
}

// Generate calls the engine for the supplied plan and returns a
// GeneratedDraft. Progress and timeline events are emitted through
// the tracker.
func (r *GenerationEngineRunner) Generate(
	ctx context.Context,
	item scriptpkg.GenerationItemV2,
	plan scriptpkg.ResolvedGenerationPlan,
	tracker *ProgressTracker,
) (*GeneratedDraft, error) {
	if r == nil || r.engine == nil {
		return nil, &scriptpkg.GenerationError{
			ItemID: item.ID,
			Phase:  "engine",
			Inner:  fmt.Errorf("engine not configured"),
		}
	}

	tracker.PhaseGenerateStart()
	engineStart := time.Now()

	engineResult, engineErr := r.engine.Generate(ctx, &plan)
	if engineErr != nil {
		return nil, &scriptpkg.GenerationError{
			ItemID: item.ID,
			Phase:  "engine",
			Inner:  fmt.Errorf("ollama generation failed: %w", engineErr),
		}
	}

	engineMs := time.Since(engineStart).Milliseconds()
	tracker.PhaseGenerateDone()
	tracker.TrackEvent("script.generated", "Script text generated", map[string]any{
		"item_id":      item.ID,
		"word_count":   engineResult.WordCount,
		"model":        engineResult.Model,
		"cache_status": engineResult.CacheStatus,
	})
	if len(engineResult.Output.SpecScene.Scenes) > 0 {
		tracker.TrackEvent("scenes.created", "Scenes created from generated script", map[string]any{
			"item_id":     item.ID,
			"scene_count": len(engineResult.Output.SpecScene.Scenes),
		})
	}

	return &GeneratedDraft{
		EngineResult: engineResult,
		EngineMs:     engineMs,
	}, nil
}
