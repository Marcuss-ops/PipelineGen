// Package qdrant — reaper_test.go (QDRANT-005, June 2026).
//
// Smoke tests for the Reaper contract changes introduced in
// QDRANT-005:
//   1. DefaultReaperKeys is empty (no auto-redaction of canonical
//      fields like "status").
//   2. Hard batch cap (MaxReaperBatchSize = 100) is enforced — the
//      result BatchCapped field reports the cap event so dashboards
//      can detect it.
//   3. Reap with no keys specified returns StatusNoop without
//      touching Qdrant — defense in depth for ops who forget to
//      opt in.
//   4. redactPayload strips the requested keys from a payload
//      subset and leaves other keys untouched.
//
// Behavioural coverage of the OverwritePayload call path (the
// vector-preserving mutation) lives in client_test.go (QDRANT-005
// follow-up); this file pins the canonical contract surface so a
// future refactor cannot regress the DefaultReaperKeys security
// boundary silently.

package maintenance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
)

// TestDefaultReaperKeys_EmptyByDefault pins the QDRANT-005 closure:
// DefaultReaperKeys no longer lists canonical payload keys
// (status, drive_link, local_path, download_link). Any future
// change that re-adds a key to this default list is a regression
// and must trip a test failure here.
func TestDefaultReaperKeys_EmptyByDefault(t *testing.T) {
	t.Parallel()

	assert.Empty(t, DefaultReaperKeys,
		"QDRANT-005 closure: DefaultReaperKeys MUST be empty. Re-adding canonical keys (status, drive_link, local_path, download_link) re-introduces the search-outage bug. Use explicit ReaperOptions.Keys per-run instead.")
}

