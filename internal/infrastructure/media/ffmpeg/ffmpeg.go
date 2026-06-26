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

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg/types"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
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

// ── Type aliases — canonical definitions in ffmpeg/types (PR6-B, June 2026) ──
//
// Deprecated: import ".../internal/infrastructure/media/ffmpeg/types" and use
// types.NormalizeOptions, types.CutJob, etc. directly. These aliases exist
// for backward compatibility; they will be removed in a future wave.

type (
	NormalizeOptions       = types.NormalizeOptions
	CutAndNormalizeOptions = types.CutAndNormalizeOptions
	CutJob                 = types.CutJob
	WatermarkOptions       = types.WatermarkOptions
)



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
	_, err := process.Run(ctx, p.path, args, process.Options{
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
		_, err := process.Run(ctx, p.path, []string{
			"-y", "-hide_banner", "-loglevel", "warning",
			"-i", inputs[0],
			"-c", "copy",
			output,
		}, process.Options{Timeout: 10 * time.Minute})
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

	_, err = process.Run(ctx, p.path, []string{
		"-y", "-hide_banner", "-loglevel", "warning",
		"-f", "concat",
		"-safe", "0",
		"-i", tmpFile.Name(),
		"-c", "copy",
		output,
	}, process.Options{Timeout: 10 * time.Minute})
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

	_, err := process.Run(ctx, ffmpegPath, args, process.Options{
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

	_, err := process.Run(ctx, ffmpegPath, args, process.Options{
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

	_, err := process.Run(ctx, p.path, args, process.Options{
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

	_, err := process.Run(ctx, p.path, args, process.Options{
		Timeout: 15 * time.Minute,
	})
	return err
}
