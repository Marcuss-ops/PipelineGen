package sqlite

// DB-03 fase 2 (2026-11-27): the consolidated baseline used to be a single
// `all` dump, so a fresh observability/cache database was built with the
// business schema and had it dropped immediately by 307/308. The corpus now
// carries one plane-pure dump per operational plane, and the runner must pick
// the plane's own dump over the frozen shared one — otherwise the shared dump
// sorts first, is applied, and the plane dump is skipped.

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap/zaptest"
)

func TestPlaneBaseline_SelectPrefersPlaneDump(t *testing.T) {
	shared := migrationFile{version: 0, filename: "000_baseline_268.sql", scope: "all", baselineCovers: 298}
	obs := migrationFile{version: 0, filename: "000_baseline_268_observability.sql", scope: "observability", baselineCovers: 308}
	cache := migrationFile{version: 0, filename: "000_baseline_268_cache.sql", scope: "cache", baselineCovers: 308}
	corpus := []migrationFile{shared, cache, obs}

	selected, ok := selectBaselineForPlane(corpus, "observability")
	if !ok || selected.filename != obs.filename {
		t.Fatalf("observability baseline = %q (ok=%v), want %s", selected.filename, ok, obs.filename)
	}
	selected, ok = selectBaselineForPlane(corpus, "cache")
	if !ok || selected.filename != cache.filename {
		t.Fatalf("cache baseline = %q (ok=%v), want %s", selected.filename, ok, cache.filename)
	}
	// Primary has no plane dump: the frozen shared dump still bootstraps it.
	selected, ok = selectBaselineForPlane(corpus, "primary")
	if !ok || selected.filename != shared.filename {
		t.Fatalf("primary baseline = %q (ok=%v), want %s", selected.filename, ok, shared.filename)
	}
	if got := baselineThresholdForMigrations(corpus, "observability"); got != 308 {
		t.Fatalf("observability threshold = %d, want 308", got)
	}
	if got := baselineThresholdForMigrations(corpus, "primary"); got != 298 {
		t.Fatalf("primary threshold = %d, want 298", got)
	}
}

func TestPlaneBaseline_ValidateRejectsAmbiguousScope(t *testing.T) {
	shared := migrationFile{version: 0, filename: "000_baseline_268.sql", scope: "all"}
	first := migrationFile{version: 0, filename: "000_baseline_268_cache.sql", scope: "cache"}
	second := migrationFile{version: 0, filename: "000_baseline_269_cache.sql", scope: "cache"}
	if err := validateBaselineScopes([]migrationFile{shared, first, second}); err == nil {
		t.Fatal("two cache baselines must be rejected")
	}
	if err := validateBaselineScopes([]migrationFile{shared, first}); err != nil {
		t.Fatalf("one shared + one plane baseline is valid, got %v", err)
	}
	twin := migrationFile{version: 0, filename: "000_baseline_269.sql", scope: "all"}
	if err := validateBaselineScopes([]migrationFile{shared, twin}); err == nil {
		t.Fatal("two shared baselines must be rejected")
	}
}

func TestPlaneBaseline_FreshPlaneBootstrapsFromItsOwnDump(t *testing.T) {
	migrationsDir, err := filepath.Abs("../../../migrations/sqlite")
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}
	for _, tc := range []struct {
		plane    string
		filename string
	}{
		{plane: "observability", filename: "000_baseline_268_observability.sql"},
		{plane: "cache", filename: "000_baseline_268_cache.sql"},
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

			var applied string
			if err := db.QueryRow(`SELECT filename FROM schema_migrations WHERE version = 0`).Scan(&applied); err != nil {
				t.Fatalf("read baseline ledger row: %v", err)
			}
			if applied != tc.filename {
				t.Fatalf("%s bootstrapped from %q, want its own dump %q", tc.plane, applied, tc.filename)
			}

			// The window the dump covers must be fully satisfied: a fresh plane
			// has no individual rows for 1..N and must not report them pending.
			status, err := GetMigrationStatusForPlane(db, migrationsDir, tc.plane)
			if err != nil {
				t.Fatalf("migration status: %v", err)
			}
			if status.PendingN != 0 {
				t.Fatalf("%s reports pending migrations after its plane dump: %+v", tc.plane, status.Pending)
			}
		})
	}
}
