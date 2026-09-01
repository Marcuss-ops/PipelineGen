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
	"strings"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// GeneratedDraft holds the canonical output of the engine phase.
type GeneratedDraft struct {
	EngineResult *EngineResult
	EngineMs     int64
	// EngineReport is the canonical script.engine StageReport observation, so
	// the orchestrator can project EngineMs via CanonicalTimingAdapter without
	// starting a second clock. EngineMs is the same value, kept for callers
	// that read the flat field.
	EngineReport kernobs.StageReport
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
	var engineResult *EngineResult
	var engineErr error
	stageReport, measureErr := kernobs.MeasureStageReport(ctx, scriptgen.StageScriptEngine, func(opCtx context.Context) error {
		engineResult, engineErr = r.engine.Generate(opCtx, &plan)
		return engineErr
	})
	if measureErr != nil && engineErr == nil {
		engineErr = measureErr
	}
	if engineErr != nil {
		return nil, &scriptpkg.GenerationError{
			ItemID: item.ID,
			Phase:  "engine",
			Inner:  fmt.Errorf("ollama generation failed: %w", engineErr),
		}
	}
	if item.ScriptParams.SingleScene {
		collapseToSingleScene(engineResult)
	}
	if err := alignExplicitSegmentOutput(engineResult, item, plan); err != nil {
		return nil, fmt.Errorf("align explicit segment output: %w", err)
	}

	engineMs := stageReport.DurationMs
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
		EngineReport: stageReport,
	}, nil
}

// alignExplicitSegmentOutput protects the scene identity boundary after the
// model/parser phase. A generator may return one opaque prose block even when
// the caller supplied ordered segments. Passing that block unchanged to
// postprocessors gives every scene the same text hash and contaminates
// provider queries. Explicit segment source text/topic is authoritative here.
func alignExplicitSegmentOutput(result *EngineResult, item scriptpkg.GenerationItemV2, plan scriptpkg.ResolvedGenerationPlan) error {
	if result == nil || len(plan.Segments) == 0 || plan.SingleScene {
		return nil
	}
	paragraphs := nonEmptyParagraphs(item.Source.SourceText)
	scenes := make([]scriptpkg.SpecScene, 0, len(plan.Segments))
	for i, segment := range plan.Segments {
		text := strings.TrimSpace(segment.SourceText)
		if text == "" && i < len(paragraphs) {
			text = paragraphs[i]
		}
		if text == "" {
			text = strings.TrimSpace(segment.Topic)
		}
		if text == "" {
			return fmt.Errorf("segment %d has no authoritative text", i)
		}
		id := strings.TrimSpace(segment.ID)
		if id == "" {
			id = fmt.Sprintf("scene-%d", i)
		}
		scenes = append(scenes, scriptpkg.SpecScene{ID: id, SegmentID: id, Index: i, Kind: scriptpkg.SceneNarration, Text: text})
	}
	result.Output.SpecScene.Scenes = scenes
	result.Output.Text = strings.Join(func() []string {
		out := make([]string, 0, len(scenes))
		for _, scene := range scenes {
			out = append(out, scene.Text)
		}
		return out
	}(), "\n\n")
	return nil
}

func nonEmptyParagraphs(source string) []string {
	parts := strings.Split(strings.TrimSpace(source), "\n\n")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			out = append(out, value)
		}
	}
	return out
}

// collapseToSingleScene preserves the generated prose while making the
// structured scene surface match an explicit single-scene request. Bindings
// are intentionally left for the postprocessor chain, which is the canonical
// owner of stock/clip assignment.
func collapseToSingleScene(result *EngineResult) {
	if result == nil {
		return
	}
	text := strings.TrimSpace(result.Output.Text)
	if text == "" {
		parts := make([]string, 0, len(result.Output.SpecScene.Scenes))
		for _, scene := range result.Output.SpecScene.Scenes {
			if value := strings.TrimSpace(scene.Text); value != "" {
				parts = append(parts, value)
			}
		}
		text = strings.Join(parts, "\n\n")
	}
	result.Output.SpecScene.Scenes = []scriptpkg.SpecScene{{
		ID:    "scene-0",
		Index: 0,
		Kind:  scriptpkg.SceneNarration,
		Text:  text,
	}}
}
