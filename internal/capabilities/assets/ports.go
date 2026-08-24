// Package assets — port surface. Shared types live in
// capabilities/assets/ports/ (godlike/06 SSOT). This file re-exports
// them so existing consumers keep compiling.
package assets

import (
	"context"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/ports"
)

// ── MaintenanceRepository (application-specific — not shared) ──────

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
	AlreadyOrphan  string
	PrevDetectedAt string
}

type DriveOrphanCandidate struct {
	ID             string
	DriveLink      string
	AlreadyOrphan  string
	PrevDetectedAt string
}

// ── Re-exports from capabilities/assets/ports ──────────────────────

type ProcessResult = ports.ProcessResult
type ProcessRunner = ports.ProcessRunner
type ProcessOptions = ports.ProcessOptions
type ToolChecker = ports.ToolChecker
type DBHealthCheckResult = ports.DBHealthCheckResult
type DBHealthChecker = ports.DBHealthChecker
type MediaDownloader = ports.MediaDownloader

type SourceRef = ports.SourceRef
type StagedSource = ports.StagedSource
type StagedAsset = ports.StagedAsset

var DefaultProcessOptions = ports.DefaultProcessOptions
