package config

import (
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"

	"github.com/Marcuss-ops/PipelineGen/pkg/defaults"
)

// CanonicalVideoProfile describes the materialized video artifact. It contains
// no encoder choice: the same profile can be produced by libx264 or NVENC.
// SSOT: kernel/media.VideoContract (AssemblyReadyVideoContractID). This struct is the
// platform-level working copy; frozen values live in kernel/media.DefaultAssemblyReadyVideoContract().
type CanonicalVideoProfile struct {
	Width            int
	Height           int
	FPSNum           int `json:"fps_num"`
	FPSDen           int `json:"fps_den"`
	KeyframeInterval int
	AudioCodec       string
	AudioBitrate     string
	SampleRate       int
	Channels         int
}

// FrameRate returns the rational FPS. Canonical is 24/1.
func (p CanonicalVideoProfile) FrameRate() (num, den int) {
	if p.FPSNum > 0 && p.FPSDen > 0 {
		return p.FPSNum, p.FPSDen
	}
	return 24, 1
}

// FPSFloat is projection for logs/UI only.
func (p CanonicalVideoProfile) FPSFloat() float64 {
	n, d := p.FrameRate()
	return float64(n) / float64(d)
}

// ToVideoContract uplifts this platform profile to the canonical SSOT.
func (p CanonicalVideoProfile) ToVideoContract() kernelmedia.VideoContract {
	n, d := p.FrameRate()
	return kernelmedia.VideoContract{
		ID:                 kernelmedia.AssemblyReadyVideoContractID,
		Version:            kernelmedia.AssemblyReadyVideoVersion,
		Container:          "mp4",
		VideoCodec:         "h264",
		VideoProfile:       "high",
		VideoLevel:         "4.0",
		PixelFormat:        "yuv420p",
		Width:              p.Width,
		Height:             p.Height,
		FPS:                kernelmedia.FrameRate{Num: n, Den: d},
		VideoTimeBase:      kernelmedia.Rational{Num: 1, Den: 90000},
		AudioTimeBase:      kernelmedia.Rational{Num: 1, Den: 48000},
		SAR:                kernelmedia.Rational{Num: 1, Den: 1},
		ColorRange:         "tv",
		ColorSpace:         "bt709",
		ColorTransfer:      "bt709",
		ColorPrimaries:     "bt709",
		FieldOrder:         "progressive",
		KeyframeInterval:   p.KeyframeInterval,
		AudioCodec:         p.AudioCodec,
		AudioProfile:       "LC",
		AudioSampleRate:    p.SampleRate,
		AudioChannels:      p.Channels,
		AudioChannelLayout: "stereo",
		AudioBitrate:       p.AudioBitrate,
		VideoStreams:       1,
		AudioStreams:       1,
		StartPTS:           0,
	}
}

// WithDefaults makes partially populated profiles safe for direct consumers.
func (p CanonicalVideoProfile) WithDefaults() CanonicalVideoProfile {
	if p.Width <= 0 {
		p.Width = 1920
	}
	if p.Height <= 0 {
		p.Height = 1080
	}
	if p.FPSNum <= 0 || p.FPSDen <= 0 {
		p.FPSNum = 24
		p.FPSDen = 1
	}
	if p.KeyframeInterval <= 0 {
		p.KeyframeInterval = 48
	}
	if p.AudioCodec == "" {
		p.AudioCodec = "aac"
	}
	if p.AudioBitrate == "" {
		p.AudioBitrate = "128k"
	}
	if p.SampleRate <= 0 {
		p.SampleRate = 48000
	}
	if p.Channels <= 0 {
		p.Channels = 2
	}
	return p
}

// Canonical NVENC encoder names used by capability probes and the encoder
// resolver. These strings must exactly match the ffmpeg encoder listing
// format; they are the single source of truth for NVENC detection tokens.
const (
	EncoderNVENCH264 = "h264_nvenc"
	EncoderNVENCHEVC = "hevc_nvenc"
)

// VideoEncoderPolicy describes how a canonical video profile is encoded.
type VideoEncoderPolicy struct {
	Codec  string
	Preset string
	CRF    int
}

// VideoConfig holds all video processing parameters shared across the clip, stock,
// and video rendering pipelines. Centralizing these values ensures that every
// stage uses the same codec, resolution, and preset so that ffmpeg can perform
// fast stream-copy concatenation without re-encoding.
//
// HC-7 (June 2026): ChunkDuration consumes defaults.DefaultVideoConfig() instead
// of a hard-coded literal 25. The pkg/defaults/video.go SSOT is the sole owner
// of the canonical value; re-introduction is gated by Check 39 in
// scripts/ci-architectural-checks.sh.
type VideoConfig struct {
	Width  int `yaml:"width" default:"1920"`
	Height int `yaml:"height" default:"1080"`
	FPSNum int `yaml:"fps_num" default:"24"`
	FPSDen int `yaml:"fps_den" default:"1"`
	// Codec, Preset and CRF are encoder policy. They are deliberately kept
	// separate from CanonicalVideoProfile, which describes only the artifact.
	Codec              string   `yaml:"codec" default:"libx264"`
	Preset             string   `yaml:"preset" default:"veryfast"`
	CRF                int      `yaml:"crf" default:"23"`
	Duration           int      `yaml:"duration" default:"7"`
	KeyframeInterval   int      `yaml:"keyframe_interval" default:"48"`
	AudioCodec         string   `yaml:"audio_codec" default:"aac"`
	AudioBitrate       string   `yaml:"audio_bitrate" default:"128k"`
	SampleRate         int      `yaml:"sample_rate" default:"48000"`
	Channels           int      `yaml:"channels" default:"2"`
	ClipDuration       int      `yaml:"clip_duration" default:"5"`
	ChunkDuration      int      `yaml:"chunk_duration" default:"25"`
	MaxClipsPerSource  int      `yaml:"max_clips_per_source" default:"30"`
	SearchCount        int      `yaml:"search_count" default:"25"`
	OverlayOpacity     float64  `yaml:"overlay_opacity" default:"0.25"`
	EffectInterval     int      `yaml:"effect_interval" default:"4"`
	TransitionInterval int      `yaml:"transition_interval" default:"4"`
	TransitionPresets  []string `yaml:"transition_presets"`
}

