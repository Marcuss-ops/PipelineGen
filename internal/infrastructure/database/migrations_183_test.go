// Package storage — migrations_183_test.go covers the lifecycle shadow
// reconciliation migration.
//
// lifecycle_state remains the operational SSOT. lifecycle_status is only a
// compatibility/shadow column and must be repaired from lifecycle_state.
package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrations_183_ReconcileLifecycleStatusShadow(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	var applied int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM schema_migrations WHERE version = 183`,
	).Scan(&applied); err != nil {
		t.Fatalf("check migration 183 ledger entry: %v", err)
	}
	if applied != 1 {
		t.Fatalf("migration 183 ledger entry count = %d, want 1", applied)
	}

	const (
		activeID  = "lifecycle-shadow-active"
		deletedID = "lifecycle-shadow-deleted"
	)
	_, err := db.Exec(`
		INSERT INTO media_assets (
			id, source, name, lifecycle_state, lifecycle_status
		) VALUES
			(?, 'youtube', 'active asset', 'ACTIVE', 'ACTIVE'),
			(?, 'youtube', 'deleted asset', 'DELETED', 'ACTIVE')
	`, activeID, deletedID)
	if err != nil {
		t.Fatalf("insert lifecycle shadow fixtures: %v", err)
	}

	migrationPath := filepath.Join(migrationsDirFrom, "183_reconcile_lifecycle_status_shadow.sql")
	migration, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatalf("read migration 183 %s: %v", migrationPath, err)
	}
	if _, err := db.Exec(string(migration)); err != nil {
		t.Fatalf("execute migration 183 reconciliation: %v", err)
	}

	rows, err := db.Query(`
		SELECT lifecycle_state, lifecycle_status
		FROM media_assets
		WHERE id IN (?, ?)
		ORDER BY id
	`, activeID, deletedID)
	if err != nil {
		t.Fatalf("read reconciled lifecycle rows: %v", err)
	}
	defer rows.Close()

	got := make(map[string]string, 2)
	for rows.Next() {
		var state, status string
		if err := rows.Scan(&state, &status); err != nil {
			t.Fatalf("scan reconciled lifecycle row: %v", err)
		}
		got[state] = status
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate reconciled lifecycle rows: %v", err)
	}
	if got["ACTIVE"] != "ACTIVE" {
		t.Errorf("ACTIVE lifecycle_status = %q, want ACTIVE", got["ACTIVE"])
	}
	if got["DELETED"] != "DELETED" {
		t.Errorf("DELETED lifecycle_status = %q, want DELETED", got["DELETED"])
	}

	var state string
	if err := db.QueryRow(
		`SELECT lifecycle_state FROM media_assets WHERE id = ?`, deletedID,
	).Scan(&state); err != nil {
		t.Fatalf("read canonical lifecycle_state: %v", err)
	}
	if state != "DELETED" {
		t.Fatalf("migration changed lifecycle_state to %q; lifecycle_state must remain the operational SSOT", state)
	}
}
