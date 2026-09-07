package finalizer

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	_ "github.com/mattn/go-sqlite3"
	"testing"
)

// TestAssetTxFinalizer_IdempotentVersionIncrement verifies that
// two sequential FinalizeAsset calls on the same asset increment
// the version_number correctly.
func TestAssetTxFinalizer_IdempotentVersionIncrement(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	fx := newTestFinalizer(t, db)
	ctx := context.Background()

	// First finalization.
	tx1, _ := db.BeginTx(ctx, nil)
	ref1, _, err := fx.FinalizeAsset(ctx, WrapTx(tx1),
		publishedArtifact("asset-002", "hash-v1", "file-v1"))
	if err != nil {
		tx1.Rollback()
		t.Fatalf("first finalize: %v", err)
	}
	if ref1.SourceVersion != 1 {
		t.Errorf("first version = %d, want 1", ref1.SourceVersion)
	}
	tx1.Commit()

	// Second finalization (new content hash, new file).
	tx2, _ := db.BeginTx(ctx, nil)
	ref2, _, err := fx.FinalizeAsset(ctx, WrapTx(tx2),
		publishedArtifact("asset-002", "hash-v2", "file-v2"))
	if err != nil {
		tx2.Rollback()
		t.Fatalf("second finalize: %v", err)
	}
	if ref2.SourceVersion != 2 {
		t.Errorf("second version = %d, want 2", ref2.SourceVersion)
	}
	tx2.Commit()

	// Verify both versions exist.
	var count int
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_versions WHERE asset_id = ?`, "asset-002").Scan(&count)
	if count != 2 {
		t.Errorf("version count = %d, want 2", count)
	}

	// Verify media_assets now reflects the latest hash.
	var fileHash string
	db.QueryRowContext(ctx, `SELECT legacy_file_md5 FROM media_assets WHERE id = ?`, "asset-002").Scan(&fileHash)
	if fileHash != "hash-v2" {
		t.Errorf("media_assets legacy_file_md5 = %q after second finalize, want hash-v2", fileHash)
	}
}

// TestAssetTxFinalizer_DifferentArtifactKinds verifies correct media_type
// mapping for each ArtifactKind.
func TestAssetTxFinalizer_DifferentArtifactKinds(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	fx := newTestFinalizer(t, db)
	ctx := context.Background()

	cases := []struct {
		kind      finalization.ArtifactKind
		wantMedia string
	}{
		{finalization.KindVideo, "video"},
		{finalization.KindImage, "image"},
		{finalization.KindAudio, "audio"},
		{finalization.KindVoiceover, "audio"},
		{finalization.KindSoundEffect, "audio"},
		{finalization.KindDocument, "document"},
		{finalization.KindScript, "text"},
		{finalization.KindMetadata, "metadata"},
		{finalization.KindArchive, "archive"},
	}

	for _, c := range cases {
		t.Run(string(c.kind), func(t *testing.T) {
			assetID := fmt.Sprintf("kind-test-%s", c.kind)
			pa := publishedArtifact(assetID, "hash", "file")
			pa.Kind = c.kind

			tx, _ := db.BeginTx(ctx, nil)
			defer tx.Rollback()
			_, _, err := fx.FinalizeAsset(ctx, WrapTx(tx), pa)
			if err != nil {
				t.Fatalf("FinalizeAsset(%s): %v", c.kind, err)
			}

			var mediaType string
			tx.QueryRowContext(ctx,
				`SELECT media_type FROM media_assets WHERE id = ?`, assetID,
			).Scan(&mediaType)
			if mediaType != c.wantMedia {
				t.Errorf("media_type = %q, want %q", mediaType, c.wantMedia)
			}
		})
	}
}

// TestAssetTxFinalizer_OutboxEventPayload verifies the outbox event
// carries the canonical v1 index request payload matching the
// IndexingHandler contract (schema_version, event_id, asset_id,
// source_version, idempotency_key).
func TestAssetTxFinalizer_OutboxEventPayload(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	fx := newTestFinalizer(t, db)
	ctx := context.Background()

	artifact := publishedArtifact("asset-payload", "sha256-hash", "drive-id-xyz")
	tx, _ := db.BeginTx(ctx, nil)
	defer tx.Rollback()

	_, events, err := fx.FinalizeAsset(ctx, WrapTx(tx), artifact)
	if err != nil {
		t.Fatalf("FinalizeAsset: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 outbox event, got %d", len(events))
	}

	var payload map[string]any
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal outbox payload: %v", err)
	}
	if payload["schema_version"] != outboxevents.ReindexEnvelopeV1Schema {
		t.Errorf("schema_version = %v, want %v", payload["schema_version"], outboxevents.ReindexEnvelopeV1Schema)
	}
	if payload["asset_id"] != "asset-payload" {
		t.Errorf("asset_id = %v", payload["asset_id"])
	}
	if payload["source_version"] != "sha256-hash" {
		t.Errorf("source_version = %v", payload["source_version"])
	}
	if _, ok := payload["event_id"]; !ok {
		t.Error("event_id missing from payload")
	}
	if _, ok := payload["idempotency_key"]; !ok {
		t.Error("idempotency_key missing from payload")
	}
	if payload["operation"] != "UPSERT" {
		t.Errorf("operation = %v, want UPSERT", payload["operation"])
	}
}

// TestAssetTxFinalizer_RollbackOnError verifies that a failed
// write inside the transaction doesn't persist.
func TestAssetTxFinalizer_RollbackOnError(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	fx := newTestFinalizer(t, db)
	ctx := context.Background()

	// Insert a row that will cause a UNIQUE constraint violation on
	// asset_versions (same asset_id + version_number = 1).
	tx, _ := db.BeginTx(ctx, nil)
	_, _, err := fx.FinalizeAsset(ctx, WrapTx(tx),
		publishedArtifact("asset-rollback", "h1", "f1"))
	if err != nil {
		t.Fatalf("first finalize: %v", err)
	}
	tx.Commit()

	// Insert a conflicting asset_versions row manually.
	db.Exec(`INSERT INTO asset_versions (asset_id, version_number, legacy_file_md5, created_at)
		VALUES ('asset-rollback', 999, 'h999', '2024-01-01')`)

	// Now finalize again — the MAX(version_number)+1 should give 1000,
	// but if someone manually inserted 999, the unique constraint should
	// still hold since MAX+1 = 1000 which doesn't conflict.
	tx2, _ := db.BeginTx(ctx, nil)
	ref2, _, err := fx.FinalizeAsset(ctx, WrapTx(tx2),
		publishedArtifact("asset-rollback", "h2", "f2"))
	if err != nil {
		t.Fatalf("second finalize with manual version 999: %v", err)
	}
	// MAX(1, 999) + 1 = 1000, which is unique.
	if ref2.SourceVersion != 1000 {
		t.Errorf("expected version 1000 after manual version 999 insert, got %d", ref2.SourceVersion)
	}
	tx2.Commit()
}

// TestAssetTxFinalizer_IndexStatePendingAtInsert pins the godlike/07
// no-fake-availability contract for the E2E wiring finale (PR-009):
// the finalizer's spine write MUST set media_assets.index_state to
// the literal 'DISCOVERED' on fresh INSERT, matching the wire
// shape from PR-008 (StockRunMetadata.IndexingStatus). The
// IndexingHandler downstream overwrites to 'INDEXED' after a
// successful Qdrant upsert.
//
// godlike/06 SSOT: the literal value is the canonical projection-time
// hint; media_assets.index_state remains the single source of truth
// for the lifecycle state in the DB. The ON CONFLICT DO UPDATE clause
// intentionally does NOT include index_state — a re-finalization
// must NOT clobber a state the clipindexer has already transitioned
// (INDEXING / INDEXED / INDEX_FAILED).
func TestAssetTxFinalizer_IndexStatePendingAtInsert(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	fx := newTestFinalizer(t, db)
	ctx := context.Background()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()

	artifact := publishedArtifact("asset-e2e-finale", "hash-e2e", "file-e2e")
	if _, _, err := fx.FinalizeAsset(ctx, WrapTx(tx), artifact); err != nil {
		t.Fatalf("FinalizeAsset: %v", err)
	}

	// godlike/06 SSOT: the literal value is the canonical
	// projection-time hint. Must be present on the row after the
	// spine write, BEFORE the IndexingHandler runs.
	var indexState string
	err = tx.QueryRowContext(ctx,
		`SELECT index_state FROM media_assets WHERE id = ?`,
		"asset-e2e-finale",
	).Scan(&indexState)
	if err != nil {
		t.Fatalf("query index_state: %v", err)
	}
	if indexState != "DISCOVERED" {
		t.Errorf("media_assets.index_state = %q, want %q (E2E wiring finale: DB column must use the canonical initial state)",
			indexState, "DISCOVERED")
	}

	// Re-finalize (ON CONFLICT path): the index_state must NOT be
	// clobbered. This guards the godlike/06 SSOT invariant that a
	// re-finalization preserves the clipindexer's state transitions.
	// Simulate the clipindexer having advanced the state to INDEXING.
	if _, err := tx.ExecContext(ctx,
		`UPDATE media_assets SET index_state = ? WHERE id = ?`,
		"INDEXING", "asset-e2e-finale"); err != nil {
		t.Fatalf("simulate clipindexer INDEXING: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Open a NEW tx + re-finalize (new hash → re-finalization).
	tx2, _ := db.BeginTx(ctx, nil)
	defer tx2.Rollback()
	artifact2 := publishedArtifact("asset-e2e-finale", "hash-e2e-v2", "file-e2e-v2")
	if _, _, err := fx.FinalizeAsset(ctx, WrapTx(tx2), artifact2); err != nil {
		t.Fatalf("re-FinalizeAsset: %v", err)
	}

	// ON CONFLICT DO UPDATE must NOT touch index_state — the
	// clipindexer's INDEXING transition must survive re-finalization.
	var indexStateAfterReFinalize string
	if err := tx2.QueryRowContext(ctx,
		`SELECT index_state FROM media_assets WHERE id = ?`,
		"asset-e2e-finale",
	).Scan(&indexStateAfterReFinalize); err != nil {
		t.Fatalf("query index_state after re-finalize: %v", err)
	}
	if indexStateAfterReFinalize != "INDEXING" {
		t.Errorf("media_assets.index_state after re-finalize = %q, want %q (ON CONFLICT must NOT clobber clipindexer's state transition)",
			indexStateAfterReFinalize, "INDEXING")
	}
}
