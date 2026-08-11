// Package audio owns the canonical, implementation-independent audio timeline
// contract. It deliberately contains no FFmpeg, filesystem, or transport code.
package audio

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	TimelineVersion      = "canonical-timeline.v1"
	AudioPlanVersion     = "compiled-audio-plan.v1"
	AudioContractVersion = "canonical-audio.v1"
)

type AudioSegmentMode string

const (
	AudioVoiceover AudioSegmentMode = "VOICEOVER"
	AudioClip      AudioSegmentMode = "CLIP_AUDIO"
	AudioSilence   AudioSegmentMode = "SILENCE"
)

type VideoSegment struct {
	AssetID     string `json:"asset_id,omitempty"`
	SourceInMS  int64  `json:"source_in_ms,omitempty"`
	SourceOutMS int64  `json:"source_out_ms,omitempty"`
}

type AudioIntent struct {
	Mode             AudioSegmentMode `json:"mode"`
	VoiceoverAssetID string           `json:"voiceover_asset_id,omitempty"`
	ClipAssetID      string           `json:"clip_asset_id,omitempty"`
	SourceInMS       int64            `json:"source_in_ms,omitempty"`
	SourceOutMS      int64            `json:"source_out_ms,omitempty"`
	UseOriginalAudio bool             `json:"use_original_audio,omitempty"`
	GainDB           float64          `json:"gain_db,omitempty"`
}

type TimelineSegment struct {
	ID              string       `json:"id"`
	Index           int          `json:"index"`
	TimelineStartMS int64        `json:"timeline_start_ms"`
	DurationMS      int64        `json:"duration_ms"`
	Video           VideoSegment `json:"video"`
	Audio           AudioIntent  `json:"audio"`
}

type CanonicalTimeline struct {
	Version    string            `json:"version"`
	DurationMS int64             `json:"duration_ms"`
	Segments   []TimelineSegment `json:"segments"`
}

type AudioEventType string

const (
	EventVoiceover AudioEventType = "VOICEOVER"
	EventClip      AudioEventType = "CLIP_AUDIO"
	EventSilence   AudioEventType = "SILENCE"
)

type AudioEvent struct {
	Type            AudioEventType `json:"type"`
	AssetID         string         `json:"asset_id,omitempty"`
	TimelineStartMS int64          `json:"timeline_start_ms"`
	DurationMS      int64          `json:"duration_ms"`
	SourceInMS      int64          `json:"source_in_ms,omitempty"`
	SourceOutMS     int64          `json:"source_out_ms,omitempty"`
	GainDB          float64        `json:"gain_db,omitempty"`
}

type AudioLayer struct {
	AssetID         string  `json:"asset_id"`
	TimelineStartMS int64   `json:"timeline_start_ms"`
	DurationMS      int64   `json:"duration_ms"`
	GainDB          float64 `json:"gain_db,omitempty"`
}

type AudioAutomation struct {
	TargetLayer string  `json:"target_layer"`
	StartMS     int64   `json:"start_ms"`
	EndMS       int64   `json:"end_ms"`
	GainDB      float64 `json:"gain_db"`
	AttackMS    int64   `json:"attack_ms"`
	ReleaseMS   int64   `json:"release_ms"`
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
	DurationMS      int64               `json:"duration_ms"`
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
	if t.Version != TimelineVersion || t.DurationMS <= 0 || len(t.Segments) == 0 {
		return fmt.Errorf("%w: version or duration", ErrInvalidTimeline)
	}
	var end int64
	for i, s := range t.Segments {
		if s.Index != i || strings.TrimSpace(s.ID) == "" || s.DurationMS <= 0 || s.TimelineStartMS != end {
			return fmt.Errorf("%w: segment %d is not contiguous", ErrInvalidTimeline, i)
		}
		end += s.DurationMS
		if s.Audio.SourceInMS < 0 || s.Audio.SourceOutMS < 0 || (s.Audio.SourceOutMS > 0 && s.Audio.SourceOutMS <= s.Audio.SourceInMS) {
			return fmt.Errorf("%w: segment %d source range", ErrInvalidTimeline, i)
		}
		switch s.Audio.Mode {
		case AudioVoiceover, AudioClip, AudioSilence:
		default:
			return fmt.Errorf("%w: segment %d audio mode", ErrInvalidTimeline, i)
		}
	}
	if end != t.DurationMS {
		return fmt.Errorf("%w: duration %d does not equal segment end %d", ErrInvalidTimeline, t.DurationMS, end)
	}
	return nil
}

func (p *CompiledAudioPlan) Validate() error {
	if p == nil || p.Version != AudioPlanVersion || p.TimelineVersion != TimelineVersion || p.DurationMS < 0 {
		return fmt.Errorf("%w: version or duration", ErrInvalidAudioPlan)
	}
	if p.Output != (AudioOutputContract{}) && (p.Output.Codec == "" || p.Output.SampleRate <= 0 || p.Output.Channels <= 0 || p.Output.ChannelLayout == "") {
		return fmt.Errorf("%w: incomplete output contract", ErrInvalidAudioPlan)
	}
	var previous int64
	for i, e := range p.Events {
		if e.TimelineStartMS != previous || e.TimelineStartMS < 0 || e.DurationMS <= 0 || e.TimelineStartMS+e.DurationMS > p.DurationMS {
			return fmt.Errorf("%w: event %d range", ErrInvalidAudioPlan, i)
		}
		previous = e.TimelineStartMS + e.DurationMS
		if e.Type != EventVoiceover && e.Type != EventClip && e.Type != EventSilence {
			return fmt.Errorf("%w: event %d type", ErrInvalidAudioPlan, i)
		}
		if (e.Type == EventVoiceover || e.Type == EventClip) && strings.TrimSpace(e.AssetID) == "" {
			return fmt.Errorf("%w: event %d asset", ErrInvalidAudioPlan, i)
		}
		if e.Type == EventClip && e.SourceOutMS <= e.SourceInMS {
			return fmt.Errorf("%w: event %d source range", ErrInvalidAudioPlan, i)
		}
	}
	for _, layer := range append(append([]AudioLayer{}, p.BackgroundMusic...), p.SFX...) {
		if strings.TrimSpace(layer.AssetID) == "" || layer.TimelineStartMS < 0 || layer.DurationMS <= 0 || layer.TimelineStartMS+layer.DurationMS > p.DurationMS {
			return fmt.Errorf("%w: layer range or asset", ErrInvalidAudioPlan)
		}
	}
	for _, automation := range p.Automation {
		if strings.TrimSpace(automation.TargetLayer) == "" || automation.StartMS < 0 || automation.EndMS <= automation.StartMS || automation.EndMS > p.DurationMS || automation.AttackMS < 0 || automation.ReleaseMS < 0 {
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
