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
	n, d := p.FrameRate()
	return VideoContract{
		ID:                 AssemblyReadyVideoContractID,
		Version:            AssemblyReadyVideoVersion,
		Container:          "mp4",
		VideoCodec:         "h264",
		VideoProfile:       "high",
		VideoLevel:         "4.0",
		PixelFormat:        "yuv420p",
		Width:              p.Width,
		Height:             p.Height,
		FPS:                FrameRate{Num: n, Den: d},
		VideoTimeBase:      Rational{Num: 1, Den: 90000},
		AudioTimeBase:      Rational{Num: 1, Den: 48000},
		SAR:                Rational{Num: 1, Den: 1},
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

// WithDefaults makes a partially populated profile safe for direct consumers.
func (p VideoProfile) WithDefaults() VideoProfile {
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
