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

// ─────────────────────────────────────────────────────────────────────
// QDRANT-003 close-out tests (kept verbatim — TODO 12 must NOT regress
// these). The legacy global fallback test passes because the per-channel
// check fires on the missing/wrong per-channel key regardless of whether
// the global `embedding_version` field is set.
// ─────────────────────────────────────────────────────────────────────

// TestReindexVerifier_PerChannelVersionMismatch_PresentMismatch installs
// a mock Qdrant backend and asserts that a point whose per-channel
// payload["embedding_version_text"] does NOT match the schema's
// EmbeddingSpec.ModelVersion trips BOTH the global VersionMismatch AND
// the channel-level VersionMismatchPerChannel["text"] counter.
func TestReindexVerifier_PerChannelVersionMismatch_PresentMismatch(t *testing.T) {
	t.Parallel()

	const payload = `{
		"id": "pt-1",
		"payload": {
			"asset_id": "asset-1",
			"name": "a1",
			"source": "youtube",
			"embedding_version_text": "wrong-version"
		}
	}`
	srv := mockQdrantForVerifier(t, []string{payload}, "media_assets_v3")
	defer srv.Close()

	schema := DefaultV3Schema() // ModelVersion=2026-06-16-v1 for text+transcript+visual+audio
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())

	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 1)
	require.NoError(t, err)
	assert.Equal(t, 1, report.VersionMismatch)
	assert.Equal(t, 1, report.VersionMismatchPerChannel["text"])
	assert.False(t, report.Ready)
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
			"embedding_version_text": "2026-06-16-v1",
			"embedding_version_transcript": "2026-06-16-v1",
			"embedding_version_visual": "2026-06-16-v1",
			"embedding_version_audio": "2026-06-16-v1"
		}
	}`
	srv := mockQdrantForVerifier(t, []string{payload}, "media_assets_v3")
	defer srv.Close()

	schema := DefaultV3Schema()
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())

	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 1)
	require.NoError(t, err)
	assert.Equal(t, 0, report.VersionMismatch)
	assert.True(t, report.Ready)
}

// TestReindexVerifier_PerChannelVersionMismatch_AbsentPerChannelFails asserts
// that a point WITHOUT per-channel keys is rejected (legacy global fallback
// REMOVED under QDRANT-003 close-out + TODO 12 strict).
func TestReindexVerifier_PerChannelVersionMismatch_AbsentPerChannelFails(t *testing.T) {
	t.Parallel()

	const payload = `{
		"id": "pt-1",
		"payload": {
			"asset_id": "asset-1",
			"name": "a1",
			"source": "youtube"
		}
	}`
	srv := mockQdrantForVerifier(t, []string{payload}, "media_assets_v3")
	defer srv.Close()

	schema := DefaultV3Schema()
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())

	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 1)
	require.NoError(t, err)
	assert.Equal(t, 1, report.VersionMismatch)
	assert.False(t, report.Ready)
}

// TestReindexVerifier_PerChannelVersionMismatch_AbsentLegacyFallbackFail covers
// the migration-window case: a point with neither per-channel nor global
// embedding_version field set. Per-channel check fires.
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
	srv := mockQdrantForVerifier(t, []string{payload}, "media_assets_v3")
	defer srv.Close()

	schema := DefaultV3Schema()
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())

	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 1)
	require.NoError(t, err)
	assert.Equal(t, 1, report.VersionMismatch)
	assert.False(t, report.Ready)
}

// TestReindexVerifier_PerChannelCounter_BumpsOncePerPoint asserts the
// per-point-vs-per-channel invariant: many-channel mismatch → once per
// point globally, but per-channel counter increments per channel.
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
	srv := mockQdrantForVerifier(t, []string{payload}, "media_assets_v3")
	defer srv.Close()

	schema := DefaultV3Schema()
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())

	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 1)
	require.NoError(t, err)
	if report.VersionMismatch > 0 {
		assert.GreaterOrEqual(t, report.VersionMismatchPerChannel["text"], 1)
		assert.GreaterOrEqual(t, report.VersionMismatchPerChannel["visual"], 1)
		assert.GreaterOrEqual(t, report.VersionMismatchPerChannel["transcript"], 1)
		assert.Equal(t, 1, report.VersionMismatch, "global VersionMismatch must bump AT MOST once per point regardless of channel failures")
	}
}

// ─────────────────────────────────────────────────────────────────────
// QDRANT-008 / TODO 12 spec tests (June 2026): the 6 blocking conditions
// + 1 success path. Each test pins one spec scenario directly.
// ─────────────────────────────────────────────────────────────────────

// TestVerifier_BlocksOn_CountExtra asserts that a collection whose
// actual point count EXCEEDS the expected count from ReindexAll trips
// the Ready gate. Strict equality contract per TODO 12 spec test #1:
// any delta (extra OR missing) blocks the alias switch.
func TestVerifier_BlocksOn_CountExtra(t *testing.T) {
	t.Parallel()

	// Build a fully-compliant point; we will ask the mock to REPORT
	// 3 points via CountPoints but deliver only 1 scrollable payload.
	// This simulates a collection that grew extra orphan points after
	// a partial reindex — both delta and orphan must trip the gate.
	const payload = `{
		"id": "pt-1",
		"payload": {
			"asset_id": "asset-1",
			"name": "a1",
			"source": "youtube",
			"media_type": "video",
			"embedding_version_text": "2026-06-16-v1",
			"embedding_version_transcript": "2026-06-16-v1",
			"embedding_version_visual": "2026-06-16-v1"
		}
	}`
	srv := mockQdrantForVerifierAdvanced(t,
		[]string{payload},
		withCollection("media_assets_v3"),
		withCountOverride(3), // mock claims 3 points
	)
	defer srv.Close()

	schema := DefaultV3Schema()
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())

	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 1)
	require.NoError(t, err)
	assert.Equal(t, 3, report.ActualPoints, "mock claims count=3")
	assert.Equal(t, 1, report.ExpectedPoints)
	assert.False(t, report.Ready, "count extra must block Ready (TODO 12 strict)")
	require.NotEmpty(t, report.Errors, "must surface a count-mismatch error")
	assert.Contains(t, report.Errors[0], "point count mismatch")
}

// TestVerifier_BlocksOn_CountMissing asserts the inverse case: actual <
// expected blocks the gate too. The verifier's strict equality is
// symmetric for both extra and missing.
func TestVerifier_BlocksOn_CountMissing(t *testing.T) {
	t.Parallel()

	const payload = `{
		"id": "pt-1",
		"payload": {
			"asset_id": "asset-1",
			"name": "a1",
			"source": "youtube",
			"media_type": "video",
			"embedding_version_text": "2026-06-16-v1",
			"embedding_version_transcript": "2026-06-16-v1",
			"embedding_version_visual": "2026-06-16-v1"
		}
	}`
	srv := mockQdrantForVerifierAdvanced(t,
		[]string{payload},
		withCollection("media_assets_v3"),
		withCountOverride(1), // Qdrant reports 1 point
	)
	defer srv.Close()

	schema := DefaultV3Schema()
	assetStore := &stubAssetStore{ids: []string{"asset-1", "asset-2", "asset-3"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())

	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 3) // expected 3
	require.NoError(t, err)
	assert.Equal(t, 1, report.ActualPoints)
	assert.Equal(t, 3, report.ExpectedPoints)
	assert.False(t, report.Ready, "count missing must block Ready (TODO 12 strict)")
	require.NotEmpty(t, report.Errors, "must surface a count-mismatch error")
	assert.Contains(t, report.Errors[0], "point count mismatch")
}

// TestVerifier_BlocksOn_NonCanonicalPointID asserts that a UUID-form
// pt.ID that DOES NOT equal AssetIDToQdrantPointID(assetID) trips the
// PayloadIssues counter and blocks the Ready gate.
//
// Spec test #3: "Point ID non canonico (UUID che non corrisponde a
// AssetIDToQdrantPointID) blocca".
func TestVerifier_BlocksOn_NonCanonicalPointID(t *testing.T) {
	t.Parallel()

	// Hardcoded non-canonical UUID; AssetIDToQdrantPointID("asset-1")
	// will produce a different UUID. The verifier's two-step check:
	//  1. isUUIDForm(pt.ID) → true (this is a valid UUID).
	//  2. pt.ID != expectedID → true (mismatch) → PayloadIssues++.
	const payload = `{
		"id": "00000000-0000-4000-8000-000000000001",
		"payload": {
			"asset_id": "asset-1",
			"name": "a1",
			"source": "youtube",
			"media_type": "video",
			"embedding_version_text": "2026-06-16-v1",
			"embedding_version_transcript": "2026-06-16-v1",
			"embedding_version_visual": "2026-06-16-v1"
		}
	}`
	srv := mockQdrantForVerifier(t, []string{payload}, "media_assets_v3")
	defer srv.Close()

	schema := DefaultV3Schema()
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())

	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 1)
	require.NoError(t, err)
	require.NotZero(t, report.PayloadIssues, "non-canonical PointID must bump PayloadIssues")
	assert.False(t, report.Ready, "non-canonical PointID must block Ready (TODO 12 strict)")
	require.NotEmpty(t, report.Errors, "must surface a PointID-mismatch error")
	assert.Contains(t, report.Errors[0], "point UUID mismatch")
}

// TestVerifier_BlocksOn_MissingChannelVersion asserts that a point
// whose payload lacks one of the per-channel embedding_version_<channel>
// keys (per schema.DenseVectors) is rejected. The per-channel gate
// fires whenever ANY schema channel is missing from a scrolled point.
//
// Spec test #4: "versione mancante su uno dei canali blocca". We omit
// embedding_version_transcript here; the per-channel counter must
// surface this on the right channel.
func TestVerifier_BlocksOn_MissingChannelVersion(t *testing.T) {
	t.Parallel()

	// Payload has all schema channels EXCEPT transcript.
	const payload = `{
		"id": "pt-1",
		"payload": {
			"asset_id": "asset-1",
			"name": "a1",
			"source": "youtube",
			"media_type": "video",
			"embedding_version_text": "2026-06-16-v1",
			"embedding_version_visual": "2026-06-16-v1"
		}
	}`
	srv := mockQdrantForVerifier(t, []string{payload}, "media_assets_v3")
	defer srv.Close()

	schema := DefaultV3Schema()
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())

	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 1)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, report.VersionMismatchPerChannel["transcript"], 1, "missing transcript channel must bump per-channel counter")
	assert.False(t, report.Ready, "missing channel version must block Ready (TODO 12 strict)")
}

// TestVerifier_BlocksOn_ScanCapReached asserts that when the verifier's
// maxScrolls safety cap is hit, a "scroll limit reached" error is
// appended to report.Errors and Ready stays false. The mock uses
// withInfiniteScroll to keep returning empty-point pages with a
// non-empty next_page_offset, forcing the loop to iterate until the cap.
//
// Spec test #5: "scan incompleto (page cap raggiunto) blocca".
//
// NOTE: maxScrolls is 400 (200k points); the test triggers the cap
// path with a fast mock — ~400 page round-trips.
func TestVerifier_BlocksOn_ScanCapReached(t *testing.T) {
	// Do NOT t.Parallel() — this test makes 400 HTTP round-trips and
	// running it in parallel with the others slows the suite.

	const payload = `{
		"id": "pt-1",
		"payload": {
			"asset_id": "asset-1",
			"name": "a1",
			"source": "youtube",
			"media_type": "video",
			"embedding_version_text": "2026-06-16-v1",
			"embedding_version_transcript": "2026-06-16-v1",
			"embedding_version_visual": "2026-06-16-v1"
		}
	}`
	srv := mockQdrantForVerifierAdvanced(t,
		[]string{payload},
		withCollection("media_assets_v3"),
		withInfiniteScroll(), // every page → empty result + non-empty next_offset
	)
	defer srv.Close()

	schema := DefaultV3Schema()
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())

	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 1)
	require.NoError(t, err)
	assert.False(t, report.Ready, "scan-cap-reached must block Ready (TODO 12 strict)")
	require.NotEmpty(t, report.Errors, "must surface a scroll-limit-reached error")
	found := false
	for _, e := range report.Errors {
		if strings.Contains(e, "scroll limit reached") {
			found = true
			break
		}
	}
	assert.True(t, found, "errors must contain a 'scroll limit reached' diagnostic (got: %v)", report.Errors)
}

// TestVerifier_BlocksOn_MissingChannelForMediaType asserts the matrix
// gate: a point whose media_type is "video" but whose payload lacks
// embedding_version_transcript (a manifest-required channel for
// videos) bumps MissingChannelForMediaType and blocks the gate.
//
// Spec test #6: "canali mancanti per media_type (es. video senza
// transcript) blocca".
func TestVerifier_BlocksOn_MissingChannelForMediaType(t *testing.T) {
	t.Parallel()

	// Video point with all per-channel version keys EXCEPT transcript.
	const payload = `{
		"id": "pt-1",
		"payload": {
			"asset_id": "asset-1",
			"name": "a1",
			"source": "youtube",
			"media_type": "video",
			"embedding_version_text": "2026-06-16-v1",
			"embedding_version_visual": "2026-06-16-v1"
		}
	}`
	srv := mockQdrantForVerifier(t, []string{payload}, "media_assets_v3")
	defer srv.Close()

	schema := DefaultV3Schema()
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())

	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 1)
	require.NoError(t, err)
	// The per-channel check ALSO fires on missing transcript
	// (VersionMismatchPerChannel["transcript"]++) — the matrix gate
	// adds MissingChannelForMediaType++ on top of that.
	assert.GreaterOrEqual(t, report.VersionMismatchPerChannel["transcript"], 1, "per-channel check fires on missing transcript")
	assert.Equal(t, 1, report.MissingChannelForMediaType, "matrix gate bumps MissingChannelForMediaType")
	assert.Equal(t, 1, report.MissingChannelForMediaTypeDetail["transcript"], "per-channel breakdown surfaces the missing channel")
	assert.False(t, report.Ready, "missing channel for media_type must block Ready (TODO 12 strict)")
	require.NotEmpty(t, report.Errors, "must surface a matrix-gate error")
	found := false
	for _, e := range report.Errors {
		if strings.Contains(e, "matrix gate") {
			found = true
			break
		}
	}
	assert.True(t, found, "errors must contain a 'matrix gate' diagnostic (got: %v)", report.Errors)
}

// TestVerifier_SuccessPath asserts the happy path: a fully compliant
// point (correct count + correct UUID + every embedding_version key
// present and matching schema + media_type matrix satisfied) yields
// a Ready=true report.
//
// Spec test #7: "reindex success quando tutti i check passano".
func TestVerifier_SuccessPath(t *testing.T) {
	t.Parallel()

	// Compute the canonical PointID so the pt.ID matches
	// AssetIDToQdrantPointID(assetID) exactly. Without this the
	// Ready=false path fires on non-canonical UUID (test #3).
	expected := AssetIDToQdrantPointID("asset-1")
	require.NotEmpty(t, expected, "AssetIDToQdrantPointID must produce a canonical UUID")
	// %q is safe here: AssetIDToQdrantPointID returns a UUID v5 hex
	// string (no characters that need JSON escaping). Slot it
	// directly via %q so the test stays next to the assertion.
	payload := fmt.Sprintf(`{
		"id": %q,
		"payload": {
			"asset_id": "asset-1",
			"name": "a1",
			"source": "youtube",
			"media_type": "video",
			"embedding_version_text": "2026-06-16-v1",
			"embedding_version_transcript": "2026-06-16-v1",
			"embedding_version_visual": "2026-06-16-v1"
		}
	}`, expected)

	srv := mockQdrantForVerifier(t, []string{payload}, "media_assets_v3")
	defer srv.Close()

	schema := DefaultV3Schema()
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())

	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 1)
	require.NoError(t, err)
	assert.Equal(t, 1, report.ActualPoints)
	assert.Equal(t, 0, report.MissingCount)
	assert.Equal(t, 0, report.OrphanCount)
	assert.Equal(t, 0, report.PayloadIssues)
	assert.Equal(t, 0, report.VersionMismatch)
	assert.Equal(t, 0, report.MissingChannelForMediaType)
	assert.Equal(t, 0, report.NonUUIDPointIDs)
	assert.Empty(t, report.Errors, "no errors expected on success path")
	assert.True(t, report.Ready, "happy path with all gates passing must yield Ready=true")
}

// ── helpers ──────────────────────────────────────────────────────────

// mockQdrantForVerifier installs an httptest server that mimics the
// Qdrant REST surface used by ReindexVerifier.VerifyReindex: count
// points + scroll. Single-page (counts = len(payloads), single scroll
// delivery). Existing tests rely on this minimal surface.
func mockQdrantForVerifier(t *testing.T, payloadJSONs []string, collection string) *httptest.Server {
	t.Helper()
	return mockQdrantForVerifierAdvanced(t, payloadJSONs, withCollection(collection))
}

// verifierMockOpts are optional behaviors layered on the standard
// mockQdrantForVerifier. Use the helpers below to construct them.
type verifierMockOpts struct {
	collection       string
	countOverride    int
	countOverrideSet bool
	infiniteScroll   bool
}

// verifierMockOpt is a functional option applied to verifierMockOpts.
type verifierMockOpt func(*verifierMockOpts)

// withCollection overrides the target collection name (default
// "media_assets_v3"). Affects both CountPoints and ScrollPoints
// routing.
func withCollection(name string) verifierMockOpt {
	return func(o *verifierMockOpts) { o.collection = name }
}

// withCountOverride overrides the points_count returned by CountPoints
// (default = len(payloadJSONs)). Use to simulate a Qdrant-reported
// count that diverges from the actual number of scrollable payloads.
func withCountOverride(n int) verifierMockOpt {
	return func(o *verifierMockOpts) {
		o.countOverride = n
		o.countOverrideSet = true
	}
}

// withInfiniteScroll causes the mock to keep returning empty-point
// pages with a non-empty next_page_offset after the initial payload
// set has been delivered. Forces the verifier to iterate its scroll
// loop until the maxScrolls safety cap.
func withInfiniteScroll() verifierMockOpt {
	return func(o *verifierMockOpts) { o.infiniteScroll = true }
}

// mockQdrantForVerifierAdvanced installs an httptest server with
// optional count-override and infinite-pagination behaviors layered
// on top of the standard count + scroll surface.
func mockQdrantForVerifierAdvanced(t *testing.T, payloadJSONs []string, opts ...verifierMockOpt) *httptest.Server {
	t.Helper()

	cfg := verifierMockOpts{collection: "media_assets_v3"}
	for _, opt := range opts {
		opt(&cfg)
	}

	var mu sync.Mutex
	payloadIdx := 0
	pageIdx := 0

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/collections/"+cfg.collection:
			count := len(payloadJSONs)
			if cfg.countOverrideSet {
				count = cfg.countOverride
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{
					"points_count": count,
					"status":       "green",
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/collections/"+cfg.collection+"/points/scroll":
			pageIdx++
			if cfg.infiniteScroll && pageIdx > len(payloadJSONs) {
				// Empty points, non-empty next_offset → loop continues.
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"result": map[string]interface{}{
						"points":           []interface{}{},
						"next_page_offset": fmt.Sprintf("page-%d", pageIdx),
					},
				})
				return
			}
			if payloadIdx >= len(payloadJSONs) {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": map[string]interface{}{}})
				return
			}
			body := payloadJSONs[payloadIdx]
			payloadIdx++
			var wrapper map[string]interface{}
			_ = json.Unmarshal([]byte(body), &wrapper)
			raw, _ := json.Marshal(wrapper["payload"])
			pt := map[string]interface{}{
				"id":      wrapper["id"],
				"payload": json.RawMessage(raw),
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{
					"points":           []interface{}{pt},
					"next_page_offset": "",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

// stubAssetStore is an inline AssetStore implementation. The verifier
// reads ListAllAssetIDs to compute missing/orphan.
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
