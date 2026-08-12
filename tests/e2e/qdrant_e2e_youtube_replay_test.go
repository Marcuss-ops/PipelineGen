package e2e

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ── Subtest 2: replay_is_no_op ──────────────────────────────────────────
//
// Re-enqueueing the same asset with the same source_version must NOT
// produce a second outbox row (ON CONFLICT DO NOTHING contract) AND
// must NOT produce a second Qdrant upsert (the worker skips because no
// new event is claimable).
func TestE2E_Qdrant_ReplayIsNoOp(t *testing.T) {
	fx := newE2EFixture(t, "media_assets_current")

	const assetID = "yt_replay_e2e_002"
	require.NoError(t, commitYouTubeClip(t, fx, assetID,
		"E2E replay clip — first enqueue",
		"e2e_replay_video_002",
	))
	injectMetadataJSON(t, fx, assetID, map[string]any{"search_text": "first"})

	runOutboxWorkerClaim(t, fx, assetID, "worker-replay-1")

	// ── Re-enqueue (same asset_id + same file_hash → same source_version). ──
	require.NoError(t, commitYouTubeClip(t, fx, assetID,
		"E2E replay clip — second enqueue (same hash)",
		"e2e_replay_video_002",
	))

	// No new outbox events should appear.
	var n int
	require.NoError(t, fx.DB.QueryRow(
		`SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = ?`, assetID,
	).Scan(&n))
	require.Equal(t, 1, n,
		"godlike/07 idempotency: replay with same source_version must NOT produce a second outbox row")

	// ── Replay worker claim — should find nothing because the only event is "completed". ──
	ctx := context.Background()
	claim, err := fx.Events.ClaimNext(ctx, "worker-replay-2", 30*time.Second)
	require.NoError(t, err)
	require.Nil(t, claim,
		"godlike/07 #2 replay: ClaimNext must return nil when no pending events exist")

	// ── Qdrant upsert count remains 1 (replay did not double-write). ──
	require.Equal(t, 1, fx.Qdrant.upsertsCount,
		"godlike/07 #2: replay must NOT produce a second Qdrant upsert")

	// Obligations #1, #4 still satisfied post-replay.
	var idxState string
	require.NoError(t, fx.DB.QueryRow(
		`SELECT index_state FROM media_assets WHERE id = ?`, assetID,
	).Scan(&idxState))
	require.Equal(t, "INDEXED", idxState, "godlike/07 #1: index_state must remain INDEXED after replay")

	payload := fx.Qdrant.findUpserted(t, assetID)
	var payloadMap map[string]interface{}
	require.NoError(t, json.Unmarshal(payload, &payloadMap))
	require.Equal(t, "ACTIVE", payloadMap["lifecycle_state"], "godlike/07 #4: lifecycle_state must remain ACTIVE")
}
