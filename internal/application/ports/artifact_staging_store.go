// Package ports — ArtifactStagingStore port (Fase 5(a), July 2026).
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the artifact
// staging seam (the narrow surface between artifact creation and
// finalization). Pre-Fase-5, this concern was entangled with full asset
// persistence + lifecycle + cleanup in
// `internal/application/assets/persistence/writer.go`. Fase 5(a) re-homes
// the staging-only concern here; `writer.go` will be migrated to import
// this port (Push 5.2 caller-migration).
//
// The concrete adapter lives in Phase 5(b) at
// `internal/platform/sqlite/artifact_staging_repository.go`
// (NOT this push — only the port declaration lands today).
//
// godlike/07 minimum-blast-radius: 3 methods, narrowly typed. Each
// operation maps 1:1 to a single SQLite table mutation; lifecycle
// state transitions are out of scope (they go through JobFinalizer).
package ports

import (
	"context"
)

// ArtifactStageKey is the typed identity for an artifact-staging row.
// The StageRow's Key is the canonical input; the staging adapter
// enforces uniqueness via PRIMARY KEY on the underlying table.
//
// godlike/06 SSOT: key equality is byte-exact; a caller using
// HashKey(ArtifactRef) MUST produce the same value as HashKey on
// a reload. The staging adapter computes keys; the port only
// consumes them.
type ArtifactStageKey string

// ArtifactStagingStore is the canonical narrow port for staging
// artifacts between creation and finalization. The staging row holds
// the artifact's local path + content-hash + idempotency key + metadata
// until the JobFinalizer promotes it to a canonical asset (Push 4.1
// typed surface).
//
// godlike/07 fail-closed contract:
//
//   - Get returns `nil, nil` when the row does NOT exist (NOT
//     `nil, ErrNotFound`). Callers branch on `row == nil`.
//   - Stage is idempotent on `req.Key`: re-staging the same key
//     MERGES the new fields with the existing row, never duplicates.
//     The originating upload-side-effect (Drive publish) is governed
//     by the artifact_preparation port (out of scope for this push).
//
// The StageRequest / StageRecord / StageListEntry types are exported
// for legibility but the canonical schemas live in Phase 5(b) once
// the staging table is migrated to the application/ports contract.
// Pre-Fase-5b callers use `internal/application/assets/types.StagingRow`
// (legacy alias).
type ArtifactStagingStore interface {
	// Stage writes a new staging row (or merges fields into an
	// existing row keyed by `req.Key`). Idempotent: re-staging the
	// same Key is a no-op for matching fields + replace for
	// non-empty fields on the new request.
	//
	// Errors: typed sentinel `ErrArtifactStageInvalidKey` if `req.Key`
	// is empty (godlike/07 fail-closed at fence).
	Stage(ctx context.Context, req StageRequest) (*StageRecord, error)

	// Get returns the staging row for `key`, or `(nil, nil)` if no
	// row exists. The `nil`-returns-nil-error contract is the
	// godlike/07 idempotency-friendly probe (caller branches on
	// presence rather than an error probe).
	Get(ctx context.Context, key ArtifactStageKey) (*StageRecord, error)

	// ListByJob returns all staging rows owned by `jobID`, ordered
	// by stage-time ASC. Used by the parent aggregator to compute
	// the final aggregate state of a multi-artifact job.
	ListByJob(ctx context.Context, jobID string) ([]StageListEntry, error)
}

// StageRequest is the input to ArtifactStagingStore.Stage.
type StageRequest struct {
	Key         ArtifactStageKey
	JobID       string
	ArtifactID  string
	ContentHash string
	LocalPath   string
	MimeType    string
	Metadata    map[string]string // arbitrary key/value metadata (driver, profile, etc.)
}

// StageRecord is the result of ArtifactStagingStore.Stage / Get.
type StageRecord struct {
	Key         ArtifactStageKey
	JobID       string
	ArtifactID  string
	ContentHash string
	LocalPath   string
	MimeType    string
	Metadata    map[string]string
	StageTime   string // RFC3339 UTC timestamp from the staging adapter
}

// StageListEntry is the lightweight projection returned by
// ArtifactStagingStore.ListByJob. It omits the full Metadata map to
// keep the list query cheap (drivers, profiles, etc. are irrelevant
// for the aggregation query).
type StageListEntry struct {
	Key         ArtifactStageKey
	ArtifactID  string
	ContentHash string
	LocalPath   string
	MimeType    string
	StageTime   string
}
