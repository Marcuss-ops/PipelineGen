package verification

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	qdrantSchema "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
)

// ─── Pure parity core ────────────────────────────────────────────────

// TestComputeProjectionParity_Pass — identical eligible and active sets
// produce 0 missing / 0 orphan.
func TestComputeProjectionParity_Pass(t *testing.T) {
	t.Parallel()
	p := computeProjectionParity(
		[]string{"asset-1", "asset-2", "asset-3"},
		map[string]struct{}{"asset-2": {}, "asset-3": {}, "asset-1": {}}, // same set, different order
	)
	assert.Equal(t, 3, p.QdrantPoints)
	assert.Equal(t, 0, p.MissingCount)
	assert.Equal(t, 0, p.OrphanCount)
	assert.Empty(t, p.MissingIDs)
	assert.Empty(t, p.OrphanIDs)
}

// TestComputeProjectionParity_Missing — an eligible asset absent from
// the active collection is a missing_in_qdrant (projection bug).
func TestComputeProjectionParity_Missing(t *testing.T) {
	t.Parallel()
	p := computeProjectionParity([]string{"asset-1", "asset-2"}, map[string]struct{}{"asset-1": {}})
	assert.Equal(t, 1, p.MissingCount)
	assert.Equal(t, []string{"asset-2"}, p.MissingIDs)
	assert.Equal(t, 0, p.OrphanCount)
}

// TestComputeProjectionParity_Orphan — a Qdrant point whose asset_id is
// not in the eligible SQLite set is an orphan_in_qdrant (stale projection).
func TestComputeProjectionParity_Orphan(t *testing.T) {
	t.Parallel()
	p := computeProjectionParity([]string{"asset-1"}, map[string]struct{}{"asset-1": {}, "asset-9": {}})
	assert.Equal(t, 0, p.MissingCount)
	assert.Equal(t, 1, p.OrphanCount)
	assert.Equal(t, []string{"asset-9"}, p.OrphanIDs)
}

// TestComputeProjectionParity_Both — missing AND orphan surface together.
func TestComputeProjectionParity_Both(t *testing.T) {
	t.Parallel()
	p := computeProjectionParity(
		[]string{"asset-1", "asset-2", "asset-3"},
		map[string]struct{}{"asset-1": {}, "asset-9": {}},
	)
	assert.Equal(t, 2, p.MissingCount)
	assert.Equal(t, []string{"asset-2", "asset-3"}, p.MissingIDs)
	assert.Equal(t, 1, p.OrphanCount)
	assert.Equal(t, []string{"asset-9"}, p.OrphanIDs)
}

// TestProjectionReport_PassesRule — the single PASS rule: 0 missing AND
// 0 orphan AND 0 points without asset_id AND complete scan AND no errors.
func TestProjectionReport_PassesRule(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		report     ProjectionVerificationReport
		wantPassed bool
	}{
		{
			name: "all green passes",
			report: ProjectionVerificationReport{
				CompleteScan: true,
				MissingCount: 0,
				OrphanCount:  0,
			},
			wantPassed: true,
		},
		{
			name: "missing blocks",
			report: ProjectionVerificationReport{
				CompleteScan: true,
				MissingCount: 1,
			},
			wantPassed: false,
		},
		{
			name: "orphan blocks",
			report: ProjectionVerificationReport{
				CompleteScan: true,
				OrphanCount:  1,
			},
			wantPassed: false,
		},
		{
			name: "point without asset_id blocks",
			report: ProjectionVerificationReport{
				CompleteScan:         true,
				PointsMissingAssetID: 1,
			},
			wantPassed: false,
		},
		{
			name: "incomplete scan blocks",
			report: ProjectionVerificationReport{
				CompleteScan: false,
			},
			wantPassed: false,
		},
		{
			name: "errors block",
			report: ProjectionVerificationReport{
				CompleteScan: true,
				Errors:       []string{"diagnostic"},
			},
			wantPassed: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.wantPassed, tc.report.Passes())
		})
	}
}

// ─── Integration: VerifyActiveProjection against a mock Qdrant ──────

