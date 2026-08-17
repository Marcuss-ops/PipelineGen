package verification

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/indexing"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	qdrantSchema "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
)

// ─── PR 12 strict-count tests (ActualPoints == ExpectedPoints) ────────

// TestReindexVerifier_PointCountStrict_UnderCountBlocks — actual
// (=CountPoints) below expected blocks Ready. The QDRANT-003
// `actualPoints < expectedPoints` strict-inequality is preserved;
// PR 12 adds the symmetric over-count block from the user's spec.
func TestReindexVerifier_PointCountStrict_UnderCountBlocks(t *testing.T) {
	t.Parallel()
	canonicalID := qdrantSchema.AssetIDToQdrantPointID("asset-1")
	payload := fmt.Sprintf(`{
		"id": %q,
		"payload": {
			"asset_id": "asset-1",
			"name": "a1",
			"source": "youtube",
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
	// expectedPoints=2 → actual is 1 → strict mismatch blocks Ready.
	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 2)
	require.NoError(t, err)
	assert.False(t, report.Ready)
	assert.Equal(t, 1, report.ActualPoints)
	assert.Equal(t, 2, report.ExpectedPoints)
	found := false
	for _, e := range report.Errors {
		if strings.Contains(e, "point count mismatch") {
			found = true
			break
		}
	}
	assert.True(t, found, "strict mismatch must surface 'point count mismatch'")
}

// TestReindexVerifier_PointCountStrict_OverCountBlocks — symmetric
// PR 12 hardening: actual > expected blocks Ready (e.g. a partially-
// cancelled writer that produced duplicate points that don't round-
// trip through any SQLite row).
func TestReindexVerifier_PointCountStrict_OverCountBlocks(t *testing.T) {
	t.Parallel()
	canonicalID := qdrantSchema.AssetIDToQdrantPointID("asset-1")
	payload := fmt.Sprintf(`{
		"id": %q,
		"payload": {
			"asset_id": "asset-1",
			"name": "a1",
			"source": "youtube",
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
	// actual=1, expected=0 → strict inequality blocks Ready (was
	// accepted by >=).
	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 0)
	require.NoError(t, err)
	assert.False(t, report.Ready)
	assert.Equal(t, 1, report.ActualPoints)
}

// ─── PR 12 scroll-error-is-fatal test ───────────────────────────────

// TestReindexVerifier_ScrollErrorIsFatal_MidwayAbort — the second
// scroll page returns an error. PR 12 demands: returns non-nil err,
// Ready=false, CompleteScan=false, Errors include "PR 12 scroll page".
//
// The test installs a mock that returns ok on page 0 and a 500 on
// page 1.
func TestReindexVerifier_ScrollErrorIsFatal_MidwayAbort(t *testing.T) {
	t.Parallel()
	srv := mockQdrantForVerifierWithHooks(t, mockQdrantHooks{
		PagePayloads: []string{
			canonicalPointPayload("asset-1"),
		},
		// One clean page, then a non-empty NextOffset, then 500.
		PageNextOffsets: []string{"offset-1"},
		ErrorAfterPage:  1, // page 1 (=iteration 1) errors
	})
	defer srv.Close()
	schema := qdrantSchema.DefaultV3Schema()
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())
	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 1)
	require.Error(t, err, "PR 12: any scroll page error returns non-nil err")
	assert.False(t, report.Ready)
	assert.False(t, report.CompleteScan, "PR 12: CompleteScan=false on fatal page error")
	found := false
	for _, e := range report.Errors {
		if strings.Contains(e, "PR 12 scroll page") && strings.Contains(e, "fatal") {
			found = true
			break
		}
	}
	assert.True(t, found, "Errors must surface the PR 12 fatal scroll marker")
}

// ─── PR 12 maxScrolls-cap-is-blocking test ───────────────────────────

