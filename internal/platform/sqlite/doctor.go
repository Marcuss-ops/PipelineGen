// Package storage — doctor.go (June 2026 codex/db-doctor-restore):
//
// Read-only diagnostic helper functions consumed by the admin CLI
// `admin db {status,check,backup,restore --verify}` subcommands.
// Each function operates on a *sql.DB handle so callers pass the
// DatabaseSet-opened handle directly without recompiling pragmas.
//
// ALL helper functions live in `internal/infrastructure/database/`
// (the only package allowed to import `database/sql` per Check 17).
// Callers in `cmd/admin/` invoke these helpers; they never touch
// `database/sql` themselves. This is the canonical pattern for the
// doctor subsystem.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"
)

// IntegrityCheck runs `PRAGMA integrity_check` returning the status
// string. `ok` means a clean DB; any other value means corruption.
// Returns nil on `ok`, error otherwise. The runtime integrity_check
// in DatabaseSet.Health uses PRAGMA quick_check for cheap polling;
// this thorough variant is for `db check` and post-restore verification.
func IntegrityCheck(ctx context.Context, db *sql.DB) error {
	var status string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&status); err != nil {
		return fmt.Errorf("integrity_check: %w", err)
	}
	if status != "ok" {
		return fmt.Errorf("integrity_check returned %q (not ok)", status)
	}
	return nil
}

// ForeignKeyCheck returns the list of FK violations reported by
// `PRAGMA foreign_key_check`. An empty slice means clean. Each
// violation is reported as "table[rowid] -> parent.fk".
//
// Schema-level FK mismatches (e.g. a foreign key referencing a
// non-existent column in the parent table) are surfaced as warnings
// in the violations slice rather than hard errors, because they
// indicate a schema inconsistency (not actual data corruption) and
// `db check` is a diagnostic tool that should report these without
// failing the entire run.
func ForeignKeyCheck(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		// SQLite returns "foreign key mismatch" at prepare time when
		// a FK constraint references a column that doesn't exist in
		// the parent table (schema-level mismatch, not row-level
		// violation). Return it as a warning rather than a hard
		// failure so `db check` can report the schema issue without
		// aborting the entire diagnostic run.
		if strings.Contains(err.Error(), "foreign key mismatch") {
			return []string{fmt.Sprintf("WARNING: schema-level FK mismatch: %v", err)}, nil
		}
		return nil, fmt.Errorf("foreign_key_check: %w", err)
	}
	defer rows.Close()

	var violations []string
	for rows.Next() {
		var table string
		var rowid int64
		var parent, fk string
		if err := rows.Scan(&table, &rowid, &parent, &fk); err != nil {
			return nil, fmt.Errorf("foreign_key_check scan: %w", err)
		}
		violations = append(violations, fmt.Sprintf("%s[id=%d] -> %s.%s", table, rowid, parent, fk))
	}
	return violations, rows.Err()
}

// JournalMode returns `PRAGMA journal_mode`. Expected: "wal".
func JournalMode(ctx context.Context, db *sql.DB) (string, error) {
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return "", fmt.Errorf("journal_mode: %w", err)
	}
	return mode, nil
}

// BusyTimeoutMs returns `PRAGMA busy_timeout`. Expected: 5000.
func BusyTimeoutMs(ctx context.Context, db *sql.DB) (int, error) {
	var ms int
	if err := db.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&ms); err != nil {
		return 0, fmt.Errorf("busy_timeout: %w", err)
	}
	return ms, nil
}

// WalSizeBytes returns the size of the -wal companion file to dbPath.
// Returns 0 if the WAL file is absent (e.g. journal_mode=DELETE).
func WalSizeBytes(dbPath string) (int64, error) {
	fi, err := os.Stat(dbPath + "-wal")
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	return fi.Size(), nil
}

