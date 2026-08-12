// cmd/admin/qdrant_readiness_checks_qdrant.go — Qdrant-related readiness checks
// + adapter types + noop stubs extracted from qdrant_readiness_checks.go
// (LONG-FILES-DECOMPOSITION-2026-07-06 Band C #1).
//
// Owns: checkQdrantActiveCollection, checkReconcilerProduction,
// readiNoopOutbox, readiNoopPayload, qdrantReconcilerListerAdapter,
// qdrantReconcileAssetStore, qdrantAssetStoreForReconcile.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/qdrant/reconciler"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/disasterrecovery"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/indexing"
	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/collections"
)

// ── Qdrant-related checks ──────────────────────────────────────────────

// checkQdrantActiveCollection: production-shaped. Replaces the old
// stub pattern (NewHealthProbe stub + no-op reconciler). Builds a
// real *transport.Client from cfg and runs the canonical GetAliasTarget
// + CompareActiveCollection flow.
func checkQdrantActiveCollection(ctx context.Context, deps readinessDeps) checkStatus {
	if deps.Cfg == nil || deps.Cfg.Qdrant.BaseURL == "" {
		return checkStatus{Err: "qdrant.base_url is empty"}
	}
	if !deps.Cfg.Qdrant.Enabled {
		return checkStatus{Err: "qdrant.enabled=false (enable to pass production-shaped readiness)"}
	}
	client := transport.NewClient(&qdrantschema.Config{
		BaseURL: deps.Cfg.Qdrant.BaseURL,
		Timeout: deps.Cfg.Qdrant.Timeout,
		APIKey:  deps.Cfg.Qdrant.APIKey,
	}, deps.Log)
	probe := disasterrecovery.NewHealthProbe(client)
	if err := probe.Probe(ctx); err != nil {
		return checkStatus{Err: "qdrant health probe failed: " + err.Error()}
	}
	schema := qdrantschema.DefaultV3Schema()
	mgr := collections.NewCollectionManager(client, schema, deps.Log)
	active, err := mgr.GetActiveCollection(ctx)
	if err != nil {
		return checkStatus{Err: "GetActiveCollection failed: " + err.Error()}
	}
	if active == "" {
		return checkStatus{Err: "active collection empty (alias has no target)"}
	}
	diff, err := mgr.CompareActiveCollection(ctx)
	if err != nil {
		return checkStatus{Err: "CompareActiveCollection failed: " + err.Error()}
	}
	if !diff.Compatible {
		return checkStatus{Err: "active collection is schema-incompatible"}
	}
	return checkStatus{Pass: true}
}

// checkReconcilerProduction: production-shaped. Replaces
// noOpQdrantLister + noOpSQLiteReconcileReader with the
// qdrantReconcilerListerAdapter dry-run.
func checkReconcilerProduction(ctx context.Context, deps readinessDeps) checkStatus {
	defer func() {
		_ = recover()
	}()
	if deps.Root == nil {
		return checkStatus{Err: "production composition root is nil — reconciler check requires real Client + assetStore"}
	}
	if deps.Root.QdrantClient == nil {
		return checkStatus{Err: "production *transport.Client is nil — reconciler cannot run its real scroll path"}
	}
	if deps.DB == nil {
		return checkStatus{Err: "raw *sql.DB is nil — reconciler assetStore consumer missing"}
	}
	if deps.Cfg == nil || deps.Cfg.Qdrant.BaseURL == "" {
		return checkStatus{Err: "qdrant.base_url is empty — reconciler cannot open the alias"}
	}

	schema := qdrantschema.DefaultV3Schema()
	assetStore := qdrantAssetStoreForReconcile(deps.DB)
	rec := reconciler.NewServiceFromDeps(reconciler.ServiceDeps{
		Schema: reconciler.SchemaVersions{
			Version:           schema.Version,
			RequiredKeys:      []string{"asset_id"},
			PerChannelVersion: map[string]string{},
		},
		Qdrant:  qdrantReconcilerListerAdapter{Client: deps.Root.QdrantClient},
		SQLite:  assetStore,
		Outbox:  readiNoopOutbox{},
		Payload: readiNoopPayload{},
		Log:     deps.Log,
	})
	collection := schema.RuntimeAlias
	prodColl, collErr := deps.Root.QdrantClient.GetAliasTarget(ctx, schema.RuntimeAlias)
	if collErr == nil && strings.TrimSpace(prodColl) != "" {
		collection = prodColl
	}
	if _, err := rec.Reconcile(ctx, reconciler.ReconcileOptions{
		Collection: collection,
		DryRun:     true,
	}); err != nil {
		return checkStatus{Err: "reconciler dry-run failed: " + err.Error()}
	}
	return checkStatus{Pass: true}
}

// ── PR 10 readiness noop stubs ────────────────────────────────────────

type (
	readiNoopOutbox  struct{}
	readiNoopPayload struct{}
)

func (readiNoopOutbox) EnqueueReindex(context.Context, string, string, bool) error { return nil }
func (readiNoopOutbox) EnqueueDelete(context.Context, string) error                { return nil }
func (readiNoopPayload) DeletePayloadKeys(context.Context, string, []string, []string) error {
	return nil
}

// ── Production-shaped dependencies shims ───────────────────────────────

type qdrantReconcilerListerAdapter struct {
	Client *transport.Client
}

func (a qdrantReconcilerListerAdapter) ScrollPoints(ctx context.Context, collection string, offset string, limit int) (reconciler.Points, error) {
	result, err := a.Client.ScrollPoints(ctx, collection, offset, limit, nil)
	if err != nil {
		return reconciler.Points{}, fmt.Errorf("readiness: qdrant scroll failed for collection %q: %w", collection, err)
	}
	if result == nil {
		return reconciler.Points{}, fmt.Errorf("readiness: qdrant scroll returned nil ScrollResult for collection %q", collection)
	}
	items := make([]reconciler.PointSnapshot, len(result.Points))
	for i, p := range result.Points {
		items[i] = reconciler.PointSnapshot{
			ID:      p.ID,
			Payload: p.Payload,
		}
	}
	return reconciler.Points{
		Items:      items,
		NextOffset: result.NextOffset,
	}, nil
}

func qdrantAssetStoreForReconcile(db *sql.DB) qdrantReconcileAssetStore {
	return qdrantReconcileAssetStore{Store: indexing.NewSQLiteAssetStore(db)}
}

type qdrantReconcileAssetStore struct {
	Store *indexing.SQLiteAssetStore
}

func (s qdrantReconcileAssetStore) ListForReconcile(ctx context.Context, includeLifecycleStates []string) ([]reconciler.AssetSnapshot, error) {
	if s.Store == nil {
		return nil, fmt.Errorf("readiness: indexing.SQLiteAssetStore is nil — cannot run reconciler SQLite path")
	}
	assets, err := s.Store.ListAssetsForReconcile(ctx, includeLifecycleStates)
	if err != nil {
		return nil, fmt.Errorf("readiness: indexing.SQLiteAssetStore.ListAssetsForReconcile failed: %w", err)
	}
	out := make([]reconciler.AssetSnapshot, len(assets))
	for i, a := range assets {
		out[i] = reconciler.AssetSnapshot{
			ID:             a.ID,
			WorkspaceID:    a.WorkspaceID,
			LifecycleState: a.LifecycleState,
			ContentHash:    a.ContentHash,
		}
	}
	return out, nil
}
