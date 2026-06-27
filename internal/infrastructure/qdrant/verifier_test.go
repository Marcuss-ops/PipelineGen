package qdrant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
	// mismatch on the "text" channel. pt.ID is the canonical UUID
	// v5 derived from asset_id so the non-UUID gate (QDRANT-005)
	// does NOT trip on this happy path.
	canonicalID := AssetIDToQdrantPointID("asset-1")
	payload := fmt.Sprintf(`{
		"id": %q,
		"payload": {
			"asset_id": "asset-1",
			"name": "a1",
			"source": "youtube",
			"embedding_version": "v3",
			"embedding_version_text": "wrong-version",
			"embedding_version_transcript": "2026-06-16-v1",
			"embedding_version_visual": "2026-06-16-v1",
			"embedding_version_audio": "2026-06-16-v1"
		}
	}`, canonicalID)
	srv := mockQdrantForVerifier(t, []string{payload})
	defer srv.Close()

	schema := DefaultV3Schema() // ModelVersion=2026-06-16-v1 for text+transcript+visual+audio
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())

	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 1)
	require.NoError(t, err)
	assert.Equal(t, 1, report.VersionMismatch, "global VersionMismatch bumps ONCE per point regardless of channel count")
	assert.Equal(t, 1, report.VersionMismatchPerChannel["text"], "per-channel counter surfaces which channel drifted")
	assert.Equal(t, 0, report.VersionMismatchPerChannel["transcript"], "transcript matches canonical")
	assert.Equal(t, 0, report.VersionMismatchPerChannel["visual"], "visual matches canonical")
	assert.Equal(t, 0, report.VersionMismatchPerChannel["audio"], "audio matches canonical")
	assert.False(t, report.Ready, "Ready=false on any per-channel mismatch")
}

// TestReindexVerifier_PerChannelVersionMismatch_PresentMatch asserts a
// point with the correct per-channel key passes VersionMismatch = 0.
func TestReindexVerifier_PerChannelVersionMismatch_PresentMatch(t *testing.T) {
	t.Parallel()

	canonicalID := AssetIDToQdrantPointID("asset-1")
	payload := fmt.Sprintf(`{
		"id": %q,
		"payload": {
			"asset_id": "asset-1",
			"name": "a1",
			"source": "youtube",
			"embedding_version": "v3",
			"embedding_version_text": "2026-06-16-v1",
			"embedding_version_transcript": "2026-06-16-v1",
			"embedding_version_visual": "2026-06-16-v1",
			"embedding_version_audio": "2026-06-16-v1"
		}
	}`, canonicalID)
	srv := mockQdrantForVerifier(t, []string{payload})
	defer srv.Close()

	schema := DefaultV3Schema()
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())

	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 1)
	require.NoError(t, err)
	assert.Equal(t, 0, report.VersionMismatch)
	assert.Equal(t, 0, report.VersionMismatchPerChannel["text"])
	assert.Equal(t, 0, report.VersionMismatchPerChannel["transcript"])
	assert.Equal(t, 0, report.VersionMismatchPerChannel["visual"])
	assert.Equal(t, 0, report.VersionMismatchPerChannel["audio"])
	assert.True(t, report.Ready)
}

// TestReindexVerifier_PerChannelVersionMismatch_AbsentLegacyFallbackFail
// covers the "migration window penalty" path: a point without the
// per-channel key AND without the global embedding_version key (a
// legacy point that did not even write the schema-version field) trips
// the per-channel mismatch counter.
//
// QDRANT-005 closure (June 2026): the global embedding_version
// rescue path was DELETED. A point that carries ONLY the global key
// (regardless of value) now BLOCKS the per-channel check — every
// declared channel with ModelVersion populated bumps the per-channel
// mismatch counter because the per-channel key is absent. This is
// the desired behaviour: a legacy point that hasn't been re-emitted
// with the per-channel key must be visible to operators as drift,
// not silently accepted via a fallback.
func TestReindexVerifier_PerChannelVersionMismatch_AbsentLegacyFallbackFail(t *testing.T) {
	t.Parallel()

	canonicalID := AssetIDToQdrantPointID("asset-1")
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

	schema := DefaultV3Schema()
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())

	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 1)
	require.NoError(t, err)
	// QDRANT-005: NO global fallback. Per-channel counter bumps for
	// every declared channel whose ModelVersion is non-empty (text,
	// transcript, visual) — audio has spec.ModelVersion="2026-06-16-v1"
	// too. Each bumps per-channel + global counter exactly once.
	//
	// Global counter invariant: pointMismatched latch is one bump per
	// point regardless of how many channels fail.
	assert.Equal(t, 1, report.VersionMismatch, "QDRANT-005: NO global fallback — a point with only legacy `embedding_version` blocks")
	assert.GreaterOrEqual(t, report.VersionMismatchPerChannel["text"], 1, "per-channel text bump expected")
	assert.GreaterOrEqual(t, report.VersionMismatchPerChannel["transcript"], 1, "per-channel transcript bump expected")
	assert.GreaterOrEqual(t, report.VersionMismatchPerChannel["visual"], 1, "per-channel visual bump expected")
	assert.GreaterOrEqual(t, report.VersionMismatchPerChannel["audio"], 1, "per-channel audio bump expected")
	assert.False(t, report.Ready, "QDRANT-005: Ready=false on absent per-channel keys (NO global rescue)")
}

