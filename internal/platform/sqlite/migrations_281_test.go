package sqlite

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrations_281_LegacyFileMD5ContractScenario pins the CONTRACT phase of
// the mixed 32/64-hex bucket demolition (godlike/06, one owner per fact):
//
//  1. RECONCILE — a valid 64-hex legacy value fills an empty canonical column
//     (media_assets.content_sha256 + its binary_sha256 fill-if-empty
//     projection, asset_index.content_hash, metadata_json.content_hash).
//     MD5-length and non-hex values are NEVER promoted — the digest is never
//     fabricated. An already-populated canonical column always wins.
//  2. FORCED REMOVAL — catalog rows whose digest is not a valid SHA-256 are
//     deleted (operator-approved 2026-10-24); non-catalog rows survive but
//     lose the non-reconcilable value.
//  3. RENAME — tables without a canonical content column take
//     `content_sha256` as the honest name of the surviving values.
//  4. DROP — media_assets / asset_index lose the legacy column entirely.
//
// This test is the home of the guard contract that the demolished runtime
// `BackfillContentSHA256` used to own (64-hex guard, MD5 skip, no overwrite).
func TestMigrations_281_LegacyFileMD5ContractScenario(t *testing.T) {
	targetDir, err := filepath.Abs(migrationsDirFrom)
	if err != nil {
		t.Fatal(err)
	}
	legacyDir := copyMigrationSubset(t, targetDir, 280, map[int]bool{0: true})
	fullDir := copyMigrationSubset(t, targetDir, 282, map[int]bool{0: true})
	dbPath := filepath.Join(t.TempDir(), "contract-281.sqlite")

	if err := RunMigrationsOnDB(dbPath, nil, legacyDir, "primary"); err != nil {
		t.Fatalf("apply migrations up to 280: %v", err)
	}
	db := openMigrationTestDB(t, dbPath)

	sha := strings.Repeat("a", 64)          // valid 64-hex SHA-256
	shaMixed := strings.Repeat("AB", 32)    // valid hex, mixed case → LOWER() on promote
	shaLowered := strings.ToLower(shaMixed) // expected promoted form
	md5 := strings.Repeat("b", 32)          // MD5 length, must NOT pass the guard
	nonHex := strings.Repeat("z", 64)       // 64 chars but not hex
	otherSHA := strings.Repeat("c", 64)

	// ── seed media_assets (columns + metadata plane) ──────────────────────
	if _, err := db.Exec(`
		INSERT INTO media_assets (id, lifecycle_state, index_state, content_sha256, binary_sha256, legacy_file_md5, metadata_json) VALUES
			('a-sha',      'ACTIVE', 'DISCOVERED', '',  '',  ?, '{}'),
			('a-md5',      'ACTIVE', 'DISCOVERED', '',  '',  ?, '{}'),
			('a-nonhex',   'ACTIVE', 'DISCOVERED', '',  '',  ?, '{}'),
			('a-existing', 'ACTIVE', 'DISCOVERED', ?,   ?,   ?, '{}'),
			('a-binfill',  'ACTIVE', 'DISCOVERED', '',  ?,   ?, '{}'),
			('a-meta',     'ACTIVE', 'DISCOVERED', '',  '',  '', '{"legacy_file_md5":"`+sha+`"}'),
			('a-meta2',    'ACTIVE', 'DISCOVERED', '',  '',  '', '{"file_hash":"`+otherSHA+`"}'),
			('a-meta3',    'ACTIVE', 'DISCOVERED', '',  '',  '', '{"legacy_file_md5":"`+md5+`"}'),
			('a-meta4',    'ACTIVE', 'DISCOVERED', '',  '',  '', '{"legacy_file_md5":"`+sha+`","content_hash":"`+otherSHA+`"}')
	`, shaMixed, md5, nonHex, otherSHA, otherSHA, sha, otherSHA, sha); err != nil {
		_ = db.Close()
		t.Fatalf("seed media_assets: %v", err)
	}

	// ── seed asset_index ─────────────────────────────────────────────────
	if _, err := db.Exec(`
		INSERT INTO asset_index (asset_id, asset_type, source, source_id, content_hash, legacy_file_md5) VALUES
			('idx-sha', 'clip', 'test', '1', '', ?),
			('idx-md5', 'clip', 'test', '2', '', ?)
	`, shaMixed, md5); err != nil {
		_ = db.Close()
		t.Fatalf("seed asset_index: %v", err)
	}

	// ── seed entity-image catalog (forced-removal surface) ───────────────
	if _, err := db.Exec(`
		INSERT INTO entity_image_catalog_entities (
			canonical_entity_id, entity_type, canonical_name
		) VALUES ('person:test', 'PERSON', 'Test Person');
		INSERT INTO entity_image_catalog_candidates (
			canonical_entity_id, provider, rank, source_url, thumbnail_url,
			width, height, status, legacy_file_md5
		) VALUES
			('person:test', 'duckduckgo', 1, 'https://img/keep-sha',   'https://img/t1', 1920, 1080, 'fresh', ?),
			('person:test', 'duckduckgo', 2, 'https://img/drop-md5',  'https://img/t2', 1280, 720,  'fresh', ?),
			('person:test', 'duckduckgo', 3, 'https://img/keep-empty', 'https://img/t3', 640, 480,  'fresh', '');
		INSERT INTO entity_image_catalog_materializations (
			candidate_id, asset_id, file_hash, drive_link,
			status, materialized_at, last_verified_at, legacy_file_md5
		) SELECT candidate_id, 'asset-keep-sha', '', 'https://drive/keep-sha',
			'materialized', datetime('now'), datetime('now'), ?
		FROM entity_image_catalog_candidates WHERE source_url = 'https://img/keep-sha';
		INSERT INTO entity_image_catalog_materializations (
			candidate_id, asset_id, file_hash, drive_link,
			status, materialized_at, last_verified_at, legacy_file_md5
		) SELECT candidate_id, 'asset-drop-md5', '', 'https://drive/drop-md5',
			'materialized', datetime('now'), datetime('now'), ?
		FROM entity_image_catalog_candidates WHERE source_url = 'https://img/drop-md5';
		INSERT INTO entity_image_catalog_materializations (
			candidate_id, asset_id, file_hash, drive_link,
			status, materialized_at, last_verified_at, legacy_file_md5
		) SELECT candidate_id, 'asset-drop-empty', '', 'https://drive/drop-empty',
			'materialized', datetime('now'), datetime('now'), ''
		FROM entity_image_catalog_candidates WHERE source_url = 'https://img/keep-empty';
	`, sha, md5, sha, md5); err != nil {
		_ = db.Close()
		t.Fatalf("seed entity-image catalog: %v", err)
	}

	// ── seed non-catalog rows (value-clear + rename surface) ─────────────
	if _, err := db.Exec(`
		INSERT INTO asset_locations (asset_id, location_kind, uri, legacy_file_md5) VALUES
			('a-sha', 'local', '/tmp/keep-sha', ?),
			('a-md5', 'local', '/tmp/drop-md5', ?)
	`, shaMixed, md5); err != nil {
		_ = db.Close()
		t.Fatalf("seed asset_locations: %v", err)
	}
	_ = db.Close()

	// ── apply 281 (contract phase) ───────────────────────────────────────
	if err := RunMigrationsOnDB(dbPath, nil, fullDir, "primary"); err != nil {
		t.Fatalf("apply migration 281: %v", err)
	}
	db = openMigrationTestDB(t, dbPath)
	defer func() { _ = db.Close() }()

	// 1. RECONCILE — media_assets, incl. the binary_sha256 fill-if-empty
	// projection that supersedes the demolished BackfillContentSHA256.
	for _, check := range []struct {
		id          string
		wantContent string
		wantBinary  string
	}{
		{"a-sha", shaLowered, shaLowered},  // promoted + projected
		{"a-md5", "", ""},                  // MD5 never promoted
		{"a-nonhex", "", ""},               // non-hex never promoted
		{"a-existing", otherSHA, otherSHA}, // canonical wins, never overwritten
		{"a-binfill", sha, otherSHA},       // content promoted; binary already set stays
		{"a-meta", "", ""},                 // metadata plane only
	} {
		var content, binary string
		if err := db.QueryRow(
			`SELECT content_sha256, binary_sha256 FROM media_assets WHERE id = ?`, check.id,
		).Scan(&content, &binary); err != nil {
			t.Fatalf("read %s: %v", check.id, err)
		}
		if content != check.wantContent || binary != check.wantBinary {
			t.Errorf("%s content_sha256/binary_sha256 = %q/%q, want %q/%q",
				check.id, content, binary, check.wantContent, check.wantBinary)
		}
	}

	// asset_index.content_hash follows the same guard.
	for _, check := range []struct{ id, want string }{
		{"idx-sha", shaLowered},
		{"idx-md5", ""},
	} {
		var got string
		if err := db.QueryRow(
			`SELECT COALESCE(content_hash, '') FROM asset_index WHERE asset_id = ?`, check.id,
		).Scan(&got); err != nil {
			t.Fatalf("read %s: %v", check.id, err)
		}
		if got != check.want {
			t.Errorf("%s content_hash = %q, want %q", check.id, got, check.want)
		}
	}

	// Metadata plane: both historical aliases promoted when valid and
	// absent, removed unconditionally; the canonical key always wins.
	for _, check := range []struct{ id, wantContentHash string }{
		{"a-meta", sha},
		{"a-meta2", otherSHA},
		{"a-meta3", ""},
		{"a-meta4", otherSHA},
	} {
		var meta string
		if err := db.QueryRow(
			`SELECT metadata_json FROM media_assets WHERE id = ?`, check.id,
		).Scan(&meta); err != nil {
			t.Fatalf("read metadata %s: %v", check.id, err)
		}
		for _, banned := range []string{"legacy_file_md5", "file_hash"} {
			if strings.Contains(meta, `"`+banned+`"`) {
				t.Errorf("%s metadata still carries demolished key %q: %s", check.id, banned, meta)
			}
		}
		want := `"content_hash":"` + check.wantContentHash + `"`
		if check.wantContentHash != "" && !strings.Contains(meta, want) {
			t.Errorf("%s metadata %s, want %s", check.id, meta, want)
		}
		if check.wantContentHash == "" && strings.Contains(meta, `"content_hash"`) {
			t.Errorf("%s metadata %s, want no content_hash (invalid digest never promoted)", check.id, meta)
		}
	}

	// 2. FORCED REMOVAL — catalog rows without a valid SHA-256 digest are
	// gone; every other row survives.
	for _, check := range []struct {
		query string
		want  int
		name  string
	}{
		{`SELECT COUNT(*) FROM entity_image_catalog_candidates`, 2, "candidates surviving"},
		{`SELECT COUNT(*) FROM entity_image_catalog_candidates WHERE source_url = 'https://img/drop-md5'`, 0, "md5 candidate removed"},
		{`SELECT COUNT(*) FROM entity_image_catalog_materializations WHERE asset_id = 'asset-keep-sha'`, 1, "sha materialization kept"},
		{`SELECT COUNT(*) FROM entity_image_catalog_materializations WHERE asset_id = 'asset-drop-md5'`, 0, "md5 materialization removed"},
		{`SELECT COUNT(*) FROM entity_image_catalog_materializations WHERE asset_id = 'asset-drop-empty'`, 0, "empty-digest materialization removed"},
	} {
		var got int
		if err := db.QueryRow(check.query).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", check.name, err)
		}
		if got != check.want {
			t.Errorf("%s = %d, want %d", check.name, got, check.want)
		}
	}

	// 3. RENAME + value removal on non-catalog rows: valid values survive
	// verbatim under the honest name, non-reconcilable values are gone.
	for _, check := range []struct{ uri, want string }{
		{"/tmp/keep-sha", shaMixed},
		{"/tmp/drop-md5", ""},
	} {
		var got string
		if err := db.QueryRow(
			`SELECT content_sha256 FROM asset_locations WHERE uri = ?`, check.uri,
		).Scan(&got); err != nil {
			t.Fatalf("read %s: %v", check.uri, err)
		}
		if got != check.want {
			t.Errorf("asset_locations %s content_sha256 = %q, want %q", check.uri, got, check.want)
		}
	}

	// 4. DROP / RENAME — final column set: no legacy_file_md5 anywhere, and
	// the renamed tables carry content_sha256 as the honest name.
	assertLegacyFileMD5ColumnsGone(t, db)
	for _, table := range []string{
		"asset_locations", "asset_versions", "asset_subtitle_artifacts",
		"artlist_clips", "entity_image_catalog_candidates",
		"entity_image_catalog_materializations",
	} {
		cols := scanColumnNames(t, db, table)
		if _, ok := cols["content_sha256"]; !ok {
			t.Errorf("%s missing content_sha256 after the 281 rename", table)
		}
	}
}

// TestMigrations_281_FinalSchemaOnFreshDB asserts the post-281 column set on
// a freshly migrated primary database: the bucket columns are gone from the
// two tables that had a canonical home, and the rename targets exist.
func TestMigrations_281_FinalSchemaOnFreshDB(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()
	assertLegacyFileMD5ColumnsGone(t, db)
}

func assertLegacyFileMD5ColumnsGone(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{
		"media_assets", "asset_index", "asset_locations", "asset_versions",
		"asset_subtitle_artifacts", "artlist_clips",
		"entity_image_catalog_candidates", "entity_image_catalog_materializations",
	} {
		cols := scanColumnNames(t, db, table)
		if _, ok := cols["legacy_file_md5"]; ok {
			t.Errorf("%s still carries the demolished legacy_file_md5 column", table)
		}
	}
}
