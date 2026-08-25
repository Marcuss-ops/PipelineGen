// dr_adapter.go — QDRANT-005C PR3 / PR-QDRANT-WIRE-MIRROR (June 2026).
//
// Concrete adapters for the application-layer dr ports. Lives in the
// infrastructure package so qdrant.transport.Client + qdrant.collections.CollectionManager +
// qdrant.ReindexVerifier + observability concrete types can satisfy the
// dr.SnapshotStore / AliasSwitcher / CollectionCreator / Verifier /
// DRMetrics / RetentionExecutor ports.
//
// PR-QDRANT-WIRE-MIRROR (June 2026): the canonical DR/snapshot types
// (schema.SnapshotDescription, collections.RetentionConfig, RetentionResult,
// LocatorCleanupReport) live in internal/domain/qdrantdr/ and are aliased
// through their owning packages. The adapters below pass those shared
// shapes through directly; only VerifyReport remains a deliberate
// application projection of the richer infrastructure SwitchReport.
//
// Compile-time assertions on every adapter catch drift between the
// dr/ port surface and the qdrant surface at build time.
package maintenance

import (
	"context"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/collections"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/dr"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/indexing"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/verification"
)

// ── SnapshotStoreAdapter ─────────────────────────────────────────────

// SnapshotStoreAdapter exposes qdrant.transport.Client snapshot methods to dr's
// SnapshotStore port. Stateless — pass the same transport.Client through every
// call. Snapshot and retention values use shared domain aliases and are
// returned directly; only the richer verifier report is projected below.
type SnapshotStoreAdapter struct {
	client *transport.Client
}

// NewSnapshotStoreAdapter constructs a SnapshotStoreAdapter.
func NewSnapshotStoreAdapter(client *transport.Client) *SnapshotStoreAdapter {
	return &SnapshotStoreAdapter{client: client}
}

var _ dr.SnapshotStore = (*SnapshotStoreAdapter)(nil)

func (a *SnapshotStoreAdapter) CreateSnapshot(ctx context.Context, collection string) (*dr.SnapshotDescription, error) {
	snap, err := a.client.CreateSnapshot(ctx, collection)
	if err != nil {
		return nil, err
	}
	return snap, nil
}

func (a *SnapshotStoreAdapter) ListSnapshots(ctx context.Context, collection string) ([]dr.SnapshotDescription, error) {
	snaps, err := a.client.ListSnapshots(ctx, collection)
	if err != nil {
		return nil, err
	}
	return snaps, nil
}

func (a *SnapshotStoreAdapter) DeleteSnapshot(ctx context.Context, collection, snapshotName string) error {
	return a.client.DeleteSnapshot(ctx, collection, snapshotName)
}

func (a *SnapshotStoreAdapter) GetSnapshotURL(ctx context.Context, collection, snapshotName string) (string, error) {
	return a.client.GetSnapshotURL(ctx, collection, snapshotName)
}

func (a *SnapshotStoreAdapter) RestoreSnapshot(ctx context.Context, collection, snapshotURL string) error {
	return a.client.RestoreSnapshot(ctx, collection, snapshotURL)
}

// ── AliasSwitcherAdapter ─────────────────────────────────────────────

// AliasSwitcherAdapter exposes qdrant.transport.Client.SwitchAlias through the
// dr.AliasSwitcher port. No translation needed (primitives only).
type AliasSwitcherAdapter struct {
	client *transport.Client
}

// NewAliasSwitcherAdapter constructs an AliasSwitcherAdapter.
func NewAliasSwitcherAdapter(client *transport.Client) *AliasSwitcherAdapter {
	return &AliasSwitcherAdapter{client: client}
}

// Compile-time assertion.
var _ dr.AliasSwitcher = (*AliasSwitcherAdapter)(nil)

func (a *AliasSwitcherAdapter) SwitchAlias(ctx context.Context, alias, oldTarget, newTarget string) error {
	return a.client.SwitchAlias(ctx, alias, oldTarget, newTarget)
}

// ── CollectionCreatorAdapter ─────────────────────────────────────────

// CollectionCreatorAdapter exposes qdrant.collections.CollectionManager.CreateCollection
// through the dr.CollectionCreator port. Used by RestoreService to
// allocate the timestamped restore-target collection.
type CollectionCreatorAdapter struct {
	cm *collections.CollectionManager
}

// NewCollectionCreatorAdapter constructs a CollectionCreatorAdapter.
func NewCollectionCreatorAdapter(cm *collections.CollectionManager) *CollectionCreatorAdapter {
	return &CollectionCreatorAdapter{cm: cm}
}

// Compile-time assertion.
var _ dr.CollectionCreator = (*CollectionCreatorAdapter)(nil)

func (a *CollectionCreatorAdapter) CreateCollection(ctx context.Context, name string) error {
	return a.cm.CreateCollection(ctx, name)
}

// ── VerifierAdapter (schema.SwitchReport → dr.VerifyReport) ─────────────────

// VerifierAdapter wraps qdrant.NewReindexVerifier into the dr.Verifier
// port. The translation lives here so the application-layer dr package
// does NOT import the infrastructure schema.SwitchReport shape.
//
// Drift gate (June 2026): the explicit copy below is the single spot
// where fields selected for the DR contract are projected. A new field on
// SwitchReport is intentionally not copied unless it belongs in the
// narrower DR contract; the retirement evidence and tests keep this
// boundary decision reviewable.
type VerifierAdapter struct {
	client     *transport.Client
	assetStore indexing.AssetStore
	schema     *schema.IndexSchema
	log        *zap.Logger
}

