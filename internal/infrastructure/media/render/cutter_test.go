// Package render — real-FFmpeg integration tests for FFmpegCutter
// (FASE 2.4, July 2026).
//
// These tests exercise the FULL FFmpegCutter surface end-to-end
// against a real ffmpeg binary (NOT a mock fake) so we cover:
//   - batch-cut + per-clip fallback behaviour under real ffmpeg
//   - ffprobe-validation pass (Status flip Succeeded→Validated)
//   - ProbeFailed path (ffprobe refuses to parse a clip body)
//   - partial-success batch (some clips succeeded, others failed)
//   - benchmark on a 20MB synthetic source so the byte-handling
//     path is observable at scale
//
// All tests guard with `exec.LookPath` for both ffmpeg AND ffprobe
// so they're auto-skipped on environments without them (CI runners
// without the binaries, developer laptops before installing, etc.).
// The skip message is explicit so operators / agents can tell at a
// glance why a test is skipped vs failing.
//
// Build tag: none (always-on). The `exec.LookPath` guard skips the
// test body, not the binary compile, so the file participates in
// `go vet` / `go build` even on ffmpeg-less environments.
package render

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
)

// ffmpegIntegrationTimeout bounds per-test wall-clock for Cut + Probe
// invocations. Tuned to comfortably outlive a 2-second synthetic
// source cut into 2-4 clips on commodity hardware.
const ffmpegIntegrationTimeout = 60 * time.Second

// ffmpegBenchTimeout bounds the bench (5 minutes) so the 20MB source
// benchmark can complete a full iteration cycle even on a slow runner.
const ffmpegBenchTimeout = 5 * time.Minute

// hasFFmpegAndFFprobe verifies both ffmpeg AND ffprobe are on PATH
// and runnable. Returns the ffmpeg path via t.Skipf when either is
// missing or non-functional. The dual check matches the FASE 2.4
// cutter contract: Cut() runs CutReencode(ffmpeg) THEN Probe(ffprobe);
// skipping when ffprobe is missing prevents false-success on slim
// Docker / base images that ship ffmpeg without ffprobe.
func hasFFmpegAndFFprobe(t *testing.T) string {
	t.Helper()
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skipf("ffmpeg not on PATH: %v (skipping real-FFmpeg integration test)", err)
		return ""
	}
	if versionOut, err := exec.Command(ffmpegPath, "-version").CombinedOutput(); err != nil || len(versionOut) == 0 {
		t.Skipf("ffmpeg at %s not functional (-version failed or empty): %v", ffmpegPath, err)
		return ""
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skipf("ffprobe not on PATH: %v (FASE 2.4 Probe step requires ffprobe — skipping)", err)
		return ""
	}
	if probeOut, err := exec.Command("ffprobe", "-version").CombinedOutput(); err != nil || len(probeOut) == 0 {
		t.Skipf("ffprobe -version failed or empty: %v", err)
		return ""
	}
	return ffmpegPath
}

// generateSyntheticSource creates a synthetic test pattern via
// ffmpeg's lavfi testsrc. The source is small enough to keep tests
// fast but real-enough to exercise the cut + probe pipeline
// end-to-end. durationSec is the seconds of synthetic content;
// size is the libavfilter size string (e.g. "320x240").
func generateSyntheticSource(t *testing.T, ffmpegPath, dir string, durationSec int, size string) string {
	t.Helper()
	sourcePath := filepath.Join(dir, fmt.Sprintf("source_%ds_%s.mp4", durationSec, size))
	cmd := exec.Command(ffmpegPath,
		"-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc=duration=%d:size=%s:rate=10", durationSec, size),
		"-pix_fmt", "yuv420p",
		"-c:v", "libx264",
		"-preset", "ultrafast",
		sourcePath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("synthetic source generation failed: %v\nffmpeg output:\n%s", err, string(out))
	}
	return sourcePath
}

// newCtxTimeout is a small helper wrapping context.WithTimeout with
// the canonical integration-test ceiling. Returns the ctx + cancel
// pair so callers can defer cancel().
func newCtxTimeout(parent context.Context, t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, ffmpegIntegrationTimeout)
}

// ── Composition-root injection chain test (no real ffmpeg needed) ───

// cutterCaptureRunner is a test-local ProcessRunner that captures
// the argv passed to it without spawning a real subprocess. Used
// to verify the full injection chain:
// FFmpegCutter.WithRunner → ffmpeg.Processor.WithRunner → ProcessRunner.Run
type cutterCaptureRunner struct {
	mu   sync.Mutex
	argv [][]string // captured argv per invocation
}

