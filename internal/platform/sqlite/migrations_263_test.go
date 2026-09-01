package sqlite

import "testing"

func TestMigrations_263_QuarantinesLegacyAssetRegistry(t *testing.T) {
	db, _ := applyFreshSmokeDB(t)
	for _, table := range []string{"assets", "asset_sources", "job_assets"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("legacy table %q must not remain in the live media contract", table)
		}
	}
	for _, table := range []string{"legacy_assets", "legacy_asset_sources", "legacy_job_assets"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("legacy archive table %q missing", table)
		}
	}
}
