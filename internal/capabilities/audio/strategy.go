package audio

import (
	"errors"
	"fmt"
	"strings"
)

type AudioMode string

const (
	AudioModeNone             AudioMode = "NONE"
	AudioModeChunkedVoiceover AudioMode = "CHUNKED_VOICEOVER"
	AudioModeCombinedTimeline AudioMode = "COMBINED_TIMELINE"
)

func (m AudioMode) Normalize() AudioMode {
	m = AudioMode(strings.ToUpper(strings.TrimSpace(string(m))))
	switch m {
	case AudioModeNone, AudioModeChunkedVoiceover, AudioModeCombinedTimeline:
		return m
	default:
		return ""
	}
}

func ResolveAudioMode(requested AudioMode, voiceoverEnabled bool) (AudioMode, error) {
	mode := requested
	if mode == "" {
		// An omitted mode is only valid when audio is not requested. The
		// legacy voiceover flag is intentionally not a mode selector.
		if voiceoverEnabled {
			return "", fmt.Errorf("audio mode is required when voiceover is enabled")
		}
		return AudioModeNone, nil
	}
	mode = mode.Normalize()
	if mode == "" {
		return "", fmt.Errorf("unsupported audio mode %q", requested)
	}
	// COMBINED_TIMELINE compiles one certified final_audio.m4a from the
	// canonical timeline + compiled audio plan. It requires only
	// audio/timeline prerequisites (narration, canonical timeline, audio
	// plan) — NOT video rendering. Audio mode resolution is fully
	// independent of any video flag.
	return mode, nil
}

type AudioRenderStrategy string

const (
	FinalAudioCopy AudioRenderStrategy = "FINAL_AUDIO_COPY"
	TimelineMix    AudioRenderStrategy = "TIMELINE_MIX"
)

type FinalAudioAsset struct {
	AssetID              string `json:"audio_asset_id"`
	AudioContractVersion string `json:"audio_contract_version"`
	AudioPlanVersion     string `json:"audio_plan_version"`
	AudioPlanSHA256      string `json:"audio_plan_sha256"`
	FinalAudioSHA256     string `json:"final_audio_sha256"`
	Codec                string `json:"codec"`
	Profile              string `json:"profile"`
	SampleRate           int    `json:"sample_rate"`
	Channels             int    `json:"channels"`
	ChannelLayout        string `json:"channel_layout"`
	DurationMS           int64  `json:"duration_ms"`
	StartPTS             int64  `json:"start_pts"`
	Bitrate              int64  `json:"bitrate"`
	SizeBytes            int64  `json:"size_bytes"`
	FinalMix             bool   `json:"final_mix"`
	CopyEligible         bool   `json:"copy_eligible"`
}

// ErrAudioMediaIncompatible is stable for callers that request
// FINAL_AUDIO_COPY. It must never be converted into TIMELINE_MIX implicitly.
var ErrAudioMediaIncompatible = errors.New("AUDIO_MEDIA_INCOMPATIBLE")

func ValidateFinalAudio(asset FinalAudioAsset, plan CompiledAudioPlan) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	if asset.AudioContractVersion != AudioContractVersion || asset.AudioPlanVersion != plan.Version || asset.AudioPlanSHA256 != plan.PlanSHA256 {
		return fmt.Errorf("%w: final audio contract does not match plan", ErrAudioMediaIncompatible)
	}
	planDurationMS := (plan.DurationUS + 999) / 1000
	if !asset.FinalMix || !asset.CopyEligible || asset.FinalAudioSHA256 == "" || asset.DurationMS <= 0 || asset.SizeBytes <= 0 || asset.Bitrate <= 0 || asset.StartPTS < 0 || asset.DurationMS < planDurationMS-40 || asset.DurationMS > planDurationMS+40 || asset.Codec != plan.Output.Codec || asset.Profile != plan.Output.Profile || asset.SampleRate != plan.Output.SampleRate || asset.Channels != plan.Output.Channels || asset.ChannelLayout != plan.Output.ChannelLayout {
		return fmt.Errorf("%w: final audio probe failed canonical validation", ErrAudioMediaIncompatible)
	}
	return nil
}

func ResolveAudioRenderStrategy(asset *FinalAudioAsset, plan CompiledAudioPlan) (AudioRenderStrategy, error) {
	if asset != nil {
		if err := ValidateFinalAudio(*asset, plan); err != nil {
			return "", fmt.Errorf("final audio copy validation failed: %w", err)
		}
		return FinalAudioCopy, nil
	}
	return TimelineMix, nil
}
