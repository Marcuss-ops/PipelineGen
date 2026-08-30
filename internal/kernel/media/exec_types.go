// Package media — exec_types.go defines capability-neutral media execution
// contracts (encoder policy, video profile, media info). These types are
// pure data contracts; no FFmpeg, process, or transport dependencies.
package media

import "time"

// VideoProfile describes the fully resolved video artifact independently of
// configuration, transport, or encoder implementation.
// SSOT: kernel/media.VideoContract (AssemblyReadyVideoContractID). This struct is the
// media-execution-level working copy; frozen values live in kernel/media.DefaultAssemblyReadyVideoContract().
type VideoProfile struct {
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

// FrameRate returns rational FPS. Canonical 24/1.
func (p VideoProfile) FrameRate() (num, den int) {
	if p.FPSNum > 0 && p.FPSDen > 0 {
		return p.FPSNum, p.FPSDen
	}
	return 24, 1
}

func (p VideoProfile) FPSFloat() float64 {
	n, d := p.FrameRate()
	return float64(n) / float64(d)
}

// ToVideoContract uplifts this media-execution profile to the canonical SSOT.
func (p VideoProfile) ToVideoContract() VideoContract {
	p = p.WithDefaults()
	c := DefaultAssemblyMediaContractV2()
	c.Width = p.Width
	c.Height = p.Height
	c.FPS = FrameRate{Num: p.FPSNum, Den: p.FPSDen}
	c.KeyframeInterval = p.KeyframeInterval
	return c
}

// WithDefaults makes a partially populated profile safe for direct consumers.
// Defaults are projected from the canonical V2 assembly contract so the
// frozen values stay owned by kernel/media (single source of truth).
func (p VideoProfile) WithDefaults() VideoProfile {
	c := DefaultAssemblyMediaContractV2()
	if p.Width <= 0 {
		p.Width = c.Width
	}
	if p.Height <= 0 {
		p.Height = c.Height
	}
	if p.FPSNum <= 0 || p.FPSDen <= 0 {
		p.FPSNum = c.FPS.Num
		p.FPSDen = c.FPS.Den
	}
	if p.KeyframeInterval <= 0 {
		p.KeyframeInterval = c.KeyframeInterval
	}
	if p.AudioCodec == "" {
		p.AudioCodec = c.AudioCodec
	}
	if p.AudioBitrate == "" {
		p.AudioBitrate = c.AudioBitrate
	}
	if p.SampleRate <= 0 {
		p.SampleRate = c.AudioSampleRate
	}
	if p.Channels <= 0 {
		p.Channels = c.AudioChannels
	}
	return p
}

// EncoderPolicy describes how a VideoProfile is encoded.
type EncoderPolicy struct {
	Codec  string
	Preset string
	CRF    int
}

// ExecutionConfig is the resolved media configuration passed from the
// composition root to media capabilities. Platform configuration is mapped
// into this contract once; adapters do not read platform/config themselves.
type ExecutionConfig struct {
	Profile VideoProfile
	Policy  EncoderPolicy
}

type NormalizeOptions struct {
	Profile VideoProfile
	Policy  EncoderPolicy

	Duration              int
	DisableDuration       bool
	KeepAudio             bool
	Width, Height         int
	FPSNum, FPSDen        int
	Codec, Preset         string
	CRF, KeyframeInterval int
}

type CutAndNormalizeOptions struct {
	Profile VideoProfile
	Policy  EncoderPolicy

	Width, Height  int
	FPSNum, FPSDen int
	Codec, Preset  string
	CRF            int
	NoAudio        bool
}

type WatermarkOptions struct {
	ImagePath             string
	Opacity               float64
	Position              string
	ScalePercent          int
	GreenScreenColor      string
	GreenScreenSimilarity float64
	GreenScreenBlend      float64
}

type MediaInfo struct {
	Duration                      time.Duration
	Width, Height                 int
	FPS                           float64
	BitRate                       int64
	VideoCodec, AudioCodec        string
	SampleRate, Channels          int
	HasVideo, HasAudio            bool
	PixelFormat, FormatName       string
	VideoStreamCount, StreamCount int
	AudioStreamCount              int
	FPSNum, FPSDen                int
	AudioProfile                  string
}
