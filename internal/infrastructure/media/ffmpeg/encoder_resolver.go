package ffmpeg

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
)

// EncoderMode is the requested video encoder policy.
type EncoderMode string

const (
	EncoderAuto    EncoderMode = "auto"
	EncoderNVENC   EncoderMode = "h264_nvenc"
	EncoderLibX264 EncoderMode = "libx264"
)

func IsNVENCCodec(codec string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(codec)), "nvenc")
}

// NormalizeEncoderPreset maps the canonical software preset to a valid
// NVENC preset while leaving explicit, codec-compatible values unchanged.
// The canonical profile remains libx264/veryfast; this is only a runtime
// command-line normalization.
func NormalizeEncoderPreset(codec, preset string) string {
	preset = strings.TrimSpace(preset)
	if !IsNVENCCodec(codec) {
		if preset == "" {
			return "veryfast"
		}
		return preset
	}
	if preset == "" || preset == "veryfast" || preset == "fast" {
		return "p1"
	}
	return preset
}

func NormalizeEncoderMode(requested string) EncoderMode {
	switch strings.ToLower(strings.TrimSpace(requested)) {
	case string(EncoderAuto):
		return EncoderAuto
	case "nvenc", "h264_nvenc", "hevc_nvenc":
		return EncoderNVENC
	case "libx264", "x264", "cpu", "software", "":
		return EncoderLibX264
	default:
		return EncoderLibX264
	}
}

type EncoderResolver struct {
	path   string
	runner ProcessRunner
	once   sync.Once
	nvenc  bool
}

func NewEncoderResolver(path string, runner ProcessRunner) *EncoderResolver {
	if path == "" {
		path = "ffmpeg"
	}
	if runner == nil {
		runner = defaultProcessRunner{}
	}
	return &EncoderResolver{path: path, runner: runner}
}

func (r *EncoderResolver) Resolve(ctx context.Context, requested string) (string, error) {
	switch NormalizeEncoderMode(requested) {
	case EncoderNVENC:
		return string(EncoderNVENC), nil
	case EncoderAuto:
		r.probe(ctx)
		if r.nvenc {
			return string(EncoderNVENC), nil
		}
	}
	return string(EncoderLibX264), nil
}

func (r *EncoderResolver) probe(ctx context.Context) {
	r.once.Do(func() {
		result, err := r.runner.Run(ctx, r.path, []string{"-hide_banner", "-encoders"}, process.Options{
			Timeout:        30 * time.Second,
			CombinedOutput: true,
		})
		if err != nil || result == nil {
			return
		}
		output := strings.ToLower(result.Output + "\n" + result.Stdout + "\n" + result.Stderr)
		for _, line := range strings.Split(output, "\n") {
			fields := strings.Fields(line)
			for _, field := range fields {
				if field == "h264_nvenc" {
					r.nvenc = true
					return
				}
			}
		}
	})
}
