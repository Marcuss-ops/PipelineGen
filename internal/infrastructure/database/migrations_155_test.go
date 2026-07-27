// Package storage — migrations_155_test.go holds the scenario tests
// for migration 155 (asset_text_tracks translation fingerprint).
// PR-CATALOG-MULTILINGUA step 4 added three translation-fingerprint
// columns + a partial UNIQUE INDEX on asset_text_tracks so that the
// audit-trail invariant (godlike/06 — never silently overwrite prior
// translations) is enforced at the SQL boundary.
//
// Covers:
//   - TestMigrations_155_AssetTextTracksTranslationFingerprintColumnsPresent
//     asset_text_tracks carries the three new columns added by
//     migration 155: prompt_version (TEXT NOT NULL DEFAULT ”),
//     translation_key (TEXT NOT NULL DEFAULT ”), is_current
//     (INTEGER NOT NULL DEFAULT 1).
//   - TestMigrations_155_AssetTextTracksTranslationFingerprintPartialUniqueIndexPresent
//     Pins the partial UNIQUE INDEX declared by migration 155
//     (CREATE UNIQUE INDEX idx_asset_text_tracks_current ON
//     asset_text_tracks (asset_id, language_code, text_kind)
//     WHERE is_current = 1) and asserts the WHERE clause shape.
//   - TestMigrations_155_AssetTextTracksTranslationFingerprintKeepsOneCurrentRow
//     When a Materializer-style flip-and-insert pattern is
//     executed manually (UPDATE prior is_current=0; INSERT new
//     is_current=1), the partial UNIQUE INDEX permits the insert
//     AND preserves both rows for audit.
package storage

import (
	"strings"
	"testing"
)

func TestMigrations_155_AssetTextTracksTranslationFingerprintColumnsPresent(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	seen := scanColumnNames(t, db, "asset_text_tracks")
	for _, col := range []string{
		"prompt_version",
		"translation_key",
		"is_current",
	} {
		if _, ok := seen[col]; !ok {
			t.Errorf("asset_text_tracks missing step-4 column %q (added by migration 155)", col)
		}
	}
}

func TestMigrations_155_AssetTextTracksTranslationFingerprintPartialUniqueIndexPresent(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	indexes := mustReadIndexNames(t, db, "asset_text_tracks")
	found := false
	for _, idx := range indexes {
		if idx == "idx_asset_text_tracks_current" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("asset_text_tracks missing partial UNIQUE index %q (declared by migration 155)", "idx_asset_text_tracks_current")
	}
	// Verify the index has the WHERE is_current = 1 clause.
	var sqlText string
	err := db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?`,
		"idx_asset_text_tracks_current",
	).Scan(&sqlText)
	if err != nil {
		t.Fatalf("read idx_asset_text_tracks_current sql: %v", err)
	}
	if !strings.Contains(strings.ToLower(sqlText), "where is_current = 1") {
		t.Errorf("idx_asset_text_tracks_current must filter on WHERE is_current = 1; got sql=%q", sqlText)
	}
}

func TestMigrations_155_AssetTextTracksTranslationFingerprintKeepsOneCurrentRow(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	// Need a parent media_assets row to satisfy the FK CASCADE.
	const assetID = "rt-step4-1"
	_, err := db.Exec(
		`INSERT INTO media_assets (id, source, name, media_type, lifecycle_state)
		 VALUES (?, 'artlist', 'step4 test', 'video', 'ACTIVE')`,
		assetID,
	)
	if err != nil {
		t.Fatalf("setup media_assets for step4 round-trip: %v", err)
	}

	// 1) Insert baseline row.
	if _, err := db.Exec(
		`INSERT INTO asset_text_tracks (
			asset_id, language_code, text_kind, text_content,
			source_type, is_current, translation_key, prompt_version, status
		) VALUES (?, 'it', 'transcript', '[it] hello world v1', 'translation', 1, ?, 'prompt-v1', 'READY')`,
		assetID, "key-v1",
	); err != nil {
		t.Fatalf("baseline insert: %v", err)
	}

	// 2) Flip baseline to is_current=0 + insert new row
	//    is_current=1 with a DIFFERENT translation_key. The
	//    partial UNIQUE INDEX allows this — both rows coexist
	//    (audit predecessor + new current).
	if _, err := db.Exec(
		`UPDATE asset_text_tracks SET is_current = 0, updated_at = datetime('now')
		 WHERE asset_id = ? AND language_code = 'it' AND text_kind = 'transcript' AND is_current = 1`,
		assetID,
	); err != nil {
		t.Fatalf("flip baseline: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO asset_text_tracks (
			asset_id, language_code, text_kind, text_content,
			source_type, is_current, translation_key, prompt_version, status
		) VALUES (?, 'it', 'transcript', '[it] hello world v2', 'translation', 1, ?, 'prompt-v2', 'READY')`,
		assetID, "key-v2",
	); err != nil {
		t.Fatalf("new-current insert (different translation_key): %v", err)
	}

	// 3) Verify: exactly one row is_current=1, the prior row
	//    is_current=0 (audit preserved).
	var currentCount int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM asset_text_tracks
		 WHERE asset_id = ? AND language_code = 'it' AND text_kind = 'transcript' AND is_current = 1`,
		assetID,
	).Scan(&currentCount)
	if err != nil {
		t.Fatalf("count current rows: %v", err)
	}
	if currentCount != 1 {
		t.Errorf("expected exactly 1 is_current=1 row after flip+insert, got %d", currentCount)
	}

	var totalCount int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM asset_text_tracks
		 WHERE asset_id = ? AND language_code = 'it' AND text_kind = 'transcript'`,
		assetID,
	).Scan(&totalCount)
	if err != nil {
		t.Fatalf("count total rows: %v", err)
	}
	if totalCount != 2 {
		t.Errorf("expected audit-trail to preserve 2 rows total (1 current + 1 audit-predecessor), got %d", totalCount)
	}
}
