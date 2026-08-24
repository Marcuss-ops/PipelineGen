// Package ports holds shared asset capability ports and DTOs used by both
// application/assets/ and capabilities/assets/ without creating circular
// imports. Per godlike/06 SSOT: one canonical owner per fact — these types
// live here so capabilities never imports application.
package ports

import (
	"context"
	"io"
	"time"

	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── ProcessResult / ProcessRunner / ProcessOptions ──────────────────

type ProcessResult struct {
	Stdout string
	Stderr string
	Output string
}

type ProcessRunner interface {
	Run(ctx context.Context, name string, args []string, opts ProcessOptions) (*ProcessResult, error)
	RunSimple(ctx context.Context, name string, args ...string) (*ProcessResult, error)
}

func DefaultProcessOptions() ProcessOptions {
	return ProcessOptions{Timeout: 10 * time.Minute, CombinedOutput: true}
}

type ProcessOptions struct {
	WorkDir        string
	CombinedOutput bool
	Timeout        time.Duration
}

// ── ToolChecker ─────────────────────────────────────────────────────

type ToolChecker interface {
	CommandExists(name string) bool
	LookPath(name string) (string, error)
}

// ── DBHealthCheckResult / DBHealthChecker ───────────────────────────

type DBHealthCheckResult struct {
	OK    bool
	Error string
}

type DBHealthChecker interface {
	GetAllDBs() []string
	GetDBPath(dataDir, relPath string) string
	Ping(ctx context.Context, dbPath string) DBHealthCheckResult
}

// ── Backward-compatibility aliases to kernel types ──────────────────

type (
	SourceRef    = asset.SourceRef
	StagedSource = asset.StagedSource
	StagedAsset  = asset.StagedAsset
)

// ── MediaDownloader ─────────────────────────────────────────────────

type MediaDownloader interface {
	Download(ctx context.Context, url string) (io.ReadCloser, error)
}

// ── MaintenanceRepository ───────────────────────────────────────────

type MaintenanceRepository interface {
	DeleteOldAPIRequests(ctx context.Context, retentionDays int) (int64, error)
	WALCheckpoint(ctx context.Context, mode string) error
	IncrementalVacuum(ctx context.Context, pages int) error
	FullVacuum(ctx context.Context) error
	ScanLocalOrphans(ctx context.Context, batch int) ([]LocalOrphanCandidate, error)
	ScanDriveOrphans(ctx context.Context, batch int) ([]DriveOrphanCandidate, error)
	MarkLocalOrphan(ctx context.Context, id string, detectedAt time.Time) error
	MarkDriveOrphan(ctx context.Context, id string, detectedAt time.Time) error
}

type LocalOrphanCandidate struct {
	ID             string
	LocalPath      string
	Size           int64
	AlreadyOrphan  string
	PrevDetectedAt string
}

type DriveOrphanCandidate struct {
	ID             string
	DriveLink      string
	AlreadyOrphan  string
	PrevDetectedAt string
}