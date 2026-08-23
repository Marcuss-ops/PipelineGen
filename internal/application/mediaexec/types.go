// Package mediaexec contains capability-neutral media execution contracts.
// It deliberately has no FFmpeg implementation or process lifecycle code.
package mediaexec

import (
	"context"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// VideoProfile describes the fully resolved video artifact independently of
// configuration, transport, or encoder implementation.
// FPS is deprecated: use FPSNum/FPSDen rational. FPS remains as legacy projection.
type VideoProfile struct {
	Width            int
	Height           int
	FPS              int `json:"fps"` // deprecated: use FPSNum/FPSDen
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
	if p.FPS > 0 {
		return p.FPS, 1
	}
	return 24, 1
}

func (p VideoProfile) FPSFloat() float64 {
	n, d := p.FrameRate()
	return float64(n) / float64(d)
}

// WithDefaults makes a partially populated profile safe for direct consumers.
func (p VideoProfile) WithDefaults() VideoProfile {
	if p.Width <= 0 {
		p.Width = 1920
	}
	if p.Height <= 0 {
		p.Height = 1080
	}
	if p.FPSNum <= 0 || p.FPSDen <= 0 {
		if p.FPS > 0 {
			p.FPSNum = p.FPS
			p.FPSDen = 1
		} else {
			p.FPSNum = 24
			p.FPSDen = 1
		}
	}
	if p.FPS <= 0 {
		p.FPS = p.FPSNum / p.FPSDen
		if p.FPS <= 0 {
			p.FPS = 24
		}
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

// AudioProcessor exposes media audio execution without naming an implementation.
// Implementations belong to infrastructure adapters such as rustexec.
type AudioProcessor interface {
	MergeInputs(context.Context, []string, string) error
	RemoveSilence(context.Context, string, string) error
	Probe(context.Context, string) (*MediaInfo, error)
	RenderAudioPlan(context.Context, audio.CompiledAudioPlan, audio.ResolvedAudioAssets, string) (audio.FinalAudioAsset, error)
}

type NormalizeOptions struct {
	// Profile and Policy are the canonical representations. The scalar fields
	// remain for source compatibility with legacy callers; adapters use them
	// only as fallback overrides when the canonical values are incomplete.
	Profile VideoProfile
	Policy  EncoderPolicy

	Duration              int
	DisableDuration       bool
	KeepAudio             bool
	Width, Height, FPS    int
	Codec, Preset         string
	CRF, KeyframeInterval int
}

type CutAndNormalizeOptions struct {
	Profile VideoProfile
	Policy  EncoderPolicy

	Width, Height, FPS int
	Codec, Preset      string
	CRF                int
	NoAudio            bool
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