// ShmSizeBytes returns the size of the -shm companion file to dbPath.
// Returns 0 if absent.
func ShmSizeBytes(dbPath string) (int64, error) {
	fi, err := os.Stat(dbPath + "-shm")
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	return fi.Size(), nil
}

// TableCounts runs SELECT COUNT(*) FROM <table> for each table name.
// Missing tables report 0 (not an error). The caller determines the
// table list (e.g. read from sqlite_master).
func TableCounts(ctx context.Context, db *sql.DB, tables []string) (map[string]int, error) {
	counts := make(map[string]int, len(tables))
	for _, t := range tables {
		var n int
		q := fmt.Sprintf("SELECT COUNT(*) FROM %q", t)
		if err := db.QueryRowContext(ctx, q).Scan(&n); err != nil {
			counts[t] = 0
			continue
		}
		counts[t] = n
	}
	return counts, nil
}

// AllUserTables returns the list of application tables (excludes
// sqlite-internal tables like sqlite_sequence, schema_migrations).
// Used by `db check` to enumerate table counts.
func AllUserTables(ctx context.Context, db *sql.DB) ([]string, error) {
	q := `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan table name: %w", err)
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// CriticalColumns is the canonical list of (table, column) pairs the
// runtime queries routinely. `db check` verifies each pair exists.
var CriticalColumns = []CriticalColumn{
	{"media_assets", "id"},
	{"media_assets", "drive_file_id"},
	{"media_assets", "lifecycle_state"},
	{"jobs", "id"},
	{"jobs", "status"},
	{"schema_migrations", "version"},
	{"schema_migrations", "checksum"},
	{"schema_migrations", "applied_at"},
	{"api_requests", "id"},
	{"api_requests", "ts"},
	{"api_requests", "request_id"},
	{"api_requests", "status"},
}

// CriticalColumn is a (table, column) tuple used by CriticalColumns.
type CriticalColumn struct {
	Table  string
	Column string
}

// ColumnExists returns whether (table, column) exists in the schema.
// Uses `pragma_table_info` which is the canonical sqlite-side check.
func ColumnExists(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	q := fmt.Sprintf("SELECT 1 FROM pragma_table_info(%q) WHERE name=%q", table, column)
	var x int
	err := db.QueryRowContext(ctx, q).Scan(&x)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("pragma_table_info(%s, %s): %w", table, column, err)
	}
	return true, nil
}

// IndexCount returns the total number of index records (incl. implicit
// rowid primary keys). Equivalent to SELECT COUNT(*) FROM
// sqlite_master WHERE type='index'.
func IndexCount(ctx context.Context, db *sql.DB) (int, error) {
	var n int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='index'").Scan(&n); err != nil {
		return 0, fmt.Errorf("count indexes: %w", err)
	}
	return n, nil
}

// DBSizeBytes returns the size of the main DB file on disk. Does NOT
// include WAL or SHM companion files; callers that need total disk
// usage add WalSizeBytes + ShmSizeBytes.
func DBSizeBytes(dbPath string) (int64, error) {
	fi, err := os.Stat(dbPath)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// OpenReadOnly opens a read-only *sql.DB for diagnostic commands.
// This is the ONLY place where diagnostic code opens a fresh sqlite
// handle — it's intentionally inside `internal/infrastructure/database/`
// so the api/app/domain gate (Check 17) is never violated. Callers
// in cmd/admin/ use this helper, never sql.Open directly.
func OpenReadOnly(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", path+"?mode=ro&_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open read-only %s: %w", path, err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping read-only %s: %w", path, err)
	}
	return db, nil
}

// OpenWritable opens a writable *sql.DB for diagnostic commands
// (used by `db restore --verify` to insert a probe row). Like
// OpenReadOnly, this lives inside storage so the api/app/domain gate is
// never violated.
func OpenWritable(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open writable %s: %w", path, err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping writable %s: %w", path, err)
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable WAL on %s: %w", path, err)
	}
	return db, nil
}

// ensure time is referenced (test helper, may be used by future callers)
var _ = time.Now
