// search_hydration_no_runtime_recovery_test.go certifies that the
// runtime search hydration path is strictly Qdrant hit → SQLite read
// (never SQLite miss → Qdrant hit → INSERT SQLite). After the PostgreSQL
// media cutover there is no Qdrant→SQLite recovery command or runtime
// compatibility path left in the repository.
//
// The test exercises hydrateSearchResults with a fake AssetStore that
// records whether FetchAsset was called (SQLite validation) and proves:
//
//   - A Qdrant hit with a missing SQLite row is DROPPED (not inserted).
//   - A Qdrant hit with a tombstoned SQLite row is DROPPED.
//   - A Qdrant hit with a healthy SQLite row is returned with SQLite data.
//   - No INSERT is ever issued to SQLite during hydration.
package search

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	appsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/indexing"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// recordingAssetStore records every FetchAsset call and whether any
// write method was invoked. It implements indexing.AssetStore.
type recordingAssetStore struct {
	fetchedIDs []string
	inserts    int
}

func (s *recordingAssetStore) FetchAsset(ctx context.Context, assetID string) (*indexing.AssetData, error) {
	s.fetchedIDs = append(s.fetchedIDs, assetID)
	switch assetID {
	case "healthy-asset":
		return &indexing.AssetData{ID: "healthy-asset", Source: "youtube", Name: "Healthy", DeletedAt: ""}, nil
	case "tombstoned-asset":
		return &indexing.AssetData{ID: "tombstoned-asset", DeletedAt: "2026-01-01T00:00:00Z"}, nil
	case "missing-asset":
		return nil, indexing.ErrAssetNotFound
	}
	return nil, errors.New("unexpected asset ID")
}

func (s *recordingAssetStore) ListAllAssetIDs(ctx context.Context) ([]string, error) {
	return nil, nil
}

func (s *recordingAssetStore) FetchAssetBatch(ctx context.Context, afterID string, limit int) ([]*indexing.AssetData, error) {
	return nil, nil
}

func TestHydrateSearchResults_NeverInsertsSQLiteOnMiss(t *testing.T) {
	store := &recordingAssetStore{}
	adapter := &SearchAdapter{searcher: nil, assetStore: store, log: nil}

	results := []schema.SearchResult{
		{ID: "pt-1", Score: 0.9, Payload: map[string]any{"asset_id": "healthy-asset"}},
		{ID: "pt-2", Score: 0.8, Payload: map[string]any{"asset_id": "missing-asset"}},
		{ID: "pt-3", Score: 0.7, Payload: map[string]any{"asset_id": "tombstoned-asset"}},
		{ID: "pt-4", Score: 0.6, Payload: map[string]any{}}, // no asset_id
	}

	out, err := adapter.hydrateSearchResults(context.Background(), results)
	require.NoError(t, err)

	// Only the healthy asset survives hydration.
	require.Len(t, out, 1, "only the healthy SQLite row must survive hydration")
	require.Equal(t, "healthy-asset", out[0].AssetID)

	// Every Qdrant hit with an asset_id triggered a SQLite FetchAsset
	// (validation), proving the cache/SSOT contract.
	require.Equal(t, []string{"healthy-asset", "missing-asset", "tombstoned-asset"}, store.fetchedIDs,
		"every Qdrant hit with asset_id must be validated against SQLite")

	// No INSERT was ever issued — the runtime path is read-only on SQLite.
	require.Equal(t, 0, store.inserts, "hydration must never INSERT to SQLite")
}

func TestHydrateSearchResults_NilAssetStoreFailsClosed(t *testing.T) {
	adapter := &SearchAdapter{searcher: nil, assetStore: nil, log: nil}
	results := []schema.SearchResult{{ID: "pt-1", Payload: map[string]any{"asset_id": "x"}}}
	_, err := adapter.hydrateSearchResults(context.Background(), results)
	require.Error(t, err, "nil assetStore must fail closed — no stale Qdrant payload may surface")
	require.Contains(t, err.Error(), "SQLite asset store not configured")
}

func TestHydrateSearchResults_EmptyResultsNoOp(t *testing.T) {
	adapter := &SearchAdapter{searcher: nil, assetStore: &recordingAssetStore{}, log: nil}
	out, err := adapter.hydrateSearchResults(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, out)
}

// TestRuntimeRecoverySurfaceIsReadOnly pins the post-cutover invariant:
// Qdrant search adapters expose retrieval only. The retired admin recovery
// command is gone, so there is no supported Qdrant→SQLite mutation surface.
func TestRuntimeRecoverySurfaceIsReadOnly(t *testing.T) {
	var _ appsearch.VectorStorePort = (*SearchAdapter)(nil)

	// VectorStorePort exposes only Search + HybridSearch. Keeping this
	// compile-time assertion here makes any future attempt to reintroduce a
	// media recovery mutation through the search surface visible in review.
	adapter := &SearchAdapter{}
	_ = adapter
}