// WithDefaults returns a copy of VideoConfig with zero-values replaced by defaults.
func (v VideoConfig) WithDefaults() VideoConfig {
	if v.Width <= 0 {
		v.Width = 1920
	}
	if v.Height <= 0 {
		v.Height = 1080
	}
	if v.FPSNum <= 0 || v.FPSDen <= 0 {
		v.FPSNum = 24
		v.FPSDen = 1
	}
	if v.Duration <= 0 {
		v.Duration = 7
	}
	if v.Codec == "" {
		v.Codec = "libx264"
	}
	if v.Preset == "" {
		v.Preset = "veryfast"
	}
	if v.CRF <= 0 {
		v.CRF = 23
	}
	if v.KeyframeInterval <= 0 {
		v.KeyframeInterval = 48
	}
	if v.AudioCodec == "" {
		v.AudioCodec = "aac"
	}
	if v.AudioBitrate == "" {
		v.AudioBitrate = "128k"
	}
	if v.SampleRate <= 0 {
		v.SampleRate = 48000
	}
	if v.Channels <= 0 {
		v.Channels = 2
	}
	if v.ClipDuration <= 0 {
		v.ClipDuration = 5
	}
	if v.ChunkDuration <= 0 {
		v.ChunkDuration = defaults.DefaultVideoConfig().ChunkDuration
	}
	if v.MaxClipsPerSource <= 0 {
		v.MaxClipsPerSource = 30
	}
	if v.SearchCount <= 0 {
		v.SearchCount = 25
	}
	// Note: OverlayOpacity == 0 means fully transparent (no overlay); < 0 means use default.
	if v.OverlayOpacity < 0 {
		v.OverlayOpacity = 0.25
	}
	// Note: EffectInterval == 0 means no effects; < 0 means use default.
	if v.EffectInterval < 0 {
		v.EffectInterval = 4
	}
	// Note: TransitionInterval == 0 means no transitions; < 0 means use default.
	if v.TransitionInterval < 0 {
		v.TransitionInterval = 4
	}
	if len(v.TransitionPresets) == 0 {
		v.TransitionPresets = []string{
			"fade", "fadeblack", "fadewhite",
			"slideleft", "slideright", "slideup", "slidedown",
			"circleclose", "circleopen",
			"horzclose", "horzopen", "vertclose", "vertopen",
			"dissolve", "pixelize",
			"wipeleft", "wiperight", "wipeup", "wipedown",
			"smoothleft", "smoothright", "smoothup", "smoothdown",
			"radial", "hblur", "fadegrays",
			"squeezeh", "squeezev",
		}
	}
	return v
}

// CanonicalVideoProfile materializes the fully resolved technical artifact
// profile owned by Go. Rust receives this value and must not fill missing fields.
func (v VideoConfig) CanonicalVideoProfile() CanonicalVideoProfile {
	v = v.WithDefaults()
	return CanonicalVideoProfile{
		Width:            v.Width,
		Height:           v.Height,
		FPSNum:           v.FPSNum,
		FPSDen:           v.FPSDen,
		KeyframeInterval: v.KeyframeInterval,
		AudioCodec:       v.AudioCodec,
		AudioBitrate:     v.AudioBitrate,
		SampleRate:       v.SampleRate,
		Channels:         v.Channels,
	}
}

// EncoderPolicy returns the configured runtime encoding policy with defaults.
func (v VideoConfig) EncoderPolicy() VideoEncoderPolicy {
	v = v.WithDefaults()
	return VideoEncoderPolicy{Codec: v.Codec, Preset: v.Preset, CRF: v.CRF}
}

// CanonicalClip is retained as a source-compatible legacy composite. New
// infrastructure code must use CanonicalVideoProfile and EncoderPolicy
// separately; this method remains for callers that still expect a VideoConfig.
//
// The encoder policy is intentionally preserved verbatim here. In particular,
// an empty Codec must remain empty so the caller or infrastructure resolver can
// choose the runtime encoder; CanonicalClip must never silently turn it into
// libx264 while materializing the artifact profile. Other policy fields retain
// their safe defaults unless explicitly configured.
func (v VideoConfig) CanonicalClip() VideoConfig {
	codec := v.Codec
	v = v.WithDefaults()
	v.Codec = codec

	profile := v.CanonicalVideoProfile()
	v.Width = profile.Width
	v.Height = profile.Height
	v.FPSNum = profile.FPSNum
	v.FPSDen = profile.FPSDen
	v.KeyframeInterval = profile.KeyframeInterval
	v.AudioCodec = profile.AudioCodec
	v.AudioBitrate = profile.AudioBitrate
	v.SampleRate = profile.SampleRate
	v.Channels = profile.Channels
	return v
}
