// Package ffmpeg provides FFmpeg-based media processing utilities:
// video normalization, cutting, watermarking, image-to-video conversion,
// and audio extraction/silence removal.
//
// STATUS: ACTIVE - Used by mediaasset, stockpipeline, videomuscles, fullimages, and voiceover.
package ffmpeg

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/pkg/platform"
)

// ── Processor ───────────────────────────────────────────────────────────

// Processor handles FFmpeg operations.
type Processor struct {
	path string
}

// NewProcessor creates a new FFmpeg Processor with the given binary path.
func NewProcessor(ffmpegPath string) *Processor {
	return &Processor{path: ffmpegPath}
}

// NewFromConfig creates a new FFmpeg Processor using the config's resolved ffmpeg path.
func NewFromConfig(cfg *config.Config) *Processor {
	path := cfg.External.FfmpegPath
	if path == "" {
		path = "ffmpeg"
	}
	return &Processor{path: path}
}

// Path returns the configured ffmpeg binary path.
func (p *Processor) Path() string { return p.path }

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

// FormatSec formats a float64 seconds value as "SSS.mmm" for ffmpeg timestamps.
func FormatSec(sec float64) string {
	return fmt.Sprintf("%.3f", sec)
}

// CutCopy performs a fast segment cut using FFmpeg stream copy mode (-c copy).
// This is much faster than re-encoding but requires start/end to align with keyframes.
func (p *Processor) CutCopy(ctx context.Context, input, output, start, end string) error {
	args := []string{"-y", "-hide_banner", "-loglevel", "warning"}
	if start != "" {
		args = append(args, "-ss", start)
	}
	args = append(args, "-i", input)
	if end != "" {
		args = append(args, "-to", end)
	}
	args = append(args,
		"-c", "copy",
		"-avoid_negative_ts", "make_zero",
		"-reset_timestamps", "1",
		output,
	)
	_, err := platform.Run(ctx, p.path, args, platform.ExecOptions{
		Timeout: 10 * time.Minute,
	})
	return err
}

// MergeInputs concatenates multiple video files into one using the concat demuxer.
// For a single input, just copies the file. Uses a temp file for the concat list.
func (p *Processor) MergeInputs(ctx context.Context, inputs []string, output string) error {
	if len(inputs) == 0 {
		return fmt.Errorf("MergeInputs: no inputs provided")
	}
	if len(inputs) == 1 {
		// Single input: just copy/normalize
		_, err := platform.Run(ctx, p.path, []string{
			"-y", "-hide_banner", "-loglevel", "warning",
			"-i", inputs[0],
			"-c", "copy",
			output,
		}, platform.ExecOptions{Timeout: 10 * time.Minute})
		return err
	}

	// Build concat list file
	var lines []string
	absPaths := make([]string, len(inputs))
	for i, inp := range inputs {
		abs, err := filepath.Abs(inp)
		if err != nil {
			abs = inp
		}
		absPaths[i] = abs
		lines = append(lines, fmt.Sprintf("file '%s'", strings.ReplaceAll(abs, "'", "'\\''")))
	}

	tmpFile, err := os.CreateTemp("", "ffmpeg_concat_*.txt")
	if err != nil {
		return fmt.Errorf("MergeInputs: create temp concat list: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(strings.Join(lines, "\n")); err != nil {
		tmpFile.Close()
		return fmt.Errorf("MergeInputs: write concat list: %w", err)
	}
	tmpFile.Close()

	_, err = platform.Run(ctx, p.path, []string{
		"-y", "-hide_banner", "-loglevel", "warning",
		"-f", "concat",
		"-safe", "0",
		"-i", tmpFile.Name(),
		"-c", "copy",
		output,
	}, platform.ExecOptions{Timeout: 10 * time.Minute})
	return err
}

// ── Audio helpers ───────────────────────────────────────────────────────

// ExtractClip estrae la traccia audio da un file video e la taglia a maxDur secondi.
// output è il percorso del file audio risultante (es. .mp3).
func ExtractClip(ctx context.Context, ffmpegPath, input, output string, maxDur int) error {
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}

	args := []string{
		"-y",
		"-i", input,
		"-t", fmt.Sprintf("%d", maxDur),
		"-vn",
		"-c:a", "libmp3lame",
		"-q:a", "2",
		output,
	}

	_, err := platform.Run(ctx, ffmpegPath, args, platform.ExecOptions{
		Timeout:        10 * time.Minute,
		CombinedOutput: true,
	})
	return err
}

func RemoveSilence(ctx context.Context, ffmpegPath, input, output string) error {
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}

	args := []string{
		"-y",
		"-i", input,
		"-af", "silenceremove=start_periods=1:start_threshold=-45dB:start_silence=0.25:stop_periods=-1:stop_threshold=-45dB:stop_silence=0.35",
		"-c:a", "libmp3lame",
		"-q:a", "2",
		output,
	}

	_, err := platform.Run(ctx, ffmpegPath, args, platform.ExecOptions{
		Timeout:        10 * time.Minute,
		CombinedOutput: true,
	})
	return err
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

	_, err := platform.Run(ctx, p.path, args, platform.ExecOptions{
		Timeout: 10 * time.Minute,
	})
	return err
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

	_, err := platform.Run(ctx, p.path, args, platform.ExecOptions{
		Timeout: 15 * time.Minute,
	})
	return err
}
