package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestMigrations_226ProductionCopyPreservesLegacyCatalogDataAndIsIdempotent(t *testing.T) {
	targetDir, err := filepath.Abs(migrationsDirFrom)
	if err != nil {
		t.Fatal(err)
	}
	legacyDir := copyMigrationSubset(t, targetDir, 0, map[int]bool{224: true, 225: true})
	fullDir := copyMigrationSubset(t, targetDir, 0, map[int]bool{224: true, 225: true, 226: true})
	dbPath := filepath.Join(t.TempDir(), "production-copy.sqlite")

	if err := RunMigrationsOnDB(dbPath, nil, legacyDir, "primary"); err != nil {
		t.Fatalf("apply legacy schema 224/225: %v", err)
	}

	db := openMigrationTestDB(t, dbPath)
	if _, err := db.Exec(`
		INSERT INTO entity_image_catalog_entities (
			canonical_entity_id, entity_type, canonical_name
		) VALUES ('person:michael-jordan', 'PERSON', 'Michael Jordan')
	`); err != nil {
		db.Close()
		t.Fatalf("seed legacy entity: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO entity_image_catalog_candidates (
			canonical_entity_id, provider, rank, source_url, thumbnail_url,
			width, height, status
		) VALUES
			('person:michael-jordan', 'duckduckgo', 1,
			 'https://images.example/michael-jordan-1.jpg',
			 'https://images.example/thumb-1.jpg', 1920, 1080, 'fresh'),
			('person:michael-jordan', 'duckduckgo', 2,
			 'https://images.example/michael-jordan-2.jpg',
			 'https://images.example/thumb-2.jpg', 1280, 720, 'stale')
	`); err != nil {
		db.Close()
		t.Fatalf("seed legacy candidates: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO entity_image_catalog_materializations (
			candidate_id, asset_id, file_hash, drive_link, local_path,
			status, materialized_at, last_verified_at
		) SELECT candidate_id, 'asset-mj-1', 'sha-mj-1',
			'https://drive.google.com/file/d/asset-mj-1/view',
			'/data/michael-jordan-1.jpg', 'materialized', datetime('now'), datetime('now')
		FROM entity_image_catalog_candidates
		WHERE source_url = 'https://images.example/michael-jordan-1.jpg'
	`); err != nil {
		db.Close()
		t.Fatalf("seed legacy materialization: %v", err)
	}

	var beforeEntities, beforeCandidates, beforeMaterializations int
	for query, destination := range map[string]*int{
		"SELECT COUNT(*) FROM entity_image_catalog_entities":         &beforeEntities,
		"SELECT COUNT(*) FROM entity_image_catalog_candidates":       &beforeCandidates,
		"SELECT COUNT(*) FROM entity_image_catalog_materializations": &beforeMaterializations,
	} {
		if err := db.QueryRow(query).Scan(destination); err != nil {
			db.Close()
			t.Fatalf("snapshot %q: %v", query, err)
		}
	}
	backupPath := filepath.Join(t.TempDir(), "production-copy-backup.sqlite")
	if _, err := db.Exec(`VACUUM INTO ?`, backupPath); err != nil {
		db.Close()
		t.Fatalf("create backup copy: %v", err)
	}
	db.Close()

	if err := RunMigrationsOnDB(dbPath, nil, fullDir, "primary"); err != nil {
		t.Fatalf("apply migration 226 to production-shaped copy: %v", err)
	}
	db = openMigrationTestDB(t, dbPath)
	assertEntityImageCatalog226Schema(t, db)
	assertEntityImageCatalogCounts(t, db, beforeEntities, beforeCandidates, beforeMaterializations)
	assertLegacyEntityImageCatalogRows(t, db)

	if _, err := db.Exec(`
		INSERT INTO entity_image_catalog_candidates (
			canonical_entity_id, provider, rank, source_url, status,
			semantic_status, semantic_score, technical_score, quality_reason
		) VALUES ('person:michael-jordan', 'duckduckgo', 3,
			'https://images.example/michael-jordan-3.jpg', 'fresh',
			'accepted', 1.0, 0.9, 'migration 226 compatibility write')
	`); err != nil {
		db.Close()
		t.Fatalf("insert post-226 candidate: %v", err)
	}
	var postMigrationCandidates int
	if err := db.QueryRow(`SELECT COUNT(*) FROM entity_image_catalog_candidates`).Scan(&postMigrationCandidates); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if postMigrationCandidates != beforeCandidates+1 {
		db.Close()
		t.Fatalf("post-migration candidate count = %d, want %d", postMigrationCandidates, beforeCandidates+1)
	}
	var duplicateCount int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM entity_image_catalog_candidates
		WHERE canonical_entity_id='person:michael-jordan'
		  AND provider='duckduckgo'
		  AND source_url='https://images.example/michael-jordan-1.jpg'
	`).Scan(&duplicateCount); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if duplicateCount != 1 {
		db.Close()
		t.Fatalf("legacy URL uniqueness count = %d, want 1", duplicateCount)
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if integrity != "ok" {
		db.Close()
		t.Fatalf("post-226 integrity = %q, want ok", integrity)
	}
	db.Close()

	if err := RunMigrationsOnDB(dbPath, nil, fullDir, "primary"); err != nil {
		t.Fatalf("reapply migration 226: %v", err)
	}
	db = openMigrationTestDB(t, dbPath)
	var reappliedCandidates int
	if err := db.QueryRow(`SELECT COUNT(*) FROM entity_image_catalog_candidates`).Scan(&reappliedCandidates); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if reappliedCandidates != beforeCandidates+1 {
		db.Close()
		t.Fatalf("reapplied candidate count = %d, want %d", reappliedCandidates, beforeCandidates+1)
	}
	var applied226 int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=226`).Scan(&applied226); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if applied226 != 1 {
		db.Close()
		t.Fatalf("schema_migrations version 226 rows = %d, want 1", applied226)
	}
	db.Close()

	if err := RunMigrationsOnDB(backupPath, nil, fullDir, "primary"); err != nil {
		t.Fatalf("apply migration 226 to backup copy: %v", err)
	}
	backupDB := openMigrationTestDB(t, backupPath)
	defer backupDB.Close()
	assertEntityImageCatalog226Schema(t, backupDB)
	assertEntityImageCatalogCounts(t, backupDB, beforeEntities, beforeCandidates, beforeMaterializations)
	assertLegacyEntityImageCatalogRows(t, backupDB)
}

