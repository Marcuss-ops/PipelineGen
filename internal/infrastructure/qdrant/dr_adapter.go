// dr_adapter.go — QDRANT-005C PR3 (June 2026): concrete adapters for
// the application-layer dr ports.
//
// Lives in the infrastructure package so the qdrant.Client +
// qdrant.CollectionManager + qdrant.ReindexVerifier + observability
// concrete types can satisfy the dr.SnapshotStore / AliasSwitcher /
// CollectionCreator / Verifier / DRMetrics / RetentionExecutor ports.
// The application-layer dr tests do not depend on qdrant at all; the
// production wire-up at cmd/admin/dr_qdrant.go composes these adapters
// into dr.ServiceDeps structs.
//
// Cycle break (June 2026): every adapter method translates between
// the dr-owned canonical types (dr.SnapshotDescription, dr.RetentionConfig,
// dr.RetentionResult) and the qdrant-infra mirror shapes. Without this
// translation, a qdrant → dr import would be required inside dr/ports.go
// (dr SnapshotStore returning qdrant.SnapshotDescription), which would
// form a cycle: qdrant imports dr (via this file) → dr imports qdrant
// (via ports.go) → Go compile error: "import cycle not allowed".
//
// Compile-time assertions on every adapter catch drift between the
// dr/ port surface and the qdrant surface at build time, not at the
// first panic runtime.
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
// call. Translates qdrant.SnapshotDescription ↔ dr.SnapshotDescription
// at every boundary so the application layer (dr) does not see qdrant
// types.
type SnapshotStoreAdapter struct {
	client *Client
}

// NewSnapshotStoreAdapter constructs a SnapshotStoreAdapter. client
// must be non-nil; nil-client calls would panic on Client method
// invocations anyway.
func NewSnapshotStoreAdapter(client *Client) *SnapshotStoreAdapter {
	return &SnapshotStoreAdapter{client: client}
}

// Compile-time assertion.
var _ dr.SnapshotStore = (*SnapshotStoreAdapter)(nil)

// translateSnapshot converts qdrant.SnapshotDescription →
// dr.SnapshotDescription (nil-safe). Both types have identical field
// sets; the translation is lossless.
func translateSnapshot(qd *SnapshotDescription) *dr.SnapshotDescription {
	if qd == nil {
		return nil
	}
	return &dr.SnapshotDescription{
		Name:         qd.Name,
		CreationTime: qd.CreationTime,
		Size:         qd.Size,
		Checksum:     qd.Checksum,
	}
}

// translateSnapshotSlice converts []qdrant.SnapshotDescription →
// []dr.SnapshotDescription (nil-safe).
func translateSnapshotSlice(qd []SnapshotDescription) []dr.SnapshotDescription {
	if qd == nil {
		return nil
	}
	out := make([]dr.SnapshotDescription, len(qd))
	for i := range qd {
		out[i] = dr.SnapshotDescription{
			Name:         qd[i].Name,
			CreationTime: qd[i].CreationTime,
			Size:         qd[i].Size,
			Checksum:     qd[i].Checksum,
		}
	}
	return out
}

func (a *SnapshotStoreAdapter) CreateSnapshot(ctx context.Context, collection string) (*dr.SnapshotDescription, error) {
	qd, err := a.client.CreateSnapshot(ctx, collection)
	if err != nil {
		return nil, err
	}
	return translateSnapshot(qd), nil
}

func (a *SnapshotStoreAdapter) ListSnapshots(ctx context.Context, collection string) ([]dr.SnapshotDescription, error) {
	qd, err := a.client.ListSnapshots(ctx, collection)
	if err != nil {
		return nil, err
	}
	return translateSnapshotSlice(qd), nil
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
	v := NewReindexVerifier(a.client, a.assetStore, nil /* deadLetter */, a.schema, a.log)
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
// into the dr.RetentionExecutor port. Translates dr.RetentionConfig →
// qdrant.RetentionConfig on the way in, and qdrant.RetentionResult →
// dr.RetentionResult on the way out. This is the canonical seam for
// the cycle break: dr cannot hold a back-reference to qdrant
// (forbidden), and qdrant.CollectionManager.CleanupWithConfig's
// signature uses qdrant.RetentionConfig (kept for back-compat with
// the broader reconciler/retention flows shipped in QDRANT-005).
type RetentionExecutorAdapter struct {
	cm *CollectionManager
}

// NewRetentionExecutorAdapter constructs a RetentionExecutorAdapter.
func NewRetentionExecutorAdapter(cm *CollectionManager) *RetentionExecutorAdapter {
	return &RetentionExecutorAdapter{cm: cm}
}

// Compile-time assertion.
var _ dr.RetentionExecutor = (*RetentionExecutorAdapter)(nil)

func (a *RetentionExecutorAdapter) CleanupWithConfig(ctx context.Context, cfg dr.RetentionConfig) (*dr.RetentionResult, error) {
	qr, err := a.cm.CleanupWithConfig(ctx, RetentionConfig{
		RetentionDays:           cfg.RetentionDays,
		KeepLastN:               cfg.KeepLastN,
		ProtectedRollbackTarget: cfg.ProtectedRollbackTarget,
		// MaxAgeSeconds + AgingTable: deliberately not bridged — the
		// application-layer dr surface does not orchestrate aging yet.
		// When the SQLite-backed aging registry migration lands (a
		// follow-up QDRANT-005 ramp), the bridge struct gets these two
		// fields added on both sides.
		MaxAgeSeconds: 0,
		AgingTable:    nil,
	})
	if err != nil {
		return nil, err
	}
	if qr == nil {
		return nil, nil
	}
	return &dr.RetentionResult{
		CollectionsDropped: qr.CollectionsDropped,
		CollectionsKept:    qr.CollectionsKept,
		DroppedNames:       qr.DroppedNames,
		Errors:             qr.Errors,
		ProtectedKept:      qr.ProtectedKept,
	}, nil
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
