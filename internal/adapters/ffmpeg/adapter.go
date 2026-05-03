// Package ffmpeg defines adapter interfaces for FFmpeg operations.
//
// STATUS: EXPERIMENTAL - Interface defined but not yet implemented or used.
// TODO: Implement and migrate media processing to use this adapter.
package ffmpeg

import "context"

type ProcessClipInput struct {
	InputPath  string
	OutputPath string
	Duration   int
	Width      int
	Height     int
	FPS        int
}

type ProcessClipResult struct {
	OutputPath string
	Duration   float64
}

type MediaInfo struct {
	Duration float64
	Width    int
	Height   int
	FPS      float64
	Codec    string
}

type FFmpegAdapter interface {
	ProcessClip(ctx context.Context, input ProcessClipInput) (*ProcessClipResult, error)
	Probe(ctx context.Context, path string) (*MediaInfo, error)
}
