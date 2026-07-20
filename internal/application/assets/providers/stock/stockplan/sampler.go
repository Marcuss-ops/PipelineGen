package stockplan

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
)

// DefaultMaxClipsPerGroup caps the number of clips a single child
// job is asked to process. The value matches the user's request
// (max 15 clips per group).
const DefaultMaxClipsPerGroup = 15

// deterministicSampler is the canonical ClipSampler.
// It slices a temporal group into fixed-duration, non-overlapping
// clips, capping both the per-group clip count and the total
// group duration consumed.
type deterministicSampler struct{}

// NewDeterministicSampler returns the canonical ClipSampler.
func NewDeterministicSampler() ClipSampler {
	return &deterministicSampler{}
}

// Sample implements ClipSampler.
func (s *deterministicSampler) Sample(group GroupSpec, policy SamplingPolicy) ([]stockpipeline.ClipSpec, error) {
	policy.Normalize()

	if policy.MaxClipsPerGroup > DefaultMaxClipsPerGroup {
		policy.MaxClipsPerGroup = DefaultMaxClipsPerGroup
	}

	if group.EndSec <= group.StartSec {
		return nil, fmt.Errorf("stockplan.sampler: group %q has invalid range [%.3f, %.3f]", group.Key, group.StartSec, group.EndSec)
	}
	if policy.ClipDurationSec <= 0 {
		return nil, fmt.Errorf("stockplan.sampler: clip_duration_sec must be > 0")
	}

	clipDur := float64(policy.ClipDurationSec)
	maxDuration := float64(policy.MaxGroupDurationSec)

	var clips []stockpipeline.ClipSpec
	cursor := group.StartSec
	for len(clips) < policy.MaxClipsPerGroup {
		clipStart := cursor
		clipEnd := clipStart + clipDur
		if clipEnd > group.EndSec {
			clipEnd = group.EndSec
		}
		if clipStart >= group.EndSec || clipEnd <= clipStart {
			break
		}
		if clipEnd > group.StartSec+maxDuration {
			break
		}
		clips = append(clips, stockpipeline.ClipSpec{
			StartSec:   clipStart,
			EndSec:     clipEnd,
			Title:      group.Title,
			Slug:       group.Key,
			ParentSlug: group.Key,
		})
		cursor = clipEnd
	}

	return clips, nil
}
