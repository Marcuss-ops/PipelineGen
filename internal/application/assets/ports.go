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
// SourceStager is the shared port for downloading source media into a
// staging location. It is implemented by YouTube, stock, and Artlist
// adapters so callers (ingest pipelines, channel monitors, fetch
// providers) can stage source bytes without knowing which provider
// owns the download.
//
// SourceStager MUST NOT decide asset lifecycle transitions, emit
// Qdrant upserts, or decide Drive destinations. It just stages bytes
// to disk and returns the local path. The caller is responsible for
// cleanup of the temp directory that contains the staged file.
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
type StagedAsset struct {
	LocalPath string
	Bytes     int64
	SourceID  string
}

// SourceStager downloads source media into a staging location and
// returns the staged file path. Cleanup removes staged files when the
// caller no longer needs them.
type SourceStager interface {
	StageSource(ctx context.Context, ref SourceRef) (*StagedAsset, error)
	Cleanup(ctx context.Context, staged *StagedAsset) error
}
