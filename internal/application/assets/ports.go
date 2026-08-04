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

// ── SourceStager port (PR-MEDIATRANSFORMER-RENAME, July 2026) ────────────
//
// assets.SourceStager is the LEGACY per-call staging port. It downloads
// source media into a temp location that the caller owns and must
// explicitly Cleanup after use. Every call to StageSource allocates a
// fresh download; there is no persistent registry, no TTL eviction, no
// cross-call dedupe.
//
// CURRENT CONSUMERS (July 2026):
//   - Stock pipeline (legacy compatibility surface still used by the
//     stock orchestrator and fixtures)
//   - Images and jobs/assets (StageSourceV2 compatibility surface)
//
// MIGRATED CONSUMERS: YouTube and Artlist now use
// acquisition.SourceStager directly. Do not add new consumers here.
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
//
// PR-MEDIATRANSFORMER-RENAME (July 2026): SourceRef + StagedSource
// are NOT defined here — they live in the domain layer at
// `internal/kernel/asset/staged_source.go` (the canonical SSOT per
// godlike/06). The domain types are imported as `asset.SourceRef`
// and `asset.StagedSource` via the `asset` import alias. The
// SourceStager port signature uses the domain types so the port
// itself stays free of application-layer concerns.

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
//
// PR-MEDIATRANSFORMER-RENAME (July 2026): the StageSourceV2 and
// CleanupStagedSource methods use the domain-layer
// `asset.SourceRef` and `asset.StagedSource` types (imported via
// the `asset` import alias). The application-layer `assets`
// package is NOT the owner of these types — it only references
// them through the port.
type SourceStager interface {
	StageSource(ctx context.Context, ref asset.SourceRef) (*StagedAsset, error)
	Cleanup(ctx context.Context, staged *StagedAsset) error
	StageSourceV2(ctx context.Context, ref asset.SourceRef) (*asset.StagedSource, error)
	CleanupStagedSource(ctx context.Context, staged *asset.StagedSource) error
}

// ── Backward-compatibility aliases (PR-MEDIATRANSFORMER-RENAME, July 2026) ──
//
// These Go type aliases let the ~50 existing callers in
// `internal/application/{youtube,clips,artlist,voiceover}` and
// `internal/infrastructure/{stager,media}` continue to reference
// `assets.SourceRef` and `assets.StagedSource` without churn. The
// canonical SSOT lives in the domain layer at
// `internal/kernel/asset/staged_source.go`; the aliases are
// transparent forwarders that resolve to the same underlying type
// at compile time.
//
// Deprecated: use `asset.SourceRef` and `asset.StagedSource`
// (the domain types) directly. The aliases are removed in
// PR-MEDIATRANSFORMER-RENAME step 2 when the forbidden fields
// are deleted from RenditionSet and all callers migrate to the
// domain import.
type (
	SourceRef    = asset.SourceRef
	StagedSource = asset.StagedSource
)
