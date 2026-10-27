package sqlite

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrations_286_MirrorFieldsContract pins the CONTRACT phase of the
// mirror-field demolition (migration-museum audit point 8):
//
//  1. The residual pre-229 `file_hash` columns are GONE from the six dead
//     tables (their data was absorbed by 229 → 281; 286 re-proves the
//     reconcile guard before dropping).
//  2. `voiceovers.file_hash` and `asset_subtitle_artifacts.file_hash` STAY:
//     those are their own artifact digests with active writers
//     (voiceovers_repository, subtitle_artifact_repository) — a mirror of
//     nothing. This pin is what stops a future cleanup from over-dropping.
//  3. `media_assets.thumb_url` is gone (thumbnail_url is canonical).
//
// The reconcile guard mirrors 281's demolition rule: only a valid 64-hex
// value fills an empty canonical column, never MD5-length/non-hex values,
// and a populated canonical column always wins.
func TestMigrations_286_MirrorFieldsContract(t *testing.T) {
	// ── part 1: full-chain shape pins ────────────────────────────────────
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	for _, table := range []string{
		"media_assets", "asset_index", "asset_locations", "asset_versions",
		"artlist_clips", "entity_image_catalog_materializations",
	} {
		columns := scanColumnNames(t, db, table)
		if _, ok := columns["file_hash"]; ok {
			t.Errorf("%s still carries the dead file_hash column (286 contract)", table)
		}
	}
	for _, table := range []string{"voiceovers", "asset_subtitle_artifacts"} {
		columns := scanColumnNames(t, db, table)
		if _, ok := columns["file_hash"]; !ok {
			t.Errorf("%s lost its LIVE artifact-digest column file_hash — 286 deliberately keeps it", table)
		}
	}
	mediaCols := scanColumnNames(t, db, "media_assets")
	if _, ok := mediaCols["thumb_url"]; ok {
		t.Errorf("media_assets still carries the thumb_url mirror (286 contract)")
	}
	if _, ok := mediaCols["thumbnail_url"]; !ok {
		t.Errorf("media_assets lost the canonical thumbnail_url column")
	}

	// ── part 2: reconcile-then-drop data behaviour (chain up to 286) ─────
	targetDir, err := filepath.Abs(migrationsDirFrom)
	if err != nil {
		t.Fatal(err)
	}
	preDir := copyMigrationSubset(t, targetDir, 285, map[int]bool{0: true})
	fullDir := copyMigrationSubset(t, targetDir, 286, map[int]bool{0: true})
	dbPath := filepath.Join(t.TempDir(), "contract-286.sqlite")
	if err := RunMigrationsOnDB(dbPath, nil, preDir, "primary"); err != nil {
		t.Fatalf("apply migrations up to 285: %v", err)
	}
	db2 := openMigrationTestDB(t, dbPath)

	sha := strings.Repeat("a", 64) // valid SHA-256 → promoted, LOWER()
	shaMixed := strings.Repeat("AB", 32)
	shaLowered := strings.ToLower(shaMixed)
	md5 := strings.Repeat("b", 32)    // MD5 length → NOT promoted
	nonHex := strings.Repeat("z", 64) // 64 chars, not hex → NOT promoted

	if _, err := db2.Exec(`
		INSERT INTO asset_versions (asset_id, version_number, source_uri, content_sha256, file_hash) VALUES
			('v-sha',    1, '', '', ?),
			('v-md5',    1, '', '', ?),
			('v-nonhex', 1, '', '', ?),
			('v-exists', 1, '', ?, ?)
	`, shaMixed, md5, nonHex, sha, sha); err != nil {
		t.Fatalf("seed asset_versions: %v", err)
	}
	if _, err := db2.Exec(`
		INSERT INTO media_assets (id, lifecycle_state, index_state, content_sha256, binary_sha256, file_hash) VALUES
			('m-sha', 'ACTIVE', 'DISCOVERED', '', '', ?),
			('m-md5', 'ACTIVE', 'DISCOVERED', '', '', ?)	`, sha, md5); err != nil {
		t.Fatalf("seed media_assets: %v", err)
	}

	// Now run the contract migration itself over the seeded rows.
	if err := RunMigrationsOnDB(dbPath, nil, fullDir, "primary"); err != nil {
		t.Fatalf("apply migration 286 over seeded rows: %v", err)
	}

	type row struct {
		content, binary string
	}
	checks := []struct {
		query   string
		want    row
		message string
	}{
		{`SELECT COALESCE(content_sha256,''), '' FROM asset_versions WHERE asset_id='v-sha'`, row{shaLowered, ""}, "64-hex mixed-case file_hash must be promoted LOWER() into content_sha256"},
		{`SELECT COALESCE(content_sha256,''), '' FROM asset_versions WHERE asset_id='v-md5'`, row{"", ""}, "MD5-length values must NOT be promoted"},
		{`SELECT COALESCE(content_sha256,''), '' FROM asset_versions WHERE asset_id='v-nonhex'`, row{"", ""}, "non-hex values must NOT be promoted"},
		{`SELECT COALESCE(content_sha256,''), '' FROM asset_versions WHERE asset_id='v-exists'`, row{sha, ""}, "a populated canonical column must win over the legacy bucket"},
		{`SELECT COALESCE(content_sha256,''), COALESCE(binary_sha256,'') FROM media_assets WHERE id='m-sha'`, row{sha, sha}, "media_assets promote fills content_sha256 + the binary_sha256 fill-if-empty projection"},
		{`SELECT COALESCE(content_sha256,''), COALESCE(binary_sha256,'') FROM media_assets WHERE id='m-md5'`, row{"", ""}, "media_assets must not fabricate a digest from an MD5-length value"},
	}
	for _, c := range checks {
		var got row
		if err := db2.QueryRow(c.query).Scan(&got.content, &got.binary); err != nil {
			t.Fatalf("%s: %v", c.query, err)
		}
		if got != c.want {
			t.Errorf("%s: got (%q,%q), want (%q,%q)", c.message, got.content, got.binary, c.want.content, c.want.binary)
		}
	}

	// The columns are gone after the contract migration.
	for _, table := range []string{"media_assets", "asset_versions"} {
		cols := scanColumnNames(t, db2, table)
		if _, ok := cols["file_hash"]; ok {
			t.Errorf("post-286 %s still has file_hash", table)
		}
	}
}
