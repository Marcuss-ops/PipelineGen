package storage

import "testing"

func TestMigrations_204_JobRegistryExecutionLedger(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	for _, table := range []string{
		"job_steps",
		"job_registry_metrics",
		"job_registry_events",
		"job_asset_relations",
	} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			t.Fatalf("table %s lookup: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("table %s is missing", table)
		}
	}

	jobCols := scanColumnNames(t, db, "jobs")
	for _, want := range []string{"git_sha", "app_version"} {
		if _, ok := jobCols[want]; !ok {
			t.Fatalf("jobs missing runtime identity column %q (present: %v)", want, jobCols)
		}
	}

	for _, want := range []string{
		"idx_job_steps_job_created",
		"idx_job_registry_metrics_job",
		"idx_job_registry_events_job_created",
		"idx_job_asset_relations_asset",
	} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, want).Scan(&count); err != nil {
			t.Fatalf("index %s lookup: %v", want, err)
		}
		if count != 1 {
			t.Fatalf("index %s is missing", want)
		}
	}
}