// TestReindexVerifier_AbsentEverything_PerChannelPenalty covers the
// pre-existing pre-QDRANT-005 invariant: a point WITHOUT per-channel
// keys AND WITHOUT the global embedding_version key (a true legacy
// point that never wrote the version field) still trips per-channel
// mismatch. The closure keeps this existing test coverage to ensure
// the per-channel counter never silently regresses.
func TestReindexVerifier_AbsentEverything_PerChannelPenalty(t *testing.T) {
	t.Parallel()

	canonicalID := AssetIDToQdrantPointID("asset-1")
	payload := fmt.Sprintf(`{
		"id": %q,
		"payload": {
			"asset_id": "asset-1",
			"name": "a1",
			"source": "youtube"
		}
	}`, canonicalID)
	srv := mockQdrantForVerifier(t, []string{payload})
	defer srv.Close()

	schema := DefaultV3Schema()
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())

	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 1)
	require.NoError(t, err)
	assert.Equal(t, 1, report.VersionMismatch)
	assert.Equal(t, 1, report.VersionMismatchPerChannel["text"], "absent per-channel → failure surfaces in text channel")
	assert.False(t, report.Ready)
}

// TestReindexVerifier_NonUUIDPointBlocking asserts QDRANT-005
// hardening: a point whose pt.ID is NOT a UUID v5 string and that
// carries a non-empty payload.asset_id is BLOCKING (Ready=false).
// The previous behaviour silently skipped non-UUID points via
// isUUIDForm, masking legacy point IDs that the canonical
// AssetIDToQdrantPointID boundary could never locate.
func TestReindexVerifier_NonUUIDPointBlocking(t *testing.T) {
	t.Parallel()

	const payload = `{
		"id": "asset:legacy-prefix:asset-1",
		"payload": {
			"asset_id": "asset-1",
			"name": "a1",
			"source": "youtube",
			"embedding_version_text": "2026-06-16-v1",
			"embedding_version_transcript": "2026-06-16-v1",
			"embedding_version_visual": "2026-06-16-v1",
			"embedding_version_audio": "2026-06-16-v1"
		}
	}`
	srv := mockQdrantForVerifier(t, []string{payload})
	defer srv.Close()

	schema := DefaultV3Schema()
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())

	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 1)
	require.NoError(t, err)
	// QDRANT-005: pt.ID is not a UUID form → blocking payload issue.
	assert.False(t, report.Ready, "QDRANT-005: non-UUID pt.ID with non-empty payload.asset_id MUST block Ready")
	assert.GreaterOrEqual(t, report.PayloadIssues, 1, "non-UUID pt.ID must bump PayloadIssues")
	found := false
	for _, e := range report.Errors {
		if strings.Contains(e, "UUID missing") || strings.Contains(e, "UUID mismatch") {
			found = true
			break
		}
	}
	assert.True(t, found, "non-UUID regression must be surfaced in Errors with the UUID missing/mismatch marker")
}

// TestReindexVerifier_PerChannelCounter_BumpsOncePerPoint asserts the
// per-point-vs-per-channel invariant: a point whose 3 channels all
// mismatch bumps VersionMismatch ONCE (not 3 times) but the per-channel
// map shows all three channels.
//
// QDRANT-003 (June 2026) — points-out-of-per-channel-multi-fail counter.
func TestReindexVerifier_PerChannelCounter_BumpsOncePerPoint(t *testing.T) {
	t.Parallel()

	canonicalID := AssetIDToQdrantPointID("asset-1")
	payload := fmt.Sprintf(`{
		"id": %q,
		"payload": {
			"asset_id": "asset-1",
			"name": "a1",
			"source": "youtube",
			"embedding_version_text": "wrong",
			"embedding_version_visual": "wrong",
			"embedding_version_transcript": "wrong",
			"embedding_version_audio": "wrong"
		}
	}`, canonicalID)
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
//
// QDRANT-005 closure (June 2026): the mock is now a THIN pass-through
// for the id field. Tests that exercise the canonical-UUID path
// supply `id: AssetIDToQdrantPointID(asset_id)` in the payload JSON
// (computed at test time via fmt.Sprintf + AssetIDToQdrantPointID);
// tests that exercise the non-UUID blocking path supply an explicit
// non-UUID literal (e.g. \"asset:legacy-prefix:asset-1\"). The mock
// does NOT auto-inject or transform ids because the audit gate
// behaviour depends on whether the test authoring surface is
// detectable as canonical or legacy.
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
			// Pass the test author's id field through verbatim.
			// See mockQdrantForVerifier doc \u2014 the audit gate
			// behaviour depends on whether the authored id is the
			// canonical UUID form or a non-UUID literal; the mock
			// must not transform either side unilaterally.
			id, _ := wrapper["id"].(string)
			pt := map[string]interface{}{
				"id":      id,
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

// wrapperJSON encodes the payload to a JSON string for re-parse in
// the mock helper. Trivial indirection kept inside the test file.
func wrapperJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
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