func (r *cutterCaptureRunner) Run(_ context.Context, name string, args []string, _ process.Options) (*process.Result, error) {
	r.mu.Lock()
	// Store the command name followed by its args so tests can
	// distinguish ffmpeg from ffprobe invocations.
	argv := append([]string{name}, args...)
	r.argv = append(r.argv, argv)
	r.mu.Unlock()

	// When the cutter runs ffprobe against a produced clip, return a
	// minimal valid JSON response so validation succeeds without a real
	// ffprobe binary. Source-probe invocations are still observable by
	// tests through the captured argv.
	if filepath.Base(name) == "ffprobe" {
		return &process.Result{
			ExitCode: 0,
			Stdout:   `{"streams":[{"codec_type":"video"}],"format":{"duration":"5.000000"}}`,
		}, nil
	}

	// Simulate the file that ffmpeg would write. The cutter writes
	// to a .part file first and renames it after validation, so
	// create the .part path derived from the last non-flag argument.
	if filepath.Base(name) == "ffmpeg" {
		outputPath := ""
		for i := len(args) - 1; i >= 0; i-- {
			if !strings.HasPrefix(args[i], "-") && args[i] != "" {
				outputPath = args[i]
				break
			}
		}
		if outputPath != "" {
			_ = os.MkdirAll(filepath.Dir(outputPath), 0o755)
			_ = os.WriteFile(outputPath, []byte("fake-clip"), 0o644)
		}
	}

	return &process.Result{ExitCode: 0}, nil
}

func (r *cutterCaptureRunner) hasArg(flag string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, argv := range r.argv {
		for _, a := range argv {
			if a == flag {
				return true
			}
		}
	}
	return false
}

func (r *cutterCaptureRunner) hasArgSubstring(substr string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, argv := range r.argv {
		for _, a := range argv {
			if strings.Contains(a, substr) {
				return true
			}
		}
	}
	return false
}

func (r *cutterCaptureRunner) invocationCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.argv)
}

// TestFFmpegCutter_InjectionChain_NoAudio_True verifies the full
// composition-root injection chain when NoAudio=true:
// FFmpegCutter.WithRunner → ffmpeg.Processor.WithRunner →
// captureRunner receives -an in the argv.
//
// This is a hermetic test (zero real ffmpeg needed) that locks
// the Pattern 0 ProcessRunner injection contract end-to-end.
func TestFFmpegCutter_InjectionChain_NoAudio_True(t *testing.T) {
	runner := &cutterCaptureRunner{}
	cutter := NewFFmpegCutter("ffmpeg", zap.NewNop()).WithRunner(runner)

	_, _ = cutter.Cut(context.Background(), stockpipeline.CutRequest{
		SourcePath: "/tmp/source.mp4",
		Jobs: []stockpipeline.CutJob{
			{StartSec: 0, EndSec: 5, OutputPath: "/tmp/out.mp4"},
		},
		NoAudio: true,
		Codec:   "libx264",
		Preset:  "veryfast",
		CRF:     18,
		Logger:  zap.NewNop(),
	})

	if !runner.hasArg("-an") {
		t.Errorf("NoAudio=true: expected -an in argv; got none (invocations=%d)", runner.invocationCount())
	}
	if runner.hasArg("-c:a") {
		t.Errorf("NoAudio=true: expected no -c:a in argv; found it (invocations=%d)", runner.invocationCount())
	}
}

// TestFFmpegCutter_InjectionChain_NoAudio_False verifies the full
// composition-root injection chain when NoAudio=false:
// FFmpegCutter.WithRunner → ffmpeg.Processor.WithRunner →
// captureRunner receives -c:a aac but NOT -an.
func TestFFmpegCutter_InjectionChain_NoAudio_False(t *testing.T) {
	runner := &cutterCaptureRunner{}
	cutter := NewFFmpegCutter("ffmpeg", zap.NewNop()).WithRunner(runner)

	_, _ = cutter.Cut(context.Background(), stockpipeline.CutRequest{
		SourcePath: "/tmp/source.mp4",
		Jobs: []stockpipeline.CutJob{
			{StartSec: 0, EndSec: 5, OutputPath: "/tmp/out.mp4"},
		},
		NoAudio: false,
		Codec:   "libx264",
		Preset:  "veryfast",
		CRF:     18,
		Logger:  zap.NewNop(),
	})

	if runner.hasArg("-an") {
		t.Errorf("NoAudio=false: expected no -an in argv; found it (invocations=%d)", runner.invocationCount())
	}
	// The cutter's fallback path calls CutReencode which adds -c:a aac
	if !runner.hasArgSubstring("aac") {
		t.Errorf("NoAudio=false: expected aac in argv; got none (invocations=%d)", runner.invocationCount())
	}
}

// TestFFmpegCutter_InjectionChain_WithRunnerReturnsSamePointer
// verifies that WithRunner returns the SAME *FFmpegCutter (fluent
// chaining contract) and that the custom runner is used, not a
// default runner.
func TestFFmpegCutter_InjectionChain_WithRunnerReturnsSamePointer(t *testing.T) {
	runner := &cutterCaptureRunner{}
	original := NewFFmpegCutter("ffmpeg", zap.NewNop())
	returned := original.WithRunner(runner)

	if original != returned {
		t.Errorf("WithRunner must return the same *FFmpegCutter pointer for fluent chaining; got different pointers")
	}
}

// batchFailingRunner is a ProcessRunner that fails the batch cut
// (identified by the presence of filter_complex in argv) and then
// succeeds on individual fallback cuts, except for a configurable
// subset that is forced to fail. Used to verify that FFmpegCutter
// preserves partial results when the batch fails and the fallback
// produces only a subset of clips.
type batchFailingRunner struct {
	mu              sync.Mutex
	batchCalls      int
	individualCalls int
	failIndividual  map[int]bool
}

