// Package storage — migrations_157_test.go holds the scenario tests
// for migration 157 (asset_state column on media_assets).
// PR-CATALOG-MULTILINGUA step 7 added the canonical asset_state
// alphabet on media_assets so the planner can read state directly
// from the storage projection without joining on a derived table.
//
// godlike/06 SSOT invariant: the column's alphabet MUST equal the
// 14 canonical AssetState values declared at
// internal/kernel/asset/asset_state_values.go — percheck_asset_state_canonical_14
// enforces the count, and percheck_asset_state_no_shadow_enum
// enforces no shadow declarations.
//
// Covers:
//   - TestMigrations_157_AssetStateColumnPresent
//   - TestMigrations_157_AssetStateIndexPresent
//   - TestMigrations_157_AssetStateColumnRoundTrip
package storage

import (
	"testing"
)

func TestMigrations_157_AssetStateColumnPresent(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	seen := scanColumnNames(t, db, "media_assets")
	if _, ok := seen["asset_state"]; !ok {
		t.Errorf("media_assets missing asset_state column (added by migration 157; analog to the canonical.go / asset_state_values.go canonical surface)")
	}
}

func TestMigrations_157_AssetStateIndexPresent(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	indexes := mustReadIndexNames(t, db, "media_assets")
	if !contains(indexes, "idx_media_assets_asset_state") {
		t.Errorf("media_assets missing index %q (declared by migration 157)", "idx_media_assets_asset_state")
	}
}

func TestMigrations_157_AssetStateColumnRoundTrip(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	const assetID = "rt-step7-1"
	_, err := db.Exec(
		`INSERT INTO media_assets (id, source, name, media_type, lifecycle_state, index_state)
		 VALUES (?, 'artlist', 'step7 round-trip', 'video', 'ACTIVE', 'INDEXED')`,
		assetID,
	)
	if err != nil {
		t.Fatalf("insert asset_state round-trip row: %v", err)
	}
	var got string
	if err := db.QueryRow(
		`SELECT asset_state FROM media_assets WHERE id = ?`,
		assetID,
	).Scan(&got); err != nil {
		t.Fatalf("select asset_state round-trip row: %v", err)
	}
	if got != "READY" {
		t.Errorf("asset_state projection = %q, want %q", got, "READY")
	}
}