// TestReindexVerifier_MaxPagesCapBlocking — the mock always returns
// a non-empty NextOffset, never terminating the loop. After
// maxScrolls (400 in verifier.go) pages, the cap is hit and the
// verifier must abort with non-nil err + CompleteScan=false.
func TestReindexVerifier_MaxPagesCapBlocking(t *testing.T) {
	t.Parallel()
	// Single page payload; cap-binding occurs because NextOffset
	// never returns empty.
	srv := mockQdrantForVerifierWithHooks(t, mockQdrantHooks{
		InfiniteOffset: true,
		PagePayloads:   []string{canonicalPointPayload("asset-1")},
	})
	defer srv.Close()
	schema := qdrantSchema.DefaultV3Schema()
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())
	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 1)
	require.Error(t, err, "PR 12: cap hit with trailing NextOffset returns non-nil err")
	assert.False(t, report.Ready)
	assert.False(t, report.CompleteScan, "PR 12: CompleteScan=false on cap-block")
	found := false
	for _, e := range report.Errors {
		if strings.Contains(e, "PR 12 scroll iteration cap") {
			found = true
			break
		}
	}
	assert.True(t, found, "Errors must surface the PR 12 cap-block marker")
}

// ─── PR 12 non-canonical pt.ID strict-mismatch test ──────────────────

// TestReindexVerifier_NonCanonicalPointIDBlocking — a point whose
// pt.ID is a generic UUID (parseable but NOT equal to
// qdrantSchema.AssetIDToQdrantPointID(asset_id)) is BLOCKING. The previous
// uuid.Parse(pt.ID) accept-anything-UUID mask is gone.
func TestReindexVerifier_NonCanonicalPointIDBlocking(t *testing.T) {
	t.Parallel()
	const nonCanonicalUUID = "00000000-0000-0000-0000-000000000001"
	canonical := qdrantSchema.AssetIDToQdrantPointID("asset-1")
	if nonCanonicalUUID == canonical {
		t.Fatalf("test setup invariant: non-canonical UUID must differ from canonical")
	}
	payload := fmt.Sprintf(`{
		"id": %q,
		"payload": {
			"asset_id": "asset-1",
			"name": "a1",
			"source": "youtube",
			"embedding_version_text": "2026-06-26-v1",
			"embedding_version_transcript": "2026-06-26-v1",
			"embedding_version_visual": "2026-06-16-v1",
			"embedding_version_audio": "2026-06-26-v1"
		}
	}`, nonCanonicalUUID)
	srv := mockQdrantForVerifier(t, []string{payload})
	defer srv.Close()
	schema := qdrantSchema.DefaultV3Schema()
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())
	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 1)
	require.NoError(t, err)
	assert.False(t, report.Ready, "PR 12: non-canonical pt.ID must block Ready")
	assert.Equal(t, 1, report.NonCanonicalPointCount,
		"PR 12: NonCanonicalPointCount must bump on literal mismatch")
	assert.Contains(t, report.NonCanonicalPointIDs, nonCanonicalUUID,
		"non-canonical pt.ID must be reported in NonCanonicalPointIDs")
	found := false
	for _, e := range report.Errors {
		if strings.Contains(e, "PR 12 non-canonical pt.ID") {
			found = true
			break
		}
	}
	assert.True(t, found, "Errors must surface the PR 12 non-canonical marker")
}

// ─── PR 12 per-channel check runs on EVERY page ──────────────────────