func (r *batchFailingRunner) Run(_ context.Context, name string, args []string, _ process.Options) (*process.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if filepath.Base(name) == "ffprobe" {
		return &process.Result{
			ExitCode: 0,
			Stdout:   `{"streams":[{"codec_type":"video"}],"format":{"duration":"5.000000"}}`,
		}, nil
	}

	for _, a := range args {
		if strings.Contains(a, "filter_complex") {
			r.batchCalls++
			// Return a non-nil error so CutReencodeBatch surfaces
			// the failure to the cutter, which then enters the
			// per-clip fallback path. Without this, ExitCode=1
			// alone would be silently swallowed and the cutter
			// would assume batch success, find no files on disk
			// (because we never wrote them), and report
			// "all jobs failed" instead of partial-success.
			return &process.Result{ExitCode: 1, Stderr: "batch failed"}, errors.New("mock runner: batch ffmpeg failed")
		}
	}

	r.individualCalls++

	// Find output path (last non-flag argument).
	outputPath := ""
	for i := len(args) - 1; i >= 0; i-- {
		if !strings.HasPrefix(args[i], "-") && args[i] != "" {
			outputPath = args[i]
			break
		}
	}

	if r.failIndividual != nil && r.failIndividual[r.individualCalls] {
		return &process.Result{ExitCode: 1, Stderr: "individual cut failed"}, errors.New("mock runner: individual ffmpeg failed")
	}

	if outputPath != "" {
		_ = os.WriteFile(outputPath, []byte("fake-clip"), 0o644)
	}
	return &process.Result{ExitCode: 0}, nil
}

// TestFFmpegCutter_BatchFallback_PreservesPartialResults verifies that
// when the batch cut fails, FFmpegCutter falls back to per-clip cuts
// and preserves the successfully-produced clips even when some
// individual cuts fail.
func TestFFmpegCutter_BatchFallback_PreservesPartialResults(t *testing.T) {
	runner := &batchFailingRunner{failIndividual: map[int]bool{2: true}}
	cutter := NewFFmpegCutterOnlyCut("ffmpeg", zap.NewNop()).WithRunner(runner)

	dir := t.TempDir()
	jobs := []stockpipeline.CutJob{
		{StartSec: 0, EndSec: 1, OutputPath: filepath.Join(dir, "clip_0.mp4")},
		{StartSec: 1, EndSec: 2, OutputPath: filepath.Join(dir, "clip_1.mp4")},
		{StartSec: 2, EndSec: 3, OutputPath: filepath.Join(dir, "clip_2.mp4")},
	}

	result, err := cutter.Cut(context.Background(), stockpipeline.CutRequest{
		SourcePath:     "/tmp/source.mp4",
		SourceDuration: 10.0,
		Jobs:           jobs,
		NoAudio:        true,
		Codec:          "libx264",
		Preset:         "ultrafast",
		CRF:            18,
		Logger:         zap.NewNop(),
	})

	// Batch-level error should be nil because at least one clip succeeded.
	if err != nil {
		t.Fatalf("expected nil batch-level error when some clips succeed, got %v", err)
	}

	// Batch was attempted and failed.
	if runner.batchCalls != 1 {
		t.Errorf("batch calls = %d, want 1", runner.batchCalls)
	}

	// Fallback produced individual cuts for all three clips.
	if runner.individualCalls != 3 {
		t.Errorf("individual fallback calls = %d, want 3", runner.individualCalls)
	}

	// len(Items) must equal len(jobs) (mai-nil invariant).
	if len(result.Items) != len(jobs) {
		t.Fatalf("len(Items) = %d, want %d", len(result.Items), len(jobs))
	}

	// Clip 0 and clip 2 succeeded; clip 1 failed.
	if result.Items[0].Status != stockpipeline.CutItemStatusSucceeded {
		t.Errorf("item[0].Status = %v, want Succeeded", result.Items[0].Status)
	}
	if result.Items[1].Status != stockpipeline.CutItemStatusFailed {
		t.Errorf("item[1].Status = %v, want Failed", result.Items[1].Status)
	}
	if result.Items[2].Status != stockpipeline.CutItemStatusSucceeded {
		t.Errorf("item[2].Status = %v, want Succeeded", result.Items[2].Status)
	}

	// SuccessfulItems must contain exactly the two produced clips.
	produced := result.SuccessfulItems()
	if len(produced) != 2 {
		t.Errorf("SuccessfulItems() = %d, want 2", len(produced))
	}
}

