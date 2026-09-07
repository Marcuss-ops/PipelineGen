package jobs

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	assets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	_ "github.com/mattn/go-sqlite3"
	"testing"
	"time"
)

// runDurableIndexingStaleTier2Tail drives the final Stage 6 stale-Tier-2
// regression guard of TestDurableIndexing_FinalizerWrites_HandlerDoesNotSupersede.
// Extracted so the umbrella test stays under the per-file LOC budget.
func runDurableIndexingStaleTier2Tail(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	repo *outboxevents.Repository,
	fx *finalizer.AssetTxFinalizer,
	handler *IndexingHandler,
	clipper *fakeIndexClipper,
	assetID string,
	artifact1 finalization.PublishedArtifact,
	claim2 *outboxevents.Claim,
) {
	// ── Stage 6: stale-Tier-2 regression guard ──
	// This simulates the ACTUAL production bug: a different pipeline
	// (YouTube) wrote metadata_json.file_hash = "stale_old_hash" in a
	// previous ingest. After the finalizer republishes, ON CONFLICT
	// replaces metadata_json — but without content_hash, Tier 2
	// (stale file_hash) would have beaten Tier 3 (fresh column).
	//
	// The fix ensures Tier 1 (content_hash) wins because the finalizer
	// writes it on every republish. This stage verifies the fix
	// survives even when metadata_json previously contained a stale
	// file_hash from a different pipeline.
	staleHash := "sha256:stale_from_youtube_pipeline"
	hash3 := "sha256:integration_hash_v3"

	// Simulate: YouTube pipeline wrote stale file_hash to metadata_json.
	if _, err := db.Exec(`UPDATE media_assets SET metadata_json = ? WHERE id = ?`,
		`{"file_hash":"`+staleHash+`","publish_action":"drive"}`, assetID); err != nil {
		t.Fatalf("simulate stale Tier 2: %v", err)
	}

	// Verify: before the fix, SourceVersionFor would read stale Tier 2.
	// After the fix, the next FinalizeAsset overwrites metadata_json
	// with content_hash.
	svStale, err := assets.SourceVersionFor(ctx, db, assetID)
	if err != nil {
		t.Fatalf("SourceVersionFor (stale): %v", err)
	}
	if svStale != staleHash {
		t.Fatalf("pre-condition failed: SourceVersionFor = %q, want stale %q (stale Tier 2 simulation didn't work)",
			svStale, staleHash)
	}

	// Now republish with hash3 — the finalizer writes content_hash=hash3
	// to metadata_json, overwriting the stale file_hash.
	artifact3 := artifact1
	artifact3.SHA256 = hash3
	artifact3.IdempotencyKey = "idem-" + assetID + "-v3"

	tx3, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx3: %v", err)
	}
	_, events3, err := fx.FinalizeAsset(ctx, finalizer.WrapTx(tx3), artifact3)
	if err != nil {
		tx3.Rollback()
		t.Fatalf("FinalizeAsset (v3): %v", err)
	}
	if err := tx3.Commit(); err != nil {
		t.Fatalf("commit tx3: %v", err)
	}

	// Verify: SourceVersionFor now returns hash3 (Tier 1 content_hash),
	// NOT staleHash (the stale Tier 2 file_hash that was overwritten).
	sv3, err := assets.SourceVersionFor(ctx, db, assetID)
	if err != nil {
		t.Fatalf("SourceVersionFor (v3): %v", err)
	}
	if sv3 != hash3 {
		t.Errorf("SourceVersionFor after republish with stale Tier 2 = %q, want %q (content_hash must overwrite stale file_hash)",
			sv3, hash3)
	}

	// Insert v3 outbox event and verify handler does NOT supersede.
	if len(events3) != 1 {
		t.Fatalf("expected 1 outbox event from v3, got %d", len(events3))
	}
	_, err = db.Exec(`
		INSERT INTO outbox_events
		    (event_type, aggregate_id, aggregate_type, payload_json, event_key, status)
		VALUES (?, ?, '', ?, ?, 'pending')
		ON CONFLICT(event_key) WHERE event_key != '' DO NOTHING
	`, events3[0].EventType, events3[0].AggregateID, string(events3[0].Payload), events3[0].EventKey)
	if err != nil {
		t.Fatalf("insert outbox event (v3): %v", err)
	}
	if err := repo.MarkCompleted(ctx, claim2.Event.ID, claim2.LeaseID); err != nil {
		t.Fatalf("MarkCompleted (v2): %v", err)
	}

	claim3, err := repo.ClaimNext(ctx, "worker-integration-3", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNext (v3): %v", err)
	}
	if claim3 == nil {
		t.Fatal("expected a claim for v3 republish event")
	}

	// THE STALE-TIER-2 REGRESSION GUARD: before the fix, SourceVersionFor
	// would read staleHash from Tier 2 (stale file_hash), compare with
	// hash3 from the event, and mark as superseded. After the fix,
	// Tier 1 (content_hash=hash3) matches the event — no supersede.
	err = handler.Handle(ctx, claim3.Event)
	if err != nil {
		var supersede *outboxevents.SupersedeError
		if errors.As(err, &supersede) {
			t.Fatalf("STALE-TIER-2 SupersedeError — content_hash fix is BROKEN! (current=%q, expected=%q). \n"+
				"Before the fix: Tier 2 (stale file_hash=%q) beat empty Tier 1. \n"+
				"After the fix: Tier 1 (content_hash=%q) wins.",
				supersede.Current, supersede.Expected, staleHash, hash3)
		}
		t.Fatalf("handler.Handle (v3): %v", err)
	}
	if got := clipper.CallCount(assetID); got != 3 {
		t.Errorf("IndexClip(%s) called %d times after stale-Tier-2 scenario, want 3 (v1 + v2 + v3)", assetID, got)
	}
}
