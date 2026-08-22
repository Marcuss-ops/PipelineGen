package mediaregistry

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// newAdvanceDB builds an in-memory DB with the minimal schema the
// AdvanceActiveProjectionSequence path touches: registry_events (seq +
// asset_id), media_assets (embedding/eligibility boundary) and
// projection_registry (status + source_registry_seq).
func newAdvanceDB(t *testing.T) (*sql.DB, *Ledger) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`
		CREATE TABLE registry_events (
			seq INTEGER PRIMARY KEY AUTOINCREMENT,
			event_id TEXT UNIQUE NOT NULL,
			asset_id TEXT,
			event_type TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE media_assets (
			id TEXT PRIMARY KEY,
			media_type TEXT NOT NULL DEFAULT '',
			asset_kind TEXT NOT NULL DEFAULT '',
			namespace TEXT NOT NULL DEFAULT '',
			source_type TEXT NOT NULL DEFAULT '',
			deleted_at TEXT NOT NULL DEFAULT '',
			embedding_json TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE projection_registry (
			projection_id TEXT PRIMARY KEY,
			status TEXT NOT NULL DEFAULT '',
			source_registry_seq INTEGER NOT NULL DEFAULT 0
		);`)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := NewLedger(db)
	if err != nil {
		t.Fatal(err)
	}
	return db, ledger
}

// insertIndexableAsset seeds a row that satisfies the canonical
// SearchIndexEligibilitySQL boundary (video/clip taxonomy + namespace +
// source_type + populated embedding), so its registry event advances the
// qdrant projection sequence.
func insertIndexableAsset(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO media_assets (id, media_type, asset_kind, namespace, source_type, embedding_json) VALUES (?, 'video', 'clip', 'stock', 'youtube', '[1.0]')`, id); err != nil {
		t.Fatal(err)
	}
}

func insertEvent(t *testing.T, db *sql.DB, assetID string) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO registry_events (event_id, asset_id) VALUES (lower(hex(randomblob(16))), NULLIF(?, ''))`, assetID)
	if err != nil {
		t.Fatal(err)
	}
	seq, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return seq
}

func TestAdvanceActiveProjectionSequence(t *testing.T) {
	db, ledger := newAdvanceDB(t)
	ctx := context.Background()

	// Two indexable assets: their registry events are qdrant-eligible.
	for _, id := range []string{"asset-1", "asset-2"} {
		insertIndexableAsset(t, db, id)
	}
	insertEvent(t, db, "asset-1")
	latest := insertEvent(t, db, "asset-2")

	if _, err := db.Exec(`INSERT INTO projection_registry (projection_id, status, source_registry_seq) VALUES ('proj-active', 'ACTIVE', 0)`); err != nil {
		t.Fatal(err)
	}

	if err := ledger.AdvanceActiveProjectionSequence(ctx); err != nil {
		t.Fatalf("advance: %v", err)
	}
	var seq int64
	if err := db.QueryRowContext(ctx, `SELECT source_registry_seq FROM projection_registry WHERE projection_id='proj-active'`).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	if seq != latest {
		t.Fatalf("active projection seq: got %d, want %d", seq, latest)
	}
}

func TestAdvanceActiveProjectionSequenceIsMonotonic(t *testing.T) {
	db, ledger := newAdvanceDB(t)
	ctx := context.Background()

	insertIndexableAsset(t, db, "asset-1")
	insertEvent(t, db, "asset-1")

	if _, err := db.Exec(`INSERT INTO projection_registry (projection_id, status, source_registry_seq) VALUES ('proj-active', 'ACTIVE', 100)`); err != nil {
		t.Fatal(err)
	}

	// The canonical sequence (1) is BELOW the stored checkpoint (100); the
	// advance must never rewind the ACTIVE projection.
	if err := ledger.AdvanceActiveProjectionSequence(ctx); err != nil {
		t.Fatalf("advance: %v", err)
	}
	var seq int64
	if err := db.QueryRowContext(ctx, `SELECT source_registry_seq FROM projection_registry WHERE projection_id='proj-active'`).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	if seq != 100 {
		t.Fatalf("advance rewound the checkpoint: got %d, want 100", seq)
	}
}