// TestVerifyActiveProjection_HappyPath — alias resolves to the target,
// every eligible asset is present, scan completes → PASS.
func TestVerifyActiveProjection_HappyPath(t *testing.T) {
	t.Parallel()
	srv := mockProjectionQdrant(t, projectionMockSpec{
		AliasTarget: "media_assets_v4_test",
		Pages: [][]map[string]any{
			{
				point("asset-1"),
				point("asset-2"),
			},
		},
	})
	defer srv.Close()

	schema := qdrantSchema.DefaultV3Schema()
	verifier := NewProjectionVerifier(newClientAt(srv.URL), &stubAssetStore{ids: []string{"asset-1", "asset-2"}}, schema, zap.NewNop())

	report, err := verifier.VerifyActiveProjection(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "media_assets_v4_test", report.Collection)
	assert.Equal(t, 2, report.EligibleSQLite)
	assert.Equal(t, 2, report.QdrantPoints)
	assert.Equal(t, 0, report.MissingCount)
	assert.Equal(t, 0, report.OrphanCount)
	assert.True(t, report.CompleteScan)
	assert.True(t, report.Passes(), "PASS only when 0 missing and 0 orphan")
	assert.True(t, report.Passed)
}

// TestVerifyActiveProjection_MissingBlocks — eligible asset absent from
// the active collection → FAIL with the ID listed.
func TestVerifyActiveProjection_MissingBlocks(t *testing.T) {
	t.Parallel()
	srv := mockProjectionQdrant(t, projectionMockSpec{
		AliasTarget: "media_assets_v4_test",
		Pages: [][]map[string]any{
			{point("asset-1")},
		},
	})
	defer srv.Close()

	schema := qdrantSchema.DefaultV3Schema()
	verifier := NewProjectionVerifier(newClientAt(srv.URL), &stubAssetStore{ids: []string{"asset-1", "asset-2"}}, schema, zap.NewNop())

	report, err := verifier.VerifyActiveProjection(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, report.MissingCount)
	assert.Equal(t, []string{"asset-2"}, report.MissingIDs)
	assert.False(t, report.Passed, "eligible-but-missing must FAIL")
}

// TestVerifyActiveProjection_OrphanBlocks — stale point in the active
// collection → FAIL with the ID listed.
func TestVerifyActiveProjection_OrphanBlocks(t *testing.T) {
	t.Parallel()
	srv := mockProjectionQdrant(t, projectionMockSpec{
		AliasTarget: "media_assets_v4_test",
		Pages: [][]map[string]any{
			{point("asset-1"), point("asset-9")},
		},
	})
	defer srv.Close()

	schema := qdrantSchema.DefaultV3Schema()
	verifier := NewProjectionVerifier(newClientAt(srv.URL), &stubAssetStore{ids: []string{"asset-1"}}, schema, zap.NewNop())

	report, err := verifier.VerifyActiveProjection(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, report.OrphanCount)
	assert.Equal(t, []string{"asset-9"}, report.OrphanIDs)
	assert.False(t, report.Passed, "orphan point must FAIL")
}

// TestVerifyActiveProjection_PointWithoutAssetIDBlocks — a point whose
// payload carries no asset_id cannot be matched → FAIL.
func TestVerifyActiveProjection_PointWithoutAssetIDBlocks(t *testing.T) {
	t.Parallel()
	srv := mockProjectionQdrant(t, projectionMockSpec{
		AliasTarget: "media_assets_v4_test",
		Pages: [][]map[string]any{
			{{"id": "uuid-point-without-asset-id", "payload": map[string]any{"name": "n"}}},
		},
	})
	defer srv.Close()

	schema := qdrantSchema.DefaultV3Schema()
	verifier := NewProjectionVerifier(newClientAt(srv.URL), &stubAssetStore{ids: nil}, schema, zap.NewNop())

	report, err := verifier.VerifyActiveProjection(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, report.PointsMissingAssetID)
	assert.False(t, report.Passed, "point without asset_id must FAIL")
}