// TestReaperOptions_KeysOptIn verifies that Reap with no explicit
// keys produces a no-op StatusNoop result — defense in depth so ops
// who forget to set ReaperOptions.Keys cannot accidentally delete
// fields. (Without this contract the previous default would have
// run "successfully" while stripping status, drive_link etc.)
func TestReaperOptions_KeysOptIn(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Any HTTP call would be a regression — Reap must NOT hit
		// Qdrant when DefaultReaperKeys is empty AND Keys is empty.
		t.Errorf("unexpected Qdrant call: %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}))
	defer srv.Close()

	client := transport.NewClient(&schema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	r := NewReaper(client, zap.NewNop())

	result, err := r.Reap(context.Background(), ReaperOptions{
		Collection: "media_assets_v3",
		// Keys:     nil,    // explicit empty
		// BatchSize: 100,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, StatusNoop, result.Status, "Reap with no keys MUST report StatusNoop (QDRANT-005: no auto-redaction)")
	assert.Equal(t, 0, result.PointsAffected, "Reap with no keys MUST not scan / redact any points")
	require.NotEmpty(t, result.Errors, "Reap with no keys MUST surface a defensive note so operators see why no work was done")
	assert.Contains(t, result.Errors[0], "no keys specified",
		"Reap with no keys MUST surface a defensive 'no keys specified' note")
}

// TestReap_BatchHardCap pins the QDRANT-005 closure: BatchSize > MaxReaperBatchSize
// is HARD-capped (not just log-warning). The previous impl logged a
// Warning but still passed through to Qdrant, which would return an
// error on pages > 100. The new impl clamps batch to MaxReaperBatchSize
// and bumps BatchCapped on the result, so dashboards can detect the
// cap event.
func TestReap_BatchHardCap(t *testing.T) {
	t.Parallel()

	// Capture each scroll request's body so we can inspect the
	// effective `limit` the reaper actually sent. The cap test
	// asserts that ANY limit > MaxReaperBatchSize is hard-clamped
	// before reaching Qdrant.
	type scrollReq struct {
		Limit int `json:"limit"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/collections/media_assets_v3/points/scroll":
			var body scrollReq
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
				if body.Limit > MaxReaperBatchSize {
					t.Errorf("QDRANT-005 hard-cap regression: scroll request sent limit=%d (> MaxReaperBatchSize=%d)", body.Limit, MaxReaperBatchSize)
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"result":{"points":[],"next_page_offset":""}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := transport.NewClient(&schema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	r := NewReaper(client, zap.NewNop())

	result, err := r.Reap(context.Background(), ReaperOptions{
		Collection: "media_assets_v3",
		// Opt-in to a single, harmless redaction key so we reach
		// the scroll loop (Reap with empty Keys returns StatusNoop).
		Keys:      []string{"obsolete_legacy_key"},
		BatchSize: 250, // request > MaxReaperBatchSize to test the hard cap.
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, 1, result.BatchCapped,
		"QDRANT-005 closure: BatchSize > MaxReaperBatchSize MUST hard-cap; BatchCapped=1 is the operator-visible signal.")
}

// TestRedactPayload_StripsOnlyRequestedKeys pins the redactPayload
// helper contract: only the requested keys are stripped, every
// other key+value is preserved untouched. Catches accidental
// re-addition of DefaultReaperKeys via a shared helper mutation.
func TestRedactPayload_StripsOnlyRequestedKeys(t *testing.T) {
	t.Parallel()

	payload := map[string]interface{}{
		"asset_id":        "asset-1",
		"status":          "ready",
		"lifecycle_state": "active",
		"workspace_id":    "ws-1",
		"name":            "Test clip",
		"tags":            []string{"a", "b"},
	}
	cleaned, stripped := redactPayload(payload, []string{"status"})

	assert.True(t, stripped, "stripping exactly one key MUST report stripped=true")
	assert.NotContains(t, cleaned, "status", "the requested redact key MUST be removed")
	assert.Equal(t, "asset-1", cleaned["asset_id"], "non-redacted keys MUST be preserved verbatim")
	assert.Equal(t, "active", cleaned["lifecycle_state"], "non-redacted keys MUST be preserved verbatim")
	assert.Equal(t, "ws-1", cleaned["workspace_id"], "non-redacted keys MUST be preserved verbatim")
	assert.Equal(t, []string{"a", "b"}, cleaned["tags"], "non-redacted slices MUST be preserved verbatim")
	assert.NotNil(t, cleaned, "cleaned payload MUST be returned (not nil) so the caller can re-upsert safely")
}

// TestMaxReaperBatchSize_Const pins the canonical cap value so any
// future relaxation of the cap (e.g. raising to 200 because "Qdrant
// now accepts larger pages") is a deliberate, audited change.
func TestMaxReaperBatchSize_Const(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 100, MaxReaperBatchSize,
		"MaxReaperBatchSize is the Qdrant REST scroll-page ceiling; changing it requires a Qdrant version probe + product signoff")
}

// ── P1 QDRANT-REAPER PointsSelector tests (July 2026) ────────────────

// TestReaper_IdleCycle_SinglePage verifies that when the selector
// matches zero points, the Reap completes with StatusNoop and
// touches zero points — no unnecessary OverwritePayload calls.
func TestReaper_IdleCycle_SinglePage(t *testing.T) {
	t.Parallel()

	var overwriteCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/collections/test-collection/points/scroll":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"result":{"points":[
				{"id":"pt-1","payload":{"asset_id":"a1","name":"clean-1","source":"youtube"}},
				{"id":"pt-2","payload":{"asset_id":"a2","name":"clean-2","source":"artlist"}},
				{"id":"pt-3","payload":{"asset_id":"a3","name":"clean-3","source":"stock"}}
			],"next_page_offset":""}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/collections/test-collection/points/payload":
			overwriteCalls++
			t.Error("unexpected OverwritePayload call — idle cycle should not mutate Qdrant")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := transport.NewClient(&schema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	r := NewReaper(client, zap.NewNop())

	result, err := r.Reap(context.Background(), ReaperOptions{
		Collection: "test-collection",
		Keys:       []string{"obsolete_key"},
		// All points carry asset_id/name/source — none have "obsolete_key".
	})
	require.NoError(t, err)
	assert.Equal(t, 3, result.PointsScanned)
	assert.Equal(t, 0, result.PointsAffected)
	assert.Equal(t, StatusNoop, result.Status,
		"idle cycle with zero matching points must return StatusNoop")
	assert.Equal(t, 0, overwriteCalls,
		"idle cycle must not call OverwritePayload")
}

// TestReaper_DeleteCycle_OrphanPoints verifies that the selector
// correctly identifies points with redactable keys and the Reaper
// calls OverwritePayload to strip them.
func TestReaper_DeleteCycle_OrphanPoints(t *testing.T) {
	t.Parallel()

	var overwritePayloads []schema.PointPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/collections/test-collection/points/scroll":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"result":{"points":[
				{"id":"pt-1","payload":{"asset_id":"a1","name":"n1","source":"youtube","obsolete_key":"stale"}},
				{"id":"pt-2","payload":{"asset_id":"a2","name":"n2","source":"artlist"}},
				{"id":"pt-3","payload":{"asset_id":"a3","name":"n3","source":"stock","obsolete_key":"also-stale"}}
			],"next_page_offset":""}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/collections/test-collection/points/payload":
			var body struct {
				Points []schema.PointPayload `json:"points"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("failed to decode OverwritePayload body: %v", err)
			}
			overwritePayloads = append(overwritePayloads, body.Points...)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"result":{"status":"completed"},"status":"ok"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := transport.NewClient(&schema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	r := NewReaper(client, zap.NewNop())

	result, err := r.Reap(context.Background(), ReaperOptions{
		Collection: "test-collection",
		Keys:       []string{"obsolete_key"},
	})
	require.NoError(t, err)
	assert.Equal(t, 3, result.PointsScanned)
	assert.Equal(t, 2, result.PointsAffected,
		"pt-1 and pt-3 have obsolete_key and should be redacted")
	assert.Equal(t, StatusOK, result.Status,
		"successful delete cycle must return StatusOK")
	require.Len(t, overwritePayloads, 2, "exactly 2 points should be in the overwrite batch")

	// Verify pt-2 (no obsolete_key) is NOT in the overwrite batch.
	for _, pp := range overwritePayloads {
		assert.NotEqual(t, "pt-2", pp.ID, "pt-2 had no obsolete_key, must not be redacted")
		// Verify obsolete_key was stripped from the cleaned payload.
		assert.NotContains(t, pp.Payload, "obsolete_key",
			"redacted payload for %s must NOT contain obsolete_key", pp.ID)
	}
}

// TestReaper_BatchPagination verifies that the Reaper correctly
// handles multi-page scroll results, accumulating affected points
// across pages and applying overwrites per-page.
func TestReaper_BatchPagination(t *testing.T) {
	t.Parallel()

	var pageCalls int
	var overwriteBatches int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/collections/test-collection/points/scroll":
			pageCalls++
			w.Header().Set("Content-Type", "application/json")
			switch pageCalls {
			case 1:
				// Page 1: 2 points, 1 affected.
				_, _ = w.Write([]byte(`{"result":{"points":[
					{"id":"p1-1","payload":{"asset_id":"a1","name":"n1","source":"youtube","legacy":"yes"}},
					{"id":"p1-2","payload":{"asset_id":"a2","name":"n2","source":"artlist"}}
				],"next_page_offset":"page-2"}}`))
			case 2:
				// Page 2: 2 points, 1 affected.
				_, _ = w.Write([]byte(`{"result":{"points":[
					{"id":"p2-1","payload":{"asset_id":"a3","name":"n3","source":"stock"}},
					{"id":"p2-2","payload":{"asset_id":"a4","name":"n4","source":"youtube","legacy":"also-yes"}}
				],"next_page_offset":""}}`))
			default:
				_, _ = w.Write([]byte(`{"result":{"points":[],"next_page_offset":""}}`))
			}
		case r.Method == http.MethodPost && r.URL.Path == "/collections/test-collection/points/payload":
			overwriteBatches++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"result":{"status":"completed"},"status":"ok"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := transport.NewClient(&schema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	r := NewReaper(client, zap.NewNop())

	result, err := r.Reap(context.Background(), ReaperOptions{
		Collection: "test-collection",
		Keys:       []string{"legacy"},
		BatchSize:  2, // small batch to trigger per-page overwrites
	})
	require.NoError(t, err)
	assert.Equal(t, 2, pageCalls, "should have made exactly 2 scroll calls")
	assert.Equal(t, 4, result.PointsScanned, "4 points total across both pages")
	assert.Equal(t, 2, result.PointsAffected,
		"p1-1 (page 1) and p2-2 (page 2) have legacy key → 2 affected")
	assert.Equal(t, StatusOK, result.Status,
		"multi-page successful cycle must return StatusOK")
	assert.Equal(t, 2, overwriteBatches,
		"two pages with affected points → two OverwritePayload calls")
	assert.Len(t, result.AffectedSample, 2,
		"affected sample should include both matching point IDs")
}

// mockSelector always returns true — every point is "affected".
// Used to verify that the custom Selector injection path works
// end-to-end, including the empty-Keys guard not blocking it.
type mockSelector struct{}

func (mockSelector) Filter(_ map[string]interface{}) bool { return true }

// TestReaper_CustomSelector verifies that a custom PointsSelector
// injected via ReaperOptions.Selector is correctly delegated to.
// The mock returns true for every point; only points that actually
// carry the redaction key (from opts.Keys) should be overwritten.
func TestReaper_CustomSelector(t *testing.T) {
	t.Parallel()

	var overwritePayloads []schema.PointPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/collections/test-collection/points/scroll":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"result":{"points":[
				{"id":"pt-a","payload":{"asset_id":"a1","name":"n1","obsolete_key":"yes"}},
				{"id":"pt-b","payload":{"asset_id":"a2","name":"n2"}},
				{"id":"pt-c","payload":{"asset_id":"a3","name":"n3","obsolete_key":"also"}}
			],"next_page_offset":""}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/collections/test-collection/points/payload":
			var body struct {
				Points []schema.PointPayload `json:"points"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("failed to decode OverwritePayload body: %v", err)
			}
			overwritePayloads = append(overwritePayloads, body.Points...)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"result":{"status":"completed"},"status":"ok"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := transport.NewClient(&schema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	r := NewReaper(client, zap.NewNop())

	result, err := r.Reap(context.Background(), ReaperOptions{
		Collection: "test-collection",
		Keys:       []string{"obsolete_key"},
		Selector:   &mockSelector{}, // custom: ALL points selected for filtering
	})
	require.NoError(t, err)
	assert.Equal(t, 3, result.PointsScanned)
	// mockSelector returns true for all 3 points, but only pt-a and pt-c
	// actually carry "obsolete_key" → only those get overwritten.
	assert.Equal(t, 2, result.PointsAffected)
	assert.Equal(t, StatusOK, result.Status)
	require.Len(t, overwritePayloads, 2)
	// pt-b had no obsolete_key → stripped guard should skip it.
	for _, pp := range overwritePayloads {
		assert.NotEqual(t, "pt-b", pp.ID, "pt-b without obsolete_key must not be overwritten")
	}
}
