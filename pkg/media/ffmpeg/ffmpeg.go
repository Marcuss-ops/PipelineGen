// Package ffmpeg provides FFmpeg operations for video processing.
//
// STATUS: ACTIVE - This package is actively used by mediaasset.Processor and mediapipeline.
package ffmpeg

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/config"
	"github.com/Marcuss-ops/PipelineGen/pkg/executil"
	"github.com/Marcuss-ops/PipelineGen/pkg/fileutil"
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

// Path returns the configured FFmpeg path.
func (p *Processor) Path() string {
	return p.path
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

// MediaInfo holds probed media information.
type MediaInfo struct {
	Duration float64 `json:"duration,omitempty"`
	Width    int     `json:"width,omitempty"`
	Height   int     `json:"height,omitempty"`
	FPS      float64 `json:"fps,omitempty"`
	Codec    string  `json:"codec,omitempty"`
}

// CutJob defines a single clip to extract from a source video.
type CutJob struct {
	StartSec float64
	EndSec   float64
	Output   string
}

// WatermarkOptions configures how a watermark overlay is applied to a video.
type WatermarkOptions struct {
	// ImagePath is the path to the watermark image (PNG with green screen background).
	ImagePath string
	// Opacity is the opacity of the watermark (0.0 - 1.0). 0.25 = 25%.
	Opacity float64
	// Position: "center", "top-right", "top-left", "bottom-right", "bottom-left"
	Position string
	// ScalePercent scales the watermark relative to the video width (e.g. 20 = 20% of width).
	// 0 means use the original watermark size.
	ScalePercent int
	// GreenScreenColor is the hex color to key out (default "0x00FF00" for green).
	GreenScreenColor string
	// GreenScreenSimilarity is the similarity threshold for chroma key (0.0 - 1.0, default 0.3).
	GreenScreenSimilarity float64
	// GreenScreenBlend is the blend amount for chroma key edges (0.0 - 1.0, default 0.1).
	GreenScreenBlend float64
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

// CutCopy cuts a segment using stream copy. It is much faster than re-encoding,
// but the output is constrained by the source container/codec structure.
func (p *Processor) CutCopy(ctx context.Context, input, output, start, end string) error {
	args := []string{"-y", "-hide_banner", "-loglevel", "warning"}

	args = append(args, "-i", input)
	if start != "" {
		args = append(args, "-ss", start)
	}
	if end != "" {
		args = append(args, "-to", end)
	}

	args = append(args,
		"-c", "copy",
		"-avoid_negative_ts", "make_zero",
		"-reset_timestamps", "1",
		output,
	)

	_, err := executil.Run(ctx, p.path, args, executil.Options{
		Timeout: 10 * time.Minute,
	})
	return err
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

// MergeInputs concatenates multiple video files into one.
func (p *Processor) MergeInputs(ctx context.Context, inputs []string, output string) error {
	if len(inputs) == 0 {
		return fmt.Errorf("no inputs provided")
	}
	if len(inputs) == 1 {
		return fileutil.CopyFile(inputs[0], output)
	}

	listFile, err := os.CreateTemp("", "ffmpeg_concat_*.txt")
	if err != nil {
		return fmt.Errorf("create concat list: %w", err)
	}
	defer os.Remove(listFile.Name())

	escapePath := func(path string) string {
		absPath, err := filepath.Abs(path)
		if err == nil && absPath != "" {
			path = absPath
		}
		path = strings.ReplaceAll(path, "'", "'\\''")
		return path
	}

	for _, input := range inputs {
		if _, err := fmt.Fprintf(listFile, "file '%s'\n", escapePath(input)); err != nil {
			_ = listFile.Close()
			return fmt.Errorf("write concat list: %w", err)
		}
	}
	if err := listFile.Close(); err != nil {
		return fmt.Errorf("close concat list: %w", err)
	}

	args := []string{
		"-y",
		"-hide_banner",
		"-loglevel", "warning",
		"-f", "concat",
		"-safe", "0",
		"-i", listFile.Name(),
		"-c", "copy",
		"-movflags", "+faststart",
		output,
	}

	_, err = executil.Run(ctx, p.path, args, executil.Options{
		Timeout: 15 * time.Minute,
	})
	if err != nil {
		return fmt.Errorf("ffmpeg concat failed: %w", err)
	}
	return nil
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
		Timeout: 10 * time.Minute,
	})
	return err
}

// FormatSec formats a float64 seconds value as "SSS.mmm" for ffmpeg timestamps.
func FormatSec(sec float64) string {
	return fmt.Sprintf("%.3f", sec)
}
