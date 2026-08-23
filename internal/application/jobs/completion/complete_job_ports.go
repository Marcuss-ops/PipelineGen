// Package completion — complete_job_ports.go (split July 2026).
//
// This file owns the canonical port interfaces and domain row types
// for the completion service. Extracted from complete_job_service.go
// per AGENTS.md Pattern 5.
//
// godlike/06 SSOT: each port interface is the single canonical owner
// of its contract. TxContext is the single in-transaction port surface.
package completion

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs/completion/internal"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
)

// ── Port interfaces — canonical surface lives in internal/primitives.go ──
// (godlike/06 SSOT: one canonical owner per fact; these are type aliases)

type (
	IdempotencyCachePort = internal.IdempotencyCachePort
)

// CompleteJobTxRunner is NOT aliased to internal.CompleteJobTxRunner
// because completion.TxContext differs from internal.TxContext
// (GetJob returns *completion.JobRow with extra JobType field).
type CompleteJobTxRunner interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context, tx TxContext) error) error
}

// TxContext is NOT aliased to internal.TxContext because completion.JobRow
// has an extra JobType field. The two surfaces are intentionally distinct
// — the completion package owns the JobType-aware row shape.
type TxContext interface {
	GetJob(ctx context.Context, jobID string) (*JobRow, error)
	UpdateJobToSucceededCAS(ctx context.Context, jobID, leaseID string, attempt int) (int64, error)
	InsertResultOnConflict(ctx context.Context, jobID string, attempt int, codecID string, payload []byte, resultHash string) (rowID int64, replayed bool, err error)
	GetPriorArtifactHashes(ctx context.Context, jobID string) (map[string]PriorArtifactHash, error)
	PersistArtifactMap(ctx context.Context, jobID string, attempt int, entries []ArtifactMapEntry) error
	InsertOutboxEnvelope(ctx context.Context, envelope OutboxEnvelope) error
	InsertAssetLocations(ctx context.Context, entries []AssetLocationEntry) error
}

// ── JobTypeRegistry (Pattern 0 port for SSOT policy lookup) ───────────

// CompleteWithArtifactsSender is the Pattern 0 port (godlike/06 SSOT)
// for the Sender-side atomic terminal surface consumed by
// PublishAndCompleteUseCase. The single method mirrors
// WithArtifactsService.CompleteWithArtifacts — the canonical
// post-P0-COMPL-4 dedup-closure surface.
type CompleteWithArtifactsSender interface {
	CompleteWithArtifacts(ctx context.Context, req *remote.CompleteWithArtifactsRequest, published []*finalization.PublishedArtifact) (*remote.CompleteWithArtifactsResponse, error)
}

// Compile-time pin (AGENTS.md Pattern 0): drift in the concrete
// WithArtifactsService.CompleteWithArtifacts signature is a build
// failure (the interface anchor catches signature drift at compile).
var _ CompleteWithArtifactsSender = (*WithArtifactsService)(nil)

// JobTypeRegistry is the typed port for "does this job type produce
// artifacts". godlike/06 SSOT: the application-layer JobRegistry
// (`internal/application/jobs/registry.go::Registry`) is the SINGLE
// canonical owner of this fact — NOT the request envelope, NOT the
// SQL column. The in-TX typed-error gate in completeInTx uses this
// port to look up the policy by jobRow.JobType (fetched in-TX) and
// reject legacy-shape requests for artifact-producing jobs with
// remote.ErrCompleteJobPathViolation.
//
// EXPAND-phase semantics (godlike/07 fail-closed at composition): the
// port is OPTIONAL on the Service struct. When registry == nil the
// in-TX gate is silently skipped (backward-compat for callers that
// haven't yet wired the JobTypeRegistry). The BACKFILL phase wires
// the registry via Service.WithJobTypeRegistry(impl) at the composition
// root. The CONTRACT phase promotes registry to a non-optional
// constructor argument (fail-closed at boot if nil). Today this PR
// ships the surface live + 3 TDD tests + the canonical sentinel
// declaration at internal/domain/remote/complete_job.go.
type JobTypeRegistry interface {
	// ProducesArtifacts returns true if jobs of the given type MUST
	// route through CompleteWithArtifacts (and may NOT route through
	// the legacy Complete path). The registry is the SSOT; the SQL
	// map at SQLiteStore.producesArtifacts is a propagation copy
	// seeded via Registry.ProducesArtifactsMap() at boot.
	ProducesArtifacts(jobType string) bool
}

// ── Domain row types — canonical surface lives in internal/primitives.go ──
// (godlike/06 SSOT: one canonical owner per fact; these are type aliases)

type (
	PriorArtifactHash  = internal.PriorArtifactHash
	ArtifactMapEntry   = internal.ArtifactMapEntry
	OutboxEnvelope     = internal.OutboxEnvelope
	AssetLocationEntry = internal.AssetLocationEntry
)

// JobRow extends the internal.JobRow with JobType (needed for the
// in-TX typed-error gate in completeInTx that looks up
// JobTypeRegistry.ProducesArtifacts per job type). Not a type alias
// because the internal shape is intentionally narrower.
type JobRow struct {
	internal.JobRow
	JobType string
}
