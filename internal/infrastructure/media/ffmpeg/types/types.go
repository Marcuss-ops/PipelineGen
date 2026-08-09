// Package types holds the canonical FFmpeg option types: NormalizeOptions,
// CutJob, WatermarkOptions, and CutAndNormalizeOptions. These were moved out
// of the top-level ffmpeg package (PR6-B, June 2026) so the parent package
// stays focused on Processor orchestration. The parent package exports
// type aliases for backward compatibility.
package types

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ── Option types ────────────────────────────────────────────────────────

// NormalizeOptions configures video normalization.
type NormalizeOptions = mediaexec.NormalizeOptions

/*
	type NormalizeOptions struct {
	// Profile and Policy are the canonical separated contracts. The legacy
	// scalar fields below remain source-compatible with existing callers.
	Profile config.CanonicalVideoProfile
	Policy  config.VideoEncoderPolicy

	Duration         int  // Max duration in seconds (0 = no limit)
	DisableDuration  bool // If true, ignore Duration even if > 0
	KeepAudio        bool // If true, do not strip audio
	Width            int
	Height           int
	FPS              int
	Codec            string
	Preset           string
	CRF              int
	KeyframeInterval int // GOP size (keyframe interval, 0 = default)
}
*/

// CutAndNormalizeOptions combines cut boundaries with normalization parameters.
type CutAndNormalizeOptions = mediaexec.CutAndNormalizeOptions

/*
	type CutAndNormalizeOptions struct {
	// Profile and Policy are the canonical separated contracts. The legacy
	// scalar fields remain source-compatible with existing callers.
	Profile config.CanonicalVideoProfile
	Policy  config.VideoEncoderPolicy

	Width   int
	Height  int
	FPS     int
	Codec   string
	Preset  string
	CRF     int
	NoAudio bool
}
*/

// CutJob defines a single clip to extract from a source video.
type CutJob struct {
	StartSec float64
	EndSec   float64
	Output   string
}

// WatermarkOptions configures how a watermark overlay is applied to a video.
type WatermarkOptions = mediaexec.WatermarkOptions

/*
	type WatermarkOptions struct {
	ImagePath             string
	Opacity               float64
	Position              string
	ScalePercent          int
	GreenScreenColor      string
	GreenScreenSimilarity float64
	GreenScreenBlend      float64
}
*/

// ── Default helpers ─────────────────────────────────────────────────────

// DefaultNormalizeOptions returns defaults from config.
func DefaultNormalizeOptions(cfg *config.Config) NormalizeOptions {
	if cfg == nil {
		cfg = &config.Config{}
	}
	canonical := cfg.Video.CanonicalClip()
	profile := cfg.Video.CanonicalVideoProfile()
	// CanonicalClip deliberately preserves an empty Codec so the FFmpeg
	// processor can resolve the host's configured encoder (for example,
	// h264_nvenc). Do not use EncoderPolicy here: its legacy defaults would
	// materialize libx264 before the runtime encoder resolver gets a chance.
	policy := config.VideoEncoderPolicy{
		Codec:  canonical.Codec,
		Preset: canonical.Preset,
		CRF:    canonical.CRF,
	}
	return NormalizeOptions{
		Profile:          profile,
		Policy:           policy,
		Duration:         canonical.Duration,
		Width:            profile.Width,
		Height:           profile.Height,
		FPS:              profile.FPS,
		Codec:            policy.Codec,
		Preset:           policy.Preset,
		CRF:              policy.CRF,
		KeyframeInterval: profile.KeyframeInterval,
	}
}

// DefaultWatermarkOptions returns sensible defaults for watermark overlay.
func DefaultWatermarkOptions(imagePath string) WatermarkOptions {
	return WatermarkOptions{
		ImagePath:             imagePath,
		Opacity:               0.25,
		Position:              "center",
		ScalePercent:          20,
		GreenScreenColor:      "0x00FF00",
		GreenScreenSimilarity: 0.3,
		GreenScreenBlend:      0.1,
	}
}
