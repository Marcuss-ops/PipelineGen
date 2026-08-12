// Package audio owns the canonical, implementation-independent audio timeline
// contract. It deliberately contains no FFmpeg, filesystem, or transport code.
//
// Internal timing is exclusively integer microseconds. Millisecond fields are
// accepted only by the legacy wire DTO and normalized at the boundary.
package audio

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
)

const (
	TimelineVersion  = "canonical-timeline.v2"
	AudioPlanVersion = "compiled-audio-plan.v1"

	// FinalAudioAsset remains a separately versioned artifact contract; the
	// timeline migration does not invalidate certified media metadata.
	AudioContractVersion = "canonical-audio.v1"
)

type AudioSegmentMode string

const (
	AudioVoiceover AudioSegmentMode = "VOICEOVER"
	AudioClip      AudioSegmentMode = "CLIP_AUDIO"
	AudioSilence   AudioSegmentMode = "SILENCE"
)

type VideoSegment struct {
	AssetID          string `json:"asset_id,omitempty"`
	SourceInUS       int64  `json:"source_in_us,omitempty"`
	SourceDurationUS int64  `json:"source_duration_us,omitempty"`
}

type AudioIntent struct {
	Mode             AudioSegmentMode `json:"mode"`
	VoiceoverAssetID string           `json:"voiceover_asset_id,omitempty"`
	ClipAssetID      string           `json:"clip_asset_id,omitempty"`
	SourceInUS       int64            `json:"source_in_us,omitempty"`
	SourceDurationUS int64            `json:"source_duration_us,omitempty"`
	UseOriginalAudio bool             `json:"use_original_audio,omitempty"`
	GainDB           float64          `json:"gain_db,omitempty"`
}

type TimelineSegment struct {
	ID              string       `json:"id"`
	Index           int          `json:"index"`
	TimelineStartUS int64        `json:"timeline_start_us"`
	DurationUS      int64        `json:"duration_us"`
	Video           VideoSegment `json:"video"`
	Audio           AudioIntent  `json:"audio"`
}

type CanonicalTimeline struct {
	Version    string            `json:"version"`
	DurationUS int64             `json:"duration_us"`
	Segments   []TimelineSegment `json:"segments"`
}

// UnmarshalJSON prevents internal consumers from silently accepting legacy
// millisecond fields by unmarshalling directly into the canonical type. Legacy
// wire payloads must use NormalizeTimelineJSON at the boundary.
func (t *CanonicalTimeline) UnmarshalJSON(data []byte) error {
	if t == nil {
		return fmt.Errorf("cannot unmarshal canonical timeline into nil receiver")
	}
	if hasLegacyTimelineFields(data) {
		return fmt.Errorf("canonical timeline accepts only microsecond fields; use NormalizeTimelineJSON for legacy input")
	}
	type canonicalTimeline CanonicalTimeline
	var decoded canonicalTimeline
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*t = CanonicalTimeline(decoded)
	return nil
}

type AudioEventType string

const (
	EventVoiceover AudioEventType = "VOICEOVER"
	EventClip      AudioEventType = "CLIP_AUDIO"
	EventSilence   AudioEventType = "SILENCE"
)

type AudioEvent struct {
	Type             AudioEventType `json:"type"`
	AssetID          string         `json:"asset_id,omitempty"`
	TimelineStartUS  int64          `json:"timeline_start_us"`
	DurationUS       int64          `json:"duration_us"`
	SourceInUS       int64          `json:"source_in_us,omitempty"`
	SourceDurationUS int64          `json:"source_duration_us,omitempty"`
	UseOriginalAudio bool           `json:"use_original_audio,omitempty"`
	GainDB           float64        `json:"gain_db,omitempty"`
}

type AudioLayer struct {
	AssetID         string  `json:"asset_id"`
	TimelineStartUS int64   `json:"timeline_start_us"`
	DurationUS      int64   `json:"duration_us"`
	GainDB          float64 `json:"gain_db,omitempty"`
}

