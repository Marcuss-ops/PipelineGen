// Package dr — QDRANT-005C PR3 (June 2026): DR/snapshots domain ports.
//
// Carries the application-layer SnapshotService / RestoreService /
// RetentionService and the ports (SnapshotStore / AliasSwitcher /
// CollectionCreator / Verifier / DRMetrics / RetentionExecutor) they
// depend on. Infrastructure-layer adapters live in
// internal/infrastructure/qdrant/dr_adapter.go (qdrant.Client +
// qdrant.CollectionManager + observability metrics).
//
// Why a dedicated package: the canonical search-flow package
// (qdrant.SearchService) is already coupled to PayloadMapper and lives
// at the application/infrastructure boundary. Adding the DR paths
// in-place would create circular import pressure on
// PayloadMapper.AssetToPoint. A separate dr/ package is the
// lease-expensive surface for opt-in scope (admin paths only).
//
// Architecture map (PR3 deliverable):
//
//   cmd/admin/dr_qdrant.go        ─► dr.SnapshotService / dr.RestoreService / dr.RetentionService
//   internal/application/qdrant/dr/ports.go    ─► port interfaces
//   internal/application/qdrant/dr/snapshot.go ─► SnapshotService (Take / List / Delete)
//   internal/application/qdrant/dr/restore.go  ─► RestoreService (Verify-then-Switch)
//   internal/application/qdrant/dr/retention.go ─► RetentionService (SafeKeep + Drop)
//   internal/application/qdrant/dr/types.go    ─► DR-owned canonical types
//   internal/infrastructure/qdrant/dr_adapter.go ─► SnapshotStore / AliasSwitcher / CollectionCreator / Verifier / DRMetrics / RetentionExecutor adapters
//   internal/infrastructure/observability/metrics.go ─► qdrant_alias_switch_{total, duration, current}
//
// Cycle break (June 2026): dr owns the 3 canonical DR types
// (SnapshotDescription, RetentionConfig, RetentionResult — see dr/types.go).
// infra owns mirror copies; dr_adapter.go loses no cycles translating
// at the seam. This pattern keeps Clean Architecture: ports own types,
// adapters adapt wire-bound types to satisfy the ports.
//
// Mirror of PR2 (reconciler/) ServiceDeps pattern: each service has a
// `XServiceDeps` struct + `NewXServiceFromDeps(deps) *XService` ctor
// that panics on nil required ports + falls back to no-op for optional
// ones. Tests substitute stub adapters; production wires concrete
// qdrant adapters.
package dr

import (
	"context"
	"time"
)

// SnapshotStore is the port through which SnapshotService + RestoreService
// reach Qdrant's /snapshots REST endpoints. Implementation: qdrant.Client
// (low-level HTTP) wrapped by SnapshotStoreAdapter in dr_adapter.go.
type SnapshotStore interface {
	CreateSnapshot(ctx context.Context, collection string) (*SnapshotDescription, error)
	ListSnapshots(ctx context.Context, collection string) ([]SnapshotDescription, error)
	DeleteSnapshot(ctx context.Context, collection, snapshotName string) error
	GetSnapshotURL(ctx context.Context, collection, snapshotName string) (string, error)
	RestoreSnapshot(ctx context.Context, collection, snapshotURL string) error
}

// AliasSwitcher is the port through which RestoreService promotes the
// rehydrated collection into service. Implementation: qdrant.Client.SwitchAlias
// wrapped by AliasSwitcherAdapter. Required by RestoreService; nil
// means restore-then-block (alias stays at oldTarget).
type AliasSwitcher interface {
	SwitchAlias(ctx context.Context, alias, oldTarget, newTarget string) error
}

// CollectionCreator is the port through which RestoreService allocates
// the timestamped restore-target collection. Implementation:
// qdrant.CollectionManager.CreateCollection wrapped by CollectionCreatorAdapter.
// Required by RestoreService; nil fails the restore with a clear error.
type CollectionCreator interface {
	CreateCollection(ctx context.Context, name string) error
}

// Verifier is the port through which RestoreService gates the alias
// switch on post-restore integrity. Implementation: qdrant.ReindexVerifier
// wrapped by VerifierAdapter (which translates SwitchReport -> dr.VerifyReport
// so the application layer stays free of infrastructure types).
type Verifier interface {
	VerifyReindex(ctx context.Context, targetCollection string, expectedPoints int) (*VerifyReport, error)
}

// VerifyReport mirrors qdrant.SwitchReport at the dr-package level so
// the application layer doesn't import the qdrant infrastructure
// SwitchReport type. Fields are 1-to-1: drift is caught at compile
// time by dr_adapter.go's VerifierAdapter manual field-copy (a missed
// field surfaces immediately as a Go compile error there, not as a
// silent zero at runtime).
type VerifyReport struct {
	Ready           bool
	ExpectedPoints  int
	ActualPoints    int
	MissingCount    int
	OrphanCount     int
	PayloadIssues   int
	VersionMismatch int
	DeadLetterOpen  int
	Errors          []string
}

// DRMetrics is the port for emitting alias_switch_* observability
// (PR3 wiring). The concrete Prometheus adapter lives in
// dr_adapter.go (PromDRMetricsAdapter). Required by RestoreService
// (preserves the rehydrate signal) and RetentionService (preserves
// the safe-keep signal); nilling the port at construction is silently
// safe (noopMetrics).
type DRMetrics interface {
	RecordAliasSwitch(action string, durationSeconds float64)
	SetAliasCurrent(alias, collection string)
}

// RetentionExecutor is the port through which RetentionService
// delegates to the canonical retentor. Implementation:
// qdrant.CollectionManager.CleanupWithConfig wrapped by
// RetentionExecutorAdapter in dr_adapter.go (which translates
// dr.RetentionConfig → qdrant.RetentionConfig and back).
type RetentionExecutor interface {
	CleanupWithConfig(ctx context.Context, cfg RetentionConfig) (*RetentionResult, error)
}

// ── Default / no-op implementations ─────────────────────────────────

// noopMetrics is the canonical no-op DRMetrics adapter used when the
// caller hasn't supplied a concrete Prometheus binding. Keeps the
// port viable in tests that intentionally don't exercise metrics.
type noopMetrics struct{}

func (noopMetrics) RecordAliasSwitch(_ string, _ float64) {}
func (noopMetrics) SetAliasCurrent(_ string, _ string)      {}

// NowFunc is the canonical clock source for RestoreService — used by
// tests to inject deterministic times for the timestamped target
// collection name. Production wires time.Now; tests inject a const.
var NowFunc = time.Now