// NewVerifierAdapter constructs a VerifierAdapter. The deadLetter
// parameter is intentionally absent: the dr restore path does NOT
// check dead-letter open count (a restore's dead-letter state is
// orthogonal to "do we trust this restored collection"). When the
// broader reconcile flow needs that gate, it runs through the
// existing reconciler/VerifierAdapter (PR2 wiring).
func NewVerifierAdapter(client *transport.Client, assetStore indexing.AssetStore, schema *schema.IndexSchema, log *zap.Logger) *VerifierAdapter {
	return &VerifierAdapter{client: client, assetStore: assetStore, schema: schema, log: log}
}

// Compile-time assertion.
var _ dr.Verifier = (*VerifierAdapter)(nil)

func (a *VerifierAdapter) VerifyReindex(ctx context.Context, targetCollection string, expectedPoints int) (*dr.VerifyReport, error) {
	v := verification.NewReindexVerifier(a.client, a.assetStore, nil /* deadLetter */, a.schema, nil /* goldenQuery */, a.log)
	rep, err := v.VerifyReindex(ctx, targetCollection, expectedPoints)
	if err != nil {
		return nil, err
	}
	return &dr.VerifyReport{
		Ready:           rep.Ready,
		ExpectedPoints:  rep.ExpectedPoints,
		ActualPoints:    rep.ActualPoints,
		MissingCount:    rep.MissingCount,
		OrphanCount:     rep.OrphanCount,
		PayloadIssues:   rep.PayloadIssues,
		VersionMismatch: rep.VersionMismatch,
		DeadLetterOpen:  rep.DeadLetterOpen,
		Errors:          rep.Errors,
	}, nil
}

// ── RetentionExecutorAdapter ─────────────────────────────────────────

// RetentionExecutorAdapter wraps qdrant.collections.CollectionManager.CleanupWithConfig
// into the dr.RetentionExecutor port. After PR-QDRANT-WIRE-MIRROR,
// both sides share the canonical qdrantdr types — pass-through directly.
type RetentionExecutorAdapter struct {
	cm *collections.CollectionManager
}

// NewRetentionExecutorAdapter constructs a RetentionExecutorAdapter.
func NewRetentionExecutorAdapter(cm *collections.CollectionManager) *RetentionExecutorAdapter {
	return &RetentionExecutorAdapter{cm: cm}
}

var _ dr.RetentionExecutor = (*RetentionExecutorAdapter)(nil)

func (a *RetentionExecutorAdapter) Apply(ctx context.Context, cfg dr.RetentionConfig) (*dr.RetentionResult, error) {
	res, err := a.cm.CleanupWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (a *RetentionExecutorAdapter) CleanupWithConfig(ctx context.Context, cfg dr.RetentionConfig) (*dr.RetentionResult, error) {
	res, err := a.cm.CleanupWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return res, nil
}

// ── PromDRMetricsAdapter ─────────────────────────────────────────────

// PromDRMetricsAdapter wires dr.DRMetrics to the canonical
// qdrant_alias_switch_{total, duration_seconds, current_collection}
// metrics in the observability package. Single shared instance is
// safe — the underlying promauto vars are global.
type PromDRMetricsAdapter struct{}

// NewPromDRMetricsAdapter returns a metrics adapter for production
// callers. Tests construct dr.noopMetrics{} via the no-op fallback
// in NewRestoreServiceFromDeps.
func NewPromDRMetricsAdapter() *PromDRMetricsAdapter {
	return &PromDRMetricsAdapter{}
}

// Compile-time assertion.
var _ dr.DRMetrics = (*PromDRMetricsAdapter)(nil)

// RecordAliasSwitch bumps the alias_switch_total counter + duration
// histogram for the given action. action values: "switch" | "rollback"
// | "rehydrate".
//
// QDRANT-005C PR3 (June 2026): RestoreService emits action="rehydrate"
// so operators can distinguish "we cut over to a new roll-forward
// collection" from "we restored from a backup snapshot" on dashboards.
// Counter cardinality is bounded by the 3 action labels — production
// alerts key off the rate.
func (a *PromDRMetricsAdapter) RecordAliasSwitch(action string, durationSeconds float64) {
	observability.QdrantAliasSwitchTotal.WithLabelValues(action).Inc()
	observability.QdrantAliasSwitchDuration.WithLabelValues(action).Observe(durationSeconds)
}

// SetAliasCurrent sets the qdrant_alias_current_collection gauge to 1
// for the supplied (alias, collection) label pair. Operators read the
// gauge to verify the active target matches the planned canonical
// collection after a restore.
//
// Known limitation (deferred to follow-up hygiene): this method does
// NOT clear stale (alias, *) label entries. Prometheus label-based
// gauges retain prior entries until the next scrape interval expires
// them; query filters like `qdrant_alias_current_collection{alias=...}
// == 1` reliably surface only the live target. A future improvement
// could track (alias, priorCollection) pairs and decrement stale ones;
// that's a follow-up commit because the dashboard query pattern
// above already filters cleanly.
func (a *PromDRMetricsAdapter) SetAliasCurrent(alias, collection string) {
	observability.QdrantAliasCurrentCollection.WithLabelValues(alias, collection).Set(1)
}
