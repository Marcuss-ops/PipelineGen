package ffmpeg

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
)

// Normalize processes a video to standard format (scale, crop, fps, codec).
func (p *Processor) Normalize(ctx context.Context, input, output string, opts NormalizeOptions) error {
	canonical := canonicalClipProfile()
	opts.Width = canonical.Width
	opts.Height = canonical.Height
	opts.FPS = canonical.FPS
	opts.Codec = canonical.Codec
	opts.Preset = canonical.Preset
	opts.CRF = canonical.CRF
	opts.KeyframeInterval = canonical.KeyframeInterval
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
		// The canonical clip profile is an exact target duration, not merely
		// an upper bound. Loop short sources so a one-second source still
		// produces the required seven-second clip; longer sources remain
		// bounded by -t below.
		args = append(args, "-stream_loop", "-1")
		args = append(args, "-t", fmt.Sprintf("%d", opts.Duration))
	}

	args = append(args, "-i", input)

	args = append(args, "-vf", CanonicalClipFilter(canonical))

	if !opts.KeepAudio {
		args = append(args, "-an")
	} else {
		args = append(args, "-c:a", canonical.AudioCodec, "-b:a", canonical.AudioBitrate, "-ar", "48000", "-ac", "2")
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

	_, err := p.runner.Run(ctx, p.path, args, process.Options{
		Timeout: 15 * time.Minute,
	})
	return err
}

// CutReencode cuts a segment and re-encodes it to ensure exact frame-accurate duration.
// When noAudio is true the audio stream is stripped from the output.
// codec/preset/crf allow using hardware encoders (e.g. h264_nvenc); pass "" for defaults.
func (p *Processor) CutReencode(ctx context.Context, input, output, start, end string, noAudio bool, codec string, preset string, crf int) error {
	canonical := canonicalClipProfile()
	if codec == "" {
		codec = canonical.Codec
	}
	if preset == "" {
		preset = canonical.Preset
	}
	if crf <= 0 {
		crf = canonical.CRF
	}

	args := []string{"-y", "-hide_banner", "-loglevel", "warning"}

	if strings.Contains(codec, "nvenc") {
		args = append(args, "-hwaccel", "cuda")
	}

	if start != "" {
		args = append(args, "-ss", start)
	}
	args = append(args, "-i", input)

	// Prefer -t (duration) over -to so the output length is honored even when
	// the filter chain alters timestamps. Falls back to -to when start/end are
	// not plain seconds.
	if start != "" && end != "" {
		s, err1 := strconv.ParseFloat(start, 64)
		e, err2 := strconv.ParseFloat(end, 64)
		if err1 == nil && err2 == nil && e > s {
			args = append(args, "-t", fmt.Sprintf("%.3f", e-s))
		} else {
			args = append(args, "-to", end)
		}
	} else if end != "" {
		args = append(args, "-to", end)
	}

	args = append(args, "-vf", CanonicalClipFilterTrim(canonical))
	args = append(args, "-c:v", codec)
	args = append(args, "-g", fmt.Sprintf("%d", canonical.KeyframeInterval))

	if strings.Contains(codec, "nvenc") {
		p := preset
		if p == "fast" || p == "" {
			p = "p1"
		}
		args = append(args, "-preset", p, "-rc", "vbr", "-cq", fmt.Sprintf("%d", crf), "-tune", "hq", "-bf", "0")
	} else {
		args = append(args, "-preset", preset, "-crf", fmt.Sprintf("%d", crf))
	}

	if noAudio {
		args = append(args, "-an")
	} else {
		args = append(args, "-c:a", canonical.AudioCodec, "-b:a", canonical.AudioBitrate, "-ar", "48000", "-ac", "2")
	}

	args = append(args,
		"-pix_fmt", "yuv420p",
		"-movflags", "+faststart",
		"-vsync", "cfr",
		"-avoid_negative_ts", "make_zero",
		"-reset_timestamps", "1",
		output,
	)

	_, err := p.runner.Run(ctx, p.path, args, process.Options{
		Timeout: 10 * time.Minute,
	})
	return err
}

