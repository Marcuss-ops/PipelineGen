package sqlite

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
)

// ensureMigrationLedger creates the canonical ledger and expands the legacy
// four-column shape in place without dropping applied history. The complete
// expand/backfill/validate operation is transactional so a crash cannot leave
// a partially certified ledger visible to the migration runner.
func ensureMigrationLedger(db queryable) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin schema_migrations expansion: %w", err)
	}
	defer tx.Rollback()
	if err := ensureMigrationLedgerTx(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema_migrations expansion: %w", err)
	}
	return nil
}

func ensureMigrationLedgerTx(db ledgerQueryable) error {
	if _, err := db.Exec(schemaMigrationsTable); err != nil {
		return err
	}
	columns, err := schemaMigrationColumns(db)
	if err != nil {
		return err
	}
	additions := []struct {
		name string
		ddl  string
	}{
		{"migration_id", "ALTER TABLE schema_migrations ADD COLUMN migration_id INTEGER"},
		{"checksum_sha256", "ALTER TABLE schema_migrations ADD COLUMN checksum_sha256 TEXT"},
		{"duration_ms", "ALTER TABLE schema_migrations ADD COLUMN duration_ms INTEGER NOT NULL DEFAULT 0"},
		{"app_git_sha", "ALTER TABLE schema_migrations ADD COLUMN app_git_sha TEXT NOT NULL DEFAULT 'unknown'"},
	}
	for _, addition := range additions {
		if columns[addition.name] {
			continue
		}
		if _, err := db.Exec(addition.ddl); err != nil {
			return fmt.Errorf("add schema_migrations.%s: %w", addition.name, err)
		}
	}
	if _, err := db.Exec("UPDATE schema_migrations SET migration_id = version WHERE migration_id IS NULL"); err != nil {
		return fmt.Errorf("backfill schema_migrations.migration_id: %w", err)
	}
	if _, err := db.Exec("UPDATE schema_migrations SET checksum_sha256 = checksum WHERE COALESCE(checksum_sha256, '') = ''"); err != nil {
		return fmt.Errorf("backfill schema_migrations.checksum_sha256: %w", err)
	}
	checksumRows, err := db.Query("SELECT version, checksum, checksum_sha256 FROM schema_migrations")
	if err != nil {
		return fmt.Errorf("validate schema_migrations checksums: %w", err)
	}
	for checksumRows.Next() {
		var version int
		var checksum, checksumSHA string
		if err := checksumRows.Scan(&version, &checksum, &checksumSHA); err != nil {
			checksumRows.Close()
			return fmt.Errorf("scan schema_migrations checksums: %w", err)
		}
		if checksum != checksumSHA || !isSHA256Hex(checksumSHA) {
			checksumRows.Close()
			return fmt.Errorf("schema_migrations checksum is not canonical: version=%d checksum=%q checksum_sha256=%q", version, checksum, checksumSHA)
		}
	}
	if err := checksumRows.Err(); err != nil {
		checksumRows.Close()
		return fmt.Errorf("iterate schema_migrations checksums: %w", err)
	}
	checksumRows.Close()
	rows, err := db.Query("SELECT version, migration_id FROM schema_migrations WHERE migration_id <> version")
	if err != nil {
		return fmt.Errorf("validate schema_migrations.migration_id: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		var version, migrationID int
		if err := rows.Scan(&version, &migrationID); err != nil {
			return fmt.Errorf("scan schema_migrations.migration_id: %w", err)
		}
		return fmt.Errorf("schema_migrations migration_id mismatch: version=%d migration_id=%d", version, migrationID)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate schema_migrations.migration_id: %w", err)
	}
	if _, err := db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS ux_schema_migrations_migration_id ON schema_migrations(migration_id)"); err != nil {
		return fmt.Errorf("index schema_migrations.migration_id: %w", err)
	}
	return nil
}

func isSHA256Hex(value string) bool {
	if len(value) != sha256HexLength {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

const sha256HexLength = 64

type ledgerQueryable interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
}

func schemaMigrationColumns(db ledgerQueryable) (map[string]bool, error) {
	rows, err := db.Query("PRAGMA table_info(schema_migrations)")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

// loadAppliedMigrations returns a map of version → appliedRecord for all
// migrations previously recorded in schema_migrations. Returns an empty
// map if the table doesn't exist yet (fresh database).
func loadAppliedMigrations(db queryable) (map[int]appliedRecord, error) {
	applied := make(map[int]appliedRecord)
	rows, err := db.Query("SELECT version, filename, COALESCE(NULLIF(checksum_sha256, ''), checksum) FROM schema_migrations ORDER BY version ASC")
	if err != nil {
		// Distinguish "table doesn't exist yet" from real errors
		if isMissingTableError(err) {
			return applied, nil
		}
		return nil, fmt.Errorf("query schema_migrations: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var v int
		var filename, checksum string
		if err := rows.Scan(&v, &filename, &checksum); err != nil {
			return nil, fmt.Errorf("scan schema_migrations row: %w", err)
		}
		applied[v] = appliedRecord{filename: filename, checksum: checksum}
	}
	return applied, rows.Err()
}

// isMissingTableError returns true if the error indicates the queried table
// does not exist (SQLite "no such table" error).
func isMissingTableError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no such table") &&
		strings.Contains(msg, "schema_migrations")
}
