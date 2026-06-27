package scripts

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

func (p *ClipBindingsProcessor) Process(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	model *scriptpkg.ModelScriptOutputV1,
	_ *PostProcessArtifact,
) (*PostProcessArtifact, error) {
	if model == nil || plan == nil {
		return &PostProcessArtifact{}, nil
	}
	if plan.ClipEvidence == nil || len(plan.ClipEvidence.DriveLinks) == 0 {
		return &PostProcessArtifact{}, nil
	}

	scenes := model.SpecScene.Scenes
	if len(scenes) == 0 {
		return &PostProcessArtifact{}, nil
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

	return &PostProcessArtifact{}, nil
}
