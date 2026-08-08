// Package ffmpeg provides FFmpeg-based media processing utilities:
// video normalization, cutting, watermarking, and audio extraction/silence removal.
//
// STATUS: ACTIVE - Used by mediaasset, stockpipeline, videomuscles, fullimages, and voiceover.
package ffmpeg

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg/types"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func canonicalClipProfile() config.CanonicalVideoProfile {
	return (config.VideoConfig{}).CanonicalVideoProfile()
}

// ── ProcessRunner port (Pattern 0) ────────────────────────────────────

// ProcessRunner abstracts subprocess execution for testability.
// Production wires defaultProcessRunner; tests swap captureRunner mocks.
type ProcessRunner interface {
	Run(ctx context.Context, name string, args []string, opts process.Options) (*process.Result, error)
}

// defaultProcessRunner delegates to process.Run (production path).
type defaultProcessRunner struct{}

func (d defaultProcessRunner) Run(ctx context.Context, name string, args []string, opts process.Options) (*process.Result, error) {
	return process.Run(ctx, name, args, opts)
}

// Compile-time pin: defaultProcessRunner satisfies ProcessRunner.
var _ ProcessRunner = defaultProcessRunner{}

// ── Processor ───────────────────────────────────────────────────────────

// Processor handles FFmpeg operations.
type Processor struct {
	path          string
	runner        ProcessRunner
	resolver      *EncoderResolver
	encoderMode   string
	encoderPreset string
	encoderCRF    int
	nvencSlots    chan struct{}
	nvencSlotsMu  sync.Mutex
}

// NewProcessor creates a new FFmpeg Processor with the given binary path.
// It retains the historical software-first default.
func NewProcessor(ffmpegPath string) *Processor {
	return NewProcessorWithEncoder(ffmpegPath, string(EncoderLibX264))
}

// NewProcessorWithEncoder creates a Processor with an explicit runtime
// encoder policy (auto, h264_nvenc/nvenc, or libx264).
func NewProcessorWithEncoder(ffmpegPath, encoderMode string) *Processor {
	return newProcessor(ffmpegPath, config.VideoEncoderPolicy{
		Codec:  encoderMode,
		Preset: "veryfast",
		CRF:    23,
	})
}

func newProcessor(ffmpegPath string, policy config.VideoEncoderPolicy) *Processor {
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	runner := defaultProcessRunner{}
	if policy.Codec == "" {
		policy.Codec = string(EncoderLibX264)
	}
	if policy.Preset == "" {
		policy.Preset = "veryfast"
	}
	if policy.CRF <= 0 {
		policy.CRF = 23
	}
	return &Processor{
		path:          ffmpegPath,
		runner:        runner,
		resolver:      NewEncoderResolver(ffmpegPath, runner),
		encoderMode:   policy.Codec,
		encoderPreset: policy.Preset,
		encoderCRF:    policy.CRF,
		nvencSlots:    make(chan struct{}, 1),
	}
}

// NewFromConfig creates a new FFmpeg Processor using the config's resolved ffmpeg path.
func NewFromConfig(cfg *config.Config) *Processor {
	if cfg == nil {
		return NewProcessor("ffmpeg")
	}
	path := cfg.External.FfmpegPath
	if path == "" {
		path = "ffmpeg"
	}
	// Preserve the configured encoder policy (codec, preset and CRF); the
	// canonical artifact profile must not erase it before resolution.
	return newProcessor(path, cfg.Video.EncoderPolicy())
}

// Path returns the configured ffmpeg binary path.
func (p *Processor) Path() string { return p.path }

// WithRunner replaces the default subprocess runner with a custom implementation.
// Returns the receiver for fluent chaining. Used by tests to inject a capture
// runner so ffmpeg argv can be asserted without spawning a real subprocess.
func (p *Processor) WithRunner(r ProcessRunner) *Processor {
	p.runner = r
	p.resolver = NewEncoderResolver(p.path, r)
	return p
}

// ResolveEncoder is the single Processor-side entry point for encoder
// selection. Empty requests inherit the mode captured from VideoConfig;
// explicit requests remain backward-compatible with existing callers.
func (p *Processor) ResolveEncoder(ctx context.Context, requested string) string {
	if strings.TrimSpace(requested) == "" {
		requested = p.encoderMode
	}
	if strings.TrimSpace(requested) == "" {
		requested = string(EncoderLibX264)
	}
	if p.resolver == nil {
		p.resolver = NewEncoderResolver(p.path, p.runner)
	}
	codec, _ := p.resolver.Resolve(ctx, requested)
	return codec
}

// resolveEncoder preserves the package-local call shape used by the encoding
// methods while keeping the resolver available to other infrastructure
// adapters such as the stock renderer.
func (p *Processor) resolveEncoder(ctx context.Context, requested string) string {
	return p.ResolveEncoder(ctx, requested)
}

