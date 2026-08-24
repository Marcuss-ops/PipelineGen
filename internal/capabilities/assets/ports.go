// Package assets — port surface. Shared types live in
// capabilities/assets/ports/ (godlike/06 SSOT). This file re-exports
// them so existing consumers keep compiling.
package assets

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/ports"
)

// ── MaintenanceRepository (re-exported from ports) ───────────────────

type MaintenanceRepository = ports.MaintenanceRepository
type LocalOrphanCandidate = ports.LocalOrphanCandidate
type DriveOrphanCandidate = ports.DriveOrphanCandidate

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
