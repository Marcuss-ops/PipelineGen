package finalizer

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// TestIndexState_INDEXED_OnlyViaOutboxConsumer pins the godlike/06
// SSOT contract (forward-prevention pair to
// percheck_indexed_state_writer_ssot in cmd/archcheck/scan/...):
// media_assets.index_state=INDEXED transitions ONLY via the
// canonical outbox consumer (IndexingHandler → clipindexer.IndexClip
// → setIndexedAt). CommitAsset (FinalizeAsset) MUST leave the row
// in a non-INDEXED state — specifically, the canonical projection-
// time hint 'INDEXING_PENDING' that the finalizer's spine write
// sets.
//
// Per the user directive (Italian, July 2026):
// "Aggiungere test che verifichi che senza consumo outbox lo stato
// non passa a INDEXED anche se CommitAsset è avvenuto."
//
// This test is the load-bearing forward-prevention assertion:
// without outbox consumption (i.e., without running IndexingHandler
// on the emitted event), index_state MUST remain != INDEXED even
// though CommitAsset has successfully completed.
//
// Companion test: TestAssetTxFinalizer_IndexStatePendingAtInsert
// (in asset_finalizer_tx_test.go) pins the same contract for the
// specific value INDEXING_PENDING. This test is the explicit SSOT
// version with the literal "INDEXED" assertion + outbox-event
// count assertion.
func TestIndexState_INDEXED_OnlyViaOutboxConsumer(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	fx := newTestFinalizer(t, db)
	ctx := context.Background()

	// Step 1: CommitAsset (FinalizeAsset in the same tx as the
	// outbox event). The tx commits the asset row + the outbox
	// event atomically. After commit, the IndexingHandler
	// downstream WILL consume the event and transition to
	// INDEXED — but in this test, we DO NOT run the handler.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()

	artifact := publishedArtifact("asset-indexed-only-via-outbox", "hash-ssot-1", "file-ssot-1")
	_, events, err := fx.FinalizeAsset(ctx, WrapTx(tx), artifact)
	if err != nil {
		t.Fatalf("FinalizeAsset: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 outbox event from FinalizeAsset; got %d", len(events))
	}
	if events[0].EventType != outboxevents.EventAssetIndexRequested {
		t.Errorf("event_type = %q, want %q",
			events[0].EventType, outboxevents.EventAssetIndexRequested)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Step 2: Assert media_assets.index_state != INDEXED.
	// godlike/06 SSOT: INDEXED is exclusively outbox-consumer-
	// driven. Without running the IndexingHandler, the state
	// must remain in the canonical projection-time hint
	// 'DISCOVERED' (set by the finalizer's spine write).
	var indexState string
	err = db.QueryRowContext(ctx,
		`SELECT index_state FROM media_assets WHERE id = ?`,
		"asset-indexed-only-via-outbox",
	).Scan(&indexState)
	if err != nil {
		t.Fatalf("query index_state: %v", err)
	}
	if indexState == "INDEXED" {
		t.Fatalf("media_assets.index_state = %q after CommitAsset WITHOUT outbox consumption; want != INDEXED (godlike/06 SSOT: INDEXED is exclusively outbox-consumer-driven via setIndexedAt)",
			indexState)
	}
	if indexState != "DISCOVERED" {
		t.Errorf("media_assets.index_state = %q after CommitAsset; want DISCOVERED (canonical initial state)",
			indexState)
	}

	// Step 3: Assert the outbox event IS enqueued (the
	// canonical asset.index.requested event that the
	// IndexingHandler will consume to transition to INDEXED).
	var eventCount int
	err = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM outbox_events WHERE event_type = ? AND aggregate_id = ?`,
		outboxevents.EventAssetIndexRequested, "asset-indexed-only-via-outbox",
	).Scan(&eventCount)
	if err != nil {
		t.Fatalf("count outbox events: %v", err)
	}
	if eventCount != 1 {
		t.Errorf("expected 1 asset.index.requested outbox event after CommitAsset; got %d",
			eventCount)
	}
}
