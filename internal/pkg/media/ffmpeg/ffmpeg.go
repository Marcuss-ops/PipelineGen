// Package ffmpeg provides FFmpeg operations for video processing.
//
// STATUS: ACTIVE - This package is actively used by mediaasset.Processor and mediapipeline.
package ffmpeg

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"velox/go-master/internal/config"
	"velox/go-master/internal/pkg/executil"
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
	}

	// Use hardware acceleration if NVENC is requested
	if strings.Contains(opts.Codec, "nvenc") {
		// Use CUDA for decoding if available
		args = append(args, "-hwaccel", "cuda")
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
	// Hardware acceleration note: scaling on CPU for now to keep it stable across platforms,
	// but using NVENC for the heavy lifting (encoding).
	filter := fmt.Sprintf(
		"scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=%d,setpts=PTS-STARTPTS",
		opts.Width, opts.Height, opts.Width, opts.Height, opts.FPS,
	)
	args = append(args, "-vf", filter)

	if !opts.KeepAudio {
		args = append(args, "-an")
	} else {
		args = append(args, "-c:a", "aac", "-b:a", "128k")
		args = append(args, "-af", "asetpts=PTS-STARTPTS")
	}

	// Video codec settings
	args = append(args, "-c:v", opts.Codec)

	// Keyframe settings
	keyframeInterval := opts.KeyframeInterval
	if keyframeInterval <= 0 {
		keyframeInterval = opts.FPS * 2
	}
	args = append(args, "-g", fmt.Sprintf("%d", keyframeInterval))

	// NVENC specific optimizations
	if strings.Contains(opts.Codec, "nvenc") {
		// P1 is the fastest preset for NVENC
		preset := opts.Preset
		if preset == "fast" || preset == "" {
			preset = "p1"
		}
		args = append(args, "-preset", preset)
		args = append(args, "-rc", "vbr")
		args = append(args, "-cq", fmt.Sprintf("%d", opts.CRF))
		args = append(args, "-tune", "hq")
		args = append(args, "-bf", "0")
	} else {
		args = append(args, "-preset", opts.Preset)
		args = append(args, "-crf", fmt.Sprintf("%d", opts.CRF))
		args = append(args, "-bf", "0")
		args = append(args, "-refs", "1")
	}

	args = append(args, "-pix_fmt", "yuv420p")
	args = append(args, "-movflags", "+faststart")
	args = append(args, "-vsync", "cfr")
	args = append(args, output)

	_, err := executil.Run(ctx, p.path, args, executil.Options{
		Timeout: 15 * time.Minute,
	})
	return err
}

// CutOptions configures segment cutting.
type CutOptions struct {
	Codec   string
	Preset  string
	CRF     int
	NoAudio bool
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

	if opts.NoAudio {
		args = append(args, "-an")
	}

	args = append(args, output)

	_, err := executil.Run(ctx, p.path, args, executil.Options{
		Timeout: 10 * time.Minute,
	})
	return err
}

// CutAndNormalizeOptions configures combined cutting + normalization.
type CutAndNormalizeOptions struct {
	Width   int
	Height  int
	FPS     int
	Codec   string
	Preset  string
	CRF     int
	NoAudio bool
}

// CutAndNormalize cuts a segment and normalizes it in a single ffmpeg pass,
// avoiding a double re-encode. Combines CutSegment + Normalize.
func (p *Processor) CutAndNormalize(ctx context.Context, input, output, start, end string, opts CutAndNormalizeOptions) error {
	args := []string{
		"-y", "-hide_banner", "-loglevel", "warning",
	}

	if strings.Contains(opts.Codec, "nvenc") {
		args = append(args, "-hwaccel", "cuda")
	}

	if start != "" {
		args = append(args, "-ss", start)
	}
	args = append(args, "-i", input)
	if end != "" {
		args = append(args, "-to", end)
	}

	filter := fmt.Sprintf(
		"scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=%d,setpts=PTS-STARTPTS",
		opts.Width, opts.Height, opts.Width, opts.Height, opts.FPS,
	)
	args = append(args, "-vf", filter)

	if opts.NoAudio {
		args = append(args, "-an")
	}

	args = append(args, "-c:v", opts.Codec)
	args = append(args, "-preset", opts.Preset)
	args = append(args, "-g", fmt.Sprintf("%d", opts.FPS*2))

	if strings.Contains(opts.Codec, "nvenc") {
		args = append(args, "-rc", "vbr")
		args = append(args, "-cq", fmt.Sprintf("%d", opts.CRF))
		args = append(args, "-tune", "hq")
		args = append(args, "-bf", "0")
	} else {
		args = append(args, "-crf", fmt.Sprintf("%d", opts.CRF))
		args = append(args, "-bf", "0")
		args = append(args, "-refs", "1")
	}

	args = append(args, "-pix_fmt", "yuv420p", "-movflags", "+faststart", "-vsync", "cfr")
	args = append(args, output)

	_, err := executil.Run(ctx, p.path, args, executil.Options{
		Timeout: 10 * time.Minute,
	})
	return err
}

