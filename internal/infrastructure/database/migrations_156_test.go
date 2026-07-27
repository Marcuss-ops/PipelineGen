// Package storage — migrations_156_test.go holds the scenario tests
// for migration 156 (asset_text_tracks step-2 audit-trail columns +
// asset_text_track_segments.text_hash). PR-CATALOG-MULTILINGUA step
// 2 added the audit-trail surface for the catalog translation
// pipeline: source_track_id / source_text_hash columns on
// asset_text_tracks (with FK ON DELETE SET NULL → asset_text_tracks.id)
// and a text_hash index on asset_text_track_segments.
//
// Covers:
//   - TestMigrations_156_AssetTextTracksSpecColumnsPresent
//     asset_text_tracks carries source_track_id (INTEGER
//     nullable FK) + source_text_hash (TEXT NOT NULL DEFAULT ”).
//   - TestMigrations_156_AssetTextTracksSourceTrackFKRoundTrip
//     Pin the audit-trail shape: insert a parent EN transcript + a
//     child IT translation with source_track_id pointing back to
//     the parent; DELETE the parent; verify the child survives
//     with source_track_id = NULL (ON DELETE SET NULL — NOT
//     CASCADE, which would erase the audit row entirely).
//   - TestMigrations_156_AssetTextTracksSpecDefaultsPermissive
//     A row insert WITHOUT source_track_id / source_text_hash
//     MUST succeed and yield NULL / ” defaults — the additive
//     contract lets migration 156 ship without a separate
//     back-fill.
//   - TestMigrations_156_AssetTextTrackSegmentsTextHashPresent
//     asset_text_track_segments carries the text_hash column +
//     supporting index (DEFAULT ” / non-unique).
package storage

import (
	"database/sql"
	"testing"
)

func TestMigrations_156_AssetTextTracksSpecColumnsPresent(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	seen := scanColumnNames(t, db, "asset_text_tracks")
	for _, col := range []string{"source_track_id", "source_text_hash"} {
		if _, ok := seen[col]; !ok {
			t.Errorf("asset_text_tracks missing step-2 column %q (added by migration 156)", col)
		}
	}
}

func TestMigrations_156_AssetTextTracksSourceTrackFKRoundTrip(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	const assetID = "rt-step2-fk-1"
	if _, err := db.Exec(
		`INSERT INTO media_assets (id, source, name, media_type, lifecycle_state) VALUES (?, 'artlist', 'step2-fk', 'video', 'ACTIVE')`,
		assetID,
	); err != nil {
		t.Fatalf("setup media_assets: %v", err)
	}
	res, err := db.Exec(
		`INSERT INTO asset_text_tracks (asset_id, language_code, text_kind, text_content, source_type, is_original, text_hash, status) VALUES (?, 'en', 'transcript', '[en] hello', 'provided', 1, ?, 'READY')`,
		assetID, "en-hash-1",
	)
	if err != nil {
		t.Fatalf("insert parent EN track: %v", err)
	}
	parentID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("read parent LastInsertId: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO asset_text_tracks (asset_id, language_code, text_kind, text_content, source_type, source_track_id, source_text_hash, is_original, text_hash, translation_key, prompt_version, status) VALUES (?, 'it', 'transcript', '[it] hello', 'translation', ?, 'en-hash-1', 0, ?, 'tk-step2-1', 'prompt-v1', 'READY')`,
		assetID, parentID, "it-hash-1",
	); err != nil {
		t.Fatalf("insert child IT translation: %v", err)
	}
	var childSourceID sql.NullInt64
	var childSourceHash string
	if err := db.QueryRow(
		`SELECT source_track_id, source_text_hash FROM asset_text_tracks WHERE asset_id = ? AND language_code = 'it' AND text_kind = 'transcript'`,
		assetID,
	).Scan(&childSourceID, &childSourceHash); err != nil {
		t.Fatalf("read child row: %v", err)
	}
	if !childSourceID.Valid || childSourceID.Int64 != parentID {
		t.Errorf("child.source_track_id = %v, want %d", childSourceID, parentID)
	}
	if childSourceHash != "en-hash-1" {
		t.Errorf("child.source_text_hash = %q, want %q", childSourceHash, "en-hash-1")
	}
	if _, err := db.Exec(`DELETE FROM asset_text_tracks WHERE id = ?`, parentID); err != nil {
		t.Fatalf("delete parent: %v", err)
	}
	var afterDeleteID sql.NullInt64
	err = db.QueryRow(
		`SELECT source_track_id FROM asset_text_tracks WHERE asset_id = ? AND language_code = 'it' AND text_kind = 'transcript'`,
		assetID,
	).Scan(&afterDeleteID)
	if err != nil {
		t.Fatalf("read child after parent-delete: %v", err)
	}
	if afterDeleteID.Valid {
		t.Errorf("child.source_track_id should be NULL after parent delete (ON DELETE SET NULL); got %d", afterDeleteID.Int64)
	}
}

func TestMigrations_156_AssetTextTracksSpecDefaultsPermissive(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	const assetID = "rt-step2-defaults-1"
	if _, err := db.Exec(
		`INSERT INTO media_assets (id, source, name, media_type, lifecycle_state) VALUES (?, 'artlist', 'step2-defaults', 'video', 'ACTIVE')`,
		assetID,
	); err != nil {
		t.Fatalf("setup media_assets: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO asset_text_tracks (asset_id, language_code, text_kind, text_content, source_type, is_original, text_hash, status) VALUES (?, 'de', 'transcript', '[de] hallo', 'provided', 1, ?, 'READY')`,
		assetID, "de-hash-1",
	); err != nil {
		t.Fatalf("insert without source_track_id / source_text_hash: %v", err)
	}
	var sourceID sql.NullInt64
	var sourceHash string
	if err := db.QueryRow(
		`SELECT source_track_id, source_text_hash FROM asset_text_tracks WHERE asset_id = ? AND language_code = 'de' AND text_kind = 'transcript'`,
		assetID,
	).Scan(&sourceID, &sourceHash); err != nil {
		t.Fatalf("read defaults: %v", err)
	}
	if sourceID.Valid {
		t.Errorf("default source_track_id should be NULL; got %d", sourceID.Int64)
	}
	if sourceHash != "" {
		t.Errorf("default source_text_hash should be ''; got %q", sourceHash)
	}
}

func TestMigrations_156_AssetTextTrackSegmentsTextHashPresent(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	seen := scanColumnNames(t, db, "asset_text_track_segments")
	if _, ok := seen["text_hash"]; !ok {
		t.Errorf("asset_text_track_segments missing text_hash column (added by migration 156)")
	}
	segIdx := mustReadIndexNames(t, db, "asset_text_track_segments")
	if !contains(segIdx, "idx_asset_text_track_segments_hash") {
		t.Errorf("asset_text_track_segments missing index %q (declared by migration 156)", "idx_asset_text_track_segments_hash")
	}
}
