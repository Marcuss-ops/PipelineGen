package ffmpeg

import (
	"context"
	"strings"
	"testing"
	"time"

	fftypes "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg/types"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── captureRunner mock ──────────────────────────────────────────────────

// captureRunner is a ProcessRunner mock that captures the argv passed to Run
// without spawning a real ffmpeg subprocess. Hermetic — zero live-stack dependency.
type captureRunner struct {
	calls    int
	lastArgv []string
}

func (c *captureRunner) Run(_ context.Context, _ string, args []string, _ process.Options) (*process.Result, error) {
	c.calls++
	c.lastArgv = append([]string(nil), args...) // defensive copy
	return &process.Result{}, nil
}

// Compile-time pin: captureRunner satisfies ProcessRunner.
var _ ProcessRunner = (*captureRunner)(nil)

// captureRunnerWithBinary is a ProcessRunner mock that captures the
// binary name, argv, and options. Used for tests that need to verify
// binary path propagation or timeout configuration.
type captureRunnerWithBinary struct {
	calls      int
	lastBinary string
	lastArgv   []string
	lastOpts   process.Options
}

func (c *captureRunnerWithBinary) Run(_ context.Context, name string, args []string, opts process.Options) (*process.Result, error) {
	c.calls++
	c.lastBinary = name
	c.lastArgv = append([]string(nil), args...) // defensive copy
	c.lastOpts = opts
	return &process.Result{}, nil
}

// Compile-time pin: captureRunnerWithBinary satisfies ProcessRunner.
var _ ProcessRunner = (*captureRunnerWithBinary)(nil)

// contextCaptureRunner is a ProcessRunner mock that verifies context
// cancellation propagates. Returns ctx.Err() if the context is done.
type contextCaptureRunner struct {
	calls int
}

func (c *contextCaptureRunner) Run(ctx context.Context, _ string, _ []string, _ process.Options) (*process.Result, error) {
	c.calls++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &process.Result{}, nil
}

// Compile-time pin: contextCaptureRunner satisfies ProcessRunner.
var _ ProcessRunner = (*contextCaptureRunner)(nil)

// hasArg returns true if the given flag appears in the captured argv.
func hasArg(argv []string, flag string) bool {
	for _, a := range argv {
		if a == flag {
			return true
		}
	}
	return false
}

// hasArgSubstring returns true if any argv element contains the given substring.
// Useful for checking values embedded inside -filter_complex arguments.
func hasArgSubstring(argv []string, substr string) bool {
	for _, a := range argv {
		if strings.Contains(a, substr) {
			return true
		}
	}
	return false
}

// hasArgPair returns true if the argv contains the pair (flag, value) consecutively.
func hasArgPair(argv []string, flag, value string) bool {
	for i := 0; i < len(argv)-1; i++ {
		if argv[i] == flag && argv[i+1] == value {
			return true
		}
	}
	return false
}

// ── CutCopy noAudio tests ───────────────────────────────────────────────

// TestCutCopy_NoAudio_True_AppendsAn verifies that CutCopy(noAudio=true)
// appends the "-an" flag to strip audio from the output while preserving
// stream-copy mode (-c copy).
func TestCutCopy_NoAudio_True_AppendsAn(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	err := p.CutCopy(context.Background(), "input.mp4", "output.mp4", "00:00:00", "00:00:05", true)
	require.NoError(t, err)
	require.Equal(t, 1, runner.calls, "Run must be invoked exactly once")

	assert.True(t, hasArg(runner.lastArgv, "-an"),
		"noAudio=true must append -an to strip audio; got argv: %v", runner.lastArgv)
	assert.True(t, hasArgPair(runner.lastArgv, "-c", "copy"),
		"CutCopy must use stream-copy mode (-c copy); got argv: %v", runner.lastArgv)
	assert.True(t, hasArgPair(runner.lastArgv, "-i", "input.mp4"),
		"input must be passed as -i argument; got argv: %v", runner.lastArgv)
}

// TestCutCopy_NoAudio_False_NoAn verifies that CutCopy(noAudio=false)
// does NOT append "-an" — audio is preserved by default (backward-compat).
func TestCutCopy_NoAudio_False_NoAn(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	err := p.CutCopy(context.Background(), "input.mp4", "output.mp4", "00:00:00", "00:00:05", false)
	require.NoError(t, err)
	require.Equal(t, 1, runner.calls, "Run must be invoked exactly once")

	assert.False(t, hasArg(runner.lastArgv, "-an"),
		"noAudio=false must NOT append -an; got argv: %v", runner.lastArgv)
}

// TestCutCopy_NoAudio_ArgOrder verifies that "-an" appears after "-c copy"
// and before the output path — the canonical ffmpeg argument ordering.
func TestCutCopy_NoAudio_ArgOrder(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	err := p.CutCopy(context.Background(), "in.mp4", "out.mp4", "", "", true)
	require.NoError(t, err)

	argv := runner.lastArgv
	anIdx := -1
	copyIdx := -1
	outIdx := len(argv) - 1 // output is always last

	for i, a := range argv {
		if a == "-an" {
			anIdx = i
		}
		if a == "copy" && i > 0 && argv[i-1] == "-c" {
			copyIdx = i
		}
	}

	require.Greater(t, anIdx, -1, "-an must be present")
	require.Greater(t, copyIdx, -1, "-c copy must be present")
	assert.Greater(t, anIdx, copyIdx, "-an must appear after -c copy")
	assert.Greater(t, outIdx, anIdx, "-an must appear before the output path")
}

// TestCutCopy_NoAudio_False_ArgOrder verifies that when noAudio=false,
// the output path still comes last and "-an" is absent.
func TestCutCopy_NoAudio_False_ArgOrder(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	err := p.CutCopy(context.Background(), "in.mp4", "out.mp4", "10", "15", false)
	require.NoError(t, err)

	argv := runner.lastArgv
	assert.False(t, hasArg(argv, "-an"), "noAudio=false must not append -an")
	assert.Equal(t, "out.mp4", argv[len(argv)-1], "output must be the last argument")
	assert.True(t, hasArgPair(argv, "-ss", "10"), "-ss start must be present")
	assert.True(t, hasArgPair(argv, "-to", "15"), "-to end must be present")
}

// TestCutCopy_StartEndEmpty verifies that when start and end are empty,
// -ss and -to are NOT added (cut the whole file via stream copy).
func TestCutCopy_StartEndEmpty(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	err := p.CutCopy(context.Background(), "full.mp4", "copy.mp4", "", "", false)
	require.NoError(t, err)

	argv := runner.lastArgv
	assert.False(t, hasArg(argv, "-ss"), "empty start must not add -ss")
	assert.False(t, hasArg(argv, "-to"), "empty end must not add -to")
	assert.True(t, hasArgPair(argv, "-i", "full.mp4"), "input must be present")
	assert.Equal(t, "copy.mp4", argv[len(argv)-1], "output must be last arg")
}

// TestCutCopy_ProcessorPath verifies that the Processor.path is passed
// as the binary name to Run (not hardcoded "ffmpeg").
func TestCutCopy_ProcessorPath(t *testing.T) {
	runner := &captureRunnerWithBinary{}
	p := &Processor{path: "/usr/local/bin/ffmpeg", runner: runner}

	err := p.CutCopy(context.Background(), "in.mp4", "out.mp4", "", "", false)
	require.NoError(t, err)
	require.Equal(t, 1, runner.calls)
	assert.Equal(t, "/usr/local/bin/ffmpeg", runner.lastBinary,
		"Processor.path must be passed as the binary name to Run")
}

// TestCutCopy_ContextCancellation verifies that context cancellation propagates
// through the runner. Uses contextCaptureRunner which returns ctx.Err() when
// the context is done — a falsifiable invariant.
func TestCutCopy_ContextCancellation(t *testing.T) {
	runner := &contextCaptureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := p.CutCopy(ctx, "in.mp4", "out.mp4", "", "", false)
	assert.Error(t, err, "cancelled context must propagate as error from CutCopy")
	assert.Equal(t, 1, runner.calls, "Run must be invoked even when ctx is cancelled")
}

// TestNewProcessor_DefaultRunner verifies that NewProcessor sets a non-nil runner.
func TestNewProcessor_DefaultRunner(t *testing.T) {
	p := NewProcessor("/usr/bin/ffmpeg")
	require.NotNil(t, p, "NewProcessor must return non-nil")
	assert.Equal(t, "/usr/bin/ffmpeg", p.Path())
	assert.NotNil(t, p.runner, "runner must be set by NewProcessor")
}

// TestCutCopy_Timeout verifies that the Run options include a non-zero timeout.
func TestCutCopy_Timeout(t *testing.T) {
	runner := &captureRunnerWithBinary{}
	p := &Processor{path: "ffmpeg", runner: runner}

	err := p.CutCopy(context.Background(), "in.mp4", "out.mp4", "", "", true)
	require.NoError(t, err)
	require.Equal(t, 1, runner.calls)
	assert.Greater(t, runner.lastOpts.Timeout, time.Duration(0),
		"CutCopy must pass a non-zero timeout to Run")
}

// TestCutCopy_MultipleInvocations verifies that the runner tracks calls correctly
// across multiple invocations (no state leakage).
func TestCutCopy_MultipleInvocations(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	// First call: noAudio=true
	err := p.CutCopy(context.Background(), "a.mp4", "b.mp4", "", "", true)
	require.NoError(t, err)
	assert.True(t, hasArg(runner.lastArgv, "-an"), "first call must have -an")
	assert.Equal(t, 1, runner.calls)

	// Second call: noAudio=false
	err = p.CutCopy(context.Background(), "c.mp4", "d.mp4", "", "", false)
	require.NoError(t, err)
	assert.False(t, hasArg(runner.lastArgv, "-an"), "second call must NOT have -an")
	assert.Equal(t, 2, runner.calls)

	// Verify no state leakage: first call's argv should not affect second.
	// (captureRunner overwrites lastArgv each time, so this is inherently safe.)
}

// TestCutCopy_NoAudio_PreservesStreamCopyFlags verifies that noAudio=true
// does NOT interfere with the other CutCopy flags (-c copy, -avoid_negative_ts, etc.)
func TestCutCopy_NoAudio_PreservesStreamCopyFlags(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	err := p.CutCopy(context.Background(), "in.mp4", "out.mp4", "00:01:00", "00:01:10", true)
	require.NoError(t, err)

	argv := runner.lastArgv
	assert.True(t, hasArgPair(argv, "-c", "copy"), "-c copy must be present")
	assert.True(t, hasArgPair(argv, "-avoid_negative_ts", "make_zero"), "-avoid_negative_ts must be present")
	assert.True(t, hasArgPair(argv, "-reset_timestamps", "1"), "-reset_timestamps must be present")
	assert.True(t, hasArg(argv, "-an"), "-an must be present when noAudio=true")
	assert.True(t, hasArgPair(argv, "-ss", "00:01:00"), "-ss must be present")
	assert.True(t, hasArgPair(argv, "-to", "00:01:10"), "-to must be present")
}

// ── CutReencode noAudio tests ──────────────────────────────────────────

// TestCutReencode_NoAudio_True_AppendsAn verifies that CutReencode(noAudio=true)
// appends "-an" and does NOT add AAC audio encoding flags.
func TestCutReencode_NoAudio_True_AppendsAn(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	err := p.CutReencode(context.Background(), "in.mp4", "out.mp4", "10", "15",
		true, "libx264", "veryfast", 18)
	require.NoError(t, err)
	require.Equal(t, 1, runner.calls, "Run must be invoked exactly once")

	argv := runner.lastArgv
	assert.True(t, hasArg(argv, "-an"),
		"noAudio=true must append -an; got argv: %v", argv)
	assert.False(t, hasArgPair(argv, "-c:a", "aac"),
		"noAudio=true must NOT add -c:a aac; got argv: %v", argv)
	assert.False(t, hasArgPair(argv, "-b:a", "128k"),
		"noAudio=true must NOT add -b:a 128k; got argv: %v", argv)
}

// TestCutReencode_NoAudio_False_AddsAAC verifies that CutReencode(noAudio=false)
// does NOT append "-an" and instead adds AAC audio encoding flags.
func TestCutReencode_NoAudio_False_AddsAAC(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	err := p.CutReencode(context.Background(), "in.mp4", "out.mp4", "10", "15",
		false, "libx264", "veryfast", 18)
	require.NoError(t, err)
	require.Equal(t, 1, runner.calls)

	argv := runner.lastArgv
	assert.False(t, hasArg(argv, "-an"),
		"noAudio=false must NOT append -an; got argv: %v", argv)
	assert.True(t, hasArgPair(argv, "-c:a", "aac"),
		"noAudio=false must add -c:a aac; got argv: %v", argv)
	assert.True(t, hasArgPair(argv, "-b:a", "128k"),
		"noAudio=false must add -b:a 128k; got argv: %v", argv)
}

// TestCutReencode_CodecAndPreset verifies that an explicit stock profile is
// preserved while empty arguments still use the canonical fallback.
func TestCutReencode_CodecAndPreset(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	err := p.CutReencode(context.Background(), "in.mp4", "out.mp4", "5", "10",
		false, "libx264", "medium", 23)
	require.NoError(t, err)

	argv := runner.lastArgv
	assert.True(t, hasArgPair(argv, "-c:v", "libx264"),
		"codec must be passed as -c:v; got argv: %v", argv)
	assert.True(t, hasArgPair(argv, "-preset", "medium"),
		"explicit preset must be passed; got argv: %v", argv)
	assert.True(t, hasArgPair(argv, "-crf", "23"),
		"crf must be passed; got argv: %v", argv)
}

// TestCutReencode_DefaultCodec verifies the canonical fallback profile.
func TestCutReencode_DefaultCodec(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	err := p.CutReencode(context.Background(), "in.mp4", "out.mp4", "", "",
		true, "", "", 0)
	require.NoError(t, err)

	argv := runner.lastArgv
	assert.True(t, hasArgPair(argv, "-c:v", "libx264"),
		"empty codec must default to libx264; got argv: %v", argv)
	assert.True(t, hasArgPair(argv, "-preset", "veryfast"),
		"empty preset must default to veryfast; got argv: %v", argv)
	assert.True(t, hasArgPair(argv, "-crf", "23"),
		"canonical CRF must be 23; got argv: %v", argv)
}

// TestCutReencode_ContextCancellation verifies context cancellation propagates.
func TestCutReencode_ContextCancellation(t *testing.T) {
	runner := &contextCaptureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := p.CutReencode(ctx, "in.mp4", "out.mp4", "0", "5",
		false, "libx264", "veryfast", 18)
	assert.Error(t, err, "cancelled context must propagate as error")
}

// TestCutReencode_StreamCopyFlags verifies that -avoid_negative_ts and
// -reset_timestamps are present alongside noAudio.
func TestCutReencode_StreamCopyFlags(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	err := p.CutReencode(context.Background(), "in.mp4", "out.mp4", "10", "20",
		true, "libx264", "veryfast", 18)
	require.NoError(t, err)

	argv := runner.lastArgv
	assert.True(t, hasArgPair(argv, "-avoid_negative_ts", "make_zero"),
		"-avoid_negative_ts must be present; got argv: %v", argv)
	assert.True(t, hasArgPair(argv, "-reset_timestamps", "1"),
		"-reset_timestamps must be present; got argv: %v", argv)
	assert.True(t, hasArg(argv, "-an"),
		"-an must be present when noAudio=true; got argv: %v", argv)
}

// TestCutReencode_CanonicalFilter verifies that CutReencode applies the
// canonical video filter (scale, pad, fps, setpts) to force every output
// clip to the canonical geometry.
func TestCutReencode_CanonicalFilter(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	err := p.CutReencode(context.Background(), "in.mp4", "out.mp4", "10", "15",
		false, "libx264", "veryfast", 18)
	require.NoError(t, err)

	argv := runner.lastArgv
	assert.True(t, hasArg(argv, "-vf"),
		"CutReencode must apply a video filter; got argv: %v", argv)
	assert.True(t, hasArgSubstring(argv, "scale=1920:1080"),
		"canonical filter must scale to 1920x1080; got argv: %v", argv)
	assert.True(t, hasArgSubstring(argv, "pad=1920:1080"),
		"canonical filter must pad to 1920x1080; got argv: %v", argv)
	assert.True(t, hasArgSubstring(argv, "fps=24"),
		"canonical filter must force 24 fps; got argv: %v", argv)
	// CutReencode uses the trim variant so -to/-ss remain authoritative
	// and the output duration is not expanded to the source length.
	assert.False(t, hasArgSubstring(argv, "setpts=PTS-STARTPTS"),
		"CutReencode must NOT reset PTS in the video filter; got argv: %v", argv)
}

// TestCanonicalClipFilter verifies the canonical filter string matches the
// expected scale/pad/fps/setpts chain for the canonical profile.
func TestCanonicalClipFilter(t *testing.T) {
	cfg := canonicalClipProfile()
	got := CanonicalClipFilter(cfg)
	want := "scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,fps=24,setpts=PTS-STARTPTS"
	assert.Equal(t, want, got)
}

// TestCanonicalClipFilterTrim verifies the no-setpts variant used for
// trimmed segments where the cut boundary is authoritative.
func TestCanonicalClipFilterTrim(t *testing.T) {
	cfg := canonicalClipProfile()
	got := CanonicalClipFilterTrim(cfg)
	want := "scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,fps=24"
	assert.Equal(t, want, got)
}

// ── CutReencodeBatch noAudio tests ─────────────────────────────────────

// TestCutReencodeBatch_EmptyJobs_ReturnsNil verifies that empty jobs slice
// returns nil immediately without calling Run.
func TestCutReencodeBatch_EmptyJobs_ReturnsNil(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	err := p.CutReencodeBatch(context.Background(), "in.mp4",
		nil, true, "libx264", "veryfast", 18)
	assert.NoError(t, err, "empty jobs must return nil")
	assert.Equal(t, 0, runner.calls, "Run must NOT be called for empty jobs")
}

// TestCutReencodeBatch_SingleJob_NoAudio_True verifies that a single
// CutJob with noAudio=true produces the correct argv (delegates through
// cutReencodeSingle → CutReencode internally).
func TestCutReencodeBatch_SingleJob_NoAudio_True(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	jobs := []fftypes.CutJob{{StartSec: 5.0, EndSec: 10.0, Output: "out0.mp4"}}
	err := p.CutReencodeBatch(context.Background(), "in.mp4",
		jobs, true, "libx264", "veryfast", 18)
	require.NoError(t, err)
	require.Equal(t, 1, runner.calls, "single job must call Run exactly once")

	argv := runner.lastArgv
	assert.True(t, hasArg(argv, "-an"),
		"single job noAudio=true must have -an; got argv: %v", argv)
	assert.True(t, hasArgPair(argv, "-c:v", "libx264"),
		"single job must have codec; got argv: %v", argv)
}

// TestCutReencodeBatch_MultipleJobs_NoAudio_True verifies that multiple jobs
// with noAudio=true produce filter_complex with video-only maps and -an per job.
func TestCutReencodeBatch_MultipleJobs_NoAudio_True(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	jobs := []fftypes.CutJob{
		{StartSec: 0.0, EndSec: 5.0, Output: "out0.mp4"},
		{StartSec: 10.0, EndSec: 15.0, Output: "out1.mp4"},
	}
	err := p.CutReencodeBatch(context.Background(), "in.mp4",
		jobs, true, "libx264", "veryfast", 18)
	require.NoError(t, err)
	require.Equal(t, 1, runner.calls, "batch must call Run exactly once")

	argv := runner.lastArgv
	// Filter complex must include video trims but NOT audio trims
	assert.True(t, hasArg(argv, "-filter_complex"),
		"batch must have -filter_complex; got argv: %v", argv)
	assert.False(t, hasArgSubstring(argv, "atrim"),
		"noAudio=true must NOT generate atrim filters; got argv: %v", argv)
	assert.False(t, hasArgSubstring(argv, "asetpts"),
		"noAudio=true must NOT generate asetpts; got argv: %v", argv)
	// Per-job -map must reference only video (no [a0]/[a1])
	assert.True(t, hasArgPair(argv, "-map", "[v0]"),
		"must map [v0]; got argv: %v", argv)
	assert.True(t, hasArgPair(argv, "-map", "[v1]"),
		"must map [v1]; got argv: %v", argv)
	// -an must be present (applied per job)
	assert.True(t, hasArg(argv, "-an"),
		"noAudio=true must have -an; got argv: %v", argv)
	// Must NOT have -c:a aac (no audio encoding)
	assert.False(t, hasArgPair(argv, "-c:a", "aac"),
		"noAudio=true must NOT have -c:a aac; got argv: %v", argv)
}

// TestCutReencodeBatch_MultipleJobs_NoAudio_False verifies that multiple jobs
// with noAudio=false produce filter_complex with both video and audio maps.
func TestCutReencodeBatch_MultipleJobs_NoAudio_False(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	jobs := []fftypes.CutJob{
		{StartSec: 0.0, EndSec: 5.0, Output: "out0.mp4"},
		{StartSec: 10.0, EndSec: 15.0, Output: "out1.mp4"},
	}
	err := p.CutReencodeBatch(context.Background(), "in.mp4",
		jobs, false, "libx264", "veryfast", 18)
	require.NoError(t, err)
	require.Equal(t, 1, runner.calls)

	argv := runner.lastArgv
	// Filter complex must include BOTH video and audio trims
	assert.True(t, hasArgSubstring(argv, "atrim"),
		"noAudio=false must generate atrim filters; got argv: %v", argv)
	assert.True(t, hasArgSubstring(argv, "asetpts"),
		"noAudio=false must generate asetpts; got argv: %v", argv)
	// Per-job -map must reference both video AND audio
	assert.True(t, hasArgPair(argv, "-map", "[v0]"),
		"must map [v0]; got argv: %v", argv)
	assert.True(t, hasArgPair(argv, "-map", "[a0]"),
		"must map [a0] for audio; got argv: %v", argv)
	// -c:a aac must be present (audio encoding)
	assert.True(t, hasArgPair(argv, "-c:a", "aac"),
		"noAudio=false must have -c:a aac; got argv: %v", argv)
	// -an must NOT be present
	assert.False(t, hasArg(argv, "-an"),
		"noAudio=false must NOT have -an; got argv: %v", argv)
}

// TestCutReencodeBatch_MultipleJobs_CanonicalFilter verifies that the
// batch filter_complex applies the canonical video filter to every clip.
func TestCutReencodeBatch_MultipleJobs_CanonicalFilter(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	jobs := []fftypes.CutJob{
		{StartSec: 0.0, EndSec: 5.0, Output: "out0.mp4"},
		{StartSec: 10.0, EndSec: 15.0, Output: "out1.mp4"},
	}
	err := p.CutReencodeBatch(context.Background(), "in.mp4",
		jobs, true, "libx264", "veryfast", 18)
	require.NoError(t, err)
	require.Equal(t, 1, runner.calls, "batch must call Run exactly once")

	argv := runner.lastArgv
	assert.True(t, hasArg(argv, "-filter_complex"),
		"batch must use -filter_complex; got argv: %v", argv)
	assert.True(t, hasArgSubstring(argv, "scale=1920:1080"),
		"canonical filter must scale to 1920x1080; got argv: %v", argv)
	assert.True(t, hasArgSubstring(argv, "pad=1920:1080"),
		"canonical filter must pad to 1920x1080; got argv: %v", argv)
	assert.True(t, hasArgSubstring(argv, "fps=24"),
		"canonical filter must force 24 fps; got argv: %v", argv)
	// Batch trims first, then resets PTS before applying the canonical
	// geometry filter, so the output starts at zero and has the trimmed
	// duration.
	assert.True(t, hasArgSubstring(argv, "setpts=PTS-STARTPTS"),
		"CutReencodeBatch must reset PTS after trim; got argv: %v", argv)
}

// TestCutReencodeBatch_OutputPaths verifies that each job's output path
// appears in the argv for multi-job batches.
func TestCutReencodeBatch_OutputPaths(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	jobs := []fftypes.CutJob{
		{StartSec: 0.0, EndSec: 5.0, Output: "/tmp/clip_a.mp4"},
		{StartSec: 10.0, EndSec: 15.0, Output: "/tmp/clip_b.mp4"},
		{StartSec: 20.0, EndSec: 25.0, Output: "/tmp/clip_c.mp4"},
	}
	err := p.CutReencodeBatch(context.Background(), "in.mp4",
		jobs, true, "libx264", "veryfast", 18)
	require.NoError(t, err)

	argv := runner.lastArgv
	assert.True(t, hasArg(argv, "/tmp/clip_a.mp4"),
		"first output must be present; got argv: %v", argv)
	assert.True(t, hasArg(argv, "/tmp/clip_b.mp4"),
		"second output must be present; got argv: %v", argv)
	assert.True(t, hasArg(argv, "/tmp/clip_c.mp4"),
		"third output must be present; got argv: %v", argv)
}

// TestCutReencodeBatch_ContextCancellation verifies context cancellation propagates.
func TestCutReencodeBatch_ContextCancellation(t *testing.T) {
	runner := &contextCaptureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	jobs := []fftypes.CutJob{
		{StartSec: 0.0, EndSec: 5.0, Output: "out.mp4"},
	}
	err := p.CutReencodeBatch(ctx, "in.mp4",
		jobs, true, "libx264", "veryfast", 18)
	assert.Error(t, err, "cancelled context must propagate")
}

// ── Normalize KeepAudio tests ──────────────────────────────────────────

// defaultNormalizeOpts returns minimal NormalizeOptions that satisfy the
// filter chain (non-zero Width/Height/FPS/Codec/Preset/CRF).
func defaultNormalizeOpts() NormalizeOptions {
	return NormalizeOptions{
		Width:  1280,
		Height: 720,
		FPS:    30,
		Codec:  "libx264",
		Preset: "veryfast",
		CRF:    18,
	}
}

// TestNormalize_KeepAudio_False_AppendsAn verifies that Normalize with
// KeepAudio=false (default) appends "-an" to strip audio.
func TestNormalize_KeepAudio_False_AppendsAn(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	opts := defaultNormalizeOpts()
	opts.KeepAudio = false
	err := p.Normalize(context.Background(), "in.mp4", "out.mp4", opts)
	require.NoError(t, err)
	require.Equal(t, 1, runner.calls)

	argv := runner.lastArgv
	assert.True(t, hasArg(argv, "-an"),
		"KeepAudio=false must append -an; got argv: %v", argv)
	assert.False(t, hasArgPair(argv, "-c:a", "aac"),
		"KeepAudio=false must NOT add -c:a aac; got argv: %v", argv)
	assert.False(t, hasArgPair(argv, "-b:a", "128k"),
		"KeepAudio=false must NOT add -b:a 128k; got argv: %v", argv)
}

// TestNormalize_KeepAudio_True_AddsAAC verifies that Normalize with
// KeepAudio=true does NOT append "-an" and instead adds AAC audio encoding.
func TestNormalize_KeepAudio_True_AddsAAC(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	opts := defaultNormalizeOpts()
	opts.KeepAudio = true
	err := p.Normalize(context.Background(), "in.mp4", "out.mp4", opts)
	require.NoError(t, err)
	require.Equal(t, 1, runner.calls)

	argv := runner.lastArgv
	assert.False(t, hasArg(argv, "-an"),
		"KeepAudio=true must NOT append -an; got argv: %v", argv)
	assert.True(t, hasArgPair(argv, "-c:a", "aac"),
		"KeepAudio=true must add -c:a aac; got argv: %v", argv)
	assert.True(t, hasArgPair(argv, "-b:a", "128k"),
		"KeepAudio=true must add -b:a 128k; got argv: %v", argv)
}

// TestNormalize_KeepAudio_True_AudioFilter verifies that KeepAudio=true
// adds the asetpts PTS-reset filter for the audio stream.
func TestNormalize_KeepAudio_True_AudioFilter(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	opts := defaultNormalizeOpts()
	opts.KeepAudio = true
	err := p.Normalize(context.Background(), "in.mp4", "out.mp4", opts)
	require.NoError(t, err)

	argv := runner.lastArgv
	assert.True(t, hasArgPair(argv, "-af", "asetpts=PTS-STARTPTS"),
		"KeepAudio=true must add -af asetpts=PTS-STARTPTS; got argv: %v", argv)
}

// TestNormalize_KeepAudio_False_NoAudioFilter verifies that KeepAudio=false
// does NOT add the -af asetpts filter.
func TestNormalize_KeepAudio_False_NoAudioFilter(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	opts := defaultNormalizeOpts()
	opts.KeepAudio = false
	err := p.Normalize(context.Background(), "in.mp4", "out.mp4", opts)
	require.NoError(t, err)

	argv := runner.lastArgv
	assert.False(t, hasArgPair(argv, "-af", "asetpts=PTS-STARTPTS"),
		"KeepAudio=false must NOT add -af asetpts; got argv: %v", argv)
}

// TestNormalize_VideoSettings verifies that normalization filter chain
// and codec settings are passed correctly.
func TestNormalize_VideoSettings(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	opts := defaultNormalizeOpts()
	opts.KeepAudio = false
	err := p.Normalize(context.Background(), "in.mp4", "out.mp4", opts)
	require.NoError(t, err)

	argv := runner.lastArgv
	assert.True(t, hasArgPair(argv, "-c:v", "libx264"),
		"codec must be passed; got argv: %v", argv)
	assert.True(t, hasArgPair(argv, "-preset", "veryfast"),
		"preset must be passed; got argv: %v", argv)
	assert.True(t, hasArgPair(argv, "-crf", "23"),
		"canonical CRF must be passed; got argv: %v", argv)
	assert.True(t, hasArgPair(argv, "-pix_fmt", "yuv420p"),
		"-pix_fmt yuv420p must be present; got argv: %v", argv)
	assert.True(t, hasArgPair(argv, "-movflags", "+faststart"),
		"-movflags +faststart must be present; got argv: %v", argv)
}

// TestNormalize_TargetDurationLoopsShortSources pins the exact-duration
// contract used by materialized clips: short sources must be looped before
// the output duration limit is applied.
func TestNormalize_TargetDurationLoopsShortSources(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	opts := defaultNormalizeOpts()
	opts.Duration = 7
	err := p.Normalize(context.Background(), "in.mp4", "out.mp4", opts)
	require.NoError(t, err)

	argv := runner.lastArgv
	assert.True(t, hasArgPair(argv, "-stream_loop", "-1"),
		"target duration must loop short sources; got argv: %v", argv)
	assert.True(t, hasArgPair(argv, "-t", "7"),
		"target duration must remain bounded by -t; got argv: %v", argv)
}

// TestNormalize_ContextCancellation verifies context cancellation propagates.
func TestNormalize_ContextCancellation(t *testing.T) {
	runner := &contextCaptureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	opts := defaultNormalizeOpts()
	err := p.Normalize(ctx, "in.mp4", "out.mp4", opts)
	assert.Error(t, err, "cancelled context must propagate")
}

// TestNormalize_Timeout verifies a non-zero timeout is passed to Run.
func TestNormalize_Timeout(t *testing.T) {
	runner := &captureRunnerWithBinary{}
	p := &Processor{path: "ffmpeg", runner: runner}

	opts := defaultNormalizeOpts()
	err := p.Normalize(context.Background(), "in.mp4", "out.mp4", opts)
	require.NoError(t, err)
	assert.Greater(t, runner.lastOpts.Timeout, time.Duration(0),
		"Normalize must pass a non-zero timeout")
}

// ── CutAndNormalize NoAudio tests ─────────────────────────────────────

// defaultCutAndNormalizeOpts returns minimal CutAndNormalizeOptions.
func defaultCutAndNormalizeOpts() CutAndNormalizeOptions {
	return CutAndNormalizeOptions{
		Width:  1280,
		Height: 720,
		FPS:    30,
		Codec:  "libx264",
		Preset: "veryfast",
		CRF:    18,
	}
}

// TestCutAndNormalize_NoAudio_True_AppendsAn verifies that CutAndNormalize
// with NoAudio=true appends "-an" and does NOT add AAC audio encoding.
func TestCutAndNormalize_NoAudio_True_AppendsAn(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	opts := defaultCutAndNormalizeOpts()
	opts.NoAudio = true
	err := p.CutAndNormalize(context.Background(), "in.mp4", "out.mp4",
		"00:00:05", "00:00:15", opts)
	require.NoError(t, err)
	require.Equal(t, 1, runner.calls)

	argv := runner.lastArgv
	assert.True(t, hasArg(argv, "-an"),
		"NoAudio=true must append -an; got argv: %v", argv)
	assert.False(t, hasArgPair(argv, "-c:a", "aac"),
		"NoAudio=true must NOT add -c:a aac; got argv: %v", argv)
	assert.False(t, hasArgPair(argv, "-b:a", "128k"),
		"NoAudio=true must NOT add -b:a 128k; got argv: %v", argv)
}

// TestCutAndNormalize_NoAudio_False_AddsAAC verifies that CutAndNormalize
// with NoAudio=false does NOT append "-an" and instead adds AAC encoding.
func TestCutAndNormalize_NoAudio_False_AddsAAC(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	opts := defaultCutAndNormalizeOpts()
	opts.NoAudio = false
	err := p.CutAndNormalize(context.Background(), "in.mp4", "out.mp4",
		"00:00:05", "00:00:15", opts)
	require.NoError(t, err)
	require.Equal(t, 1, runner.calls)

	argv := runner.lastArgv
	assert.False(t, hasArg(argv, "-an"),
		"NoAudio=false must NOT append -an; got argv: %v", argv)
	assert.True(t, hasArgPair(argv, "-c:a", "aac"),
		"NoAudio=false must add -c:a aac; got argv: %v", argv)
	assert.True(t, hasArgPair(argv, "-b:a", "128k"),
		"NoAudio=false must add -b:a 128k; got argv: %v", argv)
}

// TestCutAndNormalize_NoAudio_False_AudioFilter verifies that NoAudio=false
// adds the asetpts PTS-reset filter for the audio stream.
func TestCutAndNormalize_NoAudio_False_AudioFilter(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	opts := defaultCutAndNormalizeOpts()
	opts.NoAudio = false
	err := p.CutAndNormalize(context.Background(), "in.mp4", "out.mp4",
		"00:00:05", "00:00:15", opts)
	require.NoError(t, err)

	argv := runner.lastArgv
	assert.True(t, hasArgPair(argv, "-af", "asetpts=PTS-STARTPTS"),
		"NoAudio=false must add -af asetpts=PTS-STARTPTS; got argv: %v", argv)
}

// TestCutAndNormalize_CutParams verifies that -ss/-to are placed correctly
// (before -i for fast seek) and the video filter chain is present.
func TestCutAndNormalize_CutParams(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	opts := defaultCutAndNormalizeOpts()
	opts.NoAudio = true
	err := p.CutAndNormalize(context.Background(), "in.mp4", "out.mp4",
		"10", "20", opts)
	require.NoError(t, err)

	argv := runner.lastArgv
	assert.True(t, hasArgPair(argv, "-ss", "10"),
		"-ss must be present; got argv: %v", argv)
	assert.True(t, hasArgPair(argv, "-to", "20"),
		"-to must be present; got argv: %v", argv)
	assert.True(t, hasArgPair(argv, "-c:v", "libx264"),
		"codec must be passed; got argv: %v", argv)
	assert.True(t, hasArg(argv, "-vf"),
		"-vf filter must be present for normalization; got argv: %v", argv)
}

// TestCutAndNormalize_ContextCancellation verifies context cancellation propagates.
func TestCutAndNormalize_ContextCancellation(t *testing.T) {
	runner := &contextCaptureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	opts := defaultCutAndNormalizeOpts()
	err := p.CutAndNormalize(ctx, "in.mp4", "out.mp4", "5", "10", opts)
	assert.Error(t, err, "cancelled context must propagate")
}

// TestCutAndNormalize_Timeout verifies a non-zero timeout is passed to Run.
func TestCutAndNormalize_Timeout(t *testing.T) {
	runner := &captureRunnerWithBinary{}
	p := &Processor{path: "ffmpeg", runner: runner}

	opts := defaultCutAndNormalizeOpts()
	err := p.CutAndNormalize(context.Background(), "in.mp4", "out.mp4",
		"5", "10", opts)
	require.NoError(t, err)
	assert.Greater(t, runner.lastOpts.Timeout, time.Duration(0),
		"CutAndNormalize must pass a non-zero timeout")
}

// ── ApplyWatermark tests ──────────────────────────────────────────────

// defaultWatermarkOpts returns sensible WatermarkOptions for tests.
func defaultWatermarkOpts() WatermarkOptions {
	return WatermarkOptions{
		ImagePath:             "/tmp/watermark.png",
		Opacity:               0.25,
		Position:              "center",
		ScalePercent:          20,
		GreenScreenColor:      "0x00FF00",
		GreenScreenSimilarity: 0.3,
		GreenScreenBlend:      0.1,
	}
}

// TestApplyWatermark_AudioPreserved verifies that ApplyWatermark always
// uses "-c:a copy" (audio passthrough) — there is no NoAudio field.
func TestApplyWatermark_AudioPreserved(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	opts := defaultWatermarkOpts()
	err := p.ApplyWatermark(context.Background(), "in.mp4", "out.mp4", opts)
	require.NoError(t, err)
	require.Equal(t, 1, runner.calls)

	argv := runner.lastArgv
	assert.True(t, hasArgPair(argv, "-c:a", "copy"),
		"ApplyWatermark must always use -c:a copy (audio passthrough); got argv: %v", argv)
	assert.False(t, hasArg(argv, "-an"),
		"ApplyWatermark must NOT strip audio with -an; got argv: %v", argv)
}

// TestApplyWatermark_FilterComplex verifies that the filter_complex contains
// the chroma key + overlay pipeline.
func TestApplyWatermark_FilterComplex(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	opts := defaultWatermarkOpts()
	err := p.ApplyWatermark(context.Background(), "in.mp4", "out.mp4", opts)
	require.NoError(t, err)

	argv := runner.lastArgv
	assert.True(t, hasArg(argv, "-filter_complex"),
		"-filter_complex must be present; got argv: %v", argv)
	assert.True(t, hasArgSubstring(argv, "colorkey"),
		"filter must contain colorkey for green-screen removal; got argv: %v", argv)
	assert.True(t, hasArgSubstring(argv, "overlay"),
		"filter must contain overlay; got argv: %v", argv)
	assert.True(t, hasArgSubstring(argv, "colorchannelmixer"),
		"filter must contain colorchannelmixer for opacity; got argv: %v", argv)
}

// TestApplyWatermark_BothInputs verifies that both input video and watermark
// image are passed as -i arguments.
func TestApplyWatermark_BothInputs(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	opts := defaultWatermarkOpts()
	err := p.ApplyWatermark(context.Background(), "in.mp4", "out.mp4", opts)
	require.NoError(t, err)

	argv := runner.lastArgv
	assert.True(t, hasArgPair(argv, "-i", "in.mp4"),
		"first -i must be the input video; got argv: %v", argv)
	assert.True(t, hasArgPair(argv, "-i", "/tmp/watermark.png"),
		"second -i must be the watermark image; got argv: %v", argv)
}

// TestApplyWatermark_Positions verifies that different overlay positions
// produce distinct filter_complex values.
func TestApplyWatermark_Positions(t *testing.T) {
	positions := []struct {
		pos  string
		spec string // expected overlay coordinate substring
	}{
		{"top-right", "(W-w-20):20"},
		{"top-left", "20:20"},
		{"bottom-right", "(W-w-20):(H-h-20)"},
		{"bottom-left", "20:(H-h-20)"},
		{"center", "(W-w)/2:(H-h)/2"},
	}

	for _, tc := range positions {
		t.Run(tc.pos, func(t *testing.T) {
			runner := &captureRunner{}
			p := &Processor{path: "ffmpeg", runner: runner}

			opts := defaultWatermarkOpts()
			opts.Position = tc.pos
			err := p.ApplyWatermark(context.Background(), "in.mp4", "out.mp4", opts)
			require.NoError(t, err)

			argv := runner.lastArgv
			assert.True(t, hasArgSubstring(argv, tc.spec),
				"position %q must produce overlay spec %q; got argv: %v",
				tc.pos, tc.spec, argv)
		})
	}
}

// TestApplyWatermark_MissingImagePath returns an error when ImagePath is empty.
func TestApplyWatermark_MissingImagePath(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	opts := defaultWatermarkOpts()
	opts.ImagePath = ""
	err := p.ApplyWatermark(context.Background(), "in.mp4", "out.mp4", opts)
	assert.Error(t, err, "empty ImagePath must return error")
	assert.Equal(t, 0, runner.calls, "Run must NOT be called when ImagePath is empty")
}

// TestApplyWatermark_DefaultOpacity verifies that Opacity<=0 defaults to 0.25
// (tested indirectly via the filter_complex substring containing "aa=0.25").
func TestApplyWatermark_DefaultOpacity(t *testing.T) {
	runner := &captureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	opts := defaultWatermarkOpts()
	opts.Opacity = 0 // should default to 0.25
	err := p.ApplyWatermark(context.Background(), "in.mp4", "out.mp4", opts)
	require.NoError(t, err)

	argv := runner.lastArgv
	assert.True(t, hasArgSubstring(argv, "aa=0.25"),
		"Opacity=0 must default to 0.25 (aa=0.25 in filter); got argv: %v", argv)
}

// TestApplyWatermark_ContextCancellation verifies context cancellation propagates.
func TestApplyWatermark_ContextCancellation(t *testing.T) {
	runner := &contextCaptureRunner{}
	p := &Processor{path: "ffmpeg", runner: runner}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	opts := defaultWatermarkOpts()
	err := p.ApplyWatermark(ctx, "in.mp4", "out.mp4", opts)
	assert.Error(t, err, "cancelled context must propagate")
}

// TestApplyWatermark_Timeout verifies a non-zero timeout is passed to Run.
func TestApplyWatermark_Timeout(t *testing.T) {
	runner := &captureRunnerWithBinary{}
	p := &Processor{path: "ffmpeg", runner: runner}

	opts := defaultWatermarkOpts()
	err := p.ApplyWatermark(context.Background(), "in.mp4", "out.mp4", opts)
	require.NoError(t, err)
	assert.Greater(t, runner.lastOpts.Timeout, time.Duration(0),
		"ApplyWatermark must pass a non-zero timeout")
}
