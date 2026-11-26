package sqlite

import "testing"

// TestMigrations_268_MediaAuthorityRetirementMarkerIsRetired makes executable
// the retirement that migration 268 declared. 268 wrote a single
// `_media_authority_retirement` marker so runtime/CI gates could assert the
// SQLite media authority was gone; the only remaining reader was
// cmd/gen_baseline, which seeded the marker into the baseline DB.
//
// DEMOL-13 removed that seed and migration 301 drops the marker table: the
// PostgreSQL media plane is the sole authority, so carrying a second boolean
// for "the cutover happened" is itself legacy weight. The end-state is that no
// marker table and no SQLite media-authority table exist.
func TestMigrations_268_MediaAuthorityRetirementMarkerIsRetired(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	var count int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='_media_authority_retirement'`,
	).Scan(&count); err != nil {
		t.Fatalf("inspect _media_authority_retirement: %v", err)
	}
	if count != 0 {
		t.Fatal("the media-authority retirement marker must be dropped by 301")
	}
}
