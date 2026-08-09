// Package mediaexec contains capability-neutral media execution contracts.
// It deliberately has no FFmpeg implementation or process lifecycle code.
package mediaexec

import (
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

type NormalizeOptions struct {
	Profile               config.CanonicalVideoProfile
	Policy                config.VideoEncoderPolicy
	Duration              int
	DisableDuration       bool
	KeepAudio             bool
	Width, Height, FPS    int
	Codec, Preset         string
	CRF, KeyframeInterval int
}

type CutAndNormalizeOptions struct {
	Profile            config.CanonicalVideoProfile
	Policy             config.VideoEncoderPolicy
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
}
