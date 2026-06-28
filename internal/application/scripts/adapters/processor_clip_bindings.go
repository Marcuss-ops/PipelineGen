package adapters

import (
	"context"
	"sort"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// ClipBindingsProcessor assigns clips from ClipEvidence to scenes.
// Cycles through available clips sequentially, one per scene (top-10
// compilation style). No-op when ClipEvidence is nil or empty.
type ClipBindingsProcessor struct {
	log *zap.Logger
}

func NewClipBindingsProcessor(log *zap.Logger) *ClipBindingsProcessor {
	return &ClipBindingsProcessor{log: log}
}

func (p *ClipBindingsProcessor) Name() string { return "clip_bindings" }

// Policy classifies clip_bindings as ProcessorBestEffort: a missing
// ClipEvidence (or one with zero DriveLinks) is a no-op (early
// return at the top of Process), and the assignment is
// deterministic and non-destructive. The plan arg is accepted for
// interface uniformity but ignored.
//
// PR-0 build fix (June 2026): the Policy() method was missing from
// the post-PR-7 refactor of processor_clip_bindings.go. Restoring
// it is required for the struct to satisfy the PostProcessor
// interface and to align with the other 6 processors (metadata,
// persistence, entities, voiceover, document, images) which all
// expose Policy(). BestEffort matches the soft policy of
// voiceover/images (no hard-fail on missing input).
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
	if plan.ClipEvidence == nil || len(plan.ClipEvidence.DriveLinks) == 0 {
		return &PostProcessResult{}, nil
	}

	scenes := input.SpecScene.Scenes
	if len(scenes) == 0 {
		return &PostProcessResult{}, nil
	}

	// Deterministic ordering so scenes are always assigned the same clip.
	clipIDs := make([]string, 0, len(plan.ClipEvidence.DriveLinks))
	for id := range plan.ClipEvidence.DriveLinks {
		clipIDs = append(clipIDs, id)
	}
	sort.Strings(clipIDs)

	if plan.NumClips > 0 && plan.NumClips < len(clipIDs) {
		clipIDs = clipIDs[:plan.NumClips]
	}
	if len(clipIDs) > len(scenes) {
		clipIDs = clipIDs[:len(scenes)]
	}

	for i := range scenes {
		scene := &scenes[i]
		clipID := clipIDs[i%len(clipIDs)]
		driveLink := plan.ClipEvidence.DriveLinks[clipID]

		scene.Bindings.Clip = &scriptpkg.ClipBinding{
			ClipID:    clipID,
			DriveLink: driveLink,
		}
	}

	if p.log != nil {
		p.log.Info("clip_bindings: assigned clips to scenes",
			zap.Int("scenes", len(scenes)),
			zap.Int("clips", len(clipIDs)),
			zap.Strings("clip_ids", clipIDs))
	}

	return &PostProcessResult{}, nil
}
