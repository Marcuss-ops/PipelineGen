package sqlite

import (
	"path/filepath"
	"testing"
)

// DB-02 regression: after migration 302 the subtitle artifact table must not
// declare FKs against the SQLite parent tables (they are empty after the
// media PostgreSQL cutover), and must still accept the canonical artifact
// shape. The opaque ids are validated by the repository, not by SQLite.
func TestMigrations_302_SubtitleArtifactsCrossPlane(t *testing.T) {
	targetDir, err := filepath.Abs(migrationsDirFrom)
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "media-302.sqlite")
	if err := RunMigrationsOnDB(dbPath, nil, targetDir, "primary"); err != nil {
		t.Fatalf("apply primary migrations through 302: %v", err)
	}
	db := openMigrationTestDB(t, dbPath)
	defer func() { _ = db.Close() }()

	var fkCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_foreign_key_list('asset_subtitle_artifacts')`,
	).Scan(&fkCount); err != nil {
		t.Fatalf("inspect foreign keys: %v", err)
	}
	if fkCount != 0 {
		t.Fatalf("asset_subtitle_artifacts still declares %d FK(s); cross-plane refs must be opaque", fkCount)
	}

	// The parent tables are deliberately absent/empty in this fixture: the
	// insert must still succeed, as it does on the SQLite media plane.
	if _, err := db.Exec(`
		INSERT INTO asset_subtitle_artifacts (
			asset_id, text_track_id, language_code, format, local_path,
			file_hash, text_hash, cues_hash, clip_content_hash, cue_count,
			clip_duration_ms, last_cue_end_ms, style_version, generator_version, status
		) VALUES (
			'asset-from-postgres', 3809, 'it', 'ass', '/tmp/a.ass',
			'file', 'text', 'cues', 'clip', 2,
			14000, 13700, 'v1', 'g1', 'READY'
		)`); err != nil {
		t.Fatalf("insert cross-plane artifact: %v", err)
	}

	for _, index := range []string{
		"idx_current_clip_ass",
		"idx_subtitle_artifacts_drive_file_unique",
	} {
		var count int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, index,
		).Scan(&count); err != nil {
			t.Fatalf("inspect index %s: %v", index, err)
		}
		if count != 1 {
			t.Errorf("migration 302 lost index %s", index)
		}
	}

	rows, err := ForeignKeyCheck(t.Context(), db)
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("fresh primary DB has FK violations after 302: %v", rows)
	}
}
