// Package dr — port surface for the Qdrant DR + Retention capability.
//
// PR 7 (June 2026, chore/remove-qdrant-legacy): the previous
// alias-shim pattern (dr/types.go re-aliasing every type from
// internal/domain/qdrantdr) is gone. Consumers in this file
// import internal/domain/qdrantdr directly; the JSON-shape
// alignment is documented in internal/domain/qdrantdr/types.go
// (the canonical home).
//
// Application-layer ports listed in the canonical DR diagram:
//
//	internal/application/qdrant/dr/ports.go         ─► SnapshotStore,
//	                                                   AliasSwitcher,
//	                                                   CollectionCreator,
//	                                                   Verifier,
//	                                                   DRMetrics,
//	                                                   CollectionAgeReader (PR 7 typed-port),
//	                                                   RetentionExecutor
//	internal/domain/qdrantdr/types.go                ─► DR-owned canonical types
//	internal/infrastructure/qdrant/dr_adapter.go     ─► type-aliases + wire-encode
package dr

import (
	"context"
	"time"

	qdrantdr "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/qdrantdr"
)

// SnapshotStore persists + restores Qdrant collection snapshots
// across compatibility-version boundaries. Production concrete:
// *qdrant.SnapshotClient in internal/infrastructure/qdrant.
type SnapshotStore interface {
	CreateSnapshot(ctx context.Context, collection string) (*SnapshotDescription, error)
	RestoreSnapshot(ctx context.Context, collection, snapshotURL string) error
	ListSnapshots(ctx context.Context, collection string) ([]SnapshotDescription, error)
	DeleteSnapshot(ctx context.Context, collection, snapshotName string) error
	GetSnapshotURL(ctx context.Context, collection, snapshotName string) (string, error)
}

// AliasSwitcher performs the blue-green atomic alias rotation that
// Production concrete: *qdrant.CollectionManager in
// internal/infrastructure/qdrant (manual admin path) +
// internal/application/qdrant/reconciler::Service (programmatic).
type AliasSwitcher interface {
	SwitchAlias(ctx context.Context, alias, oldTarget, newTarget string) error
}

// CollectionCreator provisions a new physical collection matching
// the canonical schema (current V3). Idempotent — calling twice
// with the same name + schema returns the existing collection's
// descriptor on the second call.
type CollectionCreator interface {
	CreateCollection(ctx context.Context, name string) error
}

// Verifier is the read-only gate that decides whether an alias switch
// is safe (QDRANT-002 PR10/11/12/13 invariants). Production concrete:
// internal/infrastructure/qdrant/verifier.go.
type Verifier interface {
	VerifyReindex(ctx context.Context, targetCollection string, expectedPoints int) (*VerifyReport, error)
}

// DRMetrics emits the per-collection health + drift counters consumed
// by the readiness barrier and the operator dashboard.
type DRMetrics interface {
	RecordAliasSwitch(action string, durationSeconds float64)
	SetAliasCurrent(alias, collection string)
}

// CollectionAgeReader is the canonical typed port for per-collection
// creation-time tracking. Production concrete: the AgingTable
// adapter registered at boot (see internal/infrastructure/qdrant/
// collection_manager.go for the wire-side interface and the inline
// agreement at the construction site).
//
// PR 7 (June 2026) typed-port move: previously `RetentionConfig.AgingTable`
// was typed `any` in domain/qdrantdr/types.go and callers had to
// runtime type-assert against the infra-local AgingTable interface.
// The port now lives in this application service (per spec §7.3
// placement rule), so a RetentionConfig consumer can be statically
// checked by the compiler: `cfg.AgingTable` is `CollectionAgeReader`,
// not `any`. The infra-side AgingTable interface continues to live
// in internal/infrastructure/qdrant/collection_manager.go because
// the concrete adapter satisfies the wire-format concerns (string
// RFC3339); a compile-time assertion
// `var _ dr.CollectionAgeReader = (qdrant.AgingTable)(nil)` pins the
// interface-method parity.
//
// Spec example signature uses (time.Time, ...) for the return;
// the implementation here uses (string, RFC3339) to match the
// existing infra-side wire format. The wrapper boundary can
// graduate to time.Time in a future PR without changing this port.
type CollectionAgeReader interface {
	CreatedAt(ctx context.Context, collection string) (time.Time, bool, error)
}

// RetentionExecutor applies the keep-last-N + max-age retention
// policy using the CollectionAgeReader for per-collection timestamps.
// Production concrete: *qdrant.RetentionExecutor in
// internal/infrastructure/qdrant (registered into outbox via
// internal/application/jobs/outbox DriveHandler).
type RetentionExecutor interface {
	CleanupWithConfig(ctx context.Context, cfg qdrantdr.RetentionConfig) (*qdrantdr.RetentionResult, error)
}
