package sqlite

import "testing"

// TestMigrations_268_MediaAuthorityRetirementMarkerIsApplied makes executable
// the claim that migration 268 states in its own header: "a lightweight marker
// table lets runtime and CI gates assert that the retirement has been applied".
// Nothing asserted it before this test — the marker was written and never read.
//
// Scope note (honest limitation, tracked in architecture/catalog.yaml under
// DEMO-SQLITE-MEDIA-COMMITTER / P2-9 Phase 2): the marker does NOT mean the
// quarantined media tables are gone from the schema. On the incremental
// 001..268 path they are intentionally left in place for audit/recovery, and
// the fresh-install baseline is still 000_baseline_267.sql (which creates them).
// Regenerating to a media-free baseline requires rehoming the SQLite
// media-reader fixtures first (boot_test.go, migrations_helpers_test.go::
// essentialTables, migrations_152/157/158/197), so this test pins the marker
// (the applied guard) and not the table removal (the pending work).
func TestMigrations_268_MediaAuthorityRetirementMarkerIsApplied(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	var count int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='_media_authority_retirement'`,
	).Scan(&count); err != nil {
		t.Fatalf("inspect _media_authority_retirement: %v", err)
	}
	if count != 1 {
		t.Fatal("migration 268 did not create the media-authority retirement marker")
	}

	// Exactly one row: the marker is a singleton guard (CHECK id = 1), so a
	// second row would mean the migration ran in a non-idempotent way.
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM _media_authority_retirement`).Scan(&rows); err != nil {
		t.Fatalf("count retirement marker rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("retirement marker rows = %d, want exactly 1 (idempotent singleton)", rows)
	}

	var note string
	if err := db.QueryRow(`SELECT note FROM _media_authority_retirement WHERE id = 1`).Scan(&note); err != nil {
		t.Fatalf("read retirement marker note: %v", err)
	}
	const want = "SQLite media authority retired — PostgreSQL media SSOT (P2-9, September 2026)"
	if note != want {
		t.Fatalf("retirement marker note = %q, want %q", note, want)
	}
}
