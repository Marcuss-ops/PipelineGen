package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
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
	if got := NormalizeEncoderPreset("h264_nvenc", "medium"); got != "p4" {
		t.Fatalf("NVENC medium preset = %q, want p4", got)
	}
	if got := NormalizeEncoderPreset("h264_nvenc", "slow"); got != "p7" {
		t.Fatalf("NVENC slow preset = %q, want p7", got)
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

func TestExplicitNVENCFailureIsTerminal(t *testing.T) {
	runner := &fallbackRunner{}
	p := NewProcessorWithEncoder("ffmpeg", "h264_nvenc").WithRunner(runner)
	err := p.RunWithEncoderPolicy(context.Background(), "h264_nvenc", []string{
		"-c:v", "h264_nvenc", "-preset", "p1", "-rc", "vbr", "-cq", "23", "out.mp4",
	}, time.Second)
	if err == nil || !strings.Contains(err.Error(), "NVENC encode required and failed") {
		t.Fatalf("RunWithEncoderPolicy error=%v, want terminal NVENC failure", err)
	}
	if runner.calls != 1 {
		t.Fatalf("runner calls=%d, want 1", runner.calls)
	}
	for _, arg := range runner.args[0] {
		if arg == "libx264" {
			t.Fatalf("terminal NVENC failure must not invoke libx264: %v", runner.args[0])
		}
	}
}

func TestNVENCBatchIsMicrobatchedAndDoesNotForceCUDADecode(t *testing.T) {
	runner := &resolverRunner{}
	p := NewProcessorWithEncoder("ffmpeg", "h264_nvenc").WithRunner(runner)
	jobs := make([]CutJob, 7)
	for i := range jobs {
		jobs[i] = CutJob{StartSec: float64(i), EndSec: float64(i + 1), Output: fmt.Sprintf("out-%d.mp4", i)}
	}
	if err := p.CutReencodeBatch(context.Background(), "in.mp4", jobs, true, "h264_nvenc", "veryfast", 23); err != nil {
		t.Fatalf("CutReencodeBatch returned error: %v", err)
	}
	if runner.calls != 3 {
		t.Fatalf("NVENC batch calls=%d, want 3 microbatches (3+3+1)", runner.calls)
	}
	for i, args := range runner.args {
		if containsPair(args, "-c:v", "libx264") {
			t.Fatalf("microbatch %d unexpectedly selected libx264: %v", i, args)
		}
		if containsPair(args, "-hwaccel", "cuda") {
			t.Fatalf("microbatch %d must not force CUDA decode: %v", i, args)
		}
		if !containsPair(args, "-c:v", "h264_nvenc") {
			t.Fatalf("microbatch %d did not select h264_nvenc: %v", i, args)
		}
	}
}

func TestNVENCProcessConcurrencyIsBounded(t *testing.T) {
	runner := &blockingRunner{entered: make(chan struct{}, 2), release: make(chan struct{})}
	p := NewProcessorWithEncoder("ffmpeg", "h264_nvenc").WithRunner(runner)

	done := make(chan error, 2)
	go func() {
		done <- p.RunWithEncoderPolicy(context.Background(), "h264_nvenc", []string{"-c:v", "h264_nvenc"}, time.Second)
	}()
	<-runner.entered
	go func() {
		done <- p.RunWithEncoderPolicy(context.Background(), "h264_nvenc", []string{"-c:v", "h264_nvenc"}, time.Second)
	}()
	time.Sleep(50 * time.Millisecond)
	if got := runner.calls.Load(); got != 1 {
		t.Fatalf("active NVENC runner calls=%d, want 1 while first is blocked", got)
	}
	close(runner.release)
	if err := <-done; err != nil {
		t.Fatalf("first NVENC encode returned error: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("second NVENC encode returned error: %v", err)
	}
	if got := runner.maxActive.Load(); got > 1 {
		t.Fatalf("max concurrent NVENC calls=%d, want <=1", got)
	}
}

func TestNVENCSlotCancellationDoesNotInvokeRunner(t *testing.T) {
	runner := &blockingRunner{entered: make(chan struct{}, 1), release: make(chan struct{})}
	p := NewProcessorWithEncoder("ffmpeg", "h264_nvenc").WithRunner(runner)
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- p.RunWithEncoderPolicy(context.Background(), "h264_nvenc", []string{"-c:v", "h264_nvenc"}, time.Second)
	}()
	<-runner.entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := p.RunWithEncoderPolicy(ctx, "h264_nvenc", []string{"-c:v", "h264_nvenc"}, time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled NVENC waiter error=%v, want context.Canceled", err)
	}
	if got := runner.calls.Load(); got != 1 {
		t.Fatalf("cancelled waiter invoked runner; calls=%d, want 1", got)
	}
	close(runner.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first NVENC encode returned error: %v", err)
	}
}

type fallbackRunner struct {
	calls int
	args  [][]string
}

type blockingRunner struct {
	entered   chan struct{}
	release   chan struct{}
	calls     atomic.Int32
	active    atomic.Int32
	maxActive atomic.Int32
}

func (r *blockingRunner) Run(_ context.Context, _ string, _ []string, _ process.Options) (*process.Result, error) {
	r.calls.Add(1)
	active := r.active.Add(1)
	for {
		max := r.maxActive.Load()
		if active <= max || r.maxActive.CompareAndSwap(max, active) {
			break
		}
	}
	r.entered <- struct{}{}
	<-r.release
	r.active.Add(-1)
	return &process.Result{}, nil
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
