package wiring

import (
	"context"
	"fmt"
	"math"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func (g *SceneTextGenerator) resolveEvidenceClip(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, clipID string, allowDriveOnly bool) (*scriptgen.ClipReference, error) {
	detail := plan.ClipEvidence.ClipDetails[clipID]
	if allowDriveOnly {
		driveLink := detail.DriveLink
		if driveLink == "" {
			driveLink = plan.ClipEvidence.DriveLinks[clipID]
		}
		if driveLink == "" {
			return nil, fmt.Errorf("clip %s has no Drive link", clipID)
		}
		return &scriptgen.ClipReference{ID: clipID, Title: detail.Name, DriveLink: driveLink, SourceInMS: detail.StartMs, SourceOutMS: detail.EndMs}, nil
	}
	clip, err := g.resolveRenderClip(ctx, scriptpkg.ClipBinding{ClipID: clipID, ClipTitle: plan.ClipEvidence.ClipNames[clipID]})
	if err != nil {
		return nil, err
	}
	if detail, ok := plan.ClipEvidence.ClipDetails[clipID]; ok {
		clip.SourceInMS, clip.SourceOutMS = detail.StartMs, detail.EndMs
	}
	if clip.SourceOutMS <= clip.SourceInMS {
		clip.SourceInMS = 0
		clip.SourceOutMS = int64(math.Round(clip.Duration * 1000))
	}
	if clip.SourceOutMS <= clip.SourceInMS {
		return nil, fmt.Errorf("clip %s has no usable source duration", clipID)
	}
	return clip, nil
}
