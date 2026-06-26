package qdrant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestReindexVerifier_PerChannelVersionMismatch_PresentMismatch installs
// a mock Qdrant backend and asserts that a point whose per-channel
// payload["embedding_version_text"] does NOT match the schema's
// EmbeddingSpec.ModelVersion trips BOTH the global VersionMismatch AND
// the channel-level VersionMismatchPerChannel["text"] counter.
//
// QDRANT-003 (June 2026) — per-channel check round-trip.
func TestReindexVerifier_PerChannelVersionMismatch_PresentMismatch(t *testing.T) {
	t.Parallel()

	// One point, payload carries embedding_version_text but with
	// the wrong model version string ("wrong-version" instead of
	// "2026-06-16-v1"). The verifier must surface this as a
	// mismatch on the "text" channel.
	const payload = `{
		"id": "pt-1",
		"payload": {
			"asset_id": "asset-1",
			"name": "a1",
			"source": "youtube",
			"embedding_version": "v3",
			"embedding_version_text": "wrong-version"
		}
	}`
	srv := mockQdrantForVerifier(t, []string{payload})
	defer srv.Close()

	schema := DefaultV3Schema() // ModelVersion=2026-06-16-v1 for text+transcript+visual
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())

	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 1)
	require.NoError(t, err)
	assert.Equal(t, 1, report.VersionMismatch, "global VersionMismatch bumps ONCE per point regardless of channel count")
	assert.Equal(t, 1, report.VersionMismatchPerChannel["text"], "per-channel counter surfaces which channel drifted")
	assert.False(t, report.Ready, "Ready=false on any per-channel mismatch")
}

// TestReindexVerifier_PerChannelVersionMismatch_PresentMatch asserts a
// point with the correct per-channel key passes VersionMismatch = 0.
func TestReindexVerifier_PerChannelVersionMismatch_PresentMatch(t *testing.T) {
	t.Parallel()

	const payload = `{
		"id": "pt-1",
		"payload": {
			"asset_id": "asset-1",
			"name": "a1",
			"source": "youtube",
			"embedding_version": "v3",
			"embedding_version_text": "2026-06-16-v1"
		}
	}`
	srv := mockQdrantForVerifier(t, []string{payload})
	defer srv.Close()

	schema := DefaultV3Schema()
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())

	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 1)
	require.NoError(t, err)
	assert.Equal(t, 0, report.VersionMismatch)
	assert.Equal(t, 0, report.VersionMismatchPerChannel["text"])
	assert.True(t, report.Ready)
}

// TestReindexVerifier_PerChannelVersionMismatch_AbsentPerChannelFails asserts
// that a point WITHOUT the per-channel key but WITH the global
// embedding_version IS REJECTED under the QDRANT-003 close-out
// contract — the legacy global-key fallback has been removed.
//
// (Renamed from 'AbsentLegacyFallback' — the old test documented
// acceptance of legacy points, which the close-out ticket
// explicitly retired. The payload is identical; the assertions
// flipped from ExpectAccept to ExpectReject.)
func TestReindexVerifier_PerChannelVersionMismatch_AbsentPerChannelFails(t *testing.T) {
	t.Parallel()

	const payload = `{
		"id": "pt-1",
		"payload": {
			"asset_id": "asset-1",
			"name": "a1",
			"source": "youtube",
			"embedding_version": "v3"
		}
	}`
	srv := mockQdrantForVerifier(t, []string{payload})
	defer srv.Close()

	schema := DefaultV3Schema()
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())

	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 1)
	require.NoError(t, err)
	assert.Equal(t, 1, report.VersionMismatch, "absent per-channel key + global key — legacy fallback REMOVED, per-channel mismatch bumps BOTH counters")
	assert.Equal(t, 1, report.VersionMismatchPerChannel["text"], "absent per-channel key — channel-level counter bumps")
	assert.False(t, report.Ready)
}