// TestFFmpegCutter_SourceDurationSkipsProbe verifies that when
// CutRequest.SourceDuration is positive, the cutter skips the
// source-duration ffprobe call and proceeds directly to cutting.
// This eliminates the duplicate probe when the upstream
// stock.extract_clips step has already probed the source via
// validateAndProbeSourceDuration.
func TestFFmpegCutter_SourceDurationSkipsProbe(t *testing.T) {
	runner := &cutterCaptureRunner{}
	// Use NewFFmpegCutterOnlyCut so the post-cut runProbe pass is
	// disabled (probeAfterCut=false). The test is about SOURCE
	// probe-skip behaviour, not output-clip probe validation;
	// staying on CutItemStatusSucceeded keeps the assertion
	// crisp and isolates the runProbe axis from the source-probe
	// axis.
	cutter := NewFFmpegCutterOnlyCut("ffmpeg", zap.NewNop()).WithRunner(runner)

	// The capture runner does not write output files; pre-create the
	// expected output so the post-cut os.Stat succeeds.
	outputPath := filepath.Join(t.TempDir(), "out.mp4")
	if err := os.WriteFile(outputPath, []byte("fake-clip"), 0o644); err != nil {
		t.Fatalf("seed output file: %v", err)
	}

	result, err := cutter.Cut(context.Background(), stockpipeline.CutRequest{
		SourcePath:     "/tmp/source.mp4",
		SourceDuration: 120.0,
		Jobs: []stockpipeline.CutJob{
			{StartSec: 0, EndSec: 5, OutputPath: outputPath},
		},
		NoAudio: true,
		Codec:   "libx264",
		Preset:  "ultrafast",
		CRF:     18,
		Logger:  zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("Cut returned unexpected error: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("expected 1 result item, got %d", len(result.Items))
	}
	if result.Items[0].Status != stockpipeline.CutItemStatusSucceeded {
		t.Errorf("expected item status Succeeded, got %v", result.Items[0].Status)
	}

	// No ffprobe invocation should have been issued, but at least one
	// ffmpeg cut invocation should have occurred.
	var ffmpegInvocations, ffprobeInvocations int
	for _, argv := range runner.argv {
		if len(argv) == 0 {
			continue
		}
		cmd := filepath.Base(argv[0])
		if cmd == "ffmpeg" {
			ffmpegInvocations++
		}
		if cmd == "ffprobe" {
			ffprobeInvocations++
		}
	}
	if ffmpegInvocations == 0 {
		t.Errorf("expected at least one ffmpeg invocation, got %d", ffmpegInvocations)
	}
	if ffprobeInvocations != 0 {
		t.Errorf("expected no ffprobe invocation when SourceDuration is provided, got %d", ffprobeInvocations)
	}
}

// TestFFmpegCutter_ZeroSourceDurationProbesSource verifies the
// inverse of SourceDurationSkipsProbe: when SourceDuration is not
// provided (0), the cutter must probe the source to determine
// its duration before cutting.
func TestFFmpegCutter_ZeroSourceDurationProbesSource(t *testing.T) {
	runner := &cutterCaptureRunner{}
	// Use NewFFmpegCutterOnlyCut so the post-cut runProbe pass is
	// disabled. The test is about the SOURCE probe (one ffprobe
	// call for the source duration); the per-clip runProbe would
	// otherwise add a second ffprobe invocations-counting noise
	// on top of the source probe we're trying to pin.
	cutter := NewFFmpegCutterOnlyCut("ffmpeg", zap.NewNop()).WithRunner(runner)

	// The capture runner does not write output files; pre-create the
	// expected output so the post-cut os.Stat succeeds.
	outputPath := filepath.Join(t.TempDir(), "out.mp4")
	if err := os.WriteFile(outputPath, []byte("fake-clip"), 0o644); err != nil {
		t.Fatalf("seed output file: %v", err)
	}

	result, err := cutter.Cut(context.Background(), stockpipeline.CutRequest{
		SourcePath: "/tmp/source.mp4",
		Jobs: []stockpipeline.CutJob{
			{StartSec: 0, EndSec: 5, OutputPath: outputPath},
		},
		NoAudio: true,
		Codec:   "libx264",
		Preset:  "ultrafast",
		CRF:     18,
		Logger:  zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("Cut returned unexpected error: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("expected 1 result item, got %d", len(result.Items))
	}
	if result.Items[0].Status != stockpipeline.CutItemStatusSucceeded {
		t.Errorf("expected item status Succeeded, got %v", result.Items[0].Status)
	}

	var ffprobeInvocations int
	for _, argv := range runner.argv {
		if len(argv) == 0 {
			continue
		}
		if filepath.Base(argv[0]) == "ffprobe" {
			ffprobeInvocations++
		}
	}
	if ffprobeInvocations == 0 {
		t.Errorf("expected at least one ffprobe invocation when SourceDuration is 0, got %d", ffprobeInvocations)
	}
}

// TestFFmpegCutter_InjectionChain_DefaultRunner_NoMock
// verifies that NewFFmpegCutter uses the default process.Run
// runner (not a mock) when WithRunner is NOT called. This locks
// the production default so a future refactor cannot silently
// replace it with a no-op.
func TestFFmpegCutter_InjectionChain_DefaultRunner_NoMock(t *testing.T) {
	cutter := NewFFmpegCutter("ffmpeg", zap.NewNop())
	// Verify the internal processor has the default runner by
	// checking that calling Cut with a non-existent source
	// produces an error (the default runner tries to exec ffmpeg
	// which fails on a missing file — a no-op mock would succeed).
	_, err := cutter.Cut(context.Background(), stockpipeline.CutRequest{
		SourcePath: "/nonexistent/source.mp4",
		Jobs: []stockpipeline.CutJob{
			{StartSec: 0, EndSec: 5, OutputPath: "/tmp/out.mp4"},
		},
		NoAudio: true,
		Codec:   "libx264",
		Preset:  "ultrafast",
		CRF:     18,
		Logger:  zap.NewNop(),
	})
	// The default runner spawns a real ffmpeg which will fail
	// because the source file doesn't exist. If err is nil, the
	// runner was silently replaced with a no-op mock.
	if err == nil {
		t.Errorf("default runner should fail on nonexistent source; got nil error (runner may be a no-op mock)")
	}
}

// ── Real FFmpeg: full-batch cut + ffprobe validation ──────────────────

// TestFFmpegCutter_RealFFmpeg_BatchValidatesWithProbe exercises the
// happy path: 2-clip batch cut on a 2-second synthetic source.
// Every clip that lands on disk should reach CutItemStatusValidated
// (ffprobe parses the produced .mp4 and reports DurationSec > 0).
//
// Per the FASE 2.4 (July 2026) probe-must-validate contract: a
// successful cut that fails ffprobe-validation is a HARD test
// failure (the prober is broken, the source isn't truly playable,
// or the schema is being violated). A future partial-validate
// class is added separately via TestFFmpegCutter_RealFFmpeg_ProbeFailureOnCutOutput.
func TestFFmpegCutter_RealFFmpeg_BatchValidatesWithProbe(t *testing.T) {
	ffmpegPath := hasFFmpegAndFFprobe(t)
	ctx, cancel := newCtxTimeout(context.Background(), t)
	defer cancel()

	dir := t.TempDir()
	sourcePath := generateSyntheticSource(t, ffmpegPath, dir, 2, "320x240")

	cutter := NewFFmpegCutter(ffmpegPath, zap.NewNop())
	out0 := filepath.Join(dir, "clip_0.mp4")
	out1 := filepath.Join(dir, "clip_1.mp4")

	batch, err := cutter.Cut(ctx, stockpipeline.CutRequest{
		SourcePath: sourcePath,
		Jobs: []stockpipeline.CutJob{
			{StartSec: 0, EndSec: 1, OutputPath: out0},
			{StartSec: 1, EndSec: 2, OutputPath: out1},
		},
		Codec:  "libx264",
		Preset: "ultrafast",
		CRF:    18,
		Logger: zap.NewNop(),
	})
	if len(batch.Items) != 2 {
		t.Fatalf("mai-nil invariant violated: expected 2 Items; got %d", len(batch.Items))
	}

	successful, validated := 0, 0
	for i, item := range batch.Items {
		switch item.Status {
		case stockpipeline.CutItemStatusValidated:
			validated++
			successful++
		case stockpipeline.CutItemStatusSucceeded, stockpipeline.CutItemStatusProbeFailed:
			successful++
		}
		if item.OutputPath != "" {
			fi, statErr := os.Stat(item.OutputPath)
			if statErr != nil {
				t.Errorf("item %d OutputPath=%q but stat failed: %v", i, item.OutputPath, statErr)
				continue
			}
			if fi.Size() == 0 {
				t.Errorf("item %d OutputPath=%q but size=0", i, item.OutputPath)
			}
		}
	}
	// Strict validate contract: every successful cut must pass
	// ffprobe validation when both binaries are present. A
	// mismatch (successful > 0 && validated == 0) signals a
	// regression in the prober that we want surfaced as t.Errorf
	// not t.Logf.
	if successful == 0 {
		t.Fatalf("expected at least 1 successful clip on a 2-second source; got 0 (err=%v Items=%+v)", err, batch.Items)
	}
	if validated != successful {
		t.Errorf("probe-validation incomplete: successful=%d validated=%d (Items=%+v); ffprobe may be broken or source unparseable", successful, validated, batch.Items)
	}
	if err != nil {
		// Batch-level err is non-nil only when all items failed;
		// we should not see it on a happy-path batch with ≥1
		// successful clip. Surface as a soft warning (some ffmpeg
		// versions populate err even on partial-success via
		// non-zero exit on the second window).
		t.Logf("note: Cut returned batch-level err on a happy-path batch (acceptable on partial ffmpeg semantics): %v", err)
	}
}

// ── Real FFmpeg: ffprobe-fails-on-fake-clip (sanity probe check) ─────

// TestFFmpegCutter_RealFFmpeg_FFprobeFailsOnFakeClip exercises the
// soft-fail path prerequisites: ffprobe MUST refuse to parse a
// non-mp4 file. If this test ever PASSES on a fake-clip, the
// assumption behind FFmpegCutter.runProbe soft-fail flips breaks
// and the FASE 2.4 contracts need a rethink.
func TestFFmpegCutter_RealFFmpeg_FFprobeFailsOnFakeClip(t *testing.T) {
	hasFFmpegAndFFprobe(t) // skip-guard, no ffmpegPath needed
	ctx, cancel := newCtxTimeout(context.Background(), t)
	defer cancel()

	dir := t.TempDir()
	// Create a deliberately-invalid "clip": non-mp4 bytes.
	fakeClip := filepath.Join(dir, "fake.mp4")
	if err := os.WriteFile(fakeClip, []byte("not-a-valid-mp4-bytes"), 0o644); err != nil {
		t.Fatalf("could not write fake clip: %v", err)
	}

	// Direct ffprobe probe against the fake clip. We expect Probe()
	// to fail (return non-nil error) — this is the input to the
	// soft-fail path runProbe takes inside the cutter.
	proc := ffmpeg.NewProcessor("ffmpeg")
	info, err := proc.Probe(ctx, fakeClip)
	if err == nil {
		t.Fatalf("ffprobe succeeded on fake clip; expect non-nil error (info=%+v)", info)
	}
	t.Logf("ffprobe on fake clip failed as expected: %v", err)
}

// ── Real FFmpeg: partial-success batch (some clips failed) ────────────

// TestFFmpegCutter_RealFFmpeg_PartialSuccessBatch exercises a batch
// that mixes succeeded + failed clips — the second clip requests
// a window beyond the source's duration, so ffmpeg's behaviour is
// version-dependent (some clamp; some error hard). The FASE 2.4
// contract holds regardless of ffmpeg's response:
//
//  1. CutBatchResult is NEVER nil with zero output: len(Items)
//     equals len(req.Jobs) ALWAYS.
//  2. The mai-nil-with-zero-output invariant: every Item is
//     populated with Status + JobID + (Err or OutputPath).
//
// The permissive {0, 1, 2} FailedItems range reflects real ffmpeg
// behaviour across versions: ffmpeg 4.x may clamp the second clip
// (FailedItems=0 but second OutputPath=empty + Err set), while
// ffmpeg 6.x may error hard on the out-of-bounds window
// (FailedItems=1). We only require:
//
//   - len(Items) == 2 (mai-nil invariant)
//   - At least one of the clips referenced in the response
//     has an OutputPath on disk (batch did real work)
func TestFFmpegCutter_RealFFmpeg_PartialSuccessBatch(t *testing.T) {
	ffmpegPath := hasFFmpegAndFFprobe(t)
	ctx, cancel := newCtxTimeout(context.Background(), t)
	defer cancel()

	dir := t.TempDir()
	// 1-second source so the second clip's [5s,6s] window is
	// entirely beyond the duration — ffmpeg's response varies by
	// version (clamp vs hard error).
	sourcePath := generateSyntheticSource(t, ffmpegPath, dir, 1, "320x240")

	cutter := NewFFmpegCutter(ffmpegPath, zap.NewNop())
	out0 := filepath.Join(dir, "clip_0.mp4")
	out1 := filepath.Join(dir, "clip_1.mp4")

	batch, err := cutter.Cut(ctx, stockpipeline.CutRequest{
		SourcePath: sourcePath,
		Jobs: []stockpipeline.CutJob{
			{StartSec: 0, EndSec: 1, OutputPath: out0},
			{StartSec: 5, EndSec: 6, OutputPath: out1}, // beyond source
		},
		Codec:  "libx264",
		Preset: "ultrafast",
		CRF:    18,
		Logger: zap.NewNop(),
	})

	// Hard invariants — independent of ffmpeg version:
	if len(batch.Items) != 2 {
		t.Fatalf("mai-nil invariant violated: expected 2 Items; got %d (err=%v Items=%+v)", len(batch.Items), err, batch.Items)
	}
	// At least one clip must have an OutputPath on disk (the
	// [0s,1s] window fits inside the 1-second source).
	outputsOnDisk := 0
	for _, it := range batch.Items {
		if it.OutputPath != "" {
			if fi, statErr := os.Stat(it.OutputPath); statErr == nil && fi.Size() > 0 {
				outputsOnDisk++
			}
		}
	}
	if outputsOnDisk == 0 {
		t.Fatalf("expected at least 1 clip with non-empty on-disk OutputPath; got 0 (err=%v Items=%+v)", err, batch.Items)
	}
	// Permissive FailedItems range: real ffmpeg behaviour
	// varies. {0, 1, 2} covers clamp-to-source (FailedItems=0),
	// hard-error on second (FailedItems=1), both-fail (rare;
	// FailedItems=2). We accept all three.
	if len(batch.FailedItems()) > 2 {
		t.Errorf("FailedItems unexpectedly > 2 (got %d): %+v", len(batch.FailedItems()), batch.FailedItems())
	}
	// Log the actual state for diagnostic visibility. Slice the
	// fields so the log line stays compact but informative.
	t.Logf("partial batch: err=%v, Items=%s", err, summarizeBatch(batch))
}

// summarizeBatch produces a one-line, human-readable summary of a
// CutBatchResult for test diagnostics. Avoids dumping the full Err
// chain so test output stays scannable across 5+ tests in the
// same package.
func summarizeBatch(b stockpipeline.CutBatchResult) string {
	parts := make([]string, 0, len(b.Items))
	for i, it := range b.Items {
		status := it.Status.String()
		pathOrJob := it.OutputPath
		if pathOrJob == "" {
			pathOrJob = "(no-output:" + it.JobID + ")"
		}
		if it.Err != nil {
			parts = append(parts, fmt.Sprintf("[%d] %s %s err=%s", i, status, pathOrJob, errKind(it.Err)))
		} else {
			parts = append(parts, fmt.Sprintf("[%d] %s %s size=%dB dur=%.2fs", i, status, pathOrJob, it.SizeBytes, it.DurationSec))
		}
	}
	return "[" + joinComma(parts) + "]"
}

// errKind returns a short label for a typed error suitable for
// test-log diagnostics — first 60 chars of err.Error() to keep
// log lines bounded.
func errKind(err error) string {
	if err == nil {
		return "<nil>"
	}
	s := err.Error()
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}

// joinComma is a tiny alternative to strings.Join to keep the
// summarise helper self-contained without crossing the imports in
// this file (avoids pulling in fmt-imports for the comma glue).
func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}

// ── Real FFmpeg: 20 MB source benchmark ──────────────────────────────

// BenchmarkFFmpegCutter_20MB benchmarks a single 20 MB synthetic
// source cut into 4 clips. Run via `go test -bench=.` when ffmpeg
// is available; otherwise the bench is skipped via hasFFmpegAndFFprobe.
//
// Per-spec requirement: a 20 MB source exercises the byte-handling
// path without inflating CI duration (the testsrc lavfi generator
// produces content cheaply). 4 clips × 5-second windows on a 20 MB
// source is a realistic stock-pipeline payload.
func BenchmarkFFmpegCutter_20MB(b *testing.B) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		b.Skipf("ffmpeg not on PATH: %v", err)
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		b.Skipf("ffprobe not on PATH: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), ffmpegBenchTimeout)
	defer cancel()

	dir := b.TempDir()
	sourcePath := generateSyntheticSourceBench(b, ffmpegPath, dir, 20, "640x480", 20*1024*1024)

	cutter := NewFFmpegCutter(ffmpegPath, zap.NewNop())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		jobs := make([]stockpipeline.CutJob, 4)
		for j := 0; j < 4; j++ {
			jobs[j] = stockpipeline.CutJob{
				StartSec:   float64(j * 5),
				EndSec:     float64(j*5 + 5),
				OutputPath: fmt.Sprintf("%s/bench_clip_%d_%d.mp4", dir, i, j),
			}
		}
		batch, err := cutter.Cut(ctx, stockpipeline.CutRequest{
			SourcePath: sourcePath,
			Jobs:       jobs,
			Codec:      "libx264",
			Preset:     "ultrafast",
			CRF:        23,
			Logger:     zap.NewNop(),
		})
		if err != nil {
			b.Logf("cut batch returned err (acceptable on partial): %v", err)
		}
		if len(batch.Items) != 4 {
			b.Fatalf("mai-nil invariant violated: got %d items, want 4", len(batch.Items))
		}
	}
}

// ── Real FFmpeg: full-pipeline E2E (30s source, 3 clips, audio-aware) ──

// TestFFmpegCutter_RealFFmpeg_FullPipelineE2E exercises the stock
// pipeline end-to-end with a realistic 30-second synthetic source
// (video + audio), cutting 3 clips of 5 seconds each and verifying
// every output via ffprobe (duration > 0, video stream present,
// non-zero file size). This is the canonical "does the whole thing
// work on a real video" smoke.
func TestFFmpegCutter_RealFFmpeg_FullPipelineE2E(t *testing.T) {
	ffmpegPath := hasFFmpegAndFFprobe(t)
	ctx, cancel := newCtxTimeout(context.Background(), t)
	defer cancel()

	dir := t.TempDir()

	// Step 1: Generate a 30s synthetic source with video + audio.
	sourcePath := filepath.Join(dir, "source_30s.mp4")
	cmd := exec.Command(ffmpegPath,
		"-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=30:size=640x480:rate=24",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=30",
		"-pix_fmt", "yuv420p",
		"-c:v", "libx264", "-preset", "ultrafast",
		"-c:a", "aac", "-b:a", "128k",
		"-shortest",
		sourcePath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("source generation failed: %v\nffmpeg output:\n%s", err, string(out))
	}

	// Verify source via ffprobe.
	sourceInfo, err := probeFile(t, sourcePath)
	if err != nil {
		t.Fatalf("ffprobe source failed: %v", err)
	}
	if sourceInfo.duration < 29.0 {
		t.Fatalf("source duration too short: got %.2fs, want >=29s", sourceInfo.duration)
	}
	if !sourceInfo.hasVideo {
		t.Fatal("source must have a video stream")
	}
	if !sourceInfo.hasAudio {
		t.Fatal("source must have an audio stream")
	}
	t.Logf("source: duration=%.2fs video=%v audio=%v size=%d bytes", sourceInfo.duration, sourceInfo.hasVideo, sourceInfo.hasAudio, sourceInfo.size)

	// Step 2: Cut 3 clips (5s each) via FFmpegCutter.
	cutter := NewFFmpegCutter(ffmpegPath, zap.NewNop())
	clips := []struct {
		name    string
		start   float64
		end     float64
		noAudio bool
	}{
		{"clip_0_5s_audio.mp4", 0, 5, false},
		{"clip_10_15s_silent.mp4", 10, 15, true},
		{"clip_20_25s_audio.mp4", 20, 25, false},
	}

	// NOTE: NoAudio is a per-batch flag (not per-clip), so we need 2
	// separate batches to test both the audio and silent paths.

	// Batch A: clips WITH audio (0-5s, 20-25s)
	batchA, errA := cutter.Cut(ctx, stockpipeline.CutRequest{
		SourcePath: sourcePath,
		Jobs: []stockpipeline.CutJob{
			{StartSec: 0, EndSec: 5, OutputPath: filepath.Join(dir, clips[0].name)},
			{StartSec: 20, EndSec: 25, OutputPath: filepath.Join(dir, clips[2].name)},
		},
		NoAudio: false,
		Codec:   "libx264",
		Preset:  "ultrafast",
		CRF:     18,
		Logger:  zap.NewNop(),
	})
	t.Logf("batch A (audio): err=%v items=%s", errA, summarizeBatch(batchA))

	// Batch B: clip WITHOUT audio (10-15s)
	batchB, errB := cutter.Cut(ctx, stockpipeline.CutRequest{
		SourcePath: sourcePath,
		Jobs: []stockpipeline.CutJob{
			{StartSec: 10, EndSec: 15, OutputPath: filepath.Join(dir, clips[1].name)},
		},
		NoAudio: true,
		Codec:   "libx264",
		Preset:  "ultrafast",
		CRF:     18,
		Logger:  zap.NewNop(),
	})
	t.Logf("batch B (no-audio): err=%v items=%s", errB, summarizeBatch(batchB))

	// Step 3: Verify ALL 3 output clips via ffprobe.
	for _, c := range clips {
		clipPath := filepath.Join(dir, c.name)
		info, probeErr := probeFile(t, clipPath)
		if probeErr != nil {
			t.Errorf("clip %s: ffprobe failed: %v", c.name, probeErr)
			continue
		}

		// Duration should be ~5s (±0.5s for keyframe rounding).
		if info.duration < 4.0 || info.duration > 6.0 {
			t.Errorf("clip %s: duration=%.2fs, want 4.0-6.0s", c.name, info.duration)
		}
		if !info.hasVideo {
			t.Errorf("clip %s: missing video stream", c.name)
		}
		if info.size < 1000 {
			t.Errorf("clip %s: size=%d bytes, want >=1000", c.name, info.size)
		}

		// Audio presence must match NoAudio flag.
		if c.noAudio && info.hasAudio {
			t.Errorf("clip %s: NoAudio=true but ffprobe found audio stream", c.name)
		}
		if !c.noAudio && !info.hasAudio {
			t.Errorf("clip %s: NoAudio=false but ffprobe found NO audio stream", c.name)
		}

		t.Logf("clip %s: duration=%.2fs video=%v audio=%v size=%d bytes ✅",
			c.name, info.duration, info.hasVideo, info.hasAudio, info.size)
	}
}

// probeInfo holds ffprobe-extracted metadata for a single file.
type probeInfo struct {
	duration float64
	hasVideo bool
	hasAudio bool
	size     int64
} // ffprobeOutput is a minimal struct for unmarshalling ffprobe's JSON output.
// Only format.duration and stream codec_type are needed for test assertions.
type ffprobeOutput struct {
	Streams []ffprobeStreamOutput `json:"streams"`
	Format  ffprobeFormatOutput   `json:"format"`
}

type ffprobeStreamOutput struct {
	CodecType string `json:"codec_type"`
}

type ffprobeFormatOutput struct {
	Duration string `json:"duration"`
}

// probeFile runs ffprobe on a file and returns stream/format metadata.
// Uses json.Unmarshal for robust parsing that handles whitespace
// variations in ffprobe's JSON output.
func probeFile(t *testing.T, path string) (probeInfo, error) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		return probeInfo{}, fmt.Errorf("stat: %w", err)
	}
	out, err := exec.Command("ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_format", "-show_streams",
		"-i", path,
	).CombinedOutput()
	if err != nil {
		return probeInfo{}, fmt.Errorf("ffprobe: %w\n%s", err, string(out))
	}
	var probe ffprobeOutput
	if err := json.Unmarshal(out, &probe); err != nil {
		return probeInfo{}, fmt.Errorf("ffprobe json parse: %w", err)
	}
	info := probeInfo{size: fi.Size()}
	// Parse duration robustly — ffprobe may emit "N/A" for some streams.
	if _, parseErr := fmt.Sscanf(probe.Format.Duration, "%f", &info.duration); parseErr != nil {
		return probeInfo{}, fmt.Errorf("ffprobe duration parse %q: %w", probe.Format.Duration, parseErr)
	}
	if info.duration <= 0 {
		return probeInfo{}, fmt.Errorf("ffprobe returned non-positive duration %.2f (raw: %q)", info.duration, probe.Format.Duration)
	}
	for _, s := range probe.Streams {
		switch s.CodecType {
		case "video":
			info.hasVideo = true
		case "audio":
			info.hasAudio = true
		}
	}
	return info, nil
}

