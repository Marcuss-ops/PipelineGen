// Package render — concrete VideoCutter port implementation (PR6, June 2026; FASE 2.4, July 2026).
//
// This adapter owns the batch-then-fallback-to-individual FFmpeg logic
// that previously lived in stockpipeline.process.go's processSingleVideo,
// the per-job on-disk verification, and (FASE 2.4) the per-clip ffprobe
// validation step that lifts DurationSec + SizeBytes into the structured
// CutBatchResult surface.
//
// Import-boundary invariant (AGENTS.md Pattern 0 + Pattern 8):
//   - internal/application/** MUST NOT import this package.
//   - This package MAY import the stock port types from the application
//     layer (hexagonal: app owns the port, infra owns the adapter).
//
// Behavioural equivalence with pre-PR6:
//  1. Try CutReencodeBatch (single FFmpeg invocation producing N clips)
//  2. On batch failure, fall back to per-clip CutReencode invocations
//  3. Verify each produced output exists on disk (os.Stat)
//  4. (FASE 2.4) Run ffprobe on every produced clip to populate
//     DurationSec; flip Status to Validated on success.
//
// FASE 2.4 (July 2026): the legacy CutResult.{ProducedPaths-only} contract
// is replaced with CutBatchResult (always non-nil, len(Items)=len(input.Jobs)).
// Per-job failure detail (Err, JobID) is exposed to the application layer
// without forcing the caller to guess which clip failed.
//
// Phase 9 split (5 pipeline phases: probe / frame-cut / audio-cut /
// mux / post-process). This file holds ONLY the canonical FFmpegCutter
// struct, the fail-safe constructors, the fluent WithRunner helper,
// the partPath transient marker helper, and the toInternalCutJobs
// type adapter that bridges stockpipeline.CutJob → ffmpeg.CutJob.
// The Cut method body lives in cutter_cut.go. The source-duration
// probe prelude lives in cutter_probe.go. The post-cut validation
// (size/sha/canonical codec profile) lives in cutter_post_process.go.
// The .part→final atomic rename + result aggregation lives in
// cutter_mux.go. Audio handling (currently parameterized via
// CutRequest.NoAudio → ffmpeg -an/-c:a aac) is documented in
// cutter_audio.go.
package render

import (
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
)

// persistentStockWorkspace is the on-disk root used by the stock
// pipeline for durable per-job clip workspaces. Files under this
// prefix are trusted to be resumable and are validated with ffprobe
// before reuse.
const persistentStockWorkspace = "/data/stock/workspaces/"

// FFmpegCutter is the canonical concrete implementation of the
// VideoCutter port. It wraps the existing ffmpeg.Processor (kept as
// an internal collaborator so the well-tested cut-arg-assembly is
// preserved) AND lifts the batch/fallback + disk-verification + ffprobe
// logic out of the application layer.
type FFmpegCutter struct {
	proc *ffmpeg.Processor
	log  *zap.Logger
	// probeAfterCut toggles the ffprobe-validation pass; default true.
	// Slice with NewFFmpegCutterOnlyCut when a watcher path needs to
	// skip probe (typically the staging run where ProbeError is
	// irrelevant — the file is re-staged, not validated).
	probeAfterCut bool
}

// NewFFmpegCutter constructs the cutter with the canonical binary
// path + logger + ffprobe-after-cut enabled. The processor is built
// lazily from the supplied ffmpeg path (empty path defaults to
// "ffmpeg" via ffmpeg.NewProcessor).
func NewFFmpegCutter(ffmpegPath string, log *zap.Logger) *FFmpegCutter {
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &FFmpegCutter{
		proc:          ffmpeg.NewProcessor(ffmpegPath),
		log:           log,
		probeAfterCut: true,
	}
}

// NewFFmpegCutterOnlyCut constructs the cutter with the canonical
// binary path + logger + ffprobe-after-cut disabled. Used by tests
// that assemble a tiny synthetic clip and want to assert just the
// cut behaviour (and not the ffprobe-validation gate).
func NewFFmpegCutterOnlyCut(ffmpegPath string, log *zap.Logger) *FFmpegCutter {
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &FFmpegCutter{
		proc:          ffmpeg.NewProcessor(ffmpegPath),
		log:           log,
		probeAfterCut: false,
	}
}

// Compile-time check that FFmpegCutter satisfies VideoCutter.
var _ stockpipeline.VideoCutter = (*FFmpegCutter)(nil)

// WithRunner replaces the underlying ffmpeg.Processor's subprocess runner.
// Returns the receiver for fluent chaining. Used by composition-root tests
// to inject a capture runner through the full injection chain:
// FFmpegCutter → ffmpeg.Processor → ProcessRunner.
func (c *FFmpegCutter) WithRunner(r ffmpeg.ProcessRunner) *FFmpegCutter {
	c.proc.WithRunner(r)
	return c
}

// partPath returns the transient .part path for a final output path.
// FFmpeg writes here first; after validation the file is atomically
// renamed to the final path. The .part marker is inserted before the
// file extension so ffmpeg can still infer the container format from
// the original extension (e.g. clip.mp4 -> clip.part.mp4).
func partPath(final string) string {
	// Split the path so the .part marker is inserted before the file
	// extension, not somewhere in a parent directory name.
	base := filepath.Base(final)
	dir := filepath.Dir(final)
	if i := strings.LastIndex(base, "."); i > 0 {
		return filepath.Join(dir, base[:i]+".part"+base[i:])
	}
	return final + ".part"
}

// toInternalCutJobs adapts the neutral stockpipeline.CutJob list to the
// ffmpeg package's CutJob struct (which the existing Processor expects).
func toInternalCutJobs(jobs []stockpipeline.CutJob) []ffmpeg.CutJob {
	out := make([]ffmpeg.CutJob, len(jobs))
	for i, j := range jobs {
		out[i] = ffmpeg.CutJob{
			StartSec: j.StartSec,
			EndSec:   j.EndSec,
			Output:   j.OutputPath,
		}
	}
	return out
}
