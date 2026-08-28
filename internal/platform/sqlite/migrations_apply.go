package sqlite

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"go.uber.org/zap"
)

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
	startedAt := time.Now()
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
	durationMS := time.Since(startedAt).Milliseconds()
	if _, err := tx.Exec(
		"INSERT INTO schema_migrations (version, migration_id, filename, checksum, checksum_sha256, applied_at, duration_ms, app_git_sha) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		version, version, filename, checksum, checksum, now, durationMS, currentAppGitSHA(),
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
	return digest.SHA256Bytes(data)
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
