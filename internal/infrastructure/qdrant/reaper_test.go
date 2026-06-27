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

package qdrant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
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

	client := NewClient(&Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
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

	client := NewClient(&Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
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
