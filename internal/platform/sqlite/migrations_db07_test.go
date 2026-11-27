package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// DB-07: the index-hygiene migrations must leave one canonical duplicate and
// add the missing FK indexes on each plane that owns the tables.
func TestMigrations_DB07_IndexHygiene(t *testing.T) {
	targetDir, err := filepath.Abs(migrationsDirFrom)
	if err != nil {
		t.Fatal(err)
	}

	assertIndex := func(t *testing.T, db *sql.DB, name string, want bool) {
		t.Helper()
		var count int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, name,
		).Scan(&count); err != nil {
			t.Fatalf("inspect index %s: %v", name, err)
		}
		if got := count == 1; got != want {
			t.Errorf("index %s exists=%v, want %v", name, got, want)
		}
	}

	t.Run("primary", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "primary-db07.sqlite")
		if err := RunMigrationsOnDB(dbPath, nil, targetDir, "primary"); err != nil {
			t.Fatalf("apply primary migrations: %v", err)
		}
		db := openMigrationTestDB(t, dbPath)
		defer func() { _ = db.Close() }()

		assertIndex(t, db, "idx_media_assets_lifecycle", false)
		assertIndex(t, db, "idx_media_assets_lifecycle_state", true)
		for _, index := range []string{
			"idx_fk_video_stats_history_video_id",
			"idx_fk_script_sections_script_id",
			"idx_fk_workflow_step_dependencies_depends_on_step_id",
			"idx_fk_asset_subtitle_artifacts_text_track_id",
			"idx_fk_asset_links_asset_id",
		} {
			assertIndex(t, db, index, true)
		}
	})

	t.Run("jobs", func(t *testing.T) {
		jobsDir, err := filepath.Abs("../../../migrations/sqlite_jobs")
		if err != nil {
			t.Fatal(err)
		}
		dbPath := filepath.Join(t.TempDir(), "jobs-db07.sqlite")
		if err := RunMigrationsOnDB(dbPath, nil, jobsDir, "jobs"); err != nil {
			t.Fatalf("apply jobs migrations: %v", err)
		}
		db := openMigrationTestDB(t, dbPath)
		defer func() { _ = db.Close() }()

		for _, index := range []string{
			"idx_fk_job_payloads_job_id",
			"idx_fk_job_registry_metrics_step_id",
		} {
			assertIndex(t, db, index, true)
		}
	})

	t.Run("observability", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "obs-db07.sqlite")
		if err := RunMigrationsOnDB(dbPath, nil, targetDir, "observability"); err != nil {
			t.Fatalf("apply observability migrations: %v", err)
		}
		db := openMigrationTestDB(t, dbPath)
		defer func() { _ = db.Close() }()

		for _, index := range []string{
			"idx_fk_run_observability_attempt_id",
			"idx_fk_run_child_observations_parent_run_id",
			"idx_fk_run_artifact_checkpoints_run_id",
		} {
			assertIndex(t, db, index, true)
		}
	})
}