type AudioAutomation struct {
	TargetLayer string  `json:"target_layer"`
	StartUS     int64   `json:"start_us"`
	EndUS       int64   `json:"end_us"`
	GainDB      float64 `json:"gain_db"`
	AttackUS    int64   `json:"attack_us"`
	ReleaseUS   int64   `json:"release_us"`
}

type AudioOutputContract struct {
	Codec         string `json:"codec"`
	Profile       string `json:"profile"`
	SampleRate    int    `json:"sample_rate"`
	Channels      int    `json:"channels"`
	ChannelLayout string `json:"channel_layout"`
	Bitrate       string `json:"bitrate"`
}

type CanonicalAudioProfile struct {
	Codec         string
	Profile       string
	SampleRate    int
	Channels      int
	ChannelLayout string
	Bitrate       string
}

func DefaultAudioProfile() CanonicalAudioProfile {
	return CanonicalAudioProfile{Codec: "aac", Profile: "LC", SampleRate: 48000, Channels: 2, ChannelLayout: "stereo", Bitrate: "128k"}
}

func (p CanonicalAudioProfile) Output() AudioOutputContract {
	if p == (CanonicalAudioProfile{}) {
		p = DefaultAudioProfile()
	}
	profile := strings.TrimSpace(p.Profile)
	if strings.EqualFold(profile, "aac_low") {
		profile = "LC"
	}
	return AudioOutputContract{Codec: strings.ToLower(strings.TrimSpace(p.Codec)), Profile: profile, SampleRate: p.SampleRate, Channels: p.Channels, ChannelLayout: strings.ToLower(strings.TrimSpace(p.ChannelLayout)), Bitrate: strings.TrimSpace(p.Bitrate)}
}

type CompiledAudioPlan struct {
	Version         string              `json:"audio_plan_version"`
	TimelineVersion string              `json:"timeline_version"`
	DurationUS      int64               `json:"duration_us"`
	Events          []AudioEvent        `json:"primary_events"`
	BackgroundMusic []AudioLayer        `json:"background_music,omitempty"`
	SFX             []AudioLayer        `json:"sfx,omitempty"`
	Automation      []AudioAutomation   `json:"automation,omitempty"`
	Output          AudioOutputContract `json:"canonical_audio_profile"`
	PlanSHA256      string              `json:"audio_plan_sha256"`
}

type ResolvedAudioAsset struct {
	AssetID string `json:"asset_id"`
	Path    string `json:"path"`
}

type ResolvedAudioAssets []ResolvedAudioAsset

var (
	ErrInvalidTimeline  = errors.New("invalid canonical timeline")
	ErrInvalidAudioPlan = errors.New("invalid compiled audio plan")
)

func (t CanonicalTimeline) Validate() error {
	if t.Version != TimelineVersion || t.DurationUS <= 0 || len(t.Segments) == 0 {
		return fmt.Errorf("%w: version or duration", ErrInvalidTimeline)
	}
	var end int64
	for i, s := range t.Segments {
		if s.Index != i || strings.TrimSpace(s.ID) == "" || s.DurationUS <= 0 || s.TimelineStartUS != end {
			return fmt.Errorf("%w: segment %d is not contiguous", ErrInvalidTimeline, i)
		}
		if end > math.MaxInt64-s.DurationUS {
			return fmt.Errorf("%w: segment %d duration overflows", ErrInvalidTimeline, i)
		}
		end += s.DurationUS
		if s.Video.SourceInUS < 0 || s.Video.SourceDurationUS < 0 || (s.Video.AssetID != "" && s.Video.SourceDurationUS <= 0) {
			return fmt.Errorf("%w: segment %d video source range", ErrInvalidTimeline, i)
		}
		if s.Audio.SourceInUS < 0 || s.Audio.SourceDurationUS < 0 || (s.Audio.Mode == AudioClip && s.Audio.SourceDurationUS <= 0) {
			return fmt.Errorf("%w: segment %d audio source range", ErrInvalidTimeline, i)
		}
		switch s.Audio.Mode {
		case AudioVoiceover, AudioClip, AudioSilence:
		default:
			return fmt.Errorf("%w: segment %d audio mode", ErrInvalidTimeline, i)
		}
	}
	if end != t.DurationUS {
		return fmt.Errorf("%w: duration %d does not equal segment end %d", ErrInvalidTimeline, t.DurationUS, end)
	}
	return nil
}

