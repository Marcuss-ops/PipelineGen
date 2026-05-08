// Package ffmpeg provides FFmpeg operations for video processing.
//
// STATUS: ACTIVE - This package is actively used by mediaasset.Processor and mediapipeline.
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
	Duration        int  // Max duration in seconds (0 = no limit)
	DisableDuration bool // If true, ignore Duration even if > 0
	KeepAudio       bool // If true, do not strip audio
	Width           int
	Height          int
	FPS             int
	Codec           string
	Preset          string
	CRF             int
	KeyframeInterval int  // GOP size (keyframe interval, 0 = default)
}

// DefaultNormalizeOptions returns defaults from config.
func DefaultNormalizeOptions(cfg *config.Config) NormalizeOptions {
	v := cfg.Video.WithDefaults()
	return NormalizeOptions{
		Duration:        v.Duration,
		Width:           v.Width,
		Height:          v.Height,
		FPS:             v.FPS,
		Codec:           v.Codec,
		Preset:          v.Preset,
		CRF:             v.CRF,
		KeyframeInterval: v.KeyframeInterval,
	}
}

// RemuxHLS downloads an HLS playlist and remuxes it into an MP4 container
// without re-encoding. It is intended for already-resolved .m3u8 media URLs.
func (p *Processor) RemuxHLS(ctx context.Context, inputURL, output string) error {
	args := []string{
		"-y",
		"-hide_banner",
		"-loglevel", "warning",
		"-protocol_whitelist", "file,http,https,tcp,tls,crypto",
		"-i", inputURL,
		"-c", "copy",
		"-bsf:a", "aac_adtstoasc",
		"-movflags", "+faststart",
		output,
	}

	_, err := executil.Run(ctx, p.path, args, executil.Options{
		Timeout: 15 * time.Minute,
	})
	return err
}

// Normalize processes a video to standard format (scale, crop, fps, codec).
func (p *Processor) Normalize(ctx context.Context, input, output string, opts NormalizeOptions) error {
	args := []string{
		"-y",
		"-hide_banner",
		"-loglevel", "warning",
		"-threads", "1",
		"-filter_threads", "1",
		"-filter_complex_threads", "1",
	}

	// Generate new PTS to fix timestamp issues
	args = append(args, "-fflags", "+genpts")
	
	// Avoid negative timestamps
	args = append(args, "-avoid_negative_ts", "make_zero")

	if opts.Duration > 0 && !opts.DisableDuration {
		args = append(args, "-t", fmt.Sprintf("%d", opts.Duration))
	}

	args = append(args, "-i", input)

	// Filter chain with PTS reset to ensure monotonic timestamps
	filter := fmt.Sprintf(
		"scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=%d,setpts=PTS-STARTPTS",
		opts.Width, opts.Height, opts.Width, opts.Height, opts.FPS,
	)
	args = append(args, "-vf", filter)

	if !opts.KeepAudio {
		args = append(args, "-an")
	} else {
		// If keeping audio, we should probably encode it to a standard codec like AAC
		args = append(args, "-c:a", "aac", "-b:a", "128k")
		// Also reset audio timestamps
		args = append(args, "-af", "asetpts=PTS-STARTPTS")
	}

	// Video codec settings
	args = append(args, "-c:v", opts.Codec)
	
	// Keyframe settings to prevent frame issues
	keyframeInterval := opts.KeyframeInterval
	if keyframeInterval <= 0 {
		keyframeInterval = opts.FPS * 2 // Default: 2 seconds GOP
	}
	args = append(args, "-g", fmt.Sprintf("%d", keyframeInterval))
	args = append(args, "-keyint_min", fmt.Sprintf("%d", keyframeInterval/2))
	args = append(args, "-sc_threshold", "0") // Disable scene change detection
	args = append(args, "-bf", "0")      // Disable B-frames for simpler decoding
	args = append(args, "-refs", "1")     // Single reference frame
	
	// Rate control
	args = append(args, "-preset", opts.Preset)
	args = append(args, "-crf", fmt.Sprintf("%d", opts.CRF))
	
	// Output flags for better compatibility
	args = append(args, "-pix_fmt", "yuv420p") // Standard pixel format
	args = append(args, "-movflags", "+faststart")
	args = append(args, "-vsync", "cfr") // Constant frame rate
	args = append(args, output)

	_, err := executil.Run(ctx, p.path, args, executil.Options{
		Timeout: 15 * time.Minute,
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
