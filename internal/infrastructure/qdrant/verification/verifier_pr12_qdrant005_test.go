// verifier_test_pr12_qdrant005.go — QDRANT-003 era regression tests
// for per-channel + non-canonical behaviour that PR 12 also
// constrains.
//
// Kept separate from verifier_test.go so the PR-12 path
// stays clearly separated in the test file identity. These tests
// target the canonical boundary + per-channel gate that PR 12
// hardens; running them after the verifier.go rewrite catches
// regression in tighter-than-before semantics.
//
// QDRANT-003 / QDRANT-005 closed (June 2026):
//   - The global embedding_version rescue path was DELETED.
//   - non-UUID pt.ID is BLOCKING.
//   - Per-channel counter bumps once per point at most for the
//     global counter (via pointMismatched latch); independent
//     per-channel counters.
//
// PR 12 (June 2026):
//   - ActualPoints == ExpectedPoints (strict).
//   - Full per-channel scan on every page (no 1000-point sample).
//   - Scroll errors are fatal; maxPages-cap is blocking.
//   - pt.ID MUST equal qdrantSchema.AssetIDToQdrantPointID(payload["asset_id"])
//     (not just uuid-parseable).

package verification

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	qdrantSchema "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
)

// TestReindexVerifier_PerChannelVersionMismatch_PresentMismatch —
// a point whose per-channel embedding_version_text does NOT match
// the schema's qdrantSchema.EmbeddingSpec.ModelVersion trips BOTH the global
// VersionMismatch AND the per-channel counter on the "text" channel.
//
// Note: This is a QDRANT-003 regression test that PR 12 also
// implicitly exercises (because the per-channel check now runs on
// every page rather than the first 2 pages). On a 1-point fixture
// it lands on iteration=0; PR 12 preserves the count semantics.
func TestReindexVerifier_PerChannelVersionMismatch_PresentMismatch(t *testing.T) {
	t.Parallel()

	canonicalID := qdrantSchema.AssetIDToQdrantPointID("asset-1")
	payload := fmt.Sprintf(`{
		"id": %q,
		"payload": {
			"asset_id": "asset-1",
			"name": "a1",
			"source": "youtube",
			"embedding_version": "v3",
			"embedding_version_text": "wrong-version",
			"embedding_version_transcript": "2026-06-26-v1",
			"embedding_version_visual": "2026-06-16-v1",
			"embedding_version_audio": "2026-06-26-v1"
		}
	}`, canonicalID)
	srv := mockQdrantForVerifier(t, []string{payload})
	defer srv.Close()

	schema := qdrantSchema.DefaultV3Schema()
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())

	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 1)
	require.NoError(t, err)
	assert.Equal(t, 1, report.VersionMismatch)
	assert.Equal(t, 1, report.VersionMismatchPerChannel["text"])
	assert.Equal(t, 0, report.VersionMismatchPerChannel["transcript"])
	assert.False(t, report.Ready)
}

// TestReindexVerifier_PerChannelVersionMismatch_PresentMatch —
// happy path: 1 point with all per-channel keys at the canonical
// ModelVersion passes VersionMismatch = 0 and Ready = true.
func TestReindexVerifier_PerChannelVersionMismatch_PresentMatch(t *testing.T) {
	t.Parallel()

	canonicalID := qdrantSchema.AssetIDToQdrantPointID("asset-1")
	payload := fmt.Sprintf(`{
		"id": %q,
		"payload": {
			"asset_id": "asset-1",
			"name": "a1",
			"source": "youtube",
			"embedding_version": "v3",
			"embedding_version_text": "2026-06-26-v1",
			"embedding_version_transcript": "2026-06-26-v1",
			"embedding_version_visual": "2026-06-16-v1",
			"embedding_version_audio": "2026-06-26-v1"
		}
	}`, canonicalID)
	srv := mockQdrantForVerifier(t, []string{payload})
	defer srv.Close()

	schema := qdrantSchema.DefaultV3Schema()
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())

	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 1)
	require.NoError(t, err)
	assert.Equal(t, 0, report.VersionMismatch)
	for _, c := range report.VersionMismatchPerChannel {
		assert.Equal(t, 0, c)
	}
	assert.True(t, report.Ready)
	assert.True(t, report.CompleteScan)
	assert.Equal(t, 0, report.NonCanonicalPointCount)
}

