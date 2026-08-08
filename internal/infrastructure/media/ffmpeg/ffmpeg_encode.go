package ffmpeg

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// Normalize processes a video to standard format (scale, crop, fps, codec).
func (p *Processor) Normalize(ctx context.Context, input, output string, opts NormalizeOptions) error {
	profile := opts.Profile.WithDefaults()
	if opts.Profile == (config.CanonicalVideoProfile{}) {
		profile = canonicalClipProfile()
	}
	requestedCodec := opts.Policy.Codec
	if requestedCodec == "" {
		requestedCodec = opts.Codec
	}
	if requestedCodec == "" {
		requestedCodec = p.encoderMode
	}
	if requestedCodec == "" {
		requestedCodec = string(EncoderLibX264)
	}
	preset := opts.Policy.Preset
	if preset == "" {
		preset = opts.Preset
	}
	if preset == "" {
		preset = p.encoderPreset
	}
	if preset == "" {
		preset = "veryfast"
	}
	crf := opts.Policy.CRF
	if crf <= 0 {
		crf = opts.CRF
	}
	if crf <= 0 {
		crf = p.encoderCRF
	}
	if crf <= 0 {
		crf = 23
	}
	opts.Width = profile.Width
	opts.Height = profile.Height
	opts.FPS = profile.FPS
	opts.KeyframeInterval = profile.KeyframeInterval
	opts.Codec = p.resolveEncoder(ctx, requestedCodec)
	if preset == "" {
		preset = p.encoderPreset
	}
	if crf <= 0 {
		crf = p.encoderCRF
	}
	opts.Preset = preset
	opts.CRF = crf
	args := []string{
		"-y",
		"-hide_banner",
		"-loglevel", "warning",
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

	args = append(args, "-vf", CanonicalVideoProfileFilter(profile))

	if !opts.KeepAudio {
		args = append(args, "-an")
	} else {
		args = append(args, "-c:a", profile.AudioCodec, "-b:a", profile.AudioBitrate, "-ar", "48000", "-ac", "2")
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
		args = append(args, "-preset", NormalizeEncoderPreset(opts.Codec, opts.Preset))
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

	return p.RunWithEncoderPolicy(ctx, opts.Codec, args, 15*time.Minute)
}

// CutReencode cuts a segment and re-encodes it to ensure exact frame-accurate duration.
// When noAudio is true the audio stream is stripped from the output.
// codec/preset/crf allow using hardware encoders (e.g. h264_nvenc); pass "" for defaults.
func (p *Processor) CutReencode(ctx context.Context, input, output, start, end string, noAudio bool, codec string, preset string, crf int) error {
	canonical := canonicalClipProfile()
	if codec == "" {
		codec = p.encoderMode
	}
	if preset == "" {
		preset = p.encoderPreset
	}
	if preset == "" {
		preset = "veryfast"
	}
	if crf <= 0 {
		crf = p.encoderCRF
	}
	if crf <= 0 {
		crf = 23
	}
	codec = p.resolveEncoder(ctx, codec)

	args := []string{"-y", "-hide_banner", "-loglevel", "warning"}

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

	args = append(args, "-vf", CanonicalVideoProfileFilterTrim(canonical))
	args = append(args, "-c:v", codec)
	args = append(args, "-g", fmt.Sprintf("%d", canonical.KeyframeInterval))

	if strings.Contains(codec, "nvenc") {
		args = append(args, "-preset", NormalizeEncoderPreset(codec, preset), "-rc", "vbr", "-cq", fmt.Sprintf("%d", crf), "-tune", "hq", "-bf", "0")
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

	return p.RunWithEncoderPolicy(ctx, codec, args, 10*time.Minute)
}

// CutAndNormalize cuts a segment and normalizes it in a single ffmpeg pass,
// avoiding a double re-encode. Combines CutSegment + Normalize.
func (p *Processor) CutAndNormalize(ctx context.Context, input, output, start, end string, opts CutAndNormalizeOptions) error {
	profile := opts.Profile.WithDefaults()
	if opts.Profile == (config.CanonicalVideoProfile{}) {
		profile = canonicalClipProfile()
	}
	requestedCodec := opts.Policy.Codec
	if requestedCodec == "" {
		requestedCodec = opts.Codec
	}
	if requestedCodec == "" {
		requestedCodec = p.encoderMode
	}
	if requestedCodec == "" {
		requestedCodec = string(EncoderLibX264)
	}
	preset := opts.Policy.Preset
	if preset == "" {
		preset = opts.Preset
	}
	if preset == "" {
		preset = p.encoderPreset
	}
	if preset == "" {
		preset = "veryfast"
	}
	crf := opts.Policy.CRF
	if crf <= 0 {
		crf = opts.CRF
	}
	if crf <= 0 {
		crf = p.encoderCRF
	}
	if crf <= 0 {
		crf = 23
	}
	opts.Width = profile.Width
	opts.Height = profile.Height
	opts.FPS = profile.FPS
	opts.Codec = p.resolveEncoder(ctx, requestedCodec)
	if preset == "" {
		preset = p.encoderPreset
	}
	if crf <= 0 {
		crf = p.encoderCRF
	}
	opts.Preset = preset
	opts.CRF = crf
	args := []string{
		"-y", "-hide_banner", "-loglevel", "warning",
	}

	if start != "" {
		args = append(args, "-ss", start)
	}
	args = append(args, "-i", input)
	if end != "" {
		args = append(args, "-to", end)
	}

	args = append(args, "-vf", CanonicalVideoProfileFilter(profile))

	if opts.NoAudio {
		args = append(args, "-an")
	} else {
		args = append(args, "-c:a", profile.AudioCodec, "-b:a", profile.AudioBitrate, "-ar", "48000", "-ac", "2", "-af", "asetpts=PTS-STARTPTS")
	}

	args = append(args, "-c:v", opts.Codec)
	if !strings.Contains(opts.Codec, "nvenc") {
		args = append(args, "-preset", opts.Preset)
	}
	args = append(args, "-g", fmt.Sprintf("%d", profile.KeyframeInterval))

	if strings.Contains(opts.Codec, "nvenc") {
		// The canonical preset is software-oriented; normalize it for NVENC.
		args = append(args, "-preset", NormalizeEncoderPreset(opts.Codec, opts.Preset))
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

	return p.RunWithEncoderPolicy(ctx, opts.Codec, args, 10*time.Minute)
}

// maxTemporalOutputsPerBatch bounds the number of outputs in one temporal
// input-seek microbatch. The bound applies to every encoder; NVENC retains
// the same conservative GPU session limit while software encoders also avoid
// building one filter graph across distant source timestamps.
const maxTemporalOutputsPerBatch = 3

// maxTemporalBatchSpanSec bounds how far one input-seek microbatch may span.
// Distant trim filters otherwise make FFmpeg decode the whole interval between
// the first and last requested timestamp before producing short clips.
const maxTemporalBatchSpanSec = 120

// CutReencodeBatch extracts multiple clips from the same input in bounded
// temporal input-seek microbatches. Each invocation seeks near the first
// requested timestamp before opening the input, then applies relative trim
// timestamps. This avoids decoding from the beginning of a long source when
// clips are far apart. NVENC remains the configured encoder and its failures
// remain terminal; codec/preset/crf allow hardware encoders such as h264_nvenc.
// The bounded filter graphs also reduce disk I/O contention compared to
// running N parallel CutReencode calls.
func (p *Processor) CutReencodeBatch(ctx context.Context, input string, jobs []CutJob, noAudio bool, codec string, preset string, crf int) error {
	if len(jobs) == 0 {
		return nil
	}
	if codec == "" {
		codec = p.encoderMode
	}
	if codec == "" {
		codec = string(EncoderLibX264)
	}
	if preset == "" {
		preset = p.encoderPreset
	}
	if preset == "" {
		preset = "veryfast"
	}
	if crf <= 0 {
		crf = p.encoderCRF
	}
	if crf <= 0 {
		crf = 23
	}
	codec = p.resolveEncoder(ctx, codec)

	if len(jobs) == 1 {
		return p.cutReencodeSingle(ctx, input, jobs[0].Output,
			FormatSec(jobs[0].StartSec), FormatSec(jobs[0].EndSec), noAudio, codec, preset, crf)
	}

	planned := append([]CutJob(nil), jobs...)
	sort.SliceStable(planned, func(i, j int) bool {
		return planned[i].StartSec < planned[j].StartSec
	})
	for start := 0; start < len(planned); {
		end := start
		anchor := planned[start].StartSec
		for end < len(planned) && end-start < maxTemporalOutputsPerBatch {
			if end > start && planned[end].EndSec-anchor > maxTemporalBatchSpanSec {
				break
			}
			end++
		}
		if err := p.cutReencodeBatchWithCodec(ctx, input, planned[start:end], noAudio, codec, preset, crf, anchor); err != nil {
			return err
		}
		start = end
	}
	return nil
}

// cutReencodeSingle is the single-job fast path for CutReencodeBatch.
func (p *Processor) cutReencodeSingle(ctx context.Context, input, output, start, end string, noAudio bool, codec string, preset string, crf int) error {
	return p.CutReencode(ctx, input, output, start, end, noAudio, codec, preset, crf)
}

// cutReencodeBatchWithCodec performs the actual batch cut with the given codec.
func (p *Processor) cutReencodeBatchWithCodec(ctx context.Context, input string, jobs []CutJob, noAudio bool, codec string, preset string, crf int, seekStart float64) error {
	canonical := canonicalClipProfile()
	args := []string{"-y", "-hide_banner", "-loglevel", "warning"}

	if seekStart > 0 {
		args = append(args, "-ss", FormatSec(seekStart))
	}
	args = append(args, "-i", input)

	var filterParts []string
	for i, j := range jobs {
		start := j.StartSec - seekStart
		end := j.EndSec - seekStart
		filterParts = append(filterParts,
			fmt.Sprintf("[0:v]trim=start=%f:end=%f,setpts=PTS-STARTPTS,%s[v%d]", start, end, CanonicalVideoProfileFilterTrim(canonical), i))
		if !noAudio {
			filterParts = append(filterParts,
				fmt.Sprintf("[0:a]atrim=start=%f:end=%f,asetpts=PTS-STARTPTS[a%d]", start, end, i))
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
			args = append(args, "-preset", NormalizeEncoderPreset(codec, preset), "-rc", "vbr", "-cq", fmt.Sprintf("%d", crf), "-tune", "hq", "-bf", "0")
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

	return p.RunWithEncoderPolicy(ctx, codec, args, 15*time.Minute)
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
		"[1:v]%s,colorkey=%s:%f:%f,colorchannelmixer=aa=%f[wm];[0:v][wm]overlay=%s[vout]",
		scaleFilter,
		opts.GreenScreenColor,
		opts.GreenScreenSimilarity,
		opts.GreenScreenBlend,
		opts.Opacity,
		overlayPos,
	)

	// Watermarking necessarily re-encodes the video stream. Resolve the
	// encoder through the processor's central policy so an explicitly
	// configured GPU encoder is used when requested, while software policy
	// remains an intentional choice. RunWithEncoderPolicy makes NVENC
	// failures terminal and never retries with libx264. Audio remains a
	// stream copy because the watermark filter only transforms video.
	codec := p.resolveEncoder(ctx, "")
	preset := p.encoderPreset
	if preset == "" {
		preset = "veryfast"
	}
	quality := p.encoderCRF
	if quality <= 0 {
		quality = 23
	}
	args := []string{
		"-y", "-hide_banner", "-loglevel", "warning",
		"-i", input,
		"-i", opts.ImagePath,
		"-filter_complex", filterComplex,
		"-map", "[vout]",
		"-map", "0:a:0?",
	}
	args = appendVideoEncoderArgs(args, codec, preset, quality)
	args = append(args,
		"-c:a", "copy",
		"-movflags", "+faststart",
		output,
	)

	return p.RunWithEncoderPolicy(ctx, codec, args, 5*time.Minute)
}
