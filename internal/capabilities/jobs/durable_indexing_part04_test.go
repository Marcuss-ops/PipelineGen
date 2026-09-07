package jobs

import (
	"context"
	"errors"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	assets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry"
	testsupport "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry/testsupport"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
	"testing"
	"time"
)

// TestDurableIndexing_FinalizerWrites_HandlerDoesNotSupersede is the
// canonical integration-level test that closes the loop on the
// content_hash supersede-gate fix (asset_finalizer_tx.go PR, July 2026).
//
// It simulates the PRODUCTION flow end-to-end:
//
//  1. FinalizeAsset writes media_assets row with content_hash in
//     metadata_json (same tx as outbox event creation).
//  2. The outbox event's source_version = artifact.SHA256.
//  3. IndexingHandler reads SourceVersionFor() from the SAME DB
//     (real 3-tier COALESCE: content_hash → file_hash → column).
//  4. Supersede gate MUST NOT fire — content_hash (Tier 1) matches
//     the event's source_version (same write boundary).
//  5. IndexClip MUST be invoked.
//
// Stages 1–5 are happy-path integration coverage: they verify that
// FinalizeAsset writes content_hash to metadata_json and that
// SourceVersionFor reads it correctly through the supersede gate.
//
// Stage 6 is the ACTUAL regression guard: it simulates a different
// pipeline (YouTube) having written a stale file_hash to metadata_json
// in a previous ingest. Without the content_hash fix, SourceVersionFor
// would read the stale Tier 2 (file_hash) and the supersede gate would
// fire — Qdrant never updates. With the fix, Tier 1 (content_hash)
// wins because the finalizer writes it on every republish.
func TestDurableIndexing_FinalizerWrites_HandlerDoesNotSupersede(t *testing.T) {
	db := openInMemDB_Integration(t)
	repo := outboxevents.NewRepository(db)

	fx := finalizer.NewAssetTxFinalizer(zap.NewNop(), testsupport.NewSQLiteAssetCommitter(db, outboxevents.NewRepository(db), nil))
	ctx := context.Background()
	assetID := "yt_integration_001"

	// ── Stage 1: FinalizeAsset writes media_assets + returns outbox event ──
	hash1 := "sha256:integration_hash_v1"
	artifact1 := finalization.PublishedArtifact{
		ArtifactID:     assetID,
		Kind:           finalization.KindVideo,
		Filename:       "test-video.mp4",
		MIMEType:       "video/mp4",
		SizeBytes:      1024,
		SHA256:         hash1,
		Requirement:    finalization.ArtifactRequirementRequired,
		IdempotencyKey: "idem-" + assetID,
		Location: finalization.AssetLocation{
			Provider:     "drive",
			FileID:       "drive-file-integration",
			WebViewLink:  "https://drive.google.com/file/d/drive-file-integration/view",
			DownloadLink: "https://drive.google.com/uc?id=drive-file-integration",
			FolderID:     "folder-abc",
			FolderPath:   "/test",
			Action:       finalization.PublishCreated,
		},
	}

	tx1, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx1: %v", err)
	}
	ref1, events1, err := fx.FinalizeAsset(ctx, finalizer.WrapTx(tx1), artifact1)
	if err != nil {
		tx1.Rollback()
		t.Fatalf("FinalizeAsset (v1): %v", err)
	}
	if err := tx1.Commit(); err != nil {
		t.Fatalf("commit tx1: %v", err)
	}
	if ref1.ContentHash != hash1 {
		t.Errorf("ref1.ContentHash = %q, want %q", ref1.ContentHash, hash1)
	}

	// Insert the returned outbox event into the outbox_events table
	// (mirrors production: the JobFinalizer inserts events after commit).
	if len(events1) != 1 {
		t.Fatalf("expected 1 outbox event from finalizer, got %d", len(events1))
	}
	eventKey1 := events1[0].EventKey
	_, err = db.Exec(`
		INSERT INTO outbox_events
		    (event_type, aggregate_id, aggregate_type, payload_json, event_key, status)
		VALUES (?, ?, '', ?, ?, 'pending')
		ON CONFLICT(event_key) WHERE event_key != '' DO NOTHING
	`, events1[0].EventType, events1[0].AggregateID, string(events1[0].Payload), eventKey1)
	if err != nil {
		t.Fatalf("insert outbox event (v1): %v", err)
	}

	// ── Stage 2: wire IndexingHandler with REAL SourceVersionFor ──
	clipper := newFakeIndexClipper()
	srcQuerier := &dbSourceQuerier{db: db}
	handler := NewIndexingHandler(clipper, srcQuerier, zap.NewNop())

	// ── Stage 3: claim + handle — MUST NOT supersede ──
	claim1, err := repo.ClaimNext(ctx, "worker-integration-1", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNext (v1): %v", err)
	}
	if claim1 == nil {
		t.Fatal("expected a claim (1 pending event)")
	}

	err = handler.Handle(ctx, claim1.Event)
	if err != nil {
		var supersede *outboxevents.SupersedeError
		if errors.As(err, &supersede) {
			t.Fatalf("handler.Handle returned SupersedeError — the content_hash fix is BROKEN! (current=%q, expected=%q)",
				supersede.Current, supersede.Expected)
		}
		t.Fatalf("handler.Handle: %v", err)
	}
	if got := clipper.CallCount(assetID); got != 1 {
		t.Errorf("IndexClip(%s) called %d times after FinalizeAsset, want 1", assetID, got)
	}

	// ── Stage 4: REPUBLISH scenario (the original bug) ──
	// Finalize with a NEW hash — this is the exact scenario where the
	// supersede gate would fire if content_hash was missing from
	// metadata_json (stale Tier 2 would beat empty Tier 1).
	hash2 := "sha256:integration_hash_v2"
	artifact2 := artifact1
	artifact2.SHA256 = hash2
	artifact2.IdempotencyKey = "idem-" + assetID + "-v2"

	tx2, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx2: %v", err)
	}
	ref2, events2, err := fx.FinalizeAsset(ctx, finalizer.WrapTx(tx2), artifact2)
	if err != nil {
		tx2.Rollback()
		t.Fatalf("FinalizeAsset (v2): %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("commit tx2: %v", err)
	}
	if ref2.ContentHash != hash2 {
		t.Errorf("ref2.ContentHash = %q, want %q", ref2.ContentHash, hash2)
	}

	// Insert the republish outbox event.
	if len(events2) != 1 {
		t.Fatalf("expected 1 outbox event from republish, got %d", len(events2))
	}
	_, err = db.Exec(`
		INSERT INTO outbox_events
		    (event_type, aggregate_id, aggregate_type, payload_json, event_key, status)
		VALUES (?, ?, '', ?, ?, 'pending')
		ON CONFLICT(event_key) WHERE event_key != '' DO NOTHING
	`, events2[0].EventType, events2[0].AggregateID, string(events2[0].Payload), events2[0].EventKey)
	if err != nil {
		t.Fatalf("insert outbox event (v2): %v", err)
	}

	// Mark the v1 event as completed so ClaimNext picks up the v2 event.
	if err := repo.MarkCompleted(ctx, claim1.Event.ID, claim1.LeaseID); err != nil {
		t.Fatalf("MarkCompleted (v1): %v", err)
	}

	claim2, err := repo.ClaimNext(ctx, "worker-integration-2", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNext (v2): %v", err)
	}
	if claim2 == nil {
		t.Fatal("expected a claim for republish event")
	}

	// THE CRITICAL ASSERTION: the republish event must NOT be superseded.
	// Before the content_hash fix, metadata_json was missing content_hash,
	// so SourceVersionFor read stale Tier 2 (old file_hash from v1 ingest)
	// and the supersede gate fired — Qdrant never got updated.
	err = handler.Handle(ctx, claim2.Event)
	if err != nil {
		var supersede *outboxevents.SupersedeError
		if errors.As(err, &supersede) {
			t.Fatalf("RE-PUBLISH SupersedeError — content_hash fix is BROKEN! (current=%q, expected=%q). \n"+
				"SourceVersionFor should read Tier 1 (content_hash=new-hash) from metadata_json, \n"+
				"NOT stale Tier 2 (file_hash=old-hash) from previous ingest.",
				supersede.Current, supersede.Expected)
		}
		t.Fatalf("handler.Handle (v2): %v", err)
	}
	// IndexClip fired for the republish — Qdrant gets updated.
	if got := clipper.CallCount(assetID); got != 2 {
		t.Errorf("IndexClip(%s) called %d times after republish, want 2 (v1 + v2)", assetID, got)
	}

	// ── Stage 5: verify SourceVersionFor reads Tier 1 (content_hash) ──
	// After the republish, SourceVersionFor must return hash2 (the new
	// content_hash), NOT hash1 (the stale file_hash from v1).
	sv, err := assets.SourceVersionFor(ctx, db, assetID)
	if err != nil {
		t.Fatalf("SourceVersionFor: %v", err)
	}
	if sv != hash2 {
		t.Errorf("SourceVersionFor(%s) = %q, want %q (Tier 1 content_hash must win after republish)",
			assetID, sv, hash2)
	}

	runDurableIndexingStaleTier2Tail(t, ctx, db, repo, fx, handler, clipper, assetID, artifact1, claim2)
}
