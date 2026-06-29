package adapters

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// ClipBindingsProcessor assigns clips from ClipEvidence to scenes.
// Each clip maps to exactly one scene, in the canonical order from
// plan.ClipEvidence.ClipIDs (preserving the resolver's order). Extra
// scenes beyond the clip count receive no clip binding — this
// surfaces LLM output mismatches instead of silently cycling clips.
type ClipBindingsProcessor struct {
	log *zap.Logger
}

func NewClipBindingsProcessor(log *zap.Logger) *ClipBindingsProcessor {
	return &ClipBindingsProcessor{log: log}
}

func (p *ClipBindingsProcessor) Name() string { return "clip_bindings" }

// Policy classifies clip_bindings as ProcessorBestEffort: a nil or
// empty ClipEvidence is a no-op (Process returns early with empty
// result) rather than a hard fail. Matches the in-body comment that
// the processor "is a no-op when plan.ClipEvidence is nil/empty".
// Pair with `clip_bindings` in defaultPolicyByName so the
// LookupPolicy override path stays consistent.
func (p *ClipBindingsProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorBestEffort
}

func (p *ClipBindingsProcessor) Process(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	input ProcessInput,
) (*PostProcessResult, error) {
	if plan == nil {
		return &PostProcessResult{}, nil
	}
	if plan.ClipEvidence == nil || len(plan.ClipEvidence.ClipIDs) == 0 {
		return &PostProcessResult{}, nil
	}

	scenes := input.SpecScene.Scenes
	if len(scenes) == 0 {
		return &PostProcessResult{}, nil
	}

	// P0 #2 (June 2026): use the canonical ordered list from
	// plan.ClipEvidence.ClipIDs instead of iterating the
	// DriveLinks map + sort.Strings. The resolver's order is
	// preserved; clips bind to scenes 1:1 in arrival order.
	clipIDs := plan.ClipEvidence.ClipIDs

	// Respect NumClips limit.
	if plan.NumClips > 0 && plan.NumClips < len(clipIDs) {
		clipIDs = clipIDs[:plan.NumClips]
	}

	// One clip per scene — no modulo cycling. Extra scenes beyond
	// the clip count get no binding. This surfaces LLM output
	// mismatches (more scenes than clips) instead of silently
	// reusing clips.
	bindCount := len(clipIDs)
	if bindCount > len(scenes) {
		bindCount = len(scenes)
	}

	for i := 0; i < bindCount; i++ {
		clipID := clipIDs[i]
		driveLink := plan.ClipEvidence.DriveLinks[clipID]

		scenes[i].Bindings.Clip = &scriptpkg.ClipBinding{
			ClipID:    clipID,
			DriveLink: driveLink,
		}
	}

	// P0 #2: extra scenes beyond the clip count get no binding.
	// Explicitly nil out any LLM-assigned stale binding so the
	// mismatch is visible.
	for i := bindCount; i < len(scenes); i++ {
		scenes[i].Bindings.Clip = nil
	}

	if p.log != nil {
		p.log.Info("clip_bindings: assigned clips to scenes",
			zap.Int("scenes", len(scenes)),
			zap.Int("clips_bound", bindCount),
			zap.Int("clips_available", len(plan.ClipEvidence.ClipIDs)),
			zap.Int("scenes_unbound", len(scenes)-bindCount),
			zap.Strings("clip_ids", clipIDs[:bindCount]))
	}

	return &PostProcessResult{}, nil
}
