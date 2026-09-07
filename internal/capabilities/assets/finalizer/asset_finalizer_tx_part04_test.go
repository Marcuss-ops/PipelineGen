package finalizer

import (
	"context"
	"encoding/json"
	assets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry"
	_ "github.com/mattn/go-sqlite3"
	"testing"
)

// TestAssetTxFinalizer_ContentHashInMetadataJson pins the godlike/07
// no-fake-availability contract for the source_version supersede-gate
// fix: metadata_json MUST include content_hash = artifact.SHA256 so
// that SourceVersionFor() (Tier 1, see
// internal/platform/sqlite/assets/source_version.go)
// reads the correct fingerprint from the same write boundary as the
// outbox event.
//
// Without this key, a republish that changes legacy_file_md5 would leave
// metadata_json.$.legacy_file_md5 stale (from the previous ingest), causing
// SourceVersionFor to return the OLD hash and the IndexingHandler to
// mark the NEW event as superseded — Qdrant never updates.
func TestAssetTxFinalizer_ContentHashInMetadataJson(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	fx := newTestFinalizer(t, db)
	ctx := context.Background()

	// First ingest: asset with hash "old-hash".
	tx1, _ := db.BeginTx(ctx, nil)
	_, _, err := fx.FinalizeAsset(ctx, WrapTx(tx1),
		publishedArtifact("asset-content-hash", "old-hash", "file-old"))
	if err != nil {
		tx1.Rollback()
		t.Fatalf("first finalize: %v", err)
	}
	if err := tx1.Commit(); err != nil {
		t.Fatalf("commit first: %v", err)
	}

	// Verify content_hash in metadata_json after first ingest.
	var metaJSON1 string
	db.QueryRowContext(ctx,
		`SELECT metadata_json FROM media_assets WHERE id = ?`,
		"asset-content-hash",
	).Scan(&metaJSON1)
	var meta1 map[string]any
	if err := json.Unmarshal([]byte(metaJSON1), &meta1); err != nil {
		t.Fatalf("unmarshal metadata_json (1st): %v", err)
	}
	if meta1["content_hash"] != "old-hash" {
		t.Errorf("metadata_json.content_hash after 1st ingest = %v, want %q",
			meta1["content_hash"], "old-hash")
	}

	// End-to-end: SourceVersionFor() must read Tier 1 (content_hash)
	// and return the hash from the first ingest.
	sv1, err := assets.SourceVersionFor(ctx, db, "asset-content-hash")
	if err != nil {
		t.Fatalf("SourceVersionFor (1st): %v", err)
	}
	if sv1 != "old-hash" {
		t.Errorf("SourceVersionFor() after 1st ingest = %q, want %q (Tier 1 broken!)", sv1, "old-hash")
	}

	// Second ingest: republish with new hash (the bug scenario).
	tx2, _ := db.BeginTx(ctx, nil)
	ref2, events2, err := fx.FinalizeAsset(ctx, WrapTx(tx2),
		publishedArtifact("asset-content-hash", "new-hash", "file-new"))
	if err != nil {
		tx2.Rollback()
		t.Fatalf("second finalize: %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("commit second: %v", err)
	}
	if ref2.ContentHash != "new-hash" {
		t.Errorf("ArtifactRef.ContentHash = %q, want %q", ref2.ContentHash, "new-hash")
	}

	// Verify content_hash in metadata_json is the NEW hash after republish.
	var metaJSON2 string
	db.QueryRowContext(ctx,
		`SELECT metadata_json FROM media_assets WHERE id = ?`,
		"asset-content-hash",
	).Scan(&metaJSON2)
	var meta2 map[string]any
	if err := json.Unmarshal([]byte(metaJSON2), &meta2); err != nil {
		t.Fatalf("unmarshal metadata_json (2nd): %v", err)
	}
	if meta2["content_hash"] != "new-hash" {
		t.Errorf("metadata_json.content_hash after 2nd ingest = %v, want %q (supersede gate would fire!)",
			meta2["content_hash"], "new-hash")
	}

	// End-to-end: SourceVersionFor() must now return the NEW hash
	// from Tier 1 — this is the canonical production contract that
	// prevents the supersede gate from firing on republish.
	sv2, err := assets.SourceVersionFor(ctx, db, "asset-content-hash")
	if err != nil {
		t.Fatalf("SourceVersionFor (2nd): %v", err)
	}
	if sv2 != "new-hash" {
		t.Errorf("SourceVersionFor() after republish = %q, want %q (supersede gate would fire!)", sv2, "new-hash")
	}

	// Verify legacy_file_md5 column is also the new hash.
	var colHash string
	db.QueryRowContext(ctx,
		`SELECT legacy_file_md5 FROM media_assets WHERE id = ?`,
		"asset-content-hash",
	).Scan(&colHash)
	if colHash != "new-hash" {
		t.Errorf("legacy_file_md5 column = %q, want %q", colHash, "new-hash")
	}

	// Verify the outbox event's source_version matches the metadata
	// content_hash (same write boundary — the canonical consistency
	// contract). FinalizeAsset returns events in-memory.
	if len(events2) != 1 {
		t.Fatalf("expected 1 outbox event, got %d", len(events2))
	}
	var payload map[string]any
	if err := json.Unmarshal(events2[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal outbox payload: %v", err)
	}
	if payload["source_version"] != "new-hash" {
		t.Errorf("outbox source_version = %v, want %q", payload["source_version"], "new-hash")
	}
	// Final consistency: outbox source_version == content_hash == SourceVersionFor.
	if payload["source_version"] != sv2 {
		t.Errorf("outbox source_version=%v != SourceVersionFor()=%q (write boundary inconsistency!)",
			payload["source_version"], sv2)
	}
}

func TestTxAdapter_SatisfiesInterface(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tx, _ := db.BeginTx(context.Background(), nil)
	defer tx.Rollback()

	domainTx := WrapTx(tx)
	// Use ExecContext to verify the adapter forwards correctly.
	result, err := domainTx.ExecContext(context.Background(),
		`INSERT INTO media_assets (id, name, filename, created_at, updated_at) VALUES (?, ?, ?, '', '')`,
		"adapter-test", "test", "test.mp4")
	if err != nil {
		t.Fatalf("domainTx.ExecContext: %v", err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		t.Errorf("RowsAffected = %d, want 1", affected)
	}

	// Also test QueryRowContext.
	row := domainTx.QueryRowContext(context.Background(),
		`SELECT name FROM media_assets WHERE id = ?`, "adapter-test")
	var name string
	if err := row.Scan(&name); err != nil {
		t.Fatalf("QueryRowContext.Scan: %v", err)
	}
	if name != "test" {
		t.Errorf("name = %q, want test", name)
	}
}
