// Package qdrant — P1 QDRANT-VERIFIER-SPLIT: counts phase tests (July 2026).
package verification

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// TestVerifyCounts_PointCountMatch verifies that verifyCounts returns
// the sqliteSet and reports the correct actual count when counts match.
func TestVerifyCounts_PointCountMatch(t *testing.T) {
	t.Parallel()

	srv := mockQdrantForVerifier(t, []string{canonicalPointPayload("asset-1")})
	defer srv.Close()
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, nil, nil, zap.NewNop())

	report := newSwitchReport("media_assets_v3", 1)
	sqliteSet, err := v.verifyCounts(context.Background(), "media_assets_v3", 1, report)
	require.NoError(t, err)
	assert.True(t, sqliteSet["asset-1"], "sqliteSet must contain asset-1")
	assert.Equal(t, 1, report.ActualPoints)
	assert.Empty(t, report.Errors, "no errors when counts match")
}

// TestVerifyCounts_PointCountMismatch verifies that a strict count
// mismatch is appended to report.Errors but does not return error.
func TestVerifyCounts_PointCountMismatch(t *testing.T) {
	t.Parallel()

	srv := mockQdrantForVerifier(t, []string{canonicalPointPayload("asset-1")})
	defer srv.Close()
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, nil, nil, zap.NewNop())

	report := newSwitchReport("media_assets_v3", 5) // expected 5, actual 1
	sqliteSet, err := v.verifyCounts(context.Background(), "media_assets_v3", 5, report)
	require.NoError(t, err)
	assert.NotNil(t, sqliteSet)
	assert.Equal(t, 1, report.ActualPoints)
	found := false
	for _, e := range report.Errors {
		if strings.Contains(e, "point count mismatch") {
			found = true
			break
		}
	}
	assert.True(t, found, "strict mismatch must surface 'point count mismatch'")
}

// TestVerifyPointCountParity_FatalError verifies that CountPoints
// returning an error is fatal (returns non-nil error).
func TestVerifyPointCountParity_FatalError(t *testing.T) {
	t.Parallel()

	// Server that always returns 500 for CountPoints.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/collections/media_assets_v3" {
			http.Error(w, "qdrant down", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	v := NewReindexVerifier(newClientAt(srv.URL), nil, nil, nil, nil, zap.NewNop())
	report := newSwitchReport("media_assets_v3", 1)
	_, err := v.verifyCounts(context.Background(), "media_assets_v3", 1, report)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "count failed")
}

func newSwitchReport(target string, expected int) *schema.SwitchReport {
	return &schema.SwitchReport{
		TargetCollection:          target,
		ExpectedPoints:            expected,
		CompleteScan:              false,
		GoldenQueriesOK:           false,
		FiltersOK:                 false,
		VersionMismatchPerChannel: make(map[string]int),
	}
}