// TestVerifyActiveProjection_MultiPage — paginated scroll accumulates
// the full set across pages.
func TestVerifyActiveProjection_MultiPage(t *testing.T) {
	t.Parallel()
	srv := mockProjectionQdrant(t, projectionMockSpec{
		AliasTarget: "media_assets_v4_test",
		Pages: [][]map[string]any{
			{point("asset-1")},
			{point("asset-2")},
			{point("asset-3")},
		},
	})
	defer srv.Close()

	schema := qdrantSchema.DefaultV3Schema()
	verifier := NewProjectionVerifier(newClientAt(srv.URL), &stubAssetStore{ids: []string{"asset-1", "asset-2", "asset-3"}}, schema, zap.NewNop())

	report, err := verifier.VerifyActiveProjection(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 3, report.QdrantPoints)
	assert.True(t, report.Passed)
}

// TestVerifyActiveProjection_ScrollErrorFatal — a scroll page error
// returns a non-nil error and the report is not passed.
func TestVerifyActiveProjection_ScrollErrorFatal(t *testing.T) {
	t.Parallel()
	srv := mockProjectionQdrant(t, projectionMockSpec{
		AliasTarget: "media_assets_v4_test",
		// Two pages so the loop performs a second scroll request; the
		// injected error fires on page 1.
		Pages:        [][]map[string]any{{point("asset-1")}, {point("asset-2")}},
		ErrAfterPage: 1,
	})
	defer srv.Close()

	schema := qdrantSchema.DefaultV3Schema()
	verifier := NewProjectionVerifier(newClientAt(srv.URL), &stubAssetStore{ids: []string{"asset-1"}}, schema, zap.NewNop())

	report, err := verifier.VerifyActiveProjection(context.Background())
	require.Error(t, err, "scroll page error must be fatal")
	assert.NotNil(t, report)
	assert.False(t, report.Passes())
}

// TestVerifyActiveProjection_AliasHasNoTarget — empty alias resolution
// fails closed with a clear error.
func TestVerifyActiveProjection_AliasHasNoTarget(t *testing.T) {
	t.Parallel()
	srv := mockProjectionQdrant(t, projectionMockSpec{AliasTarget: ""})
	defer srv.Close()

	schema := qdrantSchema.DefaultV3Schema()
	verifier := NewProjectionVerifier(newClientAt(srv.URL), &stubAssetStore{ids: []string{"asset-1"}}, schema, zap.NewNop())

	_, err := verifier.VerifyActiveProjection(context.Background())
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "has no target"), "must explain the missing alias target: %v", err)
}

// ─── Mock Qdrant for the projection verifier ────────────────────────

// projectionMockSpec configures the httptest server: the alias target
// (empty = no alias registered) and the scroll pages.
type projectionMockSpec struct {
	AliasTarget  string
	Pages        [][]map[string]any
	ErrAfterPage int // scroll returns HTTP 500 from page N onward (0 = never)
}

// point builds a Qdrant scroll point with a payload asset_id.
func point(assetID string) map[string]any {
	return map[string]any{
		"id": qdrantSchema.AssetIDToQdrantPointID(assetID),
		"payload": map[string]any{
			"asset_id": assetID,
			"name":     "n",
			"source":   "youtube",
		},
	}
}

// mockProjectionQdrant installs an httptest server serving the
// /aliases and /collections/{target}/points/scroll endpoints used by
// VerifyActiveProjection.
func mockProjectionQdrant(t *testing.T, spec projectionMockSpec) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	pageIdx := 0

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/aliases":
			aliases := []map[string]any{}
			if spec.AliasTarget != "" {
				aliases = append(aliases, map[string]any{
					"alias_name":      "media_assets_current",
					"collection_name": spec.AliasTarget,
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{"aliases": aliases},
			})
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/collections/") && strings.HasSuffix(r.URL.Path, "/points/scroll"):
			if spec.ErrAfterPage > 0 && pageIdx >= spec.ErrAfterPage {
				http.Error(w, "injected scroll error", http.StatusInternalServerError)
				return
			}
			if pageIdx >= len(spec.Pages) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"result": map[string]any{"points": []any{}, "next_page_offset": nil},
				})
				return
			}
			page := spec.Pages[pageIdx]
			pageIdx++
			var nextOffset any
			if pageIdx < len(spec.Pages) {
				nextOffset = "offset-" + string(rune('0'+pageIdx))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{"points": page, "next_page_offset": nextOffset},
			})
		default:
			http.NotFound(w, r)
		}
	}))
} // stubAssetStore is defined in verifier_test.go (same package).
