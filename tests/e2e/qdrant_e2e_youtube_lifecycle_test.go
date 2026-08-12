package e2e

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/stretchr/testify/require"
)

// ── Subtest 5: lifecycle_active_filter ──────────────────────────────────
//
// Search with lifecycle_state=ACTIVE filter returns ACTIVE assets;
// soft-deleting an asset to lifecycle_state=DELETED hides it from
// subsequent filtered searches — the canonical
// "lifecycle is a payload filter" contract (per
// architecture/current.yaml#FASE-7-1 / FASE-7-2 lifecycle_state contract).
//
// Production-shape change: after the SQLite UPDATE flip (DELETED), the
// test CALLS IndexWriter.UpsertFromClip so the Qdrant payload receives
// the new lifecycle_state via the canonical wire. This avoids any
// mock-mutation hand-rolled route that wouldn't exist in production.
func TestE2E_Qdrant_LifecycleActiveFilter(t *testing.T) {
	fx := newE2EFixture(t, "media_assets_current")

	const activeAssetID = "yt_lc_active_005_active"
	const deletedAssetID = "yt_lc_active_005_deleted"

	require.NoError(t, commitYouTubeClip(t, fx, activeAssetID,
		"E2E lifecycle-active asset (must surface in ACTIVE filter)",
		"e2e_lc_active_video_005",
	))
	require.NoError(t, commitYouTubeClip(t, fx, deletedAssetID,
		"E2E lifecycle-asset-to-delete (must NOT surface in ACTIVE filter)",
		"e2e_lc_deleted_video_005",
	))
	injectMetadataJSON(t, fx, activeAssetID, map[string]any{"search_text": "active asset"})
	injectMetadataJSON(t, fx, deletedAssetID, map[string]any{"search_text": "deleted asset — will be soft-deleted"})

	runOutboxWorkerClaim(t, fx, activeAssetID, "worker-lc-1")
	runOutboxWorkerClaim(t, fx, deletedAssetID, "worker-lc-2")

	// Soft-delete: lifecycle_state=DELETED. Production:
	// LifecycleService.SoftDelete + IndexWriter re-upsert to push the
	// new lifecycle_state to Qdrant. The E2E test mirrors both.
	_, err := fx.DB.Exec(
		`UPDATE media_assets SET lifecycle_state = 'DELETED' WHERE id = ?`, deletedAssetID,
	)
	require.NoError(t, err)

	// Production-shape re-upsert: drive the canonical
	// IndexWriter.UpsertFromClip path so the Qdrant mock's upserted
	// map receives the new lifecycle_state. The writer's mapper reads
	// the fresh lifecycle_state from media_assets and emits the
	// updated payload via Production wire shape.
	if err := fx.Writer.UpsertFromClip(context.Background(), deletedAssetID); err != nil {
		t.Fatalf("UpsertFromClip (%q, re-upsert after soft-delete): %v", deletedAssetID, err)
	}

	// Sanity: the mock's upserted map now reflects lifecycle_state=DELETED.
	payloadRaw := fx.Qdrant.findUpserted(t, deletedAssetID)
	var payloadMap map[string]interface{}
	require.NoError(t, json.Unmarshal(payloadRaw, &payloadMap))
	require.Equal(t, "DELETED", payloadMap["lifecycle_state"],
		"production-shape re-upsert: Qdrant payload must surface lifecycle_state=DELETED")

	// ── Override queryHook to apply a payload filter (mimicking
	//    Qdrant native Filter expression: lifecycle_state=ACTIVE). ──
	fx.Qdrant.queryHook = func(_ []byte, points []schema.Point) []schema.SearchResult {
		var out []schema.SearchResult
		for _, p := range points {
			if ls, ok := p.Payload["lifecycle_state"].(string); ok && ls == "ACTIVE" {
				out = append(out, schema.SearchResult{
					ID:      p.ID,
					Score:   1.0,
					Payload: p.Payload,
				})
			}
		}
		return out
	}

	results, err := fx.Transport.SearchPoints(
		context.Background(), fx.Schema.RuntimeAlias,
		schema.SearchRequest{
			QueryVector: dummyQueryVector,
			VectorName:  "text",
			Limit:       50,
			// Production-shaped lifecycle filter — passed through to
			// Qdrant wire "filter" key. The mock honours the filter
			// via its lifecycle_state-aware queryHook above.
			Filter: map[string]interface{}{
				"must": []map[string]interface{}{
					{"key": "lifecycle_state", "match": map[string]interface{}{"value": "ACTIVE"}},
				},
			},
		},
	)
	require.NoError(t, err)

	// The active asset MUST appear; the deleted asset MUST NOT.
	var activeHit, deletedHit bool
	for _, r := range results {
		switch aid, ok := r.Payload["asset_id"].(string); {
		case ok && aid == activeAssetID:
			activeHit = true
		case ok && aid == deletedAssetID:
			deletedHit = true
		}
	}
	require.True(t, activeHit,
		"godlike/07 #3 + #4: lifecycle_state=ACTIVE filter MUST return the active asset (asset_id=%q)",
		activeAssetID)
	require.False(t, deletedHit,
		"godlike/07 #3 + #4: lifecycle_state=ACTIVE filter MUST HIDE the soft-deleted asset (asset_id=%q, lifecycle_state=DELETED)",
		deletedAssetID)

	// Sanity: the SQLite row reflects the soft-delete (godlike/07 SSOT).
	var stored string
	require.NoError(t, fx.DB.QueryRow(
		`SELECT lifecycle_state FROM media_assets WHERE id = ?`, deletedAssetID,
	).Scan(&stored))
	require.Equal(t, "DELETED", stored,
		"lifecycle_state must be DELETED on the SQLite row (godlike/07 source of truth)")

	// ── Verify Qdrant scroll surfaces both points (no lifecycle filter). ──
	allResults, err := fx.Transport.ScrollPoints(
		context.Background(), fx.Schema.RuntimeAlias, "", 50, nil,
	)
	require.NoError(t, err)
	deletedStillInQdrant := false
	for _, p := range allResults.Points {
		if aid, ok := p.Payload["asset_id"].(string); ok && aid == deletedAssetID {
			deletedStillInQdrant = true
			require.Equal(t, "DELETED", p.Payload["lifecycle_state"],
				"the DELETED asset remains in Qdrant (kept for audit) — only filtered out at search time")
		}
	}
	require.True(t, deletedStillInQdrant,
		"godlike/07 #2 spec: Qdrant scroll must surface ALL points regardless of lifecycle — DELETE is a filter, not a tombstone")
}