// CutAndNormalize cuts a segment and normalizes it in a single ffmpeg pass,
// avoiding a double re-encode. Combines CutSegment + Normalize.
func (p *Processor) CutAndNormalize(ctx context.Context, input, output, start, end string, opts CutAndNormalizeOptions) error {
	canonical := canonicalClipProfile()
	opts.Width = canonical.Width
	opts.Height = canonical.Height
	opts.FPS = canonical.FPS
	opts.Codec = canonical.Codec
	opts.Preset = canonical.Preset
	opts.CRF = canonical.CRF
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

	args = append(args, "-vf", CanonicalClipFilter(canonical))

	if opts.NoAudio {
		args = append(args, "-an")
	} else {
		args = append(args, "-c:a", canonical.AudioCodec, "-b:a", canonical.AudioBitrate, "-ar", "48000", "-ac", "2", "-af", "asetpts=PTS-STARTPTS")
	}

	args = append(args, "-c:v", opts.Codec)
	args = append(args, "-preset", opts.Preset)
	args = append(args, "-g", fmt.Sprintf("%d", canonical.KeyframeInterval))

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

	_, err := p.runner.Run(ctx, p.path, args, process.Options{
		Timeout: 10 * time.Minute,
	})
	return err
}

// CutReencodeBatch extracts multiple clips from the same input in a single
// ffmpeg invocation using trim filters. This reads the source file once and
// produces all clips, eliminating per-clip process spawn overhead and reducing
// disk I/O contention compared to running N parallel CutReencode calls.
// codec/preset/crf allow using hardware encoders (e.g. h264_nvenc).
// If the primary codec fails, automatically retries with libx264.
func (p *Processor) CutReencodeBatch(ctx context.Context, input string, jobs []CutJob, noAudio bool, codec string, preset string, crf int) error {
	if len(jobs) == 0 {
		return nil
	}
	canonical := canonicalClipProfile()
	if codec == "" {
		codec = canonical.Codec
	}
	if preset == "" {
		preset = canonical.Preset
	}
	if crf <= 0 {
		crf = canonical.CRF
	}

	if len(jobs) == 1 {
		return p.cutReencodeSingle(ctx, input, jobs[0].Output,
			FormatSec(jobs[0].StartSec), FormatSec(jobs[0].EndSec), noAudio, codec, preset, crf)
	}

	err := p.cutReencodeBatchWithCodec(ctx, input, jobs, noAudio, codec, preset, crf)
	if err != nil && strings.Contains(codec, "nvenc") {
		// Fallback to software encoder if hardware encoder fails
		return p.cutReencodeBatchWithCodec(ctx, input, jobs, noAudio, "libx264", preset, crf)
	}
	return err
}

// cutReencodeSingle is the single-job fast path for CutReencodeBatch.
func (p *Processor) cutReencodeSingle(ctx context.Context, input, output, start, end string, noAudio bool, codec string, preset string, crf int) error {
	return p.CutReencode(ctx, input, output, start, end, noAudio, codec, preset, crf)
}

