package assets

import (
	"context"
	"io"
	"time"

	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── MaintenanceRepository port (extracted from maintenance package) ─────
//
// MaintenanceRepository abstracts the database operations used by the
// maintenance service. It follows the same port/adapter pattern as the
// other interfaces in this file: the application layer depends on the
// port, and infrastructure adapters (e.g. SQLite) provide the concrete
// implementation.
//
// The interface intentionally groups DB optimisation and orphan-scanning
// operations because they are always executed together by the maintenance
// service on the same set of physical databases.

type MaintenanceRepository interface {
	// DeleteOldAPIRequests removes api_requests rows older than the configured
	// retention window. It returns the number of rows deleted.
	DeleteOldAPIRequests(ctx context.Context, retentionDays int) (int64, error)

	// WALCheckpoint executes a SQLite WAL checkpoint in the given mode.
	WALCheckpoint(ctx context.Context, mode string) error

	// IncrementalVacuum runs PRAGMA incremental_vacuum(pages).
	IncrementalVacuum(ctx context.Context, pages int) error

	// FullVacuum runs a full VACUUM.
	FullVacuum(ctx context.Context) error

	// ScanLocalOrphans returns up to batch rows that have a local_path and
	// may be missing on disk. The caller decides whether to mark them.
	ScanLocalOrphans(ctx context.Context, batch int) ([]LocalOrphanCandidate, error)

	// ScanDriveOrphans returns up to batch rows that have a drive_link and
	// may point to a trashed or missing Drive file.
	ScanDriveOrphans(ctx context.Context, batch int) ([]DriveOrphanCandidate, error)

	// MarkLocalOrphan stamps metadata_json with orphan_locale=1.
	MarkLocalOrphan(ctx context.Context, id string, detectedAt time.Time) error

	// MarkDriveOrphan stamps metadata_json with orphan_drive=1.
	MarkDriveOrphan(ctx context.Context, id string, detectedAt time.Time) error
}

// LocalOrphanCandidate is a row candidate for local-orphan detection.
type LocalOrphanCandidate struct {
	ID             string
	LocalPath      string
	AlreadyOrphan  string
	PrevDetectedAt string
}

// DriveOrphanCandidate is a row candidate for drive orphan detection.
type DriveOrphanCandidate struct {
	ID             string
	DriveLink      string
	AlreadyOrphan  string
	PrevDetectedAt string
}

// MediaDownloader is the port for downloading remote media. It abstracts
// the underlying HTTP client so the ingest service stays independent of
// net/http details.
type MediaDownloader interface {
	// Download fetches the resource at url and returns a ReadCloser for the
	// response body. The caller is responsible for closing the reader.
	// Implementations must return an error for non-2xx status codes.
	Download(ctx context.Context, url string) (io.ReadCloser, error)
}

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

// ── Backward-compatibility aliases (PR-MEDIATRANSFORMER-RENAME, July 2026) ──
//
// Deprecated: use `asset.SourceRef` and `asset.StagedSource`
// (the domain types in internal/kernel/asset) directly.
type (
	SourceRef    = asset.SourceRef
	StagedSource = asset.StagedSource
)
