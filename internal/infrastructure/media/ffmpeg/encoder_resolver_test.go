package ffmpeg

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

type resolverRunner struct {
	calls  int
	output string
	err    error
	args   [][]string
}

func (r *resolverRunner) Run(_ context.Context, _ string, args []string, _ process.Options) (*process.Result, error) {
	r.calls++
	r.args = append(r.args, append([]string(nil), args...))
	if r.err != nil {
		return nil, r.err
	}
	return &process.Result{Output: r.output}, nil
}

func TestNormalizeEncoderMode(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want EncoderMode
	}{
		{"auto", " auto ", EncoderAuto},
		{"nvenc alias", "nvenc", EncoderNVENC},
		{"hevc falls back to canonical h264 nvenc", "hevc_nvenc", EncoderNVENC},
		{"software", "x264", EncoderLibX264},
		{"empty is safe software", "", EncoderLibX264},
		{"unknown is safe software", "bogus", EncoderLibX264},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeEncoderMode(tt.in); got != tt.want {
				t.Fatalf("NormalizeEncoderMode(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeEncoderPreset(t *testing.T) {
	if got := NormalizeEncoderPreset("h264_nvenc", "veryfast"); got != "p1" {
		t.Fatalf("NVENC veryfast preset = %q, want p1", got)
	}
	if got := NormalizeEncoderPreset("h264_nvenc", "p4"); got != "p4" {
		t.Fatalf("explicit NVENC preset = %q, want p4", got)
	}
	if got := NormalizeEncoderPreset("libx264", "veryfast"); got != "veryfast" {
		t.Fatalf("software preset = %q, want veryfast", got)
	}
}

func TestEncoderResolverAutoUsesNVENCWhenAdvertised(t *testing.T) {
	runner := &resolverRunner{output: " V....D h264_nvenc NVIDIA NVENC H.264 encoder\n"}
	resolver := NewEncoderResolver("ffmpeg", runner)

	got, err := resolver.Resolve(context.Background(), "auto")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if got != string(EncoderNVENC) {
		t.Fatalf("Resolve(auto) = %q, want %q", got, EncoderNVENC)
	}
	if _, err := resolver.Resolve(context.Background(), "auto"); err != nil {
		t.Fatalf("second Resolve returned error: %v", err)
	}
	if runner.calls != 1 {
		t.Fatalf("encoder probe calls = %d, want 1", runner.calls)
	}
}

func TestEncoderResolverAutoFailsClosedToLibX264(t *testing.T) {
	tests := []struct {
		name   string
		output string
		err    error
	}{
		{"no nvenc", " V....D libx264 H.264 encoder\n", nil},
		{"probe error", "", errors.New("ffmpeg unavailable")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &resolverRunner{output: tt.output, err: tt.err}
			resolver := NewEncoderResolver("ffmpeg", runner)
			got, resolveErr := resolver.Resolve(context.Background(), "auto")
			if resolveErr != nil {
				t.Fatalf("Resolve returned error: %v", resolveErr)
			}
			if got != string(EncoderLibX264) {
				t.Fatalf("Resolve(auto) = %q, want %q", got, EncoderLibX264)
			}
		})
	}
}

func TestEncoderResolverExplicitNVENCDoesNotProbe(t *testing.T) {
	runner := &resolverRunner{output: "h264_nvenc"}
	resolver := NewEncoderResolver("ffmpeg", runner)
	got, err := resolver.Resolve(context.Background(), "h264_nvenc")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if got != string(EncoderNVENC) || runner.calls != 0 {
		t.Fatalf("explicit NVENC got=%q probe_calls=%d", got, runner.calls)
	}
}

func TestNewFromConfigPreservesRuntimeCodecPolicy(t *testing.T) {
	cfg := &config.Config{}
	cfg.External.FfmpegPath = "ffmpeg"
	cfg.Video.Codec = "auto"
	p := NewFromConfig(cfg).WithRunner(&resolverRunner{output: "h264_nvenc"})
	if got := p.ResolveEncoder(context.Background(), ""); got != string(EncoderNVENC) {
		t.Fatalf("configured auto policy resolved to %q, want %q", got, EncoderNVENC)
	}
}

func TestSoftwareFallbackArgsRemovesNVENCOnlyFlags(t *testing.T) {
	args := []string{
		"-hwaccel", "cuda", "-i", "in.mp4", "-c:v", "h264_nvenc",
		"-preset", "p1", "-rc", "vbr", "-cq", "26", "-tune", "hq", "-bf", "0",
		"-c:a", "aac", "out.mp4",
	}
	got := softwareFallbackArgs(args)
	want := []string{
		"-i", "in.mp4", "-c:v", "libx264", "-preset", "veryfast",
		"-bf", "0", "-c:a", "aac", "-crf", "26", "out.mp4",
	}
	if len(got) != len(want) {
		t.Fatalf("fallback argv length=%d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("fallback argv[%d]=%q, want %q; argv=%v", i, got[i], want[i], got)
		}
	}
}

func TestRunWithEncoderFallbackRetriesSoftware(t *testing.T) {
	runner := &fallbackRunner{}
	p := NewProcessor("ffmpeg").WithRunner(runner)
	if err := p.RunWithEncoderFallback(context.Background(), "h264_nvenc", []string{
		"-c:v", "h264_nvenc", "-preset", "p1", "-rc", "vbr", "-cq", "23", "out.mp4",
	}, time.Second); err != nil {
		t.Fatalf("RunWithEncoderFallback returned error: %v", err)
	}
	if runner.calls != 2 {
		t.Fatalf("runner calls=%d, want 2", runner.calls)
	}
	if !containsPair(runner.args[1], "-c:v", "libx264") {
		t.Fatalf("fallback did not select libx264: %v", runner.args[1])
	}
}

type fallbackRunner struct {
	calls int
	args  [][]string
}

func (r *fallbackRunner) Run(_ context.Context, _ string, args []string, _ process.Options) (*process.Result, error) {
	r.calls++
	r.args = append(r.args, append([]string(nil), args...))
	if r.calls == 1 {
		return &process.Result{ExitCode: 1}, errors.New("NVENC unavailable")
	}
	return &process.Result{}, nil
}

func containsPair(args []string, key, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key && args[i+1] == value {
			return true
		}
	}
	return false
}
