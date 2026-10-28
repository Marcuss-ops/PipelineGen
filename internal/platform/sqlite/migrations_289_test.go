package sqlite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrations_289_LifecycleDefaultUnification pins the schema-preserving
// rebuild of media_assets: the ONLY declaration change is the lifecycle_state
// default ('ready' -> 'ACTIVE', unified with the PostgreSQL plane and the
// state_valid trigger vocabulary); rows, indexes and triggers survive, child
// rows with REFERENCES media_assets survive, and an insert that omits
// lifecycle_state no longer aborts at the trigger (the old default was not in
// the trigger vocabulary at all).
func TestMigrations_289_LifecycleDefaultUnification(t *testing.T) {
	targetDir, err := filepath.Abs(migrationsDirFrom)
	if err != nil {
		t.Fatal(err)
	}
	preDir := copyMigrationSubset(t, targetDir, 288, map[int]bool{0: true})
	fullDir := copyMigrationSubset(t, targetDir, 289, map[int]bool{0: true})
	dbPath := filepath.Join(t.TempDir(), "lifecycle-289.sqlite")
	if err := RunMigrationsOnDB(dbPath, nil, preDir, "primary"); err != nil {
		t.Fatalf("apply migrations up to 288: %v", err)
	}
	db := openMigrationTestDB(t, dbPath)

	// Parent + child rows (asset_versions.asset_id FKs into media_assets).
	if _, err := db.Exec(`INSERT INTO media_assets (id, lifecycle_state, index_state) VALUES ('a1', 'ACTIVE', 'INDEXED')`); err != nil {
		t.Fatalf("seed media_assets: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO asset_versions (asset_id, version_number, source_uri, content_sha256) VALUES ('a1', 1, '', '')`); err != nil {
		t.Fatalf("seed asset_versions: %v", err)
	}

	if err := RunMigrationsOnDB(dbPath, nil, fullDir, "primary"); err != nil {
		t.Fatalf("apply migration 289 over seeded rows: %v", err)
	}

	// The default is the unified one.
	var def string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='media_assets'`).Scan(&def); err != nil {
		t.Fatalf("read media_assets DDL: %v", err)
	}
	if want := "lifecycle_state TEXT NOT NULL DEFAULT 'ACTIVE'"; !strings.Contains(def, want) {
		t.Errorf("media_assets DDL does not declare %q", want)
	}
	if strings.Contains(def, "DEFAULT 'ready'") {
		t.Errorf("media_assets DDL still carries the divergent 'ready' default")
	}

	// Rows survive verbatim.
	var state string
	if err := db.QueryRow(`SELECT lifecycle_state FROM media_assets WHERE id = 'a1'`).Scan(&state); err != nil {
		t.Fatalf("read rebuilt row: %v", err)
	}
	if state != "ACTIVE" {
		t.Errorf("rebuilt row lifecycle_state = %q, want %q", state, "ACTIVE")
	}

	// Child rows survive (the rebuild must not disturb external REFERENCES).
	var children int
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_versions WHERE asset_id = 'a1'`).Scan(&children); err != nil {
		t.Fatalf("read child rows: %v", err)
	}
	if children != 1 {
		t.Errorf("child rows after rebuild = %d, want 1", children)
	}

	// Indexes and triggers survive.
	var count int
	for _, idx := range []string{"idx_media_assets_lifecycle_state", "idx_media_assets_index_state"} {
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, idx).Scan(&count); err != nil {
			t.Fatalf("inspect %s: %v", idx, err)
		}
		if count != 1 {
			t.Errorf("index %s missing after rebuild", idx)
		}
	}
	for _, trg := range []string{
		"trg_media_assets_state_valid_insert",
		"trg_media_assets_state_valid_update",
		"trg_media_assets_asset_state_projection_insert",
		"trg_media_assets_asset_state_projection_update",
		"trg_media_assets_asset_state_projection_guard",
	} {
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name=?`, trg).Scan(&count); err != nil {
			t.Fatalf("inspect %s: %v", trg, err)
		}
		if count != 1 {
			t.Errorf("trigger %s missing after rebuild", trg)
		}
	}

	// The projection trigger still fires, and an insert that OMITS
	// lifecycle_state now takes the canonical default instead of aborting at
	// the state_valid trigger (the old 'ready' default was not in the
	// trigger vocabulary).
	if _, err := db.Exec(`INSERT INTO media_assets (id, index_state) VALUES ('a2', 'INDEXED')`); err != nil {
		t.Fatalf("insert omitting lifecycle_state must use the default, got: %v", err)
	}
	var got, projected string
	if err := db.QueryRow(`SELECT lifecycle_state, asset_state FROM media_assets WHERE id = 'a2'`).Scan(&got, &projected); err != nil {
		t.Fatalf("read defaulted row: %v", err)
	}
	if got != "ACTIVE" {
		t.Errorf("defaulted lifecycle_state = %q, want %q", got, "ACTIVE")
	}
	if projected != "READY" {
		t.Errorf("projected asset_state = %q, want %q (INDEXED + ACTIVE)", projected, "READY")
	}

	// The `-- foreign_keys: off` directive is scoped to its own file: a later
	// migration runs under restored enforcement, so an FK-violating insert
	// must FAIL instead of silently succeeding with the pragma still off.
	probeDir := t.TempDir()
	probe := "-- database: primary\n" +
		"INSERT INTO asset_versions (asset_id, version_number, source_uri, content_sha256) VALUES ('missing-parent', 9, '', '');\n"
	if err := os.WriteFile(filepath.Join(probeDir, "900_fk_probe.sql"), []byte(probe), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RunMigrationsOnDB(dbPath, nil, probeDir, "primary"); err == nil {
		t.Errorf("FK-violating migration after 289 succeeded: foreign_keys enforcement was not restored")
	}
}
