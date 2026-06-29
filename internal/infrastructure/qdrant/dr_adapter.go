// dr_adapter.go — QDRANT-005C PR3 / PR-QDRANT-WIRE-MIRROR (June 2026).
//
// Concrete adapters for the application-layer dr ports. Lives in the
// infrastructure package so qdrant.Client + qdrant.CollectionManager +
// qdrant.ReindexVerifier + observability concrete types can satisfy the
// dr.SnapshotStore / AliasSwitcher / CollectionCreator / Verifier /
// DRMetrics / RetentionExecutor ports.
//
// PR-QDRANT-WIRE-MIRROR (June 2026): the canonical DR/snapshot types
// (SnapshotDescription, RetentionConfig, RetentionResult) live in
// internal/domain/qdrantdr/ and are aliased through dr/. The wire-side
// REST decoders in qdrant/types_dr.go keep a distinct struct family
// (JSON tags mirror qdrantdr/, but Go treats them as separate types)
// so the RPC decoders in client_dr.go don't pull in any application
// dependency. The adapters below bridge the two at the boundary
// via snapshotToDr / retentionToDr (see helpers).
//
// Compile-time assertions on every adapter catch drift between the
// dr/ port surface and the qdrant surface at build time.
package qdrant

import (
	"context"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/qdrant/dr"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
)

// ── SnapshotStoreAdapter ─────────────────────────────────────────────

// SnapshotStoreAdapter exposes qdrant.Client snapshot methods to dr's
// SnapshotStore port. Stateless — pass the same Client through every
// call. Field-by-field translation lives in snapshotToDr (see below).
type SnapshotStoreAdapter struct {
	client *Client
}

// NewSnapshotStoreAdapter constructs a SnapshotStoreAdapter.
func NewSnapshotStoreAdapter(client *Client) *SnapshotStoreAdapter {
	return &SnapshotStoreAdapter{client: client}
}

var _ dr.SnapshotStore = (*SnapshotStoreAdapter)(nil)

func (a *SnapshotStoreAdapter) CreateSnapshot(ctx context.Context, collection string) (*dr.SnapshotDescription, error) {
	snap, err := a.client.CreateSnapshot(ctx, collection)
	if err != nil {
		return nil, err
	}
	if snap == nil {
		return nil, nil
	}
	out := snapshotToDr(*snap)
	return &out, nil
}

func (a *SnapshotStoreAdapter) ListSnapshots(ctx context.Context, collection string) ([]dr.SnapshotDescription, error) {
	snaps, err := a.client.ListSnapshots(ctx, collection)
	if err != nil {
		return nil, err
	}
	out := make([]dr.SnapshotDescription, len(snaps))
	for i := range snaps {
		out[i] = snapshotToDr(snaps[i])
	}
	return out, nil
}