// MediaInfo holds probed media information.
type MediaInfo struct {
	Duration float64 `json:"duration,omitempty"`
	Width    int     `json:"width,omitempty"`
	Height   int     `json:"height,omitempty"`
	FPS      float64 `json:"fps,omitempty"`
	Codec    string  `json:"codec,omitempty"`
}

// Probe retrieves media information using ffprobe.
func (p *Processor) Probe(ctx context.Context, path string) (*MediaInfo, error) {
	args := []string{
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height,r_frame_rate,codec_name,duration",
		"-of", "json",
		path,
	}

	result, err := executil.Run(ctx, "ffprobe", args, executil.Options{
		Timeout: 30 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}

	var probeResult struct {
		Streams []struct {
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			FrameRate string `json:"r_frame_rate"`
			CodecName string `json:"codec_name"`
			Duration  string `json:"duration"`
		} `json:"streams"`
	}

	if err := json.Unmarshal([]byte(result.Output), &probeResult); err != nil {
		return nil, fmt.Errorf("failed to parse ffprobe output: %w", err)
	}

	if len(probeResult.Streams) == 0 {
		return nil, fmt.Errorf("no video streams found")
	}

	s := probeResult.Streams[0]
	info := &MediaInfo{
		Width:  s.Width,
		Height: s.Height,
		Codec:  s.CodecName,
	}

	// Parse FPS (e.g. "30/1" or "24000/1001")
	if s.FrameRate != "" {
		var num, den float64
		if _, err := fmt.Sscanf(s.FrameRate, "%f/%f", &num, &den); err == nil && den != 0 {
			info.FPS = num / den
		} else {
			// Try single value
			fmt.Sscanf(s.FrameRate, "%f", &num)
			info.FPS = num
		}
	}

	// Parse duration
	if s.Duration != "" {
		fmt.Sscanf(s.Duration, "%f", &info.Duration)
	}

	return info, nil
}

// OverlayOptions configures overlay effect compositing.
type OverlayOptions struct {
	Width   int
	Height  int
	FPS     int
	Opacity float64 // 0.0-1.0, e.g. 0.25 = 25%
	Codec   string
	Preset  string
	CRF     int
}

// ApplyOverlay composites an effect video over a clip with reduced opacity.
// The effect is looped if shorter than the clip and trimmed if longer.
func (p *Processor) ApplyOverlay(ctx context.Context, clipPath, effectPath, output string, opts OverlayOptions) error {
	if opts.Opacity <= 0 {
		opts.Opacity = 0.25
	}
	if opts.Width <= 0 {
		opts.Width = 1920
	}
	if opts.Height <= 0 {
		opts.Height = 1080
	}
	if opts.FPS <= 0 {
		opts.FPS = 30
	}

	alpha := fmt.Sprintf("%.2f", opts.Opacity)

	args := []string{"-y", "-hide_banner", "-loglevel", "warning"}
	if strings.Contains(opts.Codec, "nvenc") {
		args = append(args, "-hwaccel", "cuda")
	}
	args = append(args, "-i", clipPath)
	args = append(args, "-stream_loop", "-1", "-i", effectPath)

	filter := fmt.Sprintf(
		"[1:v]setpts=PTS-STARTPTS,format=rgba,colorchannelmixer=aa=%s,"+
			"scale=%d:%d:force_original_aspect_ratio=decrease,"+
			"pad=%d:%d:(ow-iw)/2:(oh-ih)/2[fx];"+
			"[0:v][fx]overlay=0:0:format=auto:shortest=1,"+
			"format=yuv420p[out]",
		alpha, opts.Width, opts.Height, opts.Width, opts.Height,
	)
	args = append(args, "-filter_complex", filter)
	args = append(args, "-map", "[out]")
	args = append(args, "-an")
	args = append(args, "-c:v", opts.Codec)
	args = append(args, "-preset", opts.Preset)
	args = append(args, "-g", fmt.Sprintf("%d", opts.FPS*2))
	if strings.Contains(opts.Codec, "nvenc") {
		args = append(args, "-rc", "vbr")
		args = append(args, "-cq", fmt.Sprintf("%d", opts.CRF))
		args = append(args, "-tune", "hq")
		args = append(args, "-bf", "0")
	} else {
		args = append(args, "-crf", fmt.Sprintf("%d", opts.CRF))
		args = append(args, "-bf", "0")
		args = append(args, "-refs", "1")
	}
	args = append(args, "-pix_fmt", "yuv420p", "-movflags", "+faststart", "-vsync", "cfr")
	args = append(args, output)

	_, err := executil.Run(ctx, p.path, args, executil.Options{
		Timeout: 5 * time.Minute,
	})
	return err
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

// ExtractFrame extracts a single frame at the specified timestamp as a high-quality PNG.
func (p *Processor) ExtractFrame(ctx context.Context, input, output string, timestamp float64) error {
	args := []string{
		"-y",
		"-hide_banner",
		"-loglevel", "warning",
		"-ss", fmt.Sprintf("%.3f", timestamp),
		"-i", input,
		"-frames:v", "1",
		"-q:v", "2",
		output,
	}

	_, err := executil.Run(ctx, p.path, args, executil.Options{
		Timeout: 30 * time.Second,
	})
	return err
}
