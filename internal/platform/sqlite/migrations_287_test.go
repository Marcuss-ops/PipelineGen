package sqlite

import (
	"path/filepath"
	"testing"
)

// TestMigrations_287_RenderAttemptRunID pins the explicit run identity on
// render_attempt_analytics (migration 287): the column, the index, and the
// BACKFILL derivation over pre-287 rows. The derivation must stay in lockstep
// with scriptgeneration.RenderRunID — write path and historical backfill
// produce identical values.
func TestMigrations_287_RenderAttemptRunID(t *testing.T) {
	targetDir, err := filepath.Abs(migrationsDirFrom)
	if err != nil {
		t.Fatal(err)
	}
	preDir := copyMigrationSubset(t, targetDir, 286, map[int]bool{0: true})
	fullDir := copyMigrationSubset(t, targetDir, 287, map[int]bool{0: true})
	dbPath := filepath.Join(t.TempDir(), "run-id-287.sqlite")
	if err := RunMigrationsOnDB(dbPath, nil, preDir, "primary"); err != nil {
		t.Fatalf("apply migrations up to 286: %v", err)
	}
	db := openMigrationTestDB(t, dbPath)

	// Historical rows (pre-287 shape): the run identity is embedded in job_id.
	seed := []struct {
		attemptID string
		jobID     string
		wantRunID string
	}{
		{"a1", "run_9405664a-c61f-4c75-9e06-bc43ff1272f5:item:005:phrase", "run_9405664a-c61f-4c75-9e06-bc43ff1272f5"},
		{"a2", "run_ea669c55-f519-4930-be1b-9687e688eadf:item:000:batch-5813a9be", "run_ea669c55-f519-4930-be1b-9687e688eadf"},
		{"a3", "run_1788527686642826949_3182094a7b5e", "run_1788527686642826949_3182094a7b5e"},
		{"a4", "trump-final-render", ""},
	}
	for _, row := range seed {
		if _, err := db.Exec(
			`INSERT INTO render_attempt_analytics (attempt_id, job_id, recorded_at) VALUES (?, ?, datetime('now'))`,
			row.attemptID, row.jobID,
		); err != nil {
			t.Fatalf("seed attempt %s: %v", row.attemptID, err)
		}
	}

	// The 287 backfill runs over exactly these historical rows.
	if err := RunMigrationsOnDB(dbPath, nil, fullDir, "primary"); err != nil {
		t.Fatalf("apply migration 287 over seeded rows: %v", err)
	}

	for _, row := range seed {
		var got string
		if err := db.QueryRow(
			`SELECT run_id FROM render_attempt_analytics WHERE attempt_id = ?`, row.attemptID,
		).Scan(&got); err != nil {
			t.Fatalf("read attempt %s: %v", row.attemptID, err)
		}
		if got != row.wantRunID {
			t.Errorf("attempt %s (job_id=%q): run_id = %q, want %q", row.attemptID, row.jobID, got, row.wantRunID)
		}
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('render_attempt_analytics') WHERE name = 'run_id'`).Scan(&count); err != nil {
		t.Fatalf("inspect render_attempt_analytics.run_id: %v", err)
	}
	if count != 1 {
		t.Errorf("migration 287 did not add render_attempt_analytics.run_id")
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_render_attempt_analytics_run'`).Scan(&count); err != nil {
		t.Fatalf("inspect idx_render_attempt_analytics_run: %v", err)
	}
	if count != 1 {
		t.Errorf("migration 287 did not create idx_render_attempt_analytics_run")
	}
}