// TestReindexVerifier_PerChannelVersionMismatch_AbsentLegacyFallbackFail
// — migration-window penalty. A point with only legacy
// embedding_version (no per-channel keys) bumps every per-channel
// counter for the channels declared in DefaultV3Schema. No legacy
// rescue path (QDRANT-005 closure kept): the global
// embedding_version field is no longer consulted.
//
// YAGNI / reactivation note (July 2026): the "audio" channel is
// intentionally NOT enumerated among the asserted channels because
// DefaultV3Schema declares it YAGNI (the CLAP-HTSAT audio model
// runtime service is not deployed — see schema.go YAGNI commentary
// at the audio EmbeddingSpec). If a future migration uncomments
// the audio channel with a non-empty ModelVersion, the verifier
// loop will start iterating it and this test will need to be
// re-extended to assert the audio counter. Until then, asserting
// "audio" here would be aspirational rather than load-bearing.
func TestReindexVerifier_PerChannelVersionMismatch_AbsentLegacyFallbackFail(t *testing.T) {
	t.Parallel()
	canonicalID := qdrantSchema.AssetIDToQdrantPointID("asset-1")
	payload := fmt.Sprintf(`{
		"id": %q,
		"payload": {
			"asset_id": "asset-1",
			"name": "a1",
			"source": "youtube",
			"embedding_version": "v3"
		}
	}`, canonicalID)
	srv := mockQdrantForVerifier(t, []string{payload})
	defer srv.Close()
	schema := qdrantSchema.DefaultV3Schema()
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())

	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 1)
	require.NoError(t, err)
	// Assert only channels declared in DefaultV3Schema.DenseVectors
	// with non-empty ModelVersion. The "audio" channel is YAGNI.
	assert.GreaterOrEqual(t, report.VersionMismatchPerChannel["text"], 1)
	assert.GreaterOrEqual(t, report.VersionMismatchPerChannel["transcript"], 1)
	assert.GreaterOrEqual(t, report.VersionMismatchPerChannel["visual"], 1)
	assert.False(t, report.Ready)
}

// TestReindexVerifier_NonUUIDPointBlocking — non-canonical-id
// behaviour. The pt.ID is "asset:legacy-prefix:asset-1", and the
// canonical qdrantSchema.AssetIDToQdrantPointID for "asset-1" is a fresh UUID
// → strict mismatch; NonCanonicalPointCount == 1.
func TestReindexVerifier_NonUUIDPointBlocking(t *testing.T) {
	t.Parallel()
	const payload = `{
		"id": "asset:legacy-prefix:asset-1",
		"payload": {
			"asset_id": "asset-1",
			"name": "a1",
			"source": "youtube",
			"embedding_version_text": "2026-06-26-v1",
			"embedding_version_transcript": "2026-06-26-v1",
			"embedding_version_visual": "2026-06-26-v1",
			"embedding_version_audio": "2026-06-26-v1"
		}
	}`
	srv := mockQdrantForVerifier(t, []string{payload})
	defer srv.Close()
	schema := qdrantSchema.DefaultV3Schema()
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())

	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 1)
	require.NoError(t, err)
	assert.False(t, report.Ready)
	assert.GreaterOrEqual(t, report.NonCanonicalPointCount, 1)
	found := false
	for _, e := range report.Errors {
		if strings.Contains(e, "non-canonical") {
			found = true
			break
		}
	}
	assert.True(t, found, "non-canonical pt.ID must be surfaced")
}
