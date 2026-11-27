package media_test

import (
	"testing"

	pgmigration "github.com/Marcuss-ops/PipelineGen/migrations/postgres"
)

// DB-07: migration 018 must remove the duplicate media indexes and keep the
// stricter/composite survivors.
func TestMediaDuplicateIndexesRemoved(t *testing.T) {
	db := newMediaTestDB(t)

	// The suite shares one live database, so an earlier test (or a parallel
	// package) may have re-applied the bootstrap DDL that recreates these
	// indexes after 018's ledger row. Re-apply the migration's own idempotent
	// cleanup so the assertion tests the DDL, not the ambient schema state.
	if _, err := db.Exec(pgmigration.MediaDuplicateIndexCleanupDDL); err != nil {
		t.Fatalf("apply 018 cleanup: %v", err)
	}

	indexExists := func(name string) bool {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pg_indexes WHERE schemaname='public' AND indexname=$1`, name).Scan(&n); err != nil {
			t.Fatalf("count index %s: %v", name, err)
		}
		return n > 0
	}

	for _, removed := range []string{
		"idx_asset_text_track_segments_track",
		"idx_media_embeddings_asset",
	} {
		if indexExists(removed) {
			t.Errorf("%s still exists after migration 018", removed)
		}
	}
	for _, kept := range []string{
		"asset_text_track_segments_track_id_sequence_no_key",
	} {
		if !indexExists(kept) {
			t.Errorf("canonical index %s is missing", kept)
		}
	}

	// render_jobs is created by the live deployment, not by the media
	// self-bootstrap, so simulate the legacy pair and apply the migration DDL
	// directly to prove the guarded drop.
	if _, err := db.Exec(`CREATE TABLE render_jobs (
		id BIGSERIAL PRIMARY KEY,
		idempotency_key TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatalf("create render_jobs: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DROP TABLE IF EXISTS render_jobs`) })
	if _, err := db.Exec(`CREATE UNIQUE INDEX idx_render_jobs_idempotency ON render_jobs(idempotency_key)`); err != nil {
		t.Fatalf("create legacy idempotency index: %v", err)
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX render_jobs_idempotency_key_unique ON render_jobs(idempotency_key) WHERE idempotency_key <> ''`); err != nil {
		t.Fatalf("create canonical idempotency index: %v", err)
	}
	if _, err := db.Exec(pgmigration.MediaDuplicateIndexCleanupDDL); err != nil {
		t.Fatalf("apply 018 cleanup: %v", err)
	}
	if indexExists("idx_render_jobs_idempotency") {
		t.Error("legacy idx_render_jobs_idempotency still exists after cleanup")
	}
	if !indexExists("render_jobs_idempotency_key_unique") {
		t.Error("canonical render_jobs_idempotency_key_unique was dropped")
	}
}
