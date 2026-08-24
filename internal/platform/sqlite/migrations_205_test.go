package sqlite

import "testing"

func TestMigrations_205_JobAssetStepLineage(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	cols := scanColumnNames(t, db, "job_asset_relations")
	if _, ok := cols["step_id"]; !ok {
		t.Fatalf("job_asset_relations missing step_id column: %v", cols)
	}

	var pkColumns []string
	rows, err := db.Query(`SELECT name FROM pragma_table_info('job_asset_relations') WHERE pk > 0 ORDER BY pk`)
	if err != nil {
		t.Fatalf("read job_asset_relations primary key: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		pkColumns = append(pkColumns, name)
	}
	want := []string{"job_id", "asset_id", "relation", "step_id"}
	if len(pkColumns) != len(want) {
		t.Fatalf("primary key = %v, want %v", pkColumns, want)
	}
	for i := range want {
		if pkColumns[i] != want[i] {
			t.Fatalf("primary key = %v, want %v", pkColumns, want)
		}
	}

	if _, err := db.Exec(`INSERT INTO jobs (id, type, status, created_at, updated_at) VALUES ('job-205', 'test', 'QUEUED', '2026-08-12T00:00:00Z', '2026-08-12T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	for _, stepID := range []string{"step-a", "step-b"} {
		if _, err := db.Exec(`INSERT INTO job_asset_relations (job_id, asset_id, relation, step_id, ordinal, created_at) VALUES ('job-205', 'asset-205', 'GENERATED', ?, 0, '2026-08-12T00:00:00Z')`, stepID); err != nil {
			t.Fatalf("insert step-aware edge %s: %v", stepID, err)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM job_asset_relations WHERE job_id='job-205' AND asset_id='asset-205'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("step-aware edges = %d, want 2", count)
	}
}
