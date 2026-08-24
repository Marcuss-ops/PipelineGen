// Package storage — migrations_153_test.go holds the scenario test
// for migration 153 (asset_artifacts table). PR-CATALOG-MULTILINGUA
// step 1 introduced the asset_artifacts table that stores the
// physical artifact rows (video/image files on Drive, their FUSE
// local paths, SHA-256, dimensions, etc.) keyed by media_assets.id.
//
// Covers:
//   - TestMigrations_153_AssetArtifactsTablePresent
//     asset_artifacts has all 16 columns in canonical order, the
//     FK to media_assets(id) is registered, and the 3 supporting
//     indexes are present.
package sqlite

import (
	"testing"
)

func TestMigrations_153_AssetArtifactsTablePresent(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	var count int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='asset_artifacts'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("check asset_artifacts presence: %v", err)
	}
	if count != 1 {
		t.Fatalf("asset_artifacts table missing in sqlite_master (count=%d, want 1)", count)
	}

	seen := scanColumnNames(t, db, "asset_artifacts")
	required := []string{
		"id", "asset_id", "role", "mime_type",
		"local_path", "drive_file_id", "drive_link",
		"file_size", "file_sha256",
		"width", "height", "frame_rate", "duration_ms",
		"status", "created_at", "updated_at",
	}
	for _, col := range required {
		if _, ok := seen[col]; !ok {
			t.Errorf("asset_artifacts missing column %q (declared by migration 153)", col)
		}
	}

	// FK from asset_artifacts.asset_id → media_assets.id.
	var fkCount int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM pragma_foreign_key_list('asset_artifacts')
		 WHERE "table" = 'media_assets' AND "from" = 'asset_id' AND "to" = 'id'`,
	).Scan(&fkCount)
	if err != nil {
		t.Fatalf("read asset_artifacts foreign_key_list: %v", err)
	}
	if fkCount != 1 {
		t.Errorf("asset_artifacts.asset_id FK to media_assets.id missing (count=%d, want 1)", fkCount)
	}

	// 3 supporting indexes.
	artifactsIndexes := mustReadIndexNames(t, db, "asset_artifacts")
	for _, want := range []string{
		"idx_asset_artifacts_asset_role",
		"idx_asset_artifacts_unique_singleton",
		"idx_asset_artifacts_status_updated",
	} {
		if !contains(artifactsIndexes, want) {
			t.Errorf("asset_artifacts missing index %q (declared by migration 153)", want)
		}
	}
}
