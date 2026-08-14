package render

import (
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// RenderPlanValidator is the only capability entry point that can mint a
// ValidatedRenderPlan. It performs structural, hash, manifest-file, and
// FINAL_AUDIO_COPY checks before an executor is allowed to run. The
// filesystem port is injected so manifest-file re-hashing never touches os
// directly.
type RenderPlanValidator struct {
	fs FileSystem
}

// NewRenderPlanValidator constructs the validator with the injected
// filesystem adapter. A nil fs fails closed at first manifest-file check.
func NewRenderPlanValidator(fs FileSystem) RenderPlanValidator {
	return RenderPlanValidator{fs: fs}
}

// ValidatedRenderPlan is an immutable-by-construction handoff contract. The
// underlying RenderPlan is copied when it is minted and when it is returned,
// so callers cannot mutate a plan after validation and accidentally hand a
// different payload to Velox.
type ValidatedRenderPlan struct {
	plan RenderPlan
}

// Validate performs all checks required before the real media executor. A
// render request with no visual segments is rejected here rather than later
// inside the infrastructure adapter.
func (v RenderPlanValidator) Validate(plan RenderPlan) (ValidatedRenderPlan, error) {
	if err := plan.Validate(); err != nil {
		return ValidatedRenderPlan{}, fmt.Errorf("render plan structural validation failed: %w", err)
	}
	if len(plan.Manifest) == 0 || len(plan.VideoTracks) == 0 || len(plan.VideoTracks[0].Segments) == 0 {
		return ValidatedRenderPlan{}, fmt.Errorf("%w: at least one primary video segment is required", ErrInvalidPlan)
	}
	if v.fs == nil {
		return ValidatedRenderPlan{}, fmt.Errorf("render plan physical validation failed: filesystem adapter is not configured")
	}
	if err := plan.ValidateManifestFiles(v.fs); err != nil {
		return ValidatedRenderPlan{}, fmt.Errorf("render plan physical validation failed: %w", err)
	}
	if plan.FinalAudio != nil {
		if err := validateFinalAudioContract(plan); err != nil {
			return ValidatedRenderPlan{}, err
		}
	}
	return ValidatedRenderPlan{plan: clonePlan(plan)}, nil
}

// ValidateRenderPlan is the concise boundary helper for callers that do not
// need to retain a validator value. The filesystem adapter is injected by
// the composition root (or an infrastructure adapter for transport-layer
// re-validation).
func ValidateRenderPlan(plan RenderPlan, fs FileSystem) (ValidatedRenderPlan, error) {
	return NewRenderPlanValidator(fs).Validate(plan)
}

// Plan returns an independent copy suitable for transport or inspection.
func (p ValidatedRenderPlan) Plan() RenderPlan {
	return clonePlan(p.plan)
}

// MarshalJSON preserves the exact RenderPlan wire contract while preventing
// callers from bypassing validation at the executor interface.
func (p ValidatedRenderPlan) MarshalJSON() ([]byte, error) {
	return json.Marshal(p.plan)
}

func validateFinalAudioContract(plan RenderPlan) error {
	audioAsset := plan.FinalAudio
	if audioAsset == nil {
		return nil
	}
	if !isSHA256(audioAsset.PlanSHA256) {
		return fmt.Errorf("%w: FINAL_AUDIO_COPY requires a valid certified audio plan SHA256", ErrInvalidPlan)
	}
	if audioAsset.AssetKind != "final_audio" || audioAsset.Strategy != string(audio.FinalAudioCopy) || audioAsset.AudioContractVersion != audio.AudioContractVersion || audioAsset.AudioPlanVersion != audio.AudioPlanVersion {
		return fmt.Errorf("%w: FINAL_AUDIO_COPY asset kind, strategy, or version mismatch", ErrInvalidPlan)
	}
	profile := audio.DefaultAudioProfile().Output()
	if !audioAsset.FinalMix || !audioAsset.CopyEligible || audioAsset.Codec != profile.Codec || audioAsset.Profile != profile.Profile || audioAsset.SampleRate != profile.SampleRate || audioAsset.Channels != profile.Channels || audioAsset.ChannelLayout != profile.ChannelLayout || audioAsset.SizeBytes <= 0 || audioAsset.DurationMS <= 0 || audioAsset.StartPTS < 0 {
		return fmt.Errorf("%w: FINAL_AUDIO_COPY requires canonical copy-eligible audio metadata", ErrInvalidPlan)
	}
	expectedDurationMS := (plan.Timeline.DurationUS + 999) / 1000
	if audioAsset.DurationMS < expectedDurationMS-40 || audioAsset.DurationMS > expectedDurationMS+40 {
		return fmt.Errorf("%w: FINAL_AUDIO_COPY duration does not match render timeline", ErrInvalidPlan)
	}
	return nil
}

func clonePlan(plan RenderPlan) RenderPlan {
	copyPlan := plan
	copyPlan.Timeline.Segments = append([]audio.TimelineSegment(nil), plan.Timeline.Segments...)
	copyPlan.Manifest = append([]AssetManifestEntry(nil), plan.Manifest...)
	copyPlan.VideoTracks = make([]VideoTrack, len(plan.VideoTracks))
	for i, track := range plan.VideoTracks {
		copyPlan.VideoTracks[i] = track
		copyPlan.VideoTracks[i].Segments = append([]VideoSegment(nil), track.Segments...)
	}
	if plan.FinalAudio != nil {
		finalAudio := *plan.FinalAudio
		copyPlan.FinalAudio = &finalAudio
	}
	return copyPlan
}
