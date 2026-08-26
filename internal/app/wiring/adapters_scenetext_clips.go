package wiring

import (
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"context"
	"fmt"
	"math"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func (g *SceneTextGenerator) resolveEvidenceClip(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, clipID string, allowDriveOnly bool) (*scriptgen.ClipReference, error) {
	detail := plan.ClipEvidence.ClipDetails[clipID]
	// Local media wins when present: the COMBINED_TIMELINE audio-only master
	// mixes the original clip audio, so the resolved clip must carry the
	// canonical local path (AudioPath/Path), probed duration and source window.
	clip, err := g.resolveRenderClip(ctx, scriptpkg.ClipBinding{ClipID: clipID, ClipTitle: plan.ClipEvidence.ClipNames[clipID]})
	if err == nil {
		if d, ok := plan.ClipEvidence.ClipDetails[clipID]; ok {
			clip.SourceInMS, clip.SourceOutMS = d.StartMs, d.EndMs
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
	if !allowDriveOnly {
		return nil, err
	}
	driveLink := detail.DriveLink
	if driveLink == "" {
		driveLink = plan.ClipEvidence.DriveLinks[clipID]
	}
	if driveLink == "" {
		return nil, fmt.Errorf("clip %s has no Drive link", clipID)
	}
	return &scriptgen.ClipReference{ID: clipID, Title: detail.Name, DriveLink: driveLink, SourceInMS: detail.StartMs, SourceOutMS: detail.EndMs}, nil
}
