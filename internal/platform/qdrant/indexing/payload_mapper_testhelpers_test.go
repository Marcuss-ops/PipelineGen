// Package indexing — payload_mapper_testhelpers_test.go
// (PR-QDRANT-PAYLOAD-MAPPER-TEST-SPLIT, July 2026).
//
// Shared test plumbing extracted from payload_mapper_test.go per
// godlike/06 SSOT (one canonical owner per fact). After the split,
// this file owns the 6 helpers that are cross-test-file:
//   - mapKeys (collapse of mapKeys + mapKeysVec per I.2; both
//     returned []string from a map[string]interface{} and were
//     textually identical)
//   - fakeAssetStore struct + FetchAsset / ListAllAssetIDs /
//     FetchAssetBatch (the in-memory AssetStore used by every
//     AssetToPoint / AssetToIndexDocument test)
//   - makeFloat32Slice (vector factory used by validation +
//     document+sparse tests)
//   - requirePointID (fatal assertion used by AssetToPoint paths
//     to verify qdrantSchema.AssetIDToQdrantPointID canonicalisation)
//   - ctxRecordingBuilder (SearchTextBuilder mock for AZIONE 1
//     ctx-propagation tests in SearchText file)
//   - ErrAssetNotFound (typed sentinel for fakeAssetStore.FetchAsset)
//
// All exports are package-private (lowercase) and the file is
// suffix `_test.go` so it only compiles under `go test` — never
// pollutes the production binary (godlike/07 minimum-blast-radius).
package indexing

import (
	"context"
	"testing"

	appsearchtext "github.com/Marcuss-ops/PipelineGen/internal/capabilities/indexing/searchtext"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// mapKeys is the canonical extraction helper for readable assertion
// messages. It collapses the previously-separate `mapKeys` (operating
// on `payload map[string]interface{}`) and `mapKeysVec` (operating on
// the structurally-identical `point.Vectors` map[string]interface{})
// into a single symbol. Both original call sites use it identically.
func mapKeys(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// mapKeysVec is a cross-package alias retained for tests that called
// the original `_Vec`-suffixed symbol. Identical implementation;
// callers can be migrated to mapKeys over time (godlike/07
// minimum-blast-radius: not in this PR's scope).
func mapKeysVec(m map[string]interface{}) []string { return mapKeys(m) }

// fakeAssetStore is a minimal AssetStore for AssetToPoint +
// AssetToIndexDocument unit tests. It returns the single seeded
// asset regardless of which ID is requested (these tests do not
// round-trip through FetchAsset).
type fakeAssetStore struct {
	asset *AssetData
	ids   []string
}

func (f *fakeAssetStore) FetchAsset(ctx context.Context, id string) (*AssetData, error) {
	if f.asset == nil || f.asset.ID != id {
		return nil, &ErrAssetNotFound{ID: id}
	}
	return f.asset, nil
}

func (f *fakeAssetStore) ListAllAssetIDs(ctx context.Context) ([]string, error) {
	return f.ids, nil
}

func (f *fakeAssetStore) FetchAssetBatch(ctx context.Context, afterID string, limit int) ([]*AssetData, error) {
	return nil, nil
}

// makeFloat32Slice creates a []float32 of the given size filled with
// 1.0 (a deterministic, non-zero, finite vector). Used by VALIDATION
// (dimension, NaN/Inf tests) and DOCUMENT (BM25 sparse + dim tests).
func makeFloat32Slice(size int) []float32 {
	v := make([]float32, size)
	for i := range v {
		v[i] = 1.0
	}
	return v
}

// requirePointID fails the test if the point is nil or its ID is
// empty (would mean qdrantSchema.AssetIDToQdrantPointID canonicalisation
// didn't run). Used by every AssetToPoint test to short-circuit the
// rest of the assertions on a malformed point.
func requirePointID(t *testing.T, p *schema.Point) {
	t.Helper()
	if p == nil {
		t.Fatal("point is nil")
	}
	if p.ID == "" {
		t.Fatal("point ID is empty (qdrantSchema.AssetIDToQdrantPointID canonicalisation must run)")
	}
}

// ctxRecordingBuilder is a SearchTextBuilder mock that records the
// context passed to Build and returns a marker string so the test can
// distinguish builder output from asset.SearchText fallback. Used by
// the 3 SEARCHTEXT ctx-propagation tests.
type ctxRecordingBuilder struct {
	capturedCtx  context.Context
	capturedText string
}

func (b *ctxRecordingBuilder) Build(ctx context.Context, input appsearchtext.SearchTextInput) (string, error) {
	b.capturedCtx = ctx
	b.capturedText = "ctx-propagation-verified-by-mock"
	return b.capturedText, nil
}

// ErrAssetNotFound is a test-only typed sentinel used by
// fakeAssetStore.FetchAsset. Distinct from production
// errors.ErrAssetNotFound so tests can fail-fast with a known
// identity without cross-coupling.
type ErrAssetNotFound struct{ ID string }

func (e *ErrAssetNotFound) Error() string { return "asset not found: " + e.ID }