func (p *CompiledAudioPlan) Validate() error {
	if p == nil || p.Version != AudioPlanVersion || p.TimelineVersion != TimelineVersion || p.DurationUS < 0 {
		return fmt.Errorf("%w: version or duration", ErrInvalidAudioPlan)
	}
	if p.Output != (AudioOutputContract{}) && (p.Output.Codec == "" || p.Output.SampleRate <= 0 || p.Output.Channels <= 0 || p.Output.ChannelLayout == "") {
		return fmt.Errorf("%w: incomplete output contract", ErrInvalidAudioPlan)
	}
	var previous int64
	for i, e := range p.Events {
		if e.TimelineStartUS != previous || e.TimelineStartUS < 0 || e.DurationUS <= 0 || e.TimelineStartUS > math.MaxInt64-e.DurationUS || e.TimelineStartUS+e.DurationUS > p.DurationUS {
			return fmt.Errorf("%w: event %d range", ErrInvalidAudioPlan, i)
		}
		previous = e.TimelineStartUS + e.DurationUS
		if e.Type != EventVoiceover && e.Type != EventClip && e.Type != EventSilence {
			return fmt.Errorf("%w: event %d type", ErrInvalidAudioPlan, i)
		}
		if (e.Type == EventVoiceover || e.Type == EventClip) && strings.TrimSpace(e.AssetID) == "" {
			return fmt.Errorf("%w: event %d asset", ErrInvalidAudioPlan, i)
		}
		if e.Type == EventClip && e.SourceDurationUS <= 0 {
			return fmt.Errorf("%w: event %d source range", ErrInvalidAudioPlan, i)
		}
	}
	for _, layer := range append(append([]AudioLayer{}, p.BackgroundMusic...), p.SFX...) {
		if strings.TrimSpace(layer.AssetID) == "" || layer.TimelineStartUS < 0 || layer.DurationUS <= 0 || layer.TimelineStartUS > math.MaxInt64-layer.DurationUS || layer.TimelineStartUS+layer.DurationUS > p.DurationUS {
			return fmt.Errorf("%w: layer range or asset", ErrInvalidAudioPlan)
		}
	}
	for _, automation := range p.Automation {
		if strings.TrimSpace(automation.TargetLayer) == "" || automation.StartUS < 0 || automation.EndUS <= automation.StartUS || automation.EndUS > p.DurationUS || automation.AttackUS < 0 || automation.ReleaseUS < 0 {
			return fmt.Errorf("%w: automation range", ErrInvalidAudioPlan)
		}
	}
	return nil
}

func (p CompiledAudioPlan) Hash() (string, error) {
	copyPlan := p
	copyPlan.PlanSHA256 = ""
	if err := copyPlan.Validate(); err != nil {
		return "", err
	}
	b, err := json.Marshal(copyPlan)
	if err != nil {
		return "", fmt.Errorf("hash audio plan: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func (p *CompiledAudioPlan) Seal() error {
	h, err := p.Hash()
	if err != nil {
		return err
	}
	p.PlanSHA256 = h
	return nil
}

func (t CanonicalTimeline) Hash() (string, error) {
	if err := t.Validate(); err != nil {
		return "", err
	}
	b, err := json.Marshal(t)
	if err != nil {
		return "", fmt.Errorf("hash canonical timeline: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
