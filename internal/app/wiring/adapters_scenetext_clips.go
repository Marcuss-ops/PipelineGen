package wiring

import (
	"context"
	"fmt"
	"math"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// markSegmentStockClips applies the segment's per-clip stock marking
// (script_params.segments[].stock_clip_ids) to the resolved scene clips. A
// stock-marked clip keeps its binding and original audio, while its video is
// never processed with a localized clip render and never shown: the scene's
// visual comes from its stock binding.
func markSegmentStockClips(clips []*scriptgen.ClipReference, segment scriptpkg.ScriptSegment) {
	if len(segment.StockClipIDs) == 0 {
		return
	}
	for _, clip := range clips {
		if clip == nil {
			continue
		}
		if scriptpkg.SegmentClipIsStock(segment, clip.ID) {
			clip.AsStock = true
		}
	}
}

// stockClipAudioIntent is the canonical voice of a stock-marked clip in the
// master mix: the clip's ORIGINAL audio at full volume, protected from the
// run's global VO-only removal and from ducking (the caller explicitly asked
// for this clip to be heard as stock).
func stockClipAudioIntent(clipID string, sourceInUS, durationUS int64, timelineOffsetUS int64) capabilityaudio.AudioIntent {
	return capabilityaudio.AudioIntent{
		Mode:                   capabilityaudio.AudioClip,
		ClipAssetID:            clipID,
		SourceInUS:             sourceInUS,
		SourceDurationUS:       durationUS,
		TimelineOffsetUS:       timelineOffsetUS,
		TimelineDurationUS:     durationUS,
		UseOriginalAudio:       true,
		ProtectedOriginalAudio: true,
		GainDB:                 0,
	}
}

func (g *SceneTextGenerator) resolveEvidenceClip(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, clipID string, allowDriveOnly bool) (*scriptgen.ClipReference, error) {
	if plan == nil || plan.ClipEvidence == nil {
		return nil, fmt.Errorf("clip %s requires resolved clip evidence", clipID)
	}
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
