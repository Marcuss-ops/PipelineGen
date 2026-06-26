// Package types holds the canonical FFmpeg option types: NormalizeOptions,
// CutJob, WatermarkOptions, and CutAndNormalizeOptions. These were moved out
// of the top-level ffmpeg package (PR6-B, June 2026) so the parent package
// stays focused on Processor orchestration. The parent package exports
// type aliases for backward compatibility.
package types

import (
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ── Option types ────────────────────────────────────────────────────────

// NormalizeOptions configures video normalization.
type NormalizeOptions struct {
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

// CutAndNormalizeOptions combines cut boundaries with normalization parameters.
type CutAndNormalizeOptions struct {
	Width   int
	Height  int
	FPS     int
	Codec   string
	Preset  string
	CRF     int
	NoAudio bool
}

// CutJob defines a single clip to extract from a source video.
type CutJob struct {
	StartSec float64
	EndSec   float64
	Output   string
}

// WatermarkOptions configures how a watermark overlay is applied to a video.
type WatermarkOptions struct {
	ImagePath             string
	Opacity               float64
	Position              string
	ScalePercent          int
	GreenScreenColor      string
	GreenScreenSimilarity float64
	GreenScreenBlend      float64
}

// ── Default helpers ─────────────────────────────────────────────────────

// DefaultNormalizeOptions returns defaults from config.
func DefaultNormalizeOptions(cfg *config.Config) NormalizeOptions {
	v := cfg.Video.WithDefaults()
	return NormalizeOptions{
		Duration:         v.Duration,
		Width:            v.Width,
		Height:           v.Height,
		FPS:              v.FPS,
		Codec:            v.Codec,
		Preset:           v.Preset,
		CRF:              v.CRF,
		KeyframeInterval: v.KeyframeInterval,
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