func assertEntityImageCatalog226Schema(t *testing.T, db *sql.DB) {
	t.Helper()
	columns := scanColumnNames(t, db, "entity_image_catalog_candidates")
	for _, column := range []string{"semantic_status", "semantic_score", "technical_score", "quality_reason"} {
		if _, ok := columns[column]; !ok {
			t.Fatalf("migration 226 missing candidate column %q", column)
		}
	}
	var indexCount int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master
		WHERE type='index' AND name='idx_entity_image_catalog_candidates_quality'
	`).Scan(&indexCount); err != nil {
		t.Fatal(err)
	}
	if indexCount != 1 {
		t.Fatal("migration 226 quality index is missing")
	}
}

func assertEntityImageCatalogCounts(t *testing.T, db *sql.DB, entities, candidates, materializations int) {
	t.Helper()
	checks := []struct {
		query string
		want  int
		name  string
	}{
		{"SELECT COUNT(*) FROM entity_image_catalog_entities", entities, "entities"},
		{"SELECT COUNT(*) FROM entity_image_catalog_candidates", candidates, "candidates"},
		{"SELECT COUNT(*) FROM entity_image_catalog_materializations", materializations, "materializations"},
	}
	for _, check := range checks {
		var got int
		if err := db.QueryRow(check.query).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", check.name, err)
		}
		if got != check.want {
			t.Fatalf("count %s = %d, want %d", check.name, got, check.want)
		}
	}
}

func assertLegacyEntityImageCatalogRows(t *testing.T, db *sql.DB) {
	t.Helper()
	var status, url, driveLink string
	if err := db.QueryRow(`
		SELECT c.status, c.source_url, m.drive_link
		FROM entity_image_catalog_candidates c
		JOIN entity_image_catalog_materializations m ON m.candidate_id = c.candidate_id
		WHERE c.source_url='https://images.example/michael-jordan-1.jpg'
	`).Scan(&status, &url, &driveLink); err != nil {
		t.Fatalf("read preserved legacy candidate: %v", err)
	}
	if status != "fresh" || url != "https://images.example/michael-jordan-1.jpg" || driveLink == "" {
		t.Fatalf("preserved legacy row = status=%q url=%q drive=%q", status, url, driveLink)
	}
}