// RunWithEncoderPolicy executes exactly one encode attempt. An NVENC policy
// is GPU-required: a runtime encoder failure is returned with its original
// process output and is never hidden by a libx264 retry.
func (p *Processor) RunWithEncoderPolicy(ctx context.Context, codec string, args []string, timeout time.Duration) error {
	release, err := p.acquireNVENCSlot(ctx, codec)
	if err != nil {
		return err
	}
	if release != nil {
		defer release()
	}

	_, err = p.runner.Run(ctx, p.path, args, process.Options{
		Timeout:        timeout,
		CombinedOutput: true,
		MaxOutputBytes: 1 << 20,
	})
	if err != nil && IsNVENCCodec(codec) {
		return fmt.Errorf("NVENC encode required and failed: %w", err)
	}
	return err
}

func (p *Processor) acquireNVENCSlot(ctx context.Context, codec string) (func(), error) {
	if !IsNVENCCodec(codec) {
		return nil, nil
	}
	// Keep zero-value Processor values usable by tests and legacy callers.
	// Constructors initialize this channel, but a nil channel would block
	// forever when a GPU-required operation acquires its first slot.
	p.nvencSlotsMu.Lock()
	if p.nvencSlots == nil {
		p.nvencSlots = make(chan struct{}, 1)
	}
	slots := p.nvencSlots
	p.nvencSlotsMu.Unlock()
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ── Type aliases — canonical definitions in ffmpeg/types (PR6-B, June 2026) ──

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
// When noAudio is true, the audio stream is stripped from the output (-an) without
// re-encoding the video stream.
func (p *Processor) CutCopy(ctx context.Context, input, output, start, end string, noAudio bool) error {
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
	)
	if noAudio {
		args = append(args, "-an")
	}
	args = append(args, output)
	_, err := p.runner.Run(ctx, p.path, args, process.Options{
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
		_, err := p.runner.Run(ctx, p.path, []string{
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

	_, err = p.runner.Run(ctx, p.path, []string{
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

	_, err := p.runner.Run(ctx, p.path, args, process.Options{
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

	_, err := p.runner.Run(ctx, p.path, args, process.Options{
		Timeout: 15 * time.Minute,
	})
	return err
}

// appendVideoEncoderArgs appends the canonical runtime video encoder
// arguments. The artifact profile stays neutral; codec-specific quality
// controls are derived from the resolved encoder policy.
func appendVideoEncoderArgs(args []string, codec, preset string, quality int) []string {
	if strings.TrimSpace(preset) == "" {
		preset = "veryfast"
	}
	if quality <= 0 {
		quality = 23
	}
	args = append(args, "-c:v", codec, "-preset", NormalizeEncoderPreset(codec, preset))
	if IsNVENCCodec(codec) {
		return append(args, "-rc", "vbr", "-cq", fmt.Sprintf("%d", quality), "-tune", "hq", "-bf", "0")
	}
	return append(args, "-crf", fmt.Sprintf("%d", quality))
}

// GenerateProxy creates a 720p H.264/AAC proxy using the Processor's central
// encoder policy. A configured NVENC policy is GPU-required by
// RunWithEncoderPolicy and cannot silently retry with libx264.
func (p *Processor) GenerateProxy(ctx context.Context, input, output string) error {
	codec := p.ResolveEncoder(ctx, "")
	args := []string{
		"-y", "-hide_banner", "-loglevel", "warning",
		"-i", input,
		"-vf", "scale=-2:720",
		"-map", "0:v:0?", "-map", "0:a:0?",
	}
	args = appendVideoEncoderArgs(args, codec, p.encoderPreset, p.encoderCRF)
	args = append(args,
		"-c:a", "aac", "-b:a", "128k",
		"-movflags", "+faststart",
		output,
	)
	return p.RunWithEncoderPolicy(ctx, codec, args, 30*time.Minute)
}

// GenerateStoryboard creates a tiled sprite of key frames from the input file.
// It extracts one frame every intervalFrames frames and tiles them into a grid.
func (p *Processor) GenerateStoryboard(ctx context.Context, input, output string, intervalFrames, cols, rows int) error {
	if intervalFrames <= 0 {
		intervalFrames = 10
	}
	if cols <= 0 {
		cols = 5
	}
	if rows <= 0 {
		rows = 5
	}
	tileFilter := fmt.Sprintf("select=not(mod(n\\,%d)),scale=160:-1,tile=%dx%d", intervalFrames, cols, rows)
	args := []string{
		"-y", "-hide_banner", "-loglevel", "warning",
		"-i", input,
		"-frames", "1",
		"-q:v", "2",
		"-vf", tileFilter,
		output,
	}
	_, err := p.runner.Run(ctx, p.path, args, process.Options{
		Timeout: 10 * time.Minute,
	})
	return err
}
