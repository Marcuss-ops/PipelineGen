package sqlite

// DB-03 transition (2026-11-27): migrations 307 (observability) and 308 (cache)
// drop the business schema that the 148 unscoped migrations had replicated into
// the operational planes. The drop is only half the contract — the other half is
// that verifyDeclaredTables stays silent, because those tables are declared by
// many applied migrations (the baseline, and 302 for asset_subtitle_artifacts).
//
// This test pins both: a fresh operational plane carries exactly its own tables,
// and the declared-table/index verifier reports no gap for it.

import (
	"context"
	"database/sql"
	"path/filepath"
	"sort"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap/zaptest"
)

// Mirror of cmd/admin/internal/database/plane_allowlist.go. Duplicated on
// purpose: the verifier must fail here, in the migration suite, when the schema
// grows a table the runtime allowlist does not own, not only in `db doctor`.
var (
	observabilityOwnedTables = []string{
		"admin_mutation_audit",
		"api_requests",
		"job_attempts",
		"monitor_enqueue_outbox",
		"outbox_events",
		"replay_bundles",
		"resource_observations",
		"run_artifact_checkpoints",
		"run_artifact_observations",
		"run_child_observations",
		"run_observability",
		"run_operation_observations",
		"run_report_snapshots",
		"run_resource_aggregates",
		"run_resource_reports",
		"run_resource_samples",
		"run_stage_observations",
		"schema_migrations",
	}
	cacheOwnedTables = []string{
		"admin_mutation_audit",
		"artifact_cache_entries",
		"artifact_cache_metrics",
		"media_query_cache",
		"render_attempt_analytics",
		"research_cache",
		"schema_migrations",
		"semantic_plan_cache",
		"stock_source_cache",
		"transcript_cache",
		"translation_cache",
		"vidrush_provider_cache",
	}
)

func TestMigrations_307_308_OperationalPlanesCarryOnlyTheirOwnTables(t *testing.T) {
	migrationsDir, err := filepath.Abs("../../../migrations/sqlite")
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}

	for _, tc := range []struct {
		plane string
		owned []string
	}{
		{plane: "observability", owned: observabilityOwnedTables},
		{plane: "cache", owned: cacheOwnedTables},
	} {
		tc := tc
		t.Run(tc.plane, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), tc.plane+".sqlite")
			if err := RunMigrationsOnDB(dbPath, zaptest.NewLogger(t), migrationsDir, tc.plane); err != nil {
				t.Fatalf("RunMigrationsOnDB(%s): %v", tc.plane, err)
			}
			db, err := sql.Open("sqlite3", dbPath)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer func() { _ = db.Close() }()

			live, err := liveTableNames(db)
			if err != nil {
				t.Fatalf("live tables: %v", err)
			}
			var got []string
			for name := range live {
				if name == "sqlite_sequence" || name == "sqlite_stat1" {
					continue
				}
				got = append(got, name)
			}
			want := append([]string(nil), tc.owned...)
			sort.Strings(got)
			sort.Strings(want)
			if len(got) != len(want) {
				t.Fatalf("%s schema after 307/308 = %v, want exactly %v", tc.plane, got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("%s schema after 307/308 = %v, want exactly %v", tc.plane, got, want)
				}
			}

			// The drop must not create a gap in the declared-object contract:
			// the corpus says these tables are gone on purpose.
			migrations, err := discoverMigrations(migrationsDir)
			if err != nil {
				t.Fatalf("discover migrations: %v", err)
			}
			applied, err := loadAppliedMigrations(db)
			if err != nil {
				t.Fatalf("load applied: %v", err)
			}
			if gaps := missingDeclaredTables(migrations, applied, live, tc.plane); len(gaps) != 0 {
				t.Fatalf("%s must report no declared-table gap after 307/308, got %v", tc.plane, gaps)
			}
			owners, err := liveIndexOwners(db)
			if err != nil {
				t.Fatalf("live index owners: %v", err)
			}
			if gaps := missingDeclaredIndexes(migrations, applied, live, owners, tc.plane); len(gaps) != 0 {
				t.Fatalf("%s must report no declared-index gap after 307/308, got %v", tc.plane, gaps)
			}

			violations, err := ForeignKeyCheck(context.Background(), db)
			if err != nil {
				t.Fatalf("foreign_key_check: %v", err)
			}
			if len(violations) != 0 {
				t.Fatalf("%s must have no FK violations after the drop, got %v", tc.plane, violations)
			}

			// `admin db migrate`'s post-apply check uses this status: a plane
			// the runner just migrated must have nothing left to apply.
			status, err := GetMigrationStatusForPlane(db, migrationsDir, tc.plane)
			if err != nil {
				t.Fatalf("migration status: %v", err)
			}
			if status.PendingN != 0 {
				t.Fatalf("%s must report no pending migration after the runner, got %+v", tc.plane, status.Pending)
			}
		})
	}
}