// TestReindexVerifier_PerChannelCheckEveryPage_SampleRemoved —
// a point on page 3 (iteration=3) is missing the per-channel
// embedding_version_text key. The previous QDRANT-003 implementation
// only sampled the first 2 pages; PR 12 runs the gate on EVERY
// page. The mismatched channel must bump the per-channel counter
// AND block Ready.
func TestReindexVerifier_PerChannelCheckEveryPage_SampleRemoved(t *testing.T) {
	t.Parallel()
	// asset-on-page-0 is clean; asset-on-page-2 (iteration=2) is
	// missing embedding_version_text.
	page0 := canonicalPointPayload("asset-0")
	page1 := canonicalPointPayload("asset-2-missing-channel")
	// Strip the per-channel key for asset-2 so the test point is
	// valid except for the missing-channel vector.
	page1MissingChannel := strings.Replace(page1, `"embedding_version_text": "2026-06-26-v1",`, "", 1)
	srv := mockQdrantForVerifierWithHooks(t, mockQdrantHooks{
		PagePayloads: []string{
			canonicalPointPayload("asset-1"), // page 0
			page0,                            // page 1
			page1MissingChannel,              // page 2 — point on iteration=2
		},
		// 3 pages then empty offset.
		PageNextOffsets: []string{"offset-1", "offset-2", ""},
	})
	defer srv.Close()
	schema := qdrantSchema.DefaultV3Schema()
	assetStore := &stubAssetStore{ids: []string{"asset-1", "asset-0", "asset-2-missing-channel"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())
	// Total points = 3 (one per page). Cap the call to 3.
	report, err := v.VerifyReindex(context.Background(), "media_assets_v3", 3)
	// Note: page sequencing pulls asset-1 on iteration=0, asset-0 on
	// iteration=1, asset-2-missing-channel on iteration=2. Different
	// asset IDs but same payload shape.
	_ = page0
	require.NoError(t, err)
	assert.False(t, report.Ready, "PR 12: missing per-channel on iteration=2 must block (no sample-only!")
	assert.GreaterOrEqual(t, report.VersionMismatchPerChannel["text"], 1,
		"PR 12: text channel bump on iteration >= 2 (was sample-skipped before)")
}

// ─── PR 12 happy-path strict-equality pass ────────────────────────────

// TestReindexVerifier_PR12_HappyPath_AllGatesGreen — all PR 12
// gates green: counts match, CompleteScan=true, no missing/orphan/
// payload issues/version mismatches/non-canonical point IDs.
func TestReindexVerifier_PR12_HappyPath_AllGatesGreen(t *testing.T) {
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
	assert.True(t, report.CompleteScan, "PR 12: CompleteScan=true on clean exit")
	assert.True(t, report.Ready, "PR 12: ready when every gate green (strict equality)")
	assert.Equal(t, 0, report.NonCanonicalPointCount)
	assert.Equal(t, 0, report.PayloadIssues)
	channelTotal := 0
	for _, c := range report.VersionMismatchPerChannel {
		channelTotal += c
	}
	assert.Equal(t, 0, channelTotal, "PR 12 happy path: per-channel total == 0")

	// Task 7: GateDetails must be populated and every gate must pass.
	require.NotNil(t, report.GateDetails, "Task 7: GateDetails must be populated on verify")
	gd := report.GateDetails
	assert.True(t, gd.PointCountParity.Passed, "point_count_parity")
	assert.True(t, gd.CompleteScan.Passed, "complete_scan")
	assert.True(t, gd.MissingOrphan.Passed, "missing_orphan")
	assert.True(t, gd.PayloadValidation.Passed, "payload_validation")
	assert.True(t, gd.EmbeddingVersion.Passed, "embedding_version")
	assert.True(t, gd.CanonicalPointID.Passed, "canonical_point_id")
	assert.True(t, gd.DuplicatePoints.Passed, "duplicate_points")
	assert.True(t, gd.DeadLetters.Passed, "dead_letters")
	assert.True(t, gd.GoldenQueries.Passed, "golden_queries")
	assert.True(t, gd.FilterSmoke.Passed, "filter_smoke")
	assert.True(t, gd.ZeroErrors.Passed, "zero_errors")
}

// ─── helpers (extended for PR 12 tests) ──────────────────────────────

// canonicalPointPayload builds a Qdrant-shaped payload JSON for a
// single asset_id with the canonical pt.ID and all per-channel
// embedding_version_<channel> keys at the schema's ModelVersion.
// Used by PR 12 tests that need a happy-path point without per-test
// fmt.Sprintf boilerplate.
func canonicalPointPayload(assetID string) string {
	canonicalID := qdrantSchema.AssetIDToQdrantPointID(assetID)
	return fmt.Sprintf(`{
		"id": %q,
		"payload": {
			"asset_id": %q,
			"name": "n",
			"source": "youtube",
			"embedding_version_text": "2026-06-26-v1",
			"embedding_version_transcript": "2026-06-26-v1",
			"embedding_version_visual": "2026-06-16-v1",
			"embedding_version_audio": "2026-06-26-v1"
		}
	}`, canonicalID, assetID)
}

// mockQdrantHooks configures a richer mock with multi-page support
// and per-page error injection. Used by PR 12 tests that need to
// drive the scroll loop past the first page or inject a fatal error.
type mockQdrantHooks struct {
	// PagePayloads is the list of payloads returned per-page, in order.
	PagePayloads []string
	// PageNextOffsets controls the NextOffset per page. Empty trailing
	// string terminates the loop. Defaults: alternating non-empty
	// strings until PagePayloads exhausted, then "".
	PageNextOffsets []string
	// InfiniteOffset makes every page return a non-empty NextOffset
	// (used to drive the verifier's maxScrolls cap path).
	InfiniteOffset bool
	// ErrorAfterPage: scroll returns HTTP 500 from page N onward
	// (0-indexed). 0 = no error.
	ErrorAfterPage int
}

// mockQdrantForVerifierWithHooks installs an httptest server with
// the richer behaviour described by hooks. Backwards compatible
// with the simple mockQdrantForVerifier: passing only PagePayloads
// behaves the same.
func mockQdrantForVerifierWithHooks(t *testing.T, hooks mockQdrantHooks) *httptest.Server {
	t.Helper()
	if len(hooks.PagePayloads) == 0 {
		t.Fatalf("mockQdrantForVerifierWithHooks: PagePayloads must be non-empty")
	}

	var mu sync.Mutex
	pageIdx := 0

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/collections/media_assets_v3":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{
					"points_count": len(hooks.PagePayloads),
					"status":       "green",
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/collections/media_assets_v3/points/scroll":
			if hooks.ErrorAfterPage > 0 && pageIdx >= hooks.ErrorAfterPage {
				http.Error(w, "injected PR 12 scroll-fatal error", http.StatusInternalServerError)
				return
			}
			if pageIdx >= len(hooks.PagePayloads) {
				var noffset interface{}
				if hooks.InfiniteOffset {
					noffset = fmt.Sprintf("offset-%d", pageIdx)
				} else {
					noffset = nil
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": map[string]interface{}{
					"next_page_offset": noffset,
				}})
				return
			}
			body := hooks.PagePayloads[pageIdx]
			pageIdx++
			var wrapper map[string]interface{}
			_ = json.Unmarshal([]byte(body), &wrapper)
			raw, _ := json.Marshal(wrapper["payload"])
			id, _ := wrapper["id"].(string)
			pt := map[string]interface{}{
				"id":      id,
				"payload": json.RawMessage(raw),
			}
			var nextOffset string
			if hooks.InfiniteOffset {
				nextOffset = fmt.Sprintf("offset-%d", pageIdx)
			} else if pageIdx-1 < len(hooks.PageNextOffsets) {
				nextOffset = hooks.PageNextOffsets[pageIdx-1]
			} else {
				nextOffset = ""
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{
					"points":           []interface{}{pt},
					"next_page_offset": nextOffset,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

// ─── QDRANT-003-era tests (kept for regression coverage) ─────────────

// mockQdrantForVerifier installs an httptest server that mimics the
// Qdrant REST surface used by ReindexVerifier.VerifyReindex:
// /collections/<name> (point count), /collections/<name>/points/scroll
// (chunked payload delivery).
//
// QDRANT-005 closure (June 2026): the mock is now a THIN pass-through
// for the id field. PR 12 (June 2026) preserves this — the canonical
// boundary is the only authority for pt.ID; the mock does not transform.
func mockQdrantForVerifier(t *testing.T, payloadJSONs []string) *httptest.Server {
	return mockQdrantForVerifierWithHooks(t, mockQdrantHooks{
		PagePayloads:    payloadJSONs,
		PageNextOffsets: []string{""}, // single-page → empty offset on the only iteration
	})
}

// stubAssetStore is an inline indexing.AssetStore implementation. The verifier
// reads ListAllAssetIDs to compute missing/orphan; the test uses the
// minimum stub that produces a single-id list.
type stubAssetStore struct {
	ids []string
}

func (s *stubAssetStore) FetchAsset(ctx context.Context, id string) (*indexing.AssetData, error) {
	return nil, nil
}

func (s *stubAssetStore) ListAllAssetIDs(ctx context.Context) ([]string, error) {
	return s.ids, nil
}

func (s *stubAssetStore) FetchAssetBatch(ctx context.Context, afterID string, limit int) ([]*indexing.AssetData, error) {
	return nil, nil
}

func newClientAt(baseURL string) *transport.Client {
	return transport.NewClient(&qdrantSchema.Config{BaseURL: baseURL, Timeout: 5}, zap.NewNop())
}
