package storage

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

// schemaMigrationsTable is the DDL for the migration tracking table.
const schemaMigrationsTable = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version     INTEGER PRIMARY KEY,
    filename    TEXT    NOT NULL,
    checksum    TEXT    NOT NULL,
    applied_at  TEXT    NOT NULL DEFAULT (datetime('now'))
)`

type migrationFile struct {
	version  int
	filename string
	path     string
}

// RunMigrations scans targetDir for .sql files, compares their versions
// against the schema_migrations ledger, and applies any pending migrations
// in version order. Each migration file is applied inside its own
// transaction. Applied migrations are recorded with their version, filename,
// and SHA-256 checksum.
//
// Migrations are never modified or renamed after being applied — the
// runner rejects files whose version is already in the ledger but whose
// checksum differs from the recorded one.
func (s *SQLiteDB) RunMigrations(log *zap.Logger, targetDir string) error {
	if log == nil {
		log = s.log
		if log == nil {
			log = zap.NewNop()
		}
	}
	return migrateAll(s.DB, log, targetDir)
}

// RunMigrationsOnDB is a convenience wrapper that opens the database at
// dbPath, runs migrations, and closes the database. Useful for one-shot
// scripts (e.g. seed_fixture).
func RunMigrationsOnDB(dbPath string, log *zap.Logger, targetDir string) error {
	if log == nil {
		log = zap.NewNop()
	}
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return fmt.Errorf("storage: open %s: %w", dbPath, err)
	}
	defer db.Close()

	return migrateAll(db, log, targetDir)
}

// migrateAll is the shared migration logic used by both RunMigrations
// and RunMigrationsOnDB.
func migrateAll(db queryable, log *zap.Logger, targetDir string) error {
	// Ensure ledger table exists
	if _, err := db.Exec(schemaMigrationsTable); err != nil {
		return fmt.Errorf("storage: create schema_migrations: %w", err)
	}

	// Discover migration files
	migrations, err := discoverMigrations(targetDir)
	if err != nil {
		return err
	}

	// Validate: no duplicate versions
	if err := validateNoDuplicateVersions(migrations, log); err != nil {
		return err
	}

	// Validate: log gaps as warnings (not errors) since real migration dirs
	// may have gaps from historical renumbering / removed migrations.
	// Gaps are informational; the runner proceeds normally.
	warnOnGaps(migrations, log)

	// Load already-applied migrations for checksum validation
	applied, err := loadAppliedMigrations(db)
	if err != nil {
		return fmt.Errorf("storage: load applied migrations: %w", err)
	}

	// Apply pending migrations
	appliedCount := 0
	for _, m := range migrations {
		content, err := os.ReadFile(m.path)
		if err != nil {
			return fmt.Errorf("storage: read %s: %w", m.filename, err)
		}
		checksum := sha256Hex(content)

		if prev, ok := applied[m.version]; ok {
			if prev.checksum != checksum {
				return fmt.Errorf(
					"storage: migration %03d (%s) checksum mismatch — "+
						"applied=%s current=%s. Migrations must never be modified after being applied",
					m.version, m.filename, prev.checksum, checksum,
				)
			}
			// Already applied with matching checksum — skip
			continue
		}

		// Apply migration inside its own transaction
		log.Info("applying migration", zap.Int("version", m.version), zap.String("filename", m.filename))

		if err := applyMigration(db, log, m.version, m.filename, checksum, content); err != nil {
			return fmt.Errorf("storage: apply %s: %w", m.filename, err)
		}
		appliedCount++
	}

	log.Info("migrations complete",
		zap.Int("total", len(migrations)),
		zap.Int("newly_applied", appliedCount),
		zap.Int("already_applied", len(applied)),
	)
	return nil
}

// queryable abstracts the database handle so migrateAll works with both
// *sql.DB (from RunMigrationsOnDB) and *SQLiteDB (from RunMigrations).
type queryable interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	Begin() (*sql.Tx, error)
}

// appliedRecord holds a row from the schema_migrations ledger.
type appliedRecord struct {
	checksum string
	filename string
}

// applyMigration executes a single migration file inside a transaction
// and records its application in schema_migrations.
//
// Migration files are split into individual statements and each is executed
// in turn. SQLite does not support `ALTER TABLE ADD COLUMN IF NOT EXISTS`,
// so we soft-skip any `ALTER TABLE … ADD COLUMN` that fails with
// "duplicate column name": the column was already created by a prior
// migration (e.g. 015 was retrofitted to add columns that 017 also adds).
// The migration is still recorded as fully applied. The check is scoped to
// ALTER TABLE … ADD COLUMN specifically so other DDL with the same error
// string (e.g. a CREATE TABLE with a duplicated column in its column list)
// is still hard-errored as a real bug.
func applyMigration(db queryable, log *zap.Logger, version int, filename, checksum string, content []byte) error {
	if log == nil {
		log = zap.NewNop()
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	statements := splitSQLStatements(string(content))
	for i, stmt := range statements {
		trimmed := strings.TrimSpace(stmt)
		if trimmed == "" {
			continue
		}
		// Pre-flight skip: standalone BEGIN/BEGIN TRANSACTION/BEGIN IMMEDIATE/
		// COMMIT/ROLLBACK appear inside migration files the author wrote
		// expecting to need explicit tx boundaries. Within the outer Go-level
		// `tx` from `db.Begin()` these statements are no-ops, but more
		// importantly SQLite treats a stray `COMMIT` as terminating the
		// underlying transaction — which leaves Go's `*sql.Tx` open against a
		// closed SQLite handle. The eventual `tx.Commit()` then errors with
		// "cannot commit - no transaction is active".  Skipping before
		// exec() guarantees the driver never sees the lifecycle command,
		// fixes fresh DBs, and — because the SQL files are unchanged —
		// preserves the SHA-256 ledger invariant on existing databases.
		//
		// This is the ONLY `isNestedTransactionControl` check in the runner:
		// the previous post-error check inside `if err != nil` was deleted
		// because it relied on the driver erroring (which SQLite doesn't do
		// for `COMMIT` inside an active tx — it silently closes the handle).
		if isNestedTransactionControl(trimmed) {
			log.Info("skipping nested transaction control (handled by outer runner tx)",
				zap.Int("version", version),
				zap.String("filename", filename),
				zap.Int("statement", i+1),
			)
			continue
		}
		if _, err := tx.Exec(trimmed); err != nil {
			if isDuplicateColumnError(err.Error(), trimmed) {
				// Soft-skip: ALTER TABLE … ADD COLUMN collided with a column
				// that already exists from a prior migration (e.g. 015 was
				// retrofitted to add columns that 017 also adds). This makes
				// migrations idempotent against retrofitted column lists.
				log.Info("skipping duplicate ADD COLUMN (already exists from prior migration)",
					zap.Int("version", version),
					zap.String("filename", filename),
					zap.Int("statement", i+1),
				)
				continue
			}
			if isConditionalInsertOnMissingTable(err.Error(), trimmed) {
				// Soft-skip: conditional data-migration INSERT that gated on
				// `WHERE EXISTS (SELECT 1 FROM sqlite_master WHERE type='table'
				// AND name='<tbl>')` failed because SQLite's query planner
				// resolves the FROM-table at planning time even when the
				// EXISTS gate would have produced zero rows at runtime.
				// The migration author intended the INSERT to no-op when the
				// source table was absent; this runtime workaround fulfils
				// that intent without modifying the migration file (which
				// would break the SHA-256 ledger invariant).
				log.Info("skipping conditional INSERT onto missing source table",
					zap.Int("version", version),
					zap.String("filename", filename),
					zap.Int("statement", i+1),
					zap.Error(err),
				)
				continue
			}
			return fmt.Errorf("%s: statement %d: %w", filename, i+1, err)
		}
	}

	// Record in ledger
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.Exec(
		"INSERT INTO schema_migrations (version, filename, checksum, applied_at) VALUES (?, ?, ?, ?)",
		version, filename, checksum, now,
	); err != nil {
		return fmt.Errorf("record migration %d: %w", version, err)
	}

	return tx.Commit()
}

// parseMigrationVersion extracts the integer version prefix from a
// migration filename. Expected format: NNN_<descriptive>.sql
// e.g. "001_velox_core.sql" → 1.
func parseMigrationVersion(filename string) (int, error) {
	name := filepath.Base(filename)
	idx := strings.Index(name, "_")
	if idx <= 0 || idx > 3 {
		return 0, fmt.Errorf("invalid migration filename: %s (expected NNN_*.sql)", name)
	}
	version, err := strconv.Atoi(name[:idx])
	if err != nil || version <= 0 {
		return 0, fmt.Errorf("invalid migration version in %s: %w", name, err)
	}
	return version, nil
}

// sha256Hex returns the hex-encoded SHA-256 hash of the input bytes.
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// MigrateStatus represents the status of a single migration file.
type MigrateStatus struct {
	Version  int
	Filename string
	Applied  bool
	Checksum string
}

// MigrateStatusReport holds the result of a migration status check.
type MigrateStatusReport struct {
	Applied  []MigrateStatus
	Pending  []MigrateStatus
	Total    int
	AppliedN int
	PendingN int
}
