// Package storage — migrations_185_test.go covers the process metric details extension.
package storage

import "testing"

func TestMigrations_185_ProcessPhaseMetricsDetails(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	columns := scanColumnNames(t, db, "process_phase_metrics")
	if _, ok := columns["details_json"]; !ok {
		t.Fatal("process_phase_metrics is missing details_json from migration 185")
	}

	var applied int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM schema_migrations WHERE version = 185`,
	).Scan(&applied); err != nil {
		t.Fatalf("check migration 185 ledger entry: %v", err)
	}
	if applied != 1 {
		t.Fatalf("migration 185 ledger entry count = %d, want 1", applied)
	}

	if _, err := db.Exec(`
		INSERT INTO process_phase_metrics (
			process_type, job_id, phase, started_at, status, created_at, details_json
		) VALUES ('stock', 'migration-185-job', 'stock.download',
			'2026-07-31T12:00:00Z', 'success', '2026-07-31T12:00:00Z',
			'{"download_bytes":1234}')
	`); err != nil {
		t.Fatalf("insert details_json metric: %v", err)
	}
}
