// Package indexing — shared payload-mapper test helpers.
package indexing

import (
	"context"
	"testing"

	appsearchtext "github.com/Marcuss-ops/PipelineGen/internal/capabilities/indexing/searchtext"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// mapKeys is the canonical extraction helper for readable assertion messages.
func mapKeys(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// fakeAssetStore is a minimal AssetStore for AssetToPoint +
// AssetToIndexDocument unit tests.
type fakeAssetStore struct {
	asset *AssetData
	ids   []string
}

func (f *fakeAssetStore) FetchAsset(ctx context.Context, id string) (*AssetData, error) {
	if f.asset == nil || f.asset.ID != id {
		return nil, &testErrAssetNotFound{ID: id}
	}
	return f.asset, nil
}

func (f *fakeAssetStore) ListAllAssetIDs(ctx context.Context) ([]string, error) {
	return f.ids, nil
}

func (f *fakeAssetStore) FetchAssetBatch(ctx context.Context, afterID string, limit int) ([]*AssetData, error) {
	return nil, nil
}

// makeFloat32Slice creates a deterministic, non-zero finite vector.
func makeFloat32Slice(size int) []float32 {
	v := make([]float32, size)
	for i := range v {
		v[i] = 1.0
	}
	return v
}

func requirePointID(t *testing.T, p *schema.Point) {
	t.Helper()
	if p == nil {
		t.Fatal("point is nil")
	}
	if p.ID == "" {
		t.Fatal("point ID is empty (qdrantSchema.AssetIDToQdrantPointID canonicalisation must run)")
	}
}

type ctxRecordingBuilder struct {
	capturedCtx  context.Context
	capturedText string
}

func (b *ctxRecordingBuilder) Build(ctx context.Context, input appsearchtext.SearchTextInput) (string, error) {
	b.capturedCtx = ctx
	b.capturedText = "ctx-propagation-verified-by-mock"
	return b.capturedText, nil
}

type testErrAssetNotFound struct{ ID string }

func (e *testErrAssetNotFound) Error() string { return "asset not found: " + e.ID }