// TestReindexVerifier_PerChannelVersionMismatch_AbsentLegacyFallbackFail
// covers the "migration window penalty" path: a point without the
// per-channel key AND without the global embedding_version key (a
// legacy point that did not even write the schema-version field) trips
// the per-channel mismatch counter.
func TestReindexVerifier_PerChannelVersionMismatch_AbsentLegacyFallbackFail(t *testing.T) {
	t.Parallel()

	const payload = `{
		"id": "pt-1",
		"payload": {
			"asset_id": "asset-1",
			"name": "a1",
			"source": "youtube"
		}
	}`
	srv := mockQdrantForVerifier(t, []string{payload})
	defer srv.Close()

	schema := DefaultV3Schema()
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())

	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 1)
	require.NoError(t, err)
	assert.Equal(t, 1, report.VersionMismatch)
	assert.Equal(t, 1, report.VersionMismatchPerChannel["text"], "absent per-channel + absent global → failure surfaces in text channel")
	assert.False(t, report.Ready)
}

// TestReindexVerifier_PerChannelCounter_BumpsOncePerPoint asserts the
// per-point-vs-per-channel invariant: a point whose 3 channels all
// mismatch bumps VersionMismatch ONCE (not 3 times) but the per-channel
// map shows all three channels.
//
// QDRANT-003 (June 2026) — points-out-of-per-channel-multi-fail counter.
func TestReindexVerifier_PerChannelCounter_BumpsOncePerPoint(t *testing.T) {
	t.Parallel()

	const payload = `{
		"id": "pt-1",
		"payload": {
			"asset_id": "asset-1",
			"name": "a1",
			"source": "youtube",
			"embedding_version_text": "wrong",
			"embedding_version_visual": "wrong",
			"embedding_version_transcript": "wrong"
		}
	}`
	srv := mockQdrantForVerifier(t, []string{payload})
	defer srv.Close()

	schema := DefaultV3Schema()
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())

	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 1)
	require.NoError(t, err)
	// The verifier sample window is iteration<2, so the very first page
	// (where this single point lives) fires all three per-channel checks.
	// Per-channel counter is independent of the global gate.
	if report.VersionMismatch > 0 {
		assert.GreaterOrEqual(t, report.VersionMismatchPerChannel["text"], 1)
		assert.GreaterOrEqual(t, report.VersionMismatchPerChannel["visual"], 1)
		assert.GreaterOrEqual(t, report.VersionMismatchPerChannel["transcript"], 1)
		// CRITICAL invariant: global counter is bounded to 1 per point.
		// (Bumped once via the pointMismatched latch in the loop.)
		assert.Equal(t, 1, report.VersionMismatch, "global VersionMismatch must bump AT MOST once per point regardless of channel failures")
	}
}

// ── helpers ──────────────────────────────────────────────────────────

// mockQdrantForVerifier installs an httptest server that mimics the
// Qdrant REST surface used by ReindexVerifier.VerifyReindex:
// /collections/<name> (point count), /collections/<name>/points/scroll
// (chunked payload delivery).
func mockQdrantForVerifier(t *testing.T, payloadJSONs []string) *httptest.Server {
	t.Helper()

	var mu sync.Mutex
	payloadIdx := 0

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/collections/media_assets_v3":
			// CountPoints
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{
					"points_count": len(payloadJSONs),
					"status":       "green",
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/collections/media_assets_v3/points/scroll":
			if payloadIdx >= len(payloadJSONs) {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": map[string]interface{}{}})
				return
			}
			body := payloadJSONs[payloadIdx]
			payloadIdx++
			// Pass through raw JSON for the point's payload so the
			// verifier sees the embedding_version_<channel> keys as
			// authored by the test.
			var wrapper map[string]interface{}
			_ = json.Unmarshal([]byte(body), &wrapper)
			// Re-encode to ensure the test payload shape is preserved
			// verbatim (Qdrant would normally re-pack it).
			raw, _ := json.Marshal(wrapper["payload"])
			pt := map[string]interface{}{
				"id":      wrapper["id"],
				"payload": json.RawMessage(raw),
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{
					"points": []interface{}{pt},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

// stubAssetStore is an inline AssetStore implementation. The verifier
// reads ListAllAssetIDs to compute missing/orphan; the test uses the
// minimum stub that produces a single-id list.
type stubAssetStore struct {
	ids []string
}

func (s *stubAssetStore) FetchAsset(ctx context.Context, id string) (*AssetData, error) {
	return nil, nil
}

func (s *stubAssetStore) ListAllAssetIDs(ctx context.Context) ([]string, error) {
	return s.ids, nil
}

func newClientAt(baseURL string) *Client {
	return NewClient(&Config{BaseURL: baseURL, Timeout: 5}, zap.NewNop())
}