func TestAdvanceActiveProjectionSequenceIgnoresNonActive(t *testing.T) {
	db, ledger := newAdvanceDB(t)
	ctx := context.Background()

	insertIndexableAsset(t, db, "asset-1")
	latest := insertEvent(t, db, "asset-1")

	for _, row := range []struct{ id, status string }{
		{"proj-retired", "RETIRED"},
		{"proj-failed", "FAILED"},
	} {
		if _, err := db.Exec(`INSERT INTO projection_registry (projection_id, status, source_registry_seq) VALUES (?, ?, 0)`, row.id, row.status); err != nil {
			t.Fatal(err)
		}
	}

	if err := ledger.AdvanceActiveProjectionSequence(ctx); err != nil {
		t.Fatalf("advance: %v", err)
	}
	for _, row := range []struct{ id, status string }{
		{"proj-retired", "RETIRED"},
		{"proj-failed", "FAILED"},
	} {
		var seq int64
		if err := db.QueryRowContext(ctx, `SELECT source_registry_seq FROM projection_registry WHERE projection_id=?`, row.id).Scan(&seq); err != nil {
			t.Fatal(err)
		}
		if seq != 0 {
			t.Fatalf("non-ACTIVE projection %s must be untouched: got seq=%d, want 0", row.id, seq)
		}
	}
	_ = latest
}

func TestAdvanceActiveProjectionSequenceNoActiveIsNoop(t *testing.T) {
	_, ledger := newAdvanceDB(t)
	ctx := context.Background()
	if err := ledger.AdvanceActiveProjectionSequence(ctx); err != nil {
		t.Fatalf("advance with no ACTIVE projection must be a no-op, got %v", err)
	}
}

func TestAdvanceActiveProjectionSequenceExcludesNonIndexable(t *testing.T) {
	db, ledger := newAdvanceDB(t)
	ctx := context.Background()

	// A folder asset and a deleted asset must not count toward the qdrant
	// sequence (same eligibility boundary as LatestQdrantEventSequence).
	if _, err := db.Exec(`INSERT INTO media_assets (id, media_type, embedding_json) VALUES ('folder-1', 'folder', '[1.0]')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO media_assets (id, media_type, asset_kind, namespace, source_type, deleted_at, embedding_json) VALUES ('deleted-1', 'video', 'clip', 'stock', 'youtube', '2026-01-01', '[1.0]')`); err != nil {
		t.Fatal(err)
	}
	insertEvent(t, db, "folder-1")
	insertEvent(t, db, "deleted-1")

	if _, err := db.Exec(`INSERT INTO projection_registry (projection_id, status, source_registry_seq) VALUES ('proj-active', 'ACTIVE', 0)`); err != nil {
		t.Fatal(err)
	}

	if err := ledger.AdvanceActiveProjectionSequence(ctx); err != nil {
		t.Fatalf("advance: %v", err)
	}
	var seq int64
	if err := db.QueryRowContext(ctx, `SELECT source_registry_seq FROM projection_registry WHERE projection_id='proj-active'`).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	if seq != 0 {
		t.Fatalf("non-indexable events must not advance the checkpoint: got %d, want 0", seq)
	}
}

// TestAdvanceActiveProjectionSequenceExcludesIneligibleTaxonomy pins the
// canonical eligibility boundary (SearchIndexEligibilitySQL): events for
// REGISTERED-but-not-SEARCHABLE assets (audio) and rows with incomplete
// taxonomy (legacy media_type=video without asset_kind/namespace/source_type)
// must NOT advance the qdrant projection sequence.
func TestAdvanceActiveProjectionSequenceExcludesIneligibleTaxonomy(t *testing.T) {
	db, ledger := newAdvanceDB(t)
	ctx := context.Background()

	// Audio assets are REGISTERED but not SEARCHABLE (no Qdrant point).
	if _, err := db.Exec(`INSERT INTO media_assets (id, media_type, asset_kind, namespace, source_type, embedding_json) VALUES ('audio-1', 'audio', 'sfx', 'audio', 'artlist', '[1.0]')`); err != nil {
		t.Fatal(err)
	}
	// A legacy video row with empty asset_kind/namespace/source_type is not
	// Qdrant-eligible even though embedding_json is populated.
	if _, err := db.Exec(`INSERT INTO media_assets (id, media_type, embedding_json) VALUES ('legacy-1', 'video', '[1.0]')`); err != nil {
		t.Fatal(err)
	}
	insertEvent(t, db, "audio-1")
	insertEvent(t, db, "legacy-1")

	if _, err := db.Exec(`INSERT INTO projection_registry (projection_id, status, source_registry_seq) VALUES ('proj-active', 'ACTIVE', 0)`); err != nil {
		t.Fatal(err)
	}

	if err := ledger.AdvanceActiveProjectionSequence(ctx); err != nil {
		t.Fatalf("advance: %v", err)
	}
	var seq int64
	if err := db.QueryRowContext(ctx, `SELECT source_registry_seq FROM projection_registry WHERE projection_id='proj-active'`).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	if seq != 0 {
		t.Fatalf("ineligible-taxonomy events must not advance the checkpoint: got %d, want 0", seq)
	}
}
