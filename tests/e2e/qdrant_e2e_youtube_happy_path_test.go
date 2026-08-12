package e2e

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/stretchr/testify/require"
)

// ── Subtest 1: happy_path_youtube_clip ──────────────────────────────────
//
// Asserts all 4 godlike/07 obligations:
//
//	#1 media_assets.index_state = INDEXED (SQLite row reflects state)
//	#2 Qdrant scroll finds asset_id (mock upserted[ptID] non-nil +
//	    HTTP /points/scroll endpoint surfaces it)
//	#3 Search returns the result (mock query returns the asset)
//	#4 payload.lifecycle_state = ACTIVE
func TestE2E_Qdrant_HappyPath_YouTubeClip(t *testing.T) {
	fx := newE2EFixture(t, "media_assets_current")

	const assetID = "yt_happy_e2e_001"
	require.NoError(t, commitYouTubeClip(t, fx, assetID,
		"E2E happy path clip",
		"e2e_happy_video_001",
	))
	// The writer doesn't write search_text; the production
	// SearchTextBuilder or a separate WavesOnClip pipeline populates it.
	// For the E2E test we set it explicitly so the search hook in
	// subtest 3 has something deterministic to match on.
	injectMetadataJSON(t, fx, assetID, map[string]any{
		"search_text": "E2E happy path clip — searchable title text",
		"language":    "en",
	})

	runOutboxWorkerClaim(t, fx, assetID, "worker-happy")

	// ── Obligation #1: media_assets.index_state = INDEXED ──
	var idxState string
	require.NoError(t, fx.DB.QueryRow(
		`SELECT index_state FROM media_assets WHERE id = ?`, assetID,
	).Scan(&idxState))
	require.Equal(t, "INDEXED", idxState,
		"godlike/07 #1: media_assets.index_state must be INDEXED after worker claim + CAS fence")

	// ── Obligation #2: Qdrant scroll finds the asset_id ──
	// The mock's upserted map is the single source of truth —
	// canonical pt.ID presence alone proves the upsert landed.
	rawPayload := fx.Qdrant.findUpserted(t, assetID)
	_ = rawPayload

	// Cross-check via the production /points/scroll endpoint
	// (drives the mock's /points/scroll handler which mirrors
	// transport.Client.ScrollPoints' canonical wire shape).
	// Signature: (ctx, collection, offset, limit, filter) per
	// internal/infrastructure/qdrant/transport/client_scroll.go.
	scrollRes, err := fx.Transport.ScrollPoints(
		context.Background(), fx.Schema.RuntimeAlias, "", 100, nil,
	)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(scrollRes.Points), 1,
		"godlike/07 #2: Qdrant scroll must return at least 1 point (the just-upserted asset)")
	found := false
	for _, p := range scrollRes.Points {
		if aid, ok := p.Payload["asset_id"].(string); ok && aid == assetID {
			found = true
			break
		}
	}
	require.True(t, found,
		"godlike/07 #2: Qdrant scroll must contain asset_id=%q in payload", assetID)

	// ── Obligation #3: Search returns the result ──
	searchResults, err := fx.Transport.SearchPoints(
		context.Background(), fx.Schema.RuntimeAlias,
		schema.SearchRequest{
			QueryVector: dummyQueryVector,
			VectorName:  "text",
			Limit:       10,
			MinScore:    0.0,
		},
	)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(searchResults), 1,
		"godlike/07 #3: Search must return at least 1 result for text-vector lookup")
	searchHitFound := false
	for _, r := range searchResults {
		if aid, ok := r.Payload["asset_id"].(string); ok && aid == assetID {
			searchHitFound = true
			break
		}
	}
	require.True(t, searchHitFound,
		"godlike/07 #3: Search must return the asset (asset_id=%q)", assetID)

	// ── Obligation #4: payload.lifecycle_state = ACTIVE ──
	payload := fx.Qdrant.findUpserted(t, assetID)
	var payloadMap map[string]interface{}
	require.NoError(t, json.Unmarshal(payload, &payloadMap))
	require.Equal(t, "ACTIVE", payloadMap["lifecycle_state"],
		"godlike/07 #4: payload.lifecycle_state must be ACTIVE for ACTIVELY written clip")

	// Bonus assertion: the upsert count is exactly 1 (no double-write
	// from any replay or duplicate code path).
	require.Equal(t, 1, fx.Qdrant.upsertsCount,
		"single CommitClipAndIndexEvent + single UpsertFromClip → exactly 1 Qdrant write per asset")
}
