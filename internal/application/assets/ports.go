package assets

import (
	"context"
	"time"
)

// ProcessResult is the output of a subprocess execution.
type ProcessResult struct {
	Stdout string
	Stderr string
	Output string
}

// ProcessRunner is the port interface for running external processes.
// The composition root injects an infrastructure implementation.
type ProcessRunner interface {
	Run(ctx context.Context, name string, args []string, opts ProcessOptions) (*ProcessResult, error)
	RunSimple(ctx context.Context, name string, args ...string) (*ProcessResult, error)
}

// DefaultProcessOptions returns sensible defaults (10m timeout, combined output).
func DefaultProcessOptions() ProcessOptions {
	return ProcessOptions{Timeout: 10 * time.Minute, CombinedOutput: true}
}

// ProcessOptions configures how a process is executed.
type ProcessOptions struct {
	WorkDir        string
	CombinedOutput bool
	Timeout        time.Duration
}

// ToolChecker is the port interface for checking if external binaries exist.
type ToolChecker interface {
	CommandExists(name string) bool
	LookPath(name string) (string, error)
}

// DBHealthCheckResult is the result of a database health check.
type DBHealthCheckResult struct {
	OK    bool
	Error string
}

// DBHealthChecker is the port interface for checking database health.
// The composition root injects an infrastructure implementation.
type DBHealthChecker interface {
	GetAllDBs() []string
	GetDBPath(dataDir, relPath string) string
	Ping(ctx context.Context, dbPath string) DBHealthCheckResult
}

// ── SourceStager port (Step 9/12, July 2026) ────────────────────────────
//
// assets.SourceStager is the LEGACY per-call staging port. It downloads
// source media into a temp location that the caller owns and must
// explicitly Cleanup after use. Every call to StageSource allocates a
// fresh download; there is no persistent registry, no TTL eviction, no
// cross-call dedupe.
//
// CURRENT CONSUMERS (July 2026):
//   - YouTube (usecase/process_segment.go Step 4a — pre-stage full video)
//   - Artlist (stager_adapter.go — per-asset download)
//
// CANONICAL REPLACEMENT: acquisition.SourceStager
//   - internal/application/acquisition/port.go
//   - Persistent staging with Prepare/Release lifecycle, TTL eviction,
//     deterministic CleanupToken for idempotent release-on-retry.
//   - Stock pipeline already consumes acquisition.SourceStager
//     (internal/application/assets/providers/stock/stockpipeline/).
//   - Forward-pointer: YouTube and Artlist will migrate to
//     acquisition.SourceStager per §12-4.2 (tracked in
//     architecture/deprecations.yaml#ASSETS-SOURCESTAGER-LEGACY).
//
// SourceStager MUST NOT decide asset lifecycle transitions, emit
// Qdrant upserts, or decide Drive destinations. It just stages bytes
// to disk and returns the local path.
//
// Per Pattern 0 (AGENTS.md): the port lives at the application layer;
// concrete implementations live in infrastructure or provider packages.

// SourceRef identifies what to download. URL is the canonical source
// locator (e.g. a YouTube video URL, an Artlist m3u8, a stock clip URL).
//
// DownloadSection is an optional time range (yt-dlp format, e.g.
// "*00:01:20-00:01:35"). Empty means "download the full asset".
// ForceKeyframes forces keyframe-aligned cuts for time-section downloads.
// MergeFormat sets the output container (e.g. "mp4").
type SourceRef struct {
	URL             string
	DownloadSection string
	ForceKeyframes  bool
	MergeFormat     string
}

// StagedAsset carries the result of a successful StageSource call.
// The file at LocalPath is ready for subsequent processing (cut,
// transcode, upload). Bytes is the on-disk size.
//
// SourceID is the canonical locator that produced this staged file
// (e.g. the YouTube URL, Artlist m3u8, stock clip URL). Callers use
// it to correlate staged files with their originating ClipPlan
// entries when multiple sources are staged in one orchestrator run.
//
// DurationSec is the probed source duration in seconds, populated
// when known (e.g. yt-dlp --print-duration at staging time, or the
// ffprobe fallback in step_extract_clips). When > 0, downstream
// consumers (stock.extract_clips in particular) use it as the
// pre-cut validation surface for clip EndSec bounds checking
// (PR-STOCK-TIMESTAMP-CLIPS Front 5, July 2026). When 0 the
// downstream step must fall back to its own probe or skip the
// bounds check (godlike/07 fail-open: production composition roots
// without a SourceDurationProbe wired keep the legacy unvalidated
// path; PR-STOCK-SOURCE-DURATION-WIRE is the forward-pointer for
// production wiring).
type StagedAsset struct {
	LocalPath   string
	Bytes       int64
	SourceID    string
	DurationSec float64
}

// SourceStager downloads source media into a staging location and
// returns the staged file path. Cleanup removes staged files when the
// caller no longer needs them.
type SourceStager interface {
	StageSource(ctx context.Context, ref SourceRef) (*StagedAsset, error)
	Cleanup(ctx context.Context, staged *StagedAsset) error
}
