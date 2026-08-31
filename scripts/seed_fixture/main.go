// seed_fixture is a one-shot helper that reapplies all migrations against an
// isolated SQLite database. It never writes the operational database by
// default. Set SEED_DB_PATH only when deliberately rebuilding a checked-in
// fixture under tests/fixtures; otherwise a private temporary database is
// created and removed automatically.
//
// Run from the refactored project root:
//
//	go run ./scripts/seed_fixture
//
// Optional explicit fixture path:
//
//	SEED_DB_PATH=tests/fixtures/sqlite/media.db.sqlite go run ./scripts/seed_fixture
package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
)

// canonicalMissingTables are the tables the storage tests expect to exist
// after migrations have been applied to the isolated fixture database.
var canonicalMissingTables = []string{
	"asset_links",
	"harvester_jobs",
	"pipeline_runs",
	"pipeline_run_items",
	"script_stock_matches",
	"video_stats_history",
	"segment_embeddings",
	"sketchfab_models",
	"transcript_cache",
	"script_versions",
}

func main() {
	logger, err := zap.NewDevelopment()
	if err != nil {
		fmt.Fprintf(os.Stderr, "logger init: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	projectRoot, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: resolve project root: %v\n", err)
		os.Exit(1)
	}
	if _, err := os.Stat(filepath.Join(projectRoot, "go.mod")); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: must be run from project root (go.mod not found): %v\n", err)
		os.Exit(1)
	}

	dbPath, cleanup, err := resolveSeedDB(projectRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
		os.Exit(1)
	}
	defer cleanup()

	migrationsDir := filepath.Join(projectRoot, "migrations", "sqlite")
	if err := clearAndReapply(dbPath, migrationsDir, logger); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
		os.Exit(1)
	}
	if err := assertTablesPresent(dbPath, canonicalMissingTables); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: post-seed verification: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("OK: isolated seed database %s; all %d canonical tables present\n", dbPath, len(canonicalMissingTables))
}

// resolveSeedDB returns a temporary DB path unless SEED_DB_PATH explicitly
// names a fixture under tests/fixtures. The explicit-path restriction prevents
// this test utility from silently becoming a production-data migration tool.
func resolveSeedDB(projectRoot string) (string, func(), error) {
	if configured := strings.TrimSpace(os.Getenv("SEED_DB_PATH")); configured != "" {
		configuredAbs, err := filepath.Abs(configured)
		if err != nil {
			return "", func() {}, fmt.Errorf("resolve SEED_DB_PATH %q: %w", configured, err)
		}
		fixturesRoot, err := filepath.Abs(filepath.Join(projectRoot, "tests", "fixtures"))
		if err != nil {
			return "", func() {}, fmt.Errorf("resolve fixtures root: %w", err)
		}
		if configuredAbs != fixturesRoot && !strings.HasPrefix(configuredAbs, fixturesRoot+string(os.PathSeparator)) {
			return "", func() {}, fmt.Errorf("SEED_DB_PATH must be inside %s; refusing to open user or operational data", fixturesRoot)
		}
		if err := os.MkdirAll(filepath.Dir(configuredAbs), 0o755); err != nil {
			return "", func() {}, fmt.Errorf("create fixture directory: %w", err)
		}
		return configuredAbs, func() {}, nil
	}

	tmpDir, err := os.MkdirTemp("", "pipelinegen-seed-")
	if err != nil {
		return "", func() {}, fmt.Errorf("create temporary seed directory: %w", err)
	}
	path := filepath.Join(tmpDir, "media.db.sqlite")
	return path, func() { _ = os.RemoveAll(tmpDir) }, nil
}

// clearAndReapply wipes schema_migrations for the isolated database, then
// runs RunMigrationsOnDB to apply every primary migration in order.
func clearAndReapply(dbPath, migrationsDir string, logger *zap.Logger) error {
	db, err := storage.OpenSQLiteDB(dbPath, logger)
	if err != nil {
		return fmt.Errorf("open %s: %w", dbPath, err)
	}
	defer db.Close()

	if _, err := db.Exec("DELETE FROM schema_migrations"); err != nil {
		logger.Warn("DELETE FROM schema_migrations failed (probably first seed)", zap.Error(err))
	}
	logger.Info("cleared schema_migrations; reapplying all migrations")

	if err := storage.RunMigrationsOnDB(dbPath, logger, migrationsDir, "primary"); err != nil {
		return fmt.Errorf("RunMigrationsOnDB: %w", err)
	}

	// A few historical test expectations refer to tables whose complete
	// migrations live outside migrations/sqlite. Keep these minimal stubs
	// confined to the isolated seed database; never apply them to runtime data.
	if err := bootstrapUnmigratedTables(db.DB, logger); err != nil {
		return fmt.Errorf("bootstrap unmigrated tables: %w", err)
	}
	return nil
}

func bootstrapUnmigratedTables(db *sql.DB, logger *zap.Logger) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS harvester_jobs (id TEXT PRIMARY KEY)`,
		`CREATE TABLE IF NOT EXISTS pipeline_runs (id TEXT PRIMARY KEY)`,
		`CREATE TABLE IF NOT EXISTS pipeline_run_items (id TEXT PRIMARY KEY)`,
		`CREATE TABLE IF NOT EXISTS segment_embeddings (id TEXT PRIMARY KEY)`,
		`CREATE TABLE IF NOT EXISTS sketchfab_models (id TEXT PRIMARY KEY)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec %q: %w", stmt, err)
		}
		logger.Info("bootstrapped isolated test table", zap.String("stmt", stmt))
	}
	return nil
}

func assertTablesPresent(dbPath string, expected []string) error {
	db, err := storage.OpenSQLiteDB(dbPath, nil)
	if err != nil {
		return fmt.Errorf("open %s: %w", dbPath, err)
	}
	defer db.Close()

	actual := make(map[string]bool, len(expected))
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table'`)
	if err != nil {
		return fmt.Errorf("query sqlite_master: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("scan table name: %w", err)
		}
		actual[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate table names: %w", err)
	}

	var missing []string
	for _, table := range expected {
		if !actual[table] {
			missing = append(missing, table)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing tables after seed: %v", missing)
	}
	return nil
}
