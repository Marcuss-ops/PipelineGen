package ffmpeg

import (
	"context"
	"fmt"
	"strings"
	"time"

	"velox/go-master/pkg/config"
	"velox/go-master/pkg/executil"
)

// Processor handles FFmpeg operations.
type Processor struct {
	path string
}

// New creates a new FFmpeg processor.
func New(cfg *config.Config) *Processor {
	path := cfg.External.FfmpegPath
	if path == "" {
		path = "ffmpeg"
	}
	return &Processor{path: path}
}

// NormalizeOptions configures video normalization.
type NormalizeOptions struct {
	Duration int    // Max duration in seconds (0 = no limit)
	Width    int
	Height   int
	FPS      int
	Codec    string
	Preset   string
	CRF      int
}

// DefaultNormalizeOptions returns defaults from config.
func DefaultNormalizeOptions(cfg *config.Config) NormalizeOptions {
	v := cfg.Video.WithDefaults()
	return NormalizeOptions{
		Duration: v.Duration,
		Width:    v.Width,
		Height:   v.Height,
		FPS:      v.FPS,
		Codec:    v.Codec,
		Preset:   v.Preset,
		CRF:      v.CRF,
	}
}

// Normalize processes a video to standard format (scale, crop, fps, codec).
func (p *Processor) Normalize(ctx context.Context, input, output string, opts NormalizeOptions) error {
	args := []string{"-y"}

	if opts.Duration > 0 {
		args = append(args, "-t", fmt.Sprintf("%d", opts.Duration))
	}

	args = append(args,
		"-i", input,
		"-vf", fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d,fps=%d",
			opts.Width, opts.Height, opts.Width, opts.Height, opts.FPS),
		"-an",
		"-c:v", opts.Codec,
		"-preset", opts.Preset,
		"-crf", fmt.Sprintf("%d", opts.CRF),
		output,
	)

	_, err := executil.Run(ctx, p.path, args, executil.Options{
		Timeout: 10 * time.Minute,
	})
	return err
}

// CutOptions configures segment cutting.
type CutOptions struct {
	Codec  string
	Preset string
	CRF    int
}

// CutSegment cuts a segment from input video and saves to output.
// start and end are timestamps like "00:01:23" or "01:23".
func (p *Processor) CutSegment(ctx context.Context, input, output string, start, end string, opts CutOptions) error {
	args := []string{"-y"}

	if start != "" {
		args = append(args, "-ss", start)
	}
	args = append(args, "-i", input)
	if end != "" {
		args = append(args, "-to", end)
	}

	if opts.Codec != "" {
		args = append(args, "-c:v", opts.Codec)
	}
	if opts.Preset != "" {
		args = append(args, "-preset", opts.Preset)
	}
	if opts.CRF > 0 {
		args = append(args, "-crf", fmt.Sprintf("%d", opts.CRF))
	}

	args = append(args, output)

	_, err := executil.Run(ctx, p.path, args, executil.Options{
		Timeout: 10 * time.Minute,
	})
	return err
}

// MediaInfo holds probed media information.
type MediaInfo struct {
	Duration string `json:"duration,omitempty"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
	FPS      string `json:"fps,omitempty"`
	Codec    string `json:"codec,omitempty"`
}

// Probe retrieves media information using ffprobe.
func Probe(ctx context.Context, path string) (*MediaInfo, error) {
	ffprobe := "ffprobe"
	args := []string{
		"-v", "error",
		"-show_entries", "format=duration:stream=width,height,r_frame_rate,codec_name",
		"-of", "json",
		path,
	}

	result, err := executil.Run(ctx, ffprobe, args, executil.Options{
		Timeout: 30 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}

	// Parse the JSON output (simplified - in production use encoding/json)
	// For now, return basic info
	_ = result
	return &MediaInfo{}, nil
}

// MergeInputs concatenates multiple video files into one.
func (p *Processor) MergeInputs(ctx context.Context, inputs []string, output string) error {
	// Create a temporary file list for ffmpeg concat demuxer
	// Then run ffmpeg -f concat -safe 0 -i list.txt -c copy output.mp4
	// Implementation left for when needed
	return fmt.Errorf("not implemented")
}

// Check checks if FFmpeg is available.
func (p *Processor) Check() bool {
	return executil.CommandExists(p.path)
}

// Version returns the FFmpeg version.
func (p *Processor) Version(ctx context.Context) (string, error) {
	result, err := executil.Run(ctx, p.path, []string{"-version"}, executil.Options{
		Timeout: 10 * time.Second,
	})
	if err != nil {
		return "", err
	}
	lines := strings.Split(result.Output, "\n")
	if len(lines) > 0 {
		return lines[0], nil
	}
	return "", nil
}
