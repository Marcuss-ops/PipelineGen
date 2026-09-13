// Package media — media_index_retire_test.go pins the PostgreSQL index-retire
// primitives consumed by the deletion saga's index-delete hop (SoftDeleteAsset
// and DeleteAssetIndexPoints).
package media_test

import (
	"context"
	"database/sql"
	"testing"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

func countEmbeddings(t *testing.T, db *sql.DB, assetID string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM media_embeddings WHERE asset_id = $1`, assetID).Scan(&n); err != nil {
		t.Fatalf("count embeddings %s: %v", assetID, err)
	}
	return n
}

// TestPostgresMediaCommitter_DeleteAssetIndexPoints pins the pgvector index
// retirement surface: it removes exactly the requested assets' media_embeddings
// rows, is idempotent on re-run, and treats an empty/blank id list as a no-op.
func TestPostgresMediaCommitter_DeleteAssetIndexPoints(t *testing.T) {
	committer, db := newPostgresCommitter(t)
	ctx := context.Background()
	writer := pgmedia.NewVectorSurfaceWriter(db)
	if err := writer.EnsureEmbeddingFamily(ctx, "text", "test-model", 4); err != nil {
		t.Fatalf("ensure embedding family: %v", err)
	}

	seedIndexed := func(id string) {
		t.Helper()
		if _, err := db.ExecContext(ctx,
			`INSERT INTO media_assets (id, source, name, lifecycle_state, index_state)
			 VALUES ($1, 'clip', $1, 'DRIVE_DELETED', 'INDEXED')`, id); err != nil {
			t.Fatalf("seed asset %s: %v", id, err)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin tx for %s: %v", id, err)
		}
		if err := writer.UpsertEmbeddingTx(ctx, tx, id, "text", "test-model", []float32{0.1, 0.2, 0.3, 0.4}); err != nil {
			_ = tx.Rollback()
			t.Fatalf("seed embedding %s: %v", id, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit embedding %s: %v", id, err)
		}
	}
	seedIndexed("asset-retire-1")
	seedIndexed("asset-retire-2")
	seedIndexed("asset-keep")

	if err := committer.DeleteAssetIndexPoints(ctx, []string{"asset-retire-1", "asset-retire-2"}); err != nil {
		t.Fatalf("DeleteAssetIndexPoints: %v", err)
	}
	if n := countEmbeddings(t, db, "asset-retire-1"); n != 0 {
		t.Errorf("asset-retire-1 embeddings: want 0, got %d", n)
	}
	if n := countEmbeddings(t, db, "asset-retire-2"); n != 0 {
		t.Errorf("asset-retire-2 embeddings: want 0, got %d", n)
	}
	if n := countEmbeddings(t, db, "asset-keep"); n != 1 {
		t.Errorf("asset-keep embeddings must be untouched: want 1, got %d", n)
	}

	// Idempotent re-run: a zero-row delete is success.
	if err := committer.DeleteAssetIndexPoints(ctx, []string{"asset-retire-1"}); err != nil {
		t.Fatalf("idempotent re-run: %v", err)
	}
	// Empty / blank-only input is a no-op, never a SQL error.
	if err := committer.DeleteAssetIndexPoints(ctx, nil); err != nil {
		t.Fatalf("nil ids: %v", err)
	}
	if err := committer.DeleteAssetIndexPoints(ctx, []string{"", "   "}); err != nil {
		t.Fatalf("blank ids: %v", err)
	}
}

// TestPostgresMediaCommitter_SoftDeleteAsset pins parity with the SQLite
// SoftDelete: lifecycle_state=DELETED and a non-empty deleted_at, plus the
// required-id guard.
func TestPostgresMediaCommitter_SoftDeleteAsset(t *testing.T) {
	committer, db := newPostgresCommitter(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx,
		`INSERT INTO media_assets (id, source, name, lifecycle_state, index_state)
		 VALUES ('asset-soft-del', 'clip', 'soft', 'INDEX_DELETED', 'DELETED')`); err != nil {
		t.Fatalf("seed asset: %v", err)
	}

	if err := committer.SoftDeleteAsset(ctx, "asset-soft-del"); err != nil {
		t.Fatalf("SoftDeleteAsset: %v", err)
	}
	var state, deletedAt string
	if err := db.QueryRowContext(ctx,
		`SELECT lifecycle_state, deleted_at FROM media_assets WHERE id = 'asset-soft-del'`).Scan(&state, &deletedAt); err != nil {
		t.Fatalf("read retired row: %v", err)
	}
	if state != "DELETED" {
		t.Errorf("lifecycle_state: want DELETED, got %q", state)
	}
	if deletedAt == "" {
		t.Error("deleted_at must be stamped on soft delete")
	}

	if err := committer.SoftDeleteAsset(ctx, "   "); err == nil {
		t.Fatal("blank asset id must be rejected")
	}
}
