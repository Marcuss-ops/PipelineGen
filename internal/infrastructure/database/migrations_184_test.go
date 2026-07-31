// Package storage — migrations_184_test.go covers the durable process phase
// metrics migration and its primary-database scope.
package storage

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestMigrations_184_ProcessPhaseMetrics(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	var applied int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM schema_migrations WHERE version = 184`,
	).Scan(&applied); err != nil {
		t.Fatalf("check migration 184 ledger entry: %v", err)
	}
	if applied != 1 {
		t.Fatalf("migration 184 ledger entry count = %d, want 1", applied)
	}

	columns := scanColumnNames(t, db, "process_phase_metrics")
	for _, name := range []string{
		"id", "process_type", "job_id", "parent_job_id", "phase",
		"language", "provider", "started_at", "duration_ms", "queue_wait_ms",
		"status", "error_code", "items_in", "items_out", "bytes_in",
		"bytes_out", "retry_count", "created_at",
	} {
		if _, ok := columns[name]; !ok {
			t.Errorf("process_phase_metrics is missing column %q", name)
		}
	}

	for _, indexName := range []string{
		"idx_process_phase_metrics_job",
		"idx_process_phase_metrics_type_phase",
		"idx_process_phase_metrics_parent_job",
	} {
		var count int
		err := db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`,
			indexName,
		).Scan(&count)
		if err != nil {
			t.Fatalf("check index %q: %v", indexName, err)
		}
		if count != 1 {
			t.Errorf("index %q count = %d, want 1", indexName, count)
		}
	}

	var tableSQL sql.NullString
	if err := db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'process_phase_metrics'`,
	).Scan(&tableSQL); err != nil {
		t.Fatalf("read process_phase_metrics sqlite_master DDL: %v", err)
	}
	if !tableSQL.Valid || tableSQL.String == "" {
		t.Fatal("process_phase_metrics sqlite_master DDL is empty")
	}
}

func TestMigrations_184_ProcessPhaseMetrics_IdempotentDDL(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	if _, err := db.Exec(`
		INSERT INTO process_phase_metrics (
			process_type, job_id, phase, started_at, status, created_at
		) VALUES ('stock', 'migration-184-job', 'stock.plan',
			'2026-07-31T12:00:00Z', 'success', '2026-07-31T12:00:00Z')
	`); err != nil {
		t.Fatalf("insert metric before idempotent apply: %v", err)
	}

	migrationPath := filepath.Join(migrationsDirFrom, "184_create_process_phase_metrics.sql")
	migration, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatalf("read migration 184 %s: %v", migrationPath, err)
	}
	if _, err := db.Exec(string(migration)); err != nil {
		t.Fatalf("replay migration 184 DDL: %v", err)
	}

	var count int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM process_phase_metrics WHERE job_id = 'migration-184-job'`,
	).Scan(&count); err != nil {
		t.Fatalf("count metric after idempotent apply: %v", err)
	}
	if count != 1 {
		t.Fatalf("metric row count after idempotent apply = %d, want 1", count)
	}
}