// snapshotToDr converts the wire-side qdrant.SnapshotDescription (used
// by client_dr.go REST decoders) to the canonical dr.SnapshotDescription
// (alias for qdrantdr.SnapshotDescription). Field shapes are identical
// (Name / CreationTime / Size / Checksum); the copy is required because
// Go does not auto-convert between distinct named types in different
// packages.
//
// PR-0 build cleanup (June 2026): re-introduced after the PR-QDRANT-WIRE-MIRROR
// merge dropped the translation under a false-equivalence assumption —
// the wire type and canonical type are distinct Go types despite
// matching fields and JSON tags.
func snapshotToDr(s SnapshotDescription) dr.SnapshotDescription {
	return dr.SnapshotDescription{
		Name:         s.Name,
		CreationTime: s.CreationTime,
		Size:         s.Size,
		Checksum:     s.Checksum,
	}
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

// AliasSwitcherAdapter exposes qdrant.Client.SwitchAlias through the
// dr.AliasSwitcher port. No translation needed (primitives only).
type AliasSwitcherAdapter struct {
	client *Client
}

// NewAliasSwitcherAdapter constructs an AliasSwitcherAdapter.
func NewAliasSwitcherAdapter(client *Client) *AliasSwitcherAdapter {
	return &AliasSwitcherAdapter{client: client}
}

// Compile-time assertion.
var _ dr.AliasSwitcher = (*AliasSwitcherAdapter)(nil)

func (a *AliasSwitcherAdapter) SwitchAlias(ctx context.Context, alias, oldTarget, newTarget string) error {
	return a.client.SwitchAlias(ctx, alias, oldTarget, newTarget)
}

// ── CollectionCreatorAdapter ─────────────────────────────────────────

// CollectionCreatorAdapter exposes qdrant.CollectionManager.CreateCollection
// through the dr.CollectionCreator port. Used by RestoreService to
// allocate the timestamped restore-target collection.
type CollectionCreatorAdapter struct {
	cm *CollectionManager
}

// NewCollectionCreatorAdapter constructs a CollectionCreatorAdapter.
func NewCollectionCreatorAdapter(cm *CollectionManager) *CollectionCreatorAdapter {
	return &CollectionCreatorAdapter{cm: cm}
}

// Compile-time assertion.
var _ dr.CollectionCreator = (*CollectionCreatorAdapter)(nil)

func (a *CollectionCreatorAdapter) CreateCollection(ctx context.Context, name string) error {
	return a.cm.CreateCollection(ctx, name)
}

// ── VerifierAdapter (SwitchReport → dr.VerifyReport) ─────────────────

// VerifierAdapter wraps qdrant.NewReindexVerifier into the dr.Verifier
// port. The translation lives here so the application-layer dr package
// does NOT import the infrastructure SwitchReport shape.
//
// Drift gate (June 2026): the field-by-field copy below is the single
// spot where a new field on qdrant.SwitchReport must land a mirror on
// dr.VerifyReport. Go compilation will not catch the drift (both sides
// are different types) so a shipping reminder is appended after the
// switch summaries below — adding a field to one without the other is
// a silent zero at runtime.
type VerifierAdapter struct {
	client     *Client
	assetStore AssetStore
	schema     *IndexSchema
	log        *zap.Logger
}

// NewVerifierAdapter constructs a VerifierAdapter. The deadLetter
// parameter is intentionally absent: the dr restore path does NOT
// check dead-letter open count (a restore's dead-letter state is
// orthogonal to "do we trust this restored collection"). When the
// broader reconcile flow needs that gate, it runs through the
// existing reconciler/VerifierAdapter (PR2 wiring).
func NewVerifierAdapter(client *Client, assetStore AssetStore, schema *IndexSchema, log *zap.Logger) *VerifierAdapter {
	return &VerifierAdapter{client: client, assetStore: assetStore, schema: schema, log: log}
}

// Compile-time assertion.
var _ dr.Verifier = (*VerifierAdapter)(nil)

func (a *VerifierAdapter) VerifyReindex(ctx context.Context, targetCollection string, expectedPoints int) (*dr.VerifyReport, error) {
	v := NewReindexVerifier(a.client, a.assetStore, nil /* deadLetter */, a.schema, nil /* goldenQuery */, a.log)
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

// RetentionExecutorAdapter wraps qdrant.CollectionManager.CleanupWithConfig
// into the dr.RetentionExecutor port. After PR-QDRANT-WIRE-MIRROR,
// both sides share the canonical qdrantdr types — pass-through directly.
type RetentionExecutorAdapter struct {
	cm *CollectionManager
}

// NewRetentionExecutorAdapter constructs a RetentionExecutorAdapter.
func NewRetentionExecutorAdapter(cm *CollectionManager) *RetentionExecutorAdapter {
	return &RetentionExecutorAdapter{cm: cm}
}

var _ dr.RetentionExecutor = (*RetentionExecutorAdapter)(nil)

func (a *RetentionExecutorAdapter) Apply(ctx context.Context, cfg dr.RetentionConfig) (dr.RetentionResult, error) {
	res, err := a.cm.CleanupWithConfig(ctx, cfg)
	if err != nil {
		return dr.RetentionResult{}, err
	}
	if res == nil {
		return dr.RetentionResult{}, nil
	}
	return *res, nil
}

func (a *RetentionExecutorAdapter) CleanupWithConfig(ctx context.Context, cfg dr.RetentionConfig) (dr.RetentionResult, error) {
	res, err := a.cm.CleanupWithConfig(ctx, cfg)
	if err != nil {
		return dr.RetentionResult{}, err
	}
	if res == nil {
		return dr.RetentionResult{}, nil
	}
	return *res, nil
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