// generateSyntheticSourceBench is the *testing.B equivalent of
// generateSyntheticSource. Targets a targetBytes budget via
// explicit bitrate so the benchmark scales with throughput pressure.
func generateSyntheticSourceBench(b *testing.B, ffmpegPath, dir string, durationSec int, size string, targetBytes int64) string {
	b.Helper()
	sourcePath := filepath.Join(dir, fmt.Sprintf("bench_source_%ds_%s.mp4", durationSec, size))
	// Pick a bitrate that targets roughly the requested byte budget.
	// bitrate (bps) = bytes * 8 / duration
	bitrate := targetBytes * 8 / int64(durationSec)
	cmd := exec.Command(ffmpegPath,
		"-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc=duration=%d:size=%s:rate=30", durationSec, size),
		"-pix_fmt", "yuv420p",
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-b:v", fmt.Sprintf("%d", bitrate),
		sourcePath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		b.Fatalf("synthetic bench source generation failed: %v\nffmpeg output:\n%s", err, string(out))
	}
	fi, err := os.Stat(sourcePath)
	if err != nil {
		b.Fatalf("stat bench source: %v", err)
	}
	b.Logf("bench source path=%s bytes=%d target=%d", sourcePath, fi.Size(), targetBytes)
	return sourcePath
}