// cutReencodeBatchWithCodec performs the actual batch cut with the given codec.
func (p *Processor) cutReencodeBatchWithCodec(ctx context.Context, input string, jobs []CutJob, noAudio bool, codec string, preset string, crf int) error {
	canonical := canonicalClipProfile()
	args := []string{"-y", "-hide_banner", "-loglevel", "warning"}

	if strings.Contains(codec, "nvenc") {
		args = append(args, "-hwaccel", "cuda")
	}

	args = append(args, "-i", input)

	var filterParts []string
	for i, j := range jobs {
		filterParts = append(filterParts,
			fmt.Sprintf("[0:v]trim=start=%f:end=%f,setpts=PTS-STARTPTS,%s[v%d]", j.StartSec, j.EndSec, CanonicalClipFilterTrim(canonical), i))
		if !noAudio {
			filterParts = append(filterParts,
				fmt.Sprintf("[0:a]atrim=start=%f:end=%f,asetpts=PTS-STARTPTS[a%d]", j.StartSec, j.EndSec, i))
		}
	}
	args = append(args, "-filter_complex", strings.Join(filterParts, ";"))

	for i, j := range jobs {
		if noAudio {
			args = append(args, "-map", fmt.Sprintf("[v%d]", i))
		} else {
			args = append(args, "-map", fmt.Sprintf("[v%d]", i), "-map", fmt.Sprintf("[a%d]", i))
		}
		args = append(args, "-c:v", codec)
		if strings.Contains(codec, "nvenc") {
			args = append(args, "-preset", preset, "-rc", "vbr", "-cq", fmt.Sprintf("%d", crf), "-tune", "hq", "-bf", "0")
		} else {
			args = append(args, "-preset", preset, "-crf", fmt.Sprintf("%d", crf))
		}
		args = append(args,
			"-pix_fmt", "yuv420p",
			"-movflags", "+faststart",
			"-vsync", "cfr",
			"-g", fmt.Sprintf("%d", canonical.KeyframeInterval),
			"-avoid_negative_ts", "make_zero",
		)
		if noAudio {
			args = append(args, "-an")
		} else {
			args = append(args, "-c:a", canonical.AudioCodec, "-b:a", canonical.AudioBitrate, "-ar", "48000", "-ac", "2")
		}
		// Bound each output to the intended duration so the container duration
		// stays correct regardless of how the filter chain rewrites timestamps.
		args = append(args, "-t", fmt.Sprintf("%.3f", j.EndSec-j.StartSec))
		args = append(args, j.Output)
	}

	_, err := p.runner.Run(ctx, p.path, args, process.Options{
		Timeout: 15 * time.Minute,
	})
	return err
}

// ApplyWatermark overlays a watermark image onto a video using chroma key to
// remove the green screen background, with configurable opacity and position.
func (p *Processor) ApplyWatermark(ctx context.Context, input, output string, opts WatermarkOptions) error {
	if opts.ImagePath == "" {
		return fmt.Errorf("watermark image path is required")
	}
	if opts.Opacity <= 0 {
		opts.Opacity = 0.25
	}
	if opts.GreenScreenColor == "" {
		opts.GreenScreenColor = "0x00FF00"
	}
	if opts.GreenScreenSimilarity <= 0 {
		opts.GreenScreenSimilarity = 0.3
	}
	if opts.GreenScreenBlend <= 0 {
		opts.GreenScreenBlend = 0.1
	}

	// Determine overlay position
	var overlayPos string
	switch opts.Position {
	case "top-right":
		overlayPos = "(W-w-20):20"
	case "top-left":
		overlayPos = "20:20"
	case "bottom-right":
		overlayPos = "(W-w-20):(H-h-20)"
	case "bottom-left":
		overlayPos = "20:(H-h-20)"
	default:
		overlayPos = "(W-w)/2:(H-h)/2"
	}

	// Build the scale filter for watermark
	scaleFilter := fmt.Sprintf("scale='min(%d,iw)':-1", opts.ScalePercent)
	if opts.ScalePercent > 0 {
		scaleFilter = fmt.Sprintf("scale='iw*%d/100':-1", opts.ScalePercent)
	}

	// Build the filter complex:
	// 1. Scale watermark
	// 2. Remove green screen with colorkey
	// 3. Apply opacity with colorchannelmixer (adjust alpha)
	// 4. Overlay on video
	filterComplex := fmt.Sprintf(
		"[1:v]%s,colorkey=%s:%f:%f,colorchannelmixer=aa=%f[wm];[0:v][wm]overlay=%s",
		scaleFilter,
		opts.GreenScreenColor,
		opts.GreenScreenSimilarity,
		opts.GreenScreenBlend,
		opts.Opacity,
		overlayPos,
	)

	args := []string{
		"-y", "-hide_banner", "-loglevel", "warning",
		"-i", input,
		"-i", opts.ImagePath,
		"-filter_complex", filterComplex,
		"-c:a", "copy",
		"-preset", "veryfast",
		"-movflags", "+faststart",
		output,
	}

	_, err := p.runner.Run(ctx, p.path, args, process.Options{
		Timeout: 5 * time.Minute,
	})
	return err
}
