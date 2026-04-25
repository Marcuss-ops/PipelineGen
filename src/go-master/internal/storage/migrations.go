package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// MigrationRunner handles SQLite database migrations.
type MigrationRunner struct {
	db             *sql.DB
	log            *zap.Logger
	migrationsDir  string
}

// NewMigrationRunner creates a new migration runner.
func NewMigrationRunner(db *sql.DB, log *zap.Logger, migrationsDir string) *MigrationRunner {
	return &MigrationRunner{
		db:            db,
		log:           log,
		migrationsDir: migrationsDir,
	}
}

// EnsureSchemaMigrationsTable creates the schema_migrations table if it doesn't exist.
func (mr *MigrationRunner) EnsureSchemaMigrationsTable() error {
	_, err := mr.db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create schema_migrations table: %w", err)
	}
	return nil
}

// GetAppliedMigrations returns a map of already applied migration versions.
func (mr *MigrationRunner) GetAppliedMigrations() (map[string]bool, error) {
	rows, err := mr.db.Query("SELECT version FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("failed to query applied migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("failed to scan migration version: %w", err)
		}
		applied[version] = true
	}
	return applied, nil
}

// RunMigrations applies all pending migrations.
func (mr *MigrationRunner) RunMigrations() error {
	if err := mr.EnsureSchemaMigrationsTable(); err != nil {
		return err
	}

	applied, err := mr.GetAppliedMigrations()
	if err != nil {
		return err
	}

	migrationFiles, err := mr.getMigrationFiles()
	if err != nil {
		return err
	}

	sort.Strings(migrationFiles)

	for _, file := range migrationFiles {
		version := strings.TrimSuffix(filepath.Base(file), ".sql")

		if applied[version] {
			mr.log.Debug("Migration already applied, skipping", zap.String("version", version))
			continue
		}

		mr.log.Info("Applying migration", zap.String("version", version))

		content, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %w", file, err)
		}

		if err := mr.applyMigration(version, string(content)); err != nil {
			return fmt.Errorf("failed to apply migration %s: %w", version, err)
		}
	}

	return nil
}

// getMigrationFiles returns all SQL migration files in the migrations directory.
func (mr *MigrationRunner) getMigrationFiles() ([]string, error) {
	entries, err := os.ReadDir(mr.migrationsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read migrations directory: %w", err)
	}

	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			files = append(files, filepath.Join(mr.migrationsDir, entry.Name()))
		}
	}
	return files, nil
}

// applyMigration applies a single migration within a transaction.
func (mr *MigrationRunner) applyMigration(version, sqlContent string) error {
	tx, err := mr.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	statements := splitSQLStatements(sqlContent)
	for _, stmt := range statements {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("failed to execute statement: %w\nStatement: %s", err, stmt)
		}
	}

	if _, err := tx.Exec("INSERT INTO schema_migrations (version) VALUES (?)", version); err != nil {
		return fmt.Errorf("failed to record migration: %w", err)
	}

	return tx.Commit()
}

// splitSQLStatements splits SQL content into individual statements.
func splitSQLStatements(sqlContent string) []string {
	var statements []string
	var current strings.Builder

	for _, line := range strings.Split(sqlContent, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		current.WriteString(line)
		current.WriteString("\n")
		if strings.HasSuffix(strings.TrimSpace(trimmed), ";") {
			statements = append(statements, current.String())
			current.Reset()
		}
	}

	if current.Len() > 0 {
		statements = append(statements, current.String())
	}

	return statements
}

// RunMigrationsOnDB is a convenience function to run migrations on a database.
func RunMigrationsOnDB(dbPath string, log *zap.Logger, migrationsDir string) error {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer db.Close()

	runner := NewMigrationRunner(db, log, migrationsDir)
	return runner.RunMigrations()
}
