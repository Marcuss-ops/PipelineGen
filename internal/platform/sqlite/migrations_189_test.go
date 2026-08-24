package sqlite

import "testing"

func TestMigrations_189_CanonicalStateModel(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	var indexState, assetState string
	if _, err := db.Exec(`INSERT INTO media_assets (id, lifecycle_state, index_state, asset_state) VALUES (?, 'ACTIVE', 'INDEX_PENDING', 'DISCOVERED')`, "legacy-pending"); err == nil {
		t.Fatal("legacy INDEX_PENDING insert should be rejected after migration 189")
	}
	if _, err := db.Exec(`INSERT INTO media_assets (id, lifecycle_state, index_state, asset_state) VALUES (?, 'ACTIVE', 'INDEX_FAILED', 'DISCOVERED')`, "legacy-failed"); err == nil {
		t.Fatal("legacy INDEX_FAILED insert should be rejected after migration 189")
	}

	if _, err := db.Exec(`INSERT INTO media_assets (id, lifecycle_state, index_state) VALUES (?, 'ACTIVE', 'INDEXING_FAILED')`, "failed"); err != nil {
		t.Fatalf("insert canonical failed state: %v", err)
	}
	if err := db.QueryRow(`SELECT index_state, asset_state FROM media_assets WHERE id = ?`, "failed").Scan(&indexState, &assetState); err != nil {
		t.Fatal(err)
	}
	if indexState != "INDEXING_FAILED" || assetState != "FAILED_RETRYABLE" {
		t.Fatalf("projection = (%q, %q), want (INDEXING_FAILED, FAILED_RETRYABLE)", indexState, assetState)
	}

	if _, err := db.Exec(`INSERT INTO media_assets (id, lifecycle_state, index_state) VALUES (?, 'ACTIVE', 'INDEXED')`, "ready"); err != nil {
		t.Fatalf("insert canonical indexed state: %v", err)
	}
	if err := db.QueryRow(`SELECT asset_state FROM media_assets WHERE id = ?`, "ready").Scan(&assetState); err != nil {
		t.Fatal(err)
	}
	if assetState != "READY" {
		t.Fatalf("indexed active projection = %q, want READY", assetState)
	}

	if _, err := db.Exec(`UPDATE media_assets SET asset_state = 'READY_MULTILINGUAL' WHERE id = ?`, "ready"); err == nil {
		t.Fatal("direct divergent asset_state write should fail")
	}
	if _, err := db.Exec(`INSERT INTO media_assets_pipeline_events (id, clip_id, fase) VALUES (?, ?, ?)`, "invalid-event", "ready", "NOT_A_PIPELINE_STATE"); err == nil {
		t.Fatal("invalid PipelineState event should fail")
	}
}
